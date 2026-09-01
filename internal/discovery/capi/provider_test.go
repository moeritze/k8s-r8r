package capi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discoveryfake "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/moeritze/k8s-r8r/internal/discovery"
)

// cluster builds an unstructured CAPI Cluster object at the version the
// provider negotiates in these tests (see testClusterGVR).
func cluster(ns, name string, labels map[string]string, conditions []any) *unstructured.Unstructured {
	return clusterAt("cluster.x-k8s.io/v1beta2", ns, name, labels, conditions)
}

// clusterAt builds an unstructured CAPI Cluster object at an explicit
// apiVersion.
func clusterAt(apiVersion, ns, name string, labels map[string]string, conditions []any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       "Cluster",
		"metadata": map[string]any{
			"namespace": ns,
			"name":      name,
		},
	}}
	if labels != nil {
		l := map[string]any{}
		for k, v := range labels {
			l[k] = v
		}
		_ = unstructured.SetNestedMap(u.Object, l, "metadata", "labels")
	}
	if conditions != nil {
		_ = unstructured.SetNestedSlice(u.Object, conditions, "status", "conditions")
	}
	return u
}

func condition(typ, status string) map[string]any {
	return map[string]any{"type": typ, "status": status}
}

func TestControlPlaneReady(t *testing.T) {
	tests := []struct {
		name       string
		conditions []any
		want       bool
	}{
		{name: "no status", conditions: nil, want: false},
		{name: "empty conditions", conditions: []any{}, want: false},
		{
			name:       "ControlPlaneReady true",
			conditions: []any{condition("ControlPlaneReady", "True")},
			want:       true,
		},
		{
			name:       "ControlPlaneReady false",
			conditions: []any{condition("ControlPlaneReady", "False")},
			want:       false,
		},
		{
			name:       "ControlPlaneReady unknown",
			conditions: []any{condition("ControlPlaneReady", "Unknown")},
			want:       false,
		},
		{
			name:       "v1beta2 ControlPlaneAvailable true",
			conditions: []any{condition("ControlPlaneAvailable", "True")},
			want:       true,
		},
		{
			name:       "unrelated condition true",
			conditions: []any{condition("InfrastructureReady", "True")},
			want:       false,
		},
		{
			name: "mixed, ready among others",
			conditions: []any{
				condition("InfrastructureReady", "True"),
				condition("ControlPlaneReady", "True"),
			},
			want: true,
		},
		{
			name:       "malformed condition entries ignored",
			conditions: []any{"junk", map[string]any{"type": int64(42)}, condition("ControlPlaneReady", "True")},
			want:       true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u := cluster("ns", "c", nil, tc.conditions)
			if got := controlPlaneReady(u); got != tc.want {
				t.Fatalf("controlPlaneReady = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRecordFromCluster(t *testing.T) {
	u := cluster("fleet", "prod-eu", map[string]string{"env": "prod"},
		[]any{condition("ControlPlaneReady", "True")})
	rec := recordFromCluster(u)
	if rec.Name != "prod-eu" {
		t.Fatalf("Name = %q", rec.Name)
	}
	if !rec.Ready {
		t.Fatal("Ready = false, want true")
	}
	if rec.Labels["env"] != "prod" {
		t.Fatalf("Labels = %v", rec.Labels)
	}
	want := discovery.CredentialRef{Namespace: "fleet", Name: "prod-eu-kubeconfig"}
	if rec.CredentialRef != want {
		t.Fatalf("CredentialRef = %v, want %v", rec.CredentialRef, want)
	}
}

// Negotiating the API version is only safe because record translation reads
// nothing version-bound: ObjectMeta plus status.conditions[].type/.status,
// which carry the same shape in the v1beta1 and v1beta2 condition sets.
func TestRecordFromClusterIsVersionIndependent(t *testing.T) {
	labels := map[string]string{"env": "prod"}
	beta1 := recordFromCluster(clusterAt("cluster.x-k8s.io/v1beta1", "fleet", "prod-eu", labels,
		[]any{condition("ControlPlaneReady", "True")}))
	beta2 := recordFromCluster(clusterAt("cluster.x-k8s.io/v1beta2", "fleet", "prod-eu", labels,
		[]any{condition("ControlPlaneAvailable", "True")}))

	if !recordsEqual(beta1, beta2) {
		t.Fatalf("record differs by API version:\n v1beta1 = %+v\n v1beta2 = %+v", beta1, beta2)
	}
	if !beta2.Ready {
		t.Error("v1beta2 ControlPlaneAvailable=True did not yield Ready")
	}
}

// fakeDiscovery builds a discovery client serving the given
// cluster.x-k8s.io versions for the "clusters" resource. Versions listed in
// otherResourceOnly are served by the group but do not carry the clusters
// resource.
func fakeDiscovery(versions []string, otherResourceOnly ...string) *discoveryfake.FakeDiscovery {
	lists := make([]*metav1.APIResourceList, 0, len(versions)+len(otherResourceOnly))
	for _, v := range versions {
		lists = append(lists, &metav1.APIResourceList{
			GroupVersion: "cluster.x-k8s.io/" + v,
			APIResources: []metav1.APIResource{
				{Name: "machines", Namespaced: true, Kind: "Machine"},
				{Name: "clusters", Namespaced: true, Kind: "Cluster"},
			},
		})
	}
	for _, v := range otherResourceOnly {
		lists = append(lists, &metav1.APIResourceList{
			GroupVersion: "cluster.x-k8s.io/" + v,
			APIResources: []metav1.APIResource{
				{Name: "machines", Namespaced: true, Kind: "Machine"},
			},
		})
	}
	return &discoveryfake.FakeDiscovery{Fake: &clienttesting.Fake{Resources: lists}}
}

// testClusterGVR is the GVR the fake dynamic client is registered for in
// these tests: the negotiated version for a hub serving v1beta1 + v1beta2.
var testClusterGVR = clusterGroupResource.WithVersion("v1beta2")

// startTestProvider runs a provider against a fake dynamic client and
// returns the provider plus a channel of emitted events. The fake hub serves
// v1beta1 and v1beta2, so Start must negotiate v1beta2.
func startTestProvider(t *testing.T, objs ...runtime.Object) (*Provider, chan discovery.Event, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{testClusterGVR: "ClusterList"}, objs...)
	p := New(dyn,
		WithDiscovery(fakeDiscovery([]string{"v1beta1", "v1beta2"})),
		WithResync(0))
	events := make(chan discovery.Event, 32)
	p.Subscribe(func(e discovery.Event) { events <- e })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = p.Start(ctx) }()
	return p, events, dyn
}

func waitEvent(t *testing.T, events chan discovery.Event) discovery.Event {
	t.Helper()
	select {
	case e := <-events:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for discovery event")
		return discovery.Event{}
	}
}

func TestProviderLifecycle(t *testing.T) {
	initial := cluster("fleet", "alpha", map[string]string{"env": "prod"},
		[]any{condition("ControlPlaneReady", "True")})
	p, events, dyn := startTestProvider(t, initial)

	// Initial listing emits Register for the pre-existing cluster.
	e := waitEvent(t, events)
	if e.Type != discovery.EventRegister || e.Record.Name != "alpha" || !e.Record.Ready {
		t.Fatalf("initial event = %+v", e)
	}

	// New cluster appears, not yet ready.
	beta := cluster("fleet", "beta", nil, []any{condition("ControlPlaneReady", "False")})
	if _, err := dyn.Resource(testClusterGVR).Namespace("fleet").Create(context.Background(), beta, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	e = waitEvent(t, events)
	if e.Type != discovery.EventRegister || e.Record.Name != "beta" || e.Record.Ready {
		t.Fatalf("beta register event = %+v", e)
	}

	// beta becomes ready -> Update with Ready=true.
	betaReady := cluster("fleet", "beta", nil, []any{condition("ControlPlaneReady", "True")})
	if _, err := dyn.Resource(testClusterGVR).Namespace("fleet").Update(context.Background(), betaReady, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update beta: %v", err)
	}
	e = waitEvent(t, events)
	if e.Type != discovery.EventUpdate || e.Record.Name != "beta" || !e.Record.Ready {
		t.Fatalf("beta update event = %+v", e)
	}

	// Deletion -> Deregister.
	if err := dyn.Resource(testClusterGVR).Namespace("fleet").Delete(context.Background(), "alpha", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete alpha: %v", err)
	}
	e = waitEvent(t, events)
	if e.Type != discovery.EventDeregister || e.Record.Name != "alpha" {
		t.Fatalf("alpha deregister event = %+v", e)
	}
	if e.Record.Ready {
		t.Fatal("deregister event marked Ready")
	}

	// List reflects only beta now.
	deadline := time.Now().Add(5 * time.Second)
	for {
		records := p.List()
		if len(records) == 1 && records[0].Name == "beta" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("List = %+v, want only beta", records)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestProviderNoEventOnNoChange(t *testing.T) {
	initial := cluster("fleet", "alpha", nil, []any{condition("ControlPlaneReady", "True")})
	_, events, dyn := startTestProvider(t, initial)
	waitEvent(t, events) // initial Register

	// Re-update with identical content: no event expected.
	same := cluster("fleet", "alpha", nil, []any{condition("ControlPlaneReady", "True")})
	if _, err := dyn.Resource(testClusterGVR).Namespace("fleet").Update(context.Background(), same, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update: %v", err)
	}
	select {
	case e := <-events:
		t.Fatalf("unexpected event %+v", e)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestProviderRegisteredInDefaultRegistry(t *testing.T) {
	found := false
	for _, n := range discovery.Default.Names() {
		if n == ProviderName {
			found = true
		}
	}
	if !found {
		t.Fatalf("provider %q not in default registry: %v", ProviderName, discovery.Default.Names())
	}
	// Factory validates missing hub config.
	if _, err := discovery.New(ProviderName, discovery.Options{}); err == nil {
		t.Fatal("New without HubConfig succeeded, want error")
	}
}

// The preference list is what the provider is validated against; the
// negotiation contract is only meaningful relative to it.
func TestSupportedClusterVersionsOrder(t *testing.T) {
	want := []string{"v1", "v1beta2", "v1beta1"}
	if len(supportedClusterVersions) != len(want) {
		t.Fatalf("supportedClusterVersions = %v, want %v", supportedClusterVersions, want)
	}
	for i, v := range want {
		if supportedClusterVersions[i] != v {
			t.Fatalf("supportedClusterVersions = %v, want %v (most preferred first)", supportedClusterVersions, want)
		}
	}
}

func TestResolveClusterGVR(t *testing.T) {
	tests := []struct {
		name string
		// served versions carrying the "clusters" resource.
		served []string
		// versions the group serves without the "clusters" resource.
		otherOnly []string
		want      string
		// wantErr is the sentinel the error must wrap (nil = success).
		wantErr     error
		errContains []string
	}{
		{
			name:   "only the legacy version (CAPI <= 1.10 style)",
			served: []string{"v1beta1"},
			want:   "v1beta1",
		},
		{
			name:   "deprecated and current served (CAPI 1.11 style)",
			served: []string{"v1beta1", "v1beta2"},
			want:   "v1beta2",
		},
		{
			name:   "v1beta1 unserved (CAPI 1.16 style) still negotiates",
			served: []string{"v1beta2"},
			want:   "v1beta2",
		},
		{
			name:   "GA wins over everything",
			served: []string{"v1beta1", "v1beta2", "v1"},
			want:   "v1",
		},
		{
			name:   "unknown newer version ignored in favor of a supported one",
			served: []string{"v1beta2", "v2beta1"},
			want:   "v1beta2",
		},
		{
			name:        "only unsupported versions served is fatal",
			served:      []string{"v1alpha3", "v1alpha4"},
			wantErr:     errUnsupportedVersions,
			errContains: []string{"clusters.cluster.x-k8s.io", "v1beta1", "v1alpha3"},
		},
		{
			name:        "group absent entirely is retryable, not fatal",
			served:      nil,
			wantErr:     errClusterResourceAbsent,
			errContains: []string{"clusters.cluster.x-k8s.io"},
		},
		{
			name:        "group present but resource missing from every version",
			served:      nil,
			otherOnly:   []string{"v1beta1", "v1beta2"},
			wantErr:     errClusterResourceAbsent,
			errContains: []string{"clusters.cluster.x-k8s.io"},
		},
		{
			name:      "resource served on only one of the group's versions",
			served:    []string{"v1beta1"},
			otherOnly: []string{"v1beta2"},
			want:      "v1beta1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gvr, err := resolveClusterGVR(fakeDiscovery(tc.served, tc.otherOnly...))
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("resolveClusterGVR = %v, want error", gvr)
				}
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error %q does not wrap %v", err, tc.wantErr)
				}
				for _, want := range tc.errContains {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not name %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveClusterGVR: %v", err)
			}
			want := schema.GroupVersionResource{Group: "cluster.x-k8s.io", Version: tc.want, Resource: "clusters"}
			if gvr != want {
				t.Fatalf("resolveClusterGVR = %v, want %v", gvr, want)
			}
		})
	}
}

func TestResolveClusterGVRNoDiscoveryClient(t *testing.T) {
	if _, err := resolveClusterGVR(nil); err == nil {
		t.Fatal("resolveClusterGVR(nil) succeeded, want error")
	}
}

// The whole point of #28: an unserved version must stop the provider with a
// named error instead of leaving it wedged in WaitForCacheSync while
// reporting an empty fleet.
func TestStartFailsLoudlyWhenNoSupportedVersionIsServed(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{testClusterGVR: "ClusterList"})
	p := New(dyn,
		WithDiscovery(fakeDiscovery([]string{"v1alpha4"})),
		WithResync(0))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- p.Start(ctx) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Start succeeded, want error naming the unserved versions")
		}
		for _, want := range []string{"clusters.cluster.x-k8s.io", "v1beta1", "v1alpha4"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Start error %q does not name %q", err, want)
			}
		}
	case <-ctx.Done():
		t.Fatal("Start blocked instead of returning an error (issue #28: the silent wedge)")
	}

	if p.Watching() {
		t.Error("Watching() = true after a failed start")
	}
	if len(p.List()) != 0 {
		t.Error("List() non-empty after a failed start")
	}
}

// A hub without ClusterAPI at all is a different case from a version skew:
// the CRD may be installed later (the documented kind quickstart runs
// exactly this way), so the provider waits instead of taking the manager
// down — while reporting itself as not watching so the outage is still
// visible in k8s_r8r_discovery_up.
func TestStartWaitsWhenClusterResourceIsAbsent(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{testClusterGVR: "ClusterList"})
	p := New(dyn, WithDiscovery(fakeDiscovery(nil)), WithResync(0))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Start(ctx) }()

	// Still waiting, not failed.
	select {
	case err := <-done:
		cancel()
		t.Fatalf("Start returned %v, want it to keep retrying", err)
	case <-time.After(300 * time.Millisecond):
	}
	if p.Watching() {
		t.Error("Watching() = true while the Cluster resource is absent")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start after cancel = %v, want nil (shutdown is not a failure)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after cancel")
	}
}

// Watching() is the signal that separates "discovery is broken" from "the
// fleet is empty" — both of which show zero clusters.
func TestWatchingReportsProviderHealth(t *testing.T) {
	p, _, _ := startTestProvider(t) // no Cluster objects: a genuinely empty fleet
	deadline := time.Now().Add(5 * time.Second)
	for !p.Watching() {
		if time.Now().After(deadline) {
			t.Fatal("Watching() never became true with a healthy informer")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(p.List()) != 0 {
		t.Fatalf("List = %v, want empty", p.List())
	}
}
