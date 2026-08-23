## 1. Project Scaffold & CI

- [x] 1.1 Initialize Go module + kubebuilder project (domain `r8r.io`, repo layout, license, README stub); verify `r8r.io` group naming decision before first release tag
- [x] 1.2 CI pipeline: lint (golangci-lint), unit tests, envtest, build; kind-based e2e job skeleton (hub + one spoke via kind clusters)
- [x] 1.3 Makefile/dev tooling: local kind fleet bootstrap script for manual testing

## 2. CRD APIs

- [x] 2.1 Define `Replication` v1alpha1 type: spec (source ref, origin kind, resolved targets), status (summary counts, capped non-ready detail list, source hash, conditions), inventory field
- [x] 2.2 Define `ReplicationPolicy` v1alpha1 type (cluster-scoped): source namespaces/kinds, target clusterSelector/namespaces, options (allowNamespaceCreation, allowed conflictPolicies, revocationPolicy)
- [x] 2.3 Generate CRDs, deepcopy, clients; RBAC manifests for personas (admin-only policy write, user read-only Replication)

## 3. Policy Engine

- [x] 3.1 Implement policy evaluation library: default deny, all-dimensions-match-single-policy, union across policies; pure functions with table-driven tests
- [x] 3.2 Implement option resolution (effective conflictPolicy, namespace creation, revocationPolicy) when multiple policies permit a target
- [x] 3.3 Policy revocation semantics: detect permission withdrawal on reconcile, mark `PolicyRevoked`, act per revocationPolicy

## 4. Request Controller (annotation shim)

- [x] 4.1 Watch allowlisted kinds (Secret, ConfigMap) for `r8r.io/` annotations; parse/validate annotation contract
- [x] 4.2 Materialize/update/delete operator-owned `Replication` objects; origin field; reject hand-authored ones with `NotAuthoritative`
- [x] 4.3 Finalizer management on sources; annotation-removal cleanup path
- [x] 4.4 Kind-allowlist gate with event on non-allowlisted annotated kinds

## 5. Discovery Layer

- [x] 5.1 Define `Discovery` provider interface (cluster records: name, labels, readiness, credential ref; register/deregister events) + provider registry/config
- [x] 5.2 Implement ClusterAPI provider: watch `Cluster` objects, readiness gating (control plane ready), label extraction, kubeconfig Secret reference
- [x] 5.3 Spoke credential bootstrap: one-shot namespace + ServiceAccount + narrow RBAC creation via CAPI kubeconfig; short-lived token acquisition + rotation; RBAC re-narrowing on policy-universe shrink
- [x] 5.4 Cluster runtime manager: start/stop one runtime per ready cluster, connectivity state tracking with backoff

## 6. Replication Engine

- [x] 6.1 Unstructured fanout pipeline: payload extraction, server-managed/immutable field stripping, managed-by + source-ref labels, source-hash annotation
- [x] 6.2 Transport interface + push implementation (SA-token clients from cluster runtimes)
- [x] 6.3 Workqueue keyed `(source, targetCluster)`; per-target backoff independent of other targets
- [x] 6.4 Conflict handling: Fail (default, condition), Overwrite (policy+request gated, incl. immutable-field delete+recreate), Adopt (hash-equality only)
- [x] 6.5 Namespace ensure (policy-gated, labeled, never GC'd by engine)
- [x] 6.6 Inventory bookkeeping + GC paths: source deleted (finalizer-blocked cleanup), annotation removed, target left selection, cluster deregistered; unreachable-cluster retry + `ClusterGone` release
- [x] 6.7 Drift detection: per-cluster metadata-only filtered informers per replicated GVK, hash-mismatch/deletion enqueue, periodic resync fallback

## 7. Admission Webhook

- [x] 7.1 Validating webhook server: evaluate annotations against policies, dimension-naming error messages, malformed-annotation rejection
- [x] 7.2 Webhook configuration: CEL matchConditions (annotation presence), failurePolicy Ignore, cert management (chart-integrated)

## 8. Observability & HA

- [x] 8.1 Prometheus metrics per observability spec (bounded label cardinality); connectivity + workqueue metrics
- [x] 8.2 Rate-limited events on sources and Replications; secret-safe telemetry audit (hashes only, lint/review checklist)
- [x] 8.3 Leader election, health/readiness probes (spoke outage must not flip readiness); status-write skipping when unchanged

## 9. Packaging & Docs

- [x] 9.1 Helm chart: CRDs, operator, webhook, RBAC personas, values for discovery provider config and kind allowlist
- [x] 9.2 Documentation: quickstart (kind fleet), annotation reference, policy authoring guide, GitOps/ArgoCD integration notes (ignore-labels, adoption story)
- [x] 9.3 Security documentation: threat model (push architecture, hub blast radius, D5/D6 reasoning), advisory-webhook doctrine, minimum supported Kubernetes version
- [x] 9.4 Uninstall/teardown docs incl. finalizer escape hatch and replica cleanup pre-step

## 10. End-to-End Validation

- [x] 10.1 e2e: multi-kind-cluster suite — annotate → replicate → drift-repair → revoke → GC, covering conflict modes and namespace ensure
- [x] 10.2 e2e: cluster lifecycle — register (bootstrap SA), deregister (runtime stop + inventory release), unreachable-cluster behavior
- [x] 10.3 Scale sanity check: many-target fanout status stays under size cap; no status churn when healthy
