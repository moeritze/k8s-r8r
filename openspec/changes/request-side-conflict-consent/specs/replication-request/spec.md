# replication-request

## MODIFIED Requirements

### Requirement: Annotation-based replication request
The system SHALL accept replication requests expressed as annotations on source objects. The annotation contract is:
- `r8r.io/replicate: "true"` — opts the object in.
- `r8r.io/target-clusters: "<label selector>"` — selects target clusters by labels on discovered cluster inventory (empty selector selects no clusters; an explicit `*` is not supported).
- `r8r.io/target-namespaces: "<comma-separated list>"` — target namespaces; defaults to the source namespace when omitted.
- `r8r.io/target-name: "<name>"` — optional explicit name override for replicas; automatic renaming (prefix/suffix) SHALL NOT occur.
- `r8r.io/conflict-policy: "Fail" | "Adopt" | "Overwrite"` — the strongest conflict handling this request consents to. The value set is closed and case-sensitive, matching the `ConflictPolicy` enum. An absent or empty value SHALL mean `Fail`: a request that says nothing about conflicts consents to nothing.

Unknown `r8r.io/*` keys SHALL be rejected by the request controller so typos fail loudly rather than silently selecting nothing; the advisory webhook SHALL warn on them instead of rejecting, so an older webhook cannot block a request that uses a newer key.

#### Scenario: Valid request creates a canonical Replication object
- **WHEN** a Secret is annotated with `r8r.io/replicate: "true"` and target annotations, and at least one `ReplicationPolicy` permits the request
- **THEN** the operator creates exactly one operator-owned `Replication` object for that source, with resolved targets in its spec

#### Scenario: Request without matching policy is not acted on
- **WHEN** a Secret is annotated for replication but no `ReplicationPolicy` permits it
- **THEN** no replicas are created, and the `Replication` object (or event on the source) reports a `PolicyDenied` condition naming the reason

#### Scenario: Malformed conflict policy is rejected
- **WHEN** a source carries `r8r.io/conflict-policy` with a value outside the closed set (including a differently cased spelling such as `overwrite`)
- **THEN** the request is marked invalid with a message naming the annotation and listing the accepted values, and — where the advisory webhook is deployed — the write is rejected at apply time with the same message

#### Scenario: Conflict policy the policy set cannot grant is admitted with a warning
- **WHEN** a source requests a conflict policy that no `ReplicationPolicy` able to match it permits
- **THEN** the write is admitted with a warning naming the annotation, because the request is otherwise valid and only its conflict handling falls back to the weaker of the two keys
