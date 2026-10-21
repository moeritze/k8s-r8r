# replication-policy

## MODIFIED Requirements

### Requirement: Default deny
With no `ReplicationPolicy` objects present, the system SHALL replicate nothing. Every replication action MUST be explicitly permitted by at least one policy.

#### Scenario: No policies exist
- **WHEN** a source object carries valid replication annotations and no `ReplicationPolicy` exists
- **THEN** no replicas are created and the request is reported as denied on the `Replication` object's status, for as long as the request stands

### Requirement: Reconcile-time enforcement is authoritative
Policy SHALL be re-evaluated on every reconcile of every `Replication`. Admission-time validation is advisory only. When a policy change withdraws permission for existing replicas, the operator SHALL act per the applicable `revocationPolicy`: `Delete` removes the replicas; `Retain` leaves them but marks the `Replication` with a `PolicyRevoked` condition and stops updating them.

Revocation SHALL leave a durable status signal. Events describing the revocation expire, and a condition that is removed once the revocation has been fully processed leaves no record at all; a `Replication` whose permission was withdrawn SHALL NOT come to rest reporting readiness.

#### Scenario: Policy tightened after replication
- **WHEN** an admin edits a policy so an existing replica set of targets is no longer permitted, and the effective revocation policy is `Delete`
- **THEN** the affected replicas are deleted on the next reconcile and status reflects the revocation

#### Scenario: Status after a Delete revocation has settled
- **WHEN** every replica removed by a `Delete` revocation is gone, the revocation events have expired, and the source still requests replication
- **THEN** the `Replication` still reports `Ready: False` — its targets resolved to nothing — rather than reporting readiness with zero targets
