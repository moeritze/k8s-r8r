---
tags: [functionality, operations]
---

# Operations

## Metrics (`internal/telemetry`, prefix `k8s_r8r_`)

Replica desired/ready/failed gauges (per cluster) · policy/webhook denial counters (per dimension) · conflict, revocation, drift counters · cluster connectivity gauge (0 unreachable / 1 degraded / 2 reachable) · bootstrap + token rotation counters. Reconcile durations/workqueue depth come from controller-runtime built-ins. Cardinality bounded by test: labels only cluster/namespace/kind/provider — never object names.

**The two drift counters answer different questions — do not conflate them.**

| Metric | Counts | Use |
|---|---|---|
| `k8s_r8r_drift_events_total{cluster}` | spoke informer events on managed replicas, **the engine's own apply echoes included** | watch traffic volume. Rises on every legitimate source update — *do not alert on it* |
| `k8s_r8r_drift_corrections_total{cluster}` | replicas whose **content** the engine actually rewrote | the tamper signal: non-zero always means a replica's payload was wrong |

A source update moves the first and not the second. A replica edited on a spoke moves both. Caveats (silent annotation-only repair, event coalescing): [[drift-detection]].

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

**Drift keeps recurring on one spoke.** The signature is *one* `DriftCorrected` event and a *climbing* `k8s_r8r_drift_corrections_total{cluster}` — the event coalesced, the counter did not. That pairing means the same replica is being rewritten over and over with the same content, which is a competing controller (another replicator, a GitOps tool reconciling the object back) or a human editing in a loop. Compare the `observed` hash in the event across occurrences: a stable observed hash points at a controller with its own desired state; a changing one points at hand edits. If the counter is flat but you expected corrections, check [#36](https://github.com/moeritze/k8s-r8r/issues/36) — a replica whose `managed-by` label was rewritten is invisible to drift detection entirely.

**A `Replication` reports `Conflict` after upgrading from `v0.1.0-alpha.1`.** Conflict handling is now a two-key turn ([[replication-flow#Conflict handling is a two-key turn|why]]): a policy grant alone no longer takes over a pre-existing object. The `Conflict` message names which key is missing — if it says the request does not set `r8r.io/conflict-policy`, add that annotation to the source.

## Events

Lifecycle transitions on sources and Replications (Replicated, NoTargets, PolicyDenied, Conflict, DriftCorrected, PolicyRevoked, ClusterGone, CleanedUp), rate-limited per object+reason (5m cooldown, changed messages pass) so flapping targets can't flood. [[security-model|Never any payload data]].

Two of these are newer than the rest:

- **`NoTargets`** (Warning, on the `Replication`) — the `Ready` transition for a request that resolved to nothing. It replaced the spurious "Replicated 0/0 targets ready" success event a denied request used to emit on every reconcile ([[replication-flow#5. Status: one writer per condition|condition ownership]]).
- **`DriftCorrected`** (Warning, on the `Replication`) — `restored replica <cluster>/<ns>/<name>: observed content sha256:…, expected sha256:…`. Only real content divergence; an annotation-only repair is silent by design.

The 5m cooldown is the contract, not a bug — but it means the event stream **understates the rate** of recurring drift, because identical repeats coalesce. Read the rate off `k8s_r8r_drift_corrections_total`, which is deliberately not rate-limited. Event = "this happened, here is which object"; metric = "and it is happening N times a minute".

Recorded through the **core `v1` Event API** (`mgr.GetEventRecorderFor`), not `events.k8s.io/v1`: only the core recorder fills `firstTimestamp`/`lastTimestamp`/`count`, so `kubectl get events --sort-by=.lastTimestamp` orders them correctly and client-go's correlator aggregates repeats into a real count series. Trade-off: no `action` / `reportingController` fields. The `events.k8s.io` RBAC grant is kept regardless (a missing grant fails silently with 403), ratcheted by a repo audit test.

## Status discipline (D8)

Summary counts + conditions; per-target detail only for non-ready targets, capped at 20 + overflow counter; status writes skipped when unchanged. 60-target e2e fanout: 13.9KB status, zero churn over 2 minutes. Full per-target truth lives in metrics/events.

"Zero churn" also depends on **one writer per condition** — `Ready` belongs to the engine, `TargetsResolved` to the request controller, `NotAuthoritative` to the authority reconciler. Two controllers writing one condition is not just an ambiguous verdict, it is a status-churn loop, because each write re-triggers the other's watch. Table and reasons: [[replication-flow#5. Status: one writer per condition|condition ownership]].

## What to alert on

- **`Ready=False` on a `Replication`** is now the primary signal, and it is truthful: a request that asked for replication and got none reports `Ready=False`/`NoTargets`, whether the cause was a denial, a revocation, or a typo'd selector. Replicating nothing is no longer a vacuous success. An Argo CD Lua health check keyed on `Ready` is therefore safe to write.
- **`TargetsResolved=False`** says *why* nothing resolved: `PolicyDenied` (candidates existed, policy refused them — fix the policy or the request) versus `NoTargets` (the `r8r.io/target-clusters` selector matched no *ready* cluster; policy was never consulted, so there is no denial event to look for).
- **`k8s_r8r_drift_corrections_total` rising** — a replica's payload was actually wrong and got restored. Not `k8s_r8r_drift_events_total`, which rises during normal fanout.
- Still useful as corroboration: the `PolicyDenied` / `PolicyRevoked` events and `k8s_r8r_policy_denials_total` / `k8s_r8r_revocations_total`.

*Upgrading from `v0.1.0-alpha.1`:* Replications that reported `Ready: True` with `0/0 targets ready` flip to `Ready: False` on the first reconcile after upgrade. Nothing about what is replicated changes — only whether the object admits it.

## What you still cannot alert on

- **A replica whose `managed-by` label was rewritten** ([#36](https://github.com/moeritze/k8s-r8r/issues/36)). It leaves the label-filtered spoke cache, so it never reaches a reconcile: no drift event, no correction counter, and status keeps reporting the target ready. The correction signal above cannot cover it — an object the engine never looks at is never counted. Detail: [[drift-detection]].
- **The gap between the spoke SA's grant and the policy universe** ([#29](https://github.com/moeritze/k8s-r8r/issues/29)). Nothing reports how much wider the bootstrapped RBAC is than what any policy permits; the only lever is the `--allowed-kinds` flag. Detail: [[security-model]].

## HA

Leader election (single writer, standby failover from persisted CRs/inventory). Readiness = hub informers synced only — a dead spoke can structurally never flip readiness ([[cluster-discovery|per-cluster runtimes]] isolate outages). Deployment hardened per restricted PSS; Helm chart supports `replicaCount > 1`.

## Install / uninstall

Released versions install from the published chart: `helm install k8s-r8r oci://ghcr.io/moeritze/charts/k8s-r8r --version <x.y.z>` — pulls the matching `ghcr.io/moeritze/k8s-r8r` image automatically ([[development|release process]], `../docs/releasing.md`). For development, the in-repo chart `charts/k8s-r8r` (webhook cert-manager toggle, personas, metrics). Teardown order matters: remove annotations/policies → replicas GC → `helm uninstall`; finalizer escape hatch documented in `../docs/uninstall.md`.

Spec: `openspec/specs/observability-operations`
