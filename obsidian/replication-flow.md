---
tags: [functionality]
---

# Replication Flow

The core path from developer intent to replicas on the fleet.

## 1. Request (developer)

Annotate any allowlisted object (Secret, ConfigMap):

```yaml
metadata:
  annotations:
    r8r.io/replicate: "true"
    r8r.io/target-clusters: "env=prod"        # label selector over cluster inventory; empty = NO clusters, no wildcard
    r8r.io/target-namespaces: "istio-system"  # default: source namespace
    r8r.io/target-name: "ca-cert"             # optional explicit rename — never automatic
```

Parsed by `internal/annotations` (shared with the [[security-model|webhook]]). Devs need zero new RBAC — you can only fan out what you can already write.

## 2. Materialization (request controller)

`internal/controller/request` resolves selector × [[cluster-discovery|discovered inventory]] × [[policy-model|policy verdict]] into one operator-owned **`Replication`** object per source (origin `Annotation`, designed to admit a future `ReplicationSet`). Hand-authored Replications get `NotAuthoritative` and are ignored. Finalizer `r8r.io/finalizer` on the source blocks its deletion until replica cleanup completes.

## 3. Fanout (engine)

`internal/engine` reconciles each `Replication` per target (workqueue key = source + targetCluster; independent backoff):
- payload stripped of server-managed fields, stamped with `app.kubernetes.io/managed-by: k8s-r8r`, source-ref labels, `r8r.io/source-hash: sha256:…`
- **metadata hygiene**: foreign ownership / replication-intent keys are stripped too — `replicator.v1.mittwald.de/*`, `reflector.v1.k8s.emberstack.com/*`, `argocd.argoproj.io/*`, `app.kubernetes.io/instance`, `meta.helm.sh/*`, `kustomize.toolkit.fluxcd.io/*`, plus anything in `--strip-metadata-keys`. A replica carrying a foreign replicator's annotation would be a valid source for *that* controller, i.e. a fanout no [[policy-model|policy]] evaluated — the [[security-model|default-deny]] claim depends on this. Argo CD tracking keys are the mirror case: they make a replica prunable. Denylist, never an allowlist — a replicated sealed-secrets key must keep its own labels to work.
- one predicate (`isStrippedKey`) feeds both the replica and the canonical hash. Stripping in only one of them would make `SourceHash(replica) != SourceHash(source)` forever → apply on every reconcile → [[drift-detection|drift]] event → enqueue → hot loop against every spoke.
- applied via server-side apply (field manager `k8s-r8r`) over the push `Transport` using the spoke's [[security-model|bootstrapped SA token]]
- conflicts with pre-existing unmanaged objects: `Fail` (default) / `Overwrite` (policy-gated) / `Adopt` (hash-equal only). An object already managed by k8s-r8r for a *different* source is always `Fail`, whatever the grant.
- missing namespace: created only when policy sets `allowNamespaceCreation`; namespaces are never GC'd

Two conflict-path caveats worth knowing before granting anything above `Fail`:

- **`Overwrite` is one key, not two** ([#34](https://github.com/moeritze/k8s-r8r/issues/34)). `EffectiveConflictPolicy` (`internal/engine/conflict.go`) takes only the policy grant — there is no request-side conflict annotation to intersect with, despite `docs/security.md` and the engine spec describing a two-key turn. Granting `Overwrite` in a `ReplicationPolicy` grants it to every request that policy permits.
- **`Adopt` is not as reversible as it reads** ([#35](https://github.com/moeritze/k8s-r8r/issues/35)). `Renderer.AdoptPatch` stamps `app.kubernetes.io/managed-by: k8s-r8r` onto the pre-existing object, which permanently breaks Helm's ownership check (`helm upgrade` then fails with `invalid ownership metadata`), and the adopted object joins the inventory below — so revocation with `revocationPolicy: Delete`, source deletion, or target deselection garbage-collects an object k8s-r8r never created.

## 4. Tracking and cleanup

Every replica lands in `Replication.status.inventory` — the GC source of truth. Cleanup triggers: source deleted, annotation removed, target deselected, policy revoked ([[policy-model#Revocation|revocation]]). Cluster deregistration releases inventory with a `ClusterGone` event (no credential remains to delete with) — deselect before deregistering for clean removal. Ongoing sync: [[drift-detection]].

`Ready` on the `Replication` currently answers "did any target fail?", not "did what I asked for happen?". Denied targets are excluded from `spec.resolvedTargets` rather than reported as failures, so a fully denied request has *no* targets: the request controller does set `Ready=False, PolicyDenied` (`reportDenial`), but the engine's `buildStatus` recomputes `Ready` from the failed-target count on its next pass and — with `failed == 0` — overwrites it with `Ready=True, AllTargetsReady, 0/0 targets ready`. In status a fully denied request is then indistinguishable from a healthy fanout. The durable signals are the `PolicyDenied` event on the source and `k8s_r8r_policy_denials_total`; events expire, and status is what monitoring reads. Tracked and being fixed: [#27](https://github.com/moeritze/k8s-r8r/issues/27); see [[operations]].

Spec: `openspec/specs` (`replication-request`, `replication-engine`) · user docs: `../docs/annotations.md`
