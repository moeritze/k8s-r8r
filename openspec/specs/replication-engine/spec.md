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

A corrective write SHALL be recorded, not only performed: when the engine rewrites a replica because its content hash diverged from the source, it SHALL emit an event identifying the replica and both hashes, and SHALL increment the drift-correction metric for that target cluster, so that a corrective write is distinguishable from a no-op reconcile.

Recording SHALL be scoped to content divergence. When a replica's content already matches the source and only the engine's own source-hash annotation is stale, the engine SHALL repair the annotation without emitting a drift event or incrementing the drift-correction metric — that state is produced fleet-wide by a change to the hashing rules, and reporting it would make the correction signal unusable as a tamper indicator.

#### Scenario: Replica edited on target
- **WHEN** someone modifies a replica's payload directly on a spoke cluster
- **THEN** the engine detects the mismatch via the metadata watch, restores the replica to match the source, and records the correction as an event and a metric increment

#### Scenario: Replica's source-hash annotation edited on target
- **WHEN** a replica's `r8r.io/source-hash` annotation is stale but its content still hashes equal to the source
- **THEN** the engine repairs the annotation and reports no drift correction, because no replicated content had diverged

#### Scenario: Replica deleted on target
- **WHEN** a replica is deleted on a spoke cluster while its source still requests replication there
- **THEN** the engine recreates it

### Requirement: Replica ownership verification
The `app.kubernetes.io/managed-by: k8s-r8r` label is the membership predicate of the spoke drift watch: an inventoried replica that loses it is invisible to the watch, so drift detection SHALL NOT depend on that label alone as evidence of ownership.

The engine SHALL classify an object found at an inventoried replica's name by both of its ownership marks and SHALL distinguish three states: the object carries the managed-by label and this replication's `r8r.io/source-uid` (managed); the object carries a matching `r8r.io/source-uid` but not the managed-by label (ownership stripped); the object carries neither (foreign).

An object whose ownership is stripped SHALL be treated as this replication's replica and SHALL NOT be classified as a conflict with a foreign object. The engine SHALL restore its ownership marks and content, SHALL retain its inventory entry, and SHALL report the repair. Restoring the managed-by label returns the object to the watch's label selector, so a repaired replica SHALL again be observable by the drift watch without further action.

A foreign object SHALL NOT be repaired or taken over by ownership verification; it remains subject to the conflict-handling requirement and its two-key consent contract.

Verification SHALL be performed with live reads independent of the informer, and SHALL NOT require reads beyond those the reconcile already performs for its desired slots and its garbage-collection candidates. Revoked replicas retained under `revocationPolicy: Retain` are excluded from verification, because the engine has undertaken not to write to them.

The worst-case time to detect a stripped ownership label is one resync interval, since the watch cannot deliver events for an object outside its selector.

#### Scenario: Ownership label rewritten on a replica
- **WHEN** something on a spoke rewrites or removes `app.kubernetes.io/managed-by` on a replica while its `r8r.io/source-uid` still identifies this replication's source
- **THEN** the next reconcile restores the replica's ownership marks and content, keeps its inventory entry, reports the repair as an event and a metric increment, and the replica is delivered by the drift watch again

#### Scenario: Ownership label rewritten and content edited
- **WHEN** a replica has both lost its managed-by label and had its payload rewritten on the spoke
- **THEN** the engine reports both an ownership repair and a drift correction, so neither signal is masked by the other

#### Scenario: Foreign object at a replica's name
- **WHEN** an object at an inventoried replica's name carries neither this replication's managed-by label nor its source-uid label
- **THEN** ownership verification does not touch it and the conflict-handling requirement decides the outcome

#### Scenario: Retained replica after revocation
- **WHEN** a replica is retained under `revocationPolicy: Retain` and its ownership label is later rewritten
- **THEN** the engine does not repair it, because a retained replica is no longer written to by the engine

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

### Requirement: Namespace ensuring
When a target namespace does not exist and the effective policy sets `allowNamespaceCreation: true`, the engine SHALL create it labeled `app.kubernetes.io/managed-by: k8s-r8r`. Namespaces created by the engine SHALL NOT be deleted by the engine during replica garbage collection.

#### Scenario: Missing namespace with creation allowed
- **WHEN** a permitted request targets a nonexistent namespace and policy allows creation
- **THEN** the namespace is created and the replica placed in it

### Requirement: Inventory and garbage collection
The engine SHALL record every created replica in the `Replication` object's inventory and SHALL delete replicas (honoring `revocationPolicy` where applicable) when: the source is deleted (a finalizer on the source blocks its deletion until replica cleanup completes), the request annotations are removed, or a target leaves the resolved selection (cluster label change, selector change, namespace removed from the list). Cluster deregistration releases that cluster's inventory entries with a `ClusterGone` event without deleting replicas on the spoke — after deregistration the engine holds no credential for the cluster, so remote deletion is impossible; the clean removal path is deselecting the cluster (label/selector change) before deregistering it. No code path may lose track of a created replica.

Before deleting, the engine SHALL verify that the object at an inventory entry's name is one it created, so that a planned-but-never-applied entry can never delete an object the engine does not manage. That verification SHALL use both ownership marks: an object whose managed-by label was rewritten but whose `r8r.io/source-uid` still matches this replication's source is still this replication's replica and SHALL be deleted.

An inventory entry SHALL NOT be released silently. When the engine drops an entry without deleting the corresponding object — because the object at that name is foreign and must not be touched — it SHALL emit an event identifying the replica and stating that the object may require manual cleanup, and SHALL increment a metric for it, so that a replica the engine can no longer account for is observable rather than merely absent.

#### Scenario: Source deletion cleans the fleet
- **WHEN** a replicated Secret is deleted on the hub
- **THEN** the finalizer defers deletion until all inventoried replicas are removed from reachable targets, then the source and its `Replication` object are released

#### Scenario: Target leaves selection
- **WHEN** a cluster's labels change so it no longer matches the request's cluster selector
- **THEN** replicas on that cluster are deleted and removed from inventory

#### Scenario: Unreachable target during cleanup
- **WHEN** replicas must be cleaned from a cluster that is unreachable
- **THEN** cleanup is retried with backoff, the condition is reported, and after the cluster is deregistered from discovery the inventory entries are released with a `ClusterGone` event rather than blocking forever

#### Scenario: Cleanup of a replica whose ownership label was rewritten
- **WHEN** an inventoried replica whose managed-by label was rewritten must be cleaned up
- **THEN** it is deleted from the spoke, because its source-uid label still identifies it as this replication's replica, and the deletion is reported

#### Scenario: Inventory entry pointing at a foreign object
- **WHEN** the object at an inventory entry's name carries none of this replication's ownership marks
- **THEN** the engine deletes nothing, releases the entry, and reports the release with an event and a metric rather than dropping the entry silently
