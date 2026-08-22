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

// Reconciler tests: envtest hub + fake spoke transport. Spec scenario
// coverage is annotated on each test.

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func twoClusterTargets() []r8rv1alpha1.ResolvedTarget {
	return []r8rv1alpha1.ResolvedTarget{
		{ClusterName: "spoke-a"},
		{ClusterName: "spoke-b"},
	}
}

// replicaPassword extracts data.password from a replica.
func replicaPassword(t *testing.T, obj *unstructured.Unstructured) string {
	t.Helper()
	if obj == nil {
		t.Fatal("replica does not exist")
	}
	v, found, err := unstructured.NestedString(obj.Object, "data", "password")
	if err != nil || !found {
		t.Fatalf("replica has no data.password (found=%v err=%v)", found, err)
	}
	return v
}

// Spec (replication-engine) "Replica creation and identity" +
// (replication-request) "Healthy fanout has compact status": full fanout to
// two clusters with identity labels, hash annotation, inventory, Ready=True.
func TestReconcile_HappyPathFanout(t *testing.T) {
	f := newFixture(t, "hunter2", twoClusterTargets(), "spoke-a", "spoke-b")
	f.policy(nil, nil)

	res, rep := f.reconcile()

	for _, c := range []string{"spoke-a", "spoke-b"} {
		replica := f.replica(c, f.ns)
		if replica == nil {
			t.Fatalf("no replica on %s", c)
		}
		labels := replica.GetLabels()
		if labels[LabelManagedBy] != ManagedByValue ||
			labels[LabelSourceUID] != string(f.secret.UID) ||
			labels[LabelSourceNamespace] != f.ns ||
			labels[LabelSourceName] != "web-creds" ||
			labels[LabelSourceKind] != "Secret" ||
			labels[LabelSourceCluster] != "hub" {
			t.Errorf("replica on %s misses identity labels: %v", c, labels)
		}
		if replica.GetAnnotations()[AnnotationSourceHash] != rep.Status.SourceHash {
			t.Errorf("replica hash annotation != status.sourceHash on %s", c)
		}
		if got := replicaPassword(t, replica); got != b64("hunter2") {
			t.Errorf("replica payload on %s = %q", c, got)
		}
	}

	s := rep.Status.Summary
	if s.DesiredTargets != 2 || s.ReadyTargets != 2 || s.FailedTargets != 0 {
		t.Errorf("summary = %+v, want 2/2/0", s)
	}
	if len(rep.Status.NonReadyTargets) != 0 {
		t.Errorf("healthy fanout has non-ready detail: %+v", rep.Status.NonReadyTargets)
	}
	if cond := readyCondition(rep); cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != ReasonAllTargetsReady {
		t.Errorf("Ready condition = %+v", cond)
	}
	if len(rep.Status.Inventory) != 2 {
		t.Fatalf("inventory has %d entries, want 2", len(rep.Status.Inventory))
	}
	for _, e := range rep.Status.Inventory {
		if e.LastAppliedHash != rep.Status.SourceHash {
			t.Errorf("inventory entry %+v hash != sourceHash", e)
		}
	}
	if !strings.HasPrefix(rep.Status.SourceHash, "sha256:") {
		t.Errorf("sourceHash = %q", rep.Status.SourceHash)
	}
	if len(rep.Finalizers) == 0 || rep.Finalizers[0] != FinalizerName {
		t.Errorf("engine finalizer missing: %v", rep.Finalizers)
	}
	if res.RequeueAfter != defaultDriftResync {
		t.Errorf("healthy requeue = %v, want resync fallback %v", res.RequeueAfter, defaultDriftResync)
	}
}

// Spec "Source updated": payload change propagates to all replicas and their
// hash annotations.
func TestReconcile_SourceUpdatePropagates(t *testing.T) {
	f := newFixture(t, "hunter2", twoClusterTargets(), "spoke-a", "spoke-b")
	f.policy(nil, nil)
	_, before := f.reconcile()

	f.updateSecret("changed!")
	_, after := f.reconcile()

	if after.Status.SourceHash == before.Status.SourceHash {
		t.Fatal("sourceHash did not change after payload update")
	}
	for _, c := range []string{"spoke-a", "spoke-b"} {
		replica := f.replica(c, f.ns)
		if got := replicaPassword(t, replica); got != b64("changed!") {
			t.Errorf("replica on %s not updated: %q", c, got)
		}
		if replica.GetAnnotations()[AnnotationSourceHash] != after.Status.SourceHash {
			t.Errorf("replica hash annotation on %s not updated", c)
		}
	}
	for _, e := range after.Status.Inventory {
		if e.LastAppliedHash != after.Status.SourceHash {
			t.Errorf("inventory hash not updated: %+v", e)
		}
	}
}

// Spec "Source is never modified": the engine performs no writes on the
// source object (its only finalizer lives on the Replication).
func TestReconcile_SourceNeverModified(t *testing.T) {
	f := newFixture(t, "hunter2", twoClusterTargets(), "spoke-a", "spoke-b")
	f.policy(nil, nil)

	before := &corev1.Secret{}
	if err := hubClient.Get(testCtx, client.ObjectKeyFromObject(f.secret), before); err != nil {
		t.Fatal(err)
	}
	f.reconcile()
	f.reconcile()
	after := &corev1.Secret{}
	if err := hubClient.Get(testCtx, client.ObjectKeyFromObject(f.secret), after); err != nil {
		t.Fatal(err)
	}
	if before.ResourceVersion != after.ResourceVersion {
		t.Errorf("engine wrote the source: rv %s -> %s", before.ResourceVersion, after.ResourceVersion)
	}
	if len(after.Finalizers) != 0 {
		t.Errorf("engine put a finalizer on the source: %v", after.Finalizers)
	}
}

// Spec "Unmanaged object with default policy" (conflict Fail): existing
// object untouched, per-target Conflict reported, no inventory entry.
func TestReconcile_ConflictFailDefault(t *testing.T) {
	f := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: "spoke-a"}}, "spoke-a")
	f.policy(nil, nil) // default options: conflicts Fail

	preexisting := unmanagedObject(b64("victim-data"))
	preexisting.SetNamespace(f.ns)
	f.transport.put("spoke-a", preexisting)

	_, rep := f.reconcile()

	replica := f.replica("spoke-a", f.ns)
	if got := replicaPassword(t, replica); got != b64("victim-data") {
		t.Errorf("existing object was touched: %q", got)
	}
	if replica.GetLabels()[LabelManagedBy] == ManagedByValue {
		t.Error("existing object was labeled")
	}
	if rep.Status.Summary.FailedTargets != 1 {
		t.Errorf("summary = %+v", rep.Status.Summary)
	}
	if len(rep.Status.NonReadyTargets) != 1 || rep.Status.NonReadyTargets[0].Reason != r8rv1alpha1.ReasonConflict {
		t.Errorf("nonReadyTargets = %+v, want one Conflict entry", rep.Status.NonReadyTargets)
	}
	if cond := readyCondition(rep); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != r8rv1alpha1.ReasonConflict {
		t.Errorf("Ready condition = %+v", cond)
	}
	if len(rep.Status.Inventory) != 0 {
		t.Errorf("conflicted slot must not stay in inventory: %+v", rep.Status.Inventory)
	}
	if !eventsContaining(f.drainEvents(), r8rv1alpha1.ReasonConflict) {
		t.Error("no Conflict event emitted")
	}
}

// Spec "Adopt on identical content": labels + hash annotation added, payload
// untouched.
func TestReconcile_ConflictAdopt(t *testing.T) {
	f := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: "spoke-a"}}, "spoke-a")
	f.policy(nil, func(p *r8rv1alpha1.ReplicationPolicy) {
		p.Spec.Options.AllowedConflictPolicies = []r8rv1alpha1.ConflictPolicy{
			r8rv1alpha1.ConflictPolicyFail, r8rv1alpha1.ConflictPolicyAdopt,
		}
	})

	// Identical content, plus a marker field proving no payload rewrite.
	existing := unmanagedObject(b64("hunter2"))
	existing.SetNamespace(f.ns)
	f.transport.put("spoke-a", existing)

	_, rep := f.reconcile()

	replica := f.replica("spoke-a", f.ns)
	if replica.GetLabels()[LabelManagedBy] != ManagedByValue ||
		replica.GetLabels()[LabelSourceUID] != string(f.secret.UID) {
		t.Errorf("adopted object lacks ownership labels: %v", replica.GetLabels())
	}
	if replica.GetAnnotations()[AnnotationSourceHash] != rep.Status.SourceHash {
		t.Error("adopted object lacks hash annotation")
	}
	if got := replicaPassword(t, replica); got != b64("hunter2") {
		t.Errorf("adoption rewrote the payload: %q", got)
	}
	if rep.Status.Summary.ReadyTargets != 1 || len(rep.Status.Inventory) != 1 {
		t.Errorf("adopted target not ready/inventoried: %+v %+v", rep.Status.Summary, rep.Status.Inventory)
	}
	// Adopt with DIFFERING content must fail instead (covered by the
	// decision table too; here end-to-end).
	f2 := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: "spoke-a"}}, "spoke-a")
	f2.policy(nil, func(p *r8rv1alpha1.ReplicationPolicy) {
		p.Spec.Options.AllowedConflictPolicies = []r8rv1alpha1.ConflictPolicy{
			r8rv1alpha1.ConflictPolicyFail, r8rv1alpha1.ConflictPolicyAdopt,
		}
	})
	other := unmanagedObject(b64("different"))
	other.SetNamespace(f2.ns)
	f2.transport.put("spoke-a", other)
	_, rep2 := f2.reconcile()
	if rep2.Status.Summary.FailedTargets != 1 || rep2.Status.NonReadyTargets[0].Reason != r8rv1alpha1.ReasonConflict {
		t.Errorf("adopt on differing content should conflict: %+v", rep2.Status)
	}
	if got := replicaPassword(t, f2.replica("spoke-a", f2.ns)); got != b64("different") {
		t.Error("differing object was modified under Adopt")
	}
}

// Design D7 Overwrite: policy-granted takeover replaces the payload.
func TestReconcile_ConflictOverwrite(t *testing.T) {
	f := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: "spoke-a"}}, "spoke-a")
	f.policy(nil, func(p *r8rv1alpha1.ReplicationPolicy) {
		p.Spec.Options.AllowedConflictPolicies = []r8rv1alpha1.ConflictPolicy{
			r8rv1alpha1.ConflictPolicyFail, r8rv1alpha1.ConflictPolicyOverwrite,
		}
	})
	existing := unmanagedObject(b64("victim-data"))
	existing.SetNamespace(f.ns)
	f.transport.put("spoke-a", existing)

	_, rep := f.reconcile()

	replica := f.replica("spoke-a", f.ns)
	if got := replicaPassword(t, replica); got != b64("hunter2") {
		t.Errorf("overwrite did not replace payload: %q", got)
	}
	if replica.GetLabels()[LabelManagedBy] != ManagedByValue {
		t.Error("overwritten object not labeled")
	}
	if rep.Status.Summary.ReadyTargets != 1 {
		t.Errorf("summary = %+v", rep.Status.Summary)
	}
	if !eventsContaining(f.drainEvents(), "ConflictOverwritten") {
		t.Error("no ConflictOverwritten event")
	}
}

// Spec "Missing namespace with creation allowed": namespace created with
// managed-by label and the replica placed in it.
func TestReconcile_NamespaceCreatedWhenAllowed(t *testing.T) {
	f := newFixture(t, "hunter2",
		[]r8rv1alpha1.ResolvedTarget{{ClusterName: "spoke-a", Namespaces: []string{"fresh-ns"}}}, "spoke-a")
	f.policy([]string{"fresh-ns"}, func(p *r8rv1alpha1.ReplicationPolicy) {
		p.Spec.Options.AllowNamespaceCreation = true
	})

	_, rep := f.reconcile()

	ns := f.transport.object("spoke-a", namespaceGVK, "", "fresh-ns")
	if ns == nil {
		t.Fatal("namespace was not created")
	}
	if ns.GetLabels()[LabelManagedBy] != ManagedByValue {
		t.Error("created namespace lacks managed-by label")
	}
	if f.replica("spoke-a", "fresh-ns") == nil {
		t.Fatal("replica not placed in created namespace")
	}
	if rep.Status.Summary.ReadyTargets != 1 {
		t.Errorf("summary = %+v", rep.Status.Summary)
	}
}

// Policy spec "Namespace creation denied by default": per-target
// NamespaceMissing failure, nothing created.
func TestReconcile_NamespaceMissingDenied(t *testing.T) {
	f := newFixture(t, "hunter2",
		[]r8rv1alpha1.ResolvedTarget{{ClusterName: "spoke-a", Namespaces: []string{"fresh-ns"}}}, "spoke-a")
	f.policy([]string{"fresh-ns"}, nil) // allowNamespaceCreation defaults false

	res, rep := f.reconcile()

	if f.transport.object("spoke-a", namespaceGVK, "", "fresh-ns") != nil {
		t.Fatal("namespace was created although not allowed")
	}
	if len(rep.Status.NonReadyTargets) != 1 || rep.Status.NonReadyTargets[0].Reason != ReasonNamespaceMissing {
		t.Errorf("nonReadyTargets = %+v, want NamespaceMissing", rep.Status.NonReadyTargets)
	}
	if len(rep.Status.Inventory) != 0 {
		t.Errorf("nothing was created, inventory must be empty: %+v", rep.Status.Inventory)
	}
	if res.RequeueAfter <= 0 || res.RequeueAfter > time.Second {
		t.Errorf("expected backoff requeue, got %v", res.RequeueAfter)
	}
}

// Policy spec "No policies exist" (default deny) on the engine side: nothing
// replicated, PolicyDenied reported per target.
func TestReconcile_DefaultDeny(t *testing.T) {
	f := newFixture(t, "hunter2", twoClusterTargets(), "spoke-a", "spoke-b")
	// no policy created

	_, rep := f.reconcile()

	for _, c := range []string{"spoke-a", "spoke-b"} {
		if f.replica(c, f.ns) != nil {
			t.Fatalf("replica created on %s despite default deny", c)
		}
	}
	if rep.Status.Summary.FailedTargets != 2 || rep.Status.Summary.ReadyTargets != 0 {
		t.Errorf("summary = %+v", rep.Status.Summary)
	}
	for _, nrt := range rep.Status.NonReadyTargets {
		if nrt.Reason != r8rv1alpha1.ReasonPolicyDenied {
			t.Errorf("reason = %q, want PolicyDenied", nrt.Reason)
		}
	}
	if cond := readyCondition(rep); cond == nil || cond.Reason != r8rv1alpha1.ReasonPolicyDenied {
		t.Errorf("Ready condition = %+v", cond)
	}
	if len(rep.Status.Inventory) != 0 {
		t.Errorf("inventory not empty: %+v", rep.Status.Inventory)
	}
}

// Policy spec "Policy tightened after replication" + task 3.3: revocation
// with effective policy Delete removes replicas and inventory entries.
func TestReconcile_RevocationDelete(t *testing.T) {
	f := newFixture(t, "hunter2", twoClusterTargets(), "spoke-a", "spoke-b")
	pol := f.policy(nil, nil) // revocationPolicy defaults to Delete
	f.reconcile()

	// Tighten: target namespace no longer allowlisted.
	fresh := &r8rv1alpha1.ReplicationPolicy{}
	if err := hubClient.Get(testCtx, client.ObjectKeyFromObject(pol), fresh); err != nil {
		t.Fatal(err)
	}
	fresh.Spec.Targets.Namespaces = []string{"somewhere-else"}
	if err := hubClient.Update(testCtx, fresh); err != nil {
		t.Fatal(err)
	}

	_, rep := f.reconcile()

	for _, c := range []string{"spoke-a", "spoke-b"} {
		if f.replica(c, f.ns) != nil {
			t.Fatalf("revoked replica still present on %s", c)
		}
	}
	if len(rep.Status.Inventory) != 0 {
		t.Errorf("inventory not cleaned after revocation: %+v", rep.Status.Inventory)
	}
	if !eventsContaining(f.drainEvents(), r8rv1alpha1.ReasonPolicyRevoked) {
		t.Error("no PolicyRevoked event emitted")
	}
}

// Task 3.3 Retain: replicas stay, updates stop, PolicyRevoked condition set;
// retention survives further reconciles AND an operator restart (fresh
// reconciler with empty in-memory state).
func TestReconcile_RevocationRetain(t *testing.T) {
	f := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: "spoke-a"}}, "spoke-a")
	pol := f.policy(nil, func(p *r8rv1alpha1.ReplicationPolicy) {
		p.Spec.Options.RevocationPolicy = r8rv1alpha1.RevocationPolicyRetain
	})
	f.reconcile()

	fresh := &r8rv1alpha1.ReplicationPolicy{}
	if err := hubClient.Get(testCtx, client.ObjectKeyFromObject(pol), fresh); err != nil {
		t.Fatal(err)
	}
	fresh.Spec.Targets.Namespaces = []string{"somewhere-else"}
	if err := hubClient.Update(testCtx, fresh); err != nil {
		t.Fatal(err)
	}

	_, rep := f.reconcile()

	if f.replica("spoke-a", f.ns) == nil {
		t.Fatal("retained replica was deleted")
	}
	if len(rep.Status.Inventory) != 1 {
		t.Fatalf("retained replica left inventory: %+v", rep.Status.Inventory)
	}
	revoked := false
	for _, c := range rep.Status.Conditions {
		if c.Type == ConditionPolicyRevoked && c.Status == metav1.ConditionTrue {
			revoked = true
		}
	}
	if !revoked {
		t.Error("PolicyRevoked condition not set")
	}
	if len(rep.Status.NonReadyTargets) != 1 || rep.Status.NonReadyTargets[0].Reason != r8rv1alpha1.ReasonPolicyRevoked {
		t.Errorf("nonReadyTargets = %+v", rep.Status.NonReadyTargets)
	}

	// Updates stop: source change must not reach the retained replica.
	f.updateSecret("new-value")
	f.reconcile()
	if got := replicaPassword(t, f.replica("spoke-a", f.ns)); got != b64("hunter2") {
		t.Errorf("retained replica was updated: %q", got)
	}

	// Restart: a fresh reconciler (no in-memory revocation cache) must
	// still retain — never fall through to garbage collection.
	f.rec = f.newReconciler()
	_, rep = f.reconcile()
	if f.replica("spoke-a", f.ns) == nil {
		t.Fatal("retained replica deleted after operator restart")
	}
	if len(rep.Status.Inventory) != 1 {
		t.Fatalf("inventory lost after restart: %+v", rep.Status.Inventory)
	}
}

// Spec "Target leaves selection": deselected targets are deleted and removed
// from inventory (no revocation semantics involved).
func TestReconcile_TargetLeavesSelection(t *testing.T) {
	f := newFixture(t, "hunter2", twoClusterTargets(), "spoke-a", "spoke-b")
	f.policy(nil, nil)
	f.reconcile()

	fresh := &r8rv1alpha1.Replication{}
	if err := hubClient.Get(testCtx, client.ObjectKey{Namespace: f.ns, Name: "rep"}, fresh); err != nil {
		t.Fatal(err)
	}
	fresh.Spec.ResolvedTargets = []r8rv1alpha1.ResolvedTarget{
		{ClusterName: "spoke-a", Namespaces: []string{f.ns}},
	}
	if err := hubClient.Update(testCtx, fresh); err != nil {
		t.Fatal(err)
	}

	_, rep := f.reconcile()

	if f.replica("spoke-b", f.ns) != nil {
		t.Fatal("deselected replica still present on spoke-b")
	}
	if f.replica("spoke-a", f.ns) == nil {
		t.Fatal("remaining replica was lost")
	}
	if len(rep.Status.Inventory) != 1 || rep.Status.Inventory[0].ClusterName != "spoke-a" {
		t.Errorf("inventory = %+v", rep.Status.Inventory)
	}
	if rep.Status.Summary.DesiredTargets != 1 || rep.Status.Summary.ReadyTargets != 1 {
		t.Errorf("summary = %+v", rep.Status.Summary)
	}
	if eventsContaining(f.drainEvents(), r8rv1alpha1.ReasonPolicyRevoked) {
		t.Error("plain deselection must not emit PolicyRevoked")
	}
}

// Spec "Source deletion cleans the fleet" (engine half): the engine
// finalizer defers Replication deletion until all inventoried replicas are
// removed, then releases.
func TestReconcile_DeletionCleansFleet(t *testing.T) {
	f := newFixture(t, "hunter2", twoClusterTargets(), "spoke-a", "spoke-b")
	f.policy(nil, nil)
	f.reconcile()

	fresh := &r8rv1alpha1.Replication{}
	if err := hubClient.Get(testCtx, client.ObjectKey{Namespace: f.ns, Name: "rep"}, fresh); err != nil {
		t.Fatal(err)
	}
	if err := hubClient.Delete(testCtx, fresh); err != nil {
		t.Fatal(err)
	}

	_, rep := f.reconcile()

	if rep != nil {
		t.Fatalf("replication still exists after cleanup: %+v", rep.Status.Inventory)
	}
	for _, c := range []string{"spoke-a", "spoke-b"} {
		if f.replica(c, f.ns) != nil {
			t.Errorf("replica on %s survived deletion", c)
		}
	}
}

// Spec "Unreachable target during cleanup": cleanup retries with backoff and
// reports; once the cluster leaves discovery, entries are released with a
// ClusterGone event instead of blocking forever.
func TestReconcile_UnreachableCleanupThenClusterGone(t *testing.T) {
	f := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: "spoke-a"}}, "spoke-a")
	f.policy(nil, nil)
	f.reconcile()

	f.transport.setUnavailable("spoke-a", true)
	fresh := &r8rv1alpha1.Replication{}
	if err := hubClient.Get(testCtx, client.ObjectKey{Namespace: f.ns, Name: "rep"}, fresh); err != nil {
		t.Fatal(err)
	}
	if err := hubClient.Delete(testCtx, fresh); err != nil {
		t.Fatal(err)
	}

	res, rep := f.reconcile()
	if rep == nil {
		t.Fatal("replication released although the cluster was unreachable")
	}
	if len(rep.Status.Inventory) != 1 {
		t.Fatalf("inventory dropped while unreachable: %+v", rep.Status.Inventory)
	}
	if res.RequeueAfter <= 0 || res.RequeueAfter > time.Second {
		t.Errorf("expected bounded backoff requeue, got %v", res.RequeueAfter)
	}
	found := false
	for _, nrt := range rep.Status.NonReadyTargets {
		if nrt.Reason == ReasonClusterUnreachable {
			found = true
		}
	}
	if !found {
		t.Errorf("no ClusterUnreachable detail reported: %+v", rep.Status.NonReadyTargets)
	}

	// Cluster leaves discovery entirely: release with ClusterGone.
	f.clusters.remove("spoke-a")
	_, rep = f.reconcile()
	if rep != nil {
		t.Fatalf("replication not released after ClusterGone: %+v", rep.Status.Inventory)
	}
	if !eventsContaining(f.drainEvents(), "ClusterGone") {
		t.Error("no ClusterGone event emitted")
	}
}

// Spec "Replica edited on target" (reconcile half): a drifted payload is
// restored; the watch half is covered by TestDrift_HandlerEnqueues....
func TestReconcile_RepairsDriftedReplica(t *testing.T) {
	f := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: "spoke-a"}}, "spoke-a")
	f.policy(nil, nil)
	f.reconcile()

	// Tamper with the replica payload directly on the spoke (hash
	// annotation intact — the nastier case).
	drifted := f.replica("spoke-a", f.ns)
	if err := unstructured.SetNestedField(drifted.Object, b64("tampered"), "data", "password"); err != nil {
		t.Fatal(err)
	}
	f.transport.put("spoke-a", drifted)

	_, rep := f.reconcile()

	if got := replicaPassword(t, f.replica("spoke-a", f.ns)); got != b64("hunter2") {
		t.Errorf("drifted replica not restored: %q", got)
	}
	if rep.Status.Summary.ReadyTargets != 1 {
		t.Errorf("summary = %+v", rep.Status.Summary)
	}
}

// Spec "Replica deleted on target" (reconcile half): a deleted replica is
// recreated while its source still selects the target.
func TestReconcile_RecreatesDeletedReplica(t *testing.T) {
	f := newFixture(t, "hunter2", []r8rv1alpha1.ResolvedTarget{{ClusterName: "spoke-a"}}, "spoke-a")
	f.policy(nil, nil)
	f.reconcile()

	gone := f.replica("spoke-a", f.ns)
	if err := f.transport.Delete(testCtx, "spoke-a", gone); err != nil {
		t.Fatal(err)
	}

	_, rep := f.reconcile()

	replica := f.replica("spoke-a", f.ns)
	if replica == nil {
		t.Fatal("deleted replica was not recreated")
	}
	if got := replicaPassword(t, replica); got != b64("hunter2") {
		t.Errorf("recreated replica payload = %q", got)
	}
	if rep.Status.Summary.ReadyTargets != 1 {
		t.Errorf("summary = %+v", rep.Status.Summary)
	}
}

// Design D8: a second reconcile with nothing changed performs no status
// write (no resourceVersion churn) and no spoke writes.
func TestReconcile_StatusWriteSkippedWhenUnchanged(t *testing.T) {
	f := newFixture(t, "hunter2", twoClusterTargets(), "spoke-a", "spoke-b")
	f.policy(nil, nil)
	_, first := f.reconcile()

	f.transport.mu.Lock()
	appliesBefore := f.transport.applies
	f.transport.mu.Unlock()

	_, second := f.reconcile()

	if first.ResourceVersion != second.ResourceVersion {
		t.Errorf("status written although unchanged: rv %s -> %s", first.ResourceVersion, second.ResourceVersion)
	}
	f.transport.mu.Lock()
	appliesAfter := f.transport.applies
	f.transport.mu.Unlock()
	if appliesAfter != appliesBefore {
		t.Errorf("healthy reconcile still wrote to spokes (%d new applies)", appliesAfter-appliesBefore)
	}
}

// Secret safety: no event, condition, or per-target message may contain the
// secret payload (raw or base64) — hashes only.
func TestReconcile_NoSecretPayloadInMessagesOrEvents(t *testing.T) {
	const canary = "SUPERSECRETVALUE"
	f := newFixture(t, canary, []r8rv1alpha1.ResolvedTarget{{ClusterName: "spoke-a"}}, "spoke-a")
	// Extend the fanout with a missing namespace and seed a conflict, to
	// generate rich failure output to audit.
	fresh := &r8rv1alpha1.Replication{}
	if err := hubClient.Get(testCtx, client.ObjectKey{Namespace: f.ns, Name: "rep"}, fresh); err != nil {
		t.Fatal(err)
	}
	fresh.Spec.ResolvedTargets = []r8rv1alpha1.ResolvedTarget{
		{ClusterName: "spoke-a", Namespaces: []string{f.ns, "missing-ns"}},
	}
	if err := hubClient.Update(testCtx, fresh); err != nil {
		t.Fatal(err)
	}
	f.policy([]string{f.ns, "missing-ns"}, func(p *r8rv1alpha1.ReplicationPolicy) {
		p.Spec.Options.AllowedConflictPolicies = []r8rv1alpha1.ConflictPolicy{
			r8rv1alpha1.ConflictPolicyFail, r8rv1alpha1.ConflictPolicyAdopt,
		}
	})
	conflicting := unmanagedObject(b64("other-payload"))
	conflicting.SetNamespace(f.ns)
	f.transport.put("spoke-a", conflicting)

	_, rep := f.reconcile()

	var sink strings.Builder
	for _, e := range f.drainEvents() {
		sink.WriteString(e)
	}
	for _, c := range rep.Status.Conditions {
		sink.WriteString(c.Message)
		sink.WriteString(c.Reason)
	}
	for _, nrt := range rep.Status.NonReadyTargets {
		sink.WriteString(nrt.Message)
		sink.WriteString(nrt.Reason)
	}
	out := sink.String()
	for _, needle := range []string{canary, b64(canary), b64("other-payload"), "other-payload"} {
		if strings.Contains(out, needle) {
			t.Errorf("secret payload %q leaked into events/conditions:\n%s", needle, out)
		}
	}
	if out == "" {
		t.Error("fixture produced no messages to audit")
	}
}
