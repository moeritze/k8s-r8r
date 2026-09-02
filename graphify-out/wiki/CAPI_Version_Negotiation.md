# CAPI Version Negotiation

> 13 nodes

## Key Concepts

- **.Handle()** (11 connections) — `internal/webhook/webhook.go`
- **webhook.go** (4 connections) — `internal/webhook/webhook.go`
- **Handler** (4 connections) — `internal/webhook/webhook.go`
- **NewHandler()** (4 connections) — `internal/webhook/webhook.go`
- **IncWebhookDenial()** (3 connections) — `internal/telemetry/metrics.go`
- **Setup()** (3 connections) — `internal/webhook/webhook.go`
- **warningsFor()** (3 connections) — `internal/webhook/webhook.go`
- **Reader** (2 connections) — `internal/webhook/webhook.go`
- **Manager** (1 connections) — `internal/webhook/webhook.go`
- **Context** (1 connections) — `internal/webhook/webhook.go`
- **Request** (1 connections) — `internal/webhook/webhook.go`
- **Response** (1 connections) — `internal/webhook/webhook.go`
- **parsedRequest** (1 connections) — `internal/webhook/webhook.go`

## Relationships

- [[Prometheus Collectors]] (2 shared connections)
- [[Policy Evaluation Types]] (2 shared connections)
- [[Annotation Parsing]] (2 shared connections)
- [[Community 110]] (1 shared connections)

## Source Files

- `internal/telemetry/metrics.go`
- `internal/webhook/webhook.go`

## Audit Trail

- EXTRACTED: 31 (79%)
- INFERRED: 8 (21%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*