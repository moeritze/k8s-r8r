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

package webhook

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
	"github.com/moeritze/k8s-r8r/internal/policy"
)

const secretPayload = "hunter2-super-secret" // must never appear in responses

// admissionRequest builds an admission.Request for a Secret-shaped object with
// the given annotations. The raw object includes payload data so tests can
// assert it never leaks into responses.
func admissionRequest(t *testing.T, op admissionv1.Operation, kind, namespace string, ann map[string]string) admission.Request {
	t.Helper()
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       kind,
		"metadata": map[string]any{
			"name":        "app-credentials",
			"namespace":   namespace,
			"annotations": ann,
		},
		"data": map[string]any{"password": secretPayload},
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal object: %v", err)
	}
	return admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: op,
			Kind:      metav1.GroupVersionKind{Version: "v1", Kind: kind},
			Namespace: namespace,
			Object:    runtime.RawExtension{Raw: raw},
		},
	}
}

func newHandler(t *testing.T, policies ...*r8rv1alpha1.ReplicationPolicy) *Handler {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := r8rv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	b := fake.NewClientBuilder().WithScheme(scheme)
	for _, p := range policies {
		b = b.WithObjects(p)
	}
	return NewHandler(b.Build())
}

// testPolicy allows Secrets from "prod" to namespace "prod" on clusters
// labeled env=prod.
func testPolicy() *r8rv1alpha1.ReplicationPolicy {
	return &r8rv1alpha1.ReplicationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-secrets"},
		Spec: r8rv1alpha1.ReplicationPolicySpec{
			Sources: r8rv1alpha1.PolicySources{
				Namespaces: []string{"prod"},
				Kinds:      []string{"Secret"},
			},
			Targets: r8rv1alpha1.PolicyTargets{
				ClusterSelector: metav1.LabelSelector{MatchLabels: map[string]string{"env": "prod"}},
				Namespaces:      []string{"prod"},
			},
		},
	}
}

func requireDenied(t *testing.T, resp admission.Response, wantInMessage ...string) {
	t.Helper()
	if resp.Allowed {
		t.Fatalf("expected denial, got allowed: %+v", resp.Result)
	}
	msg := resp.Result.Message
	for _, want := range wantInMessage {
		if !strings.Contains(msg, want) {
			t.Errorf("denial message %q does not contain %q", msg, want)
		}
	}
	if strings.Contains(msg, secretPayload) {
		t.Errorf("denial message leaks secret payload: %q", msg)
	}
}

func requireAllowed(t *testing.T, resp admission.Response) {
	t.Helper()
	if !resp.Allowed {
		t.Fatalf("expected admit, got denial: %v", resp.Result)
	}
}

func TestNonAnnotatedObjectAdmitted(t *testing.T) {
	h := newHandler(t) // no policies: must still admit instantly
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "prod", nil))
	requireAllowed(t, resp)
}

func TestAnnotationRemovalAdmitted(t *testing.T) {
	// UPDATE whose incoming object carries no r8r.io annotations is admitted
	// even though the old object had them: cleanup is legitimate.
	h := newHandler(t)
	req := admissionRequest(t, admissionv1.Update, "Secret", "prod", nil)
	old := admissionRequest(t, admissionv1.Update, "Secret", "prod", map[string]string{
		annReplicate: "true", annTargetClusters: "env=prod",
	})
	req.OldObject = old.Object
	requireAllowed(t, h.Handle(context.Background(), req))
}

func TestMalformedSelectorDenied(t *testing.T) {
	h := newHandler(t, testPolicy())
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "prod",
		map[string]string{annReplicate: "true", annTargetClusters: "env==(bad"}))
	requireDenied(t, resp, annTargetClusters, "label selector")
}

func TestStarSelectorDenied(t *testing.T) {
	h := newHandler(t, testPolicy())
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "prod",
		map[string]string{annReplicate: "true", annTargetClusters: "*"}))
	requireDenied(t, resp, annTargetClusters, `"*" is not supported`)
}

func TestInvalidTargetNameDenied(t *testing.T) {
	h := newHandler(t, testPolicy())
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "prod",
		map[string]string{annReplicate: "true", annTargetName: "Not_A_Valid_Name"}))
	requireDenied(t, resp, annTargetName, "Not_A_Valid_Name")
}

func TestInvalidTargetNamespaceDenied(t *testing.T) {
	h := newHandler(t, testPolicy())
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "prod",
		map[string]string{annReplicate: "true", annTargetNamespaces: "prod,BAD NS"}))
	requireDenied(t, resp, annTargetNamespaces, "BAD NS")
}

func TestInvalidReplicateValueDenied(t *testing.T) {
	h := newHandler(t, testPolicy())
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "prod",
		map[string]string{annReplicate: "yes"}))
	requireDenied(t, resp, annReplicate, `"yes"`)
}

func TestReplicateFalseAdmitted(t *testing.T) {
	h := newHandler(t) // no policies, but not opted in -> admit
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "prod",
		map[string]string{annReplicate: "false"}))
	requireAllowed(t, resp)
}

func TestNoPoliciesDenied(t *testing.T) {
	h := newHandler(t)
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "prod",
		map[string]string{annReplicate: "true", annTargetClusters: "env=prod"}))
	requireDenied(t, resp, policy.DimensionNoPolicies)
}

func TestSourceNamespaceDenied(t *testing.T) {
	h := newHandler(t, testPolicy())
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "staging",
		map[string]string{annReplicate: "true", annTargetClusters: "env=prod"}))
	requireDenied(t, resp, policy.DimensionSourceNamespace, `"staging"`)
}

func TestSourceKindDenied(t *testing.T) {
	h := newHandler(t, testPolicy())
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "ConfigMap", "prod",
		map[string]string{annReplicate: "true", annTargetClusters: "env=prod"}))
	requireDenied(t, resp, policy.DimensionSourceKind, `"ConfigMap"`)
}

func TestTargetNamespaceDenied(t *testing.T) {
	h := newHandler(t, testPolicy())
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "prod",
		map[string]string{annReplicate: "true", annTargetClusters: "env=prod", annTargetNamespaces: "other"}))
	requireDenied(t, resp, policy.DimensionTargetNamespace, `"other"`)
}

// Spec scenario: "Disallowed request rejected at apply time" — a Secret
// annotated to target clusters no policy allows for its namespace is rejected
// with a message identifying the unmatched dimension.
func TestUnsatisfiableClusterSelectorDenied(t *testing.T) {
	h := newHandler(t, testPolicy())
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "prod",
		map[string]string{annReplicate: "true", annTargetClusters: "env=dev"}))
	requireDenied(t, resp, policy.DimensionTargetCluster, "env=dev")
}

// Spec scenario: "Allowed request passes" — annotations permitted by at least
// one policy are admitted.
func TestAllowedRequestAdmitted(t *testing.T) {
	h := newHandler(t, testPolicy())
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "prod",
		map[string]string{annReplicate: "true", annTargetClusters: "env=prod", annTargetNamespaces: "prod"}))
	requireAllowed(t, resp)
}

func TestTargetNamespaceDefaultsToSourceNamespace(t *testing.T) {
	// No target-namespaces annotation: defaults to the source namespace, which
	// the policy allows.
	h := newHandler(t, testPolicy())
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "prod",
		map[string]string{annReplicate: "true", annTargetClusters: "env=prod"}))
	requireAllowed(t, resp)
}

func TestUnknownKeyWarnsButAdmits(t *testing.T) {
	h := newHandler(t, testPolicy())
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "prod",
		map[string]string{annReplicate: "true", annTargetClusters: "env=prod", "r8r.io/tarrget-name": "typo"}))
	requireAllowed(t, resp)
	found := false
	for _, w := range resp.Warnings {
		if strings.Contains(w, "r8r.io/tarrget-name") {
			found = true
		}
		if strings.Contains(w, secretPayload) {
			t.Errorf("warning leaks secret payload: %q", w)
		}
	}
	if !found {
		t.Errorf("expected a warning naming the unknown key, got %v", resp.Warnings)
	}
}

// The new request-side key is part of the closed set the parser accepts, so a
// malformed value is denied at admission with a message naming the key
// (issue #34).
func TestInvalidConflictPolicyDenied(t *testing.T) {
	h := newHandler(t, testPolicy())
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "prod",
		map[string]string{annReplicate: "true", annTargetClusters: "env=prod", annConflictPolicy: "overwrite"}))
	requireDenied(t, resp, annConflictPolicy, "expected one of")
}

// A conflict policy no candidate policy grants is advisory only: the request
// still replicates, so it warns rather than denying.
func TestUnsatisfiableConflictPolicyWarnsButAdmits(t *testing.T) {
	h := newHandler(t, testPolicy()) // grants nothing beyond the Fail default
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "prod",
		map[string]string{
			annReplicate: "true", annTargetClusters: "env=prod",
			annTargetNamespaces: "prod", annConflictPolicy: "Overwrite",
		}))
	requireAllowed(t, resp)
	found := false
	for _, w := range resp.Warnings {
		if strings.Contains(w, annConflictPolicy) && strings.Contains(w, "Overwrite") {
			found = true
		}
		if strings.Contains(w, secretPayload) {
			t.Errorf("warning leaks secret payload: %q", w)
		}
	}
	if !found {
		t.Errorf("expected a warning about the ungranted conflict policy, got %v", resp.Warnings)
	}
}

// A granted conflict policy passes silently — no warning noise on the happy
// path, and Fail (the default) never warns either.
func TestGrantedConflictPolicyDoesNotWarn(t *testing.T) {
	p := testPolicy()
	p.Spec.Options.AllowedConflictPolicies = []r8rv1alpha1.ConflictPolicy{
		r8rv1alpha1.ConflictPolicyFail, r8rv1alpha1.ConflictPolicyOverwrite,
	}
	for _, value := range []string{"Overwrite", "Fail", ""} {
		ann := map[string]string{
			annReplicate: "true", annTargetClusters: "env=prod", annTargetNamespaces: "prod",
		}
		if value != "" {
			ann[annConflictPolicy] = value
		}
		resp := newHandler(t, p).Handle(context.Background(),
			admissionRequest(t, admissionv1.Create, "Secret", "prod", ann))
		requireAllowed(t, resp)
		for _, w := range resp.Warnings {
			if strings.Contains(w, annConflictPolicy) {
				t.Errorf("conflict policy %q warned unnecessarily: %q", value, w)
			}
		}
	}
}

func TestNamespaceSelectorPolicyFailsOpen(t *testing.T) {
	// A policy that allowlists source namespaces by selector cannot be checked
	// without namespace labels at admission time; it must count as matching.
	p := testPolicy()
	p.Spec.Sources.Namespaces = nil
	p.Spec.Sources.NamespaceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"tier": "prod"}}
	h := newHandler(t, p)
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "whatever",
		map[string]string{annReplicate: "true", annTargetClusters: "env=prod", annTargetNamespaces: "prod"}))
	requireAllowed(t, resp)
}

func TestNonSecretPayloadNeverInMessages(t *testing.T) {
	// Redundant belt-and-braces: even an allowed response must not echo data.
	h := newHandler(t, testPolicy())
	resp := h.Handle(context.Background(), admissionRequest(t, admissionv1.Create, "Secret", "prod",
		map[string]string{annReplicate: "true", annTargetClusters: "env=prod"}))
	if resp.Result != nil && strings.Contains(resp.Result.Message, secretPayload) {
		t.Errorf("response message leaks secret payload")
	}
}

// Spec scenario: "Ordinary secret traffic bypasses the webhook" — enforced by
// the CEL matchConditions in the webhook configuration; this test pins the
// manifest so the scoping cannot silently disappear. (The handler-level
// fast-path for non-annotated objects is covered by
// TestNonAnnotatedObjectAdmitted.)
func TestManifestScopesWebhookToAnnotatedObjects(t *testing.T) {
	data, err := os.ReadFile("../../config/webhook/manifests.yaml")
	if err != nil {
		t.Fatalf("read webhook manifest: %v", err)
	}
	manifest := string(data)
	for _, want := range []string{
		"matchConditions",
		"k.startsWith('r8r.io/')",
		"failurePolicy: Ignore",
		"sideEffects: None",
		Path,
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("config/webhook/manifests.yaml missing %q", want)
		}
	}
}

func TestJointlySatisfiable(t *testing.T) {
	parse := func(s string) labels.Selector {
		sel, err := labels.Parse(s)
		if err != nil {
			t.Fatalf("parse selector %q: %v", s, err)
		}
		return sel
	}
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"disjoint equality", "env=dev", "env=prod", false},
		{"same equality", "env=prod", "env=prod", true},
		{"in overlaps", "env in (dev,prod)", "env=prod", true},
		{"in disjoint", "env in (dev,qa)", "env=prod", false},
		{"required forbidden", "env=prod", "env!=prod", false},
		{"notin allows others", "env=prod", "env notin (dev)", true},
		{"exists vs not exists", "env", "!env", false},
		{"empty matches anything", "", "env=prod", true},
		{"different keys", "region=eu", "env=prod", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jointlySatisfiable(parse(tc.a), parse(tc.b)); got != tc.want {
				t.Errorf("jointlySatisfiable(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
