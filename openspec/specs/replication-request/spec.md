# replication-request

## Purpose

Defines how users request replication of Kubernetes objects via annotations, and how each request is materialized into a canonical, operator-owned `Replication` object that carries status and replica inventory.

## Requirements

### Requirement: Annotation-based replication request
The system SHALL accept replication requests expressed as annotations on source objects. The annotation contract is:
- `r8r.io/replicate: "true"` — opts the object in.
- `r8r.io/target-clusters: "<label selector>"` — selects target clusters by labels on discovered cluster inventory (empty selector selects no clusters; an explicit `*` is not supported).
- `r8r.io/target-namespaces: "<comma-separated list>"` — target namespaces; defaults to the source namespace when omitted.
- `r8r.io/target-name: "<name>"` — optional explicit name override for replicas; automatic renaming (prefix/suffix) SHALL NOT occur.

#### Scenario: Valid request creates a canonical Replication object
- **WHEN** a Secret is annotated with `r8r.io/replicate: "true"` and target annotations, and at least one `ReplicationPolicy` permits the request
- **THEN** the operator creates exactly one operator-owned `Replication` object for that source, with resolved targets in its spec

#### Scenario: Request without matching policy is not acted on
- **WHEN** a Secret is annotated for replication but no `ReplicationPolicy` permits it
- **THEN** no replicas are created, and the `Replication` object carries a condition naming the denial reason. An event on the source MAY accompany the condition but SHALL NOT substitute for it: events expire, and the denial must stay discoverable for as long as the request stands.

#### Scenario: Request resolves to no target at all
- **WHEN** a source is annotated for replication and its `r8r.io/target-clusters` selector matches no ready cluster, so no target is ever evaluated against policy
- **THEN** the `Replication` object reports that it resolved to no targets, distinguishably from a request that was denied by policy, even though no denial occurred and no denial event is emitted

### Requirement: Kind allowlist for request acceptance
The system SHALL only accept replication requests for kinds on the configured allowlist. The launch allowlist is `Secret` and `ConfigMap`. The internal pipeline SHALL be kind-agnostic so extending the allowlist requires no API change.

#### Scenario: Non-allowlisted kind is rejected
- **WHEN** an object of a kind not on the allowlist carries replication annotations
- **THEN** the request is ignored and an event explains the kind is not enabled

### Requirement: Canonical Replication object is operator-owned
`Replication` objects SHALL be created, updated, and deleted only by the operator, derived from requests. Users SHALL NOT author `Replication` objects directly; the API server RBAC provided by the project's manifests grants users read-only access to them.

#### Scenario: Manual Replication object is not reconciled
- **WHEN** a user creates a `Replication` object by hand
- **THEN** the operator marks it with a `NotAuthoritative` condition and does not replicate anything based on it

### Requirement: Replication status reports fanout state
Each `Replication` object SHALL expose: a summary (`desiredTargets`, `readyTargets`, `failedTargets`), an aggregate `Ready` condition, a `TargetsResolved` condition reporting whether the request resolved to any target, per-target detail entries only for non-ready targets capped at a fixed limit with an overflow count, and the observed source content hash. Status writes SHALL be skipped when nothing changed.

`Ready` SHALL answer "is everything that was asked for actually replicated?". A `Replication` that is not being deleted and has zero desired targets SHALL therefore report `Ready: False` with a reason distinguishing it from a target failure: replicating nothing is not a vacuous success, and it is the state an operator most needs to alert on.

Each condition on a `Replication` SHALL have exactly one writing controller, and controllers SHALL NOT write conditions they do not own. Two controllers writing one condition produce a status whose value depends on write order, and — because each write re-triggers the other controller — unbounded status churn.

#### Scenario: Healthy fanout has compact status
- **WHEN** a source is successfully replicated to all selected targets
- **THEN** status contains summary counts, `Ready: True`, and `TargetsResolved: True`, with an empty per-target detail list

#### Scenario: Zero resolved targets is not reported as ready
- **WHEN** a `Replication` exists for a source that still requests replication, but its resolved target set is empty — every candidate denied by policy, or no candidate produced at all
- **THEN** status reports `desiredTargets: 0` together with `Ready: False`, and `TargetsResolved: False` naming whether the cause was a policy denial or an empty resolution

#### Scenario: Failing target is visible
- **WHEN** replication to one target cluster fails
- **THEN** status summary shows one failed target and the detail list contains an entry naming the cluster, namespace, and error reason

#### Scenario: Settled status is not rewritten
- **WHEN** every controller that writes to a `Replication` has reconciled it and nothing about the request, the policy set, or the fleet has changed
- **THEN** no further status write occurs, regardless of how many controllers observe the object

### Requirement: Request removal triggers cleanup
Removing the replication annotations from a source SHALL cause deletion of all its replicas and its `Replication` object, subject to the policy revocation setting in effect.

#### Scenario: Annotation removed
- **WHEN** `r8r.io/replicate` is removed from a previously replicated Secret
- **THEN** all replicas recorded in inventory are deleted and the `Replication` object is removed

### Requirement: Selector-based requests are additive later
The canonical layer SHALL be designed so that a future selector-based request CRD (`ReplicationSet`) can materialize the same `Replication` objects without changes to the engine, policy, or status contracts. This requirement documents the compatibility constraint; the `ReplicationSet` CRD itself is out of scope for this change.

#### Scenario: Canonical layer accepts multiple request origins
- **WHEN** a `Replication` object is materialized
- **THEN** it records its originating request kind (annotation) in a field designed to admit other origins
