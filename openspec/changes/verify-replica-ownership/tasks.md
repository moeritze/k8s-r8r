# Tasks — verify replica ownership

## 1. Ownership classification

- [x] 1.1 Add `ReplicaOwnership` (`Foreign` / `Managed` / `Stripped`) and
      `ClassifyReplicaOwnership(labels, sourceUID)` in
      `internal/engine/drift.go`, with a doc comment stating that
      `app.kubernetes.io/managed-by` is the spoke cache's membership
      predicate, so `Stripped` means "invisible to the drift watch".
- [x] 1.2 Leave `IsManagedReplica` unchanged — it is the GC safety gate and
      the conflict predicate (design D1).

## 2. Telemetry

- [x] 2.1 Add the `replicaOwnershipLost` counter
      (`k8s_r8r_replica_ownership_lost_total{cluster, action}`) with
      `IncOwnershipLost(cluster, action)` and the three action constants
      (`repaired`, `deleted`, `orphaned`); register it. Help string must say
      what separates it from `k8s_r8r_drift_corrections_total`.

## 3. Engine — repair on the apply path

- [x] 3.1 New case in `applyTarget`'s existing switch, ahead of `default:`,
      for `OwnershipStripped`: re-apply via `applyWithRecreate`, count
      `repaired`, emit a `Warning` / `OwnershipRepaired` event naming the
      replica and the label. Keep the inventory entry.
- [x] 3.2 When the stripped replica's content had *also* diverged, additionally
      emit `DriftCorrected` and increment `k8s_r8r_drift_corrections_total`,
      preserving #30's invariant (design D6).
- [x] 3.3 Note in a code comment that restoring the label puts the object back
      inside the cache's label selector — the repair ends the blindness.

## 4. Engine — reported release on the GC path

- [x] 4.1 Replace `collectGarbage`'s `!IsManagedReplica` gate with the
      three-state classification: `Stripped` falls through to the delete
      (count `deleted`), `Foreign` releases the entry with a `Warning` /
      `ReplicaOrphaned` event and counts `orphaned`.
- [x] 4.2 No `targetState` for either path — event only, like the
      `ClusterGone` release (design D7).

## 5. Tests

- [x] 5.1 `ClassifyReplicaOwnership` table: both marks, stripped label,
      rewritten label, foreign object, empty labels, different source UID.
- [x] 5.2 Stripped label at a desired slot: label restored, entry kept, one
      `repaired` increment, one `OwnershipRepaired` Warning naming the replica
      and the label, no `Conflict`.
- [x] 5.3 Stripped label + diverged content: both events, both counters.
- [x] 5.4 Foreign object at a desired slot still takes the conflict path
      unchanged (regression guard for #34's two-key contract).
- [x] 5.5 GC of a stripped replica actually deletes it from the spoke and
      counts `deleted` (deselected target and Replication deletion).
- [x] 5.6 GC of a foreign object at an inventoried name deletes nothing,
      releases the entry, counts `orphaned`, and emits `ReplicaOrphaned`.
- [x] 5.7 No payload appears in any new message (explicit leak assertions).
- [x] 5.8 Telemetry: the new family joins `exerciseAll` and the inventory /
      cardinality audits.
- [x] 5.9 Confirm `TestNoPayloadFieldsInMessageFormatting` still passes.

## 6. Docs

- [x] 6.1 `docs/security.md`: the ownership label as the watch's membership
      predicate, the failure it used to cause, the new signals table, the
      `--spoke-resync` detection window, and the retained-replica exception.
- [x] 6.2 `CHANGELOG.md`: entry under Unreleased, including the
      behaviour change that a stripped replica is now deleted at GC time.
- [ ] 6.3 `obsidian/` vault updates: owned by a parallel change in this
      sprint; the required edits are listed in the PR body.

## 7. Verification

- [x] 7.1 `make lint && make test` — 290 tests green. Lint verified against a
      pristine export of the tree (`golangci-lint run` from inside an agent
      worktree also analyses sibling worktrees, which produces unrelated
      `SA5011` noise from other agents' in-flight files); `origin/main` and
      this branch both report 0 issues in isolation.
- [x] 7.2 `make test-e2e` — not run locally (no docker on this machine), but
      CI's `E2E (kind fleet, hub + 2 spokes)` job ran it on this branch and
      passed (10m59s, run 33870175449). No e2e case was
      added for the label-strip path on purpose: e2e runs with the default
      10h `--spoke-resync`, and the eviction is invisible to the watch by
      construction, so the case cannot be exercised there without a
      resync-interval override. The existing "drift repair on replica
      edit/delete" cases still cover the unchanged watch path.
