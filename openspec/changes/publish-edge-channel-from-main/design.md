## Context

See proposal.md — Why. Current state that shapes the design:

- `release.yml` (tag-triggered) already does everything the edge lane needs — multi-arch buildx push, `docker/metadata-action` labels landing on the index, cosign keyless signing by digest, `provenance: mode=max` + `sbom: true`, `actions/attest-build-provenance` pushed as a referrer, chart package/push with digest parsed from `helm push` output and cosign-signed. It is one job per artifact, inline steps, all actions SHA-pinned, `permissions: {}` at the top with job-scoped grants.
- `docs/releasing.md` publishes consumer verification commands that pin `--certificate-identity` to `https://github.com/moeritze/k8s-r8r/.github/workflows/release.yml@refs/tags/v…`. That identity is part of the consumer contract.
- Branch protection on `main` requires DCO, gitleaks, lint, unit+envtest, build, e2e and govulncheck to pass before merge. Anything on `main` is already CI-green.
- The `v0.1.0-alpha.2` release build spent 25m35s in the build step: the builder stage runs `go build` under QEMU for arm64. The Dockerfile already builds with `CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH`, so cross-compilation is a one-line change.
- The chart's default image tag is `.Chart.AppVersion` (`values.yaml: image.tag: ""`), so a chart whose `appVersion` equals the image tag installs the matching image with no `--set`.

## Goals / Non-Goals

**Goals:**
- Every commit on `main` is installable within minutes, by chart version, with the same supply-chain guarantees as a release.
- The release lane's externally visible contract — tags, `:latest` gating, GitHub releases, and the documented cosign identity — is byte-for-byte unchanged.
- One implementation of the publish steps.
- Edge artifacts cannot be mistaken for releases by Helm, by a registry browser, or by a cosign verifier.
- Storage is bounded.

**Non-Goals:**
- Automatic version bumping or tagging.
- Running the test suite again on push to `main` (branch protection already guarantees it).
- Serving edge to adopters: it is a trial channel, and the docs say so.
- Changing branch protection.

## Decisions

### D1: Composite actions, not a reusable workflow, for the shared publish steps

**Alternatives:** (a) reusable workflow via `workflow_call`; (b) composite actions under `.github/actions/`; (c) leave the steps duplicated.

**Decision: (b).** A reusable workflow runs as its own job, so the Sigstore certificate's SAN becomes `…/publish-image.yml@<ref>` for both lanes — the documented identity `release.yml@refs/tags/v…` would stop matching for every future tag, and the lane would no longer be visible in the signing identity (only in the ref). A composite action runs inside the caller's job, so the identity stays `release.yml@refs/tags/v…` for releases and becomes `edge.yml@refs/heads/main` for edge: the lane is in the certificate, the existing verify commands keep working, and an adopter who pins the release identity structurally cannot be handed an edge artifact. (c) was rejected because two copies of the cosign/SBOM/provenance logic would drift.

Composite actions cannot declare permissions; the caller job grants `contents: read`, `packages: write`, `id-token: write`, `attestations: write`. Top-level `permissions: {}` stays.

Inputs, `publish-image`: `tags` (comma-separated full refs), `annotations-level` (default `index,manifest`), `version` and `revision` for OCI labels. Outputs: `digest`. It does not decide tags — tag selection stays in each caller so the `:latest` rule remains expressed in exactly one place (`release.yml`), as today.

Inputs, `publish-chart`: `chart-version`, `app-version`. Outputs: `digest`. It keeps the `helm push` digest-parse-or-fail step.

### D2: Edge tags and chart version

- Image: `:main` (moving) and `:sha-<7>` (immutable). `sha-` prefix so a short SHA can never collide with or be misread as a version. No `:latest`, no `:edge` alias — one moving tag is enough and `main` says exactly what it is.
- Chart: `0.0.0-main.g<7>`. The `0.0.0-` prerelease sorts below every real version under semver, so `helm search`, `helm upgrade` without `--version`, and Artifact Hub never treat an edge chart as newest. The `g` prefix (git convention) guarantees the prerelease identifier is alphanumeric: a short SHA that happens to be all digits with a leading zero (e.g. `0123456`) is an **invalid** semver numeric identifier and Helm would reject the package. `appVersion: sha-<7>` so the chart's default image tag resolves to this run's image.

**Alternative considered:** `0.1.0-main.<sha>` (next planned version). Rejected: it implies a relationship to a version that has not been cut, and it would sort *above* `0.1.0-alpha.N` prereleases, so `helm upgrade` could pick an edge build over a real prerelease.

### D3: Trigger, concurrency, and no re-test

`on: push: branches: [main]` plus `workflow_dispatch`. Tags are excluded so a tag push runs only `release.yml`. `concurrency: { group: edge, cancel-in-progress: true }`: rapid merges cancel the older build, so `:main` always points at the newest commit and the runner does not queue stale builds. Cancellation before the push step publishes nothing; buildx pushes the index atomically, so a cancelled run cannot leave a half-published `sha-` tag.

No lint/test jobs in `edge.yml`. Every commit on `main` already passed the full required-check set as a PR; repeating it would double CI cost for zero information.

### D4: Cross-compile the builder stage

`FROM --platform=$BUILDPLATFORM golang:1.27 AS builder`. The build runs natively on the amd64 runner for both target architectures; only the final `distroless/static:nonroot` stage is per-platform, and that stage is a pull, not a compile. `setup-qemu-action` is removed from both lanes. Expected effect: the >18-minute build step drops to a few minutes. Buildx cache `type=gha` (`mode=max` on export) so unchanged Go modules are not re-downloaded per run.

### D5: Retention via a maintained multi-arch-aware cleanup action

A multi-arch image is an index plus untagged per-platform manifests plus untagged attestation/SBOM referrers. Deleting "untagged versions" through the raw packages API deletes the children of **current** tags and breaks every live image. **Decision:** use `dataaxiom/ghcr-cleanup-action` (SHA-pinned), which understands index → child relationships: delete tags matching `sha-*` older than 30 days on the image package and `0.0.0-main.*` on the chart package, and let it remove only the children orphaned by those deletions. `main`, `latest` and semver tags are excluded by pattern. Weekly schedule plus `workflow_dispatch` with a `dry-run` input defaulting to `true`; the first scheduled run is a dry-run until the log has been checked once.

### D6: Verification identity for edge

`--certificate-identity https://github.com/moeritze/k8s-r8r/.github/workflows/edge.yml@refs/heads/main`. Documented next to the release identity with the contrast made explicit: a verifier pinned to the release identity rejects edge artifacts, and vice versa.

## Risks / Trade-offs

- [Both lanes share the composite actions, so a bug in an action breaks releases too] → the edge lane runs on every merge, so the shared steps are exercised continuously and a regression is visible long before the next tag. The next real tag is a deferred verification item in tasks.
- [`cancel-in-progress` on `main` could starve `:main` if merges are continuous] → each run is a few minutes after D4; realistically the last run always completes.
- [`:main` moves under a running trial] → trials should install by chart version (`0.0.0-main.g<sha>`), which pins the image via `appVersion`; documented as the recommended form, `:main` is for "what is newest".
- [Prune deletes something still in use] → dry-run first run, tag patterns anchored, retention 30 days, and a trial install by immutable `sha-` tag older than 30 days would fail to pull with a clear error rather than run stale code silently.
- [Edge artifacts appear next to releases in the ghcr package UI] → naming makes them unmistakable; the docs state edge is for trials.
- [Changing the Dockerfile builder platform changes the provenance's recorded build environment] → provenance is still generated by the same action with `mode=max`; nothing in the verify docs depends on the builder platform.

## Migration Plan

1. Merge the change. The push to `main` that merges it is itself the first edge run — that run is the acceptance test (see tasks 6.x).
2. No consumer action needed. Existing release artifacts and their signatures are unchanged; `docs/releasing.md` verify commands keep working because the release identity is preserved (D1).
3. Rollback: revert the PR. Edge artifacts already published stay in ghcr until the prune workflow (or manual deletion) removes them; nothing else references them.

## Open Questions

None that affect the approach. Retention length (30 days) and the choice of one moving tag are tunables recorded in `prune-edge.yml` and `edge.yml` respectively; adjust without a spec change.
