## 1. Version negotiation in the CAPI provider

- [x] 1.1 Replace the pinned `clusterGVR` package var with `clusterGroupResource` plus an ordered `supportedClusterVersions = []string{"v1", "v1beta2", "v1beta1"}`
- [x] 1.2 Add `resolveClusterGVR(d discovery.DiscoveryInterface) (schema.GroupVersionResource, error)`: read served group-versions for `cluster.x-k8s.io`, keep those actually listing the `clusters` resource, return the first supported version in preference order
- [x] 1.3 Return a named error when the resource is served at no supported version: `capi: clusters.cluster.x-k8s.io serves none of [...] (served: [...])`; keep "resource absent" / "hub unreachable" retryable (sentinel errors) so a hub without ClusterAPI waits instead of crash-looping
- [x] 1.4 Build a discovery client next to the dynamic client in the registry factory; store it on `Provider` (`WithDiscovery` option for tests)
- [x] 1.5 Resolve inside `Start`, before the informer is created, so an unreachable hub retries through the manager instead of crash-looping the factory
- [x] 1.6 Log the negotiated version once at info level
- [x] 1.7 Bound `cache.WaitForCacheSync` with a timeout and report it in the error

## 2. Discovery-health metrics

- [x] 2.1 Add `k8s_r8r_discovery_up{provider}` and `k8s_r8r_discovery_clusters{provider}` collectors to `internal/telemetry/metrics.go`, injected via `SetDiscoverySnapshot` (same pattern as `SetClusterSnapshot`)
- [x] 2.2 Provider tracks its own watch state; `cmd/main.go` wires the snapshot from `provider.Name()` / `provider.List()` in the discovery region
- [x] 2.3 Add `provider` to the cardinality allowlist and the new families to the metric inventory test

## 3. Tests

- [x] 3.1 Unit-test `resolveClusterGVR` against a fake discovery client: v1beta1 only, v1beta1+v1beta2, all three, only unsupported alpha versions, group absent entirely, resource absent from a served group-version
- [x] 3.2 Unit-test that `Start` returns the named error (and never blocks) when no supported version is served, and that it waits (not fails) when the resource is absent
- [x] 3.3 Verify readiness stays version-tolerant: `controlPlaneReady` covers `ControlPlaneReady` and `ControlPlaneAvailable` on a v1beta2 object
- [x] 3.4 e2e: `test/e2e/testdata/capi-cluster-crd.yaml` serves `v1beta1` (deprecated) + `v1beta2` (served, storage); `test/e2e/framework.go` `clusterGVK` moves to `v1beta2`

## 4. Docs

- [x] 4.1 `internal/discovery/capi` package doc: replace the "cluster.x-k8s.io/v1beta1" statement of fact with the negotiated set
- [x] 4.2 `obsidian/cluster-discovery.md`: negotiation + fail-loud behavior
- [x] 4.3 `obsidian/operations.md`: discovery-health metrics and a "zero clusters discovered — check the negotiated CAPI version" troubleshooting entry
- [x] 4.4 `docs/quickstart.md`: same troubleshooting entry next to the discovery section

## 5. Verification

- [x] 5.1 `openspec validate negotiate-capi-cluster-version --strict`
- [x] 5.2 `make lint && make test`
- [x] 5.3 Add `TestCAPIVersionNegotiation` asserting the operator logged the negotiated `v1beta2`; `go vet -tags e2e ./test/e2e/...` compiles
- [ ] 5.4 **Not run:** `make test-e2e` — the Docker daemon was unavailable in this environment. The e2e suite compiles and the negotiation assertion is in place; it needs a run on a machine with docker before merge.
