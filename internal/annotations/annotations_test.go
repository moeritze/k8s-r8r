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

package annotations

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/labels"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
)

// selProd is the selector string used across the valid-request cases.
const selProd = "env=prod"

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		ann  map[string]string

		wantNil     bool   // expect (nil, nil): no request
		wantErr     string // substring the error must contain; empty = no error
		wantSel     string // expected ClusterSelector.String()
		wantNss     []string
		wantTgtName string
	}{
		{
			name:    "no annotations",
			ann:     nil,
			wantNil: true,
		},
		{
			name:    "unrelated annotations only",
			ann:     map[string]string{"foo.io/bar": "x"},
			wantNil: true,
		},
		{
			name: "valid full request",
			ann: map[string]string{
				KeyReplicate:        ValueTrue,
				KeyTargetClusters:   selProd,
				KeyTargetNamespaces: "a,b",
				KeyTargetName:       "renamed",
			},
			wantSel:     selProd,
			wantNss:     []string{"a", "b"},
			wantTgtName: "renamed",
		},
		{
			name: "namespaces trimmed and ordered",
			ann: map[string]string{
				KeyReplicate:        ValueTrue,
				KeyTargetClusters:   "tier in (edge,core)",
				KeyTargetNamespaces: " x , y ",
			},
			wantSel: "tier in (core,edge)", // labels.Parse sorts set values
			wantNss: []string{"x", "y"},
		},
		{
			name: "absent target-clusters selects nothing",
			ann: map[string]string{
				KeyReplicate: ValueTrue,
			},
			wantSel: labels.Nothing().String(),
		},
		{
			name: "empty target-clusters selects nothing",
			ann: map[string]string{
				KeyReplicate:      ValueTrue,
				KeyTargetClusters: "  ",
			},
			wantSel: labels.Nothing().String(),
		},
		{
			name: "replicate false opts out",
			ann: map[string]string{
				KeyReplicate:      ValueFalse,
				KeyTargetClusters: selProd,
			},
			wantNil: true,
		},
		{
			name: "staged target keys without replicate opt out",
			ann: map[string]string{
				KeyTargetClusters: selProd,
			},
			wantNil: true,
		},
		{
			name: "engine source-hash annotation is tolerated",
			ann: map[string]string{
				KeyReplicate:      ValueTrue,
				KeyTargetClusters: selProd,
				KeySourceHash:     "sha256:abc",
			},
			wantSel: selProd,
		},
		{
			name:    "replicate garbage value",
			ann:     map[string]string{KeyReplicate: "yes"},
			wantErr: `annotation "r8r.io/replicate": expected "true" or "false", got "yes"`,
		},
		{
			name: "wildcard cluster selector rejected",
			ann: map[string]string{
				KeyReplicate:      ValueTrue,
				KeyTargetClusters: "*",
			},
			wantErr: `annotation "r8r.io/target-clusters": wildcard "*" is not supported`,
		},
		{
			name: "unparseable cluster selector",
			ann: map[string]string{
				KeyReplicate:      ValueTrue,
				KeyTargetClusters: "env==(bad",
			},
			wantErr: `annotation "r8r.io/target-clusters": invalid label selector`,
		},
		{
			name: "empty namespace entry",
			ann: map[string]string{
				KeyReplicate:        ValueTrue,
				KeyTargetNamespaces: "a,,b",
			},
			wantErr: `annotation "r8r.io/target-namespaces": empty namespace entry`,
		},
		{
			name: "invalid namespace name",
			ann: map[string]string{
				KeyReplicate:        ValueTrue,
				KeyTargetNamespaces: "Bad_NS",
			},
			wantErr: `annotation "r8r.io/target-namespaces": invalid namespace "Bad_NS"`,
		},
		{
			name: "duplicate namespace",
			ann: map[string]string{
				KeyReplicate:        ValueTrue,
				KeyTargetNamespaces: "a,a",
			},
			wantErr: `annotation "r8r.io/target-namespaces": duplicate namespace "a"`,
		},
		{
			name: "invalid target name",
			ann: map[string]string{
				KeyReplicate:  ValueTrue,
				KeyTargetName: "Not_A_Subdomain",
			},
			wantErr: `annotation "r8r.io/target-name": invalid name "Not_A_Subdomain"`,
		},
		{
			name: "unknown r8r.io key rejected",
			ann: map[string]string{
				KeyReplicate:            ValueTrue,
				"r8r.io/target-cluster": selProd, // typo: singular
			},
			wantErr: `annotation "r8r.io/target-cluster": unknown r8r.io annotation`,
		},
		{
			name: "multiple errors aggregated",
			ann: map[string]string{
				KeyReplicate:        "maybe",
				KeyTargetNamespaces: "a,,b",
			},
			wantErr: "r8r.io/replicate",
		},
		{
			name: "validation happens even when opted out",
			ann: map[string]string{
				KeyReplicate:      ValueFalse,
				KeyTargetClusters: "env==(bad",
			},
			wantErr: `annotation "r8r.io/target-clusters": invalid label selector`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := Parse(tc.ann)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				if req != nil {
					t.Fatalf("expected nil request on error, got %+v", req)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantNil {
				if req != nil {
					t.Fatalf("expected nil request, got %+v", req)
				}
				return
			}
			if req == nil {
				t.Fatal("expected a request, got nil")
			}
			if got := req.ClusterSelector.String(); got != tc.wantSel {
				t.Errorf("ClusterSelector = %q, want %q", got, tc.wantSel)
			}
			if !reflect.DeepEqual(req.TargetNamespaces, tc.wantNss) {
				t.Errorf("TargetNamespaces = %v, want %v", req.TargetNamespaces, tc.wantNss)
			}
			if req.TargetName != tc.wantTgtName {
				t.Errorf("TargetName = %q, want %q", req.TargetName, tc.wantTgtName)
			}
		})
	}
}

func TestParseAggregatesAllErrors(t *testing.T) {
	_, err := Parse(map[string]string{
		KeyReplicate:        "maybe",
		KeyTargetClusters:   "*",
		KeyTargetNamespaces: "a,a",
		KeyTargetName:       "UPPER",
		KeyConflictPolicy:   "Yolo",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{
		KeyReplicate, KeyTargetClusters, KeyTargetNamespaces, KeyTargetName, KeyConflictPolicy,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("aggregate error missing %q: %v", want, err)
		}
	}
}

// TestParseConflictPolicy covers the request side of the conflict two-key turn
// (issue #34): the closed value set, and the deliberate default when the
// annotation says nothing.
func TestParseConflictPolicy(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		absent  bool
		want    r8rv1alpha1.ConflictPolicy
		wantErr string
	}{
		{name: "absent defaults to Fail", absent: true, want: r8rv1alpha1.ConflictPolicyFail},
		{name: "empty defaults to Fail", value: "  ", want: r8rv1alpha1.ConflictPolicyFail},
		{name: "explicit Fail", value: "Fail", want: r8rv1alpha1.ConflictPolicyFail},
		{name: "Adopt", value: "Adopt", want: r8rv1alpha1.ConflictPolicyAdopt},
		{name: "Overwrite", value: "Overwrite", want: r8rv1alpha1.ConflictPolicyOverwrite},
		{name: "surrounding space tolerated", value: " Overwrite ", want: r8rv1alpha1.ConflictPolicyOverwrite},
		{name: "wrong case rejected", value: "overwrite", wantErr: "expected one of"},
		{name: "unknown value rejected", value: "Yolo", wantErr: "expected one of"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ann := map[string]string{KeyReplicate: ValueTrue, KeyTargetClusters: selProd}
			if !tc.absent {
				ann[KeyConflictPolicy] = tc.value
			}
			req, err := Parse(ann)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
				}
				if got := RequestedConflictPolicy(ann); got != DefaultConflictPolicy {
					t.Errorf("RequestedConflictPolicy on a malformed value = %v, want %v",
						got, DefaultConflictPolicy)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if req.ConflictPolicy != tc.want {
				t.Errorf("ConflictPolicy = %q, want %q", req.ConflictPolicy, tc.want)
			}
			if got := RequestedConflictPolicy(ann); got != tc.want {
				t.Errorf("RequestedConflictPolicy = %q, want %q", got, tc.want)
			}
		})
	}
}

// The closed-set check must keep rejecting typos, including near-misses of
// the new key, and the hint must list every valid key.
func TestConflictPolicyKeyIsClosedSet(t *testing.T) {
	_, err := Parse(map[string]string{
		KeyReplicate:            ValueTrue,
		"r8r.io/conflict-polic": "Overwrite",
	})
	if err == nil {
		t.Fatal("expected the typo to be rejected")
	}
	if !strings.Contains(err.Error(), KeyConflictPolicy) {
		t.Errorf("unknown-key hint does not list %q: %v", KeyConflictPolicy, err)
	}
	if got := RequestKeys(); len(got) != 5 || !slices.Contains(got, KeyConflictPolicy) {
		t.Errorf("RequestKeys() = %v, want the five request keys including %q", got, KeyConflictPolicy)
	}
	if !HasRequest(map[string]string{KeyConflictPolicy: "Overwrite"}) {
		t.Error("conflict-policy must count as a request annotation")
	}
}

func TestEffectiveNamespaces(t *testing.T) {
	empty := &Request{}
	if got := empty.EffectiveNamespaces("src-ns"); !reflect.DeepEqual(got, []string{"src-ns"}) {
		t.Errorf("default = %v, want [src-ns]", got)
	}
	explicit := &Request{TargetNamespaces: []string{"a", "b"}}
	if got := explicit.EffectiveNamespaces("src-ns"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("explicit = %v, want [a b]", got)
	}
}

func TestHasRequestAndReplicates(t *testing.T) {
	if HasRequest(nil) {
		t.Error("HasRequest(nil) = true")
	}
	if HasRequest(map[string]string{KeySourceHash: "sha256:abc"}) {
		t.Error("source-hash alone must not count as a request")
	}
	if !HasRequest(map[string]string{KeyTargetClusters: selProd}) {
		t.Error("target-clusters must count as a request annotation")
	}
	if !Replicates(map[string]string{KeyReplicate: ValueTrue}) {
		t.Error("Replicates(true) = false")
	}
	if Replicates(map[string]string{KeyReplicate: "yes"}) {
		t.Error("Replicates(yes) = true")
	}
}

func TestClusterSelectorMatching(t *testing.T) {
	req, err := Parse(map[string]string{
		KeyReplicate:      ValueTrue,
		KeyTargetClusters: selProd,
	})
	if err != nil {
		t.Fatal(err)
	}
	prod := labels.Set{"env": "prod"}
	if !req.ClusterSelector.Matches(prod) {
		t.Error("selector must match env=prod")
	}
	if req.ClusterSelector.Matches(labels.Set{"env": "dev"}) {
		t.Error("selector must not match env=dev")
	}

	// Absent selector selects no clusters, even label-less ones.
	none, err := Parse(map[string]string{KeyReplicate: ValueTrue})
	if err != nil {
		t.Fatal(err)
	}
	if none.ClusterSelector.Matches(labels.Set{}) || none.ClusterSelector.Matches(prod) {
		t.Error("absent selector must select no clusters")
	}
}
