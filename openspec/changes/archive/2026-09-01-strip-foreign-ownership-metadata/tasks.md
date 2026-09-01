# Tasks — strip foreign ownership metadata

## 1. Engine

- [x] 1.1 Rename `isPipelineKey` to `isStrippedKey` and extend it with the
      foreign-ownership denylist (prefixes `argocd.argoproj.io/`,
      `replicator.v1.mittwald.de/`, `reflector.v1.k8s.emberstack.com/`,
      `meta.helm.sh/`, `kustomize.toolkit.fluxcd.io/`; exact key
      `app.kubernetes.io/instance`), keeping `cleanPipelineKeys` and
      `cleanRawKeys` on the same predicate so replica and hash agree.
- [x] 1.2 Add `SetExtraStrippedKeys` for operator-configured additions
      (trailing `/` = prefix, otherwise exact key), additive only.

## 2. Flag plumbing

- [x] 2.1 Add the `--strip-metadata-keys` flag in `cmd/main.go` (comma-separated,
      repeatable) and call `engine.SetExtraStrippedKeys` before manager start.
- [x] 2.2 Add the `stripMetadataKeys` chart value and render it as the flag in
      the deployment template.

## 3. Tests

- [x] 3.1 Table test over each stripped prefix/exact key on labels and
      annotations of a source Secret.
- [x] 3.2 Regression test: `SourceHash(source) == SourceHash(rendered)` with
      foreign keys present (guards the drift hot-loop).
- [x] 3.3 Test that unrelated metadata (e.g. the sealed-secrets key label)
      still propagates — the denylist must not become an allowlist.
- [x] 3.4 Test `SetExtraStrippedKeys` for both exact and prefix entries,
      including hash exclusion.

## 4. Docs

- [x] 4.1 `docs/gitops.md`: correct the false "a normal Application will not
      prune them" claim and describe the stripping behavior.
- [x] 4.2 `docs/annotations.md`: document stripped foreign metadata and the
      flag under operator-written metadata.
- [x] 4.3 `obsidian/replication-flow.md`: note metadata hygiene in the fanout
      step.
- [x] 4.4 `CHANGELOG.md`: entry under Unreleased (Fixed + Added).

## 5. Verification

- [x] 5.1 `make lint && make test` — green on PR #40 (CI `Lint` and
      `Unit tests + envtest` jobs).
- [x] 5.2 `make test-e2e` — green on PR #40 (CI `E2E (kind fleet, hub + 2
      spokes)` job).
