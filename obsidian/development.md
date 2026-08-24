---
tags: [development, workflow]
---

# Development

## Layout

kubebuilder project, module `github.com/moeritze/k8s-r8r`, API group `r8r.io/v1alpha1`. Package map: [[architecture]].

## Testing ladder

1. **Unit + envtest** — `make test` (207 tests; engine/request suites run against an envtest API server, spokes faked via recording Transport)
2. **e2e** — `make test-e2e`: provisions a real kind fleet (`hack/kind-fleet.sh`, hub + 2 spokes), simulates ClusterAPI with a minimal CRD + status-patched `Cluster` objects + `--internal` kubeconfig Secrets, exercises full [[replication-flow]], [[cluster-discovery|cluster lifecycle]], and scale. `K8S_R8R_E2E_KEEP=1` keeps the fleet for debugging.
3. **CI** (`.github/workflows/ci.yml`) — lint (custom golangci-lint w/ logcheck), test, build, e2e. Lint runs the same binary locally via `make lint`.

## Release process

Pushing a semver tag (`v*`) triggers `.github/workflows/release.yml`: multi-arch image (amd64/arm64) → `ghcr.io/moeritze/k8s-r8r:<tag>` (stable tags also move `:latest`), Helm chart → `oci://ghcr.io/moeritze/charts/k8s-r8r` (chart version = tag without `v`, appVersion = tag, so the chart's default image tag matches the published image), plus a GitHub release with generated notes. Tags containing `-` (`-alpha.N` convention) are prereleases and never move `:latest`. Actions SHA-pinned, `GITHUB_TOKEN` only. Install side: [[operations]]; full walkthrough: `../docs/releasing.md`.

## Documentation workflow (keep in sync — part of every change)

Three layers, updated together:

1. **openspec** (`../openspec/`) — behavior source of truth. New work = `openspec new change`, artifacts per `/opsx:*` flow; specs updated when behavior changes.
2. **This vault** (`obsidian/`) — curated functionality notes ([[k8s-r8r|hub]]). Update the affected note in the same session as the code change.
3. **Knowledge graph** (`graph/` + `graphify-out/`) — regenerate incrementally with `/graphify --update` (or the post-commit hook for code changes); re-export with `graphify export obsidian --dir obsidian/graph`.

Guardrail: repo `CLAUDE.md` codifies this for agent sessions.

## Secret hygiene (public repo)

- `make install-hooks` installs the **gitleaks pre-push hook** — every push scanned; CI runs gitleaks too ([[security-model]])
- exported kubeconfigs (`bin/`), `.remember/`, graph build artifacts (`graphify-out/`) are gitignored
- example values in docs: obviously fake only

## Conventions

Conventional commits; Claude co-authorship trailer is deliberate policy (AI-usage transparency for contributors). DCO/CONTRIBUTING: planned post-v1.
