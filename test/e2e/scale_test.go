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

// Task 10.3: scale sanity — a single source fanned out to many namespaces on
// both spokes must keep the Replication object small (design D8: summary
// counts, non-ready detail only, capped) and must not churn status while
// healthy. The suite runs with the default --spoke-resync (10h), so any
// resourceVersion movement during the quiet window would be genuine churn.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"

	"github.com/moeritze/k8s-r8r/internal/controller/request"
)

// scaleNamespaces is the fanout width per spoke: 30 namespaces x 2 spokes =
// 60 replica slots from one source.
const scaleNamespaces = 30

func TestScaleFanout(t *testing.T) {
	g := NewWithT(t)
	const srcNS = "e2e-scale"
	const secretName = "fanout"
	repName := request.ReplicationName("Secret", secretName)
	wantSlots := int32(scaleNamespaces * len(spokeClients))

	targets := make([]string, 0, scaleNamespaces)
	for i := range scaleNamespaces {
		targets = append(targets, fmt.Sprintf("e2e-scale-%02d", i))
	}

	g.Expect(ensureNamespace(hubClient, srcNS)).To(Succeed())
	g.Expect(applyPolicy(policySpec{
		name:             "e2e-scale-policy",
		sourceNamespaces: []string{srcNS},
		sourceKinds:      []string{"Secret"},
		targetNamespaces: targets,
		allowNSCreation:  true,
	})).To(Succeed())
	t.Cleanup(func() { _ = deletePolicy("e2e-scale-policy") })

	_, err := createAnnotatedSecret(srcNS, secretName,
		map[string][]byte{"payload": []byte("scale-me")},
		map[string]string{"r8r.io/target-namespaces": strings.Join(targets, ",")})
	g.Expect(err).NotTo(HaveOccurred())

	t.Run("fanout converges with compact status", func(t *testing.T) {
		g := NewWithT(t)
		g.Eventually(func(g Gomega) {
			rep, err := getReplication(srcNS, repName)
			g.Expect(err).NotTo(HaveOccurred())
			cond := readyCondition(rep)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(rep.Status.Summary.DesiredTargets).To(Equal(wantSlots))
			g.Expect(rep.Status.Summary.ReadyTargets).To(Equal(wantSlots))
			g.Expect(rep.Status.Summary.FailedTargets).To(BeZero())
		}, 6*time.Minute, replPoll).Should(Succeed())

		rep, err := getReplication(srcNS, repName)
		g.Expect(err).NotTo(HaveOccurred())

		// Healthy targets never appear as per-target detail (design D8).
		g.Expect(rep.Status.NonReadyTargets).To(BeEmpty(), "nonReadyTargets empty when healthy")
		g.Expect(rep.Status.NonReadyOverflow).To(BeZero())
		g.Expect(rep.Status.Inventory).To(HaveLen(int(wantSlots)))

		// The whole object must stay far below etcd/api object limits
		// (~1.5MiB): with 60 slots the serialized object is expected in the
		// tens of KB.
		raw, err := json.Marshal(rep)
		g.Expect(err).NotTo(HaveOccurred())
		t.Logf("Replication object size at %d slots: %d bytes", wantSlots, len(raw))
		g.Expect(len(raw)).To(BeNumerically("<", 100*1024),
			"Replication object must stay well under object-size limits")

		// Spot-check replicas landed in first and last namespace.
		for spokeName, spoke := range spokeClients {
			for _, ns := range []string{targets[0], targets[len(targets)-1]} {
				_, err := spokeSecret(spoke, ns, secretName)
				g.Expect(err).NotTo(HaveOccurred(), "replica %s on %s", ns, spokeName)
			}
		}
	})

	t.Run("no status churn while healthy", func(t *testing.T) {
		g := NewWithT(t)
		rep, err := getReplication(srcNS, repName)
		g.Expect(err).NotTo(HaveOccurred())
		baseline := rep.ResourceVersion

		// Two resync-free minutes: with the default 10h drift resync any
		// write in this window is churn, not periodic reconciliation.
		g.Consistently(func(g Gomega) {
			rep, err := getReplication(srcNS, repName)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(rep.ResourceVersion).To(Equal(baseline),
				"status must not be rewritten while nothing changes")
		}, 2*time.Minute, 10*time.Second).Should(Succeed())
	})

	// Cleanup: drop the source; verify the wide fanout drains fully.
	src := &corev1.Secret{}
	g.Expect(hubClient.Get(ctx(), types.NamespacedName{Namespace: srcNS, Name: secretName}, src)).To(Succeed())
	uid := src.UID
	g.Expect(hubClient.Delete(ctx(), src)).To(Succeed())
	g.Eventually(func(g Gomega) {
		_, err := getReplication(srcNS, repName)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		for spokeName, spoke := range spokeClients {
			list, err := replicasBySourceUID(spoke, uid)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(list.Items).To(BeEmpty(), "fanout drained on %s", spokeName)
		}
	}, 6*time.Minute, replPoll).Should(Succeed())
}
