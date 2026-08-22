# Proposal: Bootstrap k8s-r8r — Kubernetes object replication operator

## Why

Platform teams need to fan out Kubernetes objects (primarily Secrets, also ConfigMaps) across namespaces and across a fleet of clusters — declaratively, GitOps-friendly, and policy-gated. Existing tools cover only fragments: in-cluster annotation replicators (mittwald/kubernetes-replicator, emberstack/reflector) have no cross-cluster support and no policy layer; External Secrets requires an external store; fleet tools (Fleet, Sveltos, ACM) are heavyweight app-delivery platforms, not "replicate this one Secret". The intersection — cross-cluster object fanout + pluggable cluster discovery (ClusterAPI first) + admin policy gating — is unowned. k8s-r8r fills it.

## What Changes

Greenfield: bootstrap the entire operator. Architecture principle agreed during exploration: **interfaces and data model are final from day one; feature surface ships narrow.** Later features (generic GVKs, selector-based requests, pull transport, more discovery providers) must be additions, never rewrites.

- New Go operator built on controller-runtime/kubebuilder; CRDs as primary API.
- Annotation-based replication requests on source objects (developer UX), materialized into an operator-owned canonical `Replication` object per source (status, inventory, orphan tracking).
- `ReplicationPolicy` cluster-scoped CRD as the security boundary: default deny, allowlist-only, union semantics across policies.
- Internal pipeline is dynamic/unstructured (any GVK); launch allowlist limited to Secrets + ConfigMaps.
- Pluggable `Discovery` interface; first provider: ClusterAPI (Cluster objects + kubeconfig Secrets).
- Pluggable `Transport` interface; first (and v1 only) implementation: push from hub. Spoke access via bootstrapped narrow ServiceAccount, not raw admin kubeconfig.
- Drift detection via per-target-cluster metadata-only filtered informers + source-hash annotations + periodic resync fallback.
- Validating admission webhook (CEL matchConditions, failurePolicy Ignore) as advisory UX; reconcile-time policy check is authoritative enforcement.
- Conflict handling on targets: `Fail` (default) / `Overwrite` (policy-gated) / `Adopt` (hash match only).
- Garbage collection: finalizers + inventory; no orphaned replicas on source deletion, annotation removal, or cluster deregistration.
- Operational baseline from day one: leader election, Prometheus metrics, structured events (no secret data, hashes only), size-capped status, Helm chart, CI with envtest.

## Capabilities

### New Capabilities

- `replication-request`: how users request replication (annotations, target selection, per-target name override) and how requests materialize into canonical `Replication` objects with status/inventory.
- `replication-policy`: admin policy model — default deny, allowlists (source namespaces/kinds, target clusters/namespaces), union semantics, options (namespace creation, conflict policy, revocation), reconcile-time authoritative enforcement.
- `replication-engine`: reconciliation core — dynamic pipeline, fanout, drift detection via metadata watches + hash comparison, conflict handling, GC/orphan tracking, namespace ensuring.
- `cluster-discovery`: pluggable discovery interface; ClusterAPI provider — cluster registration/deregistration lifecycle, label-based cluster targeting, credential bootstrap (narrow spoke ServiceAccount).
- `admission-validation`: advisory validating webhook — CEL-scoped, failurePolicy Ignore, early feedback on disallowed requests.
- `observability-operations`: metrics, events, status size discipline, leader election, HA posture.

### Modified Capabilities

None (greenfield project, no existing specs).

## Impact

- New repository content: Go module, kubebuilder project layout, CRD APIs (`r8r.io/v1alpha1`: `Replication`, `ReplicationPolicy`), controllers, webhook, Helm chart, CI pipeline, documentation.
- Dependencies: controller-runtime, ClusterAPI API types (discovery provider), Prometheus client.
- Security posture: operator ServiceAccount reads allowlisted kinds + CAPI kubeconfig Secrets (crown jewels — isolated namespace, tight RBAC); replication grants developers zero new privileges (can only fan out what they can already write; policy gates destinations).
- GitOps: replicas carry managed-by labels + source refs so ArgoCD can ignore them; source objects remain fully GitOps-managed; adoption cost is one annotation.
