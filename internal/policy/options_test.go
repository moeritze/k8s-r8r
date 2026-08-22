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

package policy

import (
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	r8rv1alpha1 "github.com/moritzroeseler/k8s-r8r/api/v1alpha1"
)

func policyWithOptions(name string, opts r8rv1alpha1.PolicyOptions) r8rv1alpha1.ReplicationPolicy {
	p := makePolicy(name, specAllowingWestTeamA())
	p.Spec.Options = opts
	return p
}

func TestResolveOptions(t *testing.T) {
	tests := []struct {
		name    string
		matched []r8rv1alpha1.ReplicationPolicy
		want    EffectiveOptions
	}{
		{
			// Spec: "Policy options gate side effects" / Scenario: "Namespace
			// creation denied by default": no matching policy sets
			// allowNamespaceCreation, so it stays false; conflict policies
			// default to [Fail]; revocation defaults to Delete.
			name: "defaults_no_namespace_creation",
			matched: []r8rv1alpha1.ReplicationPolicy{
				policyWithOptions("plain", r8rv1alpha1.PolicyOptions{}),
			},
			want: EffectiveOptions{
				AllowNamespaceCreation:  false,
				AllowedConflictPolicies: []r8rv1alpha1.ConflictPolicy{r8rv1alpha1.ConflictPolicyFail},
				RevocationPolicy:        r8rv1alpha1.RevocationPolicyDelete,
			},
		},
		{
			// Empty matched set: safe defaults (target is denied anyway).
			name:    "empty_matched_safe_defaults",
			matched: nil,
			want: EffectiveOptions{
				AllowNamespaceCreation:  false,
				AllowedConflictPolicies: []r8rv1alpha1.ConflictPolicy{r8rv1alpha1.ConflictPolicyFail},
				RevocationPolicy:        r8rv1alpha1.RevocationPolicyDelete,
			},
		},
		{
			// allowNamespaceCreation is OR across matching policies.
			name: "namespace_creation_or",
			matched: []r8rv1alpha1.ReplicationPolicy{
				policyWithOptions("strict", r8rv1alpha1.PolicyOptions{AllowNamespaceCreation: false}),
				policyWithOptions("permissive", r8rv1alpha1.PolicyOptions{AllowNamespaceCreation: true}),
			},
			want: EffectiveOptions{
				AllowNamespaceCreation:  true,
				AllowedConflictPolicies: []r8rv1alpha1.ConflictPolicy{r8rv1alpha1.ConflictPolicyFail},
				RevocationPolicy:        r8rv1alpha1.RevocationPolicyDelete,
			},
		},
		{
			// allowedConflictPolicies is the union across matching policies,
			// deduplicated, in canonical order (Fail, Adopt, Overwrite).
			name: "conflict_policies_union",
			matched: []r8rv1alpha1.ReplicationPolicy{
				policyWithOptions("overwriter", r8rv1alpha1.PolicyOptions{
					AllowedConflictPolicies: []r8rv1alpha1.ConflictPolicy{
						r8rv1alpha1.ConflictPolicyFail,
						r8rv1alpha1.ConflictPolicyOverwrite,
					},
				}),
				policyWithOptions("adopter", r8rv1alpha1.PolicyOptions{
					AllowedConflictPolicies: []r8rv1alpha1.ConflictPolicy{
						r8rv1alpha1.ConflictPolicyAdopt,
					},
				}),
			},
			want: EffectiveOptions{
				AllowedConflictPolicies: []r8rv1alpha1.ConflictPolicy{
					r8rv1alpha1.ConflictPolicyFail,
					r8rv1alpha1.ConflictPolicyAdopt,
					r8rv1alpha1.ConflictPolicyOverwrite,
				},
				RevocationPolicy: r8rv1alpha1.RevocationPolicyDelete,
			},
		},
		{
			// Fail is always included even when a policy lists only others.
			name: "fail_always_included",
			matched: []r8rv1alpha1.ReplicationPolicy{
				policyWithOptions("only-adopt", r8rv1alpha1.PolicyOptions{
					AllowedConflictPolicies: []r8rv1alpha1.ConflictPolicy{
						r8rv1alpha1.ConflictPolicyAdopt,
					},
				}),
			},
			want: EffectiveOptions{
				AllowedConflictPolicies: []r8rv1alpha1.ConflictPolicy{
					r8rv1alpha1.ConflictPolicyFail,
					r8rv1alpha1.ConflictPolicyAdopt,
				},
				RevocationPolicy: r8rv1alpha1.RevocationPolicyDelete,
			},
		},
		{
			// Most conservative revocation wins: Retain beats Delete.
			name: "retain_beats_delete",
			matched: []r8rv1alpha1.ReplicationPolicy{
				policyWithOptions("deleter", r8rv1alpha1.PolicyOptions{
					RevocationPolicy: r8rv1alpha1.RevocationPolicyDelete,
				}),
				policyWithOptions("retainer", r8rv1alpha1.PolicyOptions{
					RevocationPolicy: r8rv1alpha1.RevocationPolicyRetain,
				}),
			},
			want: EffectiveOptions{
				AllowedConflictPolicies: []r8rv1alpha1.ConflictPolicy{r8rv1alpha1.ConflictPolicyFail},
				RevocationPolicy:        r8rv1alpha1.RevocationPolicyRetain,
			},
		},
		{
			// Unset revocation (API default Delete) cannot override Retain.
			name: "unset_revocation_does_not_override_retain",
			matched: []r8rv1alpha1.ReplicationPolicy{
				policyWithOptions("retainer", r8rv1alpha1.PolicyOptions{
					RevocationPolicy: r8rv1alpha1.RevocationPolicyRetain,
				}),
				policyWithOptions("unset", r8rv1alpha1.PolicyOptions{}),
			},
			want: EffectiveOptions{
				AllowedConflictPolicies: []r8rv1alpha1.ConflictPolicy{r8rv1alpha1.ConflictPolicyFail},
				RevocationPolicy:        r8rv1alpha1.RevocationPolicyRetain,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveOptions(tc.matched)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ResolveOptions() = %+v, want %+v", got, tc.want)
			}
			// Determinism: reversed input must produce the same result.
			rev := make([]r8rv1alpha1.ReplicationPolicy, len(tc.matched))
			for i := range tc.matched {
				rev[len(tc.matched)-1-i] = tc.matched[i]
			}
			if got2 := ResolveOptions(rev); !reflect.DeepEqual(got2, got) {
				t.Errorf("ResolveOptions depends on policy order: %+v vs %+v", got, got2)
			}
		})
	}
}

// TestDetectRevocations covers the revocation-detection primitive used by the
// revocation flow (task 3.3). Spec: "Reconcile-time enforcement is
// authoritative" / Scenario: "Policy tightened after replication" — detection
// side only; acting on it is the engine's job.
func TestDetectRevocations(t *testing.T) {
	pol := makePolicy("allow-west", specAllowingWestTeamA())
	allowAll := makePolicy("allow-all", r8rv1alpha1.ReplicationPolicySpec{
		Sources: r8rv1alpha1.PolicySources{
			Namespaces: []string{"team-a"},
			Kinds:      []string{"Secret"},
		},
		Targets: r8rv1alpha1.PolicyTargets{
			ClusterSelector: metav1.LabelSelector{},
			Namespaces:      []string{"team-a"},
		},
	})

	req := Request{Source: secretInTeamA, Targets: []Target{westProd, eastProd}}

	tests := []struct {
		name             string
		previous         Result
		current          Result
		wantClusters     []string
		wantNilCurrent   map[string]bool // cluster -> expect Current == nil
		wantCurDimension map[string]string
	}{
		{
			// Policy tightened: allow-all replaced by west-only. east-1 loses
			// permission and is reported with its current denial.
			name:         "policy_tightened_denies_target",
			previous:     Evaluate(req, []r8rv1alpha1.ReplicationPolicy{allowAll}),
			current:      Evaluate(req, []r8rv1alpha1.ReplicationPolicy{pol}),
			wantClusters: []string{"east-1"},
			wantCurDimension: map[string]string{
				"east-1": DimensionTargetCluster,
			},
		},
		{
			// All policies deleted: everything previously allowed is revoked.
			name:         "all_policies_deleted_revokes_everything",
			previous:     Evaluate(req, []r8rv1alpha1.ReplicationPolicy{allowAll}),
			current:      Evaluate(req, nil),
			wantClusters: []string{"west-1", "east-1"},
			wantCurDimension: map[string]string{
				"west-1": DimensionNoPolicies,
				"east-1": DimensionNoPolicies,
			},
		},
		{
			// Target absent from current evaluation (deselected): reported
			// with nil Current.
			name:     "target_absent_from_current",
			previous: Evaluate(req, []r8rv1alpha1.ReplicationPolicy{allowAll}),
			current: Evaluate(Request{Source: secretInTeamA, Targets: []Target{westProd}},
				[]r8rv1alpha1.ReplicationPolicy{allowAll}),
			wantClusters:   []string{"east-1"},
			wantNilCurrent: map[string]bool{"east-1": true},
		},
		{
			// Nothing changed: no revocations.
			name:         "no_change_no_revocations",
			previous:     Evaluate(req, []r8rv1alpha1.ReplicationPolicy{allowAll}),
			current:      Evaluate(req, []r8rv1alpha1.ReplicationPolicy{allowAll}),
			wantClusters: nil,
		},
		{
			// A previously denied target that stays denied is not a
			// revocation; a newly allowed target is not one either.
			name:         "previously_denied_targets_are_not_revoked",
			previous:     Evaluate(req, []r8rv1alpha1.ReplicationPolicy{pol}), // east denied
			current:      Evaluate(req, []r8rv1alpha1.ReplicationPolicy{allowAll}),
			wantClusters: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectRevocations(tc.previous, tc.current)
			var clusters []string
			for _, rev := range got {
				clusters = append(clusters, rev.Target.ClusterName)
				if !rev.Previous.Allowed {
					t.Errorf("revocation for %s carries a non-allowed Previous decision",
						rev.Target.ClusterName)
				}
				if tc.wantNilCurrent[rev.Target.ClusterName] {
					if rev.Current != nil {
						t.Errorf("revocation for %s: Current = %+v, want nil",
							rev.Target.ClusterName, rev.Current)
					}
				} else if wantDim, ok := tc.wantCurDimension[rev.Target.ClusterName]; ok {
					if rev.Current == nil {
						t.Errorf("revocation for %s: Current is nil, want dimension %s",
							rev.Target.ClusterName, wantDim)
					} else if rev.Current.DeniedDimension != wantDim {
						t.Errorf("revocation for %s: DeniedDimension = %q, want %q",
							rev.Target.ClusterName, rev.Current.DeniedDimension, wantDim)
					}
				}
			}
			if !reflect.DeepEqual(clusters, tc.wantClusters) {
				t.Errorf("revoked clusters = %v, want %v", clusters, tc.wantClusters)
			}
		})
	}
}
