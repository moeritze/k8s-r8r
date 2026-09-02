# Prometheus Collectors

> 27 nodes

## Key Concepts

- **metrics.go** (22 connections) — `internal/telemetry/metrics.go`
- **exerciseAll()** (13 connections) — `internal/telemetry/metrics_test.go`
- **replicaAggregator** (7 connections) — `internal/telemetry/metrics.go`
- **Desc** (6 connections) — `internal/telemetry/metrics.go`
- **ObserveReplicas()** (5 connections) — `internal/telemetry/metrics.go`
- **clusterCollector** (5 connections) — `internal/telemetry/metrics.go`
- **discoveryCollector** (5 connections) — `internal/telemetry/metrics.go`
- **SetDiscoverySnapshot()** (5 connections) — `internal/telemetry/metrics.go`
- **IncConflict()** (3 connections) — `internal/telemetry/metrics.go`
- **IncTokenRotation()** (3 connections) — `internal/telemetry/metrics.go`
- **resultLabel()** (3 connections) — `internal/telemetry/metrics.go`
- **ReplicaCounts** (3 connections) — `internal/telemetry/metrics.go`
- **Metric** (3 connections) — `internal/telemetry/metrics.go`
- **ConnectivityValue()** (3 connections) — `internal/telemetry/metrics.go`
- **SetClusterSnapshot()** (3 connections) — `internal/telemetry/metrics.go`
- **newClusterCollector()** (3 connections) — `internal/telemetry/metrics.go`
- **newDiscoveryCollector()** (3 connections) — `internal/telemetry/metrics.go`
- **init()** (3 connections) — `internal/telemetry/metrics.go`
- **newReplicaAggregator()** (2 connections) — `internal/telemetry/metrics.go`
- **.Describe()** (2 connections) — `internal/telemetry/metrics.go`
- **.Collect()** (2 connections) — `internal/telemetry/metrics.go`
- **.Describe()** (2 connections) — `internal/telemetry/metrics.go`
- **.Collect()** (2 connections) — `internal/telemetry/metrics.go`
- **DiscoveryState** (2 connections) — `internal/telemetry/metrics.go`
- **.Describe()** (2 connections) — `internal/telemetry/metrics.go`
- *... and 2 more nodes in this community*

## Relationships

- [[Manager Wiring (main.go)]] (6 shared connections)
- [[Community 72]] (6 shared connections)
- [[Reconcile Helpers]] (2 shared connections)
- [[Drift Detection]] (2 shared connections)
- [[Policy Revocation]] (2 shared connections)
- [[Community 104]] (2 shared connections)
- [[CAPI Version Negotiation]] (2 shared connections)
- [[Server-Side Apply Path]] (1 shared connections)

## Source Files

- `internal/telemetry/metrics.go`
- `internal/telemetry/metrics_test.go`

## Audit Trail

- EXTRACTED: 92 (80%)
- INFERRED: 23 (20%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*