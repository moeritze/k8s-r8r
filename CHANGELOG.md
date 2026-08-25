# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While the project is in `0.x` alpha, breaking changes may land in any
release; they are called out under **Changed** or **Removed**.

## [Unreleased]

_Nothing yet._

## [0.1.0-alpha.1] - 2026-08-24

First tagged pre-release. Publishes a multi-arch operator image and Helm
chart to ghcr.io.

### Added

- **`r8r.io/v1alpha1` API** — `Replication` (operator-owned canonical
  object carrying status and replica inventory) and cluster-scoped
  `ReplicationPolicy` CRDs.
- **Annotation shim controller** — developers annotate a source Secret or
  ConfigMap (`r8r.io/replicate`, `r8r.io/target-clusters`, …); the
  controller materializes and owns the corresponding `Replication`. Source
  objects are watched metadata-only.
- **Policy engine** — default-deny, allowlist-only, union semantics across
  matching `ReplicationPolicy` objects; option resolution for conflict
  strategy and revocation behavior; evaluated authoritatively on every
  reconcile.
- **Pluggable discovery** with a ClusterAPI provider — watches
  `cluster.x-k8s.io` `Cluster` objects via a dynamic informer (no CAPI
  module dependency), derives readiness from `ControlPlaneReady` /
  `ControlPlaneAvailable`, and resolves the conventional
  `<cluster>-kubeconfig` Secret.
- **Minimal-privilege spoke bootstrap** — the CAPI admin kubeconfig is
  used exactly once per spoke to create the `k8s-r8r-system` namespace, a
  `k8s-r8r` ServiceAccount, a kind-scoped `k8s-r8r-replicator` ClusterRole
  and a namespaced token-minting Role. Steady state runs on short-lived,
  self-rotated SA tokens; RBAC is re-narrowed when the kind allowlist
  shrinks.
- **Cluster runtime manager** — one client/cache runtime per registered
  ready cluster, with metadata-only informers for the engine.
- **Replication engine** — fanout, drift detection and repair via
  `r8r.io/source-hash`, conflict handling (`Fail` / `Overwrite` / `Adopt`),
  per-`Replication` inventory garbage collection, and live policy
  revocation (`Delete` / `Retain`).
- **Advisory validating webhook** — CEL-scoped `matchConditions`,
  `failurePolicy: Ignore`; apply-time feedback that is never an
  availability or security dependency.
- **Observability baseline** — leader election, Prometheus metrics,
  secret-safe events and conditions (hashes only, never payloads),
  size-capped status, and an AST audit test that enforces the
  secret-safety rule.
- **Helm chart** (`charts/k8s-r8r`) and user documentation: quickstart,
  annotation reference, policy authoring guide, GitOps integration,
  security/threat model, uninstall, and releasing.
- **End-to-end suite** (`test/e2e`) — provisions a kind fleet (hub + 2
  spokes) via `hack/kind-fleet.sh`, builds and side-loads the operator
  image, installs the Helm chart, and simulates ClusterAPI inventory.
  Covers replication lifecycle, conflict-fail, namespace ensure, policy
  revocation, source-delete cleanup, cluster lifecycle, and scale fanout.
  Runs as a required check on every pull request.
- **Release publishing** — a tag triggers a multi-arch image build, an OCI
  Helm chart push to ghcr.io, and a GitHub release.
- **Community and supply-chain files** — Apache-2.0 license, DCO
  sign-off enforcement in CI, contribution guidelines, security policy,
  code of conduct, issue/PR templates, and gitleaks secret scanning on
  every push and pull request.

### Fixed

- Grant `create`/`patch` on `events.k8s.io` events so operator events
  actually reach the API server.
- Grant `patch` on namespaces in the spoke bootstrap RBAC — the
  server-side-apply namespace-ensure path needs both `create` and `patch`.

[Unreleased]: https://github.com/moeritze/k8s-r8r/compare/v0.1.0-alpha.1...HEAD
[0.1.0-alpha.1]: https://github.com/moeritze/k8s-r8r/releases/tag/v0.1.0-alpha.1
