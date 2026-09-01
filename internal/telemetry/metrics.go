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

// Package telemetry holds the operator's Prometheus metrics, the event
// rate limiter, and the readiness gate (observability-operations spec).
//
// # Cardinality rule
//
// Metric labels are BOUNDED: only cluster, namespace, kind, and small
// enumerations (dimension, policy, action, result, provider) may appear as
// label values — never object names or any other unbounded identifier. Object
// names belong in events, not metrics. A unit test walks every registered
// k8s_r8r_* metric and enforces this.
//
// Reconcile result counters, reconcile duration histograms, and workqueue
// depth gauges are exposed by controller-runtime per controller
// (controller_runtime_reconcile_*, workqueue_*) and are deliberately not
// duplicated here.
//
// # Secret safety
//
// Nothing in this package ever receives object payloads; all inputs are
// names, states, and counts.
package telemetry

import (
	"maps"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Bounded label names (observability-operations spec).
const (
	labelCluster   = "cluster"
	labelDimension = "dimension"
	labelPolicy    = "policy"
	labelAction    = "action"
	labelResult    = "result"
	// labelProvider is the discovery provider's registry name
	// ("cluster-api", ...) — a closed set with exactly one value per
	// process.
	labelProvider = "provider"
)

var (
	// policyDenials counts reconcile-time policy denial decisions (the
	// authoritative evaluation, design D4), by denied dimension.
	policyDenials = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "k8s_r8r_policy_denials_total",
		Help: "Reconcile-time policy denial decisions, labeled by the denied policy dimension.",
	}, []string{labelDimension})

	// webhookDenials counts advisory admission-webhook denials (design D6),
	// by denied dimension ("annotations" for malformed requests).
	webhookDenials = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "k8s_r8r_webhook_denials_total",
		Help: "Advisory admission webhook denials, labeled by the denied policy dimension (or \"annotations\" for malformed requests).",
	}, []string{labelDimension})

	// conflicts counts conflict decisions on existing unmanaged target
	// objects (design D7), by the conflict policy that resolved them.
	conflicts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "k8s_r8r_conflicts_total",
		Help: "Conflicts with pre-existing target objects, labeled by the resolving conflict policy (fail, overwrite, adopt).",
	}, []string{labelPolicy})

	// revocations counts policy revocations applied to targets with
	// recorded replicas, by revocation action.
	revocations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "k8s_r8r_revocations_total",
		Help: "Policy revocations applied to targets with existing replicas, labeled by action (retain, delete).",
	}, []string{labelAction})

	// driftEvents counts spoke informer events on managed replicas that
	// enqueued a reconcile (drift candidates, including apply echoes).
	driftEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "k8s_r8r_drift_events_total",
		Help: "Spoke informer events on managed replicas that triggered a reconcile (drift candidates, including the engine's own apply echoes), labeled by target cluster.",
	}, []string{labelCluster})

	// spokeBootstraps counts spoke bootstrap attempts by result.
	spokeBootstraps = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "k8s_r8r_spoke_bootstraps_total",
		Help: "Spoke bootstrap attempts (namespace, ServiceAccount, RBAC, first token), labeled by result (success, failure).",
	}, []string{labelResult})

	// tokenRotations counts spoke ServiceAccount token mints by result.
	tokenRotations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "k8s_r8r_token_rotations_total",
		Help: "Spoke ServiceAccount token mints (initial and rotations), labeled by result (success, failure).",
	}, []string{labelResult})
)

// IncPolicyDenial records one reconcile-time policy denial decision.
func IncPolicyDenial(dimension string) { policyDenials.WithLabelValues(dimension).Inc() }

// IncWebhookDenial records one advisory webhook denial.
func IncWebhookDenial(dimension string) { webhookDenials.WithLabelValues(dimension).Inc() }

// IncConflict records one conflict decision (policy: fail, overwrite, adopt).
func IncConflict(policy string) { conflicts.WithLabelValues(policy).Inc() }

// IncRevocation records one applied revocation (action: retain, delete).
func IncRevocation(action string) { revocations.WithLabelValues(action).Inc() }

// IncDriftEvent records one drift-candidate informer event for a cluster.
func IncDriftEvent(cluster string) { driftEvents.WithLabelValues(cluster).Inc() }

// IncSpokeBootstrap records one spoke bootstrap attempt.
func IncSpokeBootstrap(success bool) { spokeBootstraps.WithLabelValues(resultLabel(success)).Inc() }

// IncTokenRotation records one token mint attempt.
func IncTokenRotation(success bool) { tokenRotations.WithLabelValues(resultLabel(success)).Inc() }

func resultLabel(success bool) string {
	if success {
		return "success"
	}
	return "failure"
}

// ReplicaCounts is the per-(owner, cluster) replica tally reported by the
// engine after each reconcile.
type ReplicaCounts struct {
	Desired float64
	Ready   float64
	Failed  float64
}

// replicaAggregator sums per-owner per-cluster replica tallies into
// cluster-labeled gauges. Owners are Replication namespace/name keys held
// only in memory — they never become label values (cardinality rule).
type replicaAggregator struct {
	mu      sync.Mutex
	byOwner map[string]map[string]ReplicaCounts

	desiredDesc *prometheus.Desc
	readyDesc   *prometheus.Desc
	failedDesc  *prometheus.Desc
}

func newReplicaAggregator() *replicaAggregator {
	return &replicaAggregator{
		byOwner: map[string]map[string]ReplicaCounts{},
		desiredDesc: prometheus.NewDesc("k8s_r8r_replicas_desired",
			"Desired replicas across all Replications, labeled by target cluster.", []string{labelCluster}, nil),
		readyDesc: prometheus.NewDesc("k8s_r8r_replicas_ready",
			"Ready replicas across all Replications, labeled by target cluster.", []string{labelCluster}, nil),
		failedDesc: prometheus.NewDesc("k8s_r8r_replicas_failed",
			"Non-ready replicas across all Replications, labeled by target cluster.", []string{labelCluster}, nil),
	}
}

// Describe implements prometheus.Collector.
func (a *replicaAggregator) Describe(ch chan<- *prometheus.Desc) {
	ch <- a.desiredDesc
	ch <- a.readyDesc
	ch <- a.failedDesc
}

// Collect implements prometheus.Collector: it sums the per-owner tallies
// per cluster at scrape time.
func (a *replicaAggregator) Collect(ch chan<- prometheus.Metric) {
	a.mu.Lock()
	sums := map[string]ReplicaCounts{}
	for _, perCluster := range a.byOwner {
		for cluster, c := range perCluster {
			s := sums[cluster]
			s.Desired += c.Desired
			s.Ready += c.Ready
			s.Failed += c.Failed
			sums[cluster] = s
		}
	}
	a.mu.Unlock()
	for cluster, s := range sums {
		ch <- prometheus.MustNewConstMetric(a.desiredDesc, prometheus.GaugeValue, s.Desired, cluster)
		ch <- prometheus.MustNewConstMetric(a.readyDesc, prometheus.GaugeValue, s.Ready, cluster)
		ch <- prometheus.MustNewConstMetric(a.failedDesc, prometheus.GaugeValue, s.Failed, cluster)
	}
}

var replicas = newReplicaAggregator()

// ObserveReplicas replaces the per-cluster replica tally attributed to one
// owner (a Replication, keyed by namespace/name). Clusters absent from
// perCluster are cleared for that owner.
func ObserveReplicas(owner string, perCluster map[string]ReplicaCounts) {
	replicas.mu.Lock()
	defer replicas.mu.Unlock()
	if len(perCluster) == 0 {
		delete(replicas.byOwner, owner)
		return
	}
	cp := make(map[string]ReplicaCounts, len(perCluster))
	maps.Copy(cp, perCluster)
	replicas.byOwner[owner] = cp
}

// ForgetReplicas drops all tallies attributed to one owner (Replication
// deleted or gone).
func ForgetReplicas(owner string) {
	replicas.mu.Lock()
	defer replicas.mu.Unlock()
	delete(replicas.byOwner, owner)
}

// Connectivity gauge value mapping (documented contract):
//
//	0 = Unreachable, 1 = Degraded, 2 = Reachable
const (
	ConnectivityUnreachable float64 = 0
	ConnectivityDegraded    float64 = 1
	ConnectivityReachable   float64 = 2
)

// ConnectivityValue maps a cluster.State string to its gauge value.
func ConnectivityValue(state string) float64 {
	switch state {
	case "Reachable":
		return ConnectivityReachable
	case "Degraded":
		return ConnectivityDegraded
	default:
		return ConnectivityUnreachable
	}
}

// clusterCollector renders the cluster runtime manager's connectivity
// snapshot as gauges at scrape time. The source is injected (from
// cluster.Manager.Snapshot via SetClusterSnapshot) to keep this package
// free of internal dependencies.
type clusterCollector struct {
	connDesc  *prometheus.Desc
	countDesc *prometheus.Desc
}

var clusterSource struct {
	mu sync.Mutex
	fn func() map[string]float64
}

// SetClusterSnapshot wires the connectivity snapshot source: a function
// returning, per registered cluster, the connectivity gauge value
// (ConnectivityValue applied to cluster.Manager.Snapshot).
func SetClusterSnapshot(fn func() map[string]float64) {
	clusterSource.mu.Lock()
	defer clusterSource.mu.Unlock()
	clusterSource.fn = fn
}

func newClusterCollector() *clusterCollector {
	return &clusterCollector{
		connDesc: prometheus.NewDesc("k8s_r8r_cluster_connectivity",
			"Connectivity of each registered target cluster: 0 unreachable, 1 degraded, 2 reachable.", []string{labelCluster}, nil),
		countDesc: prometheus.NewDesc("k8s_r8r_clusters",
			"Number of target clusters with a registered runtime.", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *clusterCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.connDesc
	ch <- c.countDesc
}

// Collect implements prometheus.Collector.
func (c *clusterCollector) Collect(ch chan<- prometheus.Metric) {
	clusterSource.mu.Lock()
	fn := clusterSource.fn
	clusterSource.mu.Unlock()
	if fn == nil {
		return
	}
	snap := fn()
	ch <- prometheus.MustNewConstMetric(c.countDesc, prometheus.GaugeValue, float64(len(snap)))
	for cluster, v := range snap {
		ch <- prometheus.MustNewConstMetric(c.connDesc, prometheus.GaugeValue, v, cluster)
	}
}

// DiscoveryState is the discovery provider's own health snapshot: whether
// its inventory watch is established, and how many clusters it currently
// knows about.
//
// This is deliberately separate from k8s_r8r_clusters, which counts
// *runtimes* registered with the cluster manager — three layers downstream
// of discovery, and therefore 0 both when discovery is structurally broken
// and when the fleet is genuinely empty. Up distinguishes the two.
type DiscoveryState struct {
	// Provider is the discovery provider's registry name (bounded label).
	Provider string
	// Up reports whether the provider's inventory watch is established.
	Up bool
	// Clusters is the size of the provider's own inventory (List()).
	Clusters int
}

// discoveryCollector renders the discovery provider's health snapshot as
// gauges at scrape time. The source is injected (SetDiscoverySnapshot) to
// keep this package free of internal dependencies, exactly like
// SetClusterSnapshot.
type discoveryCollector struct {
	upDesc       *prometheus.Desc
	clustersDesc *prometheus.Desc
}

var discoverySource struct {
	mu sync.Mutex
	fn func() DiscoveryState
}

// SetDiscoverySnapshot wires the discovery-health source: a function
// returning the configured provider's name, watch state, and inventory size.
func SetDiscoverySnapshot(fn func() DiscoveryState) {
	discoverySource.mu.Lock()
	defer discoverySource.mu.Unlock()
	discoverySource.fn = fn
}

func newDiscoveryCollector() *discoveryCollector {
	return &discoveryCollector{
		upDesc: prometheus.NewDesc("k8s_r8r_discovery_up",
			"1 when the configured discovery provider's inventory watch is established, 0 otherwise. Distinguishes broken discovery from an empty fleet.", []string{labelProvider}, nil),
		clustersDesc: prometheus.NewDesc("k8s_r8r_discovery_clusters",
			"Number of clusters in the discovery provider's own inventory (distinct from k8s_r8r_clusters, which counts registered runtimes).", []string{labelProvider}, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *discoveryCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.upDesc
	ch <- c.clustersDesc
}

// Collect implements prometheus.Collector.
func (c *discoveryCollector) Collect(ch chan<- prometheus.Metric) {
	discoverySource.mu.Lock()
	fn := discoverySource.fn
	discoverySource.mu.Unlock()
	if fn == nil {
		return
	}
	s := fn()
	up := 0.0
	if s.Up {
		up = 1
	}
	ch <- prometheus.MustNewConstMetric(c.upDesc, prometheus.GaugeValue, up, s.Provider)
	ch <- prometheus.MustNewConstMetric(c.clustersDesc, prometheus.GaugeValue, float64(s.Clusters), s.Provider)
}

func init() {
	metrics.Registry.MustRegister(
		policyDenials,
		webhookDenials,
		conflicts,
		revocations,
		driftEvents,
		spokeBootstraps,
		tokenRotations,
		replicas,
		newClusterCollector(),
		newDiscoveryCollector(),
	)
}
