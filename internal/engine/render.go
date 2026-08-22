/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package engine implements the replication core (tasks 6.x, design D3, D7,
// D8, D9): fanning out source objects to permitted targets through a
// pluggable Transport, detecting drift via per-cluster metadata-only
// informers, handling conflicts with pre-existing objects, and garbage
// collecting replicas from the Replication inventory so none are ever
// orphaned.
//
// The pipeline is kind-agnostic: everything flows through unstructured
// objects, so enabling a new GVK is purely an allowlist (Options.KindGVKs)
// change. The engine never mutates source objects; its only finalizer lives
// on the Replication object itself (FinalizerName), guarding
// inventory-backed cleanup.
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// Labels and annotations the engine writes on every replica. They are the
// identity contract of the system: the managed-by label plus the source-ref
// labels identify a replica's owner, and the source-hash annotation is the
// drift signal compared by the metadata-only watches (design D3).
const (
	// LabelManagedBy marks every object the engine writes.
	LabelManagedBy = "app.kubernetes.io/managed-by"
	// ManagedByValue is the LabelManagedBy value identifying k8s-r8r.
	ManagedByValue = "k8s-r8r"
	// LabelSourceCluster names the cluster the source object lives on
	// (the hub; configurable via Options.HubName, default "hub").
	LabelSourceCluster = "r8r.io/source-cluster"
	// LabelSourceNamespace is the source object's namespace on the hub.
	LabelSourceNamespace = "r8r.io/source-namespace"
	// LabelSourceName is the source object's name on the hub. Note that
	// label values are capped at 63 characters; a longer source name
	// surfaces as an apply error on the target rather than silent
	// truncation.
	LabelSourceName = "r8r.io/source-name"
	// LabelSourceKind is the source object's kind.
	LabelSourceKind = "r8r.io/source-kind"
	// LabelSourceUID pins the replica to a concrete source incarnation, so
	// a delete-and-recreate of the source under the same name is never
	// confused with the original.
	LabelSourceUID = "r8r.io/source-uid"
	// AnnotationSourceHash carries "sha256:<hex>" of the canonical source
	// payload. Metadata watches compare it against the desired hash to
	// detect drift without caching replica payloads on the hub.
	AnnotationSourceHash = "r8r.io/source-hash"

	// FinalizerName is the engine's finalizer on Replication objects: a
	// Replication is only released once every inventoried replica has been
	// cleaned from reachable clusters (or released via ClusterGone).
	FinalizerName = "r8r.io/engine-finalizer"

	// annotationLastApplied is kubectl's bookkeeping annotation; it embeds
	// a full copy of the object and is stripped from replicas and hashes.
	annotationLastApplied = "kubectl.kubernetes.io/last-applied-configuration"

	// r8rKeyPrefix prefixes every label/annotation key the pipeline itself
	// owns. Such keys are stripped from replicas before the engine's own
	// keys are re-added, and excluded from canonical hashing, so request
	// annotations (r8r.io/replicate, ...) never propagate to spokes.
	r8rKeyPrefix = "r8r.io/"
)

// serverManagedMetadataFields are the metadata fields stripped from replicas:
// server-managed and identity fields that must never be copied across
// clusters (replication-engine spec, "Kind-agnostic pipeline").
var serverManagedMetadataFields = []string{
	"resourceVersion",
	"uid",
	"generation",
	"creationTimestamp",
	"deletionTimestamp",
	"deletionGracePeriodSeconds",
	"managedFields",
	"ownerReferences",
	"selfLink",
	"finalizers",
}

// Renderer turns a hub source object into the replica payload written to
// targets. It is pure: no API calls, deterministic output.
type Renderer struct {
	// HubName is the value written to LabelSourceCluster. Empty means
	// "hub".
	HubName string
}

// Render produces the replica for one target slot: a deep copy of the source
// with server-managed fields stripped, engine identity labels applied, and
// the source-hash annotation set. targetName overrides the replica name when
// non-empty (the explicit r8r.io/target-name mechanism; automatic renaming
// never occurs, design D7). The returned hash is SourceHash(src).
func (r Renderer) Render(src *unstructured.Unstructured, targetNamespace, targetName string) (*unstructured.Unstructured, string) {
	hash := SourceHash(src)

	rep := src.DeepCopy()
	stripServerManaged(rep)
	rep.SetNamespace(targetNamespace)
	if targetName == "" {
		targetName = src.GetName()
	}
	rep.SetName(targetName)

	labels := cleanPipelineKeys(rep.GetLabels())
	if labels == nil {
		labels = map[string]string{}
	}
	labels[LabelManagedBy] = ManagedByValue
	labels[LabelSourceCluster] = r.hubName()
	labels[LabelSourceNamespace] = src.GetNamespace()
	labels[LabelSourceName] = src.GetName()
	labels[LabelSourceKind] = src.GetKind()
	labels[LabelSourceUID] = string(src.GetUID())
	rep.SetLabels(labels)

	ann := cleanPipelineKeys(rep.GetAnnotations())
	if ann == nil {
		ann = map[string]string{}
	}
	ann[AnnotationSourceHash] = hash
	rep.SetAnnotations(ann)

	return rep, hash
}

// AdoptPatch builds the metadata-only server-side-apply payload used for the
// Adopt conflict policy: it adds the engine's identity labels and the
// source-hash annotation without touching the existing object's payload
// (replication-engine spec, "Adopt on identical content").
func (r Renderer) AdoptPatch(src *unstructured.Unstructured, gvk schema.GroupVersionKind, namespace, name, hash string) *unstructured.Unstructured {
	patch := &unstructured.Unstructured{}
	patch.SetGroupVersionKind(gvk)
	patch.SetNamespace(namespace)
	patch.SetName(name)
	patch.SetLabels(map[string]string{
		LabelManagedBy:       ManagedByValue,
		LabelSourceCluster:   r.hubName(),
		LabelSourceNamespace: src.GetNamespace(),
		LabelSourceName:      src.GetName(),
		LabelSourceKind:      src.GetKind(),
		LabelSourceUID:       string(src.GetUID()),
	})
	patch.SetAnnotations(map[string]string{AnnotationSourceHash: hash})
	return patch
}

func (r Renderer) hubName() string {
	if r.HubName == "" {
		return "hub"
	}
	return r.HubName
}

// SourceHash computes the canonical content hash of an object, formatted
// "sha256:<hex>". The hash covers the object's payload (apiVersion, kind,
// data fields, user labels/annotations) but excludes:
//
//   - status and all server-managed metadata (they differ per cluster),
//   - metadata identity (name, namespace) so an explicitly renamed replica
//     still hashes equal to its source and Adopt comparisons work,
//   - every key the pipeline itself writes (r8r.io/*, the managed-by label,
//     kubectl's last-applied annotation), so source and replica hash
//     identically.
//
// Determinism: the canonical form is Go's JSON encoding of the reduced
// object, which sorts map keys, so equal content always yields equal hashes.
func SourceHash(obj *unstructured.Unstructured) string {
	payload := canonicalPayload(obj)
	b, err := json.Marshal(payload)
	if err != nil {
		// unstructured content is JSON-derived; Marshal cannot fail on
		// it. Guard anyway with a poisoned value that never matches.
		return "sha256:unhashable:" + err.Error()
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// canonicalPayload reduces an object to the content the hash covers (see
// SourceHash).
func canonicalPayload(obj *unstructured.Unstructured) map[string]interface{} {
	content := runtime.DeepCopyJSON(obj.Object)
	delete(content, "status")
	reduced := map[string]interface{}{}
	if md, ok := content["metadata"].(map[string]interface{}); ok {
		if l := cleanRawKeys(md["labels"]); len(l) > 0 {
			reduced["labels"] = l
		}
		if a := cleanRawKeys(md["annotations"]); len(a) > 0 {
			reduced["annotations"] = a
		}
	}
	if len(reduced) > 0 {
		content["metadata"] = reduced
	} else {
		delete(content, "metadata")
	}
	return content
}

// stripServerManaged removes status and the server-managed metadata fields
// in place.
func stripServerManaged(obj *unstructured.Unstructured) {
	delete(obj.Object, "status")
	md, ok := obj.Object["metadata"].(map[string]interface{})
	if !ok {
		return
	}
	for _, f := range serverManagedMetadataFields {
		delete(md, f)
	}
}

// isPipelineKey reports whether a label/annotation key is written by the
// replication pipeline itself and must therefore be stripped from replicas
// (before re-adding current values) and excluded from hashing.
func isPipelineKey(k string) bool {
	return strings.HasPrefix(k, r8rKeyPrefix) || k == LabelManagedBy || k == annotationLastApplied
}

// cleanPipelineKeys returns a copy of m without pipeline-owned keys; nil in,
// nil out.
func cleanPipelineKeys(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if !isPipelineKey(k) {
			out[k] = v
		}
	}
	return out
}

// cleanRawKeys is cleanPipelineKeys over the raw unstructured representation
// of a string map.
func cleanRawKeys(raw interface{}) map[string]interface{} {
	m, ok := raw.(map[string]interface{})
	if !ok {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		if !isPipelineKey(k) {
			out[k] = v
		}
	}
	return out
}

// namespacePayload builds the Namespace object applied when namespace
// creation is permitted: name plus the managed-by label, nothing else.
// Namespaces created this way are never deleted by the engine (spec:
// "Namespace ensuring").
func namespacePayload(name string) *unstructured.Unstructured {
	ns := &unstructured.Unstructured{}
	ns.SetGroupVersionKind(namespaceGVK)
	ns.SetName(name)
	ns.SetLabels(map[string]string{LabelManagedBy: ManagedByValue})
	return ns
}

var namespaceGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"}

// IsManagedReplica reports whether obj carries the engine's ownership marks
// for the given source UID: the managed-by label and a matching source-uid
// label.
func IsManagedReplica(labels map[string]string, sourceUID types.UID) bool {
	return labels[LabelManagedBy] == ManagedByValue && labels[LabelSourceUID] == string(sourceUID)
}

// replicaRef renders a loggable identity for a replica; it never contains
// payload data.
func replicaRef(cluster, namespace, name string) string {
	return fmt.Sprintf("%s/%s/%s", cluster, namespace, name)
}
