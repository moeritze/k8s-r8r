# Policy Revocation

> 11 nodes

## Key Concepts

- **.deniedState()** (11 connections) — `internal/engine/reconciler.go`
- **InventoryEntry** (8 connections) — `internal/engine/reconciler.go`
- **hasEntry()** (6 connections) — `internal/engine/reconciler.go`
- **GroupVersionKind** (4 connections) — `internal/engine/reconciler.go`
- **SlotKey** (4 connections) — `internal/engine/reconciler.go`
- **.gvkForEntry()** (4 connections) — `internal/engine/reconciler.go`
- **clusterSet()** (4 connections) — `internal/engine/reconciler.go`
- **nsKey** (3 connections) — `internal/engine/reconciler.go`
- **IncPolicyDenial()** (3 connections) — `internal/telemetry/metrics.go`
- **Decision** (1 connections) — `internal/engine/reconciler.go`
- **ResolvedTarget** (1 connections) — `internal/engine/reconciler.go`

## Relationships

- [[Engine Reconcile Loop]] (8 shared connections)
- [[Reconcile Helpers]] (6 shared connections)
- [[Server-Side Apply Path]] (3 shared connections)
- [[Community 104]] (3 shared connections)
- [[Prometheus Collectors]] (2 shared connections)
- [[Replica Inventory and GC]] (1 shared connections)

## Source Files

- `internal/engine/reconciler.go`
- `internal/telemetry/metrics.go`

## Audit Trail

- EXTRACTED: 45 (92%)
- INFERRED: 4 (8%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*