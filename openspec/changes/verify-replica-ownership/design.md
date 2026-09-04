# Design: Verify replica ownership

## Context

Issue #36 proposes two complementary shapes:

1. reconcile-time verification of inventory members via `Transport.Get`,
   independent of the informer;
2. a signal when an inventory member is not found in the filtered cache after
   a resync.

Reading the engine changes the shape of both.

**Shape (1) already exists — it is just misinterpreted.** `applyTarget`
already performs a live `Transport.Get` for every allowed slot on every
reconcile, and `collectGarbage` performs one for every entry it is about to
delete. Between them they cover every inventory entry except revoked-retained
ones (D5). What is missing is not the read; it is that the *result* of the
read is classified with a predicate that cannot express "this is our replica,
relabelled":

```go
// DecideConflict
if IsManagedReplica(labels, src.GetUID()) { return ConflictDecision{Action: ActionApply} }
if labels[LabelManagedBy] == ManagedByValue { /* different source → Fail */ }
// → falls through to "unmanaged object", conflict policy decides
```

`IsManagedReplica` requires *both* `app.kubernetes.io/managed-by: k8s-r8r`
*and* a matching `r8r.io/source-uid`. Strip only the first and the engine's
own replica lands in the unmanaged branch. With the default effective conflict
policy of `Fail` that produces `ActionFail`, and `fail(..., retry=false)`
removes the inventory entry — the engine forgets a replica it created.

So the actual behaviour today is not "nothing happens". It is:

| when | today |
|---|---|
| watch | permanently blind to the replica (it is outside the label selector) |
| next periodic reconcile (`--spoke-resync`, default 10h) | live read succeeds, object misclassified as a foreign conflict, `Conflict` condition, **inventory entry dropped** |
| following reconcile | entry re-planned, dropped again — an oscillation, and one `Conflict` event per cooldown window |
| `Replication` deleted meanwhile | the replica is only cleaned up if the deletion catches a cycle where the entry exists; otherwise it is stranded |
| GC of a deselected target | safety gate sees a non-replica, drops the entry **silently** — replica stranded, no event, no metric |

Detection latency degrading from seconds to `--spoke-resync` is the reported
bug. The dropped inventory entry and the silent GC release are worse, because
they strand a copy of the replicated Secret with nothing tracking it.

**Shape (2) does not need cache probing.** "Not in the filtered cache" and
"does not carry `managed-by: k8s-r8r`" are the same statement — the label *is*
the selector. Deriving the signal from the live read is therefore strictly
better than asking the informer: it is authoritative rather than
eventually-consistent, it cannot false-positive on cache lag or on an informer
that has not synced yet, and it needs no new plumbing from `DriftDetector`
into the reconciler. Shape (2) is implemented as a classification of the read
that shape (1) already performs.

## Decisions

### D1: Three-state ownership, keyed on the provenance label

```go
type ReplicaOwnership int
const (
    OwnershipForeign  ReplicaOwnership = iota // neither mark
    OwnershipManaged                          // managed-by + matching source-uid
    OwnershipStripped                         // source-uid matches, managed-by does not
)
```

`r8r.io/source-uid` carries the hub source object's UID. Nothing but this
engine writes it, and a UID is not guessable or reusable — a Secret deleted
and recreated under the same name gets a different UID, and the request
controller then treats it as a different source. An object carrying our
source's UID at the exact name our inventory records is our replica, whatever
happened to its `managed-by` label.

The two labels are not interchangeable: `managed-by` is a *conventional*
label that third-party tooling legitimately writes (it is
`app.kubernetes.io/`-namespaced and half the ecosystem stamps it), while
`source-uid` is `r8r.io/`-namespaced and specific. That asymmetry is the whole
reason the failure exists, and it is what makes `source-uid` the right thing
to key recovery on.

`IsManagedReplica` is left exactly as it is. It is the GC safety gate and the
conflict predicate; weakening it would weaken both. The new classification is
additive.

**Rejected: widening the cache selector.** That is design D3's ("the hub must
never cache replica payloads") memory bound across a fleet, and it would trade
a correctness bug for an unbounded one.

**Rejected: gating repair on the inventory entry's `LastAppliedHash`.** A
recorded hash proves the engine applied the object, which looks like a useful
second key. It is a trap: the pre-fix code path *removes* the entry on
conflict-`Fail`, so after a single bad cycle on an old operator the hash is
gone and the entry is re-planned empty — an upgrade from an affected version
would find the repair permanently disabled on exactly the replicas that need
it. The source-UID label alone is the durable evidence.

### D2: `Stripped` is repaired, not conflicted

A new case in `applyTarget`'s existing switch, ahead of `DecideConflict`:

```go
case ClassifyReplicaOwnership(existing.GetLabels(), src.GetUID()) == OwnershipStripped:
    // re-apply; the ownership marks come back with it
```

The re-apply is a plain `applyWithRecreate` of the rendered desired object,
identical to the drift-repair write. Its important second effect is that
**restoring `managed-by` puts the object back inside the spoke cache's label
selector, so the informer starts delivering events for it again.** The repair
is not merely a workaround for the blindness — it ends it. Nothing else needs
to notify the `DriftDetector`.

Is re-applying a form of stealing? No, and the boundary is worth stating:
this is not the conflict path and it is not `Overwrite`. It applies only to
an object that carries this replication's own source UID at a name its own
inventory records. A controller that genuinely wants to own the object has to
remove `r8r.io/source-uid` too — at which point the object is `Foreign`, the
existing conflict machinery handles it, the two-key conflict contract (#34)
applies unchanged, and the engine will not touch it under the default policy.

**Ready is truthful afterwards.** Post-repair the replica matches the source
and carries correct marks, so `Ready=True` for that target is a true
statement, not the false one the issue describes.

### D3: Cost at fleet scale — no additional reads on the healthy path

The instruction was to think about 50 clusters × 500 replicas and say so
explicitly.

Take the whole inventory of a hub, and ask which entries are read live today:

| entry class | live `Get` today | by |
|---|---|---|
| desired + policy-allowed | yes, every reconcile | `applyTarget` |
| not kept (deselected, renamed, revoked-`Delete`, Replication deleting) | yes, before deleting | `collectGarbage` safety gate |
| revoked + retained | **no** | — (D5) |

Every entry except the retained ones is already read. This change adds a
label comparison to reads that already happen. **The steady-state API cost of
the fix is zero additional requests.**

The absolute numbers, for the record. 50 clusters × 500 replicas = 25,000
inventory entries. A healthy `Replication` requeues after
`Options.DriftResync` (`--spoke-resync`, default 10h), so the existing
verification sweep is already ~25,000 spoke `GET`s per 10h ≈ **0.7 requests
per second across the whole fleet**, ~0.014 rps per spoke API server. That is
noise next to a kubelet. It is also unchanged by this proposal — worth
stating precisely because the obvious implementation of "verify every
inventory member on every reconcile" would have *introduced* that load, and
it turns out not to be needed.

Extra cost only appears where something is actually wrong: one additional
apply per stripped replica, bounded by the number of tampered replicas, not
by fleet size. Metric cardinality grows by one family × clusters × 3 bounded
action values.

The one number that does move is the **hub-side** reconcile: nothing, because
classification is a map lookup on labels already in memory.

### D4: Detection latency stays bounded by `--spoke-resync`

Worst-case time to notice a stripped label is one resync interval — 10h by
default — because the watch cannot see the object and the periodic reconcile
is what finds it.

A dedicated, shorter ownership-verification interval was considered and
rejected:

- it needs a new flag in `cmd/main.go`, a file another change owns this
  sprint, and a flag is a permanent API for a transient problem;
- the repair is self-healing: once an object is repaired it is back in the
  watch, so the slow path is traversed once per tampering event, not
  continuously;
- operators who need a tighter bound already have the knob. `--spoke-resync`
  lowers both the informer resync and the periodic reconcile together, which
  is the correct coupling — they are the same fallback.

This is stated in `docs/security.md` so the window is a documented property
rather than a surprise.

### D5: Revoked-but-retained entries are deliberately not verified

`revocationPolicy: Retain` entries sit in `keep` but are never applied and
never collected, so they are the one class of inventory entry with no live
read. Adding one would be cheap. It is still wrong:

the engine's promise for a retained replica is *"retained but no longer
updated"* — the target's own condition message says so. Repairing its
ownership marks would be a write to an object the engine has explicitly
stopped writing to, and would silently re-arm drift correction on material
that policy no longer permits it to manage.

So retained replicas are outside ownership verification by the same rule that
puts them outside drift repair. This is a documented boundary, not an
oversight: `docs/security.md` states that a retained replica is released from
the engine's control, and that deleting it is the operator's call.

### D6: A separate signal from drift correction — and both when both apply

`k8s_r8r_drift_corrections_total` carries an invariant established by #30 and
written into its help string: *a non-zero value always means a replica's
payload was actually wrong*. A stripped `managed-by` label over byte-identical
content is not a payload problem, so counting it there would break that
invariant for the same reason the annotation-only repair is excluded.

Hence a separate family:

```
k8s_r8r_replica_ownership_lost_total{cluster, action}
```

with `action` ∈ `repaired` | `deleted` | `orphaned`. `cluster` and `action`
are both already on the metrics allowlist; the replica's namespace and name
are unbounded and belong in the event, which carries them in full.

The three actions answer different operator questions and must not be one
counter:

- `repaired` — a replica was outside the watch and has been put back. Ongoing
  increments mean something on that spoke keeps taking the label, i.e. a
  controller fight.
- `deleted` — a stripped replica was garbage-collected. Confirms cleanup
  worked *despite* the tampering; a silent gap here is what strands secrets.
- `orphaned` — an object at an inventoried name is unrecognisable, so the
  entry was released without deleting anything. **This is the only action
  that implies manual work**, and it needs to be alertable on its own.

**Both signals fire when both conditions hold.** A replica that lost its label
*and* had its payload rewritten increments `replica_ownership_lost{repaired}`
*and* `drift_corrections`, and emits both events. That composition is the
point: the counters stay independently meaningful, and the pair "ownership
repaired + drift corrected on the same replica" is the strongest tampering
signature the operator can get.

### D7: The silent GC release becomes an event

`collectGarbage`'s gate is correct in refusing to delete an object it cannot
recognise — a planned-but-never-applied entry must never delete a bystander.
Its bug is the `continue` that follows: the entry disappears with no output.

The spec already forbids this. *Inventory and garbage collection* ends with
"No code path may lose track of a created replica", and this path does
exactly that. The fix keeps the refusal and adds the report:

- `OwnershipStripped` → proceed to delete (it is ours; `source-uid` says so),
  count `deleted`;
- `OwnershipForeign` → release the entry, count `orphaned`, and emit a
  `Warning` `ReplicaOrphaned` naming the replica and saying the object on the
  spoke may need manual cleanup.

Deleting a stripped replica during GC deserves its own justification, because
it is a delete the old code would not have performed. It is the same object
this `Replication` created, at the name it recorded, carrying its source UID;
GC is precisely the removal of what the engine created. Not deleting it is
the worse outcome — it leaves a copy of a Secret on a spoke with the tracking
entry gone. And the case where the object is genuinely somebody else's now
(both marks rewritten) is `Foreign`, which is still never deleted.

**No status change.** Neither path adds a `targetState`: a released orphan is
not a desired slot, and inventing a slot state for it would put a
non-target into `status.targets`. The `ClusterGone` release precedent — event
only, no state — is followed exactly.

### D8: Message contents

```
replica <cluster>/<ns>/<name> lost its app.kubernetes.io/managed-by label and had dropped
out of the drift watch; ownership marks and content restored

inventory entry <cluster>/<ns>/<name> no longer carries this replication's ownership
labels; releasing it without deleting anything — the object on the spoke may need manual
cleanup
```

Built from `replicaRef()` and label-name constants. No payload term appears,
which keeps both clear of the AST ratchet in
`internal/telemetry/secretsafety_audit_test.go` (`Data` / `StringData` /
`BinaryData` selectors and string-index keys are banned inside `event` /
`Eventf` / `Sprintf` arguments). Naming the label explicitly is deliberate:
the operator's next action is to find what is writing it.

`Warning` for both. Something outside the operator changed ownership metadata
on replicated material; that belongs in the stream an operator filters for.

## Risks / Trade-offs

- **A GitOps tool that owns `app.kubernetes.io/managed-by` fleet-wide** now
  gets its label overwritten on every reconcile instead of a `Conflict`, and
  the two controllers will fight — visibly, at ~1 write per `--spoke-resync`
  per replica plus one `repaired` increment. That is louder than today's
  silence and much louder than today's stranded secret, and the escape hatch
  is real: the tool should not target objects labelled `r8r.io/source-uid`,
  or the replica should not be replicated there.
- **Existing fleets will report ownership losses that were previously
  invisible.** Same shape as #30's rollout: a fleet with pre-existing damage
  looks like a new problem on upgrade. It is not new — it was simply
  unobservable.
- **10h default detection window** for the eviction itself (D4). Bounded and
  documented, and reduced by lowering `--spoke-resync`.
- **A stripped replica is now deleted at GC time** where it used to be
  abandoned (D7). This is a behaviour change in the deleting direction, so it
  is called out in the CHANGELOG. It only ever applies to an object carrying
  this replication's own source UID.
- **Retained replicas stay unverified** (D5) — accepted, documented.
