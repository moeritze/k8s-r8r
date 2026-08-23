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

package request

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
	"github.com/moeritze/k8s-r8r/internal/discovery"
)

// stubInventory is a test double for the ClusterInventory interface. Tests
// mutate its records and then call reconciler.NotifyClusterInventoryChanged
// to simulate discovery events.
type stubInventory struct {
	mu      sync.Mutex
	records []discovery.ClusterRecord
}

func (s *stubInventory) List() []discovery.ClusterRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]discovery.ClusterRecord, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r.Clone())
	}
	return out
}

func (s *stubInventory) set(records ...discovery.ClusterRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = records
}

var (
	testEnv    *envtest.Environment
	cfg        *rest.Config
	k8sClient  client.Client // direct (uncached) client for test assertions
	testScheme *runtime.Scheme
	inventory  = &stubInventory{}
	reconciler *Reconciler
	ctx        context.Context
	cancel     context.CancelFunc
)

func TestRequestController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Request Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	ctx, cancel = context.WithCancel(context.Background())

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	if dir := firstEnvtestBinaryDir(); dir != "" {
		testEnv.BinaryAssetsDirectory = dir
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	testScheme = runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(testScheme)).To(Succeed())
	Expect(r8rv1alpha1.AddToScheme(testScheme)).To(Succeed())

	k8sClient, err = client.New(cfg, client.Options{Scheme: testScheme})
	Expect(err).NotTo(HaveOccurred())

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  testScheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).NotTo(HaveOccurred())

	reconciler = &Reconciler{
		Inventory: inventory,
		// ServiceAccount is watched but NOT allowlisted, to exercise the
		// KindNotEnabled gate; Secret and ConfigMap come from the default
		// allowlist.
		WatchKinds: []schema.GroupVersionKind{
			{Group: "", Version: "v1", Kind: "ServiceAccount"},
		},
	}
	Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
})

// firstEnvtestBinaryDir locates the envtest binaries downloaded by
// `make setup-envtest` when KUBEBUILDER_ASSETS is not exported.
func firstEnvtestBinaryDir() string {
	basePath := filepath.Join("..", "..", "..", "bin", "k8s")
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

var nsCounter int

// newNamespaceName yields a fresh, unique namespace name per test.
func newNamespaceName() string {
	nsCounter++
	return fmt.Sprintf("req-test-%d", nsCounter)
}
