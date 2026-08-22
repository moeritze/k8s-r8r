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
)

// ConflictPolicy describes how the engine treats a pre-existing, unmanaged
// object occupying an intended replica's name on a target (design D7).
// +kubebuilder:validation:Enum=Fail;Overwrite;Adopt
type ConflictPolicy string

const (
	// ConflictPolicyFail leaves the existing object untouched and reports a
	// Conflict condition for that target. This is the default and the only
	// mode permitted unless a policy explicitly allows more.
	ConflictPolicyFail ConflictPolicy = "Fail"

	// ConflictPolicyOverwrite takes over the existing object, replacing its
	// payload. Weaponizable (it can replace a victim cluster's secret), so it
	// requires both the request to ask for it and a policy to permit it.
	ConflictPolicyOverwrite ConflictPolicy = "Overwrite"

	// ConflictPolicyAdopt takes ownership of the existing object without
	// rewriting it, and only when its content hash equals the source hash.
	ConflictPolicyAdopt ConflictPolicy = "Adopt"
)

// RevocationPolicy controls what happens to existing replicas when permission
// for them is withdrawn (policy edit, policy deletion, annotation removal
// under this policy's scope).
// +kubebuilder:validation:Enum=Retain;Delete
type RevocationPolicy string

const (
	// RevocationPolicyRetain leaves existing replicas in place but stops
	// updating them; the Replication is marked with a PolicyRevoked condition.
	RevocationPolicyRetain RevocationPolicy = "Retain"

	// RevocationPolicyDelete removes existing replicas on the next reconcile
	// after permission is withdrawn. This is the default: revoked data should
	// not linger on the fleet.
	RevocationPolicyDelete RevocationPolicy = "Delete"
)

// PolicySources is the source-side allowlist of a policy: which hub objects
// may request replication under it. A request's source must match on both the
// namespace dimension (exact list and/or selector — either match suffices) and
// the kind dimension.
type PolicySources struct {
	// Namespaces allowlists source namespaces by exact name. A source
	// namespace matches the namespace dimension if it appears here or matches
	// NamespaceSelector.
	// +optional
	Namespaces []string `json:"namespaces,omitempty"`

	// NamespaceSelector allowlists source namespaces by their labels. Nil
	// means no selector-based matching (only the exact Namespaces list
	// applies); an empty selector matches every namespace.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// Kinds allowlists source kinds (e.g. "Secret", "ConfigMap"). A request's
	// kind must appear here; kinds outside the operator's global allowlist are
	// never replicated regardless of policy.
	// +kubebuilder:validation:MinItems=1
	Kinds []string `json:"kinds"`
}

// PolicyTargets is the target-side allowlist of a policy: where permitted
// sources may be replicated to. Both dimensions (clusters, namespaces) must be
// satisfied for a target to be permitted by this policy.
type PolicyTargets struct {
	// ClusterSelector allowlists target clusters by labels on discovered
	// cluster inventory. An empty selector matches all discovered clusters.
	ClusterSelector metav1.LabelSelector `json:"clusterSelector"`

	// Namespaces allowlists target namespaces by exact name on the selected
	// clusters.
	// +kubebuilder:validation:MinItems=1
	Namespaces []string `json:"namespaces"`
}

// PolicyOptions gates the engine side effects a policy is willing to permit
// for the requests it allows. Defaults are the safest choices: no namespace
// creation, conflicts fail, revoked replicas are deleted.
type PolicyOptions struct {
	// AllowNamespaceCreation permits the engine to create missing target
	// namespaces (labeled `app.kubernetes.io/managed-by: k8s-r8r`). Defaults
	// to false: replication into a nonexistent namespace fails with a
	// condition instead of silently creating cluster-level structure.
	// +optional
	// +kubebuilder:default=false
	AllowNamespaceCreation bool `json:"allowNamespaceCreation,omitempty"`

	// AllowedConflictPolicies lists the conflict policies requests under this
	// policy may use. Defaults to [Fail]: Overwrite and Adopt must be
	// explicitly granted because they touch objects k8s-r8r does not manage.
	// +optional
	// +kubebuilder:default={Fail}
	AllowedConflictPolicies []ConflictPolicy `json:"allowedConflictPolicies,omitempty"`

	// RevocationPolicy controls what happens to already-created replicas when
	// this policy stops permitting them. Defaults to Delete.
	// +optional
	// +kubebuilder:default=Delete
	RevocationPolicy RevocationPolicy `json:"revocationPolicy,omitempty"`
}

// ReplicationPolicySpec is one allowlist entry in the default-deny policy
// universe (design D4). A request (or an individual target of it) is permitted
// only if a single policy matches it on every dimension; policies union — they
// never combine dimensions across each other and contain no deny rules.
type ReplicationPolicySpec struct {
	// Sources allowlists which hub objects may request replication.
	Sources PolicySources `json:"sources"`

	// Targets allowlists which clusters and namespaces those sources may
	// replicate into.
	Targets PolicyTargets `json:"targets"`

	// Options gates engine side effects for requests this policy permits.
	// Omitted options take the safe defaults (no namespace creation, conflicts
	// Fail, revocation Delete).
	// +optional
	// +kubebuilder:default={}
	Options PolicyOptions `json:"options,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster

// ReplicationPolicy is the admin-controlled security boundary: a cluster-scoped
// allowlist that permits replication requests. With no policies present nothing
// replicates (default deny); multiple policies combine by union. Policy is
// re-evaluated authoritatively on every reconcile — admission-time checks are
// advisory only (design D4/D6). Project RBAC restricts writes to cluster
// administrators.
type ReplicationPolicy struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the allowlists and options of this policy.
	// +optional
	Spec ReplicationPolicySpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// ReplicationPolicyList contains a list of ReplicationPolicy objects.
type ReplicationPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// Items is the list of ReplicationPolicy objects.
	Items []ReplicationPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ReplicationPolicy{}, &ReplicationPolicyList{})
}
