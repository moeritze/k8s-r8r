# Negotiate the served CAPI Cluster version

## Why

The ClusterAPI discovery provider hardcodes `cluster.x-k8s.io/v1beta1`
(`internal/discovery/capi/provider.go`, package-level `clusterGVR`). CAPI has
moved its storage version to `v1beta2` and has scheduled `v1beta1` to stop
being served in CAPI 1.16. When that happens the failure is total and silent:

- the dynamic informer's list/watch against the unserved version gets `404`,
  which the reflector treats as retryable and retries forever, so
  `HasSynced` never flips and `Start` blocks inside `cache.WaitForCacheSync`
  until context cancellation — the `cluster informer cache never synced`
  error only surfaces at *shutdown*;
- readiness is hub-cache-only by design, so the pod stays `Ready`;
- `k8s_r8r_clusters` reads `0` from the cluster **runtime manager** snapshot,
  which is indistinguishable from a genuinely empty fleet;
- annotated Secrets still materialize `Replication`s, with zero targets and
  no denial event.

Green pod, green probes, zero replication, no operator-visible signal, on a
scheduled upstream trigger.

Two spec observations, both of which explain how this slipped through:

1. **`cluster-discovery` is silent on versioning.** The "ClusterAPI provider"
   requirement says the provider discovers targets from ClusterAPI `Cluster`
   objects but says nothing about which API version. Today's pinned-GVR code
   is therefore *not strictly non-conformant* — this change tightens the
   requirement rather than fixing a violation of it.
2. **`observability-operations`' metrics minimum is satisfied by the very
   metric that cannot see the failure.** It requires "cluster runtime count",
   and `k8s_r8r_clusters` delivers exactly that — a count derived from the
   runtime manager, downstream of discovery. Structurally broken discovery
   and an empty fleet produce the identical observation, so the required
   metric inventory was complete while the failure stayed invisible. The
   minimum is amended to include a discovery-health signal.

## What Changes

- **Version negotiation (behavior change).** The provider resolves the
  `clusters.cluster.x-k8s.io` version from the hub's discovery API at
  `Start`, intersecting the versions the API server actually serves with an
  explicit, ordered preference list `[v1, v1beta2, v1beta1]`, and uses the
  first hit. An explicit list is preferred over the group's
  `preferredVersion` so the ready-condition contract stays auditable: the
  provider never binds to a CAPI version it has not been validated against.
- **Loud failure on version skew.** When the resource is served but at none
  of the known versions, `Start` returns a named error before touching the
  informer — e.g. `capi: clusters.cluster.x-k8s.io serves none of
  [v1 v1beta2 v1beta1] (served: [v1alpha3 v1alpha4])`. As a manager
  `Runnable`, a non-nil return stops the manager and restarts the pod with
  the message in its logs, which is the correct response to a total,
  non-transient misconfiguration.
- **Retry, not failure, when ClusterAPI is simply absent.** A hub that is
  unreachable, or that has no `clusters.cluster.x-k8s.io` at all, is a
  different condition: both resolve themselves (an API server recovers,
  ClusterAPI gets installed), and the documented kind quickstart deliberately
  runs a hub with no ClusterAPI. The provider keeps retrying, logs why, and
  reports itself as not watching — so the state is visible in
  `k8s_r8r_discovery_up` instead of taking the operator down.
- **Resolution at `Start`, not construction.** A hub that is unreachable at
  process start must retry through the manager's normal lifecycle rather
  than crash-loop the factory.
- **Negotiated version logged once** at info level on success, so future
  skew is visible before it becomes an outage.
- **Bounded cache sync.** `cache.WaitForCacheSync` gets a timeout so a
  never-syncing informer reports instead of hanging forever.
- **Discovery-health metrics.** New `k8s_r8r_discovery_up{provider}` (1 when
  the provider's watch is established, 0 otherwise) and
  `k8s_r8r_discovery_clusters{provider}` (inventory size from the provider's
  own `List()`, distinct from the runtime-manager-derived
  `k8s_r8r_clusters`). `provider` is a bounded label — the registry's
  provider names.
- **e2e exercises negotiation.** `test/e2e/testdata/capi-cluster-crd.yaml`
  serves only `v1beta1` today, so negotiation would be a no-op there. It now
  serves `v1beta1` (deprecated) and `v1beta2` (served + storage), matching
  the shape of a real CAPI 1.11+ management cluster.

Not in scope: making the version selectable by configuration. A flag does
not fix the silent half at all — set it wrong and you get the same wedged
informer and the same green dashboard — and it moves a fact the API server
already publishes into human-maintained config.

## Impact

- Specs: `cluster-discovery` (ClusterAPI provider requirement + new
  scenario), `observability-operations` (metrics minimum).
- Code: `internal/discovery/capi/provider.go`,
  `internal/telemetry/metrics.go`, `cmd/main.go` (discovery wiring only).
- Tests: `internal/discovery/capi/provider_test.go`,
  `internal/telemetry/metrics_test.go`, `test/e2e/`.
- Docs: `obsidian/cluster-discovery.md`, `obsidian/operations.md`,
  `docs/quickstart.md`.
- Fixes #28. Related: #37 (`discovery.Options.Settings` is never populated in
  production) is deliberately left open — negotiation removes the need for a
  manual escape hatch here.
