//go:build e2e
// +build e2e

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

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
)

// operatorImage is the image built from the working tree and side-loaded into
// the hub kind cluster.
const operatorImage = "k8s-r8r"
const operatorTag = "e2e"

// helmRelease names the operator installation on the hub.
const helmRelease = "k8s-r8r"

// TestMain provisions the whole fleet environment once for the suite:
//
//  1. kind fleet up (hub + 2 spokes; idempotent),
//  2. operator image build + kind load into the hub,
//  3. Helm install of charts/k8s-r8r (webhook disabled — see package doc),
//  4. simulated ClusterAPI inventory (CRD, Cluster objects, kubeconfig
//     Secrets) and a sweep of leftovers from previous runs,
//  5. wait for spoke bootstrap on both spokes.
//
// Teardown deletes the kind fleet unless K8S_R8R_E2E_KEEP=1 is set (debugging
// aid; re-runs are idempotent against a kept fleet).
func TestMain(m *testing.M) {
	if err := setup(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e setup failed: %v\n", err)
		teardown(1)
		os.Exit(1)
	}
	code := m.Run()
	teardown(code)
	os.Exit(code)
}

func setup() error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	repoRoot, err = filepath.Abs(filepath.Join(wd, "..", ".."))
	if err != nil {
		return err
	}

	step("creating kind fleet (hub + 2 spokes)")
	if out, err := run([]string{"K8S_R8R_SPOKES=2"}, filepath.Join(repoRoot, "hack", "kind-fleet.sh"), "up"); err != nil {
		return fmt.Errorf("fleet up: %w\n%s", err, out)
	}

	step("exporting fleet kubeconfigs")
	if out, err := run([]string{"K8S_R8R_SPOKES=2"}, filepath.Join(repoRoot, "hack", "kind-fleet.sh"), "kubeconfigs"); err != nil {
		return fmt.Errorf("fleet kubeconfigs: %w\n%s", err, out)
	}
	kubeconfigPath = map[string]string{}
	for _, name := range []string{hubCluster, spoke1, spoke2} {
		kubeconfigPath[name] = filepath.Join(repoRoot, "bin", "kubeconfigs", name+".kubeconfig")
	}

	step("building clients")
	hubClient, err = clientFor(kubeconfigPath[hubCluster])
	if err != nil {
		return fmt.Errorf("hub client: %w", err)
	}
	spokeClients = map[string]client.Client{}
	for _, name := range []string{spoke1, spoke2} {
		c, err := clientFor(kubeconfigPath[name])
		if err != nil {
			return fmt.Errorf("client for %s: %w", name, err)
		}
		spokeClients[name] = c
	}

	step("building operator image")
	if out, err := run(nil, "docker", "build", "-t", operatorImage+":"+operatorTag, "."); err != nil {
		return fmt.Errorf("docker build: %w\n%s", err, out)
	}
	step("loading operator image into hub")
	if out, err := run(nil, "kind", "load", "docker-image", operatorImage+":"+operatorTag, "--name", hubCluster); err != nil {
		return fmt.Errorf("kind load: %w\n%s", err, out)
	}

	step("installing operator chart on hub (webhook disabled)")
	if out, err := run(nil, "helm", "upgrade", "--install", helmRelease,
		filepath.Join(repoRoot, "charts", "k8s-r8r"),
		"--kubeconfig", kubeconfigPath[hubCluster],
		"--namespace", operatorNamespace, "--create-namespace",
		"--set", "image.repository="+operatorImage,
		"--set", "image.tag="+operatorTag,
		"--set", "webhook.enabled=false",
		"--wait", "--timeout", "5m",
	); err != nil {
		return fmt.Errorf("helm install: %w\n%s", err, out)
	}

	// A re-run against a kept fleet rebuilds the image under the same tag;
	// helm upgrade alone would not roll the pod, so force a fresh rollout.
	step("restarting operator to pick up the freshly built image")
	if out, err := kubectl(hubCluster, "-n", operatorNamespace, "rollout", "restart", "deployment", helmRelease); err != nil {
		return fmt.Errorf("rollout restart: %w\n%s", err, out)
	}
	if out, err := kubectl(hubCluster, "-n", operatorNamespace, "rollout", "status", "deployment", helmRelease, "--timeout=3m"); err != nil {
		return fmt.Errorf("rollout status: %w\n%s", err, out)
	}

	step("applying simulated ClusterAPI CRD")
	if out, err := kubectl(hubCluster, "apply", "-f", filepath.Join(repoRoot, "test", "e2e", "testdata", "capi-cluster-crd.yaml")); err != nil {
		return fmt.Errorf("capi crd: %w\n%s", err, out)
	}

	step("sweeping leftovers from previous runs")
	if err := sweepPreviousRun(); err != nil {
		return fmt.Errorf("sweep: %w", err)
	}

	step("registering spokes in simulated CAPI inventory")
	if err := ensureNamespace(hubClient, capiNamespace); err != nil {
		return err
	}
	for _, name := range []string{spoke1, spoke2} {
		if err := registerSpoke(name, map[string]string{"env": "e2e", "e2e.r8r.io/spoke": name}); err != nil {
			return fmt.Errorf("registering %s: %w", name, err)
		}
	}

	step("waiting for spoke bootstrap")
	for _, name := range []string{spoke1, spoke2} {
		if err := waitForBootstrap(name, 4*time.Minute); err != nil {
			return err
		}
	}
	step("setup complete")
	return nil
}

// sweepPreviousRun makes re-runs against a kept fleet idempotent: it deletes
// every suite-owned namespace on hub and spokes (with the finalizer escape
// hatch), suite-owned policies, and stray managed replicas on the spokes.
func sweepPreviousRun() error {
	// Policies first, so their deletion cannot re-trigger replica writes
	// while namespaces drain.
	pols := &r8rv1alpha1.ReplicationPolicyList{}
	if err := hubClient.List(ctx(), pols, client.MatchingLabels{e2eLabelKey: e2eLabelValue}); err == nil {
		for i := range pols.Items {
			_ = hubClient.Delete(ctx(), &pols.Items[i])
		}
	}

	// Suite-owned hub namespaces (sources + capi inventory).
	nss := &corev1.NamespaceList{}
	if err := hubClient.List(ctx(), nss, client.MatchingLabels{e2eLabelKey: e2eLabelValue}); err != nil {
		return err
	}
	for i := range nss.Items {
		if err := deleteNamespaceAndWait(hubClient, nss.Items[i].Name, 3*time.Minute); err != nil {
			return err
		}
	}

	// Spokes: suite-owned namespaces plus any managed replicas left in
	// namespaces the engine created (labeled managed-by).
	for name, spoke := range spokeClients {
		spokeNss := &corev1.NamespaceList{}
		if err := spoke.List(ctx(), spokeNss); err != nil {
			return fmt.Errorf("listing namespaces on %s: %w", name, err)
		}
		for i := range spokeNss.Items {
			ns := spokeNss.Items[i]
			owned := ns.Labels[e2eLabelKey] == e2eLabelValue ||
				(ns.Labels["app.kubernetes.io/managed-by"] == "k8s-r8r" && ns.Name != operatorNamespace)
			if owned {
				if err := deleteNamespaceAndWait(spoke, ns.Name, 2*time.Minute); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// waitForBootstrap waits until the operator has created its bootstrap
// artifacts on a spoke (namespace + ServiceAccount).
func waitForBootstrap(name string, timeout time.Duration) error {
	spoke := spokeClients[name]
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		sa := &corev1.ServiceAccount{}
		err := spoke.Get(ctx(), types.NamespacedName{Namespace: operatorNamespace, Name: "k8s-r8r"}, sa)
		if err == nil {
			return nil
		}
		if !apierrors.IsNotFound(err) {
			fmt.Fprintf(os.Stderr, "bootstrap poll on %s: %v\n", name, err)
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("spoke %s not bootstrapped within %s", name, timeout)
}

func teardown(code int) {
	if os.Getenv("K8S_R8R_E2E_KEEP") == "1" {
		fmt.Println("K8S_R8R_E2E_KEEP=1 set; keeping kind fleet for debugging")
		return
	}
	if repoRoot == "" {
		return
	}
	step("tearing down kind fleet")
	if out, err := run(nil, filepath.Join(repoRoot, "hack", "kind-fleet.sh"), "down"); err != nil {
		fmt.Fprintf(os.Stderr, "fleet down: %v\n%s\n", err, out)
	}
	_ = code
}

func step(msg string) {
	fmt.Printf("[e2e %s] %s\n", time.Now().Format("15:04:05"), msg)
}
