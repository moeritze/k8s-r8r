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
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
)

func TestEffectiveConflictPolicy(t *testing.T) {
	cases := []struct {
		name    string
		allowed []r8rv1alpha1.ConflictPolicy
		want    r8rv1alpha1.ConflictPolicy
	}{
		{"empty defaults to Fail", nil, r8rv1alpha1.ConflictPolicyFail},
		{"only Fail", []r8rv1alpha1.ConflictPolicy{r8rv1alpha1.ConflictPolicyFail}, r8rv1alpha1.ConflictPolicyFail},
		{"Adopt beats Fail", []r8rv1alpha1.ConflictPolicy{r8rv1alpha1.ConflictPolicyFail, r8rv1alpha1.ConflictPolicyAdopt}, r8rv1alpha1.ConflictPolicyAdopt},
		{"Overwrite beats Adopt", []r8rv1alpha1.ConflictPolicy{r8rv1alpha1.ConflictPolicyAdopt, r8rv1alpha1.ConflictPolicyOverwrite}, r8rv1alpha1.ConflictPolicyOverwrite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveConflictPolicy(tc.allowed); got != tc.want {
				t.Errorf("EffectiveConflictPolicy(%v) = %v, want %v", tc.allowed, got, tc.want)
			}
		})
	}
}

// unmanagedObject builds an existing spoke object without k8s-r8r marks.
func unmanagedObject(payload string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]interface{}{"name": "web-creds", "namespace": "app"},
		"type":       "Opaque",
		"data":       map[string]interface{}{"password": payload},
	}}
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

	fail := []r8rv1alpha1.ConflictPolicy{r8rv1alpha1.ConflictPolicyFail}
	adopt := []r8rv1alpha1.ConflictPolicy{r8rv1alpha1.ConflictPolicyFail, r8rv1alpha1.ConflictPolicyAdopt}
	overwrite := []r8rv1alpha1.ConflictPolicy{r8rv1alpha1.ConflictPolicyFail, r8rv1alpha1.ConflictPolicyOverwrite}

	cases := []struct {
		name     string
		existing *unstructured.Unstructured
		allowed  []r8rv1alpha1.ConflictPolicy
		want     ConflictAction
	}{
		{"own replica is not a conflict", ourReplica, fail, ActionApply},
		{"replica of another source always fails", foreignReplica, overwrite, ActionFail},
		{"unmanaged with Fail", differentContent, fail, ActionFail},
		{"unmanaged with default (nil) grants", differentContent, nil, ActionFail},
		{"adopt on identical content", sameContent, adopt, ActionAdopt},
		{"adopt refused on differing content", differentContent, adopt, ActionFail},
		{"overwrite takes over", differentContent, overwrite, ActionOverwrite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := DecideConflict(tc.existing, src.GetUID(), srcHash, tc.allowed)
			if d.Action != tc.want {
				t.Errorf("action = %v (%s), want %v", d.Action, d.Message, tc.want)
			}
			if d.Action != ActionApply && d.Message == "" {
				t.Error("non-trivial decision carries no message")
			}
		})
	}
}
