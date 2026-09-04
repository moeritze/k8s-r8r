---
tags: [functionality, security]
---

# Policy Model

`ReplicationPolicy` (cluster-scoped, admin-only) is the security boundary. **Default deny**: no policies → nothing replicates, ever.

## Semantics

- **Allowlists only** — no deny rules; denying = not allowing.
- **All dimensions, one policy**: a target is permitted only if a *single* policy matches source namespace, source kind, target cluster (labels), and target namespace. Dimensions never combine across policies.
- **Union across policies**: any one fully-matching policy suffices.
- Policy-side empty `clusterSelector` matches *all* clusters; request-side empty selector matches *none* (requests must be explicit, policies may be broad). Invalid selectors fail closed.
- Namespace matching is **asymmetric today**: `sources` accepts either exact `namespaces` or a `namespaceSelector`, but `targets.namespaces` is exact names only — there is no `targets.namespaceSelector`, so a policy covering a growing set of target namespaces has to be edited every time one is added ([#31](https://github.com/moeritze/k8s-r8r/issues/31)).

```yaml
apiVersion: r8r.io/v1alpha1
kind: ReplicationPolicy
spec:
  sources: {namespaces: ["platform"], kinds: [Secret]}
  targets: {clusterSelector: {matchLabels: {env: prod}}, namespaces: [istio-system]}
  options: {allowNamespaceCreation: false, allowedConflictPolicies: [Fail], revocationPolicy: Delete}
```

## Options (union when several policies permit)

- `allowNamespaceCreation` — OR
- `allowedConflictPolicies` — union, `Fail` always included; its strongest member is the **grant**, which is only one of the two keys. The engine acts on the weaker of the grant and the source's `r8r.io/conflict-policy` annotation, so granting `Overwrite` in a policy no longer grants it to every request that policy permits — each source must opt in per object ([[replication-flow#Conflict handling is a two-key turn|two-key turn]])
- `revocationPolicy` — most conservative wins (`Retain` beats `Delete`)

## Revocation

Policy is re-evaluated on **every** reconcile — reconcile-time enforcement is authoritative, the [[security-model|webhook]] is advisory only. Tightening a policy revokes live: `Delete` removes replicas; `Retain` freezes them with `PolicyRevoked`. No grandfathering.

What a denial or a full revocation looks like once it settles:

```yaml
status:
  summary: {desiredTargets: 0, readyTargets: 0, failedTargets: 0}
  conditions:
  - type: TargetsResolved       # request controller: why nothing resolved
    status: "False"
    reason: PolicyDenied
  - type: Ready                 # engine: nothing is being replicated
    status: "False"
    reason: NoTargets
```

Both are durable and both live in status, so a fully denied or revoked request no longer reads as a healthy fanout ([#27](https://github.com/moeritze/k8s-r8r/issues/27); ownership table in [[replication-flow#5. Status: one writer per condition|replication-flow]]). Do **not** look for `PolicyRevoked` as the durable record: it is set only while at least one target is frozen under `Retain`, and is actively *removed* again once the revocation is fully processed. The `PolicyDenied` / `PolicyRevoked` events and `k8s_r8r_policy_denials_total` / `k8s_r8r_revocations_total` remain useful corroboration — see [[operations]].

One thing to know before tightening in production:

- `Delete` revocation walks the inventory, and objects taken over by `Adopt` are in it — so revocation deletes pre-existing objects k8s-r8r only adopted ([#35](https://github.com/moeritze/k8s-r8r/issues/35)). Prefer `Retain` wherever `Adopt` is granted.

Implementation: `internal/policy` (pure functions: `Evaluate`, `ResolveOptions`, `DetectRevocations`). Consumed by [[replication-flow|request controller + engine]]. Spec: `openspec/specs/replication-policy` · guide: `../docs/policies.md`
