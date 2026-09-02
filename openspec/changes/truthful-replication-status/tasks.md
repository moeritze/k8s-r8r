# Tasks — truthful replication status

## 1. API

- [x] 1.1 Add `ReplicationConditionTargetsResolved` and document per-condition
      ownership (`Ready` = engine, `TargetsResolved` = request controller) on
      the condition constants.
- [x] 1.2 Add `ReasonNoTargets` and `ReasonTargetsResolved`; pin both in the
      enum-value test that guards against string drift.
- [x] 1.3 Update the `status.conditions` doc comment, regenerate
      `config/crd/bases`, and re-copy the chart CRD.

## 2. Request controller

- [x] 2.1 Replace `reportDenial` with `reportTargetResolution`: write
      `TargetsResolved` as `True`/`TargetsResolved`, `False`/`PolicyDenied`,
      or `False`/`NoTargets`, and never write `Ready`.
- [x] 2.2 Cover the previously silent path — no candidate targets at all, so
      an empty denial list — with `False`/`NoTargets`.
- [x] 2.3 Update the package doc comment: the denial verdict lives on
      `TargetsResolved`; `Ready` belongs to the engine.

## 3. Engine

- [x] 3.1 `buildStatus`: `Ready=False`/`NoTargets` when a live `Replication`
      has zero desired targets and no failures; keep `AllTargetsReady` for a
      real fanout and the per-target reason for failures.
- [x] 3.2 Skip the branch while the object is being deleted, and confirm the
      single-condition status path (source missing / kind not allowlisted) is
      unaffected — it passes one non-ready state, so `failed == 1`.
- [x] 3.3 `emitTransitionEvents`: report `NoTargets` alongside `PolicyDenied`,
      so the spurious `Replicated 0/0 targets ready` event is replaced by a
      warning that matches the condition.

## 4. Tests

- [x] 4.1 New suite running BOTH controllers against one object: the live
      request controller from the manager plus the engine reconciler driven
      synchronously.
- [x] 4.2 Denied request: terminal state is `Ready=False`/`NoTargets` with
      `TargetsResolved=False`/`PolicyDenied` surviving the engine's write.
- [x] 4.3 Selector typo: terminal state is `Ready=False`/`NoTargets` even
      though nothing was denied and no denial event fired.
- [x] 4.4 Revocation: deleting the permitting policy takes the object from
      resolved to `Ready=False`/`NoTargets`.
- [x] 4.5 No-churn test: after settling, repeated engine passes and the live
      request controller leave `resourceVersion` unchanged (design D8).
- [x] 4.6 Verify the suite fails in both directions — reverting the engine
      half reproduces `Ready=True`; reverting the request half reproduces the
      status ping-pong.
- [x] 4.7 Update the existing request-controller tests to the new condition
      contract.
- [x] 4.8 Fix the e2e revocation test, which passed by catching an
      intermediate state of the ping-pong: assert the terminal
      `Ready=False`/`NoTargets` plus `TargetsResolved=False`/`PolicyDenied`,
      and that it stays that way.

## 5. Docs

- [x] 5.1 `docs/policies.md`: describe what a denied request looks like in
      status, not just as an event, and flag the visible upgrade behavior.
- [x] 5.2 `CHANGELOG.md`: entry under Unreleased.
- [ ] 5.3 `obsidian/` notes — deferred: the vault is owned by a parallel
      change in this sprint. The required updates are listed in the PR body
      for folding in.
