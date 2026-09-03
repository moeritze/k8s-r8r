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

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
	"github.com/moeritze/k8s-r8r/internal/annotations"
)

var (
	cpFail      = r8rv1alpha1.ConflictPolicyFail
	cpAdopt     = r8rv1alpha1.ConflictPolicyAdopt
	cpOverwrite = r8rv1alpha1.ConflictPolicyOverwrite
)

// TestEffectiveConflictPolicy is the two-key intersection table: the engine
// acts on the WEAKER of what the request asks for and what policy permits
// (replication-engine spec, "Conflict handling"; issue #34).
func TestEffectiveConflictPolicy(t *testing.T) {
	grantFail := []r8rv1alpha1.ConflictPolicy{cpFail}
	grantAdopt := []r8rv1alpha1.ConflictPolicy{cpFail, cpAdopt}
	grantOverwrite := []r8rv1alpha1.ConflictPolicy{cpFail, cpOverwrite}
	grantAll := []r8rv1alpha1.ConflictPolicy{cpFail, cpAdopt, cpOverwrite}

	cases := []struct {
		name      string
		requested r8rv1alpha1.ConflictPolicy
		allowed   []r8rv1alpha1.ConflictPolicy
		want      r8rv1alpha1.ConflictPolicy
	}{
		// The policy key alone never turns the lock.
		{"grant Overwrite, request silent", cpFail, grantOverwrite, cpFail},
		{"grant Adopt, request silent", cpFail, grantAdopt, cpFail},
		{"grant Overwrite, no grants at all", cpFail, nil, cpFail},
		// The request key alone never turns it either.
		{"request Overwrite, grant Fail", cpOverwrite, grantFail, cpFail},
		{"request Adopt, grant Fail", cpAdopt, grantFail, cpFail},
		{"request Overwrite, no grants at all", cpOverwrite, nil, cpFail},
		// Both keys: the weaker one wins.
		{"request Overwrite, grant Adopt", cpOverwrite, grantAdopt, cpAdopt},
		{"request Adopt, grant Overwrite", cpAdopt, grantOverwrite, cpAdopt},
		{"request Adopt, grant Adopt", cpAdopt, grantAdopt, cpAdopt},
		{"request Overwrite, grant Overwrite", cpOverwrite, grantOverwrite, cpOverwrite},
		{"request Overwrite, grant everything", cpOverwrite, grantAll, cpOverwrite},
		// Anything unnameable ranks as Fail on both sides.
		{"unknown request value", r8rv1alpha1.ConflictPolicy("Yolo"), grantOverwrite, cpFail},
		{"unknown grant value", cpOverwrite, []r8rv1alpha1.ConflictPolicy{"Yolo"}, cpFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveConflictPolicy(tc.requested, tc.allowed); got != tc.want {
				t.Errorf("EffectiveConflictPolicy(%v, %v) = %v, want %v",
					tc.requested, tc.allowed, got, tc.want)
			}
		})
	}
}

// TestEffectiveConflictPolicyIsCommutativeInStrength pins the property the
// spec sentence states — the result is never stronger than either key — over
// every combination, so a future refactor cannot reintroduce a one-key turn.
func TestEffectiveConflictPolicyNeverExceedsEitherKey(t *testing.T) {
	all := []r8rv1alpha1.ConflictPolicy{cpFail, cpAdopt, cpOverwrite}
	for _, requested := range all {
		for _, grant := range all {
			allowed := []r8rv1alpha1.ConflictPolicy{cpFail, grant}
			got := EffectiveConflictPolicy(requested, allowed)
			if conflictPolicyStrength(got) > conflictPolicyStrength(requested) {
				t.Errorf("requested %v, grant %v: effective %v is stronger than the request",
					requested, grant, got)
			}
			if conflictPolicyStrength(got) > conflictPolicyStrength(grant) {
				t.Errorf("requested %v, grant %v: effective %v is stronger than the grant",
					requested, grant, got)
			}
		}
	}
}

// unmanagedObject builds an existing spoke object without k8s-r8r marks.
func unmanagedObject(payload string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]any{"name": "web-creds", "namespace": "app"},
		"type":       "Opaque",
		"data":       map[string]any{"password": payload},
	}}
}

// requesting returns a copy of the source annotated with the given
// request-side conflict policy; an empty policy leaves the annotation off.
func requesting(src *unstructured.Unstructured, cp r8rv1alpha1.ConflictPolicy) *unstructured.Unstructured {
	out := src.DeepCopy()
	ann := out.GetAnnotations()
	if ann == nil {
		ann = map[string]string{}
	}
	if cp == "" {
		delete(ann, annotations.KeyConflictPolicy)
	} else {
		ann[annotations.KeyConflictPolicy] = string(cp)
	}
	out.SetAnnotations(ann)
	return out
}

// TestDecideConflict is the conflict decision table (design D7; spec
// "Conflict handling" scenarios feed the reconciler tests).
func TestDecideConflict(t *testing.T) {
	src := testSecret()
	srcHash := SourceHash(src)

	sameContent := unmanagedObject("c2VjcmV0")
	sameContent.SetLabels(map[string]string{"team": "web"})
	sameContent.SetAnnotations(map[string]string{"note": "keep-me"})
	if SourceHash(sameContent) != srcHash {
		t.Fatal("fixture error: content hashes should match")
	}
	differentContent := unmanagedObject("b3RoZXI=")

	ourReplica, _ := Renderer{}.Render(src, "app", "")
	foreignReplica, _ := Renderer{}.Render(src, "app", "")
	fl := foreignReplica.GetLabels()
	fl[LabelSourceUID] = "someone-else"
	foreignReplica.SetLabels(fl)

	fail := []r8rv1alpha1.ConflictPolicy{cpFail}
	adopt := []r8rv1alpha1.ConflictPolicy{cpFail, cpAdopt}
	overwrite := []r8rv1alpha1.ConflictPolicy{cpFail, cpOverwrite}

	cases := []struct {
		name      string
		existing  *unstructured.Unstructured
		requested r8rv1alpha1.ConflictPolicy
		allowed   []r8rv1alpha1.ConflictPolicy
		want      ConflictAction
	}{
		{"own replica is not a conflict", ourReplica, cpOverwrite, fail, ActionApply},
		{"replica of another source always fails", foreignReplica, cpOverwrite, overwrite, ActionFail},
		{"unmanaged with Fail", differentContent, cpOverwrite, fail, ActionFail},
		{"unmanaged with default (nil) grants", differentContent, cpOverwrite, nil, ActionFail},
		{"adopt on identical content", sameContent, cpAdopt, adopt, ActionAdopt},
		{"adopt refused on differing content", differentContent, cpAdopt, adopt, ActionFail},
		{"overwrite takes over", differentContent, cpOverwrite, overwrite, ActionOverwrite},

		// Issue #34: the policy grant alone is not enough.
		{"grant Overwrite without a request opt-in fails", differentContent, "", overwrite, ActionFail},
		{"grant Adopt without a request opt-in fails", sameContent, "", adopt, ActionFail},
		{"explicit Fail request refuses a granted Overwrite", differentContent, cpFail, overwrite, ActionFail},
		{"request Adopt under an Overwrite grant only adopts", sameContent, cpAdopt, overwrite, ActionAdopt},
		{"request Adopt under an Overwrite grant does not overwrite", differentContent, cpAdopt, overwrite, ActionFail},
		{"request Overwrite under an Adopt grant only adopts", sameContent, cpOverwrite, adopt, ActionAdopt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := DecideConflict(tc.existing, requesting(src, tc.requested), srcHash, tc.allowed)
			if d.Action != tc.want {
				t.Errorf("action = %v (%s), want %v", d.Action, d.Message, tc.want)
			}
			if d.Action != ActionApply && d.Message == "" {
				t.Error("non-trivial decision carries no message")
			}
		})
	}
}

// TestDecideConflictExplainsWhichKeyIsMissing pins the operator-facing half of
// issue #34: a conflict that Fails because one of the two keys did not turn
// must say which one, so a missing per-object opt-in is not mistaken for a
// policy that never granted the escalation. Messages stay payload-free.
func TestDecideConflictExplainsWhichKeyIsMissing(t *testing.T) {
	src := testSecret()
	srcHash := SourceHash(src)
	existing := unmanagedObject("b3RoZXI=")
	overwrite := []r8rv1alpha1.ConflictPolicy{cpFail, cpOverwrite}
	fail := []r8rv1alpha1.ConflictPolicy{cpFail}

	cases := []struct {
		name      string
		requested r8rv1alpha1.ConflictPolicy
		allowed   []r8rv1alpha1.ConflictPolicy
		wants     []string
	}{
		{
			name:      "request silent under a grant",
			requested: "",
			allowed:   overwrite,
			wants:     []string{annotations.KeyConflictPolicy, "does not set", string(cpOverwrite)},
		},
		{
			name:      "request asks for more than policy grants",
			requested: cpOverwrite,
			allowed:   fail,
			wants:     []string{"asks for", string(cpOverwrite), "no matching ReplicationPolicy"},
		},
		{
			name:      "neither key turns",
			requested: "",
			allowed:   fail,
			wants:     []string{annotations.KeyConflictPolicy, "no matching ReplicationPolicy"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := DecideConflict(existing, requesting(src, tc.requested), srcHash, tc.allowed)
			if d.Action != ActionFail {
				t.Fatalf("action = %v, want %v", d.Action, ActionFail)
			}
			for _, want := range tc.wants {
				if !strings.Contains(d.Message, want) {
					t.Errorf("message %q does not mention %q", d.Message, want)
				}
			}
			if strings.Contains(d.Message, "b3RoZXI=") || strings.Contains(d.Message, "c2VjcmV0") {
				t.Errorf("message leaks payload: %q", d.Message)
			}
		})
	}
}

// TestDecideConflictIgnoresMalformedRequestValue: Parse rejects malformed
// values upstream, but if one reaches the engine it must read as "consents to
// nothing" rather than as a grant.
func TestDecideConflictIgnoresMalformedRequestValue(t *testing.T) {
	src := requesting(testSecret(), r8rv1alpha1.ConflictPolicy("overwrite")) // wrong case
	d := DecideConflict(unmanagedObject("b3RoZXI="), src, SourceHash(testSecret()),
		[]r8rv1alpha1.ConflictPolicy{cpFail, cpOverwrite})
	if d.Action != ActionFail {
		t.Errorf("action = %v (%s), want %v", d.Action, d.Message, ActionFail)
	}
}
