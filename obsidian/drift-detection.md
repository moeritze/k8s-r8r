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

## Why metadata-only

Memory stays bounded at fleet scale AND no fleet-wide secret data accumulates in hub caches — scale and [[security-model|security]] in one move. Cost: one watch connection per cluster per GVK (ArgoCD-proven pattern).

Implementation: `internal/engine/drift.go` · spec: `openspec/specs/replication-engine` (drift requirement) · flow context: [[replication-flow]]
