---
tags: [functionality]
---

# Cluster Discovery

Pluggable provider interface turns fleet-management systems into replication target inventory. A provider emits `ClusterRecord`s (stable name, labels, readiness, credential ref) plus register/update/deregister events. Adding Fleet/Rancher/static providers later touches nothing else.

## ClusterAPI provider (v1)

`internal/discovery/capi` watches `cluster.x-k8s.io/v1beta1 Cluster` objects **unstructured** (no CAPI module dependency — keeps go.mod lean, decouples from CAPI's k8s version pins):
- selection labels = the `Cluster` object's labels (used by [[replication-flow|`r8r.io/target-clusters`]] and [[policy-model|policy `clusterSelector`]])
- targetable only when control plane ready (`ControlPlaneReady` v1beta1 / `ControlPlaneAvailable` v1beta2)
- credentials from the conventional `<cluster>-kubeconfig` Secret

## Credential bootstrap (D5)

The CAPI kubeconfig is cluster-admin — never used for steady-state traffic. On registration, once:

```
admin kubeconfig ──▶ create on spoke: ns k8s-r8r-system, SA k8s-r8r,
                     narrow ClusterRole (only kinds/verbs the policy universe needs)
                └──▶ then: short-lived SA tokens (TokenRequest, refresh at 80% TTL)
```

Hub compromise blast radius drops from "fleet admin" to "write allowlisted kinds". RBAC re-narrows when the policy universe shrinks. Details: [[security-model]].

## Cluster runtimes

`internal/cluster.Manager` keeps exactly one runtime (client + cache) per ready cluster: started on register, stopped+drained on deregister. Connectivity tracked per cluster (`Reachable | Degraded | Unreachable{Since}`) with independent backoff — one dead spoke never blocks the rest. Runtimes carry the metadata-only caches used by [[drift-detection]]. Deregistration → inventory released with `ClusterGone` (see [[replication-flow]]).

Spec: `openspec/specs/cluster-discovery`
