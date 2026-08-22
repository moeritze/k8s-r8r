## Purpose

Defines how users request replication of Kubernetes objects via annotations, and how each request is materialized into a canonical, operator-owned `Replication` object that carries status and replica inventory.

## ADDED Requirements

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
- **THEN** no replicas are created, and the `Replication` object (or event on the source) reports a `PolicyDenied` condition naming the reason

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
Each `Replication` object SHALL expose: a summary (`desiredTargets`, `readyTargets`, `failedTargets`), an aggregate `Ready` condition, per-target detail entries only for non-ready targets capped at a fixed limit with an overflow count, and the observed source content hash. Status writes SHALL be skipped when nothing changed.

#### Scenario: Healthy fanout has compact status
- **WHEN** a source is successfully replicated to all selected targets
- **THEN** status contains summary counts and `Ready: True`, with an empty per-target detail list

#### Scenario: Failing target is visible
- **WHEN** replication to one target cluster fails
- **THEN** status summary shows one failed target and the detail list contains an entry naming the cluster, namespace, and error reason

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
