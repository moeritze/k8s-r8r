# replication-engine

## MODIFIED Requirements

### Requirement: Conflict handling
When the intended replica name already exists on a target and is not managed by k8s-r8r, the engine SHALL apply the effective conflict policy: `Fail` (default) — do not touch the object, set a per-target `Conflict` condition; `Overwrite` — take over the object, only when both the request asks for it and policy permits it; `Adopt` — take ownership without rewriting, only when the existing object's content hash equals the source hash.

The effective conflict policy SHALL be the WEAKER of the policy the request asks for (`r8r.io/conflict-policy`, absent meaning `Fail`) and the strongest policy the matching `ReplicationPolicy` objects permit (`allowedConflictPolicies`, whose union always contains `Fail`), ranked `Fail` < `Adopt` < `Overwrite`. A value the engine cannot name SHALL rank as `Fail` on either side. Neither key alone SHALL be sufficient: a policy grant with no request-side opt-in, and a request-side opt-in with no policy grant, both resolve to `Fail`.

When a conflict resolves to `Fail` because one of the two keys did not turn, the per-target `Conflict` message SHALL say which one — a request that never opted in is a different operator action from a policy that never granted the escalation. Messages SHALL name annotation keys and policy names only, never object payload.

#### Scenario: Unmanaged object with default policy
- **WHEN** a target namespace already contains an unmanaged Secret with the replica's name and the effective conflict policy is `Fail`
- **THEN** the existing object is untouched and the `Replication` status reports a conflict for that target

#### Scenario: Adopt on identical content
- **WHEN** the conflict policy is `Adopt` and the existing object's content hash equals the source hash
- **THEN** the engine adds its management labels and hash annotation to the object without changing its payload

#### Scenario: Policy grant alone does not take over an object
- **WHEN** a `ReplicationPolicy` permits `Overwrite` for a target that holds an unmanaged object, and the source carries no `r8r.io/conflict-policy` annotation
- **THEN** the existing object is left untouched, the target reports `Conflict`, and the message names the absent request annotation as the missing consent

#### Scenario: Request asking for more than policy permits is capped
- **WHEN** a source requests `Overwrite` but the matching policies permit only up to `Adopt`
- **THEN** the engine acts with `Adopt` — adopting on identical content and reporting `Conflict` otherwise — and never overwrites

#### Scenario: Request asking for an escalation no policy grants is reported
- **WHEN** a source requests `Overwrite`, no matching `ReplicationPolicy` permits more than `Fail`, and an unmanaged object occupies the replica's name
- **THEN** the target reports `Conflict` with a message stating that the request asked for `Overwrite` and no matching policy permits it
