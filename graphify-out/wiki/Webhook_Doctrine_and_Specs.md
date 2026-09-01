# Webhook Doctrine and Specs

> 20 nodes

## Key Concepts

- **replication-policy** (10 connections) — `openspec/specs/replication-policy/spec.md`
- **Requirement: Allowlist matching dimensions** (8 connections) — `openspec/specs/replication-policy/spec.md`
- **admission-validation** (7 connections) — `openspec/specs/admission-validation/spec.md`
- **Requirement: Annotation-based replication request** (6 connections) — `openspec/specs/replication-request/spec.md`
- **D10: Stack** (5 connections) — `openspec/changes/archive/2026-08-23-bootstrap-k8s-r8r-operator/design.md`
- **Requirement: Default deny** (5 connections) — `openspec/specs/replication-policy/spec.md`
- **Requirement: Advisory validation of replication requests** (5 connections) — `openspec/specs/admission-validation/spec.md`
- **D4: Policy = Default Deny, Allowlists Only, Union** (4 connections) — `openspec/changes/archive/2026-08-23-bootstrap-k8s-r8r-operator/design.md`
- **D6: Advisory Webhook, Controller Authoritative** (4 connections) — `openspec/changes/archive/2026-08-23-bootstrap-k8s-r8r-operator/design.md`
- **Requirement: Reconcile-time enforcement is authoritative** (4 connections) — `openspec/specs/replication-policy/spec.md`
- **Requirement: Webhook is never an availability or security dependency** (4 connections) — `openspec/specs/admission-validation/spec.md`
- **ReplicationPolicy CRD** (3 connections) — `openspec/changes/archive/2026-08-23-bootstrap-k8s-r8r-operator/proposal.md`
- **Requirement: Union semantics across policies** (3 connections) — `openspec/specs/replication-policy/spec.md`
- **Requirement: Webhook scope is limited to opted-in objects** (3 connections) — `openspec/specs/admission-validation/spec.md`
- **Requirement: Policy authoring is admin-scoped** (2 connections) — `openspec/specs/replication-policy/spec.md`
- **spec.md** (1 connections) — `openspec/specs/admission-validation/spec.md`
- **Purpose** (1 connections) — `openspec/specs/admission-validation/spec.md`
- **spec.md** (1 connections) — `openspec/specs/replication-policy/spec.md`
- **Purpose** (1 connections) — `openspec/specs/replication-policy/spec.md`
- **Cross-controller fanout hazard** (1 connections) — `openspec/changes/strip-foreign-ownership-metadata/proposal.md`

## Relationships

- [[Drift and Conflict Rationale]] (5 shared connections)
- [[Canonical Replication Object]] (3 shared connections)
- [[Community 112]] (3 shared connections)
- [[Community 113]] (2 shared connections)
- [[Community 73]] (1 shared connections)
- [[Release Publishing]] (1 shared connections)
- [[Community 75]] (1 shared connections)
- [[Community 81]] (1 shared connections)
- [[Push and Credential Rationale]] (1 shared connections)

## Source Files

- `openspec/changes/archive/2026-08-23-bootstrap-k8s-r8r-operator/design.md`
- `openspec/changes/archive/2026-08-23-bootstrap-k8s-r8r-operator/proposal.md`
- `openspec/changes/strip-foreign-ownership-metadata/proposal.md`
- `openspec/specs/admission-validation/spec.md`
- `openspec/specs/replication-policy/spec.md`
- `openspec/specs/replication-request/spec.md`

## Audit Trail

- EXTRACTED: 58 (74%)
- INFERRED: 20 (26%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*