# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While the project is in `0.x` alpha, breaking changes may land in any
release; they are called out under **Changed** or **Removed**.

## [Unreleased]

### Changed

- **BREAKING: the conflict policy now needs consent from the request as well
  as from policy** ([#34](https://github.com/moeritze/k8s-r8r/issues/34)).
  `Overwrite` and `Adopt` used to be decided by `allowedConflictPolicies`
  alone, even though the spec and `docs/security.md` both described a two-key
  turn. The engine now acts on the **weaker** of the new
  `r8r.io/conflict-policy` request annotation and the policy grant, ranked
  `Fail` < `Adopt` < `Overwrite`.

  **What breaks.** A source that relies on a policy-granted `Overwrite` or
  `Adopt` and carries no `r8r.io/conflict-policy` annotation now reports
  `Conflict` for that target instead of taking the existing object over. An
  absent annotation means `Fail`: a request that says nothing consents to
  nothing.

  **Migration.** Add the annotation to the sources that should have it:

  ```yaml
  metadata:
    annotations:
      r8r.io/conflict-policy: "Overwrite"   # or "Adopt"
  ```

  Nothing is deleted or overwritten by the change — the new failure mode is
  "did not take over", never the reverse — and the `Conflict` condition and
  event name the missing annotation, so affected targets are self-diagnosing
  via `kubectl describe replication`.

### Added

- **`--discovery-setting key=value`** manager flag (chart value
  `discoverySettings`, a map) finally reaches
  `discovery.Options.Settings` ([#37](https://github.com/moeritze/k8s-r8r/issues/37)).
  The settings map was plumbed through the provider interface and read by the
  `cluster-api` provider, but nothing in a deployed operator ever populated
  it, so every provider setting was unreachable outside tests. The flag is
  comma-separated and repeatable; the chart renders one flag per map entry. A
  malformed entry (no `=`, or an empty key) fails startup instead of being
  dropped, because a mistyped setting is indistinguishable from an unset one
  once a provider reads it. Keys read by the `cluster-api` provider are
  documented in `docs/quickstart.md`; today that is `namespace` (restrict the
  ClusterAPI `Cluster` watch to a single namespace).

- **`TargetsResolved` condition on `Replication`** reports whether a request
  resolved to any target: `True` once at least one target survives selector
  matching and policy evaluation, `False`/`PolicyDenied` when policy refused
  every candidate, `False`/`NoTargets` when the `r8r.io/target-clusters`
  selector matched no ready cluster. The last case previously produced no
  condition and no event at all. Additive — no CRD schema change.
- **`r8r.io/conflict-policy`** request annotation (`Fail` / `Adopt` /
  `Overwrite`, default `Fail`) — the request's half of the conflict two-key
  turn. It joins the closed set of `r8r.io/*` keys, so a malformed value is
  rejected by the controller and denied by the advisory webhook with a message
  naming the annotation. A value no matching policy could grant is admitted
  with a warning rather than denied: such a request replicates normally and
  only its conflict handling falls back to the weaker key.
- **`--strip-metadata-keys`** manager flag (chart value `stripMetadataKeys`)
  extends the stripped-metadata denylist with fleet-specific keys.
  Comma-separated and repeatable; a trailing `/` makes an entry a prefix
  match. Additive only — built-in entries cannot be removed.

- **`k8s_r8r_drift_corrections_total{cluster}`** counts replicas whose
  diverged content the engine rewrote. Distinct from the pre-existing
  `k8s_r8r_drift_events_total`, which counts spoke informer traffic (the
  operator's own apply echoes included) and therefore rises on ordinary
  source updates.

### Fixed

- **Policy-denied and revoked `Replication` objects no longer report
  `Ready: True`** ([#27](https://github.com/moeritze/k8s-r8r/issues/27)).
  A `Replication` with zero resolved targets reported
  `Ready: True, reason: AllTargetsReady, message: 0/0 targets ready`,
  indistinguishable in status from a healthy fanout, so there was nothing to
  alert on. Two controllers wrote the `Ready` condition with no arbitration:
  the request controller set `Ready=False`/`PolicyDenied`, then the engine
  rewrote `Ready` from `failed == 0` alone — and zero targets means zero
  failures. Because the request controller also watches `Replication`
  objects, the two writes re-triggered each other in an unbounded status
  ping-pong, bounded only by the rate limiter. The request controller now
  writes `TargetsResolved` and never `Ready`; the engine reports
  `Ready: False`, reason `NoTargets`, for a live `Replication` with no
  desired targets, and emits a `NoTargets` warning event instead of the
  spurious `Replicated 0/0 targets ready` the ping-pong manufactured.
  **Upgrade note:** affected objects flip from green to red on their first
  reconcile after upgrade. Nothing about what is replicated changes.
- **Drift correction is now observable**
  ([#30](https://github.com/moeritze/k8s-r8r/issues/30)). When a replica's
  content is rewritten on a spoke, the engine emits a `DriftCorrected`
  Warning event on the `Replication` — naming the replica's
  cluster/namespace/name and the observed and expected `sha256:` hashes — and
  increments `k8s_r8r_drift_corrections_total`. Previously the corrective
  write was indistinguishable from a no-op reconcile, so repeated tampering
  with a replicated Secret, or a second replicator fighting for the same
  object, produced no output at all. Events coalesce for five minutes per
  identical (object, reason, message), so read the *rate* of recurring drift
  off the metric, which is not rate-limited. A stale `r8r.io/source-hash`
  annotation over unchanged content is repaired silently — it is a
  bookkeeping repair, not drift, and it is what a change to the hashing rules
  produces fleet-wide on upgrade.
- **Replicas no longer inherit foreign ownership or replication metadata**
  ([#26](https://github.com/moeritze/k8s-r8r/issues/26)). The engine now
  strips `replicator.v1.mittwald.de/*`,
  `reflector.v1.k8s.emberstack.com/*`, `argocd.argoproj.io/*`,
  `app.kubernetes.io/instance`, `meta.helm.sh/*` and
  `kustomize.toolkit.fluxcd.io/*` from replicas, and excludes them from the
  source hash. Previously a replica of a source annotated for another
  replication controller was itself a valid source for that controller, so
  k8s-r8r seeded a second fanout whose destinations no `ReplicationPolicy`
  evaluated; and a replica carrying ArgoCD's tracking label claimed
  membership in an Application that never declared it, making it a prune
  candidate. Existing replicas lose these keys on their next reconcile.
  All other source metadata still propagates — this is a denylist scoped to
  ownership, not an allowlist.
- `docs/gitops.md` claimed "a normal Application will not prune them", which
  was false under ArgoCD's default label-based resource tracking.

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
