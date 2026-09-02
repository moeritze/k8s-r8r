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
- `allowedConflictPolicies` — union, `Fail` always included; engine acts with the strongest granted (`Overwrite > Adopt > Fail`). The grant is the *only* input: there is no request-side conflict annotation to intersect with, so granting `Overwrite` grants it to every request the policy permits ([#34](https://github.com/moeritze/k8s-r8r/issues/34) — [[replication-flow|conflict caveats]])
- `revocationPolicy` — most conservative wins (`Retain` beats `Delete`)

## Revocation

Policy is re-evaluated on **every** reconcile — reconcile-time enforcement is authoritative, the [[security-model|webhook]] is advisory only. Tightening a policy revokes live: `Delete` removes replicas; `Retain` freezes them with `PolicyRevoked`. No grandfathering.

Two things to know before tightening in production:

- `Delete` revocation walks the inventory, and objects taken over by `Adopt` are in it — so revocation deletes pre-existing objects k8s-r8r only adopted ([#35](https://github.com/moeritze/k8s-r8r/issues/35)). Prefer `Retain` wherever `Adopt` is granted.
- Denial and full revocation both end with `desiredTargets: 0` and `Ready=True` once the engine's status pass runs — the separate `PolicyRevoked` condition is set only for the `Retain` case ([#27](https://github.com/moeritze/k8s-r8r/issues/27), being fixed; mechanism in [[replication-flow]]). Until then alert on the `PolicyDenied`/`PolicyRevoked` events and `k8s_r8r_policy_denials_total` / `k8s_r8r_revocations_total`, not on `Ready` — see [[operations]].

Implementation: `internal/policy` (pure functions: `Evaluate`, `ResolveOptions`, `DetectRevocations`). Consumed by [[replication-flow|request controller + engine]]. Spec: `openspec/specs/replication-policy` · guide: `../docs/policies.md`
