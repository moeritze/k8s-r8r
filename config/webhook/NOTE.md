# Note for the Helm chart integration (later task group)

This directory is kept kustomize-clean; nothing here is Helm-templated. When
building the chart:

- **manifests.yaml** is HAND-MAINTAINED (controller-gen markers cannot express
  `matchConditions`); template it from this file, do not regenerate it.
- Keep `failurePolicy: Ignore` — it is mandated by the admission-validation
  spec (the webhook is advisory only; the controller re-checks policy
  authoritatively). Do NOT expose a chart value that switches it to `Fail`.
- Keep the CEL `matchConditions` (annotation-presence scoping) and
  `timeoutSeconds: 2`. matchConditions require Kubernetes >= 1.30.
- `clientConfig.service.path` must equal `internal/webhook.Path`
  (`/validate-replication-request`); the service targets the manager's webhook
  server port 9443 (cert flags already exist in cmd/main.go:
  `--webhook-cert-path`, `--webhook-cert-name`, `--webhook-cert-key`).
- Cert plumbing: `../certmanager` holds the optional cert-manager overlay
  (self-signed Issuer + Certificate `serving-cert`, secret
  `webhook-server-cert`). The `cert-manager.io/inject-ca-from` annotation on
  the ValidatingWebhookConfiguration uses the kubebuilder placeholder that
  config/default replacements fill; in the chart, make cert-manager optional
  (values switch) with a manual-caBundle fallback.
- The webhook registration in code is `webhook.Setup(mgr)`; central wiring in
  cmd/main.go happens in a later task group.
