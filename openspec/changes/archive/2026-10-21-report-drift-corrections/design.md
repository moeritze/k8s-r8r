# Design: Report drift corrections

## Context

`Reconciler.applyTarget` decides what to do with an existing object on a
target cluster via `DecideConflict`. Three of the four outcomes are already
observable: `ActionFail`, `ActionAdopt` and `ActionOverwrite` each increment
`k8s_r8r_conflicts_total` and emit an event. `ActionApply` — the branch for
objects the engine already owns — is the odd one out:

```go
case ActionApply:
    if existing.GetAnnotations()[AnnotationSourceHash] != hash || SourceHash(existing) != hash {
        if applyErr := r.applyWithRecreate(ctx, s.cluster, desired); applyErr != nil {
            return fail(classifyTransportErr(applyErr), applyErr.Error(), true)
        }
    }
```

The condition is the drift check. Whether it fired or not, control reaches the
same `st.Ready = true` and the same inventory upsert. Nothing downstream can
tell the two apart.

The available signals do not fill the gap:

- `k8s_r8r_drift_events_total{cluster}` counts informer callbacks that
  enqueued a reconcile, apply echoes included. It rises on every legitimate
  source update and says nothing about whether a repair happened.
- `Replication` status reports the *current* state, which after the
  correction is legitimately healthy.
- The `Replicated` event only fires on a Ready-condition *transition*, and
  drift correction does not change Ready.

## Decisions

### D1: Event plus metric, no status field

The event carries "when and what" (object ref, both hashes, timestamp, and
`kubectl describe` visibility); the metric carries "how often" and is what an
alert rule can be written against. A `status.lastDriftCorrection` timestamp
was rejected: it would reopen the `Replication` status API and its spec delta
for information the event already carries verbatim, it would write to the hub
API server on every correction (status discipline, design D8), and a single
timestamp cannot express "twice a minute for the last hour" — which is the
question an operator actually has, and which the counter answers directly.

### D2: Only payload divergence counts as drift

The write in `ActionApply` fires on `annotation != hash || SourceHash(existing)
!= hash`. Those disjuncts are different events:

| stored annotation | content hash | meaning |
|---|---|---|
| stale | differs | replica payload was rewritten on the spoke — **drift** |
| current | differs | payload rewritten, annotation left untouched — **drift** |
| stale | equal | only the engine's own bookkeeping annotation is stale — **metadata repair** |

Only content divergence increments `k8s_r8r_drift_corrections_total` and emits
`DriftCorrected`. The metadata repair is written but stays silent, for one
concrete reason: a change to the *hashing rules* produces exactly that state
across the entire fleet on upgrade. The immediately preceding change,
`strip-foreign-ownership-metadata`, did it — extending `cleanRawKeys` changed
`SourceHash` for every source carrying a foreign ownership key, so on rollout
each affected replica had a stale stored annotation over content that
recomputed as equal. Counting those would have turned an operator upgrade into
a fleet-wide tamper alarm, and one false fleet-wide spike is enough to teach
an operator to ignore the metric permanently.

Keeping it out buys a crisp invariant, which is what makes the metric worth
alerting on: **a non-zero `k8s_r8r_drift_corrections_total` always means a
replica's payload was actually wrong.**

The cost is that hand-editing only the `r8r.io/source-hash` annotation, while
leaving content correct, is repaired without a signal. That is acceptable: no
secret material was altered, and the annotation is the engine's own cache, not
a security boundary — the engine never trusts it alone, which is exactly why
the second disjunct (`SourceHash(existing) != hash`) exists and why the tamper
detection does not depend on the annotation at all.

`SourceHash(existing)` is now computed unconditionally rather than as the
second operand of a `||`. No extra cost on the healthy path — the annotation
matches there, so the disjunction always evaluated it anyway.

### D3: Cluster-only metric label

`{cluster}` and nothing else. Namespace and name of the drifting replica are
exactly the unbounded values the cardinality rule forbids and the telemetry
test rejects; they belong in the event, which carries them in full. Splitting
payload-vs-metadata into a label was also rejected — per D2 the metadata case
is not counted at all, so the label would have a single value.

### D4: Coalescing is the contract, not a bug

`r.event` runs through `EventLimiter` (default 5-minute cooldown per
`(object, reason, message)`). A replica that is rewritten every 30 seconds
with the same content produces the same message — same object ref, same two
hashes — so after the first event the rest are suppressed until the message
changes or the window expires.

That is the intended flood control, and it is what the observability spec's
"Flapping target" scenario asks for. It does mean the event stream understates
the *rate* of drift by design. The counter is deliberately not rate-limited
and is the correct source for "how often", so the pair is: event = "this
happened, here is which object", metric = "and it is happening N times a
minute". This is written into the spec so a future reader does not file the
coalescing as a lost-event bug.

### D5: Message contents

```
restored replica <cluster>/<namespace>/<name>: observed content sha256:<hex>, expected sha256:<hex>
```

Built from `replicaRef()` and two `SourceHash()` results. No object payload is
touched, which keeps it clear of the AST ratchet in
`internal/telemetry/secretsafety_audit_test.go` (payload selectors `Data` /
`StringData` / `BinaryData` and string-index keys are banned inside `event` /
`Eventf` / `Sprintf` arguments). Reaching for `existing.Object["data"]` to
describe *what* changed would trip that audit — correctly. Full hashes rather
than truncated prefixes: they are not secrets, they are directly comparable to
`status.sourceHash` and to the replica's annotation, and truncation would make
distinct drifts collide into one limiter key.

`Warning`, not `Normal`: something outside the operator changed replicated
material, and an operator filtering events by type should see it.

## Risks / Trade-offs

- **Alert noise on legitimate churn.** A source updated in a hot loop does
  *not* count — the engine's own writes are compared against the freshly
  rendered `hash`, so a source update produces `ActionApply` with matching
  hashes on the next pass. Only writes the engine did not make can diverge.
- **A second replicator fighting for the object** produces a steady climb in
  the counter with the event coalesced — exactly the case D4 documents, and
  the reason the metric had to exist alongside the event.
- **Existing fleets** will start reporting corrections that were previously
  silent. That is the fix, but it is visible: a fleet with pre-existing drift
  looks like a new problem on upgrade.
