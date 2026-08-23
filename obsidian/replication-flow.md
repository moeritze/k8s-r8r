---
tags: [functionality]
---

# Replication Flow

The core path from developer intent to replicas on the fleet.

## 1. Request (developer)

Annotate any allowlisted object (Secret, ConfigMap):

```yaml
metadata:
  annotations:
    r8r.io/replicate: "true"
    r8r.io/target-clusters: "env=prod"        # label selector over cluster inventory; empty = NO clusters, no wildcard
    r8r.io/target-namespaces: "istio-system"  # default: source namespace
    r8r.io/target-name: "ca-cert"             # optional explicit rename — never automatic
```

Parsed by `internal/annotations` (shared with the [[security-model|webhook]]). Devs need zero new RBAC — you can only fan out what you can already write.

## 2. Materialization (request controller)

`internal/controller/request` resolves selector × [[cluster-discovery|discovered inventory]] × [[policy-model|policy verdict]] into one operator-owned **`Replication`** object per source (origin `Annotation`, designed to admit a future `ReplicationSet`). Hand-authored Replications get `NotAuthoritative` and are ignored. Finalizer `r8r.io/finalizer` on the source blocks its deletion until replica cleanup completes.

## 3. Fanout (engine)

`internal/engine` reconciles each `Replication` per target (workqueue key = source + targetCluster; independent backoff):
- payload stripped of server-managed fields, stamped with `app.kubernetes.io/managed-by: k8s-r8r`, source-ref labels, `r8r.io/source-hash: sha256:…`
- applied via server-side apply (field manager `k8s-r8r`) over the push `Transport` using the spoke's [[security-model|bootstrapped SA token]]
- conflicts with pre-existing unmanaged objects: `Fail` (default) / `Overwrite` (policy-gated) / `Adopt` (hash-equal only)
- missing namespace: created only when policy sets `allowNamespaceCreation`; namespaces are never GC'd

## 4. Tracking and cleanup

Every replica lands in `Replication.status.inventory` — the GC source of truth. Cleanup triggers: source deleted, annotation removed, target deselected, policy revoked ([[policy-model#revocation|revocation]]). Cluster deregistration releases inventory with a `ClusterGone` event (no credential remains to delete with) — deselect before deregistering for clean removal. Ongoing sync: [[drift-detection]].

Spec: `openspec/specs` (`replication-request`, `replication-engine`) · user docs: `../docs/annotations.md`
