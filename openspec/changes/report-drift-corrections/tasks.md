# Tasks — report drift corrections

## 1. Telemetry

- [x] 1.1 Add the `driftCorrections` counter
      (`k8s_r8r_drift_corrections_total{cluster}`) with a help string that
      distinguishes it from `k8s_r8r_drift_events_total` (corrective writes vs
      informer traffic), plus `IncDriftCorrection(cluster)`, and register it.

## 2. Engine

- [x] 2.1 In the `ActionApply` branch of `applyTarget`, compute the observed
      content hash once and split the write condition into payload divergence
      vs annotation-only repair.
- [x] 2.2 On payload divergence only: `telemetry.IncDriftCorrection(cluster)`
      and a `Warning` / `DriftCorrected` event naming the replica ref and both
      `sha256:` hashes. Hashes only — never the diverging content.
- [x] 2.3 Leave the annotation-only repair silent, with the upgrade-artifact
      rationale in a code comment (design D2).

## 3. Tests

- [x] 3.1 Payload divergence: replica restored, one `DriftCorrected` Warning
      naming the replica and carrying both hashes, counter +1, and an explicit
      assertion that neither the old nor the new payload appears in the
      message.
- [x] 3.2 Annotation-only repair: annotation restored, content untouched, no
      event, counter unchanged.
- [x] 3.3 Recurring drift with identical hashes: exactly one event (limiter
      coalescing) but three counter increments (design D4).
- [x] 3.4 Telemetry: the new family joins `exerciseAll` and the inventory /
      cardinality audits.
- [x] 3.5 Confirm `TestNoPayloadFieldsInMessageFormatting` still passes with
      the new event call.

## 4. Docs

- [x] 4.1 `docs/security.md`: how to detect replica tampering on a spoke — the
      two metrics and what separates them, the event, and the coalescing
      caveat.
- [x] 4.2 `CHANGELOG.md`: entry under Unreleased.
- [ ] 4.3 `obsidian/operations.md` (Metrics + Events) and
      `obsidian/replication-flow.md`: owned by a parallel change in this
      sprint; the required edits are listed in the PR body.

## 5. Verification

- [x] 5.1 `make lint && make test` — both green. Lint verified against a
      pristine export of the tree: `golangci-lint run` inside a parallel agent
      worktree also analyses sibling worktrees, which produces unrelated
      `SA5011` noise from other agents' in-flight files.
- [ ] 5.2 `make test-e2e` — not run locally (no docker on this machine); the
      CI `E2E (kind fleet, hub + 2 spokes)` job covers it. The existing
      "drift repair on replica edit" e2e case exercises the changed branch.
