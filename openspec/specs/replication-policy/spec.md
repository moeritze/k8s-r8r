# replication-policy

## Purpose

Defines the admin-controlled security boundary that decides which replication requests are allowed: default deny, allowlist-only `ReplicationPolicy` objects with union semantics, evaluated authoritatively at reconcile time.

## Requirements

### Requirement: Default deny
With no `ReplicationPolicy` objects present, the system SHALL replicate nothing. Every replication action MUST be explicitly permitted by at least one policy.

#### Scenario: No policies exist
- **WHEN** a source object carries valid replication annotations and no `ReplicationPolicy` exists
- **THEN** no replicas are created and the request is reported as denied

### Requirement: Allowlist matching dimensions
A `ReplicationPolicy` SHALL express, as allowlists: source namespaces (exact names or label selector), source kinds, target clusters (label selector over discovered cluster inventory), and target namespaces. A request is permitted by a policy only if all dimensions match. Policies SHALL NOT contain deny rules.

#### Scenario: All dimensions match
- **WHEN** a request's source namespace, kind, resolved target clusters, and target namespaces all fall within one policy's allowlists
- **THEN** the request is permitted

#### Scenario: One dimension fails
- **WHEN** a request matches a policy on all dimensions except one target namespace
- **THEN** replication to that namespace is denied while permitted targets proceed, and the denial is reported per target

### Requirement: Union semantics across policies
Multiple policies SHALL combine by union: a request (or an individual target of it) is permitted if any single policy permits it in full for that target.

#### Scenario: Two partial policies do not combine dimensions
- **WHEN** policy A allows the source namespace but not the target cluster, and policy B allows the target cluster but not the source namespace
- **THEN** the request is denied (no single policy permits it)

### Requirement: Policy options gate side effects
Each policy SHALL carry options that gate engine behavior for requests it permits: `allowNamespaceCreation` (default false), permitted `conflictPolicy` values (default only `Fail`), and `revocationPolicy: Retain | Delete` (default `Delete`) controlling what happens to existing replicas when permission is withdrawn.

#### Scenario: Namespace creation denied by default
- **WHEN** a permitted request targets a namespace that does not exist on the target cluster and no matching policy sets `allowNamespaceCreation: true`
- **THEN** replication to that target fails with a condition explaining the namespace is missing and creation is not allowed

### Requirement: Reconcile-time enforcement is authoritative
Policy SHALL be re-evaluated on every reconcile of every `Replication`. Admission-time validation is advisory only. When a policy change withdraws permission for existing replicas, the operator SHALL act per the applicable `revocationPolicy`: `Delete` removes the replicas; `Retain` leaves them but marks the `Replication` with a `PolicyRevoked` condition and stops updating them.

#### Scenario: Policy tightened after replication
- **WHEN** an admin edits a policy so an existing replica set of targets is no longer permitted, and the effective revocation policy is `Delete`
- **THEN** the affected replicas are deleted on the next reconcile and status reflects the revocation

### Requirement: Policy authoring is admin-scoped
`ReplicationPolicy` SHALL be a cluster-scoped resource. Project-provided RBAC manifests SHALL grant write access only to cluster administrators and read access to the operator. Replication SHALL grant developers no privileges beyond objects they can already write: the system only fans out objects the requester could already modify, to destinations policy allows.

#### Scenario: Developer cannot widen their own permissions
- **WHEN** a developer with namespace-scoped RBAC annotates an object in their namespace targeting a cluster no policy allows for that namespace
- **THEN** the request is denied; there is no mechanism for the developer to override policy
