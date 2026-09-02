# Policy and CRD Concepts

> 30 nodes

## Key Concepts

- **Default Kustomization Overlay** (9 connections) — `config/default/kustomization.yaml`
- **Replication CRD** (7 connections) — `config/crd/bases/r8r.io_replications.yaml`
- **Controller Manager Deployment** (7 connections) — `config/manager/manager.yaml`
- **ReplicationPolicy CRD** (6 connections) — `config/crd/bases/r8r.io_replicationpolicies.yaml`
- **Controller Manager Metrics Service** (5 connections) — `config/default/metrics_service.yaml`
- **Cert-Manager Metrics Certs Manager Patch** (4 connections) — `config/default/cert_metrics_manager_patch.yaml`
- **ServiceMonitor TLS Patch** (4 connections) — `config/prometheus/monitor_tls_patch.yaml`
- **CRD Kustomization** (3 connections) — `config/crd/kustomization.yaml`
- **Manager HTTPS Metrics Patch** (3 connections) — `config/default/manager_metrics_patch.yaml`
- **Allow Metrics Traffic NetworkPolicy** (3 connections) — `config/network-policy/allow-metrics-traffic.yaml`
- **Prometheus Kustomization** (3 connections) — `config/prometheus/kustomization.yaml`
- **Controller Manager ServiceMonitor** (3 connections) — `config/prometheus/monitor.yaml`
- **Leader Election Role** (3 connections) — `config/rbac/leader_election_role.yaml`
- **Cert-Manager Kustomization** (2 connections) — `config/certmanager/kustomization.yaml`
- **ConflictPolicy (Fail/Overwrite/Adopt)** (2 connections) — `config/crd/bases/r8r.io_replicationpolicies.yaml`
- **RevocationPolicy (Retain/Delete)** (2 connections) — `config/crd/bases/r8r.io_replicationpolicies.yaml`
- **ResolvedTarget Fanout Entry** (2 connections) — `config/crd/bases/r8r.io_replications.yaml`
- **Replica Inventory** (2 connections) — `config/crd/bases/r8r.io_replications.yaml`
- **metrics-server-cert Secret** (2 connections) — `config/default/cert_metrics_manager_patch.yaml`
- **Manager Kustomization** (2 connections) — `config/manager/kustomization.yaml`
- **Network Policy Kustomization** (2 connections) — `config/network-policy/kustomization.yaml`
- **RBAC Kustomization** (2 connections) — `config/rbac/kustomization.yaml`
- **Issuer Name Reference Kustomize Config** (1 connections) — `config/certmanager/kustomizeconfig.yaml`
- **Default-Deny Policy Allowlist** (1 connections) — `config/crd/bases/r8r.io_replicationpolicies.yaml`
- **AllowNamespaceCreation Gate** (1 connections) — `config/crd/bases/r8r.io_replicationpolicies.yaml`
- *... and 5 more nodes in this community*

## Relationships

- No strong cross-community connections detected

## Source Files

- `config/certmanager/kustomization.yaml`
- `config/certmanager/kustomizeconfig.yaml`
- `config/crd/bases/r8r.io_replicationpolicies.yaml`
- `config/crd/bases/r8r.io_replications.yaml`
- `config/crd/kustomization.yaml`
- `config/default/cert_metrics_manager_patch.yaml`
- `config/default/kustomization.yaml`
- `config/default/manager_metrics_patch.yaml`
- `config/default/metrics_service.yaml`
- `config/manager/kustomization.yaml`
- `config/manager/manager.yaml`
- `config/network-policy/allow-metrics-traffic.yaml`
- `config/network-policy/kustomization.yaml`
- `config/prometheus/kustomization.yaml`
- `config/prometheus/monitor.yaml`
- `config/prometheus/monitor_tls_patch.yaml`
- `config/rbac/kustomization.yaml`
- `config/rbac/leader_election_role.yaml`
- `config/rbac/leader_election_role_binding.yaml`

## Audit Trail

- EXTRACTED: 56 (65%)
- INFERRED: 18 (21%)
- AMBIGUOUS: 12 (14%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*