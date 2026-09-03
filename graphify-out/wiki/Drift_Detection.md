# Drift Detection

> 50 nodes

## Key Concepts

- **DriftDetector** (19 connections) — `internal/engine/drift.go`
- **fakeInformer** (9 connections) — `internal/engine/drift_test.go`
- **NewDriftDetector()** (8 connections) — `internal/engine/drift.go`
- **fakeCache** (8 connections) — `internal/engine/drift_test.go`
- **TestDrift_HandlerEnqueuesOwningReplication()** (8 connections) — `internal/engine/drift_test.go`
- **drift_test.go** (7 connections) — `internal/engine/drift_test.go`
- **TestDrift_InformerLifecycle()** (7 connections) — `internal/engine/drift_test.go`
- **driftHandler** (6 connections) — `internal/engine/drift.go`
- **.observe()** (6 connections) — `internal/engine/drift.go`
- **startDetector()** (6 connections) — `internal/engine/drift_test.go`
- **.installAll()** (5 connections) — `internal/engine/drift.go`
- **.install()** (5 connections) — `internal/engine/drift.go`
- **.GetInformer()** (5 connections) — `internal/engine/drift_test.go`
- **.informer()** (5 connections) — `internal/engine/drift_test.go`
- **Cache** (4 connections) — `internal/engine/drift.go`
- **.AddEventHandlerWithResyncPeriod()** (4 connections) — `internal/engine/drift_test.go`
- **newFakeCache()** (4 connections) — `internal/engine/drift_test.go`
- **drift.go** (3 connections) — `internal/engine/drift.go`
- **Context** (3 connections) — `internal/engine/drift.go`
- **ObjectKey** (3 connections) — `internal/engine/drift.go`
- **GroupVersionKind** (3 connections) — `internal/engine/drift.go`
- **.Start()** (3 connections) — `internal/engine/drift.go`
- **.ClusterReady()** (3 connections) — `internal/engine/drift.go`
- **.EnsureWatch()** (3 connections) — `internal/engine/drift.go`
- **.Enqueue()** (3 connections) — `internal/engine/drift.go`
- *... and 25 more nodes in this community*

## Relationships

- [[Prometheus Collectors]] (2 shared connections)
- [[Engine Reconcile Loop]] (1 shared connections)

## Source Files

- `internal/engine/drift.go`
- `internal/engine/drift_test.go`
- `internal/telemetry/metrics.go`

## Audit Trail

- EXTRACTED: 177 (96%)
- INFERRED: 8 (4%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*