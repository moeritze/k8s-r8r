# Replica Inventory and GC

> 17 nodes

## Key Concepts

- **KeyForEntry()** (12 connections) — `internal/engine/inventory.go`
- **PlanGC()** (8 connections) — `internal/engine/inventory.go`
- **inventory.go** (7 connections) — `internal/engine/inventory.go`
- **removeEntry()** (7 connections) — `internal/engine/inventory.go`
- **InventoryEntry** (6 connections) — `internal/engine/inventory.go`
- **entry()** (6 connections) — `internal/engine/inventory_test.go`
- **TestUpsertRemoveEntries()** (6 connections) — `internal/engine/inventory_test.go`
- **upsertEntry()** (5 connections) — `internal/engine/inventory.go`
- **entriesInNamespace()** (5 connections) — `internal/engine/inventory.go`
- **inventory_test.go** (5 connections) — `internal/engine/inventory_test.go`
- **TestPlanGC()** (5 connections) — `internal/engine/inventory_test.go`
- **TestPlanGC_AccountsForEveryEntry()** (5 connections) — `internal/engine/inventory_test.go`
- **SlotKey** (4 connections) — `internal/engine/inventory.go`
- **T** (4 connections) — `internal/engine/inventory_test.go`
- **TestEntriesInNamespace()** (4 connections) — `internal/engine/inventory_test.go`
- **GCPlan** (3 connections) — `internal/engine/inventory.go`
- **InventoryEntry** (1 connections) — `internal/engine/inventory_test.go`

## Relationships

- [[Reconcile Helpers]] (4 shared connections)
- [[Community 104]] (2 shared connections)
- [[Server-Side Apply Path]] (2 shared connections)
- [[Policy Revocation]] (1 shared connections)

## Source Files

- `internal/engine/inventory.go`
- `internal/engine/inventory_test.go`

## Audit Trail

- EXTRACTED: 68 (73%)
- INFERRED: 25 (27%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*