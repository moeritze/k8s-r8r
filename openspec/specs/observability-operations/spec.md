# observability-operations

## Purpose

Defines the operational baseline of the operator: metrics, events, secret-safety in telemetry, status size discipline, high availability, and health surfaces.

## Requirements

### Requirement: Prometheus metrics
The operator SHALL expose Prometheus metrics covering at minimum: replicas desired/ready/failed (by target cluster), reconcile durations and error counts (by controller), policy denials, conflicts, cluster connectivity state, cluster runtime count, discovery health, and workqueue depth. Metric labels SHALL be bounded (cluster, namespace, kind, provider, and small closed enumerations — never object names of unbounded cardinality).

Discovery health SHALL be observable independently of the cluster runtime count: the operator SHALL expose whether the configured discovery provider's inventory watch is established, and the size of that provider's own inventory, so that a broken discovery provider is distinguishable from a fleet with no matching clusters.

#### Scenario: Failing cluster visible in metrics
- **WHEN** replication to a target cluster fails
- **THEN** the failed-replica and connectivity metrics for that cluster reflect it within one scrape interval

#### Scenario: Broken discovery is distinguishable from an empty fleet
- **WHEN** the discovery provider's inventory watch is not established
- **THEN** the discovery-health metric reads not-up, distinct from a running provider whose inventory is legitimately empty

### Requirement: Secret-safe telemetry
No log line, event, metric, condition message, or error string SHALL contain secret payload data. Content comparisons in any user-visible output SHALL use hashes only.

#### Scenario: Drift event on a Secret
- **WHEN** the engine reports drift on a replicated Secret
- **THEN** the event contains the object reference and hashes, never key names' values or payload fragments

### Requirement: Rate-limited structured events
The operator SHALL emit Kubernetes events on source objects and `Replication` objects for lifecycle transitions (replicated, denied, conflict, revoked, cleaned up), rate-limited per object so flapping targets cannot flood the event stream.

#### Scenario: Flapping target
- **WHEN** a target cluster's connectivity flaps repeatedly
- **THEN** events on affected `Replication` objects are coalesced/rate-limited rather than emitted per flap

### Requirement: Leader election and single-writer semantics
The operator SHALL support running multiple replicas with leader election such that exactly one instance reconciles at a time, and failover completes without duplicate or lost replication actions.

#### Scenario: Leader pod dies
- **WHEN** the leading operator pod is killed
- **THEN** a standby acquires leadership and resumes reconciliation from persisted state (CRs and inventory), with no orphaned replicas resulting from the transition

### Requirement: Health and readiness probes
The operator SHALL expose liveness and readiness endpoints; readiness SHALL reflect the ability to serve (informers synced on the hub). Individual unreachable target clusters SHALL NOT make the operator unready.

#### Scenario: One spoke down
- **WHEN** a single target cluster is unreachable
- **THEN** the operator remains ready and continues reconciling all other clusters
