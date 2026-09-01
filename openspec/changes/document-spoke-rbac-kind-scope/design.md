# Design

## Context

Issue #29 reports that spoke RBAC is neither derived from the policy universe
nor re-narrowed. Verification against the tree splits that into two findings
with different dispositions.

**Verified as drift (spec is wrong, code is the reality):** the bootstrap
`RBACScope` comes from `--allowed-kinds` only. The grant is
`get,list,watch,create,update,patch,delete` on each allowlisted resource
(`internal/cluster/bootstrap.go:52`) with no `resourceNames` and no namespace
restriction, bound cluster-wide via a ClusterRoleBinding
(`internal/cluster/bootstrap.go:193-212`), plus `get,create,patch` on
namespaces. The `managed-by` label selector set on each spoke runtime
(`cmd/main.go:446-451`) is a *cache* filter, not an authorization boundary.

**Verified as already-working but undocumented:** re-narrowing on an allowlist
shrink. `UpdateRBAC` assigns `existing.Rules = desired`
(`internal/cluster/bootstrap.go:168`) — a full replacement, not a merge — and a
restart replays bootstrap for every spoke.

## Goals / Non-Goals

**Goals**

- The spec describes what the code does, and names what it does not do.
- The deferral is recorded with its reason and a tracking issue, so a reader
  can tell "not yet built" from "overlooked".
- The implemented shrink path gets the scenario it never had.
- Security docs stop overstating the blast-radius reduction.

**Non-Goals**

- Implementing policy-derived scoping. That needs the admin-credential decision
  below and gets its own change.
- Per-namespace narrowing (already a known, separately documented limitation).
- Any behavior change whatsoever in this change.

## Decisions

### Restate the requirement rather than weaken it

The alternative was to leave the aspirational wording and add a caveat. Rejected:
a SHALL that nothing implements and nothing tests is indistinguishable from a
lie in an audit, and the spec is meant to be the source of truth for what the
operator does today. The aspiration is preserved as an explicit deferral with
an issue reference, which is honest about both the current state and the
intent.

### Keep the blast-radius correction factual, not alarmist

The current text — reduction "from fleet admin to write allowlisted kinds in
spoke namespaces — still serious, but bounded and auditable" — overstates the
reduction: read-and-delete on every Secret in every namespace of every spoke
(`kube-system`, `cert-manager`, `argocd`, `flux-system` included) is not
meaningfully bounded away from fleet admin.

The correction says so, and also says what stays true: this is not a privilege
*escalation*. The hub already holds CAPI admin kubeconfigs, so the spoke SA
grant adds no reach a hub compromise did not already have. What the gap defeats
is the *reduction* design D5 promises — the SA is supposed to be the thing you
fall back to, and today it is not much smaller than what it replaces. The
genuine reductions D5 does deliver (no static credential in the spoke rest
config, short-lived rotated tokens, admin kubeconfig touched once per
bootstrap) are unaffected and stay stated.

### Why policy-derived scoping is deferred rather than fixed here

The naive shape — watch `ReplicationPolicy`, recompute the scope, call
`UpdateRBAC` — is worse than the bug it fixes. Widening a spoke grant needs a
credential that can escalate, which means holding the provider admin
kubeconfig for routine policy edits instead of once per bootstrap, converting
an auditable rare event into a routine one and undermining D5 more than the
over-broad grant does.

The promising alternative is to let the spoke SA narrow *itself*: grant it
`get,update` on its own ClusterRole by `resourceNames`, so RBAC
escalation-prevention makes widening structurally impossible while narrowing
needs no admin credential, and widening stays a bootstrap-class event. That is
a real design decision with a real blast-radius argument on both sides, and it
belongs in its own change with its own review — not smuggled in behind a docs
pass.

## Risks / Trade-offs

- **Disclosing a posture limitation in a public repo.** Mitigated by framing:
  this is a documented alpha limitation with a tracking issue, not a
  vulnerability with an exploit path, and it grants no reach a hub compromise
  did not already have. Leaving the docs overstating the reduction is the worse
  option — an operator could size their trust in the spoke SA boundary against
  a promise the code does not keep.
- **The spec temporarily encodes a weaker guarantee.** Accepted deliberately;
  #29 tracks restoring the stronger one, and the deferral text names it.
