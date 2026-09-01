---
tags: [functionality, security]
---

# Security Model

Full write-up: `../docs/security.md`. Core stances:

## Personas (RBAC)

| Persona | Power | Vehicle |
|---|---|---|
| Platform admin | author `ReplicationPolicy` | `k8s-r8r-policy-admin` ClusterRole, never auto-bound |
| Developer | annotate objects they already own | **zero new privileges** — [[policy-model]] gates destinations |
| Operator SA | read allowlisted kinds, CAPI clusters + kubeconfig Secrets (crown jewels), own Replications | manager ClusterRole |
| User (read) | view Replications | `k8s-r8r-replication-viewer`, aggregates to `view` |

## Push architecture + blast radius (D2/D5)

Hub holds fleet write access → mitigated by [[cluster-discovery#credential-bootstrap-d5|one-shot narrow SA bootstrap]]: admin kubeconfig used once, steady state runs on short-lived rotated SA tokens scoped to allowlisted kinds. Pull-agent transport is a future `Transport` implementation.

**How narrow the SA actually is (be precise, the docs used to overstate this).** The spoke `k8s-r8r-replicator` ClusterRole grants `get,list,watch,create,update,patch,delete` on each kind in `--allowed-kinds` (default `secrets,configmaps`) plus `get,create,patch` on namespaces — no `resourceNames`, and bound via ClusterRoleBinding, so **every namespace** of the spoke. The `managed-by` selector on spoke caches filters what the operator *watches*; it is not an authz boundary.

Two intended narrowings are not built yet:

- **Not policy-derived** — scope comes from the `--allowed-kinds` flag, never from [[policy-model]]. Grant is a superset of what policy permits and exists from first cluster discovery, before any policy. Re-narrowing tracks an `--allowed-kinds` change (restart → re-bootstrap every spoke, full rule replacement), not a policy change. Deferred because *widening* needs an escalation-capable credential, so policy-driven widening would demote the admin kubeconfig from one-shot to routine — worse for D5 than the broad grant. Tracked: [#29](https://github.com/moeritze/k8s-r8r/issues/29).
- **Not per-namespace** — ClusterRole, not per-namespace Roles. `RBACScope` (`internal/cluster/bootstrap.go`) is the seam for both.

So: reduction from fleet admin is real (no node/workload/RBAC control) but **narrower-than, not bounded** — with `secrets` allowlisted the SA reads and deletes every Secret in `kube-system`, `cert-manager`, `argocd`. Sizing: not an *escalation* (hub already holds the CAPI admin kubeconfigs), it defeats the *reduction* D5 promises. Untouched and fully delivered: no static credential in the spoke rest config, short-lived rotated tokens, admin kubeconfig used once per bootstrap. Operator lever until #29 lands: chart `allowedKinds`.

## Advisory webhook doctrine (D6)

Validating webhook (CEL matchConditions: fires only on objects carrying `r8r.io/` annotations; K8s ≥ 1.30) gives apply-time errors naming the failing policy dimension. `failurePolicy: Ignore` is **mandatory** — `Fail` would let operator downtime block all annotated secret writes. Security never depends on it: reconcile-time policy evaluation is authoritative; a bypassed webhook means worse error messages, never unauthorized replication.

## Secret-safe telemetry

No log, event, condition, or metric ever contains payload data — hashes only. Enforced twice: runtime canary test + AST-walking static ratchet (`internal/telemetry/secretsafety_audit_test.go`) that fails on any `Data`/`StringData` reaching a formatting/event call. Checklist: `internal/telemetry/REVIEW_CHECKLIST.md`.

## Weaponization guards

- `Overwrite` conflict mode could replace a victim cluster's secret → policy-gated, `Fail` default; replicas of a *different* source are never taken over ([[replication-flow]]).
- Exfiltration via annotation → blocked by default-deny [[policy-model]]; no request-side override exists. ⚠️ This claim is disputed — see [#26](https://github.com/moeritze/k8s-r8r/issues/26) / [#34](https://github.com/moeritze/k8s-r8r/issues/34); corrected wording lands with that fix, not here.

## Supply chain

Released images/charts are cosign-signed (keyless, GitHub OIDC — no long-lived key), carry SPDX SBOMs and SLSA provenance attestations, and are OCI-labelled back to the repo. Verify before running a fleet-wide Secret reader: `../docs/releasing.md#verifying-a-release`. Build-side details and rationale: [[development#supply-chain-what-a-release-proves]].

Repo hygiene: public repo — gitleaks pre-push hook + CI job gate every push; govulncheck on every PR and weekly on cron; actions SHA-pinned with Dependabot refresh (see [[development]]).
