# Push Transport

> 13 nodes

## Key Concepts

- **PushTransport** (8 connections) — `internal/engine/transport.go`
- **Transport** (5 connections) — `internal/engine/transport.go`
- **.clientFor()** (5 connections) — `internal/engine/transport.go`
- **.Apply()** (5 connections) — `internal/engine/transport.go`
- **.Get()** (5 connections) — `internal/engine/transport.go`
- **NewPushTransport()** (4 connections) — `internal/engine/transport.go`
- **.Delete()** (4 connections) — `internal/engine/transport.go`
- **ClientGetter** (3 connections) — `internal/engine/transport.go`
- **Context** (3 connections) — `internal/engine/transport.go`
- **Unstructured** (3 connections) — `internal/engine/transport.go`
- **.fieldManager()** (2 connections) — `internal/engine/transport.go`
- **Client** (1 connections) — `internal/engine/transport.go`
- **ObjectKey** (1 connections) — `internal/engine/transport.go`

## Relationships

- [[Manager Wiring (main.go)]] (1 shared connections)

## Source Files

- `internal/engine/transport.go`

## Audit Trail

- EXTRACTED: 47 (98%)
- INFERRED: 1 (2%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*