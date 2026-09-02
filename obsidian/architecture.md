---
tags: [architecture]
---

# Architecture

Hub-spoke, push-based. Everything runs on the hub; spokes only receive replicas via narrowly-scoped ServiceAccounts.

```
                     HUB CLUSTER
 annotated Secret ──▶ request controller ──▶ Replication (canonical CR)
                                                  │ gated by
                                          ReplicationPolicy (default deny)
                                                  │
        Discovery (CAPI) ──▶ cluster runtimes ──▶ push Transport
             │                    (1 per ready cluster)
             └─ kubeconfig ──▶ SA bootstrap (once) ──▶ token rotation
                                                  │
                                     spokes: replicas + metadata-only watches
```

## Design principle

**Interfaces and data model final from day one; feature surface narrow.** Generic-GVK pipeline exists internally (allowlist gates it to Secrets/ConfigMaps); `Transport` and `Discovery` are interfaces with one implementation each; the canonical [[replication-flow|Replication layer]] admits future request origins (`ReplicationSet`).

## Design decisions (openspec design.md)

| # | Decision | Why (short) |
|---|---|---|
| D1 | Annotation shim over canonical CRD | cert-manager pattern; status+inventory need a home |
| D2 | Push transport first | CAPI gives creds free; pull agent later slot |
| D3 | Metadata-only drift watches | no secret data cached on hub — see [[drift-detection]] |
| D4 | Default deny, union, allowlists only | deny-precedence kills policy engines — see [[policy-model]] |
| D5 | Narrow spoke SA bootstrap | blast radius; retrofit never happens — see [[security-model]] |
| D6 | Webhook advisory, controller authoritative | `failurePolicy: Fail` would block secret writes |
| D7 | Conflicts Fail/Overwrite/Adopt, no auto-rename | silent rename breaks name-based consumers — caveats in [[replication-flow]] |
| D8 | Capped status | etcd 1.5MB limit, no churn — see [[operations]] |
| D9 | Workqueue keyed (source, targetCluster) | slow cluster never blocks fanout; sharding-ready |
| D10 | kubebuilder + controller-runtime, `r8r.io/v1alpha1` | ecosystem standard |

## Packages

`api/v1alpha1` (CRDs) · `internal/annotations` (contract parser) · `internal/policy` (evaluation) · `internal/discovery` + `internal/discovery/capi` · `internal/cluster` (bootstrap, tokens, runtimes) · `internal/controller/request` (shim) · `internal/engine` (fanout/drift/GC) · `internal/webhook` · `internal/telemetry` · `cmd/main.go` (wiring). Full symbol-level map: `graph/graph.canvas`.
