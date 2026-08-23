# admission-validation

## Purpose

Defines the advisory validating admission webhook that gives users apply-time feedback on replication requests no policy would permit, without ever becoming an availability or security dependency.

## Requirements

### Requirement: Advisory validation of replication requests
A validating admission webhook SHALL evaluate CREATE and UPDATE of allowlisted kinds and reject writes whose replication annotations no `ReplicationPolicy` would permit, with an error message naming the failing dimension (source namespace, kind, target clusters, or target namespaces). Malformed annotation values (unparseable selector, invalid name) SHALL likewise be rejected with a specific message.

#### Scenario: Disallowed request rejected at apply time
- **WHEN** a user applies a Secret annotated to target clusters no policy allows for its namespace
- **THEN** the API server rejects the write with a message identifying the unmatched policy dimension

#### Scenario: Allowed request passes
- **WHEN** a user applies a Secret whose replication annotations are permitted by at least one policy
- **THEN** the write is admitted

### Requirement: Webhook scope is limited to opted-in objects
The webhook configuration SHALL use match conditions so that only objects carrying k8s-r8r annotations are sent to the webhook. Writes to non-annotated Secrets and ConfigMaps SHALL NOT incur a webhook call.

#### Scenario: Ordinary secret traffic bypasses the webhook
- **WHEN** a Secret without any `r8r.io/` annotation is created or updated
- **THEN** no admission call to the k8s-r8r webhook occurs

### Requirement: Webhook is never an availability or security dependency
The webhook SHALL be configured with `failurePolicy: Ignore`. The system's security MUST NOT depend on the webhook: reconcile-time policy evaluation remains authoritative, so a bypassed or unavailable webhook results only in later feedback, never in unauthorized replication.

#### Scenario: Operator down, secret writes unaffected
- **WHEN** the webhook backend is unavailable and a user writes an annotated Secret
- **THEN** the write is admitted, and the denial (if any) is reported by the controller through the `Replication` status and events instead
