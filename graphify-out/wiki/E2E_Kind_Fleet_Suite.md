# E2E Kind Fleet Suite

> 68 nodes

## Key Concepts

- **ctx()** (27 connections) — `test/e2e/framework.go`
- **framework.go** (25 connections) — `test/e2e/framework.go`
- **TestClusterLifecycle()** (17 connections) — `test/e2e/cluster_lifecycle_test.go`
- **ReplicationName()** (11 connections) — `internal/controller/request/controller.go`
- **TestReplicationLifecycle()** (11 connections) — `test/e2e/replication_test.go`
- **applyPolicy()** (10 connections) — `test/e2e/framework.go`
- **createAnnotatedSecret()** (10 connections) — `test/e2e/framework.go`
- **getReplication()** (10 connections) — `test/e2e/framework.go`
- **spokeSecret()** (10 connections) — `test/e2e/framework.go`
- **TestScaleFanout()** (10 connections) — `test/e2e/scale_test.go`
- **deletePolicy()** (9 connections) — `test/e2e/framework.go`
- **replicasBySourceUID()** (9 connections) — `test/e2e/framework.go`
- **TestConflictFail()** (9 connections) — `test/e2e/replication_test.go`
- **TestNamespaceEnsure()** (9 connections) — `test/e2e/replication_test.go`
- **TestSourceDeleteCleanup()** (9 connections) — `test/e2e/replication_test.go`
- **Client** (8 connections) — `test/e2e/framework.go`
- **TestPolicyDeleteRevocation()** (8 connections) — `test/e2e/replication_test.go`
- **deleteNamespaceAndWait()** (7 connections) — `test/e2e/framework.go`
- **setup()** (7 connections) — `test/e2e/main_test.go`
- **registerSpoke()** (6 connections) — `test/e2e/framework.go`
- **main_test.go** (6 connections) — `test/e2e/main_test.go`
- **replication_test.go** (6 connections) — `test/e2e/replication_test.go`
- **deleteIgnoreNotFound()** (5 connections) — `test/e2e/framework.go`
- **expectReplicaOnSpokes()** (5 connections) — `test/e2e/replication_test.go`
- **T** (5 connections) — `test/e2e/replication_test.go`
- *... and 43 more nodes in this community*

## Relationships

- [[Annotation Parsing]] (4 shared connections)

## Source Files

- `internal/controller/request/controller.go`
- `test/e2e/cluster_lifecycle_test.go`
- `test/e2e/framework.go`
- `test/e2e/main_test.go`
- `test/e2e/replication_test.go`
- `test/e2e/scale_test.go`

## Audit Trail

- EXTRACTED: 211 (61%)
- INFERRED: 133 (39%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*