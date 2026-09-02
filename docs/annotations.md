# Annotation reference

Replication is requested with `r8r.io/*` annotations on the source object
(Secret or ConfigMap on the launch allowlist). The operator materializes
each request into an operator-owned `Replication` object that carries
status and replica inventory — you never author `Replication` objects
yourself.

## Request annotations

| Annotation | Value | Required | Default |
|---|---|---|---|
| `r8r.io/replicate` | `"true"` / `"false"` | yes (to opt in) | — |
| `r8r.io/target-clusters` | label selector string | yes (in practice) | empty — selects **no** clusters |
| `r8r.io/target-namespaces` | comma-separated namespace list | no | the source's own namespace |
| `r8r.io/target-name` | DNS-1123 subdomain | no | replicas keep the source's name |
| `r8r.io/conflict-policy` | `Fail` / `Adopt` / `Overwrite` | no | `Fail` — consents to nothing |

### `r8r.io/replicate`

Opts the object in (`"true"`) or explicitly out (`"false"`). Any other
value is rejected as malformed. Removing the annotation (or setting
`"false"`) triggers cleanup: all replicas recorded in inventory are
deleted (subject to the effective `revocationPolicy`, see
[policies.md](policies.md)) and the `Replication` object is removed.

### `r8r.io/target-clusters`

A standard Kubernetes label selector (e.g. `env=dev`,
`env in (dev,staging),region=eu`) matched against the labels of clusters
in discovered inventory (for the ClusterAPI provider: the labels on the
`Cluster` objects).

Two deliberate contract decisions:

- **An absent or empty selector selects NO clusters.** Replication always
  requires an explicit cluster selection.
- **The wildcard `*` is not supported** and is rejected. To select every
  cluster deliberately, use a selector on a label all your clusters carry
  (e.g. `env` as a bare existence selector).

### `r8r.io/target-namespaces`

Comma-separated list of target namespaces on each selected cluster. Every
entry must be a valid namespace name; duplicates and empty entries are
rejected. When omitted or empty, replicas go to the **source object's own
namespace** on each target cluster. Whether missing namespaces are
created is policy-controlled (`allowNamespaceCreation`, default off).

### `r8r.io/target-name`

Optional explicit rename for the replicas. Must be a valid DNS-1123
subdomain. This is the **only** rename mechanism: k8s-r8r never renames
automatically (no prefixes/suffixes), because consumers mount secrets by
name and a silent rename breaks workloads with no error surfaced (design
D7).

### `r8r.io/conflict-policy`

The request's half of a **two-key turn**. It states the strongest thing the
engine may do to an object that already occupies the replica's name and was
not created by k8s-r8r:

| Value | Means |
|---|---|
| `Fail` (default) | never touch a pre-existing object; report a `Conflict` |
| `Adopt` | take ownership without rewriting, and only when the existing object's content hash already equals the source's |
| `Overwrite` | take the object over, replacing its payload |

The engine acts on the **weaker** of this value and what the matching
`ReplicationPolicy` objects permit (`allowedConflictPolicies`, see
[policies.md](policies.md)), ranked `Fail` < `Adopt` < `Overwrite`:

| request asks | policy permits | engine acts with |
|---|---|---|
| (absent) | `Overwrite` | `Fail` |
| `Overwrite` | `Fail` (the default grant) | `Fail` |
| `Overwrite` | `Adopt` | `Adopt` |
| `Adopt` | `Overwrite` | `Adopt` |
| `Overwrite` | `Overwrite` | `Overwrite` |

So an admin granting `Overwrite` in a policy does **not** hand it to every
request under that policy: each source must also opt in per object. And a
source asking for `Overwrite` gets no more than its policies allow.

Values are case-sensitive and the set is closed — `overwrite` is rejected, the
same as any other malformed value. When only one key turns and a conflict
actually occurs, the target's `Conflict` message names the key that is
missing, so `kubectl describe replication` tells you whether to add the
annotation or to change the policy.

> **Upgrade note.** Before this key existed, the policy grant alone decided.
> A source that relied on a policy-granted `Overwrite` or `Adopt` and carries
> no `r8r.io/conflict-policy` annotation now reports `Conflict` instead of
> taking the object over. Add the annotation to the sources that should have
> it.

## Validation behavior

- Malformed values (bad selector, invalid namespace, invalid name) mark
  the request invalid; the controller emits an `InvalidAnnotations` event
  and, when the advisory webhook is deployed, the write is rejected at
  apply time with a message naming the offending annotation.
- **Unknown `r8r.io/*` keys** (e.g. the typo `r8r.io/target-cluster`) are
  rejected by the controller so typos fail loudly instead of silently
  selecting nothing. The webhook only warns on unknown keys (an older
  webhook must not block newer requests).
- A `r8r.io/conflict-policy` value that no policy able to match the source
  permits is **not** an error: the request is otherwise valid and replicates
  normally, so the webhook admits it with a warning and conflict handling
  falls back to the weaker key.

## Examples

Replicate a Secret to the same namespace on all `env=dev` clusters:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: db-creds
  namespace: demo
  annotations:
    r8r.io/replicate: "true"
    r8r.io/target-clusters: "env=dev"
stringData:
  password: s3cr3t
```

Fan out a ConfigMap to two namespaces on EU staging clusters, renamed:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: app-config-main
  namespace: platform
  annotations:
    r8r.io/replicate: "true"
    r8r.io/target-clusters: "env=staging,region=eu"
    r8r.io/target-namespaces: "team-a,team-b"
    r8r.io/target-name: "app-config"
data:
  mode: canary
```

## Operator-written metadata (not part of the request contract)

These appear on **replicas** (and are ignored by request parsing):

| Key | Kind | Meaning |
|---|---|---|
| `app.kubernetes.io/managed-by: k8s-r8r` | label | ownership marker; drives drift watches and GitOps ignore rules |
| `r8r.io/source-cluster` | label | hub identity (`--hub-name`) |
| `r8r.io/source-namespace` | label | source namespace on the hub |
| `r8r.io/source-name` | label | source name |
| `r8r.io/source-kind` | label | source kind |
| `r8r.io/source-uid` | label | source UID (pins the incarnation) |
| `r8r.io/source-hash` | annotation | `sha256:<hex>` of the source payload; drift is a hash mismatch |

Request annotations themselves never propagate to replicas — all
`r8r.io/*` keys are stripped before the engine's own metadata is applied,
so a replica can never re-trigger replication.

### Stripped metadata

Beyond its own keys, the engine strips metadata that asserts **ownership
by, or replication intent toward, another controller**. Such metadata is
removed from the replica and excluded from the source hash, so source and
replica still hash identically and drift detection is unaffected.

| Stripped from replicas | Why |
|---|---|
| `replicator.v1.mittwald.de/*` | a replica carrying these is a valid *source* for mittwald/kubernetes-replicator — k8s-r8r would seed a second fanout no `ReplicationPolicy` evaluated |
| `reflector.v1.k8s.emberstack.com/*` | same hazard, emberstack/kubernetes-reflector |
| `argocd.argoproj.io/*` | ArgoCD resource tracking and sync options |
| `app.kubernetes.io/instance` | ArgoCD's default (label) tracking key — a replica carrying it is an extraneous, prunable member of an Application that never declared it |
| `meta.helm.sh/*` | Helm release ownership |
| `kustomize.toolkit.fluxcd.io/*` | Flux kustomize-controller ownership |

Everything else propagates unchanged. This is deliberately a **denylist,
not an allowlist**: source metadata is often functionally significant on
the target — a replicated sealed-secrets key without
`sealedsecrets.bitnami.com/sealed-secrets-key: active` is inert on
arrival — so dropping unknown keys by default would silently break
working replications.

Extend the list for controllers specific to your fleet:

```sh
--strip-metadata-keys=example.com/owner,vendor.example/
```

Comma-separated and repeatable. An entry ending in `/` is a prefix match;
anything else is an exact key. The chart exposes it as
`stripMetadataKeys` (a list). The flag is **additive only** — the built-in
entries above cannot be removed, because removing them reintroduces the
cross-controller fanout they exist to prevent.

Finalizers used by the operator: `r8r.io/finalizer` on annotated sources
and `r8r.io/engine-finalizer` on `Replication` objects — both exist so
deletion waits for replica cleanup (see [uninstall.md](uninstall.md)).
