# Design — request-side consent for the conflict policy

## Context

The conflict path (design D7) classifies an object found at a replica's
intended name. Ownership marks decide the easy cases; everything else falls to
the *effective conflict policy*. Until now that was a one-key lock: the
strongest member of `allowedConflictPolicies` across the policies that
permitted the target. The spec and the security docs both describe a two-key
lock. This design adds the missing key.

## Decisions

### D1 — Annotation name: `r8r.io/conflict-policy`

Every request key is `r8r.io/<kebab-case-noun-phrase>` and names the thing it
sets: `replicate`, `target-clusters`, `target-namespaces`, `target-name`.
`conflict-policy` follows that and matches the `ReplicationPolicy` field it
intersects with (`spec.options.allowedConflictPolicies`), so the two sides of
the two-key turn are spelled the same way in both places.

Rejected: `r8r.io/on-conflict` (verb-ish, does not match the policy field),
`r8r.io/allowed-conflict-policies` (plural implies a set, but a request states
one ceiling, not a grant list), and any `r8r.io/conflict` shorthand (too close
to the `Conflict` condition reason to read unambiguously in messages).

Values are the `ConflictPolicy` enum spellings, **case-sensitive**: `Fail`,
`Adopt`, `Overwrite`. Being lenient about case would mean a source could
differ from the policy YAML that grants it, and the closed-set rejection is
what makes typos loud.

### D2 — The closed-set check is extended, never weakened

`internal/annotations` rejects unknown `r8r.io/*` keys so that
`r8r.io/target-cluster` fails loudly instead of silently selecting nothing.
That check is deliberate and stays. The new key is added to `requestKeys`
properly, and the "valid keys" hint is now *derived* from that set
(`RequestKeys()`) rather than hand-listed in three places — the previous
hand-lists were exactly the mechanism by which a newly added key would have
silently gone unmentioned.

The webhook's mirrored `knownKeys` set is likewise derived from the parser's
set instead of restated, so the two can never disagree about what "unknown"
means.

### D3 — Absent means `Fail` (breaking), not `Adopt` (narrow)

Two candidate defaults:

| absent means | changes behaviour for | leaves working |
|---|---|---|
| `Fail` | policy-granted `Overwrite` **and** `Adopt` | nothing |
| `Adopt` | policy-granted `Overwrite` only | `Adopt` grants |

`Adopt` is the narrower migration: `Adopt` never rewrites a payload (it only
takes ownership when content hashes are already identical), and it would
change exactly the behaviour the spec sentence names — `Overwrite`.

`Fail` is chosen anyway, for three reasons:

1. **One rule, no exceptions.** The effective policy is
   `min(requested, granted)` with `requested` defaulting to the weakest value.
   An `Adopt` default makes the rule "the weaker of the two, except the
   request floor is Adopt", which is the kind of clause that gets lost in a
   later refactor — and the property test here ("effective is never stronger
   than either key") would not hold.
2. **It matches the project's ethos everywhere else.** An absent cluster
   selector selects *no* clusters. `*` is rejected. Policy is default-deny.
   `allowedConflictPolicies` itself defaults to `[Fail]`. A request that says
   nothing consenting to nothing is the same rule.
3. **`Adopt` is not free.** It takes ownership of an object k8s-r8r did not
   create — the replica becomes subject to drift correction and to
   revocation-time deletion. Issue #26 flags a separate problem on that path.
   Defaulting *toward* it is the wrong direction while that is open.

The cost is stated plainly in the proposal and the CHANGELOG: this is a
breaking change for anyone relying on a policy-granted `Overwrite` or `Adopt`
with no annotation. The failure mode is safe (a conflict is reported instead of
an object being taken over), and it is self-explaining (D5).

### D4 — Intersection by strength rank, and where it happens

`Fail` < `Adopt` < `Overwrite`, matching `internal/policy.conflictPolicyRank`
(which fixes the canonical order of the grant union) and matching
`docs/policies.md`. The grant side takes the *strongest* member of the union —
that part is unchanged and is why `Fail` being always present in the union is
harmless. The request side is a single value. The effective policy is the
weaker of the two.

Anything unnameable ranks as `Fail` on both sides, so an unrecognized value
can never read as a grant.

The intersection happens in `EffectiveConflictPolicy`, exactly where the old
comment predicted ("this function only gains an argument — no contract
change"). `DecideConflict` swaps its `sourceUID types.UID` parameter for the
source object it was derived from, which is already in scope at the single
call site; that is the one line this change touches in
`internal/engine/reconciler.go`.

**Why not carry the value in `ReplicationSpec`?** It would be the tidier data
flow — the request controller already materializes resolved targets — but it
would mean a CRD field, a request-controller change, and a status-arbitration
question, all in files another change currently owns. Reading the annotation
off the source object the engine already fetches is smaller and has no schema
cost. The trade-off is freshness: an edit to *only* this annotation changes no
`Replication` spec field, so it takes effect on the next engine reconcile
rather than immediately — bounded by the drift resync interval. Acceptable for
a value that is only consulted when a conflict is actually being resolved. If
the value ever needs to be authoritative at materialization time (e.g. to
report it in status), promoting it to `ReplicationSpec` is a compatible
follow-up.

### D5 — Denial visibility without touching status code

When a conflict resolves to `Fail` because a key did not turn, the reason goes
into `ConflictDecision.Message`, which the engine already surfaces two ways:
as the per-target `Conflict` entry in `status.nonReadyTargets`, and as the
existing `Warning`/`Conflict` event. The message distinguishes the cases:

- policy permits up to `Overwrite`, but the request does not set
  `r8r.io/conflict-policy` (absent means `Fail`);
- the request asks for `Overwrite`, but no matching `ReplicationPolicy`
  permits more than `Fail`;
- neither key turned.

This deliberately adds **no** new condition type and touches no status code.
A parallel change (#27 / PR #47) is redefining `Ready` and adding a
`TargetsResolved` condition; competing for those would be both a merge
conflict and a contract conflict. Riding the existing per-target `Conflict`
reason is also the honest modelling: a downgraded conflict policy is a
property of one target's conflict, not of the request as a whole.

Nothing is reported when the downgrade has no effect — no conflicting object,
or a downgrade that still permits the action taken. Silence there is correct:
there is nothing an operator needs to do.

No metric is added. `k8s_r8r_conflicts_total{action="fail"}` already counts
the outcome, and `internal/telemetry` is owned by another change right now.

### D6 — Admission: deny malformed, warn unsatisfiable

Adding the key to the known set means the shared parser validates its value,
so a malformed value is **denied** at admission with a message naming the
annotation — the same treatment as a malformed selector or target name.

An *unsatisfiable* value (no policy that could match this source lists it in
`allowedConflictPolicies`) only **warns**. It is not a denial: the request is
otherwise valid and replicates normally; only its conflict handling falls back
to the weaker key. This matches the webhook's standing rule that it denies
only what no policy could ever allow, and that it is advisory, never
authoritative (design D6). The check reuses the same namespace/kind narrowing
as the denial path, with the same fail-open treatment of `namespaceSelector`.

## Risks / Trade-offs

- **Breaking change on upgrade** (D3). Mitigated by a safe failure mode, a
  self-explaining message, and a CHANGELOG entry under **Changed**.
- **Annotation-only propagation latency** (D4). Bounded by the drift resync;
  documented rather than engineered around.
- **Two places now spell the conflict policies.** Mitigated by the annotations
  package importing the API enum instead of duplicating the strings, and by
  the property test that pins the ranking relationship.
