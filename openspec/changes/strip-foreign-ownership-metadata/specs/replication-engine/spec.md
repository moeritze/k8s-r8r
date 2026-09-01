# replication-engine

## ADDED Requirements

### Requirement: Foreign ownership metadata is not replicated
Labels and annotations on a source object that assert ownership by, or
replication intent toward, a controller other than k8s-r8r SHALL NOT be copied
onto replicas, and SHALL be excluded from the canonical source hash so that a
source and its replica hash identically.

The stripped set SHALL cover, at minimum, the key prefixes
`argocd.argoproj.io/`, `replicator.v1.mittwald.de/`,
`reflector.v1.k8s.emberstack.com/`, `meta.helm.sh/`,
`kustomize.toolkit.fluxcd.io/` and the exact key `app.kubernetes.io/instance`.
The operator SHALL be able to extend this set with additional keys or prefixes
at deployment time; the built-in entries SHALL NOT be removable.

Stripping SHALL be scoped to ownership and replication-intent metadata: all
other source labels and annotations continue to propagate, because they can be
functionally significant on the target (for example a sealed-secrets key
label).

#### Scenario: Foreign replicator annotation on the source
- **WHEN** a source Secret carries `replicator.v1.mittwald.de/replicate-to-clusters: ".*"` and is replicated
- **THEN** the replica carries no `replicator.v1.mittwald.de/*` key, so it is not itself a valid source for that controller and cannot seed a fanout no `ReplicationPolicy` evaluated

#### Scenario: GitOps tracking label on the source
- **WHEN** a source Secret carries the Argo CD tracking label `app.kubernetes.io/instance: some-app` and is replicated
- **THEN** the replica carries no `app.kubernetes.io/instance` label and no `argocd.argoproj.io/*` key, so it does not claim membership in an Application and is not a prune candidate on the target cluster

#### Scenario: Hash stability with foreign keys present
- **WHEN** the engine hashes a source that carries stripped keys and hashes the replica rendered from it
- **THEN** both hashes are equal, so the replica is not re-applied on every reconcile

#### Scenario: Unrelated source metadata still propagates
- **WHEN** a source Secret carries metadata outside the stripped set, such as `sealedsecrets.bitnami.com/sealed-secrets-key: active`
- **THEN** that metadata appears unchanged on the replica

#### Scenario: Operator-configured additional keys
- **WHEN** the operator configures additional metadata keys or key prefixes to strip
- **THEN** those keys are stripped from replicas and excluded from the hash in addition to the built-in set
