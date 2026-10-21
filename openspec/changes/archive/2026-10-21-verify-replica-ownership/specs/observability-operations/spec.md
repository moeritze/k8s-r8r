# observability-operations

## ADDED Requirements

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
