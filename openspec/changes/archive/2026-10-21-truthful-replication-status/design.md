## Context

See proposal.md — Why. The constraint that shapes everything here: three
controllers write status on a `Replication` (the request controller's source
reconciler, the request controller's authority reconciler, and the engine),
and each does a full read-modify-write of `status.conditions`. The engine
rebuilds `Ready` from the per-target outcomes of *its* reconcile on every
pass, so any condition it derives is authoritative by construction and any
other writer of that condition is transient by construction.

`status.conditions` is already `[]metav1.Condition` with `listType=map` keyed
on `type`, so adding a condition type is not a CRD schema change — only the
generated doc comments move.

## Goals / Non-Goals

**Goals:**
- A `Replication` that replicates nothing never comes to rest reporting
  readiness, whatever the cause (denial, revocation, empty selector).
- The durable record distinguishes *why* nothing resolved, so an operator can
  tell a policy problem from a selector typo without reading events.
- Exactly one writer per condition, enforced by construction rather than by
  convention.
- The seam is covered by a test that runs more than one controller.

**Non-Goals:**
- Reworking `Ready` into a multi-condition scheme (`Progressing`,
  `Degraded`, …). One aggregate readiness condition plus one resolution
  condition is enough for the alerting question this change is about.
- Making the engine's per-target `PolicyDenied` path reachable. Target
  resolution filters denied targets out of `spec.resolvedTargets` before the
  engine ever sees them, so the engine's per-slot denial branch is currently
  dead in production. That is a separate cleanup; conflating it with a status
  fix would widen the blast radius.
- An Argo CD Lua health check. This change unblocks one by making `Ready`
  meaningful; shipping it is separate.

## Decisions

### Split the verdict into two conditions rather than teaching the engine to defer

**Chosen:** the request controller writes a new `TargetsResolved` condition
and stops writing `Ready`; the engine keeps sole ownership of `Ready` and
learns that zero desired targets is not success.

**Alternative — engine guard:** have the engine skip `Ready` when it finds
`Ready` already set to `PolicyDenied`, mirroring the existing
`NotAuthoritative` guard. Rejected: it keeps two writers on one condition and
makes the engine's output depend on what it happens to read, which is exactly
the ordering dependence that produced the bug. It also cannot express "denied"
and "the surviving targets are failing" at the same time, and it does nothing
for the selector-typo case, which never sets a condition at all.

**Alternative — reason-only fix:** keep one condition and just change the
zero-target reason to `NoTargets`. Rejected as insufficient on its own: it
fixes the lie but discards *why* there are no targets, which is the thing an
operator needs, and it leaves the ping-pong in place.

The two conditions answer genuinely different questions — "did anything get
asked of the engine?" and "did the engine do everything asked of it?" — which
is why they can be owned independently without arbitration logic.

### `TargetsResolved` is tri-valued, with `NoTargets` covering the silent case

`True`/`TargetsResolved`, `False`/`PolicyDenied`, `False`/`NoTargets`. The
third value is the one the old code had no path for: when the cluster
selector matches nothing, target resolution returns before policy is
consulted, so the denial list is empty and the denial reporting path did
nothing at all. Reporting `True` only when targets actually resolved makes
the positive case a usable `kubectl wait --for=condition=TargetsResolved`
target as well.

### `Ready=False`/`NoTargets` is scoped to live objects

The zero-desired-targets branch is skipped while the `Replication` has a
deletion timestamp: an object being torn down legitimately has no desired
targets, and flagging it would add churn to the finalizer path for no reader's
benefit. The other zero-target status writer (the single-condition path used
for "source missing" and "kind not allowlisted") passes one non-ready state,
so it already lands on `Ready=False` with its own reason and is untouched by
this branch.

### The `PolicyDenied` reason stays on `Ready` when the engine sets it

The request controller stops writing `Ready`, but the engine's own per-target
denial reason is left alone. It is dead code today, yet the moment it becomes
reachable, "some targets denied" is a genuine per-target `Ready` failure that
belongs in the aggregate — and having the request controller delete it would
recreate the clobber in the opposite direction.

### Test both controllers, drive the engine synchronously

The new test runs the live request controller (registered with the suite's
manager) and calls the engine reconciler directly against the same object.
Registering both with the manager would test the same seam, but the engine's
pass would race the request controller's, making "which write landed last"
timing-dependent — the very thing that hid the bug. Driving the engine
synchronously makes the terminal state deterministic and lets the test assert
that a second and third engine pass change nothing at all (design D8).

## Risks / Trade-offs

- **Existing installations flip from green to red.** → That is the fix, but
  it is visible: anything alerting on `Ready` starts firing for replications
  that were never replicating. Called out in the proposal, the changelog, and
  `docs/policies.md`.
- **Two conditions is more surface for a reader to learn.** → Mitigated by
  making `Ready` the only one an alert needs: `TargetsResolved` explains,
  `Ready` decides.
- **A future third writer could reintroduce the clobber.** → Ownership is now
  stated normatively in the spec, documented at each condition constant, and
  covered by a test that fails on status churn rather than only on a wrong
  value.
- **`NoTargets` is emitted as a warning event on transition.** → Rate-limited
  by the existing per-(object, reason) event limiter, and it fires on
  transition only, so a permanently denied object produces one event per
  reason change, not one per reconcile.

## Migration Plan

No data migration and no CRD schema change; conditions are additive. On
upgrade, the first reconcile of each `Replication` rewrites `Ready` and adds
`TargetsResolved`. A stale `Ready`/`PolicyDenied` left behind by the old
request controller is overwritten by the engine on that same pass, so no
cleanup code is needed. Rollback is a straight revert: the older binary
rewrites `Ready` on its next pass and simply ignores the extra condition.
