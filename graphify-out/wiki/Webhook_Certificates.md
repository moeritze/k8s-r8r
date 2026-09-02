# Webhook Certificates

> 8 nodes

## Key Concepts

- **vreplicationrequest ValidatingWebhookConfiguration** (3 connections) — `charts/k8s-r8r/templates/webhook.yaml`
- **Kustomize Self-Signed Issuer** (3 connections) — `config/certmanager/issuer.yaml`
- **Annotation-Based Replication Request** (3 connections) — `README.md`
- **Advisory Admission Webhook Design (D6)** (2 connections) — `charts/k8s-r8r/templates/webhook.yaml`
- **Chart Self-Signed Issuer** (2 connections) — `charts/k8s-r8r/templates/webhook.yaml`
- **Chart Webhook Serving Certificate** (2 connections) — `charts/k8s-r8r/templates/webhook.yaml`
- **Kustomize Webhook Serving Certificate** (2 connections) — `config/certmanager/certificate.yaml`
- **Webhook Service** (1 connections) — `charts/k8s-r8r/templates/webhook.yaml`

## Relationships

- [[Replica Metadata Flags]] (1 shared connections)
- [[Secret-Safety Concepts]] (1 shared connections)

## Source Files

- `README.md`
- `charts/k8s-r8r/templates/webhook.yaml`
- `config/certmanager/certificate.yaml`
- `config/certmanager/issuer.yaml`

## Audit Trail

- EXTRACTED: 12 (67%)
- INFERRED: 6 (33%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*