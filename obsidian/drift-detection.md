---
tags: [functionality]
---

# Drift Detection

Replicas converge event-driven, and the hub never caches spoke secret payloads.

## Mechanism (D3)

Per (cluster, replicated GVK) the engine runs a **metadata-only informer** (`PartialObjectMetadata`) on the [[cluster-discovery|cluster runtime]]'s cache, label-filtered to `app.kubernetes.io/managed-by: k8s-r8r` (server-side via cache `DefaultLabelSelector`):

```
watch event ──▶ compare r8r.io/source-hash annotation vs desired
   mismatch or deletion ──▶ enqueue owning Replication (via source-ref labels)
        reconcile ──▶ live-read replica, restore from source
```

- Hash = sha256 of canonical payload after stripping server-managed/identity fields; source, replica, and renamed replica hash identically.
- Payload-only edits that don't touch metadata still repair: every managed-object event enqueues, and reconcile compares live content.
- Fallback resync (default 10h, `--spoke-resync`) catches missed events and dropped watches.
- Informer lifecycle bound to cluster register/deregister.

## A correction is observable

The corrective write is not silent any more ([#30](https://github.com/moeritze/k8s-r8r/issues/30)). `applyTarget` splits the write into its two sub-cases and only one of them is reported:

| stored `source-hash` annotation | content hash | treated as |
|---|---|---|
| stale | differs | **drift** — counted + evented |
| current | differs | **drift** — counted + evented |
| stale | equal | **metadata repair** — written, silent |

- `k8s_r8r_drift_corrections_total{cluster}` — replicas whose **content** the engine rewrote. This is the tamper signal.
- `DriftCorrected` — a Warning event on the `Replication`, naming the replica ref and both hashes: `restored replica <cluster>/<ns>/<name>: observed content sha256:…, expected sha256:…`. Hashes only, never the diverging payload ([[security-model|secret-safe telemetry]]).

**Why the annotation-only repair stays silent, deliberately.** A change to the *hashing rules* produces exactly that state fleet-wide on upgrade — the metadata-hygiene change ([#26](https://github.com/moeritze/k8s-r8r/issues/26)) did it, extending `cleanRawKeys` so every affected replica held a stale annotation over content that recomputed as equal. Counting those would turn an operator rollout into a fleet-wide tamper alarm, and one false spike is enough to teach an operator to ignore the metric permanently. Keeping it out buys the invariant worth alerting on: **a non-zero `k8s_r8r_drift_corrections_total` always means a replica's payload was actually wrong.** The cost is that hand-editing only the `r8r.io/source-hash` annotation is repaired without a signal — acceptable, because no payload was altered and the annotation is the engine's own cache, never trusted alone.

**Read the rate off the metric, not the event stream.** `EventLimiter` suppresses an identical (object, reason, message) for five minutes, so drift recurring with the same hashes collapses into one event. That is the intended flood control ([[operations|rate-limited events]]), and it means the event stream understates recurrence by design. The counter is deliberately not rate-limited: event = "this happened, here is which object", metric = "and it is happening N times a minute".

## Ownership verification

Two labels on every replica do different jobs, and [#36](https://github.com/moeritze/k8s-r8r/issues/36) was the cost of treating them as one:

| Label | Job | Who writes it |
|---|---|---|
| `app.kubernetes.io/managed-by: k8s-r8r` | the spoke cache's server-side selector, re-checked in `driftHandler.observe` — the watch's **membership predicate** | k8s-r8r, but it is an `app.kubernetes.io/` convention key plenty of other tooling stamps too |
| `r8r.io/source-uid` | the **durable provenance mark**: the hub source's UID | only k8s-r8r; a UID is neither guessable nor reused across a delete/recreate |

Strip or rewrite `managed-by` on a replica and it leaves the informer entirely — no add, update or delete is ever delivered for it again. The fix is not "watch more". `applyTarget` and `collectGarbage` already `Transport.Get` every inventory entry they act on; `ClassifyReplicaOwnership` (`internal/engine/drift.go`) reads that live object's labels in three states keyed on the source UID:

| State | Labels found | Apply path | GC path |
|---|---|---|---|
| `Managed` | both marks | normal drift comparison | deleted |
| `Stripped` | own `source-uid`, `managed-by` gone or rewritten | **repaired** — re-applied, `OwnershipRepaired` | **deleted anyway**, `OwnershipStripped` |
| `Foreign` | no matching `source-uid` | the [[replication-flow#Conflict handling is a two-key turn\|conflict contract]] decides | never touched; entry released, `ReplicaOrphaned` |

- **The repair is the fix for the blindness.** Re-applying restores the marks, which puts the object back inside the cache selector; the watch resumes and the `DriftDetector` needs no notification.
- **Zero added steady-state load.** No new API calls, no cache probing — a label comparison on reads that already happen. "Absent from the filtered cache" and "lacks `managed-by: k8s-r8r`" are the same statement, and the live read is authoritative where the informer is only eventually consistent.
- **Detection takes up to one `--spoke-resync`** (10h default): the watch cannot see an object outside its selector, so the periodic reconcile is what finds it. Lower the flag for a tighter bound — it moves the informer resync and the periodic reconcile together.
- **Retained replicas are not verified.** A slot frozen under `revocationPolicy: Retain` is neither applied nor collected, so it is never read — the same rule that keeps it out of drift repair ([[security-model]]).

Two worse bugs sat behind the reported one, fixed in the same change ([PR #51](https://github.com/moeritze/k8s-r8r/pull/51)):

- **`applyTarget` forgot replicas it created.** `IsManagedReplica` needs both marks, so a stripped replica was filed as an unmanaged foreign object; under the default `Fail` that reported `Conflict` *and* dropped the inventory entry. Re-planned next pass, dropped again — and a `Replication` deleted in the wrong half of that cycle stranded a copy of the Secret on the spoke with nothing tracking it.
- **`collectGarbage` released such entries in silence.** Its gate could not tell "we never created this" from "we created this and someone relabelled it", and dropped both with no event and no metric.

**Behaviour change:** a `Stripped` replica is now *deleted* at cleanup, where it used to be abandoned — only ever for an object carrying this replication's own source UID at the name its own inventory records.

**Its own signal, not a drift correction.** `k8s_r8r_replica_ownership_lost_total{cluster,action}`, `action` ∈ `repaired|deleted|orphaned`. A stripped label over identical content is metadata, not payload, so it leaves `k8s_r8r_drift_corrections_total` alone and that counter keeps its invariant. When the payload diverged too, both fire (`OwnershipRepaired` + `DriftCorrected`) — the pair is the strongest tampering signature there is. Alerting: [[operations#What to alert on|operations]]. User-facing write-up: `../docs/security.md` (*The ownership label is the watch's membership predicate*).

## Why metadata-only

Memory stays bounded at fleet scale AND no fleet-wide secret data accumulates in hub caches — scale and [[security-model|security]] in one move. Cost: one watch connection per cluster per GVK (ArgoCD-proven pattern).

Implementation: `internal/engine/drift.go` (watch + enqueue, `ClassifyReplicaOwnership`), `internal/engine/reconciler.go` (`applyTarget`: the corrective write, the counter and the event; `collectGarbage`: the three-state gate) · spec: `openspec/specs/replication-engine` (drift requirement; *Replica ownership verification*) + `observability-operations` (metrics, rate-limited events; *Replica ownership signals*) · flow context: [[replication-flow]]
