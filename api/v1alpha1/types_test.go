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
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// TestGroupVersion pins the API group to exactly "r8r.io" (design D10). A
// group rename after release is breaking, so this must never drift.
func TestGroupVersion(t *testing.T) {
	if GroupVersion.Group != "r8r.io" {
		t.Fatalf("API group must be exactly %q, got %q", "r8r.io", GroupVersion.Group)
	}
	if GroupVersion.Version != "v1alpha1" {
		t.Fatalf("API version must be %q, got %q", "v1alpha1", GroupVersion.Version)
	}
}

// TestReplicationDeepCopyRoundTrip verifies the generated deepcopy funcs
// produce an equal but fully independent copy of a Replication with every
// spec/status field populated.
func TestReplicationDeepCopyRoundTrip(t *testing.T) {
	orig := &Replication{
		ObjectMeta: metav1.ObjectMeta{Name: "db-creds", Namespace: "team-a"},
		Spec: ReplicationSpec{
			SourceRef: SourceReference{
				Kind:      "Secret",
				Namespace: "team-a",
				Name:      "db-creds",
				UID:       types.UID("11111111-2222-3333-4444-555555555555"),
			},
			Origin: ReplicationOriginAnnotation,
			ResolvedTargets: []ResolvedTarget{
				{ClusterName: "spoke-a", Namespaces: []string{"team-a", "team-b"}, TargetName: "db-creds-copy"},
			},
		},
		Status: ReplicationStatus{
			Summary:    TargetSummary{DesiredTargets: 2, ReadyTargets: 1, FailedTargets: 1},
			SourceHash: "sha256:deadbeef",
			Conditions: []metav1.Condition{{
				Type:               ReplicationConditionReady,
				Status:             metav1.ConditionFalse,
				Reason:             ReasonConflict,
				Message:            "1 target in conflict",
				LastTransitionTime: metav1.Now(),
			}},
			NonReadyTargets: []NonReadyTarget{{
				ClusterName: "spoke-a", Namespace: "team-b", Name: "db-creds-copy",
				Reason: ReasonConflict, Message: "unmanaged object exists",
				LastTransitionTime: metav1.Now(),
			}},
			NonReadyOverflow: 3,
			Inventory: []InventoryEntry{{
				ClusterName: "spoke-a", Namespace: "team-a", Name: "db-creds-copy",
				Kind: "Secret", LastAppliedHash: "sha256:deadbeef",
			}},
		},
	}

	cp := orig.DeepCopy()
	if !reflect.DeepEqual(orig, cp) {
		t.Fatalf("deepcopy is not equal to original:\norig: %+v\ncopy: %+v", orig, cp)
	}

	// Mutating the copy must not touch the original (independent slices).
	cp.Spec.ResolvedTargets[0].Namespaces[0] = "mutated"
	cp.Status.Inventory[0].LastAppliedHash = "sha256:mutated"
	if orig.Spec.ResolvedTargets[0].Namespaces[0] == "mutated" {
		t.Fatal("deepcopy shares ResolvedTargets.Namespaces slice with original")
	}
	if orig.Status.Inventory[0].LastAppliedHash == "sha256:mutated" {
		t.Fatal("deepcopy shares Inventory slice with original")
	}

	// DeepCopyObject must return the runtime.Object interface for scheme use.
	if obj := orig.DeepCopyObject(); obj == nil {
		t.Fatal("DeepCopyObject returned nil")
	}
}

// TestReplicationPolicyDeepCopyRoundTrip does the same independence check for
// ReplicationPolicy, including the pointer-typed NamespaceSelector.
func TestReplicationPolicyDeepCopyRoundTrip(t *testing.T) {
	orig := &ReplicationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "allow-team-a"},
		Spec: ReplicationPolicySpec{
			Sources: PolicySources{
				Namespaces: []string{"team-a"},
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"team": "a"},
				},
				Kinds: []string{"Secret", "ConfigMap"},
			},
			Targets: PolicyTargets{
				ClusterSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "prod"},
				},
				Namespaces: []string{"team-a"},
			},
			Options: PolicyOptions{
				AllowNamespaceCreation:  true,
				AllowedConflictPolicies: []ConflictPolicy{ConflictPolicyFail, ConflictPolicyAdopt},
				RevocationPolicy:        RevocationPolicyRetain,
			},
		},
	}

	cp := orig.DeepCopy()
	if !reflect.DeepEqual(orig, cp) {
		t.Fatalf("deepcopy is not equal to original:\norig: %+v\ncopy: %+v", orig, cp)
	}

	cp.Spec.Sources.NamespaceSelector.MatchLabels["team"] = "mutated"
	if orig.Spec.Sources.NamespaceSelector.MatchLabels["team"] == "mutated" {
		t.Fatal("deepcopy shares NamespaceSelector with original")
	}
	cp.Spec.Options.AllowedConflictPolicies[0] = ConflictPolicyOverwrite
	if orig.Spec.Options.AllowedConflictPolicies[0] == ConflictPolicyOverwrite {
		t.Fatal("deepcopy shares AllowedConflictPolicies slice with original")
	}
}

// TestEnumValues pins the string values of the API enums. These land in
// stored objects and in CRD validation, so changing them is a breaking change.
func TestEnumValues(t *testing.T) {
	cases := []struct {
		got, want string
	}{
		{string(ReplicationOriginAnnotation), "Annotation"},
		{string(ConflictPolicyFail), "Fail"},
		{string(ConflictPolicyOverwrite), "Overwrite"},
		{string(ConflictPolicyAdopt), "Adopt"},
		{string(RevocationPolicyRetain), "Retain"},
		{string(RevocationPolicyDelete), "Delete"},
		{ReplicationConditionReady, "Ready"},
		{ReasonPolicyDenied, "PolicyDenied"},
		{ReasonPolicyRevoked, "PolicyRevoked"},
		{ReasonNotAuthoritative, "NotAuthoritative"},
		{ReasonConflict, "Conflict"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("enum value drifted: got %q, want %q", c.got, c.want)
		}
	}
}

// TestPolicyDefaultingMarkersGenerated asserts the generated ReplicationPolicy
// CRD actually carries the defaults declared by the kubebuilder markers
// (allowNamespaceCreation=false, allowedConflictPolicies=[Fail],
// revocationPolicy=Delete). Guards against markers being dropped without
// regenerating manifests.
func TestPolicyDefaultingMarkersGenerated(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "r8r.io_replicationpolicies.yaml"))
	if err != nil {
		t.Fatalf("generated CRD missing (run `make manifests`): %v", err)
	}
	crd := string(data)
	for _, want := range []string{
		"scope: Cluster",
		"group: r8r.io",
		"default: false",  // options.allowNamespaceCreation
		"default: Delete", // options.revocationPolicy
		"- Fail",          // conflict policy enum + default member
	} {
		if !strings.Contains(crd, want) {
			t.Errorf("generated ReplicationPolicy CRD lacks %q", want)
		}
	}
}
