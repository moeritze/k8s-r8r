# Verify replica ownership

## Why

The spoke caches are built with a server-side label selector on
`app.kubernetes.io/managed-by: k8s-r8r` (`cmd/main.go`, cache options), and
`driftHandler.observe` (`internal/engine/drift.go`) filters on the same label
again. That label is therefore not merely an annotation on a replica — it is
the **membership predicate of the drift watch**.

Anything on a spoke that rewrites or removes it — a second controller
claiming the object, a GitOps tool stamping its own ownership labels, an
operator tidying labels by hand — silently evicts the replica from the watch
while it stays in the `Replication`'s inventory (issue #36).

The existing drift requirements never state this dependency. "A hash mismatch
or replica deletion observed via watch SHALL enqueue the affected
`(source, targetCluster)`" **implicitly assumes the watch sees every
inventoried replica**, and that assumption is exactly what a rewritten
`managed-by` label breaks. This change makes the assumption explicit and gives
the engine a path that does not depend on it.

Tracing what actually happens today turned up two concrete defects behind the
reported one, both worse than lost watch coverage:

1. **`applyTarget` drops the inventory entry for a replica it created.**
   The next periodic reconcile does read the object live
   (`Transport.Get`), but `DecideConflict` sees no `managed-by` label,
   classifies the engine's own replica as an *unmanaged foreign object*, and
   returns `ActionFail`. `fail(..., retry=false)` then calls
   `removeEntry` — the engine forgets a replica it wrote. The entry is
   re-planned on the next pass and dropped again, so the `Replication`
   oscillates, and a deletion landing in the wrong half of that cycle leaves
   the replica behind forever. For a Secret replicator that is a copy of
   credential material stranded on a spoke with nothing tracking it.

2. **`collectGarbage` releases such an entry silently.** Its safety gate
   (`!IsManagedReplica(...)` → "not ours, just drop the entry") cannot tell
   *"we never created this"* from *"we created this and someone relabelled
   it"*. Both are dropped, with no event and no metric. The gate is right to
   refuse to delete a foreign object; it is wrong to say nothing.

Severity is **security-relevant**: the failure mode is a replicated Secret
that the engine no longer watches, no longer tracks, and will not clean up,
while `Ready=True` and the inventory both keep looking healthy.

## What Changes

- **Ownership is classified, not just tested.** A new
  `ClassifyReplicaOwnership` distinguishes three states of an object found at
  an inventoried replica's name: `Managed` (both marks), `Stripped` (the
  `r8r.io/source-uid` provenance label still matches this source but
  `managed-by` was rewritten or removed — provably our replica, evicted from
  the watch), and `Foreign` (neither mark).
- **`applyTarget` repairs the `Stripped` state** instead of misfiling it as a
  conflict: it re-applies, which restores the ownership marks and thereby
  **puts the object back into the filtered cache — the repair is the fix for
  the blindness**. The inventory entry survives.
- **`collectGarbage` deletes a `Stripped` replica** rather than abandoning
  it: a stripped label does not stop it being a copy of the source that this
  `Replication` created and is responsible for removing. `Foreign` objects
  are still never touched, but their release is now **reported** instead of
  silent.
- **New metric `k8s_r8r_replica_ownership_lost_total{cluster, action}`**,
  `action` ∈ `repaired` | `deleted` | `orphaned` — one increment per
  inventoried replica found without the watch's membership label, labelled by
  how the engine resolved it.
- **New `Warning` events** `OwnershipRepaired` and `ReplicaOrphaned` on the
  `Replication`, naming the replica and the label. Hashes only, never
  payload.
- **Content divergence still reports as drift.** A replica that lost its
  label *and* had its payload rewritten emits both signals and increments
  both counters, preserving the invariant from #30 that a non-zero
  `k8s_r8r_drift_corrections_total` always means a payload was actually
  wrong.
- **No new flag, no new API call on the healthy path.** Verification reuses
  the live reads the reconcile already performs. See design D3 for the cost
  analysis at fleet scale.
- Deliberately **out of scope**: verifying revoked-but-retained inventory
  entries (design D5), and shortening the verification interval below
  `--spoke-resync` (design D4).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `replication-engine`: gains a *Replica ownership verification* requirement
  stating that the drift watch's membership label is not trusted as the only
  evidence of ownership, and that a stripped label is repaired rather than
  reclassified as a conflict. *Inventory and garbage collection* gains the
  rule that an inventory entry is never released without either deleting the
  replica or reporting the release — the current silent drop violates its own
  "no code path may lose track of a created replica".
- `observability-operations`: gains a *Replica ownership signals* requirement
  for the new metric and events.

The metric enumeration in *Prometheus metrics* and the event enumeration in
*Rate-limited structured events* are the natural home for the new signals,
but both are already modified by the unarchived `report-drift-corrections`
change. Restating them here would make whichever change syncs second silently
discard the other's text, so the ownership signals are stated as their own
requirement; folding them into the two enumerations belongs to the archive
pass for this sprint.

## Impact

- `internal/engine/drift.go` — `ReplicaOwnership`, `ClassifyReplicaOwnership`,
  and the doc comment tying the label to cache membership.
- `internal/engine/reconciler.go` — one new case in `applyTarget`'s existing
  switch; the `collectGarbage` safety gate.
- `internal/telemetry/metrics.go` — `replicaOwnershipLost` counter,
  `IncOwnershipLost(cluster, action)`, registration.
- `internal/telemetry/metrics_test.go` — the new family joins the inventory
  and cardinality audits.
- `internal/engine/ownership_test.go` (new) — classification unit table,
  repair, repair + content drift, foreign object unchanged, GC of a stripped
  replica, reported release of a foreign one.
- `docs/security.md` — the ownership label as a watch-membership predicate,
  the new signals, and the retained-replica exception.
- `CHANGELOG.md` — entry under Unreleased.
- No CRD, no chart, no RBAC, no flag, no `Replication` API change.
