# Releasing

How a k8s-r8r release happens, what it publishes, and how a consumer verifies
what they pulled.

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

## Supply-chain guarantees

Every published image and chart carries, in addition to the bits themselves:

| Guarantee | Mechanism | Where it lives |
|---|---|---|
| **OCI metadata** | `docker/metadata-action` + `LABEL`s in the `Dockerfile` | labels on each arch manifest, annotations on the multi-arch **index** (`DOCKER_METADATA_ANNOTATIONS_LEVELS=index,manifest`) |
| **Signature (image)** | `cosign sign --yes` on the pushed index digest, keyless via GitHub OIDC | `ghcr.io/moeritze/k8s-r8r:sha256-<digest>.sig` |
| **Signature (chart)** | same, on the digest reported by `helm push` | `ghcr.io/moeritze/charts/k8s-r8r:sha256-<digest>.sig` |
| **SBOM** | BuildKit `sbom: true` (SPDX, one per platform) | attestation manifests inside the image index |
| **Build provenance** | BuildKit `provenance: mode=max` | attestation manifests inside the image index |
| **SLSA provenance (signed)** | `actions/attest-build-provenance` with `push-to-registry` | GitHub attestations API **and** an OCI referrer of the image digest |

There is no long-lived signing key anywhere: signing certificates are
short-lived, issued by Sigstore against the workflow's OIDC identity, and
recorded in the public Rekor transparency log.

`org.opencontainers.image.source` is what links the ghcr package page back
to this repository — without it the package page shows no source and no
README.

## Verifying a release

Install [cosign](https://docs.sigstore.dev/cosign/system_config/installation/)
(`brew install cosign`). Replace `v0.2.0` with the tag you are verifying.

### 1. Verify the image signature

```sh
cosign verify \
  --certificate-identity "https://github.com/moeritze/k8s-r8r/.github/workflows/release.yml@refs/tags/v0.2.0" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  ghcr.io/moeritze/k8s-r8r:v0.2.0 | jq .
```

The identity is the workflow path plus the git ref that ran it — that is the
claim worth checking: *this image was built by release.yml, from this repo, at
this tag*. To accept any tag of this repo (e.g. in an admission policy), use a
regexp instead:

```sh
cosign verify \
  --certificate-identity-regexp "^https://github\.com/moeritze/k8s-r8r/\.github/workflows/release\.yml@refs/tags/v" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  ghcr.io/moeritze/k8s-r8r:v0.2.0
```

Prefer pinning by digest in production; verifying by tag resolves to the same
index digest that was signed:

```sh
digest=$(crane digest ghcr.io/moeritze/k8s-r8r:v0.2.0)
cosign verify --certificate-identity-regexp ... "ghcr.io/moeritze/k8s-r8r@${digest}"
```

### 2. Verify the Helm chart signature

```sh
cosign verify \
  --certificate-identity "https://github.com/moeritze/k8s-r8r/.github/workflows/release.yml@refs/tags/v0.2.0" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  ghcr.io/moeritze/charts/k8s-r8r:0.2.0
```

Note the chart tag has no `v` prefix (chart version = tag without `v`).

### 3. Verify build provenance

With the GitHub CLI (checks the signed SLSA attestation against the repo):

```sh
gh attestation verify oci://ghcr.io/moeritze/k8s-r8r:v0.2.0 --repo moeritze/k8s-r8r
```

Or with cosign, against the attestation pushed to ghcr as an OCI referrer:

```sh
cosign verify-attestation \
  --type slsaprovenance1 \
  --certificate-identity-regexp "^https://github\.com/moeritze/k8s-r8r/\.github/workflows/release\.yml@" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  ghcr.io/moeritze/k8s-r8r:v0.2.0
```

### 4. Inspect the SBOM and BuildKit provenance

These are BuildKit attestation manifests carried inside the image index, so
`docker buildx imagetools` reads them:

```sh
# SPDX SBOM, one entry per platform
docker buildx imagetools inspect ghcr.io/moeritze/k8s-r8r:v0.2.0 \
  --format '{{ json .SBOM }}' | jq .

# just the package names for linux/amd64
docker buildx imagetools inspect ghcr.io/moeritze/k8s-r8r:v0.2.0 \
  --format '{{ json (index .SBOM "linux/amd64").SPDX }}' \
  | jq -r '.packages[].name' | sort -u

# max-mode provenance: source repo, git revision, build args, base images
docker buildx imagetools inspect ghcr.io/moeritze/k8s-r8r:v0.2.0 \
  --format '{{ json .Provenance }}' | jq .
```

### 5. Inspect OCI labels and index annotations

```sh
# labels, per arch manifest
docker buildx imagetools inspect ghcr.io/moeritze/k8s-r8r:v0.2.0 \
  --format '{{ json .Image }}' | jq '.. | .Labels? // empty'

# annotations on the multi-arch index itself
docker buildx imagetools inspect --raw ghcr.io/moeritze/k8s-r8r:v0.2.0 \
  | jq '.annotations'
```

### Enforcing verification in-cluster

The keyless identity above is the input a policy engine needs. With
[policy-controller](https://docs.sigstore.dev/policy-controller/overview/) or
Kyverno, require `issuer: https://token.actions.githubusercontent.com` and
`subject` matching the `release.yml@refs/tags/v*` identity for the
`ghcr.io/moeritze/k8s-r8r` image. Do this and an unsigned or third-party image
cannot start, even if someone edits the Deployment.

## One-time setup note (ghcr visibility)

GitHub Container Registry packages may default to **private** on first
publish. After the first release creates the `k8s-r8r` image package and
the `charts/k8s-r8r` chart package, a maintainer must flip each package's
visibility to **public** in the package settings on GitHub (one-time per
package). Until then, anonymous pulls — and anonymous *verification* — fail.

## Authentication

The workflow authenticates to ghcr with the workflow's `GITHUB_TOKEN`
(`packages: write`); no extra secrets are configured, and no signing key
exists to leak. Signing and attestation additionally require `id-token:
write` (Sigstore OIDC) and `attestations: write`, granted **at job scope
only** — the workflow keeps a top-level `permissions: {}` default-deny
posture. All actions are pinned to commit SHAs, matching the CI workflow's
hardening style; Dependabot (`.github/dependabot.yml`) keeps those pins
current.
