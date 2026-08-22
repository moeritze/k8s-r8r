# Design: k8s-r8r operator

## Context

Greenfield Go project (see proposal.md — Why). Governing principle agreed up front: **interfaces and data model are final from day one; feature surface ships narrow.** Anything we know is needed for scale or hardening later is built into the architecture now; later features must be additions, not rewrites. Requirements live in the capability specs; this document records how and why.

## Goals / Non-Goals

**Goals:**
- Ceiling architecture from the start: dynamic (unstructured) pipeline, pluggable discovery and transport, watch-based drift detection, canonical `Replication` layer, minimal-privilege spoke credentials.
- Security boundary that survives review: default-deny policy, authoritative reconcile-time enforcement, documented threat model for the push architecture.
- GitOps/ArgoCD-clean behavior on both hub and spokes.

**Non-Goals (this change):**
- `ReplicationSet` selector CRD (canonical layer is built ready for it).
- Pull-agent transport (interface slot exists; documented as future work with the security reasoning for push-first).
- Discovery providers beyond ClusterAPI.
- Generic GVK enablement beyond Secrets/ConfigMaps (pipeline supports it; allowlist gates it).
- Multi-hub topologies; sharding across operator instances (workqueue keying prepared for it).

## Architecture Overview

```
                       HUB CLUSTER
  ┌─────────────────────────────────────────────────────────┐
  │  annotated Secret/CM ──▶ request controller             │
  │                              │ materializes             │
  │                              ▼                          │
  │                    Replication (canonical CR)           │
  │                              │ gated by                 │
  │                    ReplicationPolicy (union, deny-def)  │
  │                              │                          │
  │   Discovery (CAPI) ──▶ cluster runtimes ──▶ Transport   │
  │        │                (1 per ready cluster)  (push)   │
  │        └── kubeconfig ──▶ SA bootstrap (once)           │
  └──────────────────────────────┬──────────────────────────┘
                                 │ SA token, narrow RBAC
                     ┌───────────┼───────────┐
                     ▼           ▼           ▼
                 spoke A      spoke B     spoke C
              (replicas + metadata-only watches back to hub)
```

## Decisions

### D1: Annotation shim over canonical CRD (vs annotation-only or CRD-only)
Annotation is the developer entry point (zero-friction adoption, no new RBAC); every request materializes an operator-owned `Replication` CR carrying spec (resolved targets), status, and replica inventory. Precedent: cert-manager ingress-shim over `Certificate`. Rejected: annotation-only (no status home, no inventory home, no selector future); CRD-only UX (adoption friction, heavier for the common case). The `Replication` origin field admits future request kinds (`ReplicationSet`).

### D2: Push transport first, behind a `Transport` interface
CAPI hands us discovery and credentials; push is dramatically simpler than agent lifecycle + hub API. Security cost (hub holds fleet write access) is mitigated by D5 and documented as an explicit threat model. Pull agents remain a future `Transport` implementation. Rejected for v1: pull (bigger surface: agent shipping, upgrades, hub API, registration protocol).

### D3: Drift detection via per-cluster metadata-only filtered informers
One informer per (cluster, replicated GVK), `PartialObjectMetadata`, label-filtered to `app.kubernetes.io/managed-by: k8s-r8r`. Drift detected by comparing `r8r.io/source-hash` annotation; full object fetched only during reconcile. Periodic resync as fallback. Why: event-driven convergence at fleet scale; hub never caches spoke secret payloads (security + memory in one move). Rejected: polling (slow convergence, O(clusters×replicas×interval) load); full-object informers (memory, secret data cached on hub).

### D4: Policy = default deny, allowlists only, union across policies
No deny rules; deny-precedence logic is complexity that kills policy engines. Union matches the NetworkPolicy mental model. All matching dimensions must be satisfied by a single policy (no cross-policy dimension mixing). Enforcement is at reconcile time — every reconcile re-evaluates; policy tightening triggers revocation per `revocationPolicy`. Admission webhook (D6) is UX only.

### D5: Minimal-privilege spoke credentials, bootstrapped once
CAPI kubeconfigs are cluster-admin; using them for steady-state traffic makes the hub a fleet-admin credential store. Instead: use the kubeconfig once per cluster to bootstrap a dedicated namespace + ServiceAccount + narrow RBAC (scoped to what the policy universe needs), then operate only with short-lived rotated SA tokens. Hub compromise blast radius drops from "fleet admin" to "write allowlisted kinds in allowlisted namespaces". Built v1 — retrofitting credential minimization never happens in practice.

### D6: Webhook advisory (`failurePolicy: Ignore`), controller authoritative
`failurePolicy: Fail` would make operator downtime block all secret writes cluster-wide — unacceptable. Therefore the webhook is strictly UX (apply-time error messages), scoped via CEL matchConditions to annotated objects only, and its absence can never cause unauthorized replication because the controller re-checks policy on every reconcile. This split must be stated in the security docs.

### D7: Conflict policy `Fail | Overwrite | Adopt`, no automatic renaming
Overwrite is weaponizable (replace a victim cluster's existing secret) → policy-gated and request-explicit. Adopt only on content-hash equality. Automatic pre/suffix renaming rejected: consumers mount secrets by name; a silent rename breaks workloads with no error surfaced anywhere. Explicit `r8r.io/target-name` override exists for deliberate, git-visible renames.

### D8: Status size discipline
Per-target detail only for non-ready targets, capped with overflow count; summary counts otherwise; skip status writes when unchanged. Why: 1000-cluster fanout with full per-target conditions produces megabyte statuses (etcd ~1.5MB object limit) and constant churn. Full per-target truth lives in metrics/events.

### D9: Workqueue keyed `(source, targetCluster)`
Per-target keys mean one slow/unreachable cluster backs off independently without blocking the rest of the fanout, and future sharding-by-cluster requires no re-keying.

### D10: Stack
Go, kubebuilder + controller-runtime, CRD group `r8r.io/v1alpha1` (`Replication`, `ReplicationPolicy`), CAPI API types for the discovery provider, Helm chart as the deliverable packaging, envtest + kind-based e2e in CI. Conventional controller-runtime multi-cluster pattern: a `cluster.Cluster` per spoke managed by the discovery-driven runtime manager.

## Risks / Trade-offs

- [Hub is a high-value target: it can write secrets fleet-wide] → D5 narrow SAs; kubeconfig Secrets isolated (dedicated namespace, tight RBAC); threat model documented; audit-friendly events.
- [Watch connection per cluster per GVK may strain hub egress/API servers at large fleet size] → metadata-only watches are cheap; connection counts surfaced in metrics; sharding path prepared via D9.
- [Finalizer on sources can block deletion when spokes are unreachable] → bounded retries + `ClusterGone` release once discovery deregisters the cluster; documented escape hatch (manual finalizer removal) with consequences.
- [CEL matchConditions and short-lived token APIs assume reasonably current Kubernetes] → declare a minimum supported version early (matchConditions stable in 1.30); document it.
- [Adopt/Overwrite semantics on immutable fields (e.g. Secret `type`)] → strip/normalize immutable fields; on immutability conflict fall back to delete+recreate only under `Overwrite`; covered by e2e tests.
- [Union-only policy may prove too coarse for big orgs (no exceptions)] → accepted for now; revisit only with concrete demand; adding constructs later is additive.
- [Name `r8r.io` domain/group assumed] → verify domain availability before public release; group rename after release is breaking, so settle before v0.1.0.

## Migration Plan

Greenfield — no migration. Deployment path: Helm chart installs CRDs, operator (leader-elected), webhook with CEL matchConditions + `Ignore`. Rollback = uninstall chart; replicas remain (labeled, discoverable) unless explicitly cleaned; document a `helm uninstall` pre-step that removes finalizers/replicas for clean teardown. CRD versioning starts at `v1alpha1` with conversion-webhook-ready structure (no breaking field reuse; additive evolution policy documented).

## Open Questions

- Exact metric names/label sets (finalize during implementation against the observability spec).
- Token refresh cadence and bootstrap re-run triggers (on policy-universe change vs periodic) — safe to tune later.
- Whether namespace-ensure should support labels/annotations templating for created namespaces — additive if wanted.
