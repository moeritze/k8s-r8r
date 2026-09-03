# Metadata Stripping Design

> 13 nodes

## Key Concepts

- **Requirement: Drift detection via metadata watches** (7 connections) — `openspec/specs/replication-engine/spec.md`
- **Requirement: Replica creation and identity** (6 connections) — `openspec/specs/replication-engine/spec.md`
- **Requirement: Secret-safe telemetry** (6 connections) — `openspec/specs/observability-operations/spec.md`
- **isStrippedKey predicate** (4 connections) — `openspec/changes/strip-foreign-ownership-metadata/design.md`
- **SetDiscoverySnapshot wiring** (3 connections) — `openspec/changes/negotiate-capi-cluster-version/design.md`
- **SourceHash canonical payload hash** (3 connections) — `openspec/changes/strip-foreign-ownership-metadata/design.md`
- **SetExtraStrippedKeys package-level configuration** (3 connections) — `openspec/changes/strip-foreign-ownership-metadata/design.md`
- **D3: Drift Detection via Metadata-Only Filtered Informers** (2 connections) — `openspec/changes/archive/2026-08-23-bootstrap-k8s-r8r-operator/design.md`
- **Restate the Requirement Rather Than Weaken It** (2 connections) — `openspec/changes/document-spoke-rbac-kind-scope/design.md`
- **Tasks: strip foreign ownership metadata** (2 connections) — `openspec/changes/strip-foreign-ownership-metadata/tasks.md`
- **k8s_r8r_discovery_up metric** (1 connections) — `openspec/changes/negotiate-capi-cluster-version/design.md`
- **--strip-metadata-keys flag** (1 connections) — `openspec/changes/strip-foreign-ownership-metadata/proposal.md`
- **Foreign ownership denylist** (1 connections) — `openspec/changes/strip-foreign-ownership-metadata/proposal.md`

## Relationships

- [[Drift and Conflict Rationale]] (4 shared connections)
- [[Observability Requirements]] (2 shared connections)
- [[Community 113]] (2 shared connections)
- [[Community 112]] (1 shared connections)

## Source Files

- `openspec/changes/archive/2026-08-23-bootstrap-k8s-r8r-operator/design.md`
- `openspec/changes/document-spoke-rbac-kind-scope/design.md`
- `openspec/changes/negotiate-capi-cluster-version/design.md`
- `openspec/changes/strip-foreign-ownership-metadata/design.md`
- `openspec/changes/strip-foreign-ownership-metadata/proposal.md`
- `openspec/changes/strip-foreign-ownership-metadata/tasks.md`
- `openspec/specs/observability-operations/spec.md`
- `openspec/specs/replication-engine/spec.md`

## Audit Trail

- EXTRACTED: 27 (66%)
- INFERRED: 14 (34%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*