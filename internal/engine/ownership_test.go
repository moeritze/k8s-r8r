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

// Coverage for replica ownership verification (issue #36): the drift watch's
// label selector is `app.kubernetes.io/managed-by: k8s-r8r`, so a replica
// whose label is rewritten leaves the informer entirely. The reconcile's own
// live reads must recognise it as ours, repair it, and never lose track of it.

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
	"github.com/moeritze/k8s-r8r/internal/telemetry"
)

// ownershipLostCount reads k8s_r8r_replica_ownership_lost_total for one
// (cluster, action) pair (0 when it has never been touched).
func ownershipLostCount(t *testing.T, cluster, action string) float64 {
	t.Helper()
	fams, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}
	for _, mf := range fams {
		if mf.GetName() != "k8s_r8r_replica_ownership_lost_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			if hasLabel(m, "cluster", cluster) && hasLabel(m, "action", action) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// stripManagedBy removes the managed-by label from a replica in the spoke
// store — what a GitOps tool stamping its own ownership labels, or an
// operator tidying labels by hand, does to a replica.
func stripManagedBy(t *testing.T, f *testFixture, cluster string) {
	t.Helper()
	replica := f.replica(cluster, f.ns)
	if replica == nil {
		t.Fatalf("no replica on %s to strip", cluster)
		return
	}
	labels := replica.GetLabels()
	delete(labels, LabelManagedBy)
	replica.SetLabels(labels)
	f.transport.put(cluster, replica)
}

// putForeignObject replaces whatever is at the replica's name with an object
// carrying none of this replication's ownership marks.
func putForeignObject(f *testFixture, cluster string) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(secretGVK)
	obj.SetNamespace(f.ns)
	obj.SetName("web-creds")
	obj.SetLabels(map[string]string{LabelManagedBy: "argocd"})
	obj.Object["data"] = map[string]any{"password": b64("somebody-elses")}
	f.transport.put(cluster, obj)
}

// deselectAllTargets empties spec.resolvedTargets, which sends every
// inventory entry through the GC path.
func deselectAllTargets(t *testing.T, f *testFixture) {
	t.Helper()
	fresh := &r8rv1alpha1.Replication{}
	if err := hubClient.Get(testCtx, client.ObjectKey{Namespace: f.ns, Name: "rep"}, fresh); err != nil {
		t.Fatal(err)
	}
	fresh.Spec.ResolvedTargets = nil
	if err := hubClient.Update(testCtx, fresh); err != nil {
		t.Fatal(err)
	}
}

// The classification is what separates "our replica, relabelled" from "not
// ours". IsManagedReplica cannot express the middle row, which is why the
// engine used to file a stripped replica as a foreign conflict.
func TestClassifyReplicaOwnership(t *testing.T) {
	const uid = types.UID("11111111-2222-3333-4444-555555555555")
	for _, tc := range []struct {
		name   string
		labels map[string]string
		want   ReplicaOwnership
	}{
		{
			name:   "both marks present",
			labels: map[string]string{LabelManagedBy: ManagedByValue, LabelSourceUID: string(uid)},
			want:   OwnershipManaged,
		},
		{
			name:   "managed-by removed",
			labels: map[string]string{LabelSourceUID: string(uid)},
			want:   OwnershipStripped,
		},
		{
			name:   "managed-by rewritten by another controller",
			labels: map[string]string{LabelManagedBy: "argocd", LabelSourceUID: string(uid)},
			want:   OwnershipStripped,
		},
		{
			name:   "source-uid of a different source",
			labels: map[string]string{LabelManagedBy: ManagedByValue, LabelSourceUID: "some-other-uid"},
			want:   OwnershipForeign,
		},
		{
			name:   "no labels at all",
			labels: nil,
			want:   OwnershipForeign,
		},
		{
			name:   "source-uid removed leaves nothing to key recovery on",
			labels: map[string]string{LabelManagedBy: ManagedByValue},
			want:   OwnershipForeign,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyReplicaOwnership(tc.labels, uid); got != tc.want {
				t.Errorf("ClassifyReplicaOwnership() = %v, want %v", got, tc.want)
			}
		})
	}
}

// An empty source UID must never make an unlabelled object look like ours.
func TestClassifyReplicaOwnership_EmptySourceUIDIsNeverOurs(t *testing.T) {
	if got := ClassifyReplicaOwnership(map[string]string{}, ""); got != OwnershipForeign {
		t.Errorf("empty UID against empty labels = %v, want OwnershipForeign", got)
	}
	if got := ClassifyReplicaOwnership(map[string]string{LabelSourceUID: ""}, ""); got != OwnershipForeign {
		t.Errorf("empty UID against empty source-uid label = %v, want OwnershipForeign", got)
	}
}

// Issue #36, the core case: a replica whose managed-by label was rewritten is
// invisible to the drift watch. The reconcile's live read must recognise it
// as ours, restore the label (which returns it to the cache's selector), keep
// its inventory entry, and report the repair — not file it as a conflict and
// drop the entry.
func TestReconcile_StrippedOwnershipLabelIsRepairedAndReported(t *testing.T) {
	const cluster = "ownership-repair-spoke"
	f := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: cluster}}, cluster)
	f.policy(nil, nil)

	f.reconcile()
	f.drainEvents()
	before := ownershipLostCount(t, cluster, telemetry.OwnershipRepaired)
	beforeDrift := driftCorrectionCount(t, cluster)

	stripManagedBy(t, f, cluster)
	_, rep := f.reconcile()

	repaired := f.replica(cluster, f.ns)
	if repaired == nil {
		t.Fatal("replica gone after repair")
	}
	if got := repaired.GetLabels()[LabelManagedBy]; got != ManagedByValue {
		t.Errorf("managed-by label not restored: %q", got)
	}
	if got := ClassifyReplicaOwnership(repaired.GetLabels(), f.secret.UID); got != OwnershipManaged {
		t.Errorf("replica not back under management: %v", got)
	}
	if len(rep.Status.Inventory) != 1 {
		t.Fatalf("inventory entry lost for a replica the engine created: %+v", rep.Status.Inventory)
	}
	if got := ownershipLostCount(t, cluster, telemetry.OwnershipRepaired); got != before+1 {
		t.Errorf("ownership repairs = %v, want %v", got, before+1)
	}
	// Content never diverged, so the drift-correction counter's contract
	// ("a non-zero value means the payload was actually wrong") holds.
	if got := driftCorrectionCount(t, cluster); got != beforeDrift {
		t.Errorf("a metadata-only ownership repair moved the drift counter: %v, want %v", got, beforeDrift)
	}

	evs := f.drainEvents()
	if eventsContaining(evs, r8rv1alpha1.ReasonConflict) {
		t.Errorf("our own relabelled replica was reported as a conflict: %v", evs)
	}
	var msg string
	for _, e := range evs {
		if strings.Contains(e, ReasonOwnershipRepaired) {
			msg = e
		}
	}
	if msg == "" {
		t.Fatalf("no %s event: %v", ReasonOwnershipRepaired, evs)
	}
	if !strings.Contains(msg, "Warning") {
		t.Errorf("%s is not a Warning: %q", ReasonOwnershipRepaired, msg)
	}
	if !strings.Contains(msg, replicaRef(cluster, f.ns, "web-creds")) {
		t.Errorf("event does not name the replica: %q", msg)
	}
	// The operator's next action is finding what writes the label, so the
	// event has to name it.
	if !strings.Contains(msg, LabelManagedBy) {
		t.Errorf("event does not name the rewritten label: %q", msg)
	}
	for _, leak := range []string{"hunter2", b64("hunter2")} {
		if strings.Contains(msg, leak) {
			t.Errorf("event leaks payload %q: %q", leak, msg)
		}
	}
}

// Ready must be truthful after the repair: the replica really does match the
// source again.
func TestReconcile_RepairedReplicaStaysReady(t *testing.T) {
	const cluster = "ownership-ready-spoke"
	f := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: cluster}}, cluster)
	f.policy(nil, nil)
	f.reconcile()

	stripManagedBy(t, f, cluster)
	_, rep := f.reconcile()

	cond := readyCondition(rep)
	if cond == nil || cond.Status != "True" {
		t.Fatalf("Ready condition after repair = %+v, want True", cond)
	}
	if rep.Status.Summary.ReadyTargets != 1 || rep.Status.Summary.FailedTargets != 0 {
		t.Errorf("summary after repair = %+v", rep.Status.Summary)
	}
}

// The two signals compose: a replica that lost its label AND had its payload
// rewritten reports both, so neither masks the other. That pair is the
// strongest tampering evidence the operator gets.
func TestReconcile_StrippedLabelWithDivergedContentReportsBoth(t *testing.T) {
	const cluster = "ownership-drift-spoke"
	f := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: cluster}}, cluster)
	f.policy(nil, nil)

	f.reconcile()
	f.drainEvents()
	beforeOwn := ownershipLostCount(t, cluster, telemetry.OwnershipRepaired)
	beforeDrift := driftCorrectionCount(t, cluster)

	tamperReplicaPayload(t, f, cluster, b64("tampered"))
	stripManagedBy(t, f, cluster)
	f.reconcile()

	if got := replicaPassword(t, f.replica(cluster, f.ns)); got != b64("hunter2") {
		t.Errorf("content not restored: %q", got)
	}
	if got := ownershipLostCount(t, cluster, telemetry.OwnershipRepaired); got != beforeOwn+1 {
		t.Errorf("ownership repairs = %v, want %v", got, beforeOwn+1)
	}
	if got := driftCorrectionCount(t, cluster); got != beforeDrift+1 {
		t.Errorf("drift corrections = %v, want %v", got, beforeDrift+1)
	}

	evs := f.drainEvents()
	if !eventsContaining(evs, ReasonOwnershipRepaired) {
		t.Errorf("no %s event: %v", ReasonOwnershipRepaired, evs)
	}
	if !eventsContaining(evs, "DriftCorrected") {
		t.Errorf("no DriftCorrected event: %v", evs)
	}
	for _, e := range evs {
		for _, leak := range []string{"hunter2", "tampered", b64("hunter2"), b64("tampered")} {
			if strings.Contains(e, leak) {
				t.Errorf("event leaks payload %q: %q", leak, e)
			}
		}
	}
}

// Regression guard for the two-key conflict contract (#34): ownership
// verification must not become a back door. An object with neither mark is
// still a foreign object, and the default effective conflict policy still
// refuses to touch it.
func TestReconcile_ForeignObjectAtReplicaNameStillConflicts(t *testing.T) {
	const cluster = "ownership-foreign-spoke"
	f := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: cluster}}, cluster)
	f.policy(nil, nil)
	f.reconcile()
	f.drainEvents()

	putForeignObject(f, cluster)
	_, rep := f.reconcile()

	if got := replicaPassword(t, f.replica(cluster, f.ns)); got != b64("somebody-elses") {
		t.Errorf("foreign object was overwritten: %q", got)
	}
	if eventsContaining(f.drainEvents(), ReasonOwnershipRepaired) {
		t.Error("a foreign object was repaired as if it were ours")
	}
	if len(rep.Status.NonReadyTargets) != 1 || rep.Status.NonReadyTargets[0].Reason != r8rv1alpha1.ReasonConflict {
		t.Errorf("non-ready targets = %+v, want a Conflict", rep.Status.NonReadyTargets)
	}
}

// GC of a replica whose ownership label was rewritten: it is still a copy of
// the source that this Replication created, so cleanup must remove it.
// Abandoning it is what strands credential material on a spoke.
func TestReconcile_GCDeletesReplicaWhoseOwnershipLabelWasStripped(t *testing.T) {
	const cluster = "ownership-gc-spoke"
	f := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: cluster}}, cluster)
	f.policy(nil, nil)
	f.reconcile()
	f.drainEvents()
	before := ownershipLostCount(t, cluster, telemetry.OwnershipDeleted)

	stripManagedBy(t, f, cluster)
	deselectAllTargets(t, f)
	_, rep := f.reconcile()

	if f.replica(cluster, f.ns) != nil {
		t.Fatal("relabelled replica survived garbage collection")
	}
	if len(rep.Status.Inventory) != 0 {
		t.Errorf("inventory = %+v, want empty", rep.Status.Inventory)
	}
	if got := ownershipLostCount(t, cluster, telemetry.OwnershipDeleted); got != before+1 {
		t.Errorf("ownership deletions = %v, want %v", got, before+1)
	}
	if !eventsContaining(f.drainEvents(), ReasonOwnershipStripped) {
		t.Error("deleting a relabelled replica was not reported")
	}
}

// The same on the finalizer path: deleting the Replication must not leave a
// relabelled replica behind on the spoke.
func TestReconcile_DeletionCleansRelabelledReplica(t *testing.T) {
	const cluster = "ownership-finalizer-spoke"
	f := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: cluster}}, cluster)
	f.policy(nil, nil)
	f.reconcile()

	stripManagedBy(t, f, cluster)

	fresh := &r8rv1alpha1.Replication{}
	if err := hubClient.Get(testCtx, client.ObjectKey{Namespace: f.ns, Name: "rep"}, fresh); err != nil {
		t.Fatal(err)
	}
	if err := hubClient.Delete(testCtx, fresh); err != nil {
		t.Fatal(err)
	}

	_, rep := f.reconcile()
	if rep != nil {
		t.Fatalf("replication still held by the finalizer: %+v", rep.Status.Inventory)
	}
	if f.replica(cluster, f.ns) != nil {
		t.Fatal("relabelled replica stranded on the spoke after Replication deletion")
	}
}

// An inventory entry pointing at an object the engine cannot recognise is
// still not deleted — but it is no longer dropped in silence, which is what
// "no code path may lose track of a created replica" requires.
func TestReconcile_GCReleasesForeignObjectWithASignal(t *testing.T) {
	const cluster = "ownership-orphan-spoke"
	f := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: cluster}}, cluster)
	f.policy(nil, nil)
	f.reconcile()
	f.drainEvents()
	before := ownershipLostCount(t, cluster, telemetry.OwnershipOrphaned)

	putForeignObject(f, cluster)
	deselectAllTargets(t, f)
	_, rep := f.reconcile()

	if f.replica(cluster, f.ns) == nil {
		t.Fatal("a foreign object was deleted by garbage collection")
	}
	if len(rep.Status.Inventory) != 0 {
		t.Errorf("inventory = %+v, want the entry released", rep.Status.Inventory)
	}
	if got := ownershipLostCount(t, cluster, telemetry.OwnershipOrphaned); got != before+1 {
		t.Errorf("orphan releases = %v, want %v", got, before+1)
	}

	var msg string
	for _, e := range f.drainEvents() {
		if strings.Contains(e, ReasonReplicaOrphaned) {
			msg = e
		}
	}
	if msg == "" {
		t.Fatal("releasing an unrecognisable inventory entry produced no event")
	}
	if !strings.Contains(msg, "Warning") {
		t.Errorf("%s is not a Warning: %q", ReasonReplicaOrphaned, msg)
	}
	if !strings.Contains(msg, replicaRef(cluster, f.ns, "web-creds")) {
		t.Errorf("event does not name the replica: %q", msg)
	}
	if !strings.Contains(msg, "manual cleanup") {
		t.Errorf("event does not say manual cleanup may be needed: %q", msg)
	}
}
