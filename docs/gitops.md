# GitOps / ArgoCD integration

k8s-r8r is designed to coexist with GitOps rather than compete with it:
**source objects stay fully GitOps-managed; replicas are clearly labeled
operator output** that your GitOps tooling can recognize and ignore.

## One-annotation adoption

Nothing about a source object changes ownership. Adoption cost is adding
annotations to a manifest you already manage in git:

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

ArgoCD keeps syncing the source as before; the operator watches it and
fans it out. The target selection is git-visible and reviewable — so is
the `ReplicationPolicy` that permits it.

The operator's only write to a source is its `r8r.io/finalizer`
finalizer. ArgoCD ignores `metadata.finalizers` drift by default, so this
does not show up as out-of-sync.

## Recognizing replicas

Every replica carries:

| Key | Type | Content |
|---|---|---|
| `app.kubernetes.io/managed-by: k8s-r8r` | label | ownership marker |
| `r8r.io/source-cluster` | label | hub identity (`--hub-name`) |
| `r8r.io/source-namespace` | label | source namespace |
| `r8r.io/source-name` | label | source name |
| `r8r.io/source-kind` | label | source kind |
| `r8r.io/source-uid` | label | source UID |
| `r8r.io/source-hash` | annotation | `sha256:<hex>` payload hash |

Replicas are traceable back to their source from any cluster with a
label query:

```sh
kubectl get secrets -A -l app.kubernetes.io/managed-by=k8s-r8r
kubectl get secrets -A -l r8r.io/source-name=registry-creds,r8r.io/source-namespace=platform
```

## Keeping ArgoCD's hands off replicas

Replicas live outside git, so ArgoCD Applications that manage the target
namespaces should not fight or prune them.

**Pruning:** replicas are cluster-local objects ArgoCD did not create, so
a normal Application will not prune them. Trouble only starts if an
Application manages an object of the same name — then the two controllers
will fight over content (k8s-r8r restores its payload on drift). Avoid
overlapping names, or make one side the owner deliberately (see conflict
policies in [policies.md](policies.md)).

**Diffing:** if you mirror whole namespaces or use tooling that reports
unmanaged objects, exclude operator-managed ones. Instance-wide resource
exclusion in `argocd-cm` is the cleanest cut for hub-side `Replication`
objects:

```yaml
# argocd-cm ConfigMap
resource.exclusions: |
  - apiGroups: ["r8r.io"]
    kinds: ["Replication"]
    clusters: ["*"]
```

`Replication` objects are operator-owned status carriers — they should
never be in git (hand-authored ones are marked `NotAuthoritative` and
ignored by the operator).

**ApplicationSets:** if you generate per-cluster Applications with an
ApplicationSet, keep secrets/config that k8s-r8r distributes out of those
Applications entirely — one hub-side source plus a cluster selector
replaces N per-cluster copies in git. The `r8r.io/target-clusters`
selector plays the role your ApplicationSet cluster generator would, but
for a single object instead of a whole app.

## Sealed Secrets / External Secrets complementarity

k8s-r8r does not compete with secret *sourcing* tools — it distributes
whatever exists in the hub cluster:

- **sealed-secrets:** keep the `SealedSecret` in git on the hub; the
  controller unseals it into a plain Secret; annotate *that* Secret for
  replication. Spokes need no sealed-secrets controller and no private
  key — the fleet's decryption capability stays on the hub.
- **External Secrets Operator:** ESO materializes a Secret on the hub
  from your external store; annotate the materialized Secret (ESO's
  `ExternalSecret` supports adding annotations to the created Secret via
  `spec.target.template.metadata.annotations`). One store connection on
  the hub serves the whole fleet instead of per-cluster store access.

In both cases the division of labor is: **git/store → hub Secret**
(sealed-secrets/ESO), **hub Secret → fleet** (k8s-r8r, policy-gated).

## Hub vs spoke summary

| Object | Where | Managed by |
|---|---|---|
| Source Secret/ConfigMap | hub | git / ArgoCD (plus one finalizer from k8s-r8r) |
| `ReplicationPolicy` | hub | git / ArgoCD (admin repo) |
| `Replication` | hub | k8s-r8r only — never in git |
| Replicas | spokes (and hub-local namespaces) | k8s-r8r only — labeled, ignorable |
