# ReplicationPolicy authoring guide

`ReplicationPolicy` is the admin-controlled security boundary of k8s-r8r:
a cluster-scoped allowlist that decides which replication requests may
act. It is evaluated **authoritatively on every reconcile** — the
admission webhook only mirrors it for apply-time feedback.

## The model in four rules

1. **Default deny.** With no policies present, nothing replicates —
   requests are reported `PolicyDenied` on the `Replication`'s
   `TargetsResolved` condition (see [What a denial looks like in
   status](#what-a-denial-looks-like-in-status)).
2. **Allowlist only.** There are no deny rules. Denying is done by not
   allowing.
3. **All dimensions, one policy.** A target is permitted only when a
   *single* policy matches **all** dimensions: source namespace, source
   kind, target cluster, target namespace. Dimensions never combine
   across policies.
4. **Union across policies.** Multiple policies add up: a target is
   allowed if *any one* policy permits it in full.

This is the NetworkPolicy mental model: additive allowlists, no
precedence logic.

## Anatomy

```yaml
apiVersion: r8r.io/v1alpha1
kind: ReplicationPolicy
metadata:
  name: demo-secrets-to-dev
spec:
  sources:
    # Source namespace matches if it is in `namespaces` OR matches
    # `namespaceSelector` (either suffices; both optional, but you need
    # at least one to match anything).
    namespaces: ["demo"]
    namespaceSelector:
      matchLabels:
        team: payments
    # Required. Kinds outside the operator's --allowed-kinds are never
    # replicated regardless of policy.
    kinds: ["Secret"]
  targets:
    # Required. Matched against discovered cluster inventory labels.
    # An EMPTY selector ({}) matches ALL discovered clusters.
    clusterSelector:
      matchLabels:
        env: dev
    # Required, exact names, at least one entry.
    namespaces: ["demo"]
  options:            # all optional; defaults shown
    allowNamespaceCreation: false
    allowedConflictPolicies: [Fail]
    revocationPolicy: Delete
```

Dimension details worth knowing:

- `sources.namespaceSelector`: nil means "exact-name list only"; an
  empty-but-present selector (`namespaceSelector: {}`) matches **every**
  namespace.
- `targets.clusterSelector`: empty matches **all** clusters (unlike the
  request-side annotation, where empty selects none — requests must be
  explicit, policies may be broad).
- `targets.namespaces`: plain exact-name allowlist; no selector.
- An invalid selector fails closed (the dimension does not match).

## Options

Options gate engine side effects for the requests a policy permits. When
several policies permit the same target, their options combine:

| Option | Default | Combination rule |
|---|---|---|
| `allowNamespaceCreation` | `false` | OR — any matching policy granting it, grants it |
| `allowedConflictPolicies` | `[Fail]` | union — `Fail` is always included |
| `revocationPolicy` | `Delete` | most conservative wins — `Retain` beats `Delete` |

- **`allowNamespaceCreation`**: when true, the engine creates missing
  target namespaces (labeled `app.kubernetes.io/managed-by: k8s-r8r`).
  When false (default), replication into a nonexistent namespace fails
  with a condition rather than silently creating cluster structure.
- **`allowedConflictPolicies`**: what may happen when an *unmanaged*
  object already occupies a replica's name on a target. `Fail` (default)
  leaves it untouched and reports a `Conflict`. `Adopt` takes ownership
  without rewriting — only when the existing object's content hash equals
  the source hash. `Overwrite` replaces the payload — it is weaponizable
  (it can replace a victim cluster's existing secret), so grant it
  narrowly. The engine acts with the strongest granted policy
  (Overwrite > Adopt > Fail).
- **`revocationPolicy`**: what happens to already-created replicas when
  permission is withdrawn (policy edited/deleted, annotations removed).
  `Delete` (default) removes them on the next reconcile — revoked data
  should not linger on the fleet. `Retain` leaves them in place but stops
  updating them and marks the `Replication` with a `PolicyRevoked`
  condition.

## Revocation is live

Policy is re-evaluated on every reconcile, so tightening a policy acts on
existing replicas immediately: targets that lose permission are handled
per the effective `revocationPolicy` resolved from the policies that
*previously* allowed them. There is no "grandfathering".

## What a denial looks like in status

Events expire; status is what monitoring reads. A `Replication` carries
two conditions that answer two different questions:

| Condition | Written by | Question it answers |
| --- | --- | --- |
| `TargetsResolved` | request controller | Did the request resolve to any target at all, and if not, why? |
| `Ready` | replication engine | Is everything the engine was asked to do actually done? |

For a denied request that means:

```yaml
status:
  summary:
    desiredTargets: 0
    readyTargets: 0
    failedTargets: 0
  conditions:
  - type: TargetsResolved
    status: "False"
    reason: PolicyDenied           # or NoTargets — see below
    message: no ReplicationPolicy allowlists source namespace "foo" (sourceNamespace)
  - type: Ready
    status: "False"
    reason: NoTargets
    message: no targets resolved; nothing is being replicated
```

The two `TargetsResolved` failure reasons distinguish the two ways a
request can come to nothing:

- **`PolicyDenied`** — candidate targets existed and policy refused them.
  Fix the policy (or the request's target dimensions).
- **`NoTargets`** — the request produced no candidate at all: the
  `r8r.io/target-clusters` selector matched no *ready* cluster. Usually a
  typo in the selector, a label missing on the cluster inventory entry, or
  a cluster that has not become ready. Policy was never consulted, so
  there is no denial event to look for.

`Ready` is the one to alert on: a request that asked for replication and
got none reports `Ready: False`, reason `NoTargets`, whether the cause was
a denial, a revocation, or an empty selector. Replicating nothing is not
success.

> **Upgrading from 0.1.0-alpha.1:** these replications previously reported
> `Ready: True` with `0/0 targets ready`. They flip to `Ready: False` on
> the first reconcile after upgrade. Nothing about what is replicated
> changes — only whether the object admits it.

## Recipes

Platform team fans out pull secrets to every cluster, may create the
namespace:

```yaml
apiVersion: r8r.io/v1alpha1
kind: ReplicationPolicy
metadata:
  name: platform-pull-secrets
spec:
  sources:
    namespaces: ["platform"]
    kinds: ["Secret"]
  targets:
    clusterSelector: {}          # all discovered clusters
    namespaces: ["registry-creds"]
  options:
    allowNamespaceCreation: true
```

Teams labeled `replication=enabled` may copy ConfigMaps to dev clusters,
adopting identical pre-existing objects:

```yaml
apiVersion: r8r.io/v1alpha1
kind: ReplicationPolicy
metadata:
  name: team-configmaps-dev
spec:
  sources:
    namespaceSelector:
      matchLabels:
        replication: enabled
    kinds: ["ConfigMap"]
  targets:
    clusterSelector:
      matchLabels:
        env: dev
    namespaces: ["shared-config"]
  options:
    allowedConflictPolicies: [Fail, Adopt]
    revocationPolicy: Retain
```

Union pitfall — these two policies together still deny a `demo` Secret
targeting `env=prod`, because no *single* policy allows both dimensions:

```yaml
# policy A: demo sources, dev clusters only
# policy B: platform sources, prod clusters
# => demo -> prod: DENIED (dimensions never mix across policies)
```

## Who writes policies

`ReplicationPolicy` is cluster-scoped and admin-owned. The Helm chart
ships two persona ClusterRoles (created, never bound automatically):

- `k8s-r8r-policy-admin` — full CRUD on ReplicationPolicy; bind to
  cluster administrators only.
- `k8s-r8r-replication-viewer` — read-only `Replication` access,
  aggregated into the built-in `view` role.

Developers gain **no new privileges** from replication: k8s-r8r only fans
out objects they can already write, to destinations policy allows. A
developer cannot widen their own reach — there is no mechanism to
override policy from the request side.
