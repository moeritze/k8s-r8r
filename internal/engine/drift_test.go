/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var secretGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"}

// fakeInformer records handler registrations; the embedded interface covers
// the methods the detector never calls.
type fakeInformer struct {
	cache.Informer
	mu       sync.Mutex
	handlers []toolscache.ResourceEventHandler
	resyncs  []time.Duration
}

func (f *fakeInformer) AddEventHandlerWithResyncPeriod(h toolscache.ResourceEventHandler, resync time.Duration) (toolscache.ResourceEventHandlerRegistration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers = append(f.handlers, h)
	f.resyncs = append(f.resyncs, resync)
	return nil, nil
}

func (f *fakeInformer) handlerCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.handlers)
}

// fakeCache hands out fakeInformers per GVK; the embedded interface covers
// everything else.
type fakeCache struct {
	cache.Cache
	mu        sync.Mutex
	informers map[schema.GroupVersionKind]*fakeInformer
}

func newFakeCache() *fakeCache {
	return &fakeCache{informers: map[schema.GroupVersionKind]*fakeInformer{}}
}

func (f *fakeCache) GetInformer(_ context.Context, obj client.Object, _ ...cache.InformerGetOption) (cache.Informer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	gvk := obj.GetObjectKind().GroupVersionKind()
	if _, ok := obj.(*metav1.PartialObjectMetadata); !ok {
		panic("drift detector must request metadata-only informers")
	}
	inf, ok := f.informers[gvk]
	if !ok {
		inf = &fakeInformer{}
		f.informers[gvk] = inf
	}
	return inf, nil
}

func (f *fakeCache) informer(gvk schema.GroupVersionKind) *fakeInformer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.informers[gvk]
}

// startDetector runs the detector's Start loop and waits until it is live.
func startDetector(t *testing.T, d *DriftDetector) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = d.Start(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		d.mu.Lock()
		live := d.ctx != nil
		d.mu.Unlock()
		if live {
			return cancel
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("drift detector did not start")
		}
		time.Sleep(time.Millisecond)
	}
}

func managedReplicaMeta(uid string) *metav1.PartialObjectMetadata {
	pom := &metav1.PartialObjectMetadata{}
	pom.SetGroupVersionKind(secretGVK)
	pom.SetName("web-creds")
	pom.SetNamespace("app")
	pom.SetLabels(map[string]string{
		LabelManagedBy:       ManagedByValue,
		LabelSourceUID:       uid,
		LabelSourceNamespace: "hub-ns",
	})
	return pom
}

// Spec "Drift detection via metadata watches": events on managed replicas
// enqueue the owning Replication (edit scenario: any update event —
// including one that only changes the payload and therefore only the
// resourceVersion — reaches the reconciler, which restores the replica);
// deletion events do the same so replicas are recreated. Unmanaged objects
// never enqueue.
func TestDrift_HandlerEnqueuesOwningReplication(t *testing.T) {
	lookupCalls := 0
	d := NewDriftDetector(func(_ context.Context, uid, ns string) []client.ObjectKey {
		lookupCalls++
		if uid != "src-uid-1" || ns != "hub-ns" {
			t.Errorf("lookup(%q, %q): wrong source ref", uid, ns)
		}
		return []client.ObjectKey{{Namespace: "hub-ns", Name: "rep-1"}}
	}, time.Hour)

	fc := newFakeCache()
	d.ClusterReady("spoke-a", fc)
	d.EnsureWatch("spoke-a", secretGVK) // before Start: recorded, not installed
	if fc.informer(secretGVK) != nil {
		t.Fatal("informer installed before Start")
	}

	cancel := startDetector(t, d)
	defer cancel()

	deadline := time.Now().Add(2 * time.Second)
	for fc.informer(secretGVK) == nil || fc.informer(secretGVK).handlerCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("informer not installed after Start")
		}
		time.Sleep(time.Millisecond)
	}
	inf := fc.informer(secretGVK)
	if inf.resyncs[0] != time.Hour {
		t.Errorf("resync period = %v, want 1h", inf.resyncs[0])
	}
	h := inf.handlers[0]

	expectEvent := func(step string) {
		t.Helper()
		select {
		case ev := <-d.Events():
			key := client.ObjectKeyFromObject(ev.Object)
			if key != (client.ObjectKey{Namespace: "hub-ns", Name: "rep-1"}) {
				t.Errorf("%s: enqueued %v", step, key)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: no event enqueued", step)
		}
	}

	// Update on a managed replica (covers payload-only edits: those still
	// produce update events).
	h.OnUpdate(nil, managedReplicaMeta("src-uid-1"))
	expectEvent("update")

	// Deletion, including the tombstone form.
	h.OnDelete(managedReplicaMeta("src-uid-1"))
	expectEvent("delete")
	h.OnDelete(toolscache.DeletedFinalStateUnknown{Obj: managedReplicaMeta("src-uid-1")})
	expectEvent("tombstone delete")

	// Add events (resync fallback path) enqueue too.
	h.OnAdd(managedReplicaMeta("src-uid-1"), false)
	expectEvent("add")

	// Unmanaged objects and objects without source-ref labels are ignored.
	unmanaged := managedReplicaMeta("src-uid-1")
	unmanaged.SetLabels(map[string]string{"app": "whatever"})
	h.OnUpdate(nil, unmanaged)
	incomplete := managedReplicaMeta("")
	h.OnUpdate(nil, incomplete)
	select {
	case <-d.Events():
		t.Fatal("unmanaged/incomplete object produced an enqueue")
	case <-time.After(50 * time.Millisecond):
	}
}

// Informer lifecycle: one informer per (cluster, GVK); EnsureWatch is
// idempotent; clusters becoming ready after Start get informers for all
// known GVKs; ClusterGone forgets the cluster.
func TestDrift_InformerLifecycle(t *testing.T) {
	d := NewDriftDetector(func(context.Context, string, string) []client.ObjectKey { return nil }, 0)
	cancel := startDetector(t, d)
	defer cancel()

	fcA := newFakeCache()
	d.ClusterReady("spoke-a", fcA)
	d.EnsureWatch("spoke-a", secretGVK)
	d.EnsureWatch("spoke-a", secretGVK)
	if got := fcA.informer(secretGVK).handlerCount(); got != 1 {
		t.Fatalf("handlers on spoke-a = %d, want 1 (EnsureWatch must be idempotent)", got)
	}

	// A cluster that becomes ready later gets every known GVK immediately.
	fcB := newFakeCache()
	d.ClusterReady("spoke-b", fcB)
	if fcB.informer(secretGVK) == nil || fcB.informer(secretGVK).handlerCount() != 1 {
		t.Fatal("late-ready cluster did not receive informers for known GVKs")
	}

	d.ClusterGone("spoke-b")
	d.mu.Lock()
	_, stillThere := d.clusters["spoke-b"]
	d.mu.Unlock()
	if stillThere {
		t.Fatal("ClusterGone did not forget the cluster")
	}

	// Default resync applies when 0 was passed.
	if fcA.informer(secretGVK).resyncs[0] != defaultDriftResync {
		t.Errorf("resync = %v, want default %v", fcA.informer(secretGVK).resyncs[0], defaultDriftResync)
	}
}
