# Cluster Runtime Manager

> 65 nodes

## Key Concepts

- **Manager** (35 connections) — `internal/cluster/manager.go`
- **newTestManager()** (12 connections) — `internal/cluster/manager_test.go`
- **entry** (10 connections) — `internal/cluster/manager.go`
- **backoffTracker** (8 connections) — `internal/cluster/manager.go`
- **manager_test.go** (8 connections) — `internal/cluster/manager_test.go`
- **TestManagerLifecycle()** (7 connections) — `internal/cluster/manager_test.go`
- **Duration** (6 connections) — `internal/cluster/manager.go`
- **.launch()** (6 connections) — `internal/cluster/manager.go`
- **.lifecycle()** (6 connections) — `internal/cluster/manager.go`
- **stubRuntime** (6 connections) — `internal/cluster/manager_test.go`
- **State** (5 connections) — `internal/cluster/manager.go`
- **Connectivity** (5 connections) — `internal/cluster/manager.go`
- **.Observe()** (5 connections) — `internal/cluster/manager.go`
- **WithRuntimeFactory()** (5 connections) — `internal/cluster/manager.go`
- **Context** (5 connections) — `internal/cluster/manager.go`
- **NewManager()** (5 connections) — `internal/cluster/manager.go`
- **.Register()** (5 connections) — `internal/cluster/manager.go`
- **.startRuntime()** (5 connections) — `internal/cluster/manager.go`
- **T** (5 connections) — `internal/cluster/manager_test.go`
- **.Start()** (5 connections) — `internal/cluster/manager_test.go`
- **waitFor()** (5 connections) — `internal/cluster/manager_test.go`
- **TestManagerOutageIsolationAndStates()** (5 connections) — `internal/cluster/manager_test.go`
- **TestManagerShutdownStopsAllRuntimes()** (5 connections) — `internal/cluster/manager_test.go`
- **Runtime** (4 connections) — `internal/cluster/manager.go`
- **DefaultRuntimeFactory()** (4 connections) — `internal/cluster/manager.go`
- *... and 40 more nodes in this community*

## Relationships

- [[Manager Wiring (main.go)]] (3 shared connections)
- [[Community 110]] (1 shared connections)

## Source Files

- `internal/cluster/manager.go`
- `internal/cluster/manager_test.go`

## Audit Trail

- EXTRACTED: 257 (96%)
- INFERRED: 12 (4%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*