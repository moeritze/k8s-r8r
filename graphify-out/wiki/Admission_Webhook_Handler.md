# Admission Webhook Handler

> 32 nodes

## Key Concepts

- **webhook_test.go** (25 connections) — `internal/webhook/webhook_test.go`
- **T** (24 connections) — `internal/webhook/webhook_test.go`
- **admissionRequest()** (22 connections) — `internal/webhook/webhook_test.go`
- **newHandler()** (21 connections) — `internal/webhook/webhook_test.go`
- **testPolicy()** (16 connections) — `internal/webhook/webhook_test.go`
- **requireDenied()** (13 connections) — `internal/webhook/webhook_test.go`
- **requireAllowed()** (10 connections) — `internal/webhook/webhook_test.go`
- **TestMalformedSelectorDenied()** (6 connections) — `internal/webhook/webhook_test.go`
- **TestStarSelectorDenied()** (6 connections) — `internal/webhook/webhook_test.go`
- **TestInvalidTargetNameDenied()** (6 connections) — `internal/webhook/webhook_test.go`
- **TestInvalidTargetNamespaceDenied()** (6 connections) — `internal/webhook/webhook_test.go`
- **TestInvalidReplicateValueDenied()** (6 connections) — `internal/webhook/webhook_test.go`
- **TestSourceNamespaceDenied()** (6 connections) — `internal/webhook/webhook_test.go`
- **TestSourceKindDenied()** (6 connections) — `internal/webhook/webhook_test.go`
- **TestTargetNamespaceDenied()** (6 connections) — `internal/webhook/webhook_test.go`
- **TestUnsatisfiableClusterSelectorDenied()** (6 connections) — `internal/webhook/webhook_test.go`
- **TestAllowedRequestAdmitted()** (6 connections) — `internal/webhook/webhook_test.go`
- **TestTargetNamespaceDefaultsToSourceNamespace()** (6 connections) — `internal/webhook/webhook_test.go`
- **TestUnknownKeyWarnsButAdmits()** (6 connections) — `internal/webhook/webhook_test.go`
- **TestNamespaceSelectorPolicyFailsOpen()** (6 connections) — `internal/webhook/webhook_test.go`
- **TestNonAnnotatedObjectAdmitted()** (5 connections) — `internal/webhook/webhook_test.go`
- **TestAnnotationRemovalAdmitted()** (5 connections) — `internal/webhook/webhook_test.go`
- **TestReplicateFalseAdmitted()** (5 connections) — `internal/webhook/webhook_test.go`
- **TestNoPoliciesDenied()** (5 connections) — `internal/webhook/webhook_test.go`
- **TestNonSecretPayloadNeverInMessages()** (5 connections) — `internal/webhook/webhook_test.go`
- *... and 7 more nodes in this community*

## Relationships

- [[Annotation Parsing]] (1 shared connections)
- [[Community 110]] (1 shared connections)

## Source Files

- `internal/webhook/webhook_test.go`

## Audit Trail

- EXTRACTED: 244 (99%)
- INFERRED: 2 (1%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*