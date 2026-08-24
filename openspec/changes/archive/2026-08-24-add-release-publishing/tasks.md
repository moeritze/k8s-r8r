## 1. Release workflow

- [x] 1.1 Add `.github/workflows/release.yml` triggered on `v*` tag push, top-level `permissions: {}`, per-job grants (`packages: write` for publish jobs, `contents: write` for the release job)
- [x] 1.2 Job `publish-image`: buildx multi-arch (linux/amd64, linux/arm64) from the existing Dockerfile, login to ghcr with `GITHUB_TOKEN`, push `ghcr.io/moeritze/k8s-r8r:<tag>` and `:latest` only for non-prerelease tags; SHA-pin all actions
- [x] 1.3 Job `publish-chart`: `helm package charts/k8s-r8r --version <tag without v> --app-version <tag>`, `helm registry login ghcr.io`, `helm push` to `oci://ghcr.io/moeritze/charts`
- [x] 1.4 Job `github-release`: after both publish jobs, `gh release create <tag> --generate-notes`, `--prerelease` when the tag contains `-`
- [x] 1.5 Verify workflow YAML parses

## 2. Chart alignment

- [x] 2.1 Confirm values.yaml default image repository is `ghcr.io/moeritze/k8s-r8r` and deployment falls back to `.Chart.AppVersion` for the tag
- [x] 2.2 `helm lint charts/k8s-r8r` and `helm template` with default values stay green

## 3. Docs

- [x] 3.1 `docs/quickstart.md`: add "Install from published artifacts" section (OCI chart install), keep kind/dev flow as the dev path
- [x] 3.2 Add `docs/releasing.md` (tag → workflow → artifacts; prerelease convention; one-time ghcr visibility note)
- [x] 3.3 Update README install snippet to the published chart
- [x] 3.4 Update `obsidian/development.md` (release process) and `obsidian/operations.md` (install pointer)

## 4. Verification

- [x] 4.1 `openspec validate add-release-publishing --strict` passes
- [x] 4.2 `go build ./...` still green (no Go changes expected)
