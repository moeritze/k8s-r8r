# observability-operations

## MODIFIED Requirements

### Requirement: Prometheus metrics
The operator SHALL expose Prometheus metrics covering at minimum: replicas desired/ready/failed (by target cluster), reconcile durations and error counts (by controller), policy denials, conflicts, cluster connectivity state, cluster runtime count, discovery health, and workqueue depth. Metric labels SHALL be bounded (cluster, namespace, kind, provider, and small closed enumerations — never object names of unbounded cardinality).

Discovery health SHALL be observable independently of the cluster runtime count: the operator SHALL expose whether the configured discovery provider's inventory watch is established, and the size of that provider's own inventory, so that a broken discovery provider is distinguishable from a fleet with no matching clusters.

#### Scenario: Failing cluster visible in metrics
- **WHEN** replication to a target cluster fails
- **THEN** the failed-replica and connectivity metrics for that cluster reflect it within one scrape interval

#### Scenario: Broken discovery is distinguishable from an empty fleet
- **WHEN** the discovery provider's inventory watch is not established
- **THEN** the discovery-health metric reads not-up, distinct from a running provider whose inventory is legitimately empty
