# cluster-discovery

## MODIFIED Requirements

### Requirement: Minimal-privilege credential bootstrap
The system SHALL NOT operate against target clusters with fleet-management admin credentials. On registration, the operator SHALL use the provider's admin credential (for ClusterAPI: the `<cluster>-kubeconfig` Secret) exactly once per bootstrap to create a dedicated namespace, ServiceAccount, and RBAC scoped to the operator's configured kind allowlist (`--allowed-kinds`) plus the namespace-creation rights replica writes require, then obtain and use only that ServiceAccount's token for all replication traffic. Tokens SHALL be short-lived and rotated before expiry. RBAC on the spoke SHALL be re-narrowed when the configured kind allowlist shrinks.

Two narrowings are deferred and MUST be documented wherever the bootstrap's privilege reduction is claimed:

- **Policy-derived scoping.** The grant is derived from the configured kind allowlist, not from the policy universe (the kinds installed `ReplicationPolicy` objects actually permit). It is therefore a superset of what policy permits, and it exists from install time, before any policy is authored. Deriving it from the policy universe requires first deciding whether the provider's admin credential stays one-shot, since widening a grant needs a credential that can escalate. Tracked in issue #29.
- **Namespace scoping.** The grant is a ClusterRole, so its verbs apply in every namespace of the spoke. Per-namespace Roles are a future refinement; `RBACScope` is the stable seam for both narrowings.

#### Scenario: Steady-state traffic uses the narrow ServiceAccount
- **WHEN** replicas are written to a bootstrapped target cluster
- **THEN** the request is authenticated as the k8s-r8r ServiceAccount, not the ClusterAPI kubeconfig identity

#### Scenario: Token expiry
- **WHEN** a target's ServiceAccount token approaches expiry
- **THEN** the operator obtains a fresh token without interrupting replication

#### Scenario: Configured kind allowlist shrinks
- **WHEN** the operator's `--allowed-kinds` configuration is reduced to a smaller set of kinds and the operator restarts with the new value
- **THEN** each ready target cluster is re-bootstrapped at the smaller scope and its replication ClusterRole rules are replaced wholesale, so no rule for a removed kind remains on any target
