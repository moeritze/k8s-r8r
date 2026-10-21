# observability-operations

## Purpose

Defines the operational baseline of the operator: metrics, events, secret-safety in telemetry, status size discipline, high availability, and health surfaces.

## Requirements

### Requirement: Prometheus metrics
The operator SHALL expose Prometheus metrics covering at minimum: replicas desired/ready/failed (by target cluster), reconcile durations and error counts (by controller), policy denials, conflicts, drift corrections, cluster connectivity state, cluster runtime count, discovery health, and workqueue depth. Metric labels SHALL be bounded (cluster, namespace, kind, provider, and small closed enumerations — never object names of unbounded cardinality).

Discovery health SHALL be observable independently of the cluster runtime count: the operator SHALL expose whether the configured discovery provider's inventory watch is established, and the size of that provider's own inventory, so that a broken discovery provider is distinguishable from a fleet with no matching clusters.

Corrective writes SHALL be observable independently of watch traffic: the drift-correction metric SHALL count only replicas whose content the engine rewrote because it diverged from the source, and SHALL NOT be conflated with the drift-candidate metric, which counts spoke informer events (the engine's own apply echoes included) and therefore rises on legitimate source updates.

#### Scenario: Failing cluster visible in metrics
- **WHEN** replication to a target cluster fails
- **THEN** the failed-replica and connectivity metrics for that cluster reflect it within one scrape interval

#### Scenario: Broken discovery is distinguishable from an empty fleet
- **WHEN** the discovery provider's inventory watch is not established
- **THEN** the discovery-health metric reads not-up, distinct from a running provider whose inventory is legitimately empty

#### Scenario: Corrective write is distinguishable from watch traffic
- **WHEN** a source object is updated and the engine propagates it to every replica
- **THEN** the drift-candidate metric rises with the resulting informer traffic while the drift-correction metric does not, because no replica had diverged

### Requirement: Secret-safe telemetry
No log line, event, metric, condition message, or error string SHALL contain secret payload data. Content comparisons in any user-visible output SHALL use hashes only.

#### Scenario: Drift event on a Secret
- **WHEN** the engine corrects drift on a replicated Secret
- **THEN** it emits an event containing the replica's cluster/namespace/name and the observed and expected `sha256:` hashes, and never key names' values or payload fragments

### Requirement: Rate-limited structured events
The operator SHALL emit Kubernetes events on source objects and `Replication` objects for lifecycle transitions (replicated, denied, conflict, drift corrected, revoked, cleaned up), rate-limited per object so flapping targets cannot flood the event stream.

Rate limiting SHALL be understood to suppress repeats, so an event stream SHALL NOT be treated as a count of occurrences. Where the rate of a rate-limited event is itself operationally significant — as it is for drift correction, whose recurrence indicates ongoing tampering or a competing controller — the operator SHALL expose an unlimited counter for that occurrence alongside the event.

#### Scenario: Flapping target
- **WHEN** a target cluster's connectivity flaps repeatedly
- **THEN** events on affected `Replication` objects are coalesced/rate-limited rather than emitted per flap

#### Scenario: Drift recurring with identical hashes
- **WHEN** the same replica is rewritten on a spoke repeatedly and the engine corrects it each time with unchanged observed and expected hashes
- **THEN** the events coalesce into one within the cooldown window while the drift-correction metric increments once per correction, so the recurrence remains observable

### Requirement: Replica ownership signals
Loss of a replica's ownership marks SHALL be observable, because it removes the replica from the drift watch's label selector and therefore from every watch-derived signal.

The operator SHALL expose a counter of inventoried replicas found without the `app.kubernetes.io/managed-by: k8s-r8r` label, labeled by target cluster and by how the engine resolved the finding: the replica's marks were restored, the replica was deleted during garbage collection, or the inventory entry was released without deleting anything because the object at that name was foreign. The label set SHALL remain bounded — the cluster name and a closed enumeration of resolutions; the replica's namespace and name belong in the event.

This counter SHALL be distinct from the drift-correction counter. A rewritten ownership label is not payload divergence, and conflating the two would break the drift-correction counter's guarantee that a non-zero value always means a replica's content was actually wrong. When a replica has both lost its ownership marks and had its content rewritten, both counters SHALL be incremented.

The operator SHALL emit a `Warning` event on the `Replication` for each resolution: one naming the repaired replica and the label that was rewritten, and one naming a released inventory entry and stating that the object on the spoke may require manual cleanup. Both events SHALL identify the replica by cluster, namespace and name only, and SHALL NOT contain payload.

#### Scenario: Ownership label rewritten on a spoke
- **WHEN** something on a spoke rewrites a replica's managed-by label and the engine restores it on the next reconcile
- **THEN** the ownership counter increments for that cluster with the repaired resolution, a `Warning` event names the replica and the label, and the drift-correction counter does not move because no content had diverged

#### Scenario: Inventory entry released without deletion
- **WHEN** the object at an inventoried replica's name is foreign and the engine releases the entry without deleting it
- **THEN** the ownership counter increments with the released resolution and a `Warning` event states that manual cleanup may be needed, so the released replica is not merely absent from the inventory

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
