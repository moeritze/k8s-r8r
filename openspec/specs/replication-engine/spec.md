# replication-engine

## Purpose

Defines the reconciliation core: fanning out source objects to permitted targets, detecting and correcting drift, handling conflicts with pre-existing objects, and garbage-collecting replicas so none are ever orphaned.

## Requirements

### Requirement: Replica creation and identity
For each permitted `(source, targetCluster, targetNamespace)` tuple the engine SHALL create or update a replica that is byte-identical to the source in payload, carries the labels `app.kubernetes.io/managed-by: k8s-r8r` plus source-reference labels (source cluster, namespace, name, UID), and the annotation `r8r.io/source-hash: sha256:<hash of source payload>`. The engine SHALL never mutate the source object beyond its own finalizer.

#### Scenario: Source updated
- **WHEN** the payload of a replicated Secret changes on the hub
- **THEN** all replicas are updated to the new payload and their source-hash annotations updated

#### Scenario: Source is never modified
- **WHEN** the engine reconciles a source
- **THEN** the only write it ever performs on the source is adding or removing the `r8r.io/finalizer` finalizer

### Requirement: Kind-agnostic pipeline
The engine SHALL operate on unstructured objects internally so any GVK can flow through it; the request-side allowlist is the only kind gate. Server-managed and identity fields (resourceVersion, UID, ownerReferences, status, creationTimestamp, managedFields, namespace, cluster-specific defaults) SHALL be stripped before writing replicas.

#### Scenario: Replica payload excludes server-managed fields
- **WHEN** a replica is created on a target cluster
- **THEN** it contains the source's payload and k8s-r8r metadata but no server-managed fields copied from the source

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

### Requirement: Drift detection via metadata watches
The engine SHALL maintain, per connected target cluster, metadata-only informers filtered to `app.kubernetes.io/managed-by: k8s-r8r` for each replicated GVK. Full replica payloads SHALL NOT be cached on the hub. A hash mismatch or replica deletion observed via watch SHALL enqueue the affected `(source, targetCluster)` for reconciliation. A periodic resync SHALL act as fallback for missed events.

#### Scenario: Replica edited on target
- **WHEN** someone modifies a replica directly on a spoke cluster (changing its source-hash annotation or payload)
- **THEN** the engine detects the mismatch via the metadata watch and restores the replica to match the source

#### Scenario: Replica deleted on target
- **WHEN** a replica is deleted on a spoke cluster while its source still requests replication there
- **THEN** the engine recreates it

### Requirement: Conflict handling
When the intended replica name already exists on a target and is not managed by k8s-r8r, the engine SHALL apply the effective conflict policy: `Fail` (default) — do not touch the object, set a per-target `Conflict` condition; `Overwrite` — take over the object, only when both the request asks for it and policy permits it; `Adopt` — take ownership without rewriting, only when the existing object's content hash equals the source hash.

#### Scenario: Unmanaged object with default policy
- **WHEN** a target namespace already contains an unmanaged Secret with the replica's name and the effective conflict policy is `Fail`
- **THEN** the existing object is untouched and the `Replication` status reports a conflict for that target

#### Scenario: Adopt on identical content
- **WHEN** the conflict policy is `Adopt` and the existing object's content hash equals the source hash
- **THEN** the engine adds its management labels and hash annotation to the object without changing its payload

### Requirement: Namespace ensuring
When a target namespace does not exist and the effective policy sets `allowNamespaceCreation: true`, the engine SHALL create it labeled `app.kubernetes.io/managed-by: k8s-r8r`. Namespaces created by the engine SHALL NOT be deleted by the engine during replica garbage collection.

#### Scenario: Missing namespace with creation allowed
- **WHEN** a permitted request targets a nonexistent namespace and policy allows creation
- **THEN** the namespace is created and the replica placed in it

### Requirement: Inventory and garbage collection
The engine SHALL record every created replica in the `Replication` object's inventory and SHALL delete replicas (honoring `revocationPolicy` where applicable) when: the source is deleted (a finalizer on the source blocks its deletion until replica cleanup completes), the request annotations are removed, or a target leaves the resolved selection (cluster label change, selector change, namespace removed from the list). Cluster deregistration releases that cluster's inventory entries with a `ClusterGone` event without deleting replicas on the spoke — after deregistration the engine holds no credential for the cluster, so remote deletion is impossible; the clean removal path is deselecting the cluster (label/selector change) before deregistering it. No code path may lose track of a created replica.

#### Scenario: Source deletion cleans the fleet
- **WHEN** a replicated Secret is deleted on the hub
- **THEN** the finalizer defers deletion until all inventoried replicas are removed from reachable targets, then the source and its `Replication` object are released

#### Scenario: Target leaves selection
- **WHEN** a cluster's labels change so it no longer matches the request's cluster selector
- **THEN** replicas on that cluster are deleted and removed from inventory

#### Scenario: Unreachable target during cleanup
- **WHEN** replicas must be cleaned from a cluster that is unreachable
- **THEN** cleanup is retried with backoff, the condition is reported, and after the cluster is deregistered from discovery the inventory entries are released with a `ClusterGone` event rather than blocking forever
