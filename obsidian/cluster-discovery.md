---
tags: [functionality]
---

# Cluster Discovery

Pluggable provider interface turns fleet-management systems into replication target inventory. A provider emits `ClusterRecord`s (stable name, labels, readiness, credential ref) plus register/update/deregister events. Adding Fleet/Rancher/static providers later touches nothing else.

## ClusterAPI provider (v1)

`internal/discovery/capi` watches `cluster.x-k8s.io Cluster` objects **unstructured** (no CAPI module dependency — keeps go.mod lean, decouples from CAPI's k8s version pins):
- selection labels = the `Cluster` object's labels (used by [[replication-flow|`r8r.io/target-clusters`]] and [[policy-model|policy `clusterSelector`]])
- targetable only when control plane ready (`ControlPlaneReady` v1beta1 / `ControlPlaneAvailable` v1beta2)
- credentials from the conventional `<cluster>-kubeconfig` Secret

## API version negotiation

The watched version is **not pinned**. On start the provider asks the hub's discovery API which versions of `clusters.cluster.x-k8s.io` are served and takes the first of `v1 → v1beta2 → v1beta1` that appears, logging the choice once:

```
Negotiated ClusterAPI discovery version  groupVersion=cluster.x-k8s.io/v1beta2 resource=clusters
```

The list is explicit rather than the group's `preferredVersion`, so the provider never binds to a CAPI version whose readiness-condition vocabulary it has not been validated against. Nothing else in the provider is version-bound: records read `ObjectMeta` plus `status.conditions[].type/.status`, identical in both condition sets.

Failure modes, deliberately different:
- **served, but at no supported version** (CAPI 1.16 drops `v1beta1` and this build is older) → the provider **fails to start** with `capi: clusters.cluster.x-k8s.io serves none of [v1 v1beta2 v1beta1] (served: [...])`. The manager stops and the pod restarts with the reason in its logs. Readiness is untouched — that gate is still hub-informers-only.
- **not served at all / hub unreachable** → retries every 30s, logs why, and reports `k8s_r8r_discovery_up=0` (see [[operations]]). ClusterAPI installed after the operator converges on its own.

Before this, the GVR was pinned to `v1beta1`: an unserved version returned 404, the reflector retried forever, `Start` hung in `WaitForCacheSync`, the pod stayed Ready, and the fleet looked empty. Issue #28.

## Provider settings — not wired up yet

`discovery.Options.Settings` (`internal/discovery/registry.go`) is a per-provider string map, and the CAPI provider reads `namespace` from it to restrict the `Cluster` watch to one namespace. Nothing populates it in a deployed operator: `cmd/main.go` constructs `discovery.New(name, discovery.Options{HubConfig: hubCfg})` and there is no flag or chart value that reaches the map. So in practice the provider always watches `Cluster` objects **cluster-wide**, and the settings mechanism only works from tests. It also removes the natural escape hatch for a version-negotiation failure above. Tracked: [#37](https://github.com/moeritze/k8s-r8r/issues/37).

## Credential bootstrap (D5)

The CAPI kubeconfig is cluster-admin — never used for steady-state traffic. On registration, once:

```
admin kubeconfig ──▶ create on spoke: ns k8s-r8r-system, SA k8s-r8r,
                     ClusterRole scoped to the --allowed-kinds allowlist
                └──▶ then: short-lived SA tokens (TokenRequest, refresh at 80% TTL)
```

Hub compromise blast radius drops from "fleet admin" to "write allowlisted kinds". Be precise about *which* allowlist: the grant (`RBACScope`, `internal/cluster/bootstrap.go`) is derived from the `--allowed-kinds` flag, **not** from the [[policy-model|policy universe]] — full replica verbs on each allowlisted kind, in every namespace, via ClusterRoleBinding. It therefore re-narrows only when `--allowed-kinds` shrinks and the pod restarts to replay bootstrap, never when a policy tightens. Sizing and the reasoning for deferring policy-derived scoping ([#29](https://github.com/moeritze/k8s-r8r/issues/29)): [[security-model]].

## Cluster runtimes

`internal/cluster.Manager` keeps exactly one runtime (client + cache) per ready cluster: started on register, stopped+drained on deregister. Connectivity tracked per cluster (`Reachable | Degraded | Unreachable{Since}`) with independent backoff — one dead spoke never blocks the rest. Runtimes carry the metadata-only caches used by [[drift-detection]]. Deregistration → inventory released with `ClusterGone` (see [[replication-flow]]).

Spec: `openspec/specs/cluster-discovery`
