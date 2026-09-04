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

## Known gap (open issue)

- **Blind if `managed-by` is rewritten** ([#36](https://github.com/moeritze/k8s-r8r/issues/36)). The spoke cache is label-filtered server-side, and `driftHandler.observe` returns early for anything without `app.kubernetes.io/managed-by: k8s-r8r`. If another controller, a GitOps tool, or an operator rewrites or removes that label on a replica, the object leaves the watch entirely while staying in `Replication.status.inventory` — edits and deletions then go unnoticed forever, and status keeps reporting the target ready. Note the correction signal above does not help here: an object that never reaches the reconcile never gets counted. This is the metadata-only design working as specified, so the fix is not "watch more": candidates are resync-time `Transport.Get` verification of inventory members, or a signal when an inventory member is absent from the filtered cache.

## Why metadata-only

Memory stays bounded at fleet scale AND no fleet-wide secret data accumulates in hub caches — scale and [[security-model|security]] in one move. Cost: one watch connection per cluster per GVK (ArgoCD-proven pattern).

Implementation: `internal/engine/drift.go` (watch + enqueue), `internal/engine/reconciler.go` (`applyTarget`: the corrective write, the counter and the event) · spec: `openspec/specs/replication-engine` (drift requirement) + `observability-operations` (metrics, rate-limited events) · flow context: [[replication-flow]]
