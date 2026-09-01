# Design: CAPI Cluster version negotiation

## Context

`internal/discovery/capi` reads CAPI `Cluster` objects unstructured through a
dynamic informer, deliberately avoiding a `sigs.k8s.io/cluster-api` import.
That choice is what makes version negotiation cheap: there are no generated
Go types bound to one API version, only a `GroupVersionResource` and two
field paths (`metadata.labels`, `status.conditions[]`). The pinned version
was the only versioned thing left.

## Decisions

### D1: Explicit preference list, not the group's preferred version

`resolveClusterGVR` intersects the versions the API server serves for
`clusters.cluster.x-k8s.io` with an ordered, in-code list:

```go
var supportedClusterVersions = []string{"v1", "v1beta2", "v1beta1"}
```

and returns the first list entry that is served. Rejected alternative: take
the group's `preferredVersion` from `/apis`. That auto-adopts any future
version the moment a management cluster starts serving it, including one
whose readiness-condition vocabulary this provider has never been validated
against — the provider would silently report every cluster as not-ready
instead of not discovering them, trading one silent failure for another. The
explicit list makes "which CAPI versions is this build validated for?" a
readable constant and a reviewable diff.

`v1` leads the list because CAPI's own deprecation notes point at a future
GA; it costs one string and means the next bump is a one-line change with a
test, not an incident.

### D2: Resolve in `Start`, fail loudly, before the informer

Resolution happens inside `Start`, not in the registry factory, and not in
`init`. The factory runs during process startup, before the manager's
lifecycle exists; a hub that is briefly unreachable there would crash-loop
the operator for a transient condition. Inside `Start` the provider is a
manager `Runnable` and failure is handled by the manager's normal shutdown.

Two negotiation failures are separated, because they need opposite
responses:

| condition | response | why |
|---|---|---|
| resource served, no supported version | fatal — `Start` returns | structural skew between this build and the hub; it will not resolve itself |
| resource absent, or hub unreachable | retry every 30s, `Watching() == false`, reason logged | ClusterAPI may be installed later; the documented kind quickstart runs a hub with none |

Collapsing the second case into the first would crash-loop the operator on
the fleet the quickstart tells people to build, and would make the e2e
suite's install-then-apply-CRD order fail — while buying nothing, since the
condition is visible in `k8s_r8r_discovery_up` either way. Neither case is
silent, which is the actual bug.

When the resource is served at no known version, `Start` returns before
creating the informer:

```
capi: clusters.cluster.x-k8s.io serves none of [v1 v1beta2 v1beta1] (served: [v1alpha3 v1alpha4])
```

The error names the group/resource, the versions this build accepts, and the
versions the server actually serves — everything needed to diagnose it from
one log line.

A non-nil return from a `Runnable` stops the manager, so the pod restarts
and the message is in its logs. This does **not** conflict with the
`observability-operations` requirement that "individual unreachable target
clusters SHALL NOT make the operator unready": that requirement is about
*spokes*, and the isolation it demands is provided by per-cluster runtimes
and per-cluster connectivity state. Discovery serving no known version is
not one spoke being down — it is the inventory source being structurally
unusable, where every replication target is unreachable and staying up
buys nothing. Readiness itself is untouched (still hub-informers-synced
only); the pod does not go "not ready", it exits.

### D3: Bounded cache sync

`cache.WaitForCacheSync(ctx.Done(), ...)` waits forever on a reflector that
retries a retryable error forever. It is now bounded by a derived context
(`cacheSyncTimeout`, 2m — the same order as controller-runtime's own cache
sync timeout), so a never-syncing informer produces
`capi: cluster informer cache never synced within 2m0s` instead of a silent
hang. Negotiation removes the known cause of that hang; the timeout covers
the unknown ones (RBAC on `clusters`, a wedged aggregated API).

### D4: Discovery-health metrics, separate from the runtime count

`k8s_r8r_clusters` is derived from the cluster *runtime manager*, three
layers downstream of discovery, and reads 0 for both "discovery is broken"
and "no clusters match". Two new gauges break the ambiguity at the source:

| metric | source | 0 means |
|---|---|---|
| `k8s_r8r_discovery_up{provider}` | provider watch established | discovery is not running |
| `k8s_r8r_discovery_clusters{provider}` | provider `List()` | discovery works, the fleet is empty |

`up=1, clusters=0` is a genuinely empty fleet. `up=0` is a broken provider —
and with the fail-loud path above, a pod that is restarting with a named
error rather than sitting green. The alert worth writing is
`k8s_r8r_discovery_up == 0`.

`provider` is a bounded label: its value space is the discovery registry's
provider names (one value in any given process). It is added to the
cardinality allowlist in `internal/telemetry/metrics_test.go` alongside the
other closed enumerations.

Wiring follows the existing `SetClusterSnapshot` pattern: `cmd/main.go`
injects a snapshot function so `internal/telemetry` keeps no dependency on
`internal/discovery`. The gauges are collected at scrape time.

### D5: Ready conditions are already version-tolerant (verified, not assumed)

`readyConditionTypes` accepts `ControlPlaneReady` (v1beta1) or
`ControlPlaneAvailable` (the v1beta2 condition set), and `controlPlaneReady`
walks `status.conditions[]` reading only `type` and `status` as strings via
`unstructured.NestedSlice`. Both the v1beta1 (`clusterv1.Condition`) and
v1beta2 (`metav1.Condition`) condition shapes carry `type` and `status` as
strings at that path, so nothing in the readiness path is version-bound.
`recordFromCluster` reads only `metadata.name`, `metadata.namespace` and
`metadata.labels`, which are `ObjectMeta` and therefore version-invariant.
Negotiating the version does not require touching the record translation.

### D6: e2e serves two versions

`test/e2e/testdata/capi-cluster-crd.yaml` served only `v1beta1`, so a
negotiating provider would pick `v1beta1` and the new code path would never
be exercised. The CRD now declares `v1beta2` (served, storage) and
`v1beta1` (served, not storage) — the shape of a CAPI 1.11+ management
cluster. The provider must then negotiate `v1beta2`, and the suite's own
`clusterGVK` moves to `v1beta2` to match the storage version. Both versions
use `x-kubernetes-preserve-unknown-fields` with the default `None`
conversion strategy, so no conversion webhook is needed.

## Risks

- **A CAPI release adds a version we do not list.** The provider keeps using
  the newest *listed* served version; nothing breaks, and the negotiated
  version is in the logs. Adding the new version is a one-line change once
  its condition vocabulary is checked.
- **A partially-broken discovery API.** `resolveClusterGVR` queries
  `/apis` and then only the candidate group-versions, so an unrelated
  aggregated APIService being down cannot fail CAPI discovery.
- **Restart loop on a truly misconfigured hub.** Intended: an operator that
  cannot see its inventory source is not doing useful work, and
  `CrashLoopBackOff` with a named error is a signal, whereas green-and-idle
  is not.
