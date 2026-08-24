# Add release publishing

## Why

k8s-r8r has no published artifacts: no container image on a registry and no
chart in an OCI repository. Anyone who wants to run the operator on a real
cluster must clone the repo and build the image themselves, which blocks
real-cluster adoption and makes the "install via Helm" story in the README
half-true.

## What Changes

- New tag-triggered GitHub Actions workflow (`.github/workflows/release.yml`)
  that, on push of a `v*` tag:
  - builds and pushes a multi-arch (linux/amd64, linux/arm64) operator image
    to `ghcr.io/moeritze/k8s-r8r:<tag>` (plus `:latest` for non-prerelease
    semver tags),
  - packages the Helm chart with `--version <tag without v>` /
    `--app-version <tag>` and pushes it to `oci://ghcr.io/moeritze/charts`,
  - creates a GitHub release with generated notes (marked prerelease when
    the tag contains `-`).
- Docs: "Install from published artifacts" section in `docs/quickstart.md`,
  new `docs/releasing.md`, updated README install snippet, updated
  `obsidian/development.md` and `obsidian/operations.md`.

No operator behavior changes. This is pure release tooling + docs, hence
`skip_specs: true`.

## Capabilities

### New Capabilities

None — release tooling only, no spec-level behavior.

### Modified Capabilities

None.

## Impact

- `.github/workflows/release.yml` (new)
- `docs/quickstart.md`, `docs/releasing.md` (new), `README.md`
- `obsidian/development.md`, `obsidian/operations.md`
- No Go code, no chart template changes (values.yaml already defaults to
  `ghcr.io/moeritze/k8s-r8r` with the `.Chart.AppVersion` tag fallback).
