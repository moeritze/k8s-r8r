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

## Known gaps (open issues)

- **A correction leaves no trace** ([#30](https://github.com/moeritze/k8s-r8r/issues/30)). Drift is repaired promptly and then looks exactly like a routine reconcile: no event, no condition, no log line that distinguishes "found tampering and overwrote it" from "nothing to do". The one counter that exists, `k8s_r8r_drift_events_total`, counts *informer events on managed replicas* — including the engine's own apply echoes — so it cannot answer "has anything been tampering with my replicated secrets?". A fix is in flight; any signal must stay hash-only per [[security-model|secret-safe telemetry]].
- **Blind if `managed-by` is rewritten** ([#36](https://github.com/moeritze/k8s-r8r/issues/36)). The spoke cache is label-filtered server-side, and `driftHandler.observe` returns early for anything without `app.kubernetes.io/managed-by: k8s-r8r`. If another controller, a GitOps tool, or an operator rewrites or removes that label on a replica, the object leaves the watch entirely while staying in `Replication.status.inventory` — edits and deletions then go unnoticed forever, and status keeps reporting the target ready. This is the metadata-only design working as specified, so the fix is not "watch more": candidates are resync-time `Transport.Get` verification of inventory members, or a signal when an inventory member is absent from the filtered cache.

## Why metadata-only

Memory stays bounded at fleet scale AND no fleet-wide secret data accumulates in hub caches — scale and [[security-model|security]] in one move. Cost: one watch connection per cluster per GVK (ArgoCD-proven pattern).

Implementation: `internal/engine/drift.go` · spec: `openspec/specs/replication-engine` (drift requirement) · flow context: [[replication-flow]]
