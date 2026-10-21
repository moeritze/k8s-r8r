## 1. Shared publish steps (composite actions)

- [x] 1.1 Create `.github/actions/publish-image/action.yml`: inputs `tags`, `version`, `revision`, `annotations-level` (default `index,manifest`); steps: buildx setup, ghcr login, `docker/metadata-action` labels+annotations, `docker/build-push-action` multi-arch with `provenance: mode=max`, `sbom: true`, `cache-from/to: type=gha`; cosign install; `cosign sign --yes` by digest; `actions/attest-build-provenance` with `push-to-registry: true`. Output `digest`. All actions SHA-pinned with version comments, copied from the current `release.yml`.
- [x] 1.2 Create `.github/actions/publish-chart/action.yml`: inputs `chart-version`, `app-version`; steps: ghcr login (docker + helm registry), `helm package --version --app-version`, `helm push` with the existing digest-parse-or-fail logic, cosign install, `cosign sign --yes` by digest. Output `digest`.
- [x] 1.3 Refactor `.github/workflows/release.yml` to call both actions. Keep the "Compute image tags" step and its `:latest` rule in the caller; keep job-scoped permissions and the GitHub-release job unchanged. Confirm with `actionlint` that the resulting workflow is valid and that the top-level `permissions: {}` posture is intact.

## 2. Edge lane

- [x] 2.1 Create `.github/workflows/edge.yml`: `on: push: branches: [main]` + `workflow_dispatch`; `concurrency: { group: edge, cancel-in-progress: true }`; job `publish-edge` with `contents: read`, `packages: write`, `id-token: write`, `attestations: write`; compute `short=$(git rev-parse --short=7 HEAD)`; call `publish-image` with tags `ghcr.io/moeritze/k8s-r8r:main,ghcr.io/moeritze/k8s-r8r:sha-<short>`, `version=sha-<short>`, `revision=<full sha>`; call `publish-chart` with `chart-version=0.0.0-main.g<short>`, `app-version=sha-<short>`.
- [x] 2.2 Verify with a semver check (e.g. `helm package` locally or a `semver` shell check in the workflow) that `0.0.0-main.g<short>` is accepted for an all-digit short SHA, so the `g` prefix decision in design D2 is proven rather than assumed.
- [x] 2.3 Run `actionlint` on `edge.yml`.

## 3. Build speed

- [x] 3.1 Change the Dockerfile builder stage to `FROM --platform=$BUILDPLATFORM golang:1.27 AS builder`; keep `ARG TARGETOS TARGETARCH` and the `CGO_ENABLED=0 GOOS=… GOARCH=…` build line. Remove `setup-qemu-action` from the publish-image action (no longer needed for the build).
- [ ] 3.2 Locally: `docker buildx build --platform linux/amd64,linux/arm64 .` (no push) succeeds and the arm64 binary is `ELF 64-bit LSB … ARM aarch64` (check via `docker buildx build --platform linux/arm64 --output type=local,dest=out` + `file out/manager`). State explicitly if docker is unavailable and the check is deferred to CI.

## 4. Retention

- [x] 4.1 Create `.github/workflows/prune-edge.yml`: `on: schedule` (weekly) + `workflow_dispatch` with input `dry-run` (boolean, default `true`); `packages: write`; `dataaxiom/ghcr-cleanup-action` (SHA-pinned) run twice — image package `k8s-r8r` deleting tags matching `sha-*` older than 30 days, chart package `charts/k8s-r8r` deleting tags matching `0.0.0-main.*` older than 30 days; exclude `main`, `latest`, and `v*`/semver tags by pattern; orphaned-children cleanup enabled, blanket untagged deletion disabled.
- [ ] 4.2 `actionlint` on `prune-edge.yml`; trigger one `workflow_dispatch` dry-run after merge and paste the summary of what it *would* delete into the PR or a follow-up comment.

## 5. Documentation (part of done)

- [x] 5.1 `docs/releasing.md`: add an "Edge channel" section — what publishes on every push to `main`, the naming (`:main`, `:sha-<short>`, chart `0.0.0-main.g<short>`), the install command by chart version, `cosign verify` with `--certificate-identity https://github.com/moeritze/k8s-r8r/.github/workflows/edge.yml@refs/heads/main`, the note that a verifier pinned to the release identity rejects edge artifacts by construction, and the explicit warning that edge is for trials, not adoption. Mention the 30-day prune.
- [x] 5.2 `docs/quickstart.md`: add a short "Trial the newest `main`" snippet using `--version 0.0.0-main.g<sha>`, linking to the releasing doc for verification.
- [x] 5.3 `README.md` Getting started: one sentence pointing at the edge channel for trials.
- [x] 5.4 `CHANGELOG.md`: entry under `## [Unreleased]` → Added (edge channel), Changed (cross-compiled multi-arch builds; publish steps shared via composite actions — no consumer-visible change).
- [x] 5.5 `obsidian/development.md` (release/edge lanes, how to install `main` for a trial) and `obsidian/operations.md` (edge naming, verification identity, prune). Keep wikilinks resolving; hub note updated if it lists release mechanics.

## 6. Verification

- [x] 6.1 `make lint && make test` still pass (no Go changes expected; confirm nothing else broke).
- [ ] 6.2 After merge: the push to `main` triggers `edge.yml`; observe the run green end to end (image, chart, signatures, attestation).
- [ ] 6.3 From a machine with no ghcr credentials: `docker pull ghcr.io/moeritze/k8s-r8r:sha-<short>` and `helm pull oci://ghcr.io/moeritze/charts/k8s-r8r --version 0.0.0-main.g<short>` both succeed.
- [ ] 6.4 `cosign verify` succeeds for the edge image and the edge chart against the edge identity, and **fails** against the release identity (proves the lane separation in design D1/D6).
- [ ] 6.5 `helm upgrade --install` of the edge chart on a kind cluster pulls the matching `sha-<short>` image with no `--set` (proves `appVersion` plumbing).
- [ ] 6.6 Deferred: the next real `v*` tag re-verifies the refactored release lane — signature identity still `release.yml@refs/tags/v…`, `:latest` still gated, GitHub release still created, and the build step time recorded against the alpha.2 baseline of 25m35s for the build step alone (2026-09-10, QEMU arm64).
