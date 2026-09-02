# Chart RBAC Roles

> 5 nodes

## Key Concepts

- **Replication CRD** (3 connections) — `charts/k8s-r8r/crds/r8r.io_replications.yaml`
- **k8s-r8r Helm Chart** (2 connections) — `charts/k8s-r8r/Chart.yaml`
- **ReplicationPolicy CRD** (2 connections) — `charts/k8s-r8r/crds/r8r.io_replicationpolicies.yaml`
- **k8s-r8r-policy-admin ClusterRole** (1 connections) — `charts/k8s-r8r/templates/personas.yaml`
- **k8s-r8r-replication-viewer ClusterRole** (1 connections) — `charts/k8s-r8r/templates/personas.yaml`

## Relationships

- [[Community 87]] (1 shared connections)

## Source Files

- `charts/k8s-r8r/Chart.yaml`
- `charts/k8s-r8r/crds/r8r.io_replicationpolicies.yaml`
- `charts/k8s-r8r/crds/r8r.io_replications.yaml`
- `charts/k8s-r8r/templates/personas.yaml`

## Audit Trail

- EXTRACTED: 8 (89%)
- INFERRED: 1 (11%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*