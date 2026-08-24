# Releasing

How a k8s-r8r release happens, and what it publishes.

## Cutting a release

Releases are cut by pushing a semver tag:

```sh
git tag v0.2.0
git push origin v0.2.0
```

The tag push triggers `.github/workflows/release.yml`, which publishes
three artifacts:

| Artifact | Location |
|---|---|
| Operator image (multi-arch: linux/amd64, linux/arm64) | `ghcr.io/moeritze/k8s-r8r:<tag>` — plus `:latest` for stable tags |
| Helm chart | `oci://ghcr.io/moeritze/charts/k8s-r8r`, chart version `<tag without v>` |
| GitHub release | Generated notes on the [releases page](https://github.com/moeritze/k8s-r8r/releases) |

The chart is packaged with `--app-version <tag>`, so its default image tag
(`.Chart.AppVersion`) resolves to the image published by the same run —
`helm install` from the OCI chart pulls the matching image with no value
overrides.

## Prereleases

Tags containing `-` follow the prerelease convention `-alpha.N` (e.g.
`v0.2.0-alpha.1`). They:

- are marked **prerelease** on the GitHub release,
- do **not** move the `:latest` image tag.

Stable tags (`v0.2.0`) also push `:latest` and publish a normal release.

## One-time setup note (ghcr visibility)

GitHub Container Registry packages may default to **private** on first
publish. After the first release creates the `k8s-r8r` image package and
the `charts/k8s-r8r` chart package, a maintainer must flip each package's
visibility to **public** in the package settings on GitHub (one-time per
package). Until then, anonymous pulls fail.

## Authentication

The workflow authenticates to ghcr with the workflow's `GITHUB_TOKEN`
(`packages: write`); no extra secrets are configured. All actions are
pinned to commit SHAs, matching the CI workflow's hardening style.
