---
tags: [functionality, operations]
---

# Operations

## Metrics (`internal/telemetry`, prefix `k8s_r8r_`)

Replica desired/ready/failed gauges (per cluster) · policy/webhook denial counters (per dimension) · conflict, revocation, drift counters · cluster connectivity gauge (0 unreachable / 1 degraded / 2 reachable) · bootstrap + token rotation counters. Reconcile durations/workqueue depth come from controller-runtime built-ins. Cardinality bounded by test: labels only cluster/namespace/kind/provider — never object names.

**Discovery health** (`k8s_r8r_discovery_up{provider}`, `k8s_r8r_discovery_clusters{provider}`) comes from the [[cluster-discovery|provider itself]], not from the runtime manager. `k8s_r8r_clusters` counts registered *runtimes* and reads 0 both when discovery is broken and when the fleet is genuinely empty — that ambiguity is what kept a total discovery outage invisible (#28). Read them together:

| `discovery_up` | `discovery_clusters` | meaning |
|---|---|---|
| 1 | 0 | discovery works, no clusters match |
| 1 | n | healthy |
| 0 | 0 | discovery is not running — alert on this |

## Troubleshooting

**Zero clusters discovered.** First check `k8s_r8r_discovery_up`. If it is 1, the fleet really has no `Cluster` objects the provider can see — and since the provider's namespace setting is unreachable in a deployed operator ([#37](https://github.com/moeritze/k8s-r8r/issues/37), [[cluster-discovery|provider settings]]) the watch is cluster-wide, so this means the hub genuinely has none. Not an operator fault. If it is 0, check the negotiated CAPI version in the operator log:

```sh
kubectl -n k8s-r8r-system logs deploy/k8s-r8r | grep -i 'ClusterAPI'
```

- `Negotiated ClusterAPI discovery version groupVersion=... ` — discovery started fine; look further downstream.
- `capi: clusters.cluster.x-k8s.io serves none of [v1 v1beta2 v1beta1] (served: [...])` — the hub's CAPI is newer (or older) than this build supports; the pod restarts on this. Upgrade k8s-r8r to a build that lists a served version.
- `ClusterAPI inventory unavailable, retrying` — no `clusters.cluster.x-k8s.io` on the hub at all (ClusterAPI not installed) or the hub is unreachable. The operator waits and converges once it appears.

Note the pod stays **Ready** through all of these: readiness reflects hub informer sync only, by design. Discovery health lives in the metric, not in the probe.

## Events

Lifecycle transitions on sources and Replications (Replicated, PolicyDenied, Conflict, PolicyRevoked, ClusterGone, CleanedUp), rate-limited per object+reason (5m cooldown, changed messages pass) so flapping targets can't flood. [[security-model|Never any payload data]].

Recorded through the **core `v1` Event API** (`mgr.GetEventRecorderFor`), not `events.k8s.io/v1`: only the core recorder fills `firstTimestamp`/`lastTimestamp`/`count`, so `kubectl get events --sort-by=.lastTimestamp` orders them correctly and client-go's correlator aggregates repeats into a real count series. Trade-off: no `action` / `reportingController` fields. The `events.k8s.io` RBAC grant is kept regardless (a missing grant fails silently with 403), ratcheted by a repo audit test.

## Status discipline (D8)

Summary counts + `Ready` condition; per-target detail only for non-ready targets, capped at 20 + overflow counter; status writes skipped when unchanged. 60-target e2e fanout: 13.9KB status, zero churn over 2 minutes. Full per-target truth lives in metrics/events.

## What you cannot alert on yet

Two blind spots to know about before wiring alerts; both are open issues with fixes in flight.

- **`Ready` does not mean "it replicated"** ([#27](https://github.com/moeritze/k8s-r8r/issues/27)). `Ready` is computed from the failed-target count, and policy-denied targets never enter the target list — so a request that replicated nothing reports `Ready=True, AllTargetsReady, 0/0 targets ready`, identical to a healthy fanout. A typo'd cluster selector, a policy tightened too far, and a working replication all present the same. Alert instead on the `PolicyDenied` / `PolicyRevoked` events and on `k8s_r8r_policy_denials_total` / `k8s_r8r_revocations_total`. Same reason an Argo CD Lua health check keyed on `Ready` would render a no-op replication healthy. Mechanism: [[replication-flow]].
- **A corrected drift is silent** ([#30](https://github.com/moeritze/k8s-r8r/issues/30)). No event, no condition, no distinguishable log line. `k8s_r8r_drift_events_total` is *not* the counter you want — it counts spoke informer events on managed replicas including the engine's own apply echoes, so it rises during normal fanout. There is currently no way to answer "has anything been tampering with my replicated secrets?" from the operator's output. Detail: [[drift-detection]].

## HA

Leader election (single writer, standby failover from persisted CRs/inventory). Readiness = hub informers synced only — a dead spoke can structurally never flip readiness ([[cluster-discovery|per-cluster runtimes]] isolate outages). Deployment hardened per restricted PSS; Helm chart supports `replicaCount > 1`.

## Install / uninstall

Released versions install from the published chart: `helm install k8s-r8r oci://ghcr.io/moeritze/charts/k8s-r8r --version <x.y.z>` — pulls the matching `ghcr.io/moeritze/k8s-r8r` image automatically ([[development|release process]], `../docs/releasing.md`). For development, the in-repo chart `charts/k8s-r8r` (webhook cert-manager toggle, personas, metrics). Teardown order matters: remove annotations/policies → replicas GC → `helm uninstall`; finalizer escape hatch documented in `../docs/uninstall.md`.

Spec: `openspec/specs/observability-operations`
