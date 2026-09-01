# Design — strip foreign ownership metadata

## Context

`internal/engine/render.go` has one predicate, `isPipelineKey`, feeding two
consumers:

- `cleanPipelineKeys` — the map used to build the replica in `Render`.
- `cleanRawKeys` — the map used by `canonicalPayload`, i.e. the input to
  `SourceHash`.

Both must agree. `SourceHash` is deliberately identity-blind so a renamed,
relocated replica hashes equal to its source; the engine's drift path
(`reconciler.go`) compares `SourceHash(existing)` against the desired hash.

## Decisions

### D1 — Extend the predicate, not `Render`

Stripping in `Render` alone would leave `canonicalPayload` hashing the source's
original metadata. The replica, rendered without those keys, would then hash
differently: `SourceHash(existing) != hash` would never converge, so the engine
would apply on every reconcile, each apply would produce a metadata event, the
drift handler would enqueue, and the result is a hot loop against every spoke.
Routing everything through one predicate (renamed `isPipelineKey` →
`isStrippedKey`, since it no longer means "keys the pipeline owns") makes that
class of bug unrepresentable. A regression test asserts
`SourceHash(source) == SourceHash(rendered)` with foreign keys present.

### D2 — Denylist, not allowlist

The issue suggested a `passthroughLabels` allowlist as the safer default. It is
not: `render.go` deep-copies deliberately so functionally significant labels
survive, and a replicated sealed-secrets key without
`sealedsecrets.bitnami.com/sealed-secrets-key=active` is inert on arrival. An
allowlist would break working replications on upgrade with no error surfaced.
The hazard is specifically *ownership* metadata, so the denylist is scoped to
that.

### D3 — Strip rather than stamp for Argo CD

`argocd.argoproj.io/compare-options: IgnoreExtraneous` suppresses the aggregate
sync status but leaves `RequiresPruning` true, so it does not prevent the
deletion. Stripping removes the claim of membership entirely and needs no
cooperation from the target cluster's Argo CD configuration.

### D4 — Package-level configuration for the extra keys

`SourceHash` is a package-level function called from `reconciler.go` and
`conflict.go`, not a `Renderer` method. Putting operator-configured keys on the
`Renderer` struct would leave `SourceHash` unaware of them — exactly the
desynchronisation D1 exists to prevent. The extra keys are therefore
process-wide configuration: `engine.SetExtraStrippedKeys` is called once from
`cmd/main.go` during flag processing, before any reconciler runs.

Syntax: an entry ending in `/` is a prefix match, anything else is an exact key
match — the same shape as the built-in list, and unambiguous without a second
flag.

The flag is additive only. Allowing removal of a built-in entry would let an
operator reintroduce the cross-controller fanout, which is the hazard being
fixed. An operator who genuinely needs `app.kubernetes.io/instance` on replicas
has to re-add it by other means (it is on the built-in list because Argo CD
label tracking prunes on it).

## Risks

- **Visible change on upgrade.** Replicas that carry these keys today lose them
  on the next reconcile. The hash changes with them, so the apply happens once
  and then converges. Called out in the CHANGELOG.
- **The denylist is a moving target.** New replication controllers will appear.
  That is what `--strip-metadata-keys` is for; adding a widely used controller
  to the built-in list later is a normal follow-up change.
