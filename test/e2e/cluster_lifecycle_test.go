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

// Task 10.2: cluster lifecycle — register (bootstrap SA), deregister
// (runtime stop + inventory release), unreachable-cluster behavior.
//
// The suite deliberately exercises the documented engine deviation on
// deregistration: replicas REMAIN on a deregistered spoke (inventory is
// released with a ClusterGone event, no cleanup is attempted without a
// runtime) — see the replication-engine spec's unreachable-cleanup scenario.

import (
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/moeritze/k8s-r8r/internal/controller/request"
)

// operatorPodReady reports whether the controller-manager pod on the hub is
// Ready.
func operatorPodReady(g Gomega) {
	pods := &corev1.PodList{}
	g.Expect(hubClient.List(ctx(), pods,
		client.InNamespace(operatorNamespace),
		client.MatchingLabels{"control-plane": "controller-manager"})).To(Succeed())
	g.Expect(pods.Items).NotTo(BeEmpty())
	readyPods := 0
	for _, p := range pods.Items {
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				readyPods++
			}
		}
	}
	g.Expect(readyPods).To(BeNumerically(">=", 1), "operator pod must stay Ready")
}

// TestClusterLifecycle drives spoke-2 through outage, deregistration, and
// fresh re-registration while a replication to both spokes is live. It ends
// with a fully healthy fleet.
func TestClusterLifecycle(t *testing.T) {
	g := NewWithT(t)
	const srcNS = "e2e-lifecycle"
	const secretName = "lifecycle-probe"
	repName := request.ReplicationName("Secret", secretName)

	g.Expect(ensureNamespace(hubClient, srcNS)).To(Succeed())
	g.Expect(applyPolicy(policySpec{
		name:             "e2e-lifecycle-policy",
		sourceNamespaces: []string{srcNS},
		sourceKinds:      []string{"Secret"},
		targetNamespaces: []string{srcNS},
		allowNSCreation:  true,
	})).To(Succeed())
	t.Cleanup(func() { _ = deletePolicy("e2e-lifecycle-policy") })

	src, err := createAnnotatedSecret(srcNS, secretName, map[string][]byte{"gen": []byte("1")}, nil)
	g.Expect(err).NotTo(HaveOccurred())
	_ = src

	waitHealthy := func(g Gomega, wantTargets int32) {
		rep, err := getReplication(srcNS, repName)
		g.Expect(err).NotTo(HaveOccurred())
		cond := readyCondition(rep)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		g.Expect(rep.Status.Summary.DesiredTargets).To(Equal(wantTargets))
		g.Expect(rep.Status.Summary.ReadyTargets).To(Equal(wantTargets))
	}

	g.Eventually(func(g Gomega) { waitHealthy(g, 2) }, replTimeout, replPoll).Should(Succeed())

	t.Run("unreachable spoke degrades only its own target", func(t *testing.T) {
		g := NewWithT(t)
		container := spoke2 + "-control-plane"
		logf(t, "stopping %s", container)
		_, err := run(nil, "docker", "stop", container)
		g.Expect(err).NotTo(HaveOccurred())
		restarted := false
		defer func() {
			if !restarted {
				_, _ = run(nil, "docker", "start", container)
			}
		}()

		// Force a reconcile that must write to both spokes.
		g.Expect(touchSource(srcNS, secretName)).To(Succeed())

		g.Eventually(func(g Gomega) {
			rep, err := getReplication(srcNS, repName)
			g.Expect(err).NotTo(HaveOccurred())
			cond := readyCondition(rep)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse), "outage must surface")
			var spoke2Reasons []string
			for _, nrt := range rep.Status.NonReadyTargets {
				g.Expect(nrt.ClusterName).To(Equal(spoke2), "only spoke-2 may fail")
				spoke2Reasons = append(spoke2Reasons, nrt.Reason)
			}
			g.Expect(spoke2Reasons).NotTo(BeEmpty())
			// The unaffected spoke received the update.
			replica, err := spokeSecret(spokeClients[spoke1], srcNS, secretName)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(replica.Data).To(HaveKey("touched-at"), "spoke-1 unaffected by spoke-2 outage")
		}, 4*time.Minute, replPoll).Should(Succeed())

		// Hub readiness never flips on a spoke outage.
		g.Consistently(operatorPodReady, 45*time.Second, 5*time.Second).Should(Succeed())

		logf(t, "restarting %s", container)
		_, err = run(nil, "docker", "start", container)
		g.Expect(err).NotTo(HaveOccurred())
		restarted = true

		// Recovery: per-target backoff caps at 5m; allow generous headroom
		// for the kind control plane to come back too.
		g.Eventually(func(g Gomega) { waitHealthy(g, 2) }, 10*time.Minute, 5*time.Second).Should(Succeed())
	})

	t.Run("deregistration releases inventory with ClusterGone", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(deregisterSpoke(spoke2)).To(Succeed())

		g.Eventually(func(g Gomega) {
			rep, err := getReplication(srcNS, repName)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(rep.Status.Summary.DesiredTargets).To(Equal(int32(1)), "spoke-2 left the resolved selection")
			g.Expect(rep.Status.Summary.ReadyTargets).To(Equal(int32(1)))
			for _, e := range rep.Status.Inventory {
				g.Expect(e.ClusterName).NotTo(Equal(spoke2), "spoke-2 inventory released")
			}
			reasons, err := eventReasonsFor(srcNS, rep.Name)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(reasons).To(HaveKey("ClusterGone"), "release must be announced")
		}, replTimeout, replPoll).Should(Succeed())

		// Documented engine deviation: the replica REMAINS on the
		// deregistered spoke (no cleanup without a runtime).
		remaining, err := spokeSecret(spokeClients[spoke2], srcNS, secretName)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(remaining.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "k8s-r8r"))
	})

	t.Run("re-registration bootstraps fresh spoke credentials", func(t *testing.T) {
		g := NewWithT(t)
		spoke := spokeClients[spoke2]

		// Wipe the bootstrap artifacts so this is a genuinely fresh
		// bootstrap, not a leftover from setup.
		g.Expect(deleteIgnoreNotFound(spoke, &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "k8s-r8r-replicator"}})).To(Succeed())
		g.Expect(deleteIgnoreNotFound(spoke, &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{Name: "k8s-r8r-replicator"}})).To(Succeed())
		g.Expect(deleteNamespaceAndWait(spoke, operatorNamespace, 2*time.Minute)).To(Succeed())

		g.Expect(registerSpoke(spoke2, map[string]string{"env": "e2e", "e2e.r8r.io/spoke": spoke2})).To(Succeed())

		g.Eventually(func(g Gomega) {
			// Bootstrap artifacts are back.
			sa := &corev1.ServiceAccount{}
			g.Expect(spoke.Get(ctx(), types.NamespacedName{Namespace: operatorNamespace, Name: "k8s-r8r"}, sa)).To(Succeed())
			role := &rbacv1.ClusterRole{}
			g.Expect(spoke.Get(ctx(), types.NamespacedName{Name: "k8s-r8r-replicator"}, role)).To(Succeed())
			resources := map[string]bool{}
			for _, rule := range role.Rules {
				for _, res := range rule.Resources {
					resources[res] = true
				}
			}
			g.Expect(resources).To(HaveKey("secrets"))
			g.Expect(resources).To(HaveKey("configmaps"))
			g.Expect(resources).To(HaveKey("namespaces"))
			// And replication reconverged onto both spokes.
			waitHealthy(g, 2)
		}, 4*time.Minute, replPoll).Should(Succeed())
	})

	t.Run("replication traffic authenticates as the bootstrap ServiceAccount", func(t *testing.T) {
		g := NewWithT(t)
		spoke := spokeClients[spoke2]

		// Cut the SA's RBAC and force a write: the API server's Forbidden
		// error names the authenticated identity, proving steady-state
		// traffic uses the ServiceAccount, not the admin kubeconfig.
		g.Expect(deleteIgnoreNotFound(spoke, &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "k8s-r8r-replicator"}})).To(Succeed())
		defer func() {
			g.Expect(restoreSpokeClusterRoleBinding(spoke)).To(Succeed())
		}()

		g.Expect(touchSource(srcNS, secretName)).To(Succeed())

		g.Eventually(func(g Gomega) {
			rep, err := getReplication(srcNS, repName)
			g.Expect(err).NotTo(HaveOccurred())
			var messages []string
			for _, nrt := range rep.Status.NonReadyTargets {
				if nrt.ClusterName == spoke2 {
					messages = append(messages, nrt.Message)
				}
			}
			g.Expect(messages).NotTo(BeEmpty(), "spoke-2 write must fail without RBAC")
			g.Expect(strings.Join(messages, "\n")).To(
				ContainSubstring("system:serviceaccount:k8s-r8r-system:k8s-r8r"),
				"failure must name the bootstrap ServiceAccount identity")
		}, 4*time.Minute, replPoll).Should(Succeed())

		g.Expect(restoreSpokeClusterRoleBinding(spoke)).To(Succeed())
		g.Eventually(func(g Gomega) { waitHealthy(g, 2) }, 8*time.Minute, 5*time.Second).Should(Succeed())
	})

	// Leave the fleet clean for later tests.
	fresh := &corev1.Secret{}
	g.Expect(hubClient.Get(ctx(), types.NamespacedName{Namespace: srcNS, Name: secretName}, fresh)).To(Succeed())
	g.Expect(hubClient.Delete(ctx(), fresh)).To(Succeed())
	g.Eventually(func(g Gomega) {
		_, err := getReplication(srcNS, repName)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	}, replTimeout, replPoll).Should(Succeed())
}
