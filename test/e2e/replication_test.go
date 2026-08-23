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

// Task 10.1: replication lifecycle — annotate → replicate → drift-repair →
// revoke → GC, plus conflict modes and namespace ensure.

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
	"github.com/moeritze/k8s-r8r/internal/controller/request"
)

const (
	replTimeout = 3 * time.Minute
	replPoll    = 2 * time.Second
)

// expectReplicaOnSpokes asserts the replica exists on every spoke with the
// exact source payload, ownership labels, and matching source-hash.
func expectReplicaOnSpokes(g Gomega, src *corev1.Secret, namespace, name, wantHash string) {
	for spokeName, spoke := range spokeClients {
		replica, err := spokeSecret(spoke, namespace, name)
		g.Expect(err).NotTo(HaveOccurred(), "replica on %s", spokeName)
		g.Expect(replica.Data).To(Equal(src.Data), "payload on %s", spokeName)
		g.Expect(replica.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "k8s-r8r"))
		g.Expect(replica.Labels).To(HaveKeyWithValue("r8r.io/source-cluster", "hub"))
		g.Expect(replica.Labels).To(HaveKeyWithValue("r8r.io/source-namespace", src.Namespace))
		g.Expect(replica.Labels).To(HaveKeyWithValue("r8r.io/source-name", src.Name))
		g.Expect(replica.Labels).To(HaveKeyWithValue("r8r.io/source-kind", "Secret"))
		g.Expect(replica.Labels).To(HaveKeyWithValue("r8r.io/source-uid", string(src.UID)))
		g.Expect(replica.Annotations).To(HaveKeyWithValue("r8r.io/source-hash", wantHash))
		// Request annotations never propagate to replicas.
		g.Expect(replica.Annotations).NotTo(HaveKey("r8r.io/replicate"))
		g.Expect(replica.Annotations).NotTo(HaveKey("r8r.io/target-clusters"))
	}
}

// TestReplicationLifecycle drives one source through the full happy path:
// fanout, source update convergence, drift repair (edit + delete), and
// annotation-removal GC.
func TestReplicationLifecycle(t *testing.T) {
	g := NewWithT(t)
	const srcNS = "e2e-src"
	const secretName = "db-creds"
	repName := request.ReplicationName("Secret", secretName)

	g.Expect(ensureNamespace(hubClient, srcNS)).To(Succeed())
	g.Expect(applyPolicy(policySpec{
		name:             "e2e-src-policy",
		sourceNamespaces: []string{srcNS},
		sourceKinds:      []string{"Secret", "ConfigMap"},
		targetNamespaces: []string{srcNS},
		allowNSCreation:  true,
	})).To(Succeed())
	t.Cleanup(func() { _ = deletePolicy("e2e-src-policy") })

	src, err := createAnnotatedSecret(srcNS, secretName,
		map[string][]byte{"username": []byte("app"), "password": []byte("s3cr3t")}, nil)
	g.Expect(err).NotTo(HaveOccurred())

	t.Run("replicates to all selected spokes", func(t *testing.T) {
		g := NewWithT(t)
		var hash string
		g.Eventually(func(g Gomega) {
			rep, err := getReplication(srcNS, repName)
			g.Expect(err).NotTo(HaveOccurred())
			cond := readyCondition(rep)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(rep.Status.Summary.DesiredTargets).To(Equal(int32(2)))
			g.Expect(rep.Status.Summary.ReadyTargets).To(Equal(int32(2)))
			g.Expect(rep.Status.SourceHash).To(HavePrefix("sha256:"))
			hash = rep.Status.SourceHash
			expectReplicaOnSpokes(g, src, srcNS, secretName, hash)
		}, replTimeout, replPoll).Should(Succeed())

		// The engine-created target namespace carries the managed-by label.
		for spokeName, spoke := range spokeClients {
			ns := &corev1.Namespace{}
			g.Expect(spoke.Get(ctx(), types.NamespacedName{Name: srcNS}, ns)).To(Succeed())
			g.Expect(ns.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "k8s-r8r"),
				"namespace label on %s", spokeName)
		}

		// The source only ever gains the operator finalizer, nothing else.
		fresh := &corev1.Secret{}
		g.Expect(hubClient.Get(ctx(), types.NamespacedName{Namespace: srcNS, Name: secretName}, fresh)).To(Succeed())
		g.Expect(fresh.Finalizers).To(ContainElement("r8r.io/finalizer"))
		g.Expect(fresh.Labels).NotTo(HaveKey("app.kubernetes.io/managed-by"))
	})

	t.Run("source update converges replicas", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(hubClient.Get(ctx(), types.NamespacedName{Namespace: srcNS, Name: secretName}, src)).To(Succeed())
		src.Data["password"] = []byte("rotated")
		g.Expect(hubClient.Update(ctx(), src)).To(Succeed())

		g.Eventually(func(g Gomega) {
			rep, err := getReplication(srcNS, repName)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(rep.Status.Summary.ReadyTargets).To(Equal(int32(2)))
			expectReplicaOnSpokes(g, src, srcNS, secretName, rep.Status.SourceHash)
			for spokeName, spoke := range spokeClients {
				replica, err := spokeSecret(spoke, srcNS, secretName)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(replica.Data["password"]).To(Equal([]byte("rotated")), "converged on %s", spokeName)
			}
		}, replTimeout, replPoll).Should(Succeed())
	})

	t.Run("drift repair on replica edit", func(t *testing.T) {
		g := NewWithT(t)
		spoke := spokeClients[spoke1]
		replica, err := spokeSecret(spoke, srcNS, secretName)
		g.Expect(err).NotTo(HaveOccurred())
		replica.Data["password"] = []byte("tampered")
		g.Expect(spoke.Update(ctx(), replica)).To(Succeed())

		g.Eventually(func(g Gomega) {
			repaired, err := spokeSecret(spoke, srcNS, secretName)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(repaired.Data["password"]).To(Equal([]byte("rotated")), "drift repaired")
		}, replTimeout, replPoll).Should(Succeed())
	})

	t.Run("drift repair on replica delete", func(t *testing.T) {
		g := NewWithT(t)
		spoke := spokeClients[spoke2]
		replica, err := spokeSecret(spoke, srcNS, secretName)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(spoke.Delete(ctx(), replica)).To(Succeed())

		g.Eventually(func(g Gomega) {
			recreated, err := spokeSecret(spoke, srcNS, secretName)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(recreated.Data["password"]).To(Equal([]byte("rotated")), "replica recreated")
		}, replTimeout, replPoll).Should(Succeed())
	})

	t.Run("annotation removal garbage-collects replicas", func(t *testing.T) {
		g := NewWithT(t)
		fresh := &corev1.Secret{}
		g.Expect(hubClient.Get(ctx(), types.NamespacedName{Namespace: srcNS, Name: secretName}, fresh)).To(Succeed())
		uid := fresh.UID
		delete(fresh.Annotations, "r8r.io/replicate")
		delete(fresh.Annotations, "r8r.io/target-clusters")
		g.Expect(hubClient.Update(ctx(), fresh)).To(Succeed())

		g.Eventually(func(g Gomega) {
			// Replicas gone from both spokes.
			for spokeName, spoke := range spokeClients {
				list, err := replicasBySourceUID(spoke, uid)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(list.Items).To(BeEmpty(), "replicas GC'd on %s", spokeName)
			}
			// Replication object fully gone.
			_, err := getReplication(srcNS, repName)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "Replication deleted")
			// Source released: finalizer removed.
			released := &corev1.Secret{}
			g.Expect(hubClient.Get(ctx(), types.NamespacedName{Namespace: srcNS, Name: secretName}, released)).To(Succeed())
			g.Expect(released.Finalizers).NotTo(ContainElement("r8r.io/finalizer"))
		}, replTimeout, replPoll).Should(Succeed())
	})
}

// TestConflictFail verifies the default conflict policy: an unmanaged object
// occupying the replica name is never touched and the Replication reports a
// Conflict for exactly that target.
func TestConflictFail(t *testing.T) {
	g := NewWithT(t)
	const srcNS = "e2e-conflict"
	const secretName = "contested"
	repName := request.ReplicationName("Secret", secretName)

	g.Expect(ensureNamespace(hubClient, srcNS)).To(Succeed())
	g.Expect(applyPolicy(policySpec{
		name:             "e2e-conflict-policy",
		sourceNamespaces: []string{srcNS},
		sourceKinds:      []string{"Secret"},
		targetNamespaces: []string{srcNS},
		allowNSCreation:  true,
	})).To(Succeed())
	t.Cleanup(func() { _ = deletePolicy("e2e-conflict-policy") })

	// Pre-create the unmanaged victim on spoke-1 only.
	victimSpoke := spokeClients[spoke1]
	g.Expect(ensureNamespace(victimSpoke, srcNS)).To(Succeed())
	victim := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: srcNS, Name: secretName},
		Data:       map[string][]byte{"owner": []byte("someone-else")},
	}
	g.Expect(victimSpoke.Create(ctx(), victim)).To(Succeed())

	src, err := createAnnotatedSecret(srcNS, secretName,
		map[string][]byte{"owner": []byte("k8s-r8r")}, nil)
	g.Expect(err).NotTo(HaveOccurred())
	_ = src

	g.Eventually(func(g Gomega) {
		rep, err := getReplication(srcNS, repName)
		g.Expect(err).NotTo(HaveOccurred())
		cond := readyCondition(rep)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(cond.Reason).To(Equal(r8rv1alpha1.ReasonConflict))
		g.Expect(rep.Status.Summary.FailedTargets).To(Equal(int32(1)))
		g.Expect(rep.Status.Summary.ReadyTargets).To(Equal(int32(1)), "unaffected spoke still replicates")
		var reasons []string
		for _, nrt := range rep.Status.NonReadyTargets {
			if nrt.ClusterName == spoke1 {
				reasons = append(reasons, nrt.Reason)
			}
		}
		g.Expect(reasons).To(ContainElement(r8rv1alpha1.ReasonConflict))
	}, replTimeout, replPoll).Should(Succeed())

	// The victim is untouched: payload intact, no ownership marks.
	untouched, err := spokeSecret(victimSpoke, srcNS, secretName)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(untouched.Data).To(Equal(map[string][]byte{"owner": []byte("someone-else")}))
	g.Expect(untouched.Labels).NotTo(HaveKey("app.kubernetes.io/managed-by"))
	g.Expect(untouched.Annotations).NotTo(HaveKey("r8r.io/source-hash"))

	// Cleanup: delete the source; the victim must survive the teardown too.
	fresh := &corev1.Secret{}
	g.Expect(hubClient.Get(ctx(), types.NamespacedName{Namespace: srcNS, Name: secretName}, fresh)).To(Succeed())
	g.Expect(hubClient.Delete(ctx(), fresh)).To(Succeed())
	g.Eventually(func(g Gomega) {
		_, err := getReplication(srcNS, repName)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	}, replTimeout, replPoll).Should(Succeed())
	survivor, err := spokeSecret(victimSpoke, srcNS, secretName)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(survivor.Data).To(Equal(map[string][]byte{"owner": []byte("someone-else")}))
}

// TestNamespaceEnsure verifies both halves of the namespace-ensure gate:
// missing target namespace + allowNamespaceCreation=false fails with
// NamespaceMissing, and flipping the option to true creates the namespace
// (labeled) and places the replica.
func TestNamespaceEnsure(t *testing.T) {
	g := NewWithT(t)
	const srcNS = "e2e-nsensure"
	const targetNS = "e2e-nsensure-target"
	const secretName = "ns-gated"
	repName := request.ReplicationName("Secret", secretName)

	g.Expect(ensureNamespace(hubClient, srcNS)).To(Succeed())
	pol := policySpec{
		name:             "e2e-nsensure-policy",
		sourceNamespaces: []string{srcNS},
		sourceKinds:      []string{"Secret"},
		targetNamespaces: []string{targetNS},
		allowNSCreation:  false,
	}
	g.Expect(applyPolicy(pol)).To(Succeed())
	t.Cleanup(func() { _ = deletePolicy(pol.name) })

	_, err := createAnnotatedSecret(srcNS, secretName,
		map[string][]byte{"k": []byte("v")},
		map[string]string{"r8r.io/target-namespaces": targetNS})
	g.Expect(err).NotTo(HaveOccurred())

	t.Run("creation denied without allowNamespaceCreation", func(t *testing.T) {
		g := NewWithT(t)
		g.Eventually(func(g Gomega) {
			rep, err := getReplication(srcNS, repName)
			g.Expect(err).NotTo(HaveOccurred())
			cond := readyCondition(rep)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("NamespaceMissing"))
		}, replTimeout, replPoll).Should(Succeed())
		for spokeName, spoke := range spokeClients {
			err := spoke.Get(ctx(), types.NamespacedName{Name: targetNS}, &corev1.Namespace{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "namespace must not exist on %s", spokeName)
		}
	})

	t.Run("creation permitted with allowNamespaceCreation", func(t *testing.T) {
		g := NewWithT(t)
		pol.allowNSCreation = true
		g.Expect(applyPolicy(pol)).To(Succeed())
		g.Eventually(func(g Gomega) {
			rep, err := getReplication(srcNS, repName)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(rep.Status.Summary.ReadyTargets).To(Equal(int32(2)))
			for spokeName, spoke := range spokeClients {
				ns := &corev1.Namespace{}
				g.Expect(spoke.Get(ctx(), types.NamespacedName{Name: targetNS}, ns)).To(Succeed())
				g.Expect(ns.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "k8s-r8r"),
					"created namespace labeled on %s", spokeName)
				_, err := spokeSecret(spoke, targetNS, secretName)
				g.Expect(err).NotTo(HaveOccurred())
			}
		}, replTimeout, replPoll).Should(Succeed())
	})
}

// TestPolicyDeleteRevocation verifies live revocation: deleting the only
// permitting policy removes the replicas (revocationPolicy Delete, the
// default) and reports the denial.
func TestPolicyDeleteRevocation(t *testing.T) {
	g := NewWithT(t)
	const srcNS = "e2e-revoke"
	const secretName = "revocable"
	repName := request.ReplicationName("Secret", secretName)

	g.Expect(ensureNamespace(hubClient, srcNS)).To(Succeed())
	g.Expect(applyPolicy(policySpec{
		name:             "e2e-revoke-policy",
		sourceNamespaces: []string{srcNS},
		sourceKinds:      []string{"Secret"},
		targetNamespaces: []string{srcNS},
		allowNSCreation:  true,
		revocationPolicy: r8rv1alpha1.RevocationPolicyDelete,
	})).To(Succeed())
	t.Cleanup(func() { _ = deletePolicy("e2e-revoke-policy") })

	src, err := createAnnotatedSecret(srcNS, secretName, map[string][]byte{"k": []byte("v")}, nil)
	g.Expect(err).NotTo(HaveOccurred())

	g.Eventually(func(g Gomega) {
		rep, err := getReplication(srcNS, repName)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(rep.Status.Summary.ReadyTargets).To(Equal(int32(2)))
	}, replTimeout, replPoll).Should(Succeed())

	g.Expect(deletePolicy("e2e-revoke-policy")).To(Succeed())

	g.Eventually(func(g Gomega) {
		for spokeName, spoke := range spokeClients {
			list, err := replicasBySourceUID(spoke, src.UID)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(list.Items).To(BeEmpty(), "revoked replicas deleted on %s", spokeName)
		}
		rep, err := getReplication(srcNS, repName)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(rep.Status.Inventory).To(BeEmpty(), "inventory released")
		cond := readyCondition(rep)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(cond.Reason).To(BeElementOf(
			r8rv1alpha1.ReasonPolicyDenied, r8rv1alpha1.ReasonPolicyRevoked))
	}, replTimeout, replPoll).Should(Succeed())
}

// TestSourceDeleteCleanup verifies the finalizer handshake on source
// deletion: the fleet is cleaned before the source and its Replication are
// released, and nothing stays behind on the spokes.
func TestSourceDeleteCleanup(t *testing.T) {
	g := NewWithT(t)
	const srcNS = "e2e-delete"
	const secretName = "doomed"
	repName := request.ReplicationName("Secret", secretName)

	g.Expect(ensureNamespace(hubClient, srcNS)).To(Succeed())
	g.Expect(applyPolicy(policySpec{
		name:             "e2e-delete-policy",
		sourceNamespaces: []string{srcNS},
		sourceKinds:      []string{"Secret"},
		targetNamespaces: []string{srcNS},
		allowNSCreation:  true,
	})).To(Succeed())
	t.Cleanup(func() { _ = deletePolicy("e2e-delete-policy") })

	src, err := createAnnotatedSecret(srcNS, secretName, map[string][]byte{"k": []byte("v")}, nil)
	g.Expect(err).NotTo(HaveOccurred())
	uid := src.UID

	g.Eventually(func(g Gomega) {
		rep, err := getReplication(srcNS, repName)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(rep.Status.Summary.ReadyTargets).To(Equal(int32(2)))
	}, replTimeout, replPoll).Should(Succeed())

	g.Expect(hubClient.Delete(ctx(), src)).To(Succeed())

	g.Eventually(func(g Gomega) {
		// Source fully gone (finalizer released only after fleet cleanup).
		err := hubClient.Get(ctx(), types.NamespacedName{Namespace: srcNS, Name: secretName}, &corev1.Secret{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "source released")
		// Replication gone.
		_, err = getReplication(srcNS, repName)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "Replication released")
		// Nothing orphaned on the spokes.
		for spokeName, spoke := range spokeClients {
			list, err := replicasBySourceUID(spoke, uid)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(list.Items).To(BeEmpty(), "no orphans on %s", spokeName)
		}
	}, replTimeout, replPoll).Should(Succeed())
}
