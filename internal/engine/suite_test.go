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

// Test support for the reconciler tests: an envtest-backed hub API server
// (real CRDs, real status subresource, real finalizer semantics) plus a fake
// Transport standing in for spoke clusters, recording every apply/delete so
// GC, conflict, and revocation paths are fully observable.

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
	"github.com/moeritze/k8s-r8r/internal/discovery"
)

var (
	hubClient  client.Client
	testScheme *runtime.Scheme
	testCtx    = context.Background()
)

func TestMain(m *testing.M) {
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	if dir := firstEnvtestBinaryDir(); dir != "" {
		testEnv.BinaryAssetsDirectory = dir
	}
	cfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "starting envtest: %v\n", err)
		os.Exit(1)
	}

	testScheme = runtime.NewScheme()
	if err := scheme.AddToScheme(testScheme); err != nil {
		fmt.Fprintf(os.Stderr, "building scheme: %v\n", err)
		os.Exit(1)
	}
	if err := r8rv1alpha1.AddToScheme(testScheme); err != nil {
		fmt.Fprintf(os.Stderr, "building scheme: %v\n", err)
		os.Exit(1)
	}

	hubClient, err = client.New(cfg, client.Options{Scheme: testScheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "building client: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	_ = testEnv.Stop()
	os.Exit(code)
}

// firstEnvtestBinaryDir locates the envtest binaries downloaded by
// `make setup-envtest` when KUBEBUILDER_ASSETS is not exported.
func firstEnvtestBinaryDir() string {
	basePath := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Fake spoke transport

// objKey identifies one object in a fake spoke store.
type objKey struct {
	gvk       schema.GroupVersionKind
	namespace string
	name      string
}

// fakeTransport is an in-memory Transport: one object store per cluster,
// server-side-apply-like merge semantics (labels/annotations merge, other
// present fields replace), NotFound fidelity, and an unavailability switch
// for unreachable-cluster scenarios.
type fakeTransport struct {
	mu          sync.Mutex
	stores      map[string]map[objKey]*unstructured.Unstructured
	unavailable map[string]bool
	applies     int
	deletes     int
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		stores:      map[string]map[objKey]*unstructured.Unstructured{},
		unavailable: map[string]bool{},
	}
}

func keyOf(obj *unstructured.Unstructured) objKey {
	return objKey{gvk: obj.GroupVersionKind(), namespace: obj.GetNamespace(), name: obj.GetName()}
}

func notFound(gvk schema.GroupVersionKind, name string) error {
	return apierrors.NewNotFound(schema.GroupResource{
		Group:    gvk.Group,
		Resource: strings.ToLower(gvk.Kind) + "s",
	}, name)
}

func (f *fakeTransport) storeFor(cluster string) map[objKey]*unstructured.Unstructured {
	st, ok := f.stores[cluster]
	if !ok {
		st = map[objKey]*unstructured.Unstructured{}
		f.stores[cluster] = st
	}
	return st
}

func (f *fakeTransport) checkAvailable(cluster string) error {
	if f.unavailable[cluster] {
		return fmt.Errorf("cluster %q: %w", cluster, ErrClusterUnavailable)
	}
	return nil
}

// Apply implements Transport with SSA-like merge semantics.
func (f *fakeTransport) Apply(_ context.Context, cluster string, obj *unstructured.Unstructured) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkAvailable(cluster); err != nil {
		return err
	}
	f.applies++
	st := f.storeFor(cluster)
	k := keyOf(obj)
	existing, ok := st[k]
	if !ok {
		st[k] = obj.DeepCopy()
		return nil
	}
	merged := existing.DeepCopy()
	for field, v := range obj.Object {
		if field != "metadata" {
			merged.Object[field] = runtime.DeepCopyJSONValue(v)
			continue
		}
		inMD, _ := v.(map[string]any)
		exMD, _ := merged.Object["metadata"].(map[string]any)
		if exMD == nil {
			exMD = map[string]any{}
			merged.Object["metadata"] = exMD
		}
		for mk, mv := range inMD {
			if mk == "labels" || mk == "annotations" {
				exMap, _ := exMD[mk].(map[string]any)
				if exMap == nil {
					exMap = map[string]any{}
				}
				maps.Copy(exMap, mv.(map[string]any))
				exMD[mk] = exMap
				continue
			}
			exMD[mk] = runtime.DeepCopyJSONValue(mv)
		}
	}
	st[k] = merged
	return nil
}

// Get implements Transport.
func (f *fakeTransport) Get(_ context.Context, cluster string, key client.ObjectKey, obj *unstructured.Unstructured) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkAvailable(cluster); err != nil {
		return err
	}
	gvk := obj.GroupVersionKind()
	found, ok := f.storeFor(cluster)[objKey{gvk: gvk, namespace: key.Namespace, name: key.Name}]
	if !ok {
		return notFound(gvk, key.Name)
	}
	obj.Object = runtime.DeepCopyJSON(found.Object)
	return nil
}

// Delete implements Transport.
func (f *fakeTransport) Delete(_ context.Context, cluster string, obj *unstructured.Unstructured) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkAvailable(cluster); err != nil {
		return err
	}
	f.deletes++
	st := f.storeFor(cluster)
	k := keyOf(obj)
	if _, ok := st[k]; !ok {
		return notFound(k.gvk, k.name)
	}
	delete(st, k)
	return nil
}

// put seeds an object into a cluster store (test fixture path, no counters).
func (f *fakeTransport) put(cluster string, obj *unstructured.Unstructured) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.storeFor(cluster)[keyOf(obj)] = obj.DeepCopy()
}

// object returns a deep copy of a stored object, or nil.
func (f *fakeTransport) object(cluster string, gvk schema.GroupVersionKind, namespace, name string) *unstructured.Unstructured {
	f.mu.Lock()
	defer f.mu.Unlock()
	if obj, ok := f.storeFor(cluster)[objKey{gvk: gvk, namespace: namespace, name: name}]; ok {
		return obj.DeepCopy()
	}
	return nil
}

func (f *fakeTransport) setUnavailable(cluster string, unavailable bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unavailable[cluster] = unavailable
}

// nsObject builds a plain (pre-existing, unmanaged) Namespace fixture.
func nsObject(name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(namespaceGVK)
	u.SetName(name)
	return u
}

// ---------------------------------------------------------------------------
// Fake discovery inventory

// stubClusters is a mutable ClusterInventory.
type stubClusters struct {
	mu   sync.Mutex
	recs map[string]discovery.ClusterRecord
}

func newStubClusters(names ...string) *stubClusters {
	s := &stubClusters{recs: map[string]discovery.ClusterRecord{}}
	for _, n := range names {
		s.recs[n] = discovery.ClusterRecord{Name: n, Ready: true}
	}
	return s
}

// Lookup implements ClusterInventory.
func (s *stubClusters) Lookup(name string) (discovery.ClusterRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.recs[name]
	return rec, ok
}

func (s *stubClusters) remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.recs, name)
}

// ---------------------------------------------------------------------------
// Fixture helpers

var fixtureCounter int
var fixtureMu sync.Mutex

// uniqueName yields a unique DNS-safe name per call.
func uniqueName(prefix string) string {
	fixtureMu.Lock()
	defer fixtureMu.Unlock()
	fixtureCounter++
	return fmt.Sprintf("%s-%d", prefix, fixtureCounter)
}

// testFixture bundles the moving parts of one reconciler test.
type testFixture struct {
	t         *testing.T
	ns        string
	transport *fakeTransport
	clusters  *stubClusters
	recorder  *events.FakeRecorder
	rec       *Reconciler
	secret    *corev1.Secret
	rep       *r8rv1alpha1.Replication
}

// newFixture creates a hub namespace, a source Secret, and a Replication
// resolving to the given targets, plus a reconciler wired to fake spokes
// (each pre-seeded with the hub namespace's name as an existing namespace).
func newFixture(t *testing.T, secretValue string, targets []r8rv1alpha1.ResolvedTarget, clusterNames ...string) *testFixture {
	t.Helper()
	f := &testFixture{
		t:         t,
		transport: newFakeTransport(),
		clusters:  newStubClusters(clusterNames...),
		recorder:  events.NewFakeRecorder(200),
	}

	f.ns = uniqueName("eng-test")
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: f.ns}}
	if err := hubClient.Create(testCtx, ns); err != nil {
		t.Fatalf("creating hub namespace: %v", err)
	}

	f.secret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "web-creds", Namespace: f.ns},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"password": []byte(secretValue)},
	}
	if err := hubClient.Create(testCtx, f.secret); err != nil {
		t.Fatalf("creating source secret: %v", err)
	}

	for i := range targets {
		if len(targets[i].Namespaces) == 0 {
			targets[i].Namespaces = []string{f.ns}
		}
	}
	for _, c := range clusterNames {
		f.transport.put(c, nsObject(f.ns))
	}

	f.rep = &r8rv1alpha1.Replication{
		ObjectMeta: metav1.ObjectMeta{Name: "rep", Namespace: f.ns},
		Spec: r8rv1alpha1.ReplicationSpec{
			SourceRef: r8rv1alpha1.SourceReference{
				Kind:      "Secret",
				Namespace: f.ns,
				Name:      f.secret.Name,
				UID:       f.secret.UID,
			},
			Origin:          r8rv1alpha1.ReplicationOriginAnnotation,
			ResolvedTargets: targets,
		},
	}
	if err := hubClient.Create(testCtx, f.rep); err != nil {
		t.Fatalf("creating replication: %v", err)
	}

	f.rec = f.newReconciler()
	return f
}

// newReconciler builds a fresh Reconciler over the fixture's fakes (a second
// call simulates an operator restart: empty in-memory revocation cache).
func (f *testFixture) newReconciler() *Reconciler {
	return &Reconciler{
		Client:    hubClient,
		Scheme:    testScheme,
		Recorder:  f.recorder,
		Transport: f.transport,
		Clusters:  f.clusters,
		Options: Options{
			BackoffBase: 10 * time.Millisecond,
			BackoffMax:  time.Second,
		},
	}
}

// policy creates a ReplicationPolicy allowlisting the fixture's source
// namespace + Secret kind and the given target namespaces on all clusters;
// mutate customizes it before creation. Cleaned up with the test.
func (f *testFixture) policy(targetNamespaces []string, mutate func(*r8rv1alpha1.ReplicationPolicy)) *r8rv1alpha1.ReplicationPolicy {
	f.t.Helper()
	if targetNamespaces == nil {
		targetNamespaces = []string{f.ns}
	}
	pol := &r8rv1alpha1.ReplicationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: uniqueName("pol")},
		Spec: r8rv1alpha1.ReplicationPolicySpec{
			Sources: r8rv1alpha1.PolicySources{
				Namespaces: []string{f.ns},
				Kinds:      []string{"Secret"},
			},
			Targets: r8rv1alpha1.PolicyTargets{
				ClusterSelector: metav1.LabelSelector{},
				Namespaces:      targetNamespaces,
			},
		},
	}
	if mutate != nil {
		mutate(pol)
	}
	if err := hubClient.Create(testCtx, pol); err != nil {
		f.t.Fatalf("creating policy: %v", err)
	}
	f.t.Cleanup(func() {
		_ = hubClient.Delete(context.Background(), pol)
	})
	return pol
}

// reconcile runs one reconcile and refetches the Replication (nil when it
// was fully deleted).
func (f *testFixture) reconcile() (ctrl.Result, *r8rv1alpha1.Replication) {
	f.t.Helper()
	key := client.ObjectKey{Namespace: f.ns, Name: "rep"}
	res, err := f.rec.Reconcile(testCtx, ctrl.Request{NamespacedName: key})
	if err != nil {
		f.t.Fatalf("reconcile: %v", err)
	}
	rep := &r8rv1alpha1.Replication{}
	if err := hubClient.Get(testCtx, key, rep); err != nil {
		if apierrors.IsNotFound(err) {
			return res, nil
		}
		f.t.Fatalf("refetching replication: %v", err)
	}
	return res, rep
}

// updateSecret rewrites the source secret's payload.
func (f *testFixture) updateSecret(value string) {
	f.t.Helper()
	fresh := &corev1.Secret{}
	if err := hubClient.Get(testCtx, client.ObjectKeyFromObject(f.secret), fresh); err != nil {
		f.t.Fatalf("fetching secret: %v", err)
	}
	fresh.Data["password"] = []byte(value)
	if err := hubClient.Update(testCtx, fresh); err != nil {
		f.t.Fatalf("updating secret: %v", err)
	}
}

// replica fetches the stored replica Secret on one cluster (nil if absent).
func (f *testFixture) replica(cluster, namespace string) *unstructured.Unstructured {
	return f.transport.object(cluster, schema.GroupVersionKind{Version: "v1", Kind: "Secret"}, namespace, "web-creds")
}

// drainEvents collects all currently buffered recorder events.
func (f *testFixture) drainEvents() []string {
	var out []string
	for {
		select {
		case e := <-f.recorder.Events:
			out = append(out, e)
		default:
			return out
		}
	}
}

// eventsContaining reports whether any drained event contains the substring.
func eventsContaining(evs []string, substr string) bool {
	for _, e := range evs {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

// readyCondition returns the Ready condition (test helper).
func readyCondition(rep *r8rv1alpha1.Replication) *metav1.Condition {
	for i := range rep.Status.Conditions {
		if rep.Status.Conditions[i].Type == r8rv1alpha1.ReplicationConditionReady {
			return &rep.Status.Conditions[i]
		}
	}
	return nil
}
