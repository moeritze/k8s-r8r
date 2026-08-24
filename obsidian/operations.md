---
tags: [functionality, operations]
---

# Operations

## Metrics (`internal/telemetry`, prefix `k8s_r8r_`)

Replica desired/ready/failed gauges (per cluster) · policy/webhook denial counters (per dimension) · conflict, revocation, drift counters · cluster connectivity gauge (0 unreachable / 1 degraded / 2 reachable) · bootstrap + token rotation counters. Reconcile durations/workqueue depth come from controller-runtime built-ins. Cardinality bounded by test: labels only cluster/namespace/kind — never object names.

## Events

Lifecycle transitions on sources and Replications (Replicated, PolicyDenied, Conflict, PolicyRevoked, ClusterGone, CleanedUp), rate-limited per object+reason (5m cooldown, changed messages pass) so flapping targets can't flood. [[security-model|Never any payload data]].

## Status discipline (D8)

Summary counts + `Ready` condition; per-target detail only for non-ready targets, capped at 20 + overflow counter; status writes skipped when unchanged. 60-target e2e fanout: 13.9KB status, zero churn over 2 minutes. Full per-target truth lives in metrics/events.

## HA

Leader election (single writer, standby failover from persisted CRs/inventory). Readiness = hub informers synced only — a dead spoke can structurally never flip readiness ([[cluster-discovery|per-cluster runtimes]] isolate outages). Deployment hardened per restricted PSS; Helm chart supports `replicaCount > 1`.

## Install / uninstall

Released versions install from the published chart: `helm install k8s-r8r oci://ghcr.io/moeritze/charts/k8s-r8r --version <x.y.z>` — pulls the matching `ghcr.io/moeritze/k8s-r8r` image automatically ([[development|release process]], `../docs/releasing.md`). For development, the in-repo chart `charts/k8s-r8r` (webhook cert-manager toggle, personas, metrics). Teardown order matters: remove annotations/policies → replicas GC → `helm uninstall`; finalizer escape hatch documented in `../docs/uninstall.md`.

Spec: `openspec/specs/observability-operations`
