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

package engine

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// testSecret builds an unstructured Secret carrying every server-managed
// field the pipeline must strip.
func testSecret() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":              "web-creds",
			"namespace":         "app",
			"uid":               "src-uid-1",
			"resourceVersion":   "12345",
			"generation":        int64(3),
			"creationTimestamp": "2026-01-01T00:00:00Z",
			"selfLink":          "/api/v1/namespaces/app/secrets/web-creds",
			"finalizers":        []any{"r8r.io/finalizer"},
			"managedFields":     []any{map[string]any{"manager": "kubectl"}},
			"ownerReferences":   []any{map[string]any{"name": "owner"}},
			"labels": map[string]any{
				"team":            "web",
				"r8r.io/internal": "x",
			},
			"annotations": map[string]any{
				"r8r.io/replicate":                                 "true",
				"r8r.io/target-clusters":                           "env=prod",
				"kubectl.kubernetes.io/last-applied-configuration": "{...}",
				"note": "keep-me",
			},
		},
		"type":   "Opaque",
		"data":   map[string]any{"password": "c2VjcmV0"},
		"status": map[string]any{"phase": "irrelevant"},
	}}
}

// Spec "Replica payload excludes server-managed fields": the rendered replica
// carries payload and k8s-r8r metadata but no server-managed fields.
func TestRender_StripsServerManagedFields(t *testing.T) {
	src := testSecret()
	rep, _ := Renderer{}.Render(src, "target-ns", "")

	md := rep.Object["metadata"].(map[string]any)
	for _, f := range serverManagedMetadataFields {
		if _, present := md[f]; present {
			t.Errorf("replica metadata still contains server-managed field %q", f)
		}
	}
	if _, present := rep.Object["status"]; present {
		t.Error("replica still contains status")
	}
	if rep.GetNamespace() != "target-ns" {
		t.Errorf("replica namespace = %q, want target-ns", rep.GetNamespace())
	}
	if rep.GetName() != "web-creds" {
		t.Errorf("replica name = %q, want source name", rep.GetName())
	}
	if got := rep.Object["data"].(map[string]any)["password"]; got != "c2VjcmV0" {
		t.Errorf("replica payload changed: data.password = %v", got)
	}
}

// Spec "Replica creation and identity": managed-by + source-ref labels and
// the source-hash annotation; request annotations must not propagate.
func TestRender_IdentityLabelsAndAnnotations(t *testing.T) {
	src := testSecret()
	rep, hash := Renderer{HubName: "central"}.Render(src, "target-ns", "renamed")

	labels := rep.GetLabels()
	want := map[string]string{
		LabelManagedBy:       ManagedByValue,
		LabelSourceCluster:   "central",
		LabelSourceNamespace: "app",
		LabelSourceName:      "web-creds",
		LabelSourceKind:      "Secret",
		LabelSourceUID:       "src-uid-1",
		"team":               "web",
	}
	for k, v := range want {
		if labels[k] != v {
			t.Errorf("label %q = %q, want %q", k, labels[k], v)
		}
	}
	if _, leaked := labels["r8r.io/internal"]; leaked {
		t.Error("pipeline-owned source label leaked into replica")
	}

	ann := rep.GetAnnotations()
	if ann[AnnotationSourceHash] != hash {
		t.Errorf("source-hash annotation = %q, want %q", ann[AnnotationSourceHash], hash)
	}
	if !strings.HasPrefix(hash, "sha256:") {
		t.Errorf("hash %q lacks sha256: prefix", hash)
	}
	for _, k := range []string{"r8r.io/replicate", "r8r.io/target-clusters", annotationLastApplied} {
		if _, leaked := ann[k]; leaked {
			t.Errorf("request/bookkeeping annotation %q leaked into replica", k)
		}
	}
	if ann["note"] != "keep-me" {
		t.Error("user annotation was lost")
	}
	if rep.GetName() != "renamed" {
		t.Errorf("explicit target name not honored: %q", rep.GetName())
	}
}

// Hash determinism: identical content hashes identically; payload changes
// change the hash.
func TestSourceHash_Deterministic(t *testing.T) {
	a, b := testSecret(), testSecret()
	if SourceHash(a) != SourceHash(b) {
		t.Fatal("identical objects produced different hashes")
	}
	for range 50 {
		if SourceHash(a) != SourceHash(b) {
			t.Fatal("hash not deterministic across invocations")
		}
	}
	b.Object["data"].(map[string]any)["password"] = "b3RoZXI="
	if SourceHash(a) == SourceHash(b) {
		t.Fatal("payload change did not change the hash")
	}
}

// The hash must ignore identity and pipeline-owned keys: a rendered replica
// (renamed, relabeled, moved) hashes equal to its source, which is what makes
// Adopt comparisons and drift detection work.
func TestSourceHash_IgnoresIdentityAndPipelineKeys(t *testing.T) {
	src := testSecret()
	srcHash := SourceHash(src)

	rep, renderedHash := Renderer{}.Render(src, "elsewhere", "other-name")
	if renderedHash != srcHash {
		t.Fatalf("rendered hash %s != source hash %s", renderedHash, srcHash)
	}
	if SourceHash(rep) != srcHash {
		t.Fatalf("replica re-hash %s != source hash %s (identity/pipeline keys leaked into hash)", SourceHash(rep), srcHash)
	}

	// Server-managed field churn must not affect the hash either.
	src2 := testSecret()
	src2.SetResourceVersion("99999")
	if SourceHash(src2) != srcHash {
		t.Fatal("resourceVersion affected the hash")
	}
}

// withMetadata returns a source Secret carrying key=value as both a label and
// an annotation, so one table entry covers both maps.
func withMetadata(key, value string) *unstructured.Unstructured {
	src := testSecret()
	md := src.Object["metadata"].(map[string]any)
	md["labels"].(map[string]any)[key] = value
	md["annotations"].(map[string]any)[key] = value
	return src
}

// Spec "Foreign ownership metadata is not replicated": metadata asserting
// another controller's ownership or replication intent never reaches a
// replica. The mittwald case is the severe one — such a replica is a valid
// source for that controller, so k8s-r8r would seed a second fanout whose
// destinations no ReplicationPolicy evaluated.
func TestRender_StripsForeignOwnershipMetadata(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"mittwald replicate-to-clusters", "replicator.v1.mittwald.de/replicate-to-clusters", ".*"},
		{"mittwald target-namespace", "replicator.v1.mittwald.de/target-namespace", "kube-system"},
		{"emberstack reflector", "reflector.v1.k8s.emberstack.com/reflection-allowed", "true"},
		{"argocd tracking-id", "argocd.argoproj.io/tracking-id", "some-app:/Secret:app/web-creds"},
		{"argocd sync-options", "argocd.argoproj.io/sync-options", "Prune=false"},
		{"argocd instance label", "app.kubernetes.io/instance", "some-app"},
		{"helm release ownership", "meta.helm.sh/release-name", "platform"},
		{"flux kustomize ownership", "kustomize.toolkit.fluxcd.io/name", "apps"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := withMetadata(tc.key, tc.value)
			rep, hash := Renderer{}.Render(src, "target-ns", "")

			if _, leaked := rep.GetLabels()[tc.key]; leaked {
				t.Errorf("foreign ownership label %q leaked into replica", tc.key)
			}
			if _, leaked := rep.GetAnnotations()[tc.key]; leaked {
				t.Errorf("foreign ownership annotation %q leaked into replica", tc.key)
			}

			// The stripped key must also be invisible to the hash, or the
			// replica never converges (see below).
			if hash != SourceHash(testSecret()) {
				t.Errorf("stripped key %q changed the source hash", tc.key)
			}
		})
	}
}

// Regression guard for the drift hot loop: stripping a key from the replica
// while still hashing it on the source would make SourceHash(replica) differ
// from the desired hash forever, so the engine would re-apply on every
// reconcile, each apply would emit a metadata event, and the drift handler
// would enqueue again — against every spoke.
func TestSourceHash_StableAcrossForeignMetadataStripping(t *testing.T) {
	src := testSecret()
	md := src.Object["metadata"].(map[string]any)
	md["labels"].(map[string]any)["app.kubernetes.io/instance"] = "some-app"
	md["annotations"].(map[string]any)["replicator.v1.mittwald.de/replicate-to-clusters"] = ".*"
	md["annotations"].(map[string]any)["argocd.argoproj.io/tracking-id"] = "some-app:/Secret:app/web-creds"

	srcHash := SourceHash(src)
	rep, renderedHash := Renderer{}.Render(src, "target-ns", "")
	if renderedHash != srcHash {
		t.Fatalf("rendered hash %s != source hash %s", renderedHash, srcHash)
	}
	if got := SourceHash(rep); got != srcHash {
		t.Fatalf("replica re-hash %s != source hash %s: stripped keys still counted in the hash, "+
			"which would make the engine re-apply on every reconcile", got, srcHash)
	}
}

// The denylist must stay a denylist: functionally significant source metadata
// has to keep propagating. A replicated sealed-secrets key without its
// sealed-secrets-key label is inert on arrival.
func TestRender_PropagatesFunctionalMetadata(t *testing.T) {
	src := withMetadata("sealedsecrets.bitnami.com/sealed-secrets-key", "active")
	src.Object["metadata"].(map[string]any)["labels"].(map[string]any)["app.kubernetes.io/name"] = "web"

	rep, _ := Renderer{}.Render(src, "target-ns", "")
	labels, ann := rep.GetLabels(), rep.GetAnnotations()

	if labels["sealedsecrets.bitnami.com/sealed-secrets-key"] != "active" {
		t.Error("sealed-secrets key label was stripped; the denylist must not become an allowlist")
	}
	if ann["sealedsecrets.bitnami.com/sealed-secrets-key"] != "active" {
		t.Error("sealed-secrets annotation was stripped")
	}
	// app.kubernetes.io/instance is denied, but the rest of the recommended
	// label set is not.
	if labels["app.kubernetes.io/name"] != "web" {
		t.Error("app.kubernetes.io/name was stripped; only the instance label is an ownership claim")
	}
	if labels["team"] != "web" || ann["note"] != "keep-me" {
		t.Error("unrelated user metadata was lost")
	}
}

// Spec scenario "Operator-configured additional keys": --strip-metadata-keys
// extends the denylist for both the replica and the hash.
func TestSetExtraStrippedKeys(t *testing.T) {
	t.Cleanup(func() { SetExtraStrippedKeys(nil) })
	SetExtraStrippedKeys([]string{"example.com/owner", "  ", "vendor.example/", "/"})

	baseline := SourceHash(testSecret())

	for _, key := range []string{"example.com/owner", "vendor.example/anything"} {
		src := withMetadata(key, "x")
		rep, hash := Renderer{}.Render(src, "target-ns", "")
		if _, leaked := rep.GetLabels()[key]; leaked {
			t.Errorf("configured key %q not stripped from replica labels", key)
		}
		if _, leaked := rep.GetAnnotations()[key]; leaked {
			t.Errorf("configured key %q not stripped from replica annotations", key)
		}
		if hash != baseline {
			t.Errorf("configured key %q changed the source hash", key)
		}
	}

	// A near miss must not be stripped: "example.com/owner" is an exact key,
	// not a prefix.
	src := withMetadata("example.com/owner-extra", "x")
	rep, _ := Renderer{}.Render(src, "target-ns", "")
	if rep.GetLabels()["example.com/owner-extra"] != "x" {
		t.Error("exact-key entry matched as a prefix")
	}

	// Clearing the configuration restores the built-in denylist only.
	SetExtraStrippedKeys(nil)
	mixed := withMetadata("example.com/owner", "x")
	mixed.Object["metadata"].(map[string]any)["labels"].(map[string]any)["app.kubernetes.io/instance"] = "some-app"
	rep, _ = Renderer{}.Render(mixed, "target-ns", "")
	if rep.GetLabels()["example.com/owner"] != "x" {
		t.Error("configured keys were not cleared")
	}
	if _, leaked := rep.GetLabels()["app.kubernetes.io/instance"]; leaked {
		t.Error("built-in denylist entry disappeared with the configured ones")
	}
}

func TestNamespacePayload(t *testing.T) {
	ns := namespacePayload("fresh")
	if ns.GetName() != "fresh" {
		t.Errorf("namespace name = %q", ns.GetName())
	}
	if ns.GetLabels()[LabelManagedBy] != ManagedByValue {
		t.Error("created namespace lacks managed-by label")
	}
	if ns.GroupVersionKind() != namespaceGVK {
		t.Errorf("namespace GVK = %v", ns.GroupVersionKind())
	}
}
