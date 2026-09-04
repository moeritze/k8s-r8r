# Truthful replication status for zero-target Replications

## Why

A `Replication` whose targets are all refused by policy reports
`Ready: True`, reason `AllTargetsReady`, message `0/0 targets ready`
(issue #27). In status it is indistinguishable from a healthy fanout, so
there is nothing to alert on: a typo'd cluster selector, a policy tightened
too far, and a working replication all present identically. For an operator
whose job is distributing credentials, a silent no-op is the failure mode
most worth surfacing — and it is currently the only one that makes an
unhealthy system look healthy.

The cause is two controllers writing one condition with no arbitration:

1. The request controller's denial path sets `Ready=False`/`PolicyDenied`
   on the `Replication` (`internal/controller/request/controller.go`).
2. The engine's `buildStatus` then rewrites `Ready` from scratch on its
   next reconcile (`internal/engine/status.go`), deriving it purely from
   `failed == 0`. Zero targets means zero failures, so the denial becomes
   `Ready=True`.

The engine already guards one foreign writer (it skips objects marked
`NotAuthoritative`) but has no guard for the denial verdict. Because the
request controller also watches `Replication` objects, the engine's write
re-enqueues the source, which re-denies, which re-enqueues the engine: an
unbounded status ping-pong on every denied `Replication`, throttled only by
the rate limiter, and a violation of design D8 ("no status churn when
nothing changed").

Two further cases share the symptom with different causes:

- **Revocation.** Tightening a policy behaves correctly otherwise
  (`PolicyRevoked` and `CleanedUp` events fire, replicas are deleted), but
  the `PolicyRevoked` condition is actively *removed* once the revocation is
  fully processed. The events are real but transient; after they expire the
  only durable record says `Ready: True`, `0/0`.
- **Selector typo.** A `target-clusters` selector matching no ready cluster
  makes target resolution return before policy is consulted, with an empty
  denial list — so the denial reporting path does nothing at all and no
  event is emitted either. This case never even flaps: it is silently green
  forever, which is arguably worse than the denied case.

## What Changes

- **New `TargetsResolved` condition on `Replication`**, owned exclusively by
  the request controller: `True` once at least one (cluster, namespace) pair
  survives the selector and policy evaluation; `False`/`PolicyDenied` when
  candidates existed but policy refused them all; `False`/`NoTargets` when
  the request produced no candidate targets at all. This is the durable
  record of *why* nothing resolved, and it gives `kubectl wait` and Argo CD
  health checks something to key on.
- **The request controller no longer writes `Ready`.** Condition ownership
  becomes explicit: `Ready` belongs to the engine, `TargetsResolved` to the
  request controller, `NotAuthoritative` to the authority controller. This
  alone ends the ping-pong — each controller writes only its own condition,
  and each skips the write when nothing changed.
- **`Ready=False`/`NoTargets` when a live `Replication` has zero desired
  targets.** "Asked to replicate, replicated nothing" is a failure, not a
  vacuous success. Scoped to objects that are not being deleted: a
  `Replication` on its way out legitimately has no desired targets.
- **`NoTargets` joins `PolicyDenied` as an event-worthy Ready transition**,
  so the condition is accompanied by a warning event rather than the
  spurious `Replicated 0/0 targets ready` event the ping-pong used to
  manufacture.
- Tests: a new suite that runs **both** controllers against the same object
  and asserts the terminal condition state. Neither existing suite wired
  more than one controller, which is why CI could never have caught this.
- No CRD schema change: `status.conditions` is already `[]metav1.Condition`.

**Visible behavior change for existing installations:** `Replication`
objects that today report `Ready: True` with zero targets will report
`Ready: False`, reason `NoTargets`, on the next reconcile. That is the fix,
but anything alerting on `Ready` will start firing for replications that
were never actually replicating.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `replication-request`: tightens the denial-reporting requirement (a
  condition is now required, not "condition or event"), adds the
  zero-resolved-targets scenario to the status requirement, and makes
  per-controller condition ownership normative.
- `replication-policy`: makes "status reflects the revocation" concrete —
  the post-revocation state must be a durable condition, not events that
  expire.

## Impact

- `api/v1alpha1/replication_types.go` — `ReplicationConditionTargetsResolved`,
  `ReasonNoTargets`, `ReasonTargetsResolved`.
- `internal/controller/request/controller.go` — `reportDenial` becomes
  `reportTargetResolution`, writes `TargetsResolved` (including the
  previously silent no-candidates case) and never touches `Ready`.
- `internal/engine/status.go` — `buildStatus` gains the zero-desired-targets
  branch; `Ready` derivation documented as engine-owned.
- `internal/engine/reconciler.go` — `emitTransitionEvents` also reports
  `NoTargets`.
- `internal/controller/request/status_arbitration_test.go` (new) — both
  controllers on one object, terminal-state and no-churn assertions.
- `test/e2e/replication_test.go`, `test/e2e/framework.go` — the revocation
  test asserted a reason the ping-pong produced transiently; it now asserts
  the terminal state.
- `config/crd/bases`, `charts/k8s-r8r/crds` — regenerated doc comments only.
- `docs/policies.md`, `obsidian/` notes, `CHANGELOG.md`.
