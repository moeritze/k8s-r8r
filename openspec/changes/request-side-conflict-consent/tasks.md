# Tasks — request-side conflict consent

## 1. Annotation contract

- [x] 1.1 Add `KeyConflictPolicy = "r8r.io/conflict-policy"` and add it to the
      closed `requestKeys` set (the unknown-key rejection stays as strict as
      it is).
- [x] 1.2 Add `Request.ConflictPolicy`, validate the value against the
      `ConflictPolicy` enum (case-sensitive; empty means the default), and
      default it to `Fail` when the annotation is absent.
- [x] 1.3 Add `RequestedConflictPolicy` — the lenient reader the engine uses,
      mapping anything unrecognized to `Fail`.
- [x] 1.4 Derive the "valid keys" hint from the closed set (`RequestKeys()`)
      instead of hand-listing it, so a future key cannot go unmentioned.

## 2. Engine

- [x] 2.1 `EffectiveConflictPolicy(requested, allowed)` — the weaker of the
      request and the strongest grant, ranked `Fail` < `Adopt` < `Overwrite`,
      with unknown values ranking as `Fail`.
- [x] 2.2 `DecideConflict` takes the source object (it already needed its UID)
      and reads the request key from its annotations.
- [x] 2.3 Fail messages explain which of the two keys did not turn, naming
      annotation keys and policy names only.
- [x] 2.4 Update the single call site in `reconciler.go` (one line).

## 3. Webhook

- [x] 3.1 Derive `knownKeys` from the parser's request-key set so the new key
      is validated rather than warned about.
- [x] 3.2 Carry the parsed conflict policy on `parsedRequest`.
- [x] 3.3 Warn (never deny) when no candidate policy grants the requested
      escalation; reuse the namespace/kind narrowing of the denial path.

## 4. Tests

- [x] 4.1 Intersection table over every (request, grant) combination.
- [x] 4.2 Property test: the effective policy is never stronger than either
      key.
- [x] 4.3 Decision-table cases for grant-without-request and
      request-above-grant.
- [x] 4.4 Message test: the missing key is named; no payload leaks.
- [x] 4.5 Reconciler end-to-end: a granted `Overwrite` with no request
      annotation leaves the victim object intact and reports `Conflict` with
      the annotation named; the existing Adopt/Overwrite tests now set the
      request key.
- [x] 4.6 Parser tests: closed value set, case sensitivity, absent/empty
      default, typo still rejected, aggregate error names the key.
- [x] 4.7 Webhook tests: malformed value denied, ungranted value warns and
      admits, granted value and the `Fail` default warn about nothing.

## 5. Docs

- [x] 5.1 `docs/annotations.md`: the new key in the contract table, its own
      section, and the two-key turn.
- [x] 5.2 `docs/security.md`: state the control as it now exists, including
      what happens when only one key turns.
- [x] 5.3 `CHANGELOG.md`: **Changed** (breaking, with the migration) and
      **Added**.
- [ ] 5.4 `obsidian/` vault notes — deferred: another change owns that
      directory; the needed updates are listed in the PR body.

## 6. Verification

- [x] 6.1 `make test` — green.
- [x] 6.2 `make lint` — green against an isolated copy of the tree (running it
      from inside an agent worktree pulls sibling worktrees into the analysis
      and reports pre-existing findings from them; identical count before and
      after this change).
- [ ] 6.3 `make test-e2e` — not run locally (no docker available in this
      environment); CI runs the kind-fleet job. No e2e case exercises the
      conflict path today, so none needed updating.
