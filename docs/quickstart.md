# Quickstart

Get k8s-r8r running on a local kind cluster and walk through annotating a
Secret for replication.

> **Alpha notice:** k8s-r8r is v0/alpha. The full cross-cluster path
> (annotation → Replication → policy decision → fanout, drift repair, GC)
> is exercised continuously by the kind-fleet end-to-end suite
> (`make test-e2e`, see `test/e2e/`).

## Prerequisites

- [kind](https://kind.sigs.k8s.io), kubectl, [Helm](https://helm.sh), Docker, Go 1.24+
- Optional: [cert-manager](https://cert-manager.io) on the hub for the
  advisory webhook (the quickstart skips it)

## Install from published artifacts

Released versions ship as a container image
(`ghcr.io/moeritze/k8s-r8r:<tag>`) and a Helm chart in an OCI registry —
no clone or local build needed. Pick a version from the
[releases page](https://github.com/moeritze/k8s-r8r/releases) and:

```sh
helm install k8s-r8r oci://ghcr.io/moeritze/charts/k8s-r8r \
  --version <x.y.z> \
  --namespace k8s-r8r-system --create-namespace
```

The chart's default image repository and tag point at the image published
for that release, so the operator image is pulled automatically. See
[releasing.md](releasing.md) for how releases are cut.

The rest of this quickstart is the **development path**: build the image
locally and run it on a kind fleet.

## 1. Create a local fleet

```sh
# hub + 2 spoke clusters (idempotent; default is 1 spoke)
K8S_R8R_SPOKES=2 hack/kind-fleet.sh up

# export kubeconfigs to ./bin/kubeconfigs/ (optional, for poking at spokes)
hack/kind-fleet.sh kubeconfigs

kubectl config use-context kind-r8r-hub
```

**About cross-cluster replication:** the v1 discovery provider is
ClusterAPI (`--discovery-provider=cluster-api`). The operator discovers
target clusters from ClusterAPI `Cluster` objects on the hub and
bootstraps spoke credentials from their `<cluster>-kubeconfig` Secrets.
Plain kind clusters created by `kind-fleet.sh` are **not** registered in
ClusterAPI, so out of the box the fleet is raw material: to replicate to
the spokes for real you need ClusterAPI managing them (e.g. via
[CAPD](https://cluster-api.sigs.k8s.io/user/quick-start), the ClusterAPI
Docker provider). Without ClusterAPI inventory, this quickstart still
demonstrates the entire hub-side flow — request materialization, the
default-deny policy decision, and status/events — with the target list
resolving to zero clusters.

> **Troubleshooting — zero clusters discovered.** The operator does not pin
> a CAPI API version: it negotiates one of `v1`, `v1beta2`, `v1beta1` from
> the hub's discovery API at startup and logs the result. If nothing
> replicates and target lists are empty, check the negotiated version first:
>
> ```sh
> kubectl -n k8s-r8r-system logs deploy/k8s-r8r | grep -i 'ClusterAPI'
> ```
>
> `Negotiated ClusterAPI discovery version groupVersion=...` means discovery
> is fine. `ClusterAPI inventory unavailable, retrying` means the hub has no
> `clusters.cluster.x-k8s.io` (the expected state on a bare kind fleet, as
> above) — the operator waits and picks it up when ClusterAPI is installed.
> `serves none of [...]` means the hub's CAPI serves only versions this build
> does not support; the pod restarts with that message until you upgrade.
> The `k8s_r8r_discovery_up` metric carries the same signal: `0` means
> discovery is not running, `1` with `k8s_r8r_discovery_clusters=0` means a
> genuinely empty fleet. Note the pod stays **Ready** in all these cases —
> readiness reflects hub informer sync only, by design.

## 2. Build and install the operator on the hub

```sh
make docker-build IMG=k8s-r8r:dev
kind load docker-image k8s-r8r:dev --name r8r-hub

helm install k8s-r8r charts/k8s-r8r \
  --namespace k8s-r8r-system --create-namespace \
  --set image.repository=k8s-r8r --set image.tag=dev \
  --set webhook.enabled=false        # skip cert-manager for the quickstart

kubectl -n k8s-r8r-system get pods
```

Useful values (see `charts/k8s-r8r/values.yaml` for all):

| Value | Flag | Default |
|---|---|---|
| `discoveryProvider` | `--discovery-provider` | `cluster-api` |
| `hubName` | `--hub-name` | `hub` |
| `allowedKinds` | `--allowed-kinds` | `[secrets, configmaps]` |
| `spokeResync` | `--spoke-resync` | engine default (10h) |

## 3. Annotate a Secret — and watch default deny

Create a Secret and request replication:

```sh
kubectl create namespace demo
kubectl -n demo create secret generic db-creds \
  --from-literal=username=app --from-literal=password=s3cr3t

kubectl -n demo annotate secret db-creds \
  r8r.io/replicate="true" \
  "r8r.io/target-clusters=env=dev"
```

The operator materializes a `Replication` object for the request:

```sh
kubectl -n demo get replications
kubectl -n demo describe replication secret-db-creds-<hash>
```

With **no ReplicationPolicy present, nothing replicates** — this is the
default-deny security posture, not an error. The `Replication` status and
events on the Secret report `PolicyDenied` with the failing dimension.

Operator events are recorded through the core `v1` Event API, so the usual
idiom works and orders them correctly:

```sh
kubectl -n demo get events --sort-by=.lastTimestamp
```

## 4. Allow it with a ReplicationPolicy

Policies are cluster-scoped, admin-owned allowlists (see
[policies.md](policies.md)):

```yaml
apiVersion: r8r.io/v1alpha1
kind: ReplicationPolicy
metadata:
  name: demo-secrets-to-dev
spec:
  sources:
    namespaces: ["demo"]
    kinds: ["Secret"]
  targets:
    clusterSelector:
      matchLabels:
        env: dev
    namespaces: ["demo"]
```

```sh
kubectl apply -f policy.yaml
kubectl -n demo get replication -o yaml
```

The request is now permitted. On a hub with ClusterAPI-managed clusters
labeled `env=dev`, replicas of `db-creds` appear in the `demo` namespace
on each of them, carrying `app.kubernetes.io/managed-by: k8s-r8r`,
`r8r.io/source-*` labels, and the `r8r.io/source-hash` annotation. On the
bare kind fleet the selector resolves to zero clusters
(`desiredTargets: 0`) because no ClusterAPI inventory exists — attach the
spokes to ClusterAPI to see the fanout.

## 5. Clean up

```sh
# remove the request first so replicas are garbage-collected
kubectl -n demo annotate secret db-creds r8r.io/replicate- \
  r8r.io/target-clusters-

helm uninstall k8s-r8r -n k8s-r8r-system
hack/kind-fleet.sh down
```

See [uninstall.md](uninstall.md) for the full teardown order and the
finalizer escape hatch.

## Next steps

- [Annotation reference](annotations.md)
- [Policy authoring guide](policies.md)
- [GitOps / ArgoCD integration](gitops.md)
- [Security model](security.md)
