# replication-engine

## ADDED Requirements

### Requirement: Replica ownership verification
The `app.kubernetes.io/managed-by: k8s-r8r` label is the membership predicate of the spoke drift watch: an inventoried replica that loses it is invisible to the watch, so drift detection SHALL NOT depend on that label alone as evidence of ownership.

The engine SHALL classify an object found at an inventoried replica's name by both of its ownership marks and SHALL distinguish three states: the object carries the managed-by label and this replication's `r8r.io/source-uid` (managed); the object carries a matching `r8r.io/source-uid` but not the managed-by label (ownership stripped); the object carries neither (foreign).

An object whose ownership is stripped SHALL be treated as this replication's replica and SHALL NOT be classified as a conflict with a foreign object. The engine SHALL restore its ownership marks and content, SHALL retain its inventory entry, and SHALL report the repair. Restoring the managed-by label returns the object to the watch's label selector, so a repaired replica SHALL again be observable by the drift watch without further action.

A foreign object SHALL NOT be repaired or taken over by ownership verification; it remains subject to the conflict-handling requirement and its two-key consent contract.

Verification SHALL be performed with live reads independent of the informer, and SHALL NOT require reads beyond those the reconcile already performs for its desired slots and its garbage-collection candidates. Revoked replicas retained under `revocationPolicy: Retain` are excluded from verification, because the engine has undertaken not to write to them.

The worst-case time to detect a stripped ownership label is one resync interval, since the watch cannot deliver events for an object outside its selector.

#### Scenario: Ownership label rewritten on a replica
- **WHEN** something on a spoke rewrites or removes `app.kubernetes.io/managed-by` on a replica while its `r8r.io/source-uid` still identifies this replication's source
- **THEN** the next reconcile restores the replica's ownership marks and content, keeps its inventory entry, reports the repair as an event and a metric increment, and the replica is delivered by the drift watch again

#### Scenario: Ownership label rewritten and content edited
- **WHEN** a replica has both lost its managed-by label and had its payload rewritten on the spoke
- **THEN** the engine reports both an ownership repair and a drift correction, so neither signal is masked by the other

#### Scenario: Foreign object at a replica's name
- **WHEN** an object at an inventoried replica's name carries neither this replication's managed-by label nor its source-uid label
- **THEN** ownership verification does not touch it and the conflict-handling requirement decides the outcome

#### Scenario: Retained replica after revocation
- **WHEN** a replica is retained under `revocationPolicy: Retain` and its ownership label is later rewritten
- **THEN** the engine does not repair it, because a retained replica is no longer written to by the engine

## MODIFIED Requirements

### Requirement: Inventory and garbage collection
The engine SHALL record every created replica in the `Replication` object's inventory and SHALL delete replicas (honoring `revocationPolicy` where applicable) when: the source is deleted (a finalizer on the source blocks its deletion until replica cleanup completes), the request annotations are removed, or a target leaves the resolved selection (cluster label change, selector change, namespace removed from the list). Cluster deregistration releases that cluster's inventory entries with a `ClusterGone` event without deleting replicas on the spoke — after deregistration the engine holds no credential for the cluster, so remote deletion is impossible; the clean removal path is deselecting the cluster (label/selector change) before deregistering it. No code path may lose track of a created replica.

Before deleting, the engine SHALL verify that the object at an inventory entry's name is one it created, so that a planned-but-never-applied entry can never delete an object the engine does not manage. That verification SHALL use both ownership marks: an object whose managed-by label was rewritten but whose `r8r.io/source-uid` still matches this replication's source is still this replication's replica and SHALL be deleted.

An inventory entry SHALL NOT be released silently. When the engine drops an entry without deleting the corresponding object — because the object at that name is foreign and must not be touched — it SHALL emit an event identifying the replica and stating that the object may require manual cleanup, and SHALL increment a metric for it, so that a replica the engine can no longer account for is observable rather than merely absent.

#### Scenario: Source deletion cleans the fleet
- **WHEN** a replicated Secret is deleted on the hub
- **THEN** the finalizer defers deletion until all inventoried replicas are removed from reachable targets, then the source and its `Replication` object are released

#### Scenario: Target leaves selection
- **WHEN** a cluster's labels change so it no longer matches the request's cluster selector
- **THEN** replicas on that cluster are deleted and removed from inventory

#### Scenario: Unreachable target during cleanup
- **WHEN** replicas must be cleaned from a cluster that is unreachable
- **THEN** cleanup is retried with backoff, the condition is reported, and after the cluster is deregistered from discovery the inventory entries are released with a `ClusterGone` event rather than blocking forever

#### Scenario: Cleanup of a replica whose ownership label was rewritten
- **WHEN** an inventoried replica whose managed-by label was rewritten must be cleaned up
- **THEN** it is deleted from the spoke, because its source-uid label still identifies it as this replication's replica, and the deletion is reported

#### Scenario: Inventory entry pointing at a foreign object
- **WHEN** the object at an inventory entry's name carries none of this replication's ownership marks
- **THEN** the engine deletes nothing, releases the entry, and reports the release with an event and a metric rather than dropping the entry silently
