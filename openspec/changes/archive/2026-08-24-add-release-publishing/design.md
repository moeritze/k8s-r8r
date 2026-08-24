# Design — release publishing

## Context

See proposal.md — Why. Repo CI style: SHA-pinned actions, `permissions: {}`
at workflow top level with per-job grants. Chart already defaults
`image.repository` to `ghcr.io/moeritze/k8s-r8r` and falls back to
`.Chart.AppVersion` for the tag.

## Decisions

- **Trigger**: push of tags `v*`. The tag is the single source of truth:
  image tag = `<tag>`, chart version = `<tag without v>`, chart appVersion =
  `<tag>` — so the chart's default image tag (`.Chart.AppVersion`) resolves
  to the image published by the same run.
- **`:latest` gating**: only tags without `-` (non-prerelease semver) also
  push `:latest`. Prereleases (`v0.2.0-alpha.1`) never move `latest`.
- **Registry**: ghcr.io with `GITHUB_TOKEN` (`packages: write`) — no extra
  secrets. Chart goes to `oci://ghcr.io/moeritze/charts` so image and chart
  packages don't collide.
- **Action pinning**: docker/* actions pinned to commit SHAs resolved from
  the GitHub API at authoring time (latest releases); `actions/checkout`
  reuses the SHA already pinned in ci.yml.
- **GitHub release via `gh` CLI** (`gh release create --generate-notes`)
  instead of a third-party release action — one fewer supply-chain pin;
  `gh` is preinstalled on runners. Same for `helm` (preinstalled).

## Risks / Trade-offs

- [ghcr packages default to private on first publish] → one-time manual
  visibility flip by the maintainer, documented in docs/releasing.md.
- [multi-arch arm64 build under QEMU is slow] → acceptable for tag-only
  cadence; no PR latency impact.

## Migration Plan

Additive only. Rollback = delete the workflow file; published artifacts are
immutable and stay available.

## Open Questions

None.
