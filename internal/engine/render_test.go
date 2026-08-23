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
