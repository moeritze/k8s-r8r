# Report drift corrections

## Why

The `ActionApply` branch of `Reconciler.applyTarget`
(`internal/engine/reconciler.go`) rewrites a replica whenever its hash does
not match the source, then falls straight through to `st.Ready = true`. No
event, no metric, no condition. A corrective write and a healthy no-op
reconcile are byte-identical downstream (issue #30).

This is a **conformance gap, not a new feature**: the observability spec
already promises the event.
`openspec/specs/observability-operations/spec.md` (Secret-safe telemetry)
carries this scenario:

> #### Scenario: Drift event on a Secret
> - **WHEN** the engine reports drift on a replicated Secret
> - **THEN** the event contains the object reference and hashes, never key
>   names' values or payload fragments

The engine has never emitted that event. The scenario is currently vacuously
true — it constrains the *content* of an event that is never produced.

A counter exists but answers a different question.
`k8s_r8r_drift_events_total{cluster}` counts *spoke informer events on
managed replicas that enqueued a reconcile, including the engine's own apply
echoes* — its own help string says so. It is watch traffic, not a tamper
signal: it rises on every legitimate source update, and it cannot say whether
a replica was actually repaired. The two must not be conflated.

Severity is **medium and specifically forensic**, not a health lie: after the
correction the replica really is correct and `Ready=True` is truthful. But for
an operator whose job is distributing credentials, "someone repeatedly
rewrites a replicated Secret on a spoke" is a security signal that today
produces zero output, and a second replicator fighting over the same object
is invisible — both look exactly like a quiet, healthy fleet.

## What Changes

- New metric `k8s_r8r_drift_corrections_total{cluster}`, incremented once per
  replica whose **content** the engine rewrote because its payload hash did
  not match the source. Cluster-only label, per the bounded-cardinality rule.
- New `Warning` event `DriftCorrected` on the `Replication`, naming the
  replica (cluster/namespace/name) and both `sha256:` hashes — the observed
  one and the expected one. Hashes only; no payload, ever.
- The `ActionApply` branch distinguishes two sub-cases that today share one
  code path:
  - **payload divergence** (`SourceHash(existing) != hash`) — counted and
    evented, as above;
  - **annotation-only repair** (content already equal, only the stored
    `r8r.io/source-hash` annotation stale) — the write still happens, but it
    is deliberately **not** counted and **not** evented. See design D2.
- Deliberately **no status API change**. No `lastDriftCorrection` field: the
  event carries the timestamp, the metric carries the rate, and the
  `Replication` status API stays closed for this change.
- Documented consequence of the existing `EventLimiter` (5-minute
  per-(object, reason, message) cooldown): drift that recurs with identical
  hashes coalesces into a single event. That is correct flood control, so the
  "drift recurs constantly on this spoke" signal must be read off the metric,
  which is not rate-limited. Stated normatively so the coalescing is not later
  filed as a bug.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `observability-operations`: the metric minimum gains drift corrections; the
  lifecycle-event enumeration gains drift correction; the rate-limiting
  requirement states that a rate-limited event is paired with an unlimited
  counter.
- `replication-engine`: "Replica edited on target" now requires that the
  correction is *recorded*, not only performed, and separates content
  divergence from a metadata-only repair.

## Impact

- `internal/telemetry/metrics.go` — `driftCorrections` counter,
  `IncDriftCorrection(cluster)`, registration.
- `internal/telemetry/metrics_test.go` — the new family joins the inventory
  and cardinality audits.
- `internal/engine/reconciler.go` — `applyTarget`, `ActionApply` branch only.
- `internal/engine/driftcorrection_test.go` (new) — payload divergence emits
  event + metric and leaks no payload; annotation-only repair is silent;
  recurring drift coalesces events but not the counter.
- `docs/operations.md`, `docs/troubleshooting.md` — the new metric and event
  in the alerting/triage surface, including the two-signal split
  (`drift_events_total` = watch traffic, `drift_corrections_total` =
  corrective writes).
- `CHANGELOG.md` — entry under Unreleased.
- No CRD, no chart, no RBAC change. `Replication` status is untouched.
