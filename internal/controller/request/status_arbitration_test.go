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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
	"github.com/moeritze/k8s-r8r/internal/annotations"
	"github.com/moeritze/k8s-r8r/internal/engine"
)

// This file covers the seam that issue #27 fell through: the request
// controller and the replication engine both write status on the same
// Replication, and every other test in the tree exercises exactly one of
// them. The request suite wires only the request Reconciler, the engine suite
// only the engine — so "the engine overwrites the request controller's
// verdict" was invisible to CI while being the whole bug.
//
// Here the live request controller (started by the suite's manager) and the
// engine reconciler run against the same object, and the assertions are on
// the TERMINAL condition state: what an operator sees after everything has
// settled, not the intermediate state one controller happens to write first.
//
// The engine is driven synchronously rather than registered with the manager,
// so each test controls exactly when it runs and can assert that a second and
// third pass change nothing (design D8).

// engineFor builds an engine reconciler against the envtest API server. No
// Transport is needed: every case here resolves to zero targets, so the
// engine never reaches a spoke cluster.
func engineFor() *engine.Reconciler {
	return &engine.Reconciler{Client: k8sClient, Scheme: testScheme}
}

// runEngine reconciles one Replication with the engine, as the engine's own
// controller would.
func runEngine(eng *engine.Reconciler, key types.NamespacedName) {
	GinkgoHelper()
	_, err := eng.Reconcile(ctx, ctrl.Request{NamespacedName: key})
	Expect(err).NotTo(HaveOccurred())
}

// dropEngineFinalizer releases the engine finalizer the engine adds on its
// first pass, so the Replication can still be garbage-collected with its
// source at the end of the test.
func dropEngineFinalizer(key types.NamespacedName) {
	rep := &r8rv1alpha1.Replication{}
	if err := k8sClient.Get(ctx, key, rep); err != nil {
		return
	}
	if controllerutil.RemoveFinalizer(rep, engine.FinalizerName) {
		Expect(k8sClient.Update(ctx, rep)).To(Succeed())
	}
}

var _ = Describe("Status arbitration between the request controller and the engine", func() {
	var (
		ns  string
		eng *engine.Reconciler
	)

	BeforeEach(func() {
		ns = newNamespaceName()
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		})).To(Succeed())
		inventory.set() // each test declares its own fleet
		eng = engineFor()
	})

	AfterEach(func() {
		// Policies are cluster-scoped: remove them so tests stay independent.
		Expect(k8sClient.DeleteAllOf(ctx, &r8rv1alpha1.ReplicationPolicy{})).To(Succeed())
	})

	// waitForResolution blocks until the request controller has materialized
	// the Replication and recorded its TargetsResolved verdict.
	waitForResolution := func(key types.NamespacedName, reason string) *r8rv1alpha1.Replication {
		GinkgoHelper()
		rep := &r8rv1alpha1.Replication{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key, rep)).To(Succeed())
			cond := meta.FindStatusCondition(rep.Status.Conditions,
				r8rv1alpha1.ReplicationConditionTargetsResolved)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(reason))
		}, timeout, interval).Should(Succeed())
		return rep
	}

	Context("every target denied by policy", func() {
		It("ends up Ready=False/NoTargets, not Ready=True/AllTargetsReady", func() {
			inventory.set(readyCluster(spokeA, prodLabels()))
			// No policy: default deny, so the cluster is a candidate target
			// but every (cluster, namespace) pair is refused.
			sec := annotatedSecret(ns, "denied-both", map[string]string{
				annotations.KeyReplicate:      annotations.ValueTrue,
				annotations.KeyTargetClusters: selectorProd,
			})
			Expect(k8sClient.Create(ctx, sec)).To(Succeed())

			key := types.NamespacedName{Namespace: ns, Name: ReplicationName(kindSecret, "denied-both")}
			waitForResolution(key, r8rv1alpha1.ReasonPolicyDenied)
			DeferCleanup(func() { dropEngineFinalizer(key) })

			// The engine now runs over the same object. Before the fix this
			// pass replaced the request controller's verdict with
			// Ready=True/AllTargetsReady ("0/0 targets ready"), because Ready
			// was derived from failed == 0 alone.
			runEngine(eng, key)

			rep := &r8rv1alpha1.Replication{}
			Expect(k8sClient.Get(ctx, key, rep)).To(Succeed())
			ready := meta.FindStatusCondition(rep.Status.Conditions,
				r8rv1alpha1.ReplicationConditionReady)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionFalse),
				"a Replication with zero resolved targets must never report Ready=True")
			Expect(ready.Reason).To(Equal(r8rv1alpha1.ReasonNoTargets))
			Expect(rep.Status.Summary.DesiredTargets).To(BeZero())

			// The request controller's verdict survives the engine's write:
			// the two conditions coexist and say different things.
			denied := meta.FindStatusCondition(rep.Status.Conditions,
				r8rv1alpha1.ReplicationConditionTargetsResolved)
			Expect(denied).NotTo(BeNil())
			Expect(denied.Status).To(Equal(metav1.ConditionFalse))
			Expect(denied.Reason).To(Equal(r8rv1alpha1.ReasonPolicyDenied))
		})

		It("reaches a fixed point: neither controller keeps rewriting status", func() {
			inventory.set(readyCluster(spokeA, prodLabels()))
			sec := annotatedSecret(ns, "no-churn", map[string]string{
				annotations.KeyReplicate:      annotations.ValueTrue,
				annotations.KeyTargetClusters: selectorProd,
			})
			Expect(k8sClient.Create(ctx, sec)).To(Succeed())

			key := types.NamespacedName{Namespace: ns, Name: ReplicationName(kindSecret, "no-churn")}
			waitForResolution(key, r8rv1alpha1.ReasonPolicyDenied)
			DeferCleanup(func() { dropEngineFinalizer(key) })

			// Let the engine settle (first pass also adds its finalizer).
			runEngine(eng, key)
			runEngine(eng, key)

			settled := &r8rv1alpha1.Replication{}
			Expect(k8sClient.Get(ctx, key, settled)).To(Succeed())
			rv := settled.ResourceVersion

			// Further engine passes must not write, and the live request
			// controller — which watches Replications — must not answer the
			// engine's write with one of its own. Before the fix the two
			// clobbered each other's Ready condition forever, bounded only by
			// the rate limiter (design D8: no status churn when nothing
			// changed).
			for range 3 {
				runEngine(eng, key)
			}
			Consistently(func(g Gomega) {
				rep := &r8rv1alpha1.Replication{}
				g.Expect(k8sClient.Get(ctx, key, rep)).To(Succeed())
				g.Expect(rep.ResourceVersion).To(Equal(rv),
					"status is being rewritten with nothing changed")
			}, "2s", interval).Should(Succeed())
		})
	})

	Context("selector matches no cluster (the typo case)", func() {
		It("ends up Ready=False/NoTargets even though nothing was denied", func() {
			// A ready cluster exists, but the request's selector does not
			// match it — target resolution returns early with no candidates
			// and therefore no policy decisions at all, so there is no denial
			// and no denial event. This path used to leave the object
			// silently green forever.
			inventory.set(readyCluster(spokeA, prodLabels()))
			Expect(k8sClient.Create(ctx, allowPolicy("allow-"+ns, ns, ns))).To(Succeed())

			sec := annotatedSecret(ns, "typo-selector", map[string]string{
				annotations.KeyReplicate:      annotations.ValueTrue,
				annotations.KeyTargetClusters: "env=prd", // typo: no cluster matches
			})
			Expect(k8sClient.Create(ctx, sec)).To(Succeed())

			key := types.NamespacedName{Namespace: ns, Name: ReplicationName(kindSecret, "typo-selector")}
			rep := waitForResolution(key, r8rv1alpha1.ReasonNoTargets)
			DeferCleanup(func() { dropEngineFinalizer(key) })
			Expect(rep.Spec.ResolvedTargets).To(BeEmpty())

			runEngine(eng, key)

			Expect(k8sClient.Get(ctx, key, rep)).To(Succeed())
			ready := meta.FindStatusCondition(rep.Status.Conditions,
				r8rv1alpha1.ReplicationConditionReady)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			Expect(ready.Reason).To(Equal(r8rv1alpha1.ReasonNoTargets))
		})
	})

	Context("permission revoked after resolution", func() {
		It("goes from Ready=True to Ready=False/NoTargets when the policy is deleted", func() {
			inventory.set(readyCluster(spokeA, prodLabels()))
			pol := allowPolicy("allow-"+ns, ns, ns)
			Expect(k8sClient.Create(ctx, pol)).To(Succeed())

			sec := annotatedSecret(ns, "revoked", map[string]string{
				annotations.KeyReplicate:      annotations.ValueTrue,
				annotations.KeyTargetClusters: selectorProd,
			})
			Expect(k8sClient.Create(ctx, sec)).To(Succeed())

			key := types.NamespacedName{Namespace: ns, Name: ReplicationName(kindSecret, "revoked")}
			rep := &r8rv1alpha1.Replication{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, key, rep)).To(Succeed())
				g.Expect(rep.Spec.ResolvedTargets).To(HaveLen(1))
				g.Expect(meta.IsStatusConditionTrue(rep.Status.Conditions,
					r8rv1alpha1.ReplicationConditionTargetsResolved)).To(BeTrue())
			}, timeout, interval).Should(Succeed())
			DeferCleanup(func() { dropEngineFinalizer(key) })

			// Withdraw the permission. The request controller re-resolves
			// through the policy watch and empties spec.resolvedTargets.
			Expect(k8sClient.Delete(ctx, pol)).To(Succeed())
			waitForResolution(key, r8rv1alpha1.ReasonPolicyDenied)

			// The durable record after revocation is a red object, not
			// "Ready=True, 0/0 targets ready" with a PolicyRevoked condition
			// that the engine removes again on the next pass.
			runEngine(eng, key)
			Expect(k8sClient.Get(ctx, key, rep)).To(Succeed())
			ready := meta.FindStatusCondition(rep.Status.Conditions,
				r8rv1alpha1.ReplicationConditionReady)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			Expect(ready.Reason).To(Equal(r8rv1alpha1.ReasonNoTargets))
		})
	})
})
