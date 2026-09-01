# Conflict Handling

> 43 nodes

## Key Concepts

- **SourceHash()** (13 connections) — `internal/engine/render.go`
- **render.go** (11 connections) — `internal/engine/render.go`
- **render_test.go** (11 connections) — `internal/engine/render_test.go`
- **testSecret()** (11 connections) — `internal/engine/render_test.go`
- **DecideConflict()** (10 connections) — `internal/engine/conflict.go`
- **T** (9 connections) — `internal/engine/render_test.go`
- **TestDecideConflict()** (6 connections) — `internal/engine/conflict_test.go`
- **.Render()** (6 connections) — `internal/engine/render.go`
- **Unstructured** (6 connections) — `internal/engine/render.go`
- **withMetadata()** (6 connections) — `internal/engine/render_test.go`
- **TestSetExtraStrippedKeys()** (6 connections) — `internal/engine/render_test.go`
- **TestRender_StripsForeignOwnershipMetadata()** (5 connections) — `internal/engine/render_test.go`
- **conflict.go** (4 connections) — `internal/engine/conflict.go`
- **EffectiveConflictPolicy()** (4 connections) — `internal/engine/conflict.go`
- **Renderer** (4 connections) — `internal/engine/render.go`
- **.AdoptPatch()** (4 connections) — `internal/engine/render.go`
- **canonicalPayload()** (4 connections) — `internal/engine/render.go`
- **namespacePayload()** (4 connections) — `internal/engine/render.go`
- **IsManagedReplica()** (4 connections) — `internal/engine/render.go`
- **TestSourceHash_Deterministic()** (4 connections) — `internal/engine/render_test.go`
- **TestSourceHash_IgnoresIdentityAndPipelineKeys()** (4 connections) — `internal/engine/render_test.go`
- **TestSourceHash_StableAcrossForeignMetadataStripping()** (4 connections) — `internal/engine/render_test.go`
- **ConflictDecision** (3 connections) — `internal/engine/conflict.go`
- **conflict_test.go** (3 connections) — `internal/engine/conflict_test.go`
- **TestEffectiveConflictPolicy()** (3 connections) — `internal/engine/conflict_test.go`
- *... and 18 more nodes in this community*

## Relationships

- [[Server-Side Apply Path]] (4 shared connections)
- [[Reconcile Helpers]] (3 shared connections)
- [[Transport Test Doubles]] (2 shared connections)
- [[Manager Wiring (main.go)]] (2 shared connections)

## Source Files

- `internal/engine/conflict.go`
- `internal/engine/conflict_test.go`
- `internal/engine/render.go`
- `internal/engine/render_test.go`

## Audit Trail

- EXTRACTED: 159 (83%)
- INFERRED: 32 (17%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*