# Transport Test Doubles

> 75 nodes

## Key Concepts

- **newFixture()** (28 connections) — `internal/engine/suite_test.go`
- **reconciler_test.go** (21 connections) — `internal/engine/reconciler_test.go`
- **T** (19 connections) — `internal/engine/reconciler_test.go`
- **suite_test.go** (15 connections) — `internal/engine/suite_test.go`
- **testFixture** (15 connections) — `internal/engine/suite_test.go`
- **fakeTransport** (14 connections) — `internal/engine/suite_test.go`
- **replicaPassword()** (11 connections) — `internal/engine/reconciler_test.go`
- **b64()** (10 connections) — `internal/engine/reconciler_test.go`
- **twoClusterTargets()** (10 connections) — `internal/engine/reconciler_test.go`
- **Unstructured** (10 connections) — `internal/engine/suite_test.go`
- **.Get()** (10 connections) — `internal/engine/suite_test.go`
- **.storeFor()** (8 connections) — `internal/engine/suite_test.go`
- **.Delete()** (8 connections) — `internal/engine/suite_test.go`
- **unmanagedObject()** (7 connections) — `internal/engine/conflict_test.go`
- **TestReconcile_ConflictFailDefault()** (7 connections) — `internal/engine/reconciler_test.go`
- **TestReconcile_ConflictOverwrite()** (7 connections) — `internal/engine/reconciler_test.go`
- **keyOf()** (7 connections) — `internal/engine/suite_test.go`
- **stubClusters** (7 connections) — `internal/engine/suite_test.go`
- **TestReconcile_HappyPathFanout()** (6 connections) — `internal/engine/reconciler_test.go`
- **TestReconcile_SourceUpdatePropagates()** (6 connections) — `internal/engine/reconciler_test.go`
- **TestReconcile_ConflictAdopt()** (6 connections) — `internal/engine/reconciler_test.go`
- **.Apply()** (6 connections) — `internal/engine/suite_test.go`
- **eventsContaining()** (6 connections) — `internal/engine/suite_test.go`
- **TestReconcile_RevocationDelete()** (5 connections) — `internal/engine/reconciler_test.go`
- **TestReconcile_RevocationRetain()** (5 connections) — `internal/engine/reconciler_test.go`
- *... and 50 more nodes in this community*

## Relationships

- [[Conflict Handling]] (2 shared connections)

## Source Files

- `internal/engine/conflict_test.go`
- `internal/engine/reconciler_test.go`
- `internal/engine/suite_test.go`

## Audit Trail

- EXTRACTED: 340 (86%)
- INFERRED: 54 (14%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*