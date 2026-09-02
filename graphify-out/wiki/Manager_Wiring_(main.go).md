# Manager Wiring (main.go)

> 22 nodes

## Key Concepts

- **spokeWirer** (13 connections) — `cmd/main.go`
- **main()** (13 connections) — `cmd/main.go`
- **main.go** (7 connections) — `cmd/main.go`
- **newSpokeWirer()** (7 connections) — `cmd/main.go`
- **.run()** (6 connections) — `cmd/main.go`
- **Context** (5 connections) — `cmd/main.go`
- **.bootstrap()** (5 connections) — `cmd/main.go`
- **IncSpokeBootstrap()** (5 connections) — `internal/telemetry/metrics.go`
- **parseAllowedKinds()** (4 connections) — `cmd/main.go`
- **.Handle()** (4 connections) — `cmd/main.go`
- **.ensure()** (4 connections) — `cmd/main.go`
- **RBACScope** (3 connections) — `cmd/main.go`
- **ClusterRecord** (3 connections) — `cmd/main.go`
- **SetExtraStrippedKeys()** (3 connections) — `internal/engine/render.go`
- **Reader** (2 connections) — `cmd/main.go`
- **Manager** (2 connections) — `cmd/main.go`
- **.drop()** (2 connections) — `cmd/main.go`
- **init()** (1 connections) — `cmd/main.go`
- **GroupVersionKind** (1 connections) — `cmd/main.go`
- **Mutex** (1 connections) — `cmd/main.go`
- **CancelFunc** (1 connections) — `cmd/main.go`
- **Event** (1 connections) — `cmd/main.go`

## Relationships

- [[Prometheus Collectors]] (6 shared connections)
- [[Cluster Runtime Manager]] (3 shared connections)
- [[Community 129]] (2 shared connections)
- [[Conflict Handling]] (2 shared connections)
- [[Community 131]] (1 shared connections)
- [[Annotation Parsing]] (1 shared connections)
- [[Push Transport]] (1 shared connections)
- [[Telemetry Audit Ratchets]] (1 shared connections)

## Source Files

- `cmd/main.go`
- `internal/engine/render.go`
- `internal/telemetry/metrics.go`

## Audit Trail

- EXTRACTED: 76 (82%)
- INFERRED: 17 (18%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*