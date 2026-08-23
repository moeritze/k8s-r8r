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

## Status

v0 alpha (2026-08). Bootstrap change `bootstrap-k8s-r8r-operator`: 36/36 tasks, unit+envtest 207 tests, e2e green on kind fleet. Post-v1 topics: licensing model, contribution setup, distribution.
