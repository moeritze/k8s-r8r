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

Hub holds fleet write access → mitigated by [[cluster-discovery#Credential bootstrap (D5)|one-shot narrow SA bootstrap]]: admin kubeconfig used once, steady state runs on short-lived rotated SA tokens scoped to allowlisted kinds. Pull-agent transport is a future `Transport` implementation.

**How narrow the SA actually is (be precise, the docs used to overstate this).** The spoke `k8s-r8r-replicator` ClusterRole grants `get,list,watch,create,update,patch,delete` on each kind in `--allowed-kinds` (default `secrets,configmaps`) plus `get,create,patch` on namespaces — no `resourceNames`, and bound via ClusterRoleBinding, so **every namespace** of the spoke. The `managed-by` selector on spoke caches filters what the operator *watches*; it is not an authz boundary.

Two intended narrowings are not built yet:

- **Not policy-derived** — scope comes from the `--allowed-kinds` flag, never from [[policy-model]]. Grant is a superset of what policy permits and exists from first cluster discovery, before any policy. Re-narrowing tracks an `--allowed-kinds` change (restart → re-bootstrap every spoke, full rule replacement), not a policy change. Deferred because *widening* needs an escalation-capable credential, so policy-driven widening would demote the admin kubeconfig from one-shot to routine — worse for D5 than the broad grant. Tracked: [#29](https://github.com/moeritze/k8s-r8r/issues/29).
- **Not per-namespace** — ClusterRole, not per-namespace Roles. `RBACScope` (`internal/cluster/bootstrap.go`) is the seam for both.

So: reduction from fleet admin is real (no node/workload/RBAC control) but **narrower-than, not bounded** — with `secrets` allowlisted the SA reads and deletes every Secret in `kube-system`, `cert-manager`, `argocd`. Sizing: not an *escalation* (hub already holds the CAPI admin kubeconfigs), it defeats the *reduction* D5 promises. Untouched and fully delivered: no static credential in the spoke rest config, short-lived rotated tokens, admin kubeconfig used once per bootstrap. Operator lever until #29 lands: chart `allowedKinds`.

## Advisory webhook doctrine (D6)

Validating webhook (CEL matchConditions: fires only on objects carrying `r8r.io/` annotations; K8s ≥ 1.30) gives apply-time errors naming the failing policy dimension. `failurePolicy: Ignore` is **mandatory** — `Fail` would let operator downtime block all annotated secret writes. Security never depends on it: reconcile-time policy evaluation is authoritative; a bypassed webhook means worse error messages, never unauthorized replication.

The deny/warn split follows from that doctrine: **deny** only what no policy could ever allow or what the shared parser cannot read (a malformed `r8r.io/conflict-policy`, say — the key is part of the closed request contract, so its value set is validated at admission); **warn** for everything advisory, including a `conflict-policy` no candidate policy permits. That request replicates normally and only its conflict handling falls back to the weaker key, so denying it would block a legitimate write over a hypothetical conflict.

## Secret-safe telemetry

No log, event, condition, or metric ever contains payload data — hashes only. Enforced twice: runtime canary test + AST-walking static ratchet (`internal/telemetry/secretsafety_audit_test.go`) that fails on any `Data`/`StringData` reaching a formatting/event call. Checklist: `internal/telemetry/REVIEW_CHECKLIST.md`.

The `DriftCorrected` event is the design under load: it must say *that* a replicated Secret was rewritten without saying *what* changed, so it carries the replica ref and the two full `sha256:` hashes and nothing else. Full hashes rather than prefixes — they are not secrets, they compare directly against `status.sourceHash` and the replica's annotation, and truncation would make distinct drifts collide into one rate-limiter key. Reaching for the object's `data` to describe the diff would trip the ratchet, correctly. See [[drift-detection]].

## Weaponization guards

- `Overwrite` conflict mode could replace a victim cluster's secret → a genuine **two-key turn** since [#34](https://github.com/moeritze/k8s-r8r/issues/34): the engine acts on the *weaker* of the source's `r8r.io/conflict-policy` annotation and the policy grant, and an absent annotation means `Fail`. An admin grant alone no longer overwrites anything, and a request asking for `Overwrite` gets no more than its policies permit. Replicas of a *different* source are never taken over whatever either key says ([[replication-flow#Conflict handling is a two-key turn|mechanism]]). Note this cuts both ways for upgrades: fleets that relied on a grant-only `Overwrite` or `Adopt` now report `Conflict` until each source opts in.
- Exfiltration via annotation → blocked by default-deny [[policy-model]]. Every destination a request names is re-evaluated against policy on every reconcile; an annotation can only narrow what a policy already permits, never widen it.
- **Laundering a fanout through a second replicator** → blocked by [[replication-flow|metadata hygiene]]. A replica that inherited `replicator.v1.mittwald.de/replicate-to-clusters` (or the emberstack, Argo CD, Helm or Flux ownership keys) would be a valid *source* for that other controller — a second fanout no `ReplicationPolicy` ever saw. The engine strips those prefixes from the replica **and** from the canonical hash (`isStrippedKey`, `internal/engine/render.go`), operators can extend the denylist with `--strip-metadata-keys` / chart `stripMetadataKeys`. Fixed in [#26](https://github.com/moeritze/k8s-r8r/issues/26); the default-deny claim above depends on it.

## Known gaps (open issues)

Honest inventory of controls that are weaker than they read. None is an escalation; each is a promise the code does not yet keep.

| # | Gap | Where it bites |
|---|---|---|
| [#35](https://github.com/moeritze/k8s-r8r/issues/35) | `Adopt` rewrites `app.kubernetes.io/managed-by` on a **pre-existing** object (`Renderer.AdoptPatch`), permanently breaking Helm's ownership check, and files it in inventory — so a later revocation with `revocationPolicy: Delete` deletes an object k8s-r8r never created. The two-key turn now requires a per-source opt-in before this can happen, which narrows the blast radius but does not close the issue | [[replication-flow]], [[policy-model]] |
| [#36](https://github.com/moeritze/k8s-r8r/issues/36) | Rewriting `managed-by` on a replica drops it out of the label-filtered spoke cache; drift detection then never sees it again while status still reports the target ready | [[drift-detection]] |
| [#29](https://github.com/moeritze/k8s-r8r/issues/29) | Spoke RBAC scoped to `--allowed-kinds`, not to the policy universe (above) | [[cluster-discovery]] |

## Supply chain

Released images/charts are cosign-signed (keyless, GitHub OIDC — no long-lived key), carry SPDX SBOMs and SLSA provenance attestations, and are OCI-labelled back to the repo. Verify before running a fleet-wide Secret reader: `../docs/releasing.md#verifying-a-release`. Build-side details and rationale: [[development#Supply chain (what a release proves)|what a release proves]].

Repo hygiene: public repo — gitleaks pre-push hook + CI job gate every push; govulncheck on every PR and weekly on cron; actions SHA-pinned with Dependabot refresh (see [[development]]).
