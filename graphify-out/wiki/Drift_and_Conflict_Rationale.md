# Drift and Conflict Rationale

> 19 nodes

## Key Concepts

- **Requirement: ClusterAPI provider** (9 connections) — `openspec/specs/cluster-discovery/spec.md`
- **Requirement: Cluster runtime lifecycle** (9 connections) — `openspec/specs/cluster-discovery/spec.md`
- **Requirement: Inventory and garbage collection** (8 connections) — `openspec/specs/replication-engine/spec.md`
- **Requirement: Minimal-privilege credential bootstrap** (7 connections) — `openspec/specs/cluster-discovery/spec.md`
- **observability-operations capability** (6 connections) — `openspec/specs/observability-operations/spec.md`
- **cluster-discovery capability** (5 connections) — `openspec/specs/cluster-discovery/spec.md`
- **Requirement: Pluggable discovery interface** (5 connections) — `openspec/specs/cluster-discovery/spec.md`
- **Delta: ClusterAPI provider (version negotiation)** (4 connections) — `openspec/changes/negotiate-capi-cluster-version/specs/cluster-discovery/spec.md`
- **resolveClusterGVR** (4 connections) — `openspec/changes/negotiate-capi-cluster-version/design.md`
- **D2: Resolve in Start, fail loudly, before the informer** (4 connections) — `openspec/changes/negotiate-capi-cluster-version/design.md`
- **e2e CAPI Cluster CRD (v1beta2 + v1beta1)** (4 connections) — `test/e2e/testdata/capi-cluster-crd.yaml`
- **Requirement: Health and readiness probes** (3 connections) — `openspec/specs/observability-operations/spec.md`
- **Tasks: CAPI version negotiation** (3 connections) — `openspec/changes/negotiate-capi-cluster-version/tasks.md`
- **Requirement: Rate-limited structured events** (2 connections) — `openspec/specs/observability-operations/spec.md`
- **Requirement: Leader election and single-writer semantics** (2 connections) — `openspec/specs/observability-operations/spec.md`
- **k8s_r8r_clusters metric (runtime-manager count)** (2 connections) — `openspec/changes/negotiate-capi-cluster-version/design.md`
- **supportedClusterVersions preference list** (1 connections) — `openspec/changes/negotiate-capi-cluster-version/design.md`
- **D5: Ready conditions are already version-tolerant** (1 connections) — `openspec/changes/negotiate-capi-cluster-version/design.md`
- **k8s_r8r_discovery_clusters metric** (1 connections) — `openspec/changes/negotiate-capi-cluster-version/design.md`

## Relationships

- [[Webhook Doctrine and Specs]] (5 shared connections)
- [[Community 112]] (4 shared connections)
- [[Metadata Stripping Design]] (4 shared connections)
- [[Community 113]] (2 shared connections)
- [[Push and Credential Rationale]] (2 shared connections)
- [[Observability Requirements]] (2 shared connections)
- [[Canonical Replication Object]] (1 shared connections)

## Source Files

- `openspec/changes/negotiate-capi-cluster-version/design.md`
- `openspec/changes/negotiate-capi-cluster-version/specs/cluster-discovery/spec.md`
- `openspec/changes/negotiate-capi-cluster-version/tasks.md`
- `openspec/specs/cluster-discovery/spec.md`
- `openspec/specs/observability-operations/spec.md`
- `openspec/specs/replication-engine/spec.md`
- `test/e2e/testdata/capi-cluster-crd.yaml`

## Audit Trail

- EXTRACTED: 74 (92%)
- INFERRED: 6 (8%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*