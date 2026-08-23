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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
)

// makePolicy builds a ReplicationPolicy for tests.
func makePolicy(name string, spec r8rv1alpha1.ReplicationPolicySpec) r8rv1alpha1.ReplicationPolicy {
	return r8rv1alpha1.ReplicationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       spec,
	}
}

var (
	secretInTeamA = Source{
		Kind:            "Secret",
		Namespace:       "team-a",
		NamespaceLabels: map[string]string{"team": "a", "env": "prod"},
	}

	westProd = Target{
		ClusterName:   "west-1",
		ClusterLabels: map[string]string{"region": "west", "env": "prod"},
		Namespace:     "team-a",
	}
	eastProd = Target{
		ClusterName:   "east-1",
		ClusterLabels: map[string]string{"region": "east", "env": "prod"},
		Namespace:     "team-a",
	}
)

// specAllowingWestTeamA permits Secret from team-a into namespace team-a on
// region=west clusters.
func specAllowingWestTeamA() r8rv1alpha1.ReplicationPolicySpec {
	return r8rv1alpha1.ReplicationPolicySpec{
		Sources: r8rv1alpha1.PolicySources{
			Namespaces: []string{"team-a"},
			Kinds:      []string{"Secret"},
		},
		Targets: r8rv1alpha1.PolicyTargets{
			ClusterSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"region": "west"},
			},
			Namespaces: []string{"team-a"},
		},
	}
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name     string
		source   Source
		targets  []Target
		policies []r8rv1alpha1.ReplicationPolicy
		// expected per target, same order as targets
		wantAllowed   []bool
		wantDimension []string // "" for allowed targets
		wantMatched   [][]string
	}{
		{
			// Spec: "Default deny" / Scenario: "No policies exist".
			name:          "no_policies_default_deny",
			source:        secretInTeamA,
			targets:       []Target{westProd},
			policies:      nil,
			wantAllowed:   []bool{false},
			wantDimension: []string{DimensionNoPolicies},
			wantMatched:   [][]string{nil},
		},
		{
			// Spec: "Allowlist matching dimensions" / Scenario: "All
			// dimensions match".
			name:    "all_dimensions_match",
			source:  secretInTeamA,
			targets: []Target{westProd},
			policies: []r8rv1alpha1.ReplicationPolicy{
				makePolicy("allow-west", specAllowingWestTeamA()),
			},
			wantAllowed:   []bool{true},
			wantDimension: []string{""},
			wantMatched:   [][]string{{"allow-west"}},
		},
		{
			// Spec: "Allowlist matching dimensions" / Scenario: "One dimension
			// fails": one target namespace denied, permitted targets proceed,
			// denial reported per target.
			name:   "one_target_namespace_denied_others_proceed",
			source: secretInTeamA,
			targets: []Target{
				westProd,
				{ClusterName: "west-1", ClusterLabels: westProd.ClusterLabels, Namespace: "team-b"},
			},
			policies: []r8rv1alpha1.ReplicationPolicy{
				makePolicy("allow-west", specAllowingWestTeamA()),
			},
			wantAllowed:   []bool{true, false},
			wantDimension: []string{"", DimensionTargetNamespace},
			wantMatched:   [][]string{{"allow-west"}, nil},
		},
		{
			// Spec: "Union semantics across policies" / Scenario: "Two partial
			// policies do not combine dimensions": policy A allows the source
			// namespace but not the target cluster; policy B allows the target
			// cluster but not the source namespace. No single policy permits.
			name:    "cross_policy_dimensions_do_not_mix",
			source:  secretInTeamA,
			targets: []Target{eastProd},
			policies: []r8rv1alpha1.ReplicationPolicy{
				// A: source ns team-a OK, but only west clusters.
				makePolicy("policy-a", specAllowingWestTeamA()),
				// B: east clusters OK, but only source ns team-b.
				makePolicy("policy-b", r8rv1alpha1.ReplicationPolicySpec{
					Sources: r8rv1alpha1.PolicySources{
						Namespaces: []string{"team-b"},
						Kinds:      []string{"Secret"},
					},
					Targets: r8rv1alpha1.PolicyTargets{
						ClusterSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{"region": "east"},
						},
						Namespaces: []string{"team-a"},
					},
				}),
			},
			wantAllowed: []bool{false},
			// Policy A progressed furthest (failed at cluster), so denial
			// names the targetCluster dimension.
			wantDimension: []string{DimensionTargetCluster},
			wantMatched:   [][]string{nil},
		},
		{
			// Spec: "Union semantics across policies": a target is allowed if
			// ANY single policy permits it in full; both matching policies are
			// listed, sorted.
			name:    "union_any_full_match_allows",
			source:  secretInTeamA,
			targets: []Target{westProd, eastProd},
			policies: []r8rv1alpha1.ReplicationPolicy{
				makePolicy("z-allow-west", specAllowingWestTeamA()),
				makePolicy("a-allow-everywhere", r8rv1alpha1.ReplicationPolicySpec{
					Sources: r8rv1alpha1.PolicySources{
						Namespaces: []string{"team-a"},
						Kinds:      []string{"Secret"},
					},
					Targets: r8rv1alpha1.PolicyTargets{
						// Empty selector: all clusters.
						ClusterSelector: metav1.LabelSelector{},
						Namespaces:      []string{"team-a"},
					},
				}),
			},
			wantAllowed:   []bool{true, true},
			wantDimension: []string{"", ""},
			wantMatched: [][]string{
				{"a-allow-everywhere", "z-allow-west"}, // sorted
				{"a-allow-everywhere"},
			},
		},
		{
			// Spec: "Policy authoring is admin-scoped" / Scenario: "Developer
			// cannot widen their own permissions": annotating a target no
			// policy allows for that namespace is denied; nothing overrides.
			name:    "developer_cannot_widen_permissions",
			source:  secretInTeamA,
			targets: []Target{eastProd},
			policies: []r8rv1alpha1.ReplicationPolicy{
				makePolicy("allow-west", specAllowingWestTeamA()),
			},
			wantAllowed:   []bool{false},
			wantDimension: []string{DimensionTargetCluster},
			wantMatched:   [][]string{nil},
		},
		{
			// Edge: source kind not allowlisted.
			name: "source_kind_denied",
			source: Source{
				Kind: "ConfigMap", Namespace: "team-a",
				NamespaceLabels: secretInTeamA.NamespaceLabels,
			},
			targets: []Target{westProd},
			policies: []r8rv1alpha1.ReplicationPolicy{
				makePolicy("allow-west", specAllowingWestTeamA()),
			},
			wantAllowed:   []bool{false},
			wantDimension: []string{DimensionSourceKind},
			wantMatched:   [][]string{nil},
		},
		{
			// Edge: source namespace matched via namespaceSelector only.
			name:    "source_namespace_matched_by_selector",
			source:  secretInTeamA,
			targets: []Target{westProd},
			policies: []r8rv1alpha1.ReplicationPolicy{
				makePolicy("selector-only", r8rv1alpha1.ReplicationPolicySpec{
					Sources: r8rv1alpha1.PolicySources{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"team": "a"},
						},
						Kinds: []string{"Secret"},
					},
					Targets: r8rv1alpha1.PolicyTargets{
						ClusterSelector: metav1.LabelSelector{},
						Namespaces:      []string{"team-a"},
					},
				}),
			},
			wantAllowed:   []bool{true},
			wantDimension: []string{""},
			wantMatched:   [][]string{{"selector-only"}},
		},
		{
			// Edge: exact list and selector are alternatives — the exact name
			// matches even though the selector does not.
			name:    "exact_name_suffices_when_selector_does_not_match",
			source:  secretInTeamA,
			targets: []Target{westProd},
			policies: []r8rv1alpha1.ReplicationPolicy{
				makePolicy("either-or", r8rv1alpha1.ReplicationPolicySpec{
					Sources: r8rv1alpha1.PolicySources{
						Namespaces: []string{"team-a"},
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"team": "nomatch"},
						},
						Kinds: []string{"Secret"},
					},
					Targets: r8rv1alpha1.PolicyTargets{
						ClusterSelector: metav1.LabelSelector{},
						Namespaces:      []string{"team-a"},
					},
				}),
			},
			wantAllowed:   []bool{true},
			wantDimension: []string{""},
			wantMatched:   [][]string{{"either-or"}},
		},
		{
			// Edge: nil selector and empty exact list allowlist no source
			// namespaces at all.
			name:    "empty_sources_allow_nothing",
			source:  secretInTeamA,
			targets: []Target{westProd},
			policies: []r8rv1alpha1.ReplicationPolicy{
				makePolicy("no-sources", r8rv1alpha1.ReplicationPolicySpec{
					Sources: r8rv1alpha1.PolicySources{Kinds: []string{"Secret"}},
					Targets: r8rv1alpha1.PolicyTargets{
						ClusterSelector: metav1.LabelSelector{},
						Namespaces:      []string{"team-a"},
					},
				}),
			},
			wantAllowed:   []bool{false},
			wantDimension: []string{DimensionSourceNamespace},
			wantMatched:   [][]string{nil},
		},
		{
			// Edge: empty (non-nil) namespaceSelector matches every namespace.
			name:    "empty_namespace_selector_matches_all",
			source:  secretInTeamA,
			targets: []Target{westProd},
			policies: []r8rv1alpha1.ReplicationPolicy{
				makePolicy("all-namespaces", r8rv1alpha1.ReplicationPolicySpec{
					Sources: r8rv1alpha1.PolicySources{
						NamespaceSelector: &metav1.LabelSelector{},
						Kinds:             []string{"Secret"},
					},
					Targets: r8rv1alpha1.PolicyTargets{
						ClusterSelector: metav1.LabelSelector{},
						Namespaces:      []string{"team-a"},
					},
				}),
			},
			wantAllowed:   []bool{true},
			wantDimension: []string{""},
			wantMatched:   [][]string{{"all-namespaces"}},
		},
		{
			// Edge: empty clusterSelector matches all clusters.
			name:    "empty_cluster_selector_matches_all_clusters",
			source:  secretInTeamA,
			targets: []Target{westProd, eastProd},
			policies: []r8rv1alpha1.ReplicationPolicy{
				makePolicy("any-cluster", r8rv1alpha1.ReplicationPolicySpec{
					Sources: r8rv1alpha1.PolicySources{
						Namespaces: []string{"team-a"},
						Kinds:      []string{"Secret"},
					},
					Targets: r8rv1alpha1.PolicyTargets{
						ClusterSelector: metav1.LabelSelector{},
						Namespaces:      []string{"team-a"},
					},
				}),
			},
			wantAllowed:   []bool{true, true},
			wantDimension: []string{"", ""},
			wantMatched:   [][]string{{"any-cluster"}, {"any-cluster"}},
		},
		{
			// Edge: empty targets.namespaces list allows nothing (empty
			// allowlist semantics), even though every other dimension matches.
			name:    "empty_target_namespaces_allow_nothing",
			source:  secretInTeamA,
			targets: []Target{westProd},
			policies: []r8rv1alpha1.ReplicationPolicy{
				makePolicy("no-target-namespaces", r8rv1alpha1.ReplicationPolicySpec{
					Sources: r8rv1alpha1.PolicySources{
						Namespaces: []string{"team-a"},
						Kinds:      []string{"Secret"},
					},
					Targets: r8rv1alpha1.PolicyTargets{
						ClusterSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{"region": "west"},
						},
						Namespaces: nil,
					},
				}),
			},
			wantAllowed:   []bool{false},
			wantDimension: []string{DimensionTargetNamespace},
			wantMatched:   [][]string{nil},
		},
		{
			// Edge: names are exact — no glob or prefix matching.
			name:    "exact_names_no_globs",
			source:  secretInTeamA,
			targets: []Target{westProd},
			policies: []r8rv1alpha1.ReplicationPolicy{
				makePolicy("glob-like", r8rv1alpha1.ReplicationPolicySpec{
					Sources: r8rv1alpha1.PolicySources{
						Namespaces: []string{"team-*", "team-"},
						Kinds:      []string{"Secret"},
					},
					Targets: r8rv1alpha1.PolicyTargets{
						ClusterSelector: metav1.LabelSelector{},
						Namespaces:      []string{"team-a"},
					},
				}),
			},
			wantAllowed:   []bool{false},
			wantDimension: []string{DimensionSourceNamespace},
			wantMatched:   [][]string{nil},
		},
		{
			// Edge: invalid label selector fails closed — dimension does not
			// match, target denied.
			name:    "invalid_selector_fails_closed",
			source:  secretInTeamA,
			targets: []Target{westProd},
			policies: []r8rv1alpha1.ReplicationPolicy{
				makePolicy("broken-selector", r8rv1alpha1.ReplicationPolicySpec{
					Sources: r8rv1alpha1.PolicySources{
						Namespaces: []string{"team-a"},
						Kinds:      []string{"Secret"},
					},
					Targets: r8rv1alpha1.PolicyTargets{
						ClusterSelector: metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{{
								Key:      "region",
								Operator: "BogusOperator",
							}},
						},
						Namespaces: []string{"team-a"},
					},
				}),
			},
			wantAllowed:   []bool{false},
			wantDimension: []string{DimensionTargetCluster},
			wantMatched:   [][]string{nil},
		},
		{
			// Edge: multiple policies, each failing on a different dimension;
			// the reported dimension is the furthest any single policy reached.
			name:    "multiple_partial_matches_report_furthest_dimension",
			source:  secretInTeamA,
			targets: []Target{{ClusterName: "west-1", ClusterLabels: westProd.ClusterLabels, Namespace: "team-c"}},
			policies: []r8rv1alpha1.ReplicationPolicy{
				// Fails at sourceNamespace.
				makePolicy("wrong-ns", r8rv1alpha1.ReplicationPolicySpec{
					Sources: r8rv1alpha1.PolicySources{
						Namespaces: []string{"other"},
						Kinds:      []string{"Secret"},
					},
					Targets: r8rv1alpha1.PolicyTargets{
						ClusterSelector: metav1.LabelSelector{},
						Namespaces:      []string{"team-c"},
					},
				}),
				// Fails at targetNamespace (furthest).
				makePolicy("allow-west", specAllowingWestTeamA()),
			},
			wantAllowed:   []bool{false},
			wantDimension: []string{DimensionTargetNamespace},
			wantMatched:   [][]string{nil},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := Evaluate(Request{Source: tc.source, Targets: tc.targets}, tc.policies)
			if len(res.Decisions) != len(tc.targets) {
				t.Fatalf("got %d decisions, want %d", len(res.Decisions), len(tc.targets))
			}
			for i, d := range res.Decisions {
				if d.Allowed != tc.wantAllowed[i] {
					t.Errorf("target %d: Allowed = %v, want %v (reason: %s)",
						i, d.Allowed, tc.wantAllowed[i], d.Reason)
				}
				if d.DeniedDimension != tc.wantDimension[i] {
					t.Errorf("target %d: DeniedDimension = %q, want %q",
						i, d.DeniedDimension, tc.wantDimension[i])
				}
				if !reflect.DeepEqual(d.MatchedPolicies, tc.wantMatched[i]) {
					t.Errorf("target %d: MatchedPolicies = %v, want %v",
						i, d.MatchedPolicies, tc.wantMatched[i])
				}
				if d.Reason == "" {
					t.Errorf("target %d: Reason must never be empty", i)
				}
				if !d.Allowed && !strings.Contains(d.Reason, "denied") {
					t.Errorf("target %d: denial reason %q should say denied", i, d.Reason)
				}
			}
		})
	}
}

// TestEvaluateReasonsNameDimension verifies denial reasons name the failing
// dimension so webhook messages and conditions are actionable.
func TestEvaluateReasonsNameDimension(t *testing.T) {
	pol := makePolicy("allow-west", specAllowingWestTeamA())

	tests := []struct {
		name     string
		source   Source
		target   Target
		policies []r8rv1alpha1.ReplicationPolicy
		wantSub  string
	}{
		{"no_policies", secretInTeamA, westProd, nil, "no ReplicationPolicy objects exist"},
		{"source_namespace", Source{Kind: "Secret", Namespace: "team-x"}, westProd,
			[]r8rv1alpha1.ReplicationPolicy{pol}, "sourceNamespace"},
		{"source_kind", Source{Kind: "ConfigMap", Namespace: "team-a"}, westProd,
			[]r8rv1alpha1.ReplicationPolicy{pol}, "sourceKind"},
		{"target_cluster", secretInTeamA, eastProd,
			[]r8rv1alpha1.ReplicationPolicy{pol}, "targetCluster"},
		{"target_namespace", secretInTeamA,
			Target{ClusterName: "west-1", ClusterLabels: westProd.ClusterLabels, Namespace: "team-b"},
			[]r8rv1alpha1.ReplicationPolicy{pol}, "targetNamespace"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := Evaluate(Request{Source: tc.source, Targets: []Target{tc.target}}, tc.policies)
			d := res.Decisions[0]
			if d.Allowed {
				t.Fatalf("expected denial, got allowed: %+v", d)
			}
			if !strings.Contains(d.Reason, tc.wantSub) {
				t.Errorf("Reason %q does not contain %q", d.Reason, tc.wantSub)
			}
		})
	}
}

// TestResultAllowedDenied covers the aggregate accessors.
func TestResultAllowedDenied(t *testing.T) {
	res := Evaluate(Request{
		Source:  secretInTeamA,
		Targets: []Target{westProd, eastProd},
	}, []r8rv1alpha1.ReplicationPolicy{makePolicy("allow-west", specAllowingWestTeamA())})

	allowed, denied := res.Allowed(), res.Denied()
	if len(allowed) != 1 || allowed[0].Target.ClusterName != "west-1" {
		t.Errorf("Allowed() = %+v, want exactly west-1", allowed)
	}
	if len(denied) != 1 || denied[0].Target.ClusterName != "east-1" {
		t.Errorf("Denied() = %+v, want exactly east-1", denied)
	}
}

// TestEvaluateDeterministic verifies policy order does not change the outcome.
func TestEvaluateDeterministic(t *testing.T) {
	p1 := makePolicy("b-policy", specAllowingWestTeamA())
	p2 := makePolicy("a-policy", specAllowingWestTeamA())
	req := Request{Source: secretInTeamA, Targets: []Target{westProd}}

	r1 := Evaluate(req, []r8rv1alpha1.ReplicationPolicy{p1, p2})
	r2 := Evaluate(req, []r8rv1alpha1.ReplicationPolicy{p2, p1})
	if !reflect.DeepEqual(r1, r2) {
		t.Errorf("evaluation depends on policy order:\n%+v\nvs\n%+v", r1, r2)
	}
	want := []string{"a-policy", "b-policy"}
	if !reflect.DeepEqual(r1.Decisions[0].MatchedPolicies, want) {
		t.Errorf("MatchedPolicies = %v, want sorted %v", r1.Decisions[0].MatchedPolicies, want)
	}
}
