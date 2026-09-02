# Backoff Tests

> 6 nodes

## Key Concepts

- **backoff_test.go** (4 connections) — `internal/engine/backoff_test.go`
- **T** (4 connections) — `internal/engine/backoff_test.go`
- **TestMinDelay()** (3 connections) — `internal/engine/backoff_test.go`
- **TestBackoffTracker_ExponentialWithCap()** (2 connections) — `internal/engine/backoff_test.go`
- **TestBackoffTracker_PerClusterIndependence()** (2 connections) — `internal/engine/backoff_test.go`
- **TestBackoffTracker_SuccessResetsAndForgetClears()** (2 connections) — `internal/engine/backoff_test.go`

## Relationships

- [[Reconcile Helpers]] (1 shared connections)

## Source Files

- `internal/engine/backoff_test.go`

## Audit Trail

- EXTRACTED: 16 (94%)
- INFERRED: 1 (6%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*