# Telemetry review checklist

Apply to every change that adds or edits a metric, event, condition
message, error string, or log line. Backing spec:
`openspec/changes/bootstrap-k8s-r8r-operator/specs/observability-operations/spec.md`.

## Secret safety (hashes only)

- [ ] No event, condition, error, or log message interpolates payload
      fields (`Data`, `StringData`, `BinaryData`, `data[...]`). Object
      references (kind, namespace, name, cluster) and content **hashes**
      are the only identifying output allowed.
- [ ] Content comparisons surfaced anywhere use `SourceHash` values, never
      raw or base64 payload.
- [ ] Credentials (kubeconfigs, SA tokens) never appear in errors — refer
      to the credential Secret by name only.
- [ ] The static ratchet passes: `TestNoPayloadFieldsInMessageFormatting`
      (this package) and the runtime canary
      `TestReconcile_NoSecretPayloadInMessagesOrEvents` (internal/engine).

## Metric cardinality (bounded labels)

- [ ] New metrics are prefixed `k8s_r8r_` and registered on the
      controller-runtime `metrics.Registry`.
- [ ] Labels are only `cluster`, `namespace`, `kind`, or a small closed
      enumeration (`dimension`, `policy`, `action`, `result`). **Never**
      object names, UIDs, hashes, or messages.
- [ ] `TestMetricLabelCardinalityBounded` still passes (extend its
      allowlist only for another *bounded* label, with review).
- [ ] Not duplicating controller-runtime built-ins
      (`controller_runtime_reconcile_*`, `workqueue_*`).

## Events

- [ ] Events fire on lifecycle *transitions* (replicated, denied, conflict,
      revoked, cleaned up), not on every reconcile of a steady state.
- [ ] Repeatable emissions go through the engine's `EventLimiter` so a
      flapping target cannot flood the event stream.
- [ ] Event messages carry object references and counts; per-target detail
      beyond the status cap belongs here, payloads never do.

## Probes / HA

- [ ] Readiness input stays exactly one thing: hub informer sync
      (`InformerSync`). Never wire spoke connectivity, cluster manager
      state, or per-target health into readyz.
- [ ] New runnables that must run on standby replicas implement
      `NeedLeaderElection() false`; reconciling work stays leader-gated.
