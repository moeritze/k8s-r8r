# Reconcile Helpers

> 20 nodes

## Key Concepts

- **.Reconcile()** (29 connections) — `internal/engine/reconciler.go`
- **.collectGarbage()** (18 connections) — `internal/engine/reconciler.go`
- **.reconcileDeletion()** (12 connections) — `internal/engine/reconciler.go`
- **Replication** (9 connections) — `internal/engine/reconciler.go`
- **.finishSimple()** (8 connections) — `internal/engine/reconciler.go`
- **Result** (7 connections) — `internal/engine/reconciler.go`
- **.writeStatusIfChanged()** (7 connections) — `internal/engine/reconciler.go`
- **.event()** (6 connections) — `internal/engine/reconciler.go`
- **addInventoryRevocations()** (6 connections) — `internal/engine/reconciler.go`
- **Duration** (5 connections) — `internal/engine/reconciler.go`
- **.emitTransitionEvents()** (5 connections) — `internal/engine/reconciler.go`
- **minDelay()** (5 connections) — `internal/engine/reconciler.go`
- **UID** (4 connections) — `internal/engine/reconciler.go`
- **.previousResult()** (4 connections) — `internal/engine/reconciler.go`
- **.storeResult()** (4 connections) — `internal/engine/reconciler.go`
- **ForgetReplicas()** (4 connections) — `internal/telemetry/metrics.go`
- **NamespacedName** (3 connections) — `internal/engine/reconciler.go`
- **.forgetResult()** (3 connections) — `internal/engine/reconciler.go`
- **ReplicationStatus** (1 connections) — `internal/engine/reconciler.go`
- **Condition** (1 connections) — `internal/engine/reconciler.go`

## Relationships

- [[Engine Reconcile Loop]] (18 shared connections)
- [[Server-Side Apply Path]] (15 shared connections)
- [[Policy Revocation]] (6 shared connections)
- [[Community 104]] (5 shared connections)
- [[Replica Inventory and GC]] (4 shared connections)
- [[Conflict Handling]] (3 shared connections)
- [[Replication Status Building]] (3 shared connections)
- [[Policy Evaluation Types]] (3 shared connections)
- [[Prometheus Collectors]] (2 shared connections)
- [[Backoff Tests]] (1 shared connections)
- [[Community 72]] (1 shared connections)

## Source Files

- `internal/engine/reconciler.go`
- `internal/telemetry/metrics.go`

## Audit Trail

- EXTRACTED: 121 (86%)
- INFERRED: 20 (14%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*