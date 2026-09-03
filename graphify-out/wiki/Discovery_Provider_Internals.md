# Discovery Provider Internals

> 57 nodes

## Key Concepts

- **Provider** (29 connections) — `internal/discovery/capi/provider.go`
- **provider_test.go** (18 connections) — `internal/discovery/capi/provider_test.go`
- **T** (14 connections) — `internal/discovery/capi/provider_test.go`
- **startTestProvider()** (11 connections) — `internal/discovery/capi/provider_test.go`
- **WithDiscovery()** (7 connections) — `internal/discovery/capi/provider.go`
- **resolveClusterGVR()** (7 connections) — `internal/discovery/capi/provider.go`
- **recordFromCluster()** (7 connections) — `internal/discovery/capi/provider.go`
- **cluster()** (7 connections) — `internal/discovery/capi/provider_test.go`
- **WithResync()** (6 connections) — `internal/discovery/capi/provider.go`
- **.Start()** (6 connections) — `internal/discovery/capi/provider.go`
- **condition()** (6 connections) — `internal/discovery/capi/provider_test.go`
- **TestRecordFromClusterIsVersionIndependent()** (6 connections) — `internal/discovery/capi/provider_test.go`
- **fakeDiscovery()** (6 connections) — `internal/discovery/capi/provider_test.go`
- **TestProviderLifecycle()** (6 connections) — `internal/discovery/capi/provider_test.go`
- **TestProviderNoEventOnNoChange()** (6 connections) — `internal/discovery/capi/provider_test.go`
- **init()** (5 connections) — `internal/discovery/capi/provider.go`
- **.negotiate()** (5 connections) — `internal/discovery/capi/provider.go`
- **.upsert()** (5 connections) — `internal/discovery/capi/provider.go`
- **emit()** (5 connections) — `internal/discovery/capi/provider.go`
- **TestControlPlaneReady()** (5 connections) — `internal/discovery/capi/provider_test.go`
- **TestRecordFromCluster()** (5 connections) — `internal/discovery/capi/provider_test.go`
- **waitEvent()** (5 connections) — `internal/discovery/capi/provider_test.go`
- **TestStartFailsLoudlyWhenNoSupportedVersionIsServed()** (5 connections) — `internal/discovery/capi/provider_test.go`
- **TestStartWaitsWhenClusterResourceIsAbsent()** (5 connections) — `internal/discovery/capi/provider_test.go`
- **Option** (4 connections) — `internal/discovery/capi/provider.go`
- *... and 32 more nodes in this community*

## Relationships

- [[Discovery Provider Registry]] (1 shared connections)
- [[Telemetry Audit Ratchets]] (1 shared connections)

## Source Files

- `internal/discovery/capi/provider.go`
- `internal/discovery/capi/provider_test.go`

## Audit Trail

- EXTRACTED: 239 (90%)
- INFERRED: 26 (10%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*