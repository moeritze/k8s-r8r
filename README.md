# k8s-r8r

<!-- badge placeholder: CI --> ![status: alpha](https://img.shields.io/badge/status-alpha-orange)

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
Unit and envtest coverage exists across the controllers, policy engine,
and webhook; **the cross-cluster path is not yet exercised by an
end-to-end suite**. Version tags publish a multi-arch image and Helm
chart to ghcr (see [docs/releasing.md](docs/releasing.md)). Expect rough
edges; do not run it against production fleets.

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
- **Conflict handling**: `Fail` (default) / `Overwrite` (policy-gated) /
  `Adopt` (content-hash match only); never automatic renaming.
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
make lint          # golangci-lint
make manifests     # regenerate CRDs/RBAC from markers
hack/kind-fleet.sh up   # local hub + spokes
```

Project layout follows kubebuilder conventions: CRD APIs in
`api/v1alpha1/`, controllers and the engine in `internal/`, deployment
packaging in `charts/k8s-r8r/`.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
