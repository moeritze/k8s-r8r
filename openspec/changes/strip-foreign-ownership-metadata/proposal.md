# Strip foreign ownership metadata from replicas

## Why

`Renderer.Render` deep-copies the source object's labels and annotations and
strips only the keys the pipeline itself owns (`r8r.io/*`, the managed-by
label, kubectl's last-applied annotation). Every other controller's ownership
and replication metadata therefore rides along onto every replica, with two
consequences observed on a live hub/spoke pair (issue #26):

1. A replica carrying `replicator.v1.mittwald.de/replicate-to-clusters: ".*"`
   is a valid *source* for mittwald/kubernetes-replicator. If any target
   cluster runs that controller, k8s-r8r seeds a second, independent fanout
   from its own output, with the foreign controller's regex — not a
   `ReplicationPolicy` — deciding where the secret material goes next. That is
   a request-side override of default-deny, which the security model states
   does not exist.
2. A replica carrying Argo CD's tracking metadata (`app.kubernetes.io/instance`
   under the default label tracking, `argocd.argoproj.io/tracking-id` under
   annotation tracking) claims membership in an Application that never declared
   it, in a namespace and cluster outside that Application's spec. Argo CD
   reads it as extraneous, and with automated pruning enabled the replica
   becomes a deletion candidate — silent data loss.

This is an undocumented gap, not a violated requirement: the existing
"Kind-agnostic pipeline" requirement (`replication-engine/spec.md:21`) names
only server-managed and identity fields; foreign ownership metadata was never
mentioned either way. This change closes the gap by making the behavior
normative.

## What Changes

- The engine strips a denylist of foreign ownership / replication-intent keys
  from replicas — currently the prefixes `argocd.argoproj.io/`,
  `replicator.v1.mittwald.de/`, `reflector.v1.k8s.emberstack.com/`,
  `meta.helm.sh/`, `kustomize.toolkit.fluxcd.io/` and the exact key
  `app.kubernetes.io/instance`.
- The same keys are excluded from the canonical source hash, so a source and
  its replica keep hashing identically and drift detection does not see a
  permanent mismatch.
- The denylist is additively extensible by the operator via a new
  `--strip-metadata-keys` manager flag (chart value `stripMetadataKeys`).
  It only adds; the built-in entries cannot be removed, since removing them
  would reintroduce the fanout hazard.
- Deliberately **not** an allowlist: functionally significant source labels
  must keep propagating (a replicated sealed-secrets key is inert on arrival
  without `sealedsecrets.bitnami.com/sealed-secrets-key=active`). This stays a
  denylist scoped to *ownership* metadata.
- Deliberately **not** stamping `argocd.argoproj.io/compare-options:
  IgnoreExtraneous` on replicas: it suppresses aggregate sync status but leaves
  `RequiresPruning` true, so it does not prevent the deletion this change is
  about.
- Docs: correct the false pruning claim in `docs/gitops.md`, document the
  stripped keys and the flag in `docs/annotations.md`, update
  `obsidian/replication-flow.md`, add a CHANGELOG entry.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `replication-engine`: adds a requirement covering foreign ownership and
  replication-intent metadata on replicas (stripping plus hash exclusion).

## Impact

- `internal/engine/render.go` — `isPipelineKey` becomes `isStrippedKey` and
  gains the foreign-key denylist plus the operator-configured additions; both
  `cleanPipelineKeys` and `cleanRawKeys` (the hash path) route through it, so
  replica rendering and hashing stay consistent.
- `internal/engine/render_test.go` — table coverage per stripped prefix/key, a
  hash-stability regression test (`SourceHash(source) == SourceHash(rendered)`
  with foreign keys present), and a test that unrelated metadata still
  propagates.
- `cmd/main.go` — `--strip-metadata-keys` flag, parsed and handed to the engine
  package before the manager starts.
- `charts/k8s-r8r/values.yaml`, `charts/k8s-r8r/templates/deployment.yaml` —
  `stripMetadataKeys` value rendered as the flag.
- `docs/gitops.md`, `docs/annotations.md`, `obsidian/replication-flow.md`,
  `CHANGELOG.md`.
- Behavior change for existing installations: replicas that today carry these
  keys lose them on the next reconcile. That is the fix, but it is visible.
