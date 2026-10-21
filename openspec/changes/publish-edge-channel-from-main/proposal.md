## Why

`release.yml` only publishes on `v*` tags, so between tags there is no installable artifact containing merged work. The first real-fleet trial was about to install `v0.1.0-alpha.1` (2026-08-24) against 24 commits and nine merged fixes that existed only on `main`; the only remedy was a manual tag cut under time pressure. A project whose validation loop is "merge fixes, then try them on a real ClusterAPI fleet" needs `main` to be installable at all times, without making every merge a release.

## What Changes

- **New edge publishing lane.** Every push to `main` publishes the operator image as `ghcr.io/moeritze/k8s-r8r:main` (moving) and `:sha-<short>` (immutable), and the Helm chart as `oci://ghcr.io/moeritze/charts/k8s-r8r` version `0.0.0-main.g<short>` with `appVersion: sha-<short>`. Multi-arch, cosign keyless signed, SPDX SBOM and SLSA provenance attached — identical guarantees to a release. No GitHub release, never `:latest`.
- **Shared publish steps.** The build/push/sign/attest and chart package/push/sign steps move into two composite actions under `.github/actions/`, used by both `release.yml` and the new `edge.yml`, so the supply-chain guarantees have one implementation.
- **Faster multi-arch builds.** The builder stage cross-compiles on the native runner (`--platform=$BUILDPLATFORM`) instead of compiling arm64 under QEMU; buildx layer cache via GitHub Actions cache. Applies to both lanes.
- **Retention.** A weekly scheduled workflow prunes `sha-*` image tags and `0.0.0-main.*` chart versions older than 30 days, with a dry-run mode. `main`, `latest` and semver tags are never touched.
- **Docs.** `docs/releasing.md` gains an "Edge channel" section (install, verify, and the explicit "for trials, not adoption" warning); `docs/quickstart.md` gains a trial-from-`main` snippet; README gets one line; CHANGELOG entry; `obsidian/development.md` and `obsidian/operations.md` updated.

Not changing: semver release semantics, `:latest` gating on stable tags, GitHub release notes, the tag-triggered `release.yml` contract. No auto-tagging, no release-please (a later, separate change could add release-please for the forgetting-to-tag problem; it would sit on top of this rather than replace it).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. This is CI/release tooling and documentation — no operator behaviour changes, so no capability spec changes. The change declares `skip_specs: true`, the same class as the archived `add-release-publishing` change. `openspec/specs/observability-operations` does not mention publishing or distribution; verified before opting out.

## Impact

- `.github/workflows/release.yml` — refactored to call the composite actions; tag/`:latest`/GitHub-release semantics unchanged. Its cosign certificate identity (`release.yml@refs/tags/v…`) is preserved, so the verification commands in `docs/releasing.md` keep working.
- `.github/workflows/edge.yml` (new), `.github/workflows/prune-edge.yml` (new), `.github/actions/publish-image/action.yml` (new), `.github/actions/publish-chart/action.yml` (new).
- `Dockerfile` — builder stage pinned to `$BUILDPLATFORM`; QEMU setup no longer required for the build.
- `docs/releasing.md`, `docs/quickstart.md`, `README.md`, `CHANGELOG.md`, `obsidian/development.md`, `obsidian/operations.md`.
- Trial installs at the maintainer's employer can move from "wait for a tag" to `helm upgrade --install … --version 0.0.0-main.g<sha>` within minutes of a merge.
- GHCR storage grows by one image index + one chart per merge until pruned; bounded by the 30-day retention.
