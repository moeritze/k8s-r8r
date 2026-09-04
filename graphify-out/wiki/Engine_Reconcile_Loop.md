# Engine Reconcile Loop

> 26 nodes

## Key Concepts

- **Reconciler** (52 connections) — `internal/engine/reconciler.go`
- **.flattenTargets()** (7 connections) — `internal/engine/reconciler.go`
- **.SetupWithManager()** (6 connections) — `internal/engine/reconciler.go`
- **.init()** (5 connections) — `internal/engine/reconciler.go`
- **.Lookup()** (4 connections) — `internal/engine/reconciler.go`
- **Options** (4 connections) — `internal/engine/reconciler.go`
- **slotInfo** (4 connections) — `internal/engine/reconciler.go`
- **ProviderInventory** (3 connections) — `internal/engine/reconciler.go`
- **.withDefaults()** (2 connections) — `internal/engine/reconciler.go`
- **.clusterGone()** (2 connections) — `internal/engine/reconciler.go`
- **.enqueueForCluster()** (2 connections) — `internal/engine/reconciler.go`
- **ClusterInventory** (1 connections) — `internal/engine/reconciler.go`
- **Provider** (1 connections) — `internal/engine/reconciler.go`
- **ClusterRecord** (1 connections) — `internal/engine/reconciler.go`
- **ClusterEvents** (1 connections) — `internal/engine/reconciler.go`
- **Client** (1 connections) — `internal/engine/reconciler.go`
- **Scheme** (1 connections) — `internal/engine/reconciler.go`
- **EventRecorder** (1 connections) — `internal/engine/reconciler.go`
- **Transport** (1 connections) — `internal/engine/reconciler.go`
- **Renderer** (1 connections) — `internal/engine/reconciler.go`
- **DriftDetector** (1 connections) — `internal/engine/reconciler.go`
- **backoffTracker** (1 connections) — `internal/engine/reconciler.go`
- **EventLimiter** (1 connections) — `internal/engine/reconciler.go`
- **Once** (1 connections) — `internal/engine/reconciler.go`
- **Mutex** (1 connections) — `internal/engine/reconciler.go`
- *... and 1 more nodes in this community*

## Relationships

- [[Reconcile Helpers]] (18 shared connections)
- [[Server-Side Apply Path]] (10 shared connections)
- [[Policy Revocation]] (8 shared connections)
- [[Community 104]] (4 shared connections)
- [[Event Rate Limiting]] (1 shared connections)
- [[Drift Detection]] (1 shared connections)

## Source Files

- `internal/engine/reconciler.go`

## Audit Trail

- EXTRACTED: 103 (98%)
- INFERRED: 2 (2%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*