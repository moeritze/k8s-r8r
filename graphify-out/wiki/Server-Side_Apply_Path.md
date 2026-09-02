# Server-Side Apply Path

> 15 nodes

## Key Concepts

- **.applyTarget()** (22 connections) — `internal/engine/reconciler.go`
- **Context** (11 connections) — `internal/engine/reconciler.go`
- **Request** (4 connections) — `internal/engine/reconciler.go`
- **targetState** (4 connections) — `internal/engine/reconciler.go`
- **.applyWithRecreate()** (4 connections) — `internal/engine/reconciler.go`
- **.ensureNamespace()** (4 connections) — `internal/engine/reconciler.go`
- **replicaCountsByCluster()** (4 connections) — `internal/engine/reconciler.go`
- **.mapSource()** (4 connections) — `internal/engine/reconciler.go`
- **.mapPolicy()** (4 connections) — `internal/engine/reconciler.go`
- **.namespaceLabels()** (3 connections) — `internal/engine/reconciler.go`
- **classifyTransportErr()** (3 connections) — `internal/engine/reconciler.go`
- **Unstructured** (2 connections) — `internal/engine/reconciler.go`
- **Object** (2 connections) — `internal/engine/reconciler.go`
- **EffectiveOptions** (1 connections) — `internal/engine/reconciler.go`
- **ReplicaCounts** (1 connections) — `internal/engine/reconciler.go`

## Relationships

- [[Reconcile Helpers]] (15 shared connections)
- [[Engine Reconcile Loop]] (10 shared connections)
- [[Conflict Handling]] (4 shared connections)
- [[Policy Revocation]] (3 shared connections)
- [[Replica Inventory and GC]] (2 shared connections)
- [[Prometheus Collectors]] (1 shared connections)

## Source Files

- `internal/engine/reconciler.go`

## Audit Trail

- EXTRACTED: 66 (90%)
- INFERRED: 7 (10%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*