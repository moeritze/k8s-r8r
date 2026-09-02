# Request-side consent for the conflict policy

## Why

`openspec/specs/replication-engine/spec.md:77` already specifies a two-key
turn:

> `Overwrite` — take over the object, only when **both the request asks for it
> and policy permits it**

`docs/security.md:193-194` repeats it as a security control. The
implementation has never had a request side. `EffectiveConflictPolicy`
(`internal/engine/conflict.go`) takes only `allowedConflictPolicies`, and its
own comment said so: "ResolvedTarget carries no request-side conflict field
yet, so there is nothing to intersect with".

**This change makes an existing requirement true. It does not add a new
promise.** The "Conflict handling" requirement is left exactly as written; the
delta adds the scenarios that prove it, plus the annotation the request side
needs. That is the whole point of picking option 1 from issue #34 over option
2 (weakening the spec): the sentence in the spec is the behaviour the project
wants, so the code should meet it.

Today the consequence is concrete: an admin who grants `Overwrite` in a
`ReplicationPolicy` grants it for **every** request that policy permits. There
is no per-object opt-in, so anyone who can annotate a source in an allowed
namespace gets the strongest conflict behaviour the policy permits, and a
reader of `docs/security.md` believes a control exists that does not.

Related: #29 (same class of drift — a written promise the implementation never
made good on), #26 (a separate problem on the `Adopt` path).

## What Changes

- **New request annotation `r8r.io/conflict-policy`**, valued `Fail`, `Adopt`
  or `Overwrite` — the strongest conflict handling this request consents to.
  It joins the closed set of `r8r.io/*` request keys, so typos keep failing
  loudly (the parser rejects unknown keys; the webhook warns).
- **Intersection at conflict time**: the engine acts on the **weaker** of
  (what the request asks, what policy permits), ranked
  `Fail` < `Adopt` < `Overwrite`. Both keys must turn before an object
  k8s-r8r did not create is touched.
- **Absent annotation means `Fail`** — see the migration note below.
- **Denial visibility**: when a conflict resolves to `Fail` because one of the
  two keys did not turn, the per-target `Conflict` condition and the existing
  `Conflict` warning event say *which* key is missing — a request that never
  opted in reads differently from a policy that never granted the escalation.
- **Admission feedback**: a malformed value is denied with a message naming
  the annotation; a value no candidate policy grants produces a warning, never
  a denial (such a request replicates fine — only its conflict handling falls
  back).

### Migration consequence — this is a breaking change

Absent-means-`Fail` changes effective behaviour for existing installations:
**a source that relies on a policy-granted `Overwrite` or `Adopt` and carries
no `r8r.io/conflict-policy` annotation will now report `Conflict` instead of
taking the object over.** No data is destroyed by the change — the failure
mode is "did not take over", never "took over something it should not have" —
but a fanout that used to converge over a pre-existing object will stop
converging until the source is annotated.

Migration is one annotation per source that wants it:

```yaml
r8r.io/conflict-policy: "Overwrite"   # or "Adopt"
```

The conflict message names the missing annotation, so the fix is discoverable
from `kubectl describe replication` without reading release notes. Called out
under **Changed** in `CHANGELOG.md`, which is where this repo records the
breaking changes it permits itself during `0.x`.

The alternative — defaulting the absent annotation to `Adopt`, which would
change behaviour only for `Overwrite` and leave `Adopt` grants working — was
rejected in `design.md`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `replication-request`: the annotation contract gains
  `r8r.io/conflict-policy` (closed value set, absent means `Fail`).
- `replication-engine`: the "Conflict handling" requirement keeps its existing
  wording and gains scenarios that pin the intersection, the default, and the
  operator-facing explanation of which key did not turn.

## Impact

- `internal/annotations/annotations.go` — new key in the closed request set,
  `Request.ConflictPolicy`, value validation, `RequestedConflictPolicy` for the
  engine, `RequestKeys()` so "valid keys" hints derive from the set itself.
  The package gains one in-repo import (`api/v1alpha1`) for the
  `ConflictPolicy` enum rather than duplicating its three spellings.
- `internal/engine/conflict.go` — `EffectiveConflictPolicy` becomes the
  intersection of request and grant; `DecideConflict` takes the source object
  (it already needed its UID) so it can read the request key; Fail messages
  explain which key is missing.
- `internal/engine/reconciler.go` — one line: the `DecideConflict` call passes
  `src` instead of `src.GetUID()`. No other engine change.
- `internal/webhook/*` — the new key is known (so its value is validated, not
  warned about), plus an advisory warning when no candidate policy grants the
  requested escalation.
- Tests: intersection table, an "effective is never stronger than either key"
  property test, end-to-end reconciler coverage that a grant alone no longer
  overwrites, parser value coverage, webhook admission coverage.
- `docs/annotations.md`, `docs/security.md`, `CHANGELOG.md`.
- `obsidian/` needs a matching vault update (annotation contract + conflict
  handling notes); another change owns that directory right now, so it is
  listed in the PR body instead of edited here.
