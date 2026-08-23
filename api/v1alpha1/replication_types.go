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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// ReplicationOrigin identifies the kind of request that materialized a
// Replication object. The canonical layer is origin-agnostic: today only
// annotation-based requests exist, but the enum is designed to admit future
// request kinds (e.g. a selector-based ReplicationSet CRD) without any change
// to the engine, policy, or status contracts (design D1).
// +kubebuilder:validation:Enum=Annotation
type ReplicationOrigin string

const (
	// ReplicationOriginAnnotation marks a Replication that was materialized
	// from `r8r.io/*` annotations on a source object. This is the only origin
	// implemented at launch.
	ReplicationOriginAnnotation ReplicationOrigin = "Annotation"
)

// Condition types recorded on a Replication.
const (
	// ReplicationConditionReady is the aggregate readiness condition: True when
	// every desired target holds an up-to-date replica, False otherwise.
	ReplicationConditionReady = "Ready"
)

// Well-known reasons used on Replication conditions. They form the reason
// space referenced by the request, policy, and engine specs.
const (
	// ReasonPolicyDenied indicates no ReplicationPolicy permits the request
	// (default deny), so nothing was replicated.
	ReasonPolicyDenied = "PolicyDenied"

	// ReasonPolicyRevoked indicates a previously permitted request lost its
	// permission through a policy change; existing replicas were handled per
	// the effective revocationPolicy.
	ReasonPolicyRevoked = "PolicyRevoked"

	// ReasonNotAuthoritative marks a Replication object that was authored by a
	// user rather than the operator. Such objects are never reconciled.
	ReasonNotAuthoritative = "NotAuthoritative"

	// ReasonConflict indicates an intended replica name already exists on a
	// target and is not managed by k8s-r8r, and the effective conflict policy
	// did not permit taking it over.
	ReasonConflict = "Conflict"
)

// SourceReference identifies the source object a Replication fans out. The UID
// pins the reference to a concrete object incarnation so a delete-and-recreate
// of the source under the same name is never confused with the original.
type SourceReference struct {
	// Kind of the source object (e.g. "Secret", "ConfigMap"). Only kinds on
	// the operator's allowlist are ever materialized.
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`

	// Namespace of the source object on the hub cluster.
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`

	// Name of the source object on the hub cluster.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// UID of the source object. Used to detect that the referenced object is
	// still the same incarnation the request was resolved from.
	UID types.UID `json:"uid"`
}

// ResolvedTarget is one target cluster (with its namespaces) that the request
// resolved to at materialization time. Resolution happens on the hub against
// discovered cluster inventory; the engine consumes these entries verbatim.
type ResolvedTarget struct {
	// ClusterName is the discovered inventory name of the target cluster.
	// +kubebuilder:validation:MinLength=1
	ClusterName string `json:"clusterName"`

	// Namespaces are the target namespaces on this cluster that should each
	// receive a replica of the source.
	// +kubebuilder:validation:MinItems=1
	Namespaces []string `json:"namespaces"`

	// TargetName optionally overrides the replica's name on the target
	// (`r8r.io/target-name`). When empty, replicas keep the source's name.
	// Automatic renaming never occurs (design D7) — this field is the only
	// rename mechanism, and it is always explicit and git-visible.
	// +optional
	TargetName string `json:"targetName,omitempty"`
}

// ReplicationSpec describes the resolved desired state of one replication
// request. It is written exclusively by the operator (users get read-only
// access); hand-authored Replication objects are marked NotAuthoritative and
// ignored.
type ReplicationSpec struct {
	// SourceRef identifies the hub object being replicated.
	SourceRef SourceReference `json:"sourceRef"`

	// Origin records which request kind materialized this Replication.
	// Currently always "Annotation"; designed to admit future origins such as
	// "ReplicationSet".
	Origin ReplicationOrigin `json:"origin"`

	// ResolvedTargets is the full fanout the request resolved to: one entry
	// per target cluster with the namespaces to replicate into. An empty list
	// means the request currently selects no targets.
	// +optional
	ResolvedTargets []ResolvedTarget `json:"resolvedTargets,omitempty"`
}

// TargetSummary holds aggregate fanout counts. Per-target detail is only kept
// for non-ready targets (design D8: status size discipline), so these counts
// are the primary health signal at scale.
type TargetSummary struct {
	// DesiredTargets is the total number of (cluster, namespace) replica slots
	// the spec currently resolves to.
	// +optional
	DesiredTargets int32 `json:"desiredTargets"`

	// ReadyTargets is the number of replica slots whose replica exists and
	// matches the current source hash.
	// +optional
	ReadyTargets int32 `json:"readyTargets"`

	// FailedTargets is the number of replica slots in a failing state
	// (conflict, denied namespace, unreachable cluster, write error, ...).
	// +optional
	FailedTargets int32 `json:"failedTargets"`
}

// NonReadyTarget is the per-target detail entry recorded only while a target
// is not ready. The list is capped (see ReplicationStatus.NonReadyTargets);
// full per-target truth lives in metrics and events (design D8).
type NonReadyTarget struct {
	// ClusterName is the target cluster this entry describes.
	ClusterName string `json:"clusterName"`

	// Namespace is the target namespace on that cluster.
	Namespace string `json:"namespace"`

	// Name is the intended replica name in that namespace.
	Name string `json:"name"`

	// Reason is a machine-readable CamelCase reason (e.g. Conflict,
	// PolicyDenied, NamespaceMissing, ClusterUnreachable).
	Reason string `json:"reason"`

	// Message is a human-readable explanation of why the target is not ready.
	// +optional
	Message string `json:"message,omitempty"`

	// LastTransitionTime is when this target last changed readiness state.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
}

// InventoryEntry records one replica the engine has created. The inventory is
// the source of truth for garbage collection: no code path may lose track of a
// created replica (replication-engine spec).
type InventoryEntry struct {
	// ClusterName is the cluster the replica lives on.
	ClusterName string `json:"clusterName"`

	// Namespace is the replica's namespace on that cluster.
	Namespace string `json:"namespace"`

	// Name is the replica's name (source name or the explicit targetName
	// override).
	Name string `json:"name"`

	// Group is the API group of the replicated object; empty for the core
	// group (Secret, ConfigMap).
	// +optional
	Group string `json:"group,omitempty"`

	// Kind is the kind of the replicated object.
	Kind string `json:"kind"`

	// LastAppliedHash is the source content hash ("sha256:<hex>") that was
	// last written to this replica. Compared against watch events to detect
	// drift without caching replica payloads on the hub.
	// +optional
	LastAppliedHash string `json:"lastAppliedHash,omitempty"`
}

// ReplicationStatus reports the observed fanout state. Status writes are
// skipped when nothing changed, and per-target detail is bounded, to keep
// 1000-cluster fanouts well under etcd object-size limits (design D8).
type ReplicationStatus struct {
	// Summary holds the aggregate desired/ready/failed target counts.
	// +optional
	Summary TargetSummary `json:"summary,omitempty"`

	// SourceHash is the hash of the source payload the operator last observed,
	// in the form "sha256:<hex>". Replicas carrying a different
	// `r8r.io/source-hash` are considered drifted.
	// +optional
	SourceHash string `json:"sourceHash,omitempty"`

	// Conditions holds the aggregate conditions. "Ready" summarizes the whole
	// fanout; reasons include PolicyDenied, PolicyRevoked, NotAuthoritative,
	// and Conflict.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// NonReadyTargets lists detail entries for targets that are currently not
	// ready, capped at 20 entries. When the cap is exceeded, NonReadyOverflow
	// counts the entries that did not fit. Healthy targets never appear here.
	// +optional
	// +kubebuilder:validation:MaxItems=20
	NonReadyTargets []NonReadyTarget `json:"nonReadyTargets,omitempty"`

	// NonReadyOverflow is the number of non-ready targets beyond the
	// NonReadyTargets cap that are not individually listed.
	// +optional
	NonReadyOverflow int32 `json:"nonReadyOverflow,omitempty"`

	// Inventory records every replica the engine has created for this source.
	// It drives garbage collection on source deletion, annotation removal,
	// target deselection, and cluster deregistration.
	// +optional
	Inventory []InventoryEntry `json:"inventory,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Desired",type=integer,JSONPath=`.status.summary.desiredTargets`
// +kubebuilder:printcolumn:name="Failed",type=integer,JSONPath=`.status.summary.failedTargets`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Replication is the canonical, operator-owned materialization of one
// replication request (design D1). It lives in the source object's namespace,
// carries the resolved fanout in its spec, and reports status plus replica
// inventory. Users never author these directly — the annotation shim (and,
// later, other origins) creates them; project RBAC grants users read-only
// access.
type Replication struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the resolved desired replication state, written by the operator.
	// +optional
	Spec ReplicationSpec `json:"spec,omitempty"`

	// Status is the observed fanout state.
	// +optional
	Status ReplicationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ReplicationList contains a list of Replication objects.
type ReplicationList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// Items is the list of Replication objects.
	Items []Replication `json:"items"`
}

func init() {
	objectTypes = append(objectTypes, &Replication{}, &ReplicationList{})
}
