# replication-engine

## MODIFIED Requirements

### Requirement: Drift detection via metadata watches
The engine SHALL maintain, per connected target cluster, metadata-only informers filtered to `app.kubernetes.io/managed-by: k8s-r8r` for each replicated GVK. Full replica payloads SHALL NOT be cached on the hub. A hash mismatch or replica deletion observed via watch SHALL enqueue the affected `(source, targetCluster)` for reconciliation. A periodic resync SHALL act as fallback for missed events.

A corrective write SHALL be recorded, not only performed: when the engine rewrites a replica because its content hash diverged from the source, it SHALL emit an event identifying the replica and both hashes, and SHALL increment the drift-correction metric for that target cluster, so that a corrective write is distinguishable from a no-op reconcile.

Recording SHALL be scoped to content divergence. When a replica's content already matches the source and only the engine's own source-hash annotation is stale, the engine SHALL repair the annotation without emitting a drift event or incrementing the drift-correction metric — that state is produced fleet-wide by a change to the hashing rules, and reporting it would make the correction signal unusable as a tamper indicator.

#### Scenario: Replica edited on target
- **WHEN** someone modifies a replica's payload directly on a spoke cluster
- **THEN** the engine detects the mismatch via the metadata watch, restores the replica to match the source, and records the correction as an event and a metric increment

#### Scenario: Replica's source-hash annotation edited on target
- **WHEN** a replica's `r8r.io/source-hash` annotation is stale but its content still hashes equal to the source
- **THEN** the engine repairs the annotation and reports no drift correction, because no replicated content had diverged

#### Scenario: Replica deleted on target
- **WHEN** a replica is deleted on a spoke cluster while its source still requests replication there
- **THEN** the engine recreates it
