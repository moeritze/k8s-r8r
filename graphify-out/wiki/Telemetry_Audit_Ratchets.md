# Telemetry Audit Ratchets

> 28 nodes

## Key Concepts

- **InformerSync** (7 connections) — `internal/telemetry/ready.go`
- **NewInformerSync()** (6 connections) — `internal/telemetry/ready.go`
- **TestEventsV1RBACGrantPresent()** (4 connections) — `internal/telemetry/events_rbac_audit_test.go`
- **secretsafety_audit_test.go** (4 connections) — `internal/telemetry/secretsafety_audit_test.go`
- **TestNoPayloadFieldsInMessageFormatting()** (4 connections) — `internal/telemetry/secretsafety_audit_test.go`
- **auditFile()** (4 connections) — `internal/telemetry/secretsafety_audit_test.go`
- **repoRoot()** (4 connections) — `internal/telemetry/secretsafety_audit_test.go`
- **hasEventsV1Rule()** (3 connections) — `internal/telemetry/events_rbac_audit_test.go`
- **ready.go** (3 connections) — `internal/telemetry/ready.go`
- **SyncWaiter** (3 connections) — `internal/telemetry/ready.go`
- **.Check()** (3 connections) — `internal/telemetry/ready.go`
- **ready_test.go** (3 connections) — `internal/telemetry/ready_test.go`
- **.WaitForCacheSync()** (3 connections) — `internal/telemetry/ready_test.go`
- **TestInformerSyncReadiness()** (3 connections) — `internal/telemetry/ready_test.go`
- **TestInformerSyncRunsOnAllReplicas()** (3 connections) — `internal/telemetry/ready_test.go`
- **T** (3 connections) — `internal/telemetry/secretsafety_audit_test.go`
- **calleeName()** (3 connections) — `internal/telemetry/secretsafety_audit_test.go`
- **events_rbac_audit_test.go** (2 connections) — `internal/telemetry/events_rbac_audit_test.go`
- **.Start()** (2 connections) — `internal/telemetry/ready.go`
- **fakeWaiter** (2 connections) — `internal/telemetry/ready_test.go`
- **T** (2 connections) — `internal/telemetry/ready_test.go`
- **T** (1 connections) — `internal/telemetry/events_rbac_audit_test.go`
- **Once** (1 connections) — `internal/telemetry/ready.go`
- **Context** (1 connections) — `internal/telemetry/ready.go`
- **.NeedLeaderElection()** (1 connections) — `internal/telemetry/ready.go`
- *... and 3 more nodes in this community*

## Relationships

- [[Manager Wiring (main.go)]] (1 shared connections)
- [[Discovery Provider Internals]] (1 shared connections)

## Source Files

- `internal/telemetry/events_rbac_audit_test.go`
- `internal/telemetry/ready.go`
- `internal/telemetry/ready_test.go`
- `internal/telemetry/secretsafety_audit_test.go`

## Audit Trail

- EXTRACTED: 68 (87%)
- INFERRED: 10 (13%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*