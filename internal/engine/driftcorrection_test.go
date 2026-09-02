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

// Coverage for the drift-correction signal (issue #30): a corrective write
// on a tampered replica must be distinguishable downstream from a no-op
// reconcile, via a `DriftCorrected` event and the
// k8s_r8r_drift_corrections_total counter.

import (
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
)

// driftCorrectionCount reads k8s_r8r_drift_corrections_total for one cluster
// (0 when the label pair has never been touched).
func driftCorrectionCount(t *testing.T, cluster string) float64 {
	t.Helper()
	fams, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}
	for _, mf := range fams {
		if mf.GetName() != "k8s_r8r_drift_corrections_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			if hasLabel(m, "cluster", cluster) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func hasLabel(m *dto.Metric, name, value string) bool {
	for _, lp := range m.GetLabel() {
		if lp.GetName() == name && lp.GetValue() == value {
			return true
		}
	}
	return false
}

// tamperReplicaPayload rewrites a replica's payload directly in the spoke
// store, leaving the engine's source-hash annotation stale — what an operator
// (or a second replicator) editing the object on the spoke looks like.
func tamperReplicaPayload(t *testing.T, f *testFixture, cluster, value string) {
	t.Helper()
	replica := f.replica(cluster, f.ns)
	if replica == nil {
		t.Fatalf("no replica on %s to tamper with", cluster)
		return
	}
	if err := unstructured.SetNestedField(replica.Object, value, "data", "password"); err != nil {
		t.Fatalf("tampering replica: %v", err)
	}
	f.transport.put(cluster, replica)
}

// Issue #30 / observability-operations "Drift event on a Secret": a replica
// whose content was rewritten on the spoke is restored *and* reported — one
// Warning event naming the replica and both hashes, plus one increment of the
// correction counter for that cluster.
func TestReconcile_DriftCorrectionEmitsEventAndMetric(t *testing.T) {
	const cluster = "drift-payload-spoke"
	f := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: cluster}}, cluster)
	f.policy(nil, nil)

	_, rep := f.reconcile()
	f.drainEvents()
	before := driftCorrectionCount(t, cluster)

	tamperReplicaPayload(t, f, cluster, b64("tampered"))
	_, _ = f.reconcile()

	if got := replicaPassword(t, f.replica(cluster, f.ns)); got != b64("hunter2") {
		t.Errorf("replica not restored: %q", got)
	}
	if got := driftCorrectionCount(t, cluster); got != before+1 {
		t.Errorf("drift corrections = %v, want %v", got, before+1)
	}

	evs := f.drainEvents()
	if !eventsContaining(evs, "DriftCorrected") {
		t.Fatalf("no DriftCorrected event: %v", evs)
	}
	var msg string
	for _, e := range evs {
		if strings.Contains(e, "DriftCorrected") {
			msg = e
		}
	}
	if !strings.Contains(msg, "Warning") {
		t.Errorf("DriftCorrected is not a Warning: %q", msg)
	}
	if !strings.Contains(msg, replicaRef(cluster, f.ns, "web-creds")) {
		t.Errorf("event does not name the replica: %q", msg)
	}
	if !strings.Contains(msg, rep.Status.SourceHash) {
		t.Errorf("event does not carry the expected source hash: %q", msg)
	}
	if strings.Count(msg, "sha256:") != 2 {
		t.Errorf("event should carry both the observed and the expected hash: %q", msg)
	}
	// Secret-safe telemetry: hashes only, never the payload that diverged.
	for _, leak := range []string{"hunter2", "tampered", b64("hunter2"), b64("tampered")} {
		if strings.Contains(msg, leak) {
			t.Errorf("event leaks payload %q: %q", leak, msg)
		}
	}
}

// A stale source-hash annotation over unchanged content is a bookkeeping
// repair, not drift: it is what a change to the hashing rules produces
// fleet-wide on upgrade. The engine still rewrites the annotation, but must
// not raise a tamper signal for it.
func TestReconcile_AnnotationOnlyRepairIsSilent(t *testing.T) {
	const cluster = "drift-annotation-spoke"
	f := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: cluster}}, cluster)
	f.policy(nil, nil)

	_, rep := f.reconcile()
	f.drainEvents()
	before := driftCorrectionCount(t, cluster)

	replica := f.replica(cluster, f.ns)
	annotations := replica.GetAnnotations()
	annotations[AnnotationSourceHash] = "sha256:" + strings.Repeat("0", 64)
	replica.SetAnnotations(annotations)
	f.transport.put(cluster, replica)

	_, _ = f.reconcile()

	repaired := f.replica(cluster, f.ns)
	if got := repaired.GetAnnotations()[AnnotationSourceHash]; got != rep.Status.SourceHash {
		t.Errorf("annotation not repaired: %q, want %q", got, rep.Status.SourceHash)
	}
	if got := replicaPassword(t, repaired); got != b64("hunter2") {
		t.Errorf("content changed by a metadata-only repair: %q", got)
	}
	if got := driftCorrectionCount(t, cluster); got != before {
		t.Errorf("annotation-only repair counted as a drift correction: %v, want %v", got, before)
	}
	if evs := f.drainEvents(); eventsContaining(evs, "DriftCorrected") {
		t.Errorf("annotation-only repair emitted a DriftCorrected event: %v", evs)
	}
}

// Documented consequence of the event limiter (observability-operations,
// "Flapping target"): recurring drift with identical hashes coalesces into a
// single event, so "drift recurs on this spoke" has to be read off the
// counter, which is not rate-limited.
func TestReconcile_RecurringDriftCoalescesEventsButNotMetric(t *testing.T) {
	const cluster = "drift-recurring-spoke"
	f := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: cluster}}, cluster)
	f.policy(nil, nil)

	f.reconcile()
	f.drainEvents()
	before := driftCorrectionCount(t, cluster)

	events := 0
	for range 3 {
		tamperReplicaPayload(t, f, cluster, b64("tampered"))
		f.reconcile()
		for _, e := range f.drainEvents() {
			if strings.Contains(e, "DriftCorrected") {
				events++
			}
		}
	}

	if events != 1 {
		t.Errorf("DriftCorrected events = %d, want 1 (coalesced by the limiter)", events)
	}
	if got := driftCorrectionCount(t, cluster); got != before+3 {
		t.Errorf("drift corrections = %v, want %v (the counter is not rate-limited)", got, before+3)
	}
}
