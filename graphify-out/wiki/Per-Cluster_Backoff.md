# Per-Cluster Backoff

> 11 nodes

## Key Concepts

- **backoffTracker** (9 connections) — `internal/engine/backoff.go`
- **NamespacedName** (4 connections) — `internal/engine/backoff.go`
- **Duration** (4 connections) — `internal/engine/backoff.go`
- **.Failure()** (4 connections) — `internal/engine/backoff.go`
- **backoff.go** (3 connections) — `internal/engine/backoff.go`
- **backoffKey** (3 connections) — `internal/engine/backoff.go`
- **newBackoffTracker()** (3 connections) — `internal/engine/backoff.go`
- **.delayLocked()** (3 connections) — `internal/engine/backoff.go`
- **.Success()** (2 connections) — `internal/engine/backoff.go`
- **.Forget()** (2 connections) — `internal/engine/backoff.go`
- **Mutex** (1 connections) — `internal/engine/backoff.go`

## Relationships

- No strong cross-community connections detected

## Source Files

- `internal/engine/backoff.go`

## Audit Trail

- EXTRACTED: 38 (100%)
- INFERRED: 0 (0%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*