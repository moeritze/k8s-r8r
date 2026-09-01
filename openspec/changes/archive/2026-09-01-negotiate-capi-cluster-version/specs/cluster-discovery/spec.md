# cluster-discovery

## MODIFIED Requirements

### Requirement: ClusterAPI provider
The ClusterAPI provider SHALL discover targets from ClusterAPI `Cluster` objects on the hub. Cluster labels for selection are the `Cluster` object's labels. A cluster becomes a valid target only when its control plane is ready. `Cluster` deletion SHALL emit deregistration.

The provider SHALL NOT pin a single `cluster.x-k8s.io` API version. On start it SHALL resolve the version to watch from the hub's discovery API, selecting the first version of a documented, ordered set of supported versions that the API server actually serves for `clusters.cluster.x-k8s.io`, and SHALL log the negotiated version once. Readiness evaluation SHALL remain version-tolerant across that set.

When the resource is served but at none of the supported versions, the provider SHALL fail to start with an error naming the group/resource, the supported versions, and the versions the server serves, rather than starting and reporting an empty inventory. When the resource is not served at all, or the hub is unreachable — conditions that resolve themselves when the fleet-management system is installed or recovers — the provider SHALL retry rather than fail, and SHALL report itself as not watching for as long as it waits. The provider SHALL NOT block indefinitely waiting for its watch to establish.

#### Scenario: New CAPI cluster becomes targetable
- **WHEN** a ClusterAPI `Cluster` reaches control-plane-ready
- **THEN** the provider emits its registration, the engine starts its cluster runtime, and pending requests selecting it begin replicating

#### Scenario: CAPI cluster deleted
- **WHEN** a `Cluster` object is deleted
- **THEN** the provider emits deregistration, the cluster runtime stops, and inventory entries for that cluster are released per the engine's garbage-collection rules

#### Scenario: Hub serves a newer CAPI version
- **WHEN** the hub serves several supported `cluster.x-k8s.io` versions for `clusters` (for example a deprecated and a current one)
- **THEN** the provider watches the most preferred supported version the hub serves, logs which version it negotiated, and discovers the same clusters regardless of which version is the storage version

#### Scenario: CAPI Cluster CRD serves no supported version
- **WHEN** `clusters.cluster.x-k8s.io` serves none of the provider's supported versions
- **THEN** the provider fails to start with an error naming the group/resource and the versions the server serves, rather than reporting an empty inventory

#### Scenario: ClusterAPI is not installed on the hub yet
- **WHEN** `clusters.cluster.x-k8s.io` is absent from the hub's discovery API
- **THEN** the provider logs the reason, reports itself as not watching, and retries until the resource appears, rather than failing the operator — and discovers clusters normally once ClusterAPI is installed
