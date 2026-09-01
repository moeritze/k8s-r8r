# Replication Status Building

> 8 nodes

## Key Concepts

- **buildStatus()** (10 connections) — `internal/engine/status.go`
- **status.go** (3 connections) — `internal/engine/status.go`
- **targetState** (3 connections) — `internal/engine/status.go`
- **dominantReason()** (3 connections) — `internal/engine/status.go`
- **Replication** (1 connections) — `internal/engine/status.go`
- **InventoryEntry** (1 connections) — `internal/engine/status.go`
- **Time** (1 connections) — `internal/engine/status.go`
- **ReplicationStatus** (1 connections) — `internal/engine/status.go`

## Relationships

- [[Reconcile Helpers]] (3 shared connections)

## Source Files

- `internal/engine/status.go`

## Audit Trail

- EXTRACTED: 20 (87%)
- INFERRED: 3 (13%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*