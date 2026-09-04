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

package telemetry

import (
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// exerciseAll touches every metric so each family appears in Gather output.
func exerciseAll() {
	IncPolicyDenial("targetCluster")
	IncWebhookDenial("annotations")
	IncConflict("fail")
	IncConflict("overwrite")
	IncConflict("adopt")
	IncRevocation("retain")
	IncRevocation("delete")
	IncDriftEvent("spoke-a")
	IncDriftCorrection("spoke-a")
	IncOwnershipLost("spoke-a", OwnershipRepaired)
	IncOwnershipLost("spoke-a", OwnershipDeleted)
	IncOwnershipLost("spoke-a", OwnershipOrphaned)
	IncSpokeBootstrap(true)
	IncSpokeBootstrap(false)
	IncTokenRotation(true)
	IncTokenRotation(false)
	ObserveReplicas("ns/rep", map[string]ReplicaCounts{
		"spoke-a": {Desired: 2, Ready: 1, Failed: 1},
	})
	SetClusterSnapshot(func() map[string]float64 {
		return map[string]float64{"spoke-a": ConnectivityReachable}
	})
	SetDiscoverySnapshot(func() DiscoveryState {
		return DiscoveryState{Provider: "cluster-api", Up: true, Clusters: 1}
	})
}

func gatherOurs(t *testing.T) map[string]*dto.MetricFamily {
	t.Helper()
	fams, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gathering: %v", err)
	}
	out := map[string]*dto.MetricFamily{}
	for _, mf := range fams {
		if strings.HasPrefix(mf.GetName(), "k8s_r8r_") {
			out[mf.GetName()] = mf
		}
	}
	return out
}

// Observability spec: metric labels SHALL be bounded (cluster, namespace,
// kind — never object names). Every registered k8s_r8r_* metric is walked
// and its label names checked against the allowlist; "name"/"object" (or
// anything else unbounded) fail the build.
func TestMetricLabelCardinalityBounded(t *testing.T) {
	exerciseAll()
	allowed := map[string]bool{
		"cluster":   true,
		"namespace": true,
		"kind":      true,
		// Small closed enumerations:
		"dimension": true,
		"policy":    true,
		"action":    true,
		"result":    true,
		// The discovery provider's registry name: one value per process.
		"provider": true,
	}
	fams := gatherOurs(t)
	if len(fams) == 0 {
		t.Fatal("no k8s_r8r_ metric families registered")
	}
	for name, mf := range fams {
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				ln := lp.GetName()
				if ln == "name" || ln == "object" || !allowed[ln] {
					t.Errorf("metric %s carries unbounded/forbidden label %q", name, ln)
				}
			}
		}
	}
}

// The full inventory promised by the observability spec and design D8/D9
// discussion must be present under the k8s_r8r_ prefix.
func TestMetricInventoryComplete(t *testing.T) {
	exerciseAll()
	fams := gatherOurs(t)
	for _, want := range []string{
		"k8s_r8r_replicas_desired",
		"k8s_r8r_replicas_ready",
		"k8s_r8r_replicas_failed",
		"k8s_r8r_policy_denials_total",
		"k8s_r8r_webhook_denials_total",
		"k8s_r8r_conflicts_total",
		"k8s_r8r_revocations_total",
		"k8s_r8r_drift_events_total",
		"k8s_r8r_drift_corrections_total",
		"k8s_r8r_replica_ownership_lost_total",
		"k8s_r8r_cluster_connectivity",
		"k8s_r8r_clusters",
		"k8s_r8r_discovery_up",
		"k8s_r8r_discovery_clusters",
		"k8s_r8r_spoke_bootstraps_total",
		"k8s_r8r_token_rotations_total",
	} {
		if _, ok := fams[want]; !ok {
			t.Errorf("metric family %s not registered/gathered", want)
		}
	}
}

// Replica gauges must sum per cluster across owners, and Forget must clear
// an owner's contribution.
func TestReplicaAggregation(t *testing.T) {
	ObserveReplicas("ns/a", map[string]ReplicaCounts{"agg-spoke": {Desired: 2, Ready: 2}})
	ObserveReplicas("ns/b", map[string]ReplicaCounts{"agg-spoke": {Desired: 1, Failed: 1}})

	if got := replicaValue(t, "k8s_r8r_replicas_desired"); got != 3 {
		t.Errorf("desired = %v, want 3", got)
	}
	if got := replicaValue(t, "k8s_r8r_replicas_ready"); got != 2 {
		t.Errorf("ready = %v, want 2", got)
	}
	if got := replicaValue(t, "k8s_r8r_replicas_failed"); got != 1 {
		t.Errorf("failed = %v, want 1", got)
	}

	ForgetReplicas("ns/b")
	if got := replicaValue(t, "k8s_r8r_replicas_failed"); got != 0 {
		t.Errorf("failed after forget = %v, want 0", got)
	}
	ForgetReplicas("ns/a")
}

func replicaValue(t *testing.T, family string) float64 {
	t.Helper()
	mf, ok := gatherOurs(t)[family]
	if !ok {
		t.Fatalf("family %s not found", family)
	}
	for _, m := range mf.GetMetric() {
		for _, lp := range m.GetLabel() {
			if lp.GetName() == "cluster" && lp.GetValue() == "agg-spoke" {
				return m.GetGauge().GetValue()
			}
		}
	}
	return 0
}

// A broken discovery provider and a genuinely empty fleet must not produce
// the same observation: both show zero clusters, only the up gauge separates
// them (observability-operations: discovery health).
func TestDiscoveryHealthDistinguishesBrokenFromEmpty(t *testing.T) {
	t.Cleanup(func() {
		SetDiscoverySnapshot(func() DiscoveryState {
			return DiscoveryState{Provider: "cluster-api", Up: true, Clusters: 1}
		})
	})

	SetDiscoverySnapshot(func() DiscoveryState {
		return DiscoveryState{Provider: "cluster-api", Up: true, Clusters: 0}
	})
	if got := discoveryValue(t, "k8s_r8r_discovery_up"); got != 1 {
		t.Errorf("empty fleet: up = %v, want 1", got)
	}
	if got := discoveryValue(t, "k8s_r8r_discovery_clusters"); got != 0 {
		t.Errorf("empty fleet: clusters = %v, want 0", got)
	}

	SetDiscoverySnapshot(func() DiscoveryState {
		return DiscoveryState{Provider: "cluster-api", Up: false, Clusters: 0}
	})
	if got := discoveryValue(t, "k8s_r8r_discovery_up"); got != 0 {
		t.Errorf("broken provider: up = %v, want 0", got)
	}
}

func discoveryValue(t *testing.T, family string) float64 {
	t.Helper()
	mf, ok := gatherOurs(t)[family]
	if !ok {
		t.Fatalf("family %s not found", family)
	}
	for _, m := range mf.GetMetric() {
		for _, lp := range m.GetLabel() {
			if lp.GetName() == "provider" && lp.GetValue() == "cluster-api" {
				return m.GetGauge().GetValue()
			}
		}
	}
	t.Fatalf("family %s has no cluster-api sample", family)
	return 0
}

// Connectivity values follow the documented 0/1/2 mapping.
func TestConnectivityValueMapping(t *testing.T) {
	if v := ConnectivityValue("Reachable"); v != 2 {
		t.Errorf("Reachable = %v, want 2", v)
	}
	if v := ConnectivityValue("Degraded"); v != 1 {
		t.Errorf("Degraded = %v, want 1", v)
	}
	if v := ConnectivityValue("Unreachable"); v != 0 {
		t.Errorf("Unreachable = %v, want 0", v)
	}
	if v := ConnectivityValue(""); v != 0 {
		t.Errorf("unknown state = %v, want 0 (fail toward unreachable)", v)
	}
}
