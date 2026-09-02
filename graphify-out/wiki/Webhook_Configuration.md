# Webhook Configuration

> 8 nodes

## Key Concepts

- **ValidatingWebhookConfiguration** (6 connections) — `config/webhook/manifests.yaml`
- **NOTE.md** (5 connections) — `config/webhook/NOTE.md`
- **Webhook Kustomization** (3 connections) — `config/webhook/kustomization.yaml`
- **Advisory Webhook (failurePolicy Ignore)** (3 connections) — `config/webhook/manifests.yaml`
- **Webhook Service** (3 connections) — `config/webhook/service.yaml`
- **Webhook Kustomize Config** (2 connections) — `config/webhook/kustomizeconfig.yaml`
- **CEL MatchConditions Annotation Scoping** (2 connections) — `config/webhook/manifests.yaml`
- **Note for the Helm chart integration (later task group)** (1 connections) — `config/webhook/NOTE.md`

## Relationships

- [[Policy Authoring Guide]] (1 shared connections)

## Source Files

- `config/webhook/NOTE.md`
- `config/webhook/kustomization.yaml`
- `config/webhook/kustomizeconfig.yaml`
- `config/webhook/manifests.yaml`
- `config/webhook/service.yaml`

## Audit Trail

- EXTRACTED: 25 (100%)
- INFERRED: 0 (0%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*