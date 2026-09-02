# Uninstall and Orphans

> 6 nodes

## Key Concepts

- **Clean teardown order** (6 connections) — `docs/uninstall.md`
- **1. Remove replication requests (replicas get garbage-collected)** (1 connections) — `docs/uninstall.md`
- **2. Wait for cleanup to finish** (1 connections) — `docs/uninstall.md`
- **3. Uninstall the chart** (1 connections) — `docs/uninstall.md`
- **4. Optionally delete the CRDs** (1 connections) — `docs/uninstall.md`
- **5. Spoke leftovers** (1 connections) — `docs/uninstall.md`

## Relationships

- [[Annotation Contract Docs]] (1 shared connections)

## Source Files

- `docs/uninstall.md`

## Audit Trail

- EXTRACTED: 11 (100%)
- INFERRED: 0 (0%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*