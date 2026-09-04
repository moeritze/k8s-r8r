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
    r8r.io/conflict-policy: "Adopt"           # Fail | Adopt | Overwrite; absent means Fail
```

Parsed by `internal/annotations` (shared with the [[security-model|webhook]]). Devs need zero new RBAC — you can only fan out what you can already write.

## 2. Materialization (request controller)

`internal/controller/request` resolves selector × [[cluster-discovery|discovered inventory]] × [[policy-model|policy verdict]] into one operator-owned **`Replication`** object per source (origin `Annotation`, designed to admit a future `ReplicationSet`). Its verdict on that resolution lands on the `TargetsResolved` condition (§5) and nowhere else — the controller never writes `Ready`. Hand-authored Replications get `NotAuthoritative` and are ignored. Finalizer `r8r.io/finalizer` on the source blocks its deletion until replica cleanup completes.

## 3. Fanout (engine)

`internal/engine` reconciles each `Replication` per target (workqueue key = source + targetCluster; independent backoff):
- payload stripped of server-managed fields, stamped with `app.kubernetes.io/managed-by: k8s-r8r`, source-ref labels, `r8r.io/source-hash: sha256:…`
- **metadata hygiene**: foreign ownership / replication-intent keys are stripped too — `replicator.v1.mittwald.de/*`, `reflector.v1.k8s.emberstack.com/*`, `argocd.argoproj.io/*`, `app.kubernetes.io/instance`, `meta.helm.sh/*`, `kustomize.toolkit.fluxcd.io/*`, plus anything in `--strip-metadata-keys`. A replica carrying a foreign replicator's annotation would be a valid source for *that* controller, i.e. a fanout no [[policy-model|policy]] evaluated — the [[security-model|default-deny]] claim depends on this. Argo CD tracking keys are the mirror case: they make a replica prunable. Denylist, never an allowlist — a replicated sealed-secrets key must keep its own labels to work.
- one predicate (`isStrippedKey`) feeds both the replica and the canonical hash. Stripping in only one of them would make `SourceHash(replica) != SourceHash(source)` forever → apply on every reconcile → [[drift-detection|drift]] event → enqueue → hot loop against every spoke.
- applied via server-side apply (field manager `k8s-r8r`) over the push `Transport` using the spoke's [[security-model|bootstrapped SA token]]
- a replica whose stored hash no longer matches is rewritten — and the write's two sub-cases are *not* the same event: diverged **content** is a reported [[drift-detection|drift correction]], a merely stale `r8r.io/source-hash` annotation over unchanged content is repaired silently. See [[operations]] for what each one emits.
- conflicts with pre-existing unmanaged objects: `Fail` (default) / `Adopt` (hash-equal only) / `Overwrite`, decided by the two-key turn below. An object already managed by k8s-r8r for a *different* source is always `Fail`, whatever either key says.
- missing namespace: created only when policy sets `allowNamespaceCreation`; namespaces are never GC'd

### Conflict handling is a two-key turn

`EffectiveConflictPolicy` (`internal/engine/conflict.go`) acts on the **weaker** of what the request asks for and what policy grants, ranked `Fail` < `Adopt` < `Overwrite`:

```
effective = min( r8r.io/conflict-policy , strongest allowedConflictPolicies grant )
```

- **Absent annotation means `Fail`** — a request that says nothing consents to nothing. Anything the engine cannot name ranks as `Fail` on either side.
- A `ReplicationPolicy` grant alone therefore never takes over an object k8s-r8r did not create; neither does an annotation alone.
- The `Conflict` message names *which* key did not turn (`explainConflictKeys`), so "the request never opted in" reads differently from "no policy ever granted it". It surfaces twice: the per-target entry in `status.nonReadyTargets` and the `Conflict` warning event.
- The [[security-model|webhook]] **denies** a malformed value (the key is part of the closed request contract) but only **warns** when no candidate policy could grant what was asked — such a request replicates fine and only its conflict handling falls back.

**This is a breaking change since `v0.1.0-alpha.1`.** A source that relied on a policy-granted `Overwrite` **or** `Adopt` with no annotation now reports `Conflict` instead of taking the object over. Nothing is deleted or overwritten by the change — the new failure mode is always "did not take over", never the reverse. Fix is one annotation per source. Delivered by [#34](https://github.com/moeritze/k8s-r8r/issues/34).

One conflict-path caveat still stands before granting anything above `Fail`:

- **`Adopt` is not as reversible as it reads** ([#35](https://github.com/moeritze/k8s-r8r/issues/35)). `Renderer.AdoptPatch` stamps `app.kubernetes.io/managed-by: k8s-r8r` onto the pre-existing object, which permanently breaks Helm's ownership check (`helm upgrade` then fails with `invalid ownership metadata`), and the adopted object joins the inventory below — so revocation with `revocationPolicy: Delete`, source deletion, or target deselection garbage-collects an object k8s-r8r never created. Both keys must now turn before this happens, which narrows the blast radius but does not close the issue.

## 4. Tracking and cleanup

Every replica lands in `Replication.status.inventory` — the GC source of truth. Cleanup triggers: source deleted, annotation removed, target deselected, policy revoked ([[policy-model#Revocation|revocation]]). Cluster deregistration releases inventory with a `ClusterGone` event (no credential remains to delete with) — deselect before deregistering for clean removal. Ongoing sync: [[drift-detection]].

## 5. Status: one writer per condition

Status is what monitoring reads — events expire — so every condition on a `Replication` has exactly **one** owner. Two writers on one condition is what [#27](https://github.com/moeritze/k8s-r8r/issues/27) was:

| Condition | Owner | Question it answers |
|---|---|---|
| `Ready` | replication engine (`buildStatus`, `internal/engine/status.go`) | Is everything the engine was asked to do actually done? |
| `TargetsResolved` | request controller (`reportTargetResolution`) | Did the request resolve to any target at all — and if not, why? |
| `NotAuthoritative` | authority reconciler (`replication_controller.go`) | Is this a hand-authored `Replication` the operator refuses to act on? |
| `PolicyRevoked` | replication engine | Is at least one target frozen under `revocationPolicy: Retain`? |

`TargetsResolved` has three values: `True`/`TargetsResolved` once at least one (cluster, namespace) pair survives the selector *and* policy; `False`/`PolicyDenied` when candidates existed and policy refused every one; `False`/`NoTargets` when the request produced no candidate at all — a selector matching no *ready* cluster. That last case was previously silent in every channel: target resolution returns before policy is consulted, so there was no denial, no event, and no condition, and a typo'd selector stayed green forever.

`Ready` is engine-owned and derived in `buildStatus` only. A **live** `Replication` with zero desired targets is now `Ready=False`/`NoTargets` — "asked to replicate, replicated nothing" is a failure, not a vacuous success. The branch is skipped while the object is being deleted, where zero desired targets is legitimate. `NoTargets` joins `PolicyDenied` as an event-worthy `Ready` transition, replacing the spurious "0/0 targets ready" success event the old ping-pong manufactured.

Why the split matters beyond truthfulness: the request controller also watches `Replication` objects, so when both controllers wrote `Ready` each write re-triggered the other — unbounded status churn on every denied `Replication`, bounded only by the rate limiter and in violation of design D8 ([[architecture]]). Alerting guidance: [[operations]].

Spec: `openspec/specs` (`replication-request`, `replication-engine`) · user docs: `../docs/annotations.md`
