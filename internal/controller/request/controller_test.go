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
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
	"github.com/moeritze/k8s-r8r/internal/annotations"
	"github.com/moeritze/k8s-r8r/internal/discovery"
)

const (
	timeout  = 10 * time.Second
	interval = 100 * time.Millisecond

	kindSecret   = "Secret"
	spokeA       = "spoke-a"
	selectorProd = "env=prod"
)

// prodLabels returns the inventory labels of a "prod" cluster.
func prodLabels() map[string]string {
	return map[string]string{"env": "prod"}
}

// readyCluster builds a ready inventory record.
func readyCluster(name string, lbls map[string]string) discovery.ClusterRecord {
	return discovery.ClusterRecord{Name: name, Labels: lbls, Ready: true}
}

// allowPolicy builds a ReplicationPolicy permitting Secret+ConfigMap sources
// in srcNS to replicate into targetNSs on all clusters.
func allowPolicy(name, srcNS string, targetNSs ...string) *r8rv1alpha1.ReplicationPolicy {
	return &r8rv1alpha1.ReplicationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: r8rv1alpha1.ReplicationPolicySpec{
			Sources: r8rv1alpha1.PolicySources{
				Namespaces: []string{srcNS},
				Kinds:      []string{kindSecret, "ConfigMap"},
			},
			Targets: r8rv1alpha1.PolicyTargets{
				ClusterSelector: metav1.LabelSelector{}, // all clusters
				Namespaces:      targetNSs,
			},
		},
	}
}

func annotatedSecret(ns, name string, ann map[string]string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Annotations: ann},
		StringData: map[string]string{"password": "hunter2"},
	}
}

// eventsFor returns the event reasons recorded for the named object.
func eventsFor(ns, name string) func() []string {
	return func() []string {
		var list corev1.EventList
		if err := k8sClient.List(ctx, &list, client.InNamespace(ns)); err != nil {
			return nil
		}
		var reasons []string
		for _, e := range list.Items {
			if e.InvolvedObject.Name == name {
				reasons = append(reasons, e.Reason)
			}
		}
		return reasons
	}
}

// operatorEventsFor returns the full Event objects recorded for the named
// object, so tests can assert on more than the reason.
func operatorEventsFor(ns, name string) func() []corev1.Event {
	return func() []corev1.Event {
		var list corev1.EventList
		if err := k8sClient.List(ctx, &list, client.InNamespace(ns)); err != nil {
			return nil
		}
		var out []corev1.Event
		for _, e := range list.Items {
			if e.InvolvedObject.Name == name {
				out = append(out, e)
			}
		}
		return out
	}
}

var _ = Describe("Request controller", func() {
	var ns string

	BeforeEach(func() {
		ns = newNamespaceName()
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		})).To(Succeed())
		inventory.set() // each test declares its own fleet
	})

	AfterEach(func() {
		// Policies are cluster-scoped: remove them so tests stay independent.
		Expect(k8sClient.DeleteAllOf(ctx, &r8rv1alpha1.ReplicationPolicy{})).To(Succeed())
	})

	Context("valid annotated Secret with a permitting policy", func() {
		It("materializes exactly one operator-owned Replication with resolved targets", func() {
			inventory.set(
				readyCluster(spokeA, prodLabels()),
				readyCluster("spoke-b", map[string]string{"env": "dev"}),
			)
			Expect(k8sClient.Create(ctx, allowPolicy("allow-"+ns, ns, ns))).To(Succeed())

			sec := annotatedSecret(ns, "db-creds", map[string]string{
				annotations.KeyReplicate:      annotations.ValueTrue,
				annotations.KeyTargetClusters: selectorProd,
			})
			Expect(k8sClient.Create(ctx, sec)).To(Succeed())

			repName := ReplicationName(kindSecret, "db-creds")
			rep := &r8rv1alpha1.Replication{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: repName}, rep)).To(Succeed())
			}, timeout, interval).Should(Succeed())

			// Origin field records the request kind (spec: canonical layer
			// accepts multiple request origins).
			Expect(rep.Spec.Origin).To(Equal(r8rv1alpha1.ReplicationOriginAnnotation))
			Expect(rep.Spec.SourceRef.Kind).To(Equal("Secret"))
			Expect(rep.Spec.SourceRef.Name).To(Equal("db-creds"))
			Expect(rep.Spec.SourceRef.Namespace).To(Equal(ns))
			Expect(rep.Spec.SourceRef.UID).NotTo(BeEmpty())

			// Only the selector-matching, policy-permitted cluster resolves;
			// target namespaces default to the source namespace.
			Expect(rep.Spec.ResolvedTargets).To(ConsistOf(r8rv1alpha1.ResolvedTarget{
				ClusterName: spokeA,
				Namespaces:  []string{ns},
			}))

			// Owning source link + operator labels.
			ref := metav1.GetControllerOf(rep)
			Expect(ref).NotTo(BeNil())
			Expect(ref.Kind).To(Equal("Secret"))
			Expect(ref.Name).To(Equal("db-creds"))
			Expect(rep.Labels).To(HaveKeyWithValue(ManagedByLabel, ManagedByValue))
			Expect(rep.Labels).To(HaveKeyWithValue(SourceKindLabel, "secret"))
			Expect(rep.Labels).To(HaveKeyWithValue(SourceNameLabel, "db-creds"))

			// Finalizer handshake step 1: source carries r8r.io/finalizer.
			Eventually(func(g Gomega) {
				got := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sec), got)).To(Succeed())
				g.Expect(got.Finalizers).To(ContainElement(Finalizer))
			}, timeout, interval).Should(Succeed())
		})

		It("honors explicit target namespaces and target-name override", func() {
			inventory.set(readyCluster(spokeA, prodLabels()))
			Expect(k8sClient.Create(ctx, allowPolicy("allow-"+ns, ns, "team-a", "team-b"))).To(Succeed())

			sec := annotatedSecret(ns, "multi-ns", map[string]string{
				annotations.KeyReplicate:        annotations.ValueTrue,
				annotations.KeyTargetClusters:   selectorProd,
				annotations.KeyTargetNamespaces: "team-a,team-b",
				annotations.KeyTargetName:       "renamed-secret",
			})
			Expect(k8sClient.Create(ctx, sec)).To(Succeed())

			rep := &r8rv1alpha1.Replication{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: ns, Name: ReplicationName(kindSecret, "multi-ns"),
				}, rep)).To(Succeed())
				g.Expect(rep.Spec.ResolvedTargets).To(ConsistOf(r8rv1alpha1.ResolvedTarget{
					ClusterName: spokeA,
					Namespaces:  []string{"team-a", "team-b"},
					TargetName:  "renamed-secret",
				}))
			}, timeout, interval).Should(Succeed())
		})

		It("materializes ConfigMap sources from the default allowlist", func() {
			inventory.set(readyCluster("spoke-a", nil))
			Expect(k8sClient.Create(ctx, allowPolicy("allow-"+ns, ns, ns))).To(Succeed())

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: ns, Name: "app-config",
					Annotations: map[string]string{
						annotations.KeyReplicate:      annotations.ValueTrue,
						annotations.KeyTargetClusters: "", // empty selector: no clusters
					},
				},
				Data: map[string]string{"k": "v"},
			}
			// Empty selector selects no clusters — Replication exists with no
			// resolved targets and no policy denial.
			Expect(k8sClient.Create(ctx, cm)).To(Succeed())
			rep := &r8rv1alpha1.Replication{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: ns, Name: ReplicationName("ConfigMap", "app-config"),
				}, rep)).To(Succeed())
			}, timeout, interval).Should(Succeed())
			Expect(rep.Spec.Origin).To(Equal(r8rv1alpha1.ReplicationOriginAnnotation))
			Expect(rep.Spec.ResolvedTargets).To(BeEmpty())
			// Ready belongs to the engine and is never written here.
			Expect(meta.FindStatusCondition(rep.Status.Conditions,
				r8rv1alpha1.ReplicationConditionReady)).To(BeNil())
			// ...but "requested replication, resolved nothing" is reported:
			// with no denial there is no denial event either, so this
			// condition is the only durable signal (issue #27).
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: ns, Name: ReplicationName("ConfigMap", "app-config"),
				}, rep)).To(Succeed())
				cond := meta.FindStatusCondition(rep.Status.Conditions,
					r8rv1alpha1.ReplicationConditionTargetsResolved)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(r8rv1alpha1.ReasonNoTargets))
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("request without a matching policy", func() {
		It("materializes no targets and reports PolicyDenied", func() {
			inventory.set(readyCluster(spokeA, prodLabels()))
			// No policy created: default deny.

			sec := annotatedSecret(ns, "denied", map[string]string{
				annotations.KeyReplicate:      annotations.ValueTrue,
				annotations.KeyTargetClusters: selectorProd,
			})
			Expect(k8sClient.Create(ctx, sec)).To(Succeed())

			rep := &r8rv1alpha1.Replication{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: ns, Name: ReplicationName(kindSecret, "denied"),
				}, rep)).To(Succeed())
				cond := meta.FindStatusCondition(rep.Status.Conditions,
					r8rv1alpha1.ReplicationConditionTargetsResolved)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(r8rv1alpha1.ReasonPolicyDenied))
				g.Expect(cond.Message).NotTo(BeEmpty())
			}, timeout, interval).Should(Succeed())
			Expect(rep.Spec.ResolvedTargets).To(BeEmpty())
			// The denial is reported on TargetsResolved, never on Ready: the
			// engine owns Ready, and a second writer would be clobbered by it
			// (issue #27).
			Expect(meta.FindStatusCondition(rep.Status.Conditions,
				r8rv1alpha1.ReplicationConditionReady)).To(BeNil())

			// Event on the source names the denied dimension via the policy
			// decision reason.
			Eventually(eventsFor(ns, "denied"), timeout, interval).
				Should(ContainElement(EventReasonPolicyDenied))
		})

		It("flips TargetsResolved to True once a policy permits the request", func() {
			inventory.set(readyCluster(spokeA, prodLabels()))
			sec := annotatedSecret(ns, "late-allow", map[string]string{
				annotations.KeyReplicate:      annotations.ValueTrue,
				annotations.KeyTargetClusters: selectorProd,
			})
			Expect(k8sClient.Create(ctx, sec)).To(Succeed())

			key := types.NamespacedName{Namespace: ns, Name: ReplicationName(kindSecret, "late-allow")}
			Eventually(func(g Gomega) {
				rep := &r8rv1alpha1.Replication{}
				g.Expect(k8sClient.Get(ctx, key, rep)).To(Succeed())
				g.Expect(meta.IsStatusConditionFalse(rep.Status.Conditions,
					r8rv1alpha1.ReplicationConditionTargetsResolved)).To(BeTrue())
			}, timeout, interval).Should(Succeed())

			// Creating a policy retriggers resolution via the policy watch.
			Expect(k8sClient.Create(ctx, allowPolicy("allow-"+ns, ns, ns))).To(Succeed())
			Eventually(func(g Gomega) {
				rep := &r8rv1alpha1.Replication{}
				g.Expect(k8sClient.Get(ctx, key, rep)).To(Succeed())
				g.Expect(rep.Spec.ResolvedTargets).To(HaveLen(1))
				cond := meta.FindStatusCondition(rep.Status.Conditions,
					r8rv1alpha1.ReplicationConditionTargetsResolved)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Reason).To(Equal(r8rv1alpha1.ReasonTargetsResolved))
				// Still never written by this controller.
				g.Expect(meta.FindStatusCondition(rep.Status.Conditions,
					r8rv1alpha1.ReplicationConditionReady)).To(BeNil())
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("non-allowlisted kind", func() {
		It("emits a KindNotEnabled event and materializes nothing", func() {
			sa := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: ns, Name: "annotated-sa",
					Annotations: map[string]string{
						annotations.KeyReplicate:      annotations.ValueTrue,
						annotations.KeyTargetClusters: selectorProd,
					},
				},
			}
			Expect(k8sClient.Create(ctx, sa)).To(Succeed())

			Eventually(eventsFor(ns, "annotated-sa"), timeout, interval).
				Should(ContainElement(EventReasonKindNotEnabled))

			rep := &r8rv1alpha1.Replication{}
			Consistently(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: ns, Name: ReplicationName("ServiceAccount", "annotated-sa"),
				}, rep)
				return err != nil
			}, time.Second, interval).Should(BeTrue())
		})
	})

	Context("hand-authored Replication", func() {
		It("is marked NotAuthoritative and never acted on", func() {
			manual := &r8rv1alpha1.Replication{
				ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "hand-made"},
				Spec: r8rv1alpha1.ReplicationSpec{
					SourceRef: r8rv1alpha1.SourceReference{
						Kind: "Secret", Namespace: ns, Name: "whatever", UID: "fake-uid",
					},
					Origin: r8rv1alpha1.ReplicationOriginAnnotation,
				},
			}
			Expect(k8sClient.Create(ctx, manual)).To(Succeed())

			Eventually(func(g Gomega) {
				got := &r8rv1alpha1.Replication{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(manual), got)).To(Succeed())
				g.Expect(meta.IsStatusConditionTrue(got.Status.Conditions,
					ConditionNotAuthoritative)).To(BeTrue())
				ready := meta.FindStatusCondition(got.Status.Conditions,
					r8rv1alpha1.ReplicationConditionReady)
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Reason).To(Equal(r8rv1alpha1.ReasonNotAuthoritative))
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("malformed annotations", func() {
		It("emits an InvalidAnnotations event naming the offending key", func() {
			sec := annotatedSecret(ns, "broken", map[string]string{
				annotations.KeyReplicate:      annotations.ValueTrue,
				annotations.KeyTargetClusters: "env==(bad",
			})
			Expect(k8sClient.Create(ctx, sec)).To(Succeed())
			Eventually(eventsFor(ns, "broken"), timeout, interval).
				Should(ContainElement(EventReasonInvalidAnnotations))
		})
	})

	// Regression guard for issue #32: events recorded through the
	// events.k8s.io/v1 recorder populate only eventTime, leaving
	// firstTimestamp/lastTimestamp/count null after the API server's
	// eventsv1 -> corev1 conversion. `kubectl get events
	// --sort-by=.lastTimestamp` — the near-universal idiom — then sorts on a
	// null key and returns an arbitrary order, which actively misleads
	// whoever is reading the event log while debugging.
	Context("event timestamps", func() {
		It("populates lastTimestamp/firstTimestamp/count so --sort-by=.lastTimestamp orders correctly", func() {
			sa := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: ns, Name: "timestamped-sa",
					Annotations: map[string]string{
						annotations.KeyReplicate:      annotations.ValueTrue,
						annotations.KeyTargetClusters: selectorProd,
					},
				},
			}
			Expect(k8sClient.Create(ctx, sa)).To(Succeed())

			var evs []corev1.Event
			Eventually(func(g Gomega) {
				evs = operatorEventsFor(ns, "timestamped-sa")()
				g.Expect(evs).NotTo(BeEmpty())
			}, timeout, interval).Should(Succeed())

			for _, e := range evs {
				Expect(e.LastTimestamp.IsZero()).To(BeFalse(),
					"event %s/%s has a null lastTimestamp; --sort-by=.lastTimestamp would misorder it",
					e.Reason, e.Name)
				Expect(e.FirstTimestamp.IsZero()).To(BeFalse(),
					"event %s/%s has a null firstTimestamp", e.Reason, e.Name)
				Expect(e.Count).To(BeNumerically(">=", 1),
					"event %s/%s has a null count", e.Reason, e.Name)
				Expect(e.LastTimestamp.Time).NotTo(BeTemporally("<", e.FirstTimestamp.Time))
			}

			// The sort key is not merely non-null but usable: sorting the
			// recorded events by lastTimestamp yields a total, stable order.
			sorted := slices.Clone(evs)
			slices.SortFunc(sorted, func(a, b corev1.Event) int {
				return a.LastTimestamp.Compare(b.LastTimestamp.Time)
			})
			for i := 1; i < len(sorted); i++ {
				Expect(sorted[i-1].LastTimestamp.Time).
					NotTo(BeTemporally(">", sorted[i].LastTimestamp.Time))
			}
		})
	})

	Context("annotation removal", func() {
		It("deletes the Replication and releases the source finalizer", func() {
			inventory.set(readyCluster(spokeA, prodLabels()))
			Expect(k8sClient.Create(ctx, allowPolicy("allow-"+ns, ns, ns))).To(Succeed())

			sec := annotatedSecret(ns, "revoked", map[string]string{
				annotations.KeyReplicate:      annotations.ValueTrue,
				annotations.KeyTargetClusters: selectorProd,
			})
			Expect(k8sClient.Create(ctx, sec)).To(Succeed())

			key := types.NamespacedName{Namespace: ns, Name: ReplicationName(kindSecret, "revoked")}
			Eventually(func() error {
				return k8sClient.Get(ctx, key, &r8rv1alpha1.Replication{})
			}, timeout, interval).Should(Succeed())

			// Remove the request annotations.
			got := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sec), got)).To(Succeed())
			got.Annotations = nil
			Expect(k8sClient.Update(ctx, got)).To(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, &r8rv1alpha1.Replication{})
				return err != nil
			}, timeout, interval).Should(BeTrue(), "Replication must be deleted")
			Eventually(func(g Gomega) {
				fresh := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sec), fresh)).To(Succeed())
				g.Expect(fresh.Finalizers).NotTo(ContainElement(Finalizer))
			}, timeout, interval).Should(Succeed(), "finalizer must be released")
		})
	})

	Context("source deletion", func() {
		It("blocks until the Replication is gone, then completes (finalizer handshake)", func() {
			inventory.set(readyCluster(spokeA, prodLabels()))
			Expect(k8sClient.Create(ctx, allowPolicy("allow-"+ns, ns, ns))).To(Succeed())

			sec := annotatedSecret(ns, "doomed", map[string]string{
				annotations.KeyReplicate:      annotations.ValueTrue,
				annotations.KeyTargetClusters: selectorProd,
			})
			Expect(k8sClient.Create(ctx, sec)).To(Succeed())

			key := types.NamespacedName{Namespace: ns, Name: ReplicationName(kindSecret, "doomed")}
			Eventually(func(g Gomega) {
				fresh := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sec), fresh)).To(Succeed())
				g.Expect(fresh.Finalizers).To(ContainElement(Finalizer))
				g.Expect(k8sClient.Get(ctx, key, &r8rv1alpha1.Replication{})).To(Succeed())
			}, timeout, interval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, sec)).To(Succeed())

			// Without an engine finalizer the Replication deletes promptly and
			// the source finalizer is released, completing the deletion.
			Eventually(func() bool {
				return k8sClient.Get(ctx, key, &r8rv1alpha1.Replication{}) != nil
			}, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				return k8sClient.Get(ctx, client.ObjectKeyFromObject(sec), &corev1.Secret{}) != nil
			}, timeout, interval).Should(BeTrue())
		})
	})

	Context("cluster inventory changes", func() {
		It("re-resolves targets when notified", func() {
			inventory.set(readyCluster(spokeA, prodLabels()))
			Expect(k8sClient.Create(ctx, allowPolicy("allow-"+ns, ns, ns))).To(Succeed())

			sec := annotatedSecret(ns, "growing", map[string]string{
				annotations.KeyReplicate:      annotations.ValueTrue,
				annotations.KeyTargetClusters: selectorProd,
			})
			Expect(k8sClient.Create(ctx, sec)).To(Succeed())

			key := types.NamespacedName{Namespace: ns, Name: ReplicationName(kindSecret, "growing")}
			Eventually(func(g Gomega) {
				rep := &r8rv1alpha1.Replication{}
				g.Expect(k8sClient.Get(ctx, key, rep)).To(Succeed())
				g.Expect(rep.Spec.ResolvedTargets).To(HaveLen(1))
			}, timeout, interval).Should(Succeed())

			// A new matching cluster joins the fleet.
			inventory.set(
				readyCluster(spokeA, prodLabels()),
				readyCluster("spoke-c", prodLabels()),
			)
			reconciler.NotifyClusterInventoryChanged()

			Eventually(func(g Gomega) {
				rep := &r8rv1alpha1.Replication{}
				g.Expect(k8sClient.Get(ctx, key, rep)).To(Succeed())
				g.Expect(rep.Spec.ResolvedTargets).To(ConsistOf(
					r8rv1alpha1.ResolvedTarget{ClusterName: spokeA, Namespaces: []string{ns}},
					r8rv1alpha1.ResolvedTarget{ClusterName: "spoke-c", Namespaces: []string{ns}},
				))
			}, timeout, interval).Should(Succeed())
		})
	})
})
