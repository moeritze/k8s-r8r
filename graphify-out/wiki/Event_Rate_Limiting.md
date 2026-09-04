# Event Rate Limiting

> 16 nodes

## Key Concepts

- **EventLimiter** (9 connections) — `internal/telemetry/events.go`
- **NewEventLimiter()** (6 connections) — `internal/telemetry/events.go`
- **events.go** (4 connections) — `internal/telemetry/events.go`
- **TestEventLimiter()** (3 connections) — `internal/telemetry/events_test.go`
- **TestEventLimiterDefaults()** (3 connections) — `internal/telemetry/events_test.go`
- **Duration** (2 connections) — `internal/telemetry/events.go`
- **Time** (2 connections) — `internal/telemetry/events.go`
- **eventEntry** (2 connections) — `internal/telemetry/events.go`
- **events_test.go** (2 connections) — `internal/telemetry/events_test.go`
- **T** (2 connections) — `internal/telemetry/events_test.go`
- **Mutex** (1 connections) — `internal/telemetry/events.go`
- **eventKey** (1 connections) — `internal/telemetry/events.go`
- **eventEntry** (1 connections) — `internal/telemetry/events.go`
- **eventKey** (1 connections) — `internal/telemetry/events.go`
- **.Allow()** (1 connections) — `internal/telemetry/events.go`
- **.Forget()** (1 connections) — `internal/telemetry/events.go`

## Relationships

- [[Engine Reconcile Loop]] (1 shared connections)

## Source Files

- `internal/telemetry/events.go`
- `internal/telemetry/events_test.go`

## Audit Trail

- EXTRACTED: 36 (88%)
- INFERRED: 5 (12%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*