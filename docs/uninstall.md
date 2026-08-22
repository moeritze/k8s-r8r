# Uninstall / teardown

The clean order matters: the operator garbage-collects replicas **only
while it is still running**. Uninstall the chart first and the fleet
keeps whatever replicas existed at that moment (labeled and discoverable,
but unmanaged).

## Clean teardown order

### 1. Remove replication requests (replicas get garbage-collected)

Remove the request annotations from every replicated source — the
operator deletes the inventoried replicas and the `Replication` objects:

```sh
# find all sources with active requests via their Replication objects
kubectl get replications -A

# per source (the trailing "-" removes an annotation)
kubectl -n <ns> annotate <kind> <name> \
  r8r.io/replicate- r8r.io/target-clusters- \
  r8r.io/target-namespaces- r8r.io/target-name-
```

Alternatively, delete all `ReplicationPolicy` objects: with the default
`revocationPolicy: Delete`, withdrawing permission also deletes existing
replicas on the next reconcile. Note that policies with
`revocationPolicy: Retain` deliberately leave replicas in place — remove
the annotations instead for those.

### 2. Wait for cleanup to finish

```sh
kubectl get replications -A        # should drain to empty
kubectl get secrets,configmaps -A -l app.kubernetes.io/managed-by=k8s-r8r
```

Unreachable spokes: cleanup retries with backoff. Inventory entries for a
cluster are only released without cleanup when discovery deregisters that
cluster (`ClusterGone`) — replicas on it are then orphaned by definition.

### 3. Uninstall the chart

```sh
helm uninstall k8s-r8r -n k8s-r8r-system
```

### 4. Optionally delete the CRDs

Helm does **not** delete CRDs it installed from the `crds/` directory —
this is deliberate Helm behavior, and it protects you: deleting a CRD
deletes all its custom resources.

```sh
kubectl delete crd replications.r8r.io replicationpolicies.r8r.io
```

Caveats:

- Deleting `replications.r8r.io` while `Replication` objects still exist
  deletes them **with their inventory** — the record of which replicas
  exist where is gone, and nothing will ever clean them up. Only do this
  after step 2 drained.
- If the operator is already gone, remaining `Replication` objects carry
  the `r8r.io/engine-finalizer` finalizer and the CRD deletion will hang
  until you remove those finalizers (see escape hatch below).

### 5. Spoke leftovers

Uninstalling the hub chart does not touch the bootstrap artifacts on
spokes. Per spoke, remove:

```sh
kubectl delete clusterrolebinding k8s-r8r-replicator
kubectl delete clusterrole k8s-r8r-replicator
kubectl delete namespace k8s-r8r-system   # also removes the SA and token Role
```

## Finalizer escape hatch

Two finalizers block deletion while cleanup is pending:

- `r8r.io/finalizer` on annotated **source** objects
- `r8r.io/engine-finalizer` on **Replication** objects

If a source or Replication hangs in `Terminating` (typically: spokes
unreachable and not deregistered, or the operator already uninstalled),
you can force-release it:

```sh
# source object (example: a Secret)
kubectl -n <ns> patch secret <name> --type=merge \
  -p '{"metadata":{"finalizers":null}}'

# Replication object
kubectl -n <ns> patch replication <name> --type=merge \
  -p '{"metadata":{"finalizers":null}}'
```

**Consequences — this is a deliberate trade:** removing the finalizer
skips replica cleanup. Every replica recorded in that Replication's
inventory stays on its target cluster as an **orphan**: still labeled,
still containing the (possibly secret) payload, but no longer tracked,
updated, or ever garbage-collected. If the source was a credential,
treat orphaned copies as a secret-hygiene issue and remove them.

## Finding and removing orphans

Orphans remain discoverable by their labels on every cluster:

```sh
# everything k8s-r8r ever wrote to this cluster
kubectl get secrets,configmaps -A -l app.kubernetes.io/managed-by=k8s-r8r

# narrow to one former source
kubectl get secrets -A \
  -l r8r.io/source-namespace=<ns>,r8r.io/source-name=<name>

# remove them
kubectl delete secrets -A -l app.kubernetes.io/managed-by=k8s-r8r
```

Namespaces the engine created (`allowNamespaceCreation: true`) are never
deleted by the engine; they also carry
`app.kubernetes.io/managed-by: k8s-r8r` if you want to remove them
manually.
