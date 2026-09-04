---
tags: [hub]
aliases: [kubernetes-replicator, home]
---

# k8s-r8r

Declarative fanout of Kubernetes objects (Secrets, ConfigMaps) across a fleet — annotation-driven, policy-gated, with pluggable cluster discovery (ClusterAPI first). Built in Go on controller-runtime. Public repo: [github.com/moeritze/k8s-r8r](https://github.com/moeritze/k8s-r8r).

> One annotation on a Secret + one admin policy = replicas on every selected cluster, tracked, drift-repaired, garbage-collected.

## Functionality

- [[replication-flow]] — the core path: annotation → `Replication` object → engine → replicas on spokes
- [[policy-model]] — default-deny `ReplicationPolicy` gating: who may replicate what, where
- [[cluster-discovery]] — how the fleet is discovered (ClusterAPI) and spoke credentials are bootstrapped
- [[drift-detection]] — how replicas stay in sync without the hub caching secret data
- [[security-model]] — threat model, RBAC personas, advisory webhook doctrine
- [[operations]] — metrics, events, HA, status size discipline
- [[architecture]] — component map and design decisions D1–D10
- [[development]] — repo layout, testing (envtest + kind e2e), tooling workflow

## Deep reference

- `graph/` — auto-generated knowledge graph of the whole codebase (open `graph/graph.canvas`, regenerate per [[development]])
- `../docs/` — user-facing docs (quickstart, annotations, policies, gitops, security, uninstall)
- `../openspec/` — requirement specs + change history (source of truth for behavior)
- [[history/original-brainstorm]] — where this started

## Known gaps

Open issues where behaviour is thinner than a reader would expect. Each is described in place in the note that owns it — this is only the index.

| # | Gap | Note |
|---|---|---|
| [#29](https://github.com/moeritze/k8s-r8r/issues/29) | Spoke RBAC scoped to `--allowed-kinds`, not to the policy universe — full replica verbs in every spoke namespace, no `resourceNames` | [[security-model]], [[cluster-discovery]] |
| [#31](https://github.com/moeritze/k8s-r8r/issues/31) | No `targets.namespaceSelector`: policy target namespaces are exact names only, while sources may use a selector | [[policy-model]] |
| [#35](https://github.com/moeritze/k8s-r8r/issues/35) | `Adopt` rewrites `managed-by` (breaks Helm ownership) and revocation deletes the adopted object | [[replication-flow]], [[policy-model]] |
| [#36](https://github.com/moeritze/k8s-r8r/issues/36) | Drift goes permanently blind if `managed-by` is rewritten on a replica | [[drift-detection]] |
| [#37](https://github.com/moeritze/k8s-r8r/issues/37) | `discovery.Options.Settings` is never populated, so provider settings are unreachable | [[cluster-discovery]] |

Closed since the last vault pass, now described as behaviour rather than listed here: [#27](https://github.com/moeritze/k8s-r8r/issues/27) (status truthfulness → [[replication-flow#5. Status: one writer per condition|condition ownership]]), [#30](https://github.com/moeritze/k8s-r8r/issues/30) (drift corrections are observable → [[drift-detection]]), [#34](https://github.com/moeritze/k8s-r8r/issues/34) (conflict two-key turn → [[replication-flow#Conflict handling is a two-key turn|conflict handling]]). #34 and #27 both change observable behaviour on upgrade — see [[operations#What to alert on|alerting]] and the breaking-change note in [[replication-flow]].

## Status

v0 alpha, `v0.1.0-alpha.1` published (2026-09). Bootstrap change `bootstrap-k8s-r8r-operator` complete (36/36 tasks); `make test` green at 133 test functions / 275 cases, e2e green on the kind fleet. Five OpenSpec changes archived, their deltas promoted into `openspec/specs`. Since the release: foreign-ownership metadata stripping, CAPI API-version negotiation, core-v1 event recording, spoke-RBAC doc correction, truthful `Replication` status, observable drift corrections, request-side conflict consent. Post-v1 topics: licensing model, contribution setup, distribution.

Two of those are **breaking on upgrade** and worth reading before rolling forward: `Replication`s that reported `Ready=True, 0/0 targets ready` now report `Ready=False`/`NoTargets`, and conflict handling now requires the `r8r.io/conflict-policy` annotation in addition to the policy grant.
