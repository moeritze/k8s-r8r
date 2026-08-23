package capi

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/moeritze/k8s-r8r/internal/discovery"
)

// cluster builds an unstructured CAPI Cluster object.
func cluster(ns, name string, labels map[string]string, conditions []any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cluster.x-k8s.io/v1beta1",
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

// startTestProvider runs a provider against a fake dynamic client and
// returns the provider plus a channel of emitted events.
func startTestProvider(t *testing.T, objs ...runtime.Object) (*Provider, chan discovery.Event, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{clusterGVR: "ClusterList"}, objs...)
	p := New(dyn, WithResync(0))
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
	if _, err := dyn.Resource(clusterGVR).Namespace("fleet").Create(context.Background(), beta, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	e = waitEvent(t, events)
	if e.Type != discovery.EventRegister || e.Record.Name != "beta" || e.Record.Ready {
		t.Fatalf("beta register event = %+v", e)
	}

	// beta becomes ready -> Update with Ready=true.
	betaReady := cluster("fleet", "beta", nil, []any{condition("ControlPlaneReady", "True")})
	if _, err := dyn.Resource(clusterGVR).Namespace("fleet").Update(context.Background(), betaReady, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update beta: %v", err)
	}
	e = waitEvent(t, events)
	if e.Type != discovery.EventUpdate || e.Record.Name != "beta" || !e.Record.Ready {
		t.Fatalf("beta update event = %+v", e)
	}

	// Deletion -> Deregister.
	if err := dyn.Resource(clusterGVR).Namespace("fleet").Delete(context.Background(), "alpha", metav1.DeleteOptions{}); err != nil {
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
	if _, err := dyn.Resource(clusterGVR).Namespace("fleet").Update(context.Background(), same, metav1.UpdateOptions{}); err != nil {
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
