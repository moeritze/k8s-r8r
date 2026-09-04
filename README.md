# k8s-r8r

[![CI](https://img.shields.io/github/actions/workflow/status/moeritze/k8s-r8r/ci.yml?branch=main&label=CI)](https://github.com/moeritze/k8s-r8r/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/moeritze/k8s-r8r?include_prereleases&label=release)](https://github.com/moeritze/k8s-r8r/releases)
[![license: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/moeritze/k8s-r8r)](https://goreportcard.com/report/github.com/moeritze/k8s-r8r)
![status: alpha](https://img.shields.io/badge/status-alpha-orange)

**Kubernetes object replication operator**: fan out Secrets and ConfigMaps
across namespaces and across a fleet of clusters — declaratively,
GitOps-friendly, and policy-gated.

Platform teams keep rebuilding the same missing piece: in-cluster
annotation replicators (kubernetes-replicator, reflector) have no
cross-cluster support and no policy layer; External Secrets requires an
external store; fleet tools (Fleet, Sveltos, ACM) are heavyweight
app-delivery platforms, not "replicate this one Secret". k8s-r8r fills
the unowned intersection: **cross-cluster object fanout + pluggable
cluster discovery (ClusterAPI first) + admin policy gating.**

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: registry-creds
  namespace: platform
  annotations:
    r8r.io/replicate: "true"
    r8r.io/target-clusters: "env=dev"
```

That's the whole developer API. An admin-owned, default-deny
`ReplicationPolicy` decides whether it is allowed to happen.

## Status

**v0 / alpha.** The architecture is deliberately final (interfaces and
data model are designed not to change); the feature surface ships narrow.

**What is tested.** Unit and envtest coverage spans the controllers,
policy engine, and webhook. The cross-cluster path is covered by an
end-to-end suite that provisions a real kind fleet (hub + 2 spokes),
builds and side-loads the operator image, installs the Helm chart, and
simulates ClusterAPI inventory — exercising replication lifecycle,
conflict handling, namespace ensure, policy revocation, source-delete
cleanup, cluster registration/deregistration, and fanout at scale
(`test/e2e/`). All of it runs on every pull request; `E2E (kind fleet,
hub + 2 spokes)` is a required status check on `main`.

**What is being proven right now.** k8s-r8r is under active trial on a
real ClusterAPI-managed **staging** fleet — the first run outside CI,
against clusters the project did not create itself. That trial found more
in its first days than CI had in its entire life, and the findings are
being fixed rather than filed: status conditions reporting `Ready=True`
under policy denial, drift correction leaving no trace, replicas
inheriting foreign GitOps ownership metadata, and discovery pinned to a
CAPI API version upstream is removing are all fixed on `main`. Spoke RBAC
is still broader than the policy universe it should be scoped to
([#29](https://github.com/moeritze/k8s-r8r/issues/29)), and the rest of
what the trial surfaced is tracked in the open
[issues](https://github.com/moeritze/k8s-r8r/issues). Treat the coverage
above as CI-proven and now partly field-proven.

**What is still unproven.** Production operation. No production adopter,
no long-running deployment, upgrade paths untested, and day-2 failure
modes still being discovered. Do not run it against production fleets.

Version tags publish a multi-arch image and Helm chart to ghcr (see
[docs/releasing.md](docs/releasing.md)).

## Trust model

k8s-r8r moves Secrets, so the honest question is what a compromise buys
an attacker. Full threat model in [docs/security.md](docs/security.md);
the short version:

| Identity | Grant | Scope |
|---|---|---|
| Hub operator SA | `get`/`list`/`watch` (+ `patch`/`update` for finalizers) on Secrets and ConfigMaps; `get`/`list`/`watch` on Namespaces | cluster-wide on the hub |
| Hub operator SA | `get`/`list`/`watch` on ClusterAPI `Cluster` objects, and uncached reads of their `<cluster>-kubeconfig` Secrets | cluster-wide on the hub |
| Hub operator SA | full write on `Replication`; read-only (`get`/`list`/`watch`) on `ReplicationPolicy` | cluster-wide on the hub |
| Spoke SA (`k8s-r8r`) | `get`/`list`/`watch`/`create`/`update`/`patch`/`delete` on the allowlisted kinds only; `get`/`create`/`patch` on Namespaces (never `delete`) | all namespaces of the spoke (v1: one ClusterRole; per-namespace narrowing is planned) |

**Blast radius, stated plainly:** compromise of the hub operator pod
means fleet-wide read of every replicable Secret and ConfigMap on the
hub, plus the ability to read CAPI admin kubeconfigs for discovered
clusters and therefore to bootstrap into those spokes. The operator is a
high-value target; treat its namespace and its CAPI kubeconfig Secrets
accordingly.

**What the design does to keep that radius bounded:** the CAPI admin
kubeconfig is read uncached and used exactly once per spoke, to bootstrap
a ServiceAccount scoped to the allowlisted resource kinds — steady-state
traffic authenticates with short-lived, self-rotated SA tokens, and the
spoke rest config carries no static credential at all. Spoke RBAC is
re-narrowed when the allowlist shrinks. Replica payloads are never cached
on the hub (drift watches use metadata-only informers plus a source-hash
annotation), and no payload data ever reaches logs, events, conditions,
or metrics — hashes only, enforced by an AST audit test.

## Features

- **Annotation-based requests** on source objects — zero-friction
  adoption, no new RBAC for developers. Each request is materialized into
  an operator-owned `Replication` object carrying status and replica
  inventory.
- **`ReplicationPolicy` security boundary** — cluster-scoped, default
  deny, allowlist-only, union semantics; evaluated authoritatively on
  every reconcile, with live revocation (`Delete`/`Retain`).
- **Cross-cluster push transport** with minimal-privilege spoke
  credentials: the CAPI admin kubeconfig is used once to bootstrap a
  narrow ServiceAccount per spoke; steady state runs on short-lived,
  rotated SA tokens.
- **Pluggable discovery** — first provider: ClusterAPI (`Cluster`
  objects + kubeconfig Secrets); target clusters selected by labels.
- **Drift detection** via per-cluster metadata-only informers +
  `r8r.io/source-hash` comparison — replica payloads are never cached on
  the hub; edited or deleted replicas are restored.
- **Conflict handling**: `Fail` (default) / `Adopt` (content-hash match
  only) / `Overwrite` — a two-key turn, needing both the source's
  `r8r.io/conflict-policy` opt-in and a policy grant; never automatic
  renaming.
- **No orphans**: finalizers + per-Replication inventory garbage-collect
  replicas on source deletion, annotation removal, target deselection,
  and cluster deregistration.
- **Advisory admission webhook** (CEL-scoped, `failurePolicy: Ignore`) —
  apply-time feedback that is never an availability or security
  dependency.
- **Operational baseline**: leader election, Prometheus metrics,
  secret-safe events (hashes only, never payloads), size-capped status,
  Helm chart.

## Architecture

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

The internal pipeline is dynamic (unstructured — any GVK); the launch
allowlist gates it to Secrets and ConfigMaps. Discovery and transport are
interfaces: more providers and a pull-agent transport are planned as
additions, not rewrites.

## Getting started

Install a released version from the published Helm chart (pick a version
from the [releases](https://github.com/moeritze/k8s-r8r/releases)):

```sh
helm install k8s-r8r oci://ghcr.io/moeritze/charts/k8s-r8r \
  --version <x.y.z> \
  --namespace k8s-r8r-system --create-namespace
```

The chart pulls the matching operator image
(`ghcr.io/moeritze/k8s-r8r`) automatically. For development, install the
in-repo chart instead (`helm install k8s-r8r charts/k8s-r8r ...` with a
locally built image — see the quickstart).

Requires Kubernetes **1.30+** and, for the optional advisory webhook,
cert-manager (or bring your own cert — see the chart's `values.yaml`).

Then follow the **[quickstart](docs/quickstart.md)** for a local kind
fleet walkthrough.

## Documentation

| Doc | Contents |
|---|---|
| [docs/quickstart.md](docs/quickstart.md) | kind fleet, Helm install, annotate-a-Secret walkthrough |
| [docs/releasing.md](docs/releasing.md) | release process: tag → published image + chart + GitHub release |
| [docs/annotations.md](docs/annotations.md) | full `r8r.io/*` annotation reference |
| [docs/policies.md](docs/policies.md) | `ReplicationPolicy` authoring guide |
| [docs/gitops.md](docs/gitops.md) | ArgoCD integration, ignore rules, sealed-secrets/ESO complementarity |
| [docs/security.md](docs/security.md) | threat model, push-architecture reasoning, minimum K8s version |
| [docs/uninstall.md](docs/uninstall.md) | teardown order, finalizer escape hatch, orphan cleanup |

## Development

```sh
make test          # unit + envtest
make test-e2e      # kind fleet (hub + 2 spokes) e2e suite — needs docker
make lint          # golangci-lint
make manifests     # regenerate CRDs/RBAC from markers
hack/kind-fleet.sh up   # local hub + spokes
```

Project layout follows kubebuilder conventions: CRD APIs in
`api/v1alpha1/`, controllers and the engine in `internal/`, deployment
packaging in `charts/k8s-r8r/`.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
