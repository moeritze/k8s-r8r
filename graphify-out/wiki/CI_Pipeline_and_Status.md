# CI Pipeline and Status

> 12 nodes

## Key Concepts

- **Release: Publish Container Image** (4 connections) — `.github/workflows/release.yml`
- **CHANGELOG.md** (3 connections) — `CHANGELOG.md`
- **Changelog** (3 connections) — `CHANGELOG.md`
- **[Unreleased]** (3 connections) — `CHANGELOG.md`
- **[0.1.0-alpha.1] - 2026-08-24** (3 connections) — `CHANGELOG.md`
- **Release: Publish Helm Chart (OCI)** (3 connections) — `.github/workflows/release.yml`
- **Release: Create GitHub Release** (2 connections) — `.github/workflows/release.yml`
- **Added** (1 connections) — `CHANGELOG.md`
- **Fixed** (1 connections) — `CHANGELOG.md`
- **Added** (1 connections) — `CHANGELOG.md`
- **Fixed** (1 connections) — `CHANGELOG.md`
- **CI Build Job (binary + image)** (1 connections) — `.github/workflows/ci.yml`

## Relationships

- [[Community 107]] (1 shared connections)
- [[Secret-Safety Concepts]] (1 shared connections)

## Source Files

- `.github/workflows/ci.yml`
- `.github/workflows/release.yml`
- `CHANGELOG.md`

## Audit Trail

- EXTRACTED: 23 (88%)
- INFERRED: 3 (12%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*