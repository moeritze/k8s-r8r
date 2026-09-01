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

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crcluster "sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
	"github.com/moeritze/k8s-r8r/internal/cluster"
	"github.com/moeritze/k8s-r8r/internal/controller/request"
	"github.com/moeritze/k8s-r8r/internal/discovery"

	// The ClusterAPI provider registers itself as "cluster-api" in the
	// discovery registry via its init function.
	_ "github.com/moeritze/k8s-r8r/internal/discovery/capi"
	"github.com/moeritze/k8s-r8r/internal/engine"
	"github.com/moeritze/k8s-r8r/internal/telemetry"
	r8rwebhook "github.com/moeritze/k8s-r8r/internal/webhook"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(r8rv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// supportedKinds maps --allowed-kinds resource names (lowercase plural, as
// they appear in RBAC) to the GroupVersionKinds the pipeline handles.
var supportedKinds = map[string]schema.GroupVersionKind{
	"secrets":    {Group: "", Version: "v1", Kind: "Secret"},
	"configmaps": {Group: "", Version: "v1", Kind: "ConfigMap"},
}

// parseAllowedKinds turns the --allowed-kinds flag value into the three
// per-component views of the kind allowlist: the request controller's GVK
// allowlist, the engine's kind->GVK map, and the spoke RBAC scope used at
// bootstrap.
func parseAllowedKinds(
	raw string,
) ([]schema.GroupVersionKind, map[string]schema.GroupVersionKind, cluster.RBACScope, error) {
	var allowlist []schema.GroupVersionKind
	kindGVKs := map[string]schema.GroupVersionKind{}
	var scope cluster.RBACScope
	for part := range strings.SplitSeq(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		gvk, ok := supportedKinds[name]
		if !ok {
			return nil, nil, scope, fmt.Errorf(
				"unsupported kind %q in --allowed-kinds (supported: secrets, configmaps)", name)
		}
		if _, dup := kindGVKs[gvk.Kind]; dup {
			continue
		}
		allowlist = append(allowlist, gvk)
		kindGVKs[gvk.Kind] = gvk
		scope.Resources = append(scope.Resources, cluster.ScopedResource{Group: gvk.Group, Resource: name})
	}
	if len(allowlist) == 0 {
		return nil, nil, scope, fmt.Errorf("--allowed-kinds must name at least one kind")
	}
	return allowlist, kindGVKs, scope, nil
}

// stringListFlag is a flag.Value accumulating a list of strings: repeatable
// and comma-separated, so --strip-metadata-keys=a,b and --strip-metadata-keys=a
// --strip-metadata-keys=b are equivalent. Empty entries are dropped.
type stringListFlag []string

// String implements flag.Value.
func (s *stringListFlag) String() string { return strings.Join(*s, ",") }

// Set implements flag.Value.
func (s *stringListFlag) Set(v string) error {
	for part := range strings.SplitSeq(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			*s = append(*s, part)
		}
	}
	return nil
}

// rotatingAuth is an http.RoundTripper that authenticates every request with
// a currently valid short-lived ServiceAccount token from the Rotator, so a
// spoke's clients stay authenticated across token refreshes without ever
// being rebuilt.
type rotatingAuth struct {
	rotator *cluster.Rotator
	next    http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (r *rotatingAuth) RoundTrip(req *http.Request) (*http.Response, error) {
	cfg, err := r.rotator.Config(req.Context())
	if err != nil {
		return nil, fmt.Errorf("refreshing spoke token: %w", err)
	}
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
	return r.next.RoundTrip(req)
}

// spokeWirer bridges discovery events to the per-spoke bootstrap and the
// cluster runtime manager (Register -> bootstrap -> runtime flow):
//
//  1. A discovery Register/Update event for a READY cluster starts a
//     bootstrap goroutine (with backoff retry) for that cluster, once.
//  2. The bootstrap reads the provider's admin kubeconfig from the
//     CredentialRef Secret on the hub (uncached read; used exactly once per
//     bootstrap, design D5), runs cluster.BootstrapSpoke (namespace, narrow
//     SA + RBAC, first token mint) and obtains the token Rotator.
//  3. The spoke's rest config is stripped of the admin credential and wired
//     to the Rotator via a transport middleware, then registered with the
//     cluster runtime manager, which probes it and starts its runtime.
//  4. A Deregister event (or readiness dropping away) cancels any in-flight
//     bootstrap and deregisters the runtime.
type spokeWirer struct {
	ctx      context.Context
	hub      client.Reader
	clusters *cluster.Manager
	scope    cluster.RBACScope

	mu     sync.Mutex
	spokes map[string]context.CancelFunc
}

func newSpokeWirer(
	ctx context.Context, hub client.Reader, clusters *cluster.Manager, scope cluster.RBACScope,
) *spokeWirer {
	return &spokeWirer{
		ctx:      ctx,
		hub:      hub,
		clusters: clusters,
		scope:    scope,
		spokes:   map[string]context.CancelFunc{},
	}
}

// Handle is the discovery.EventHandler entry point. It never blocks: all
// network work happens in per-cluster goroutines.
func (w *spokeWirer) Handle(e discovery.Event) {
	switch e.Type {
	case discovery.EventRegister, discovery.EventUpdate:
		if e.Record.Ready {
			w.ensure(e.Record)
		} else {
			w.drop(e.Record.Name)
		}
	case discovery.EventDeregister:
		w.drop(e.Record.Name)
	}
}

// ensure starts the bootstrap flow for a cluster unless it is already
// bootstrapped or in flight.
func (w *spokeWirer) ensure(rec discovery.ClusterRecord) {
	w.mu.Lock()
	if _, active := w.spokes[rec.Name]; active {
		w.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(w.ctx)
	w.spokes[rec.Name] = cancel
	w.mu.Unlock()
	go w.run(ctx, rec)
}

// drop cancels any in-flight bootstrap and stops the cluster's runtime.
func (w *spokeWirer) drop(name string) {
	w.mu.Lock()
	cancel, active := w.spokes[name]
	delete(w.spokes, name)
	w.mu.Unlock()
	if active {
		cancel()
	}
	w.clusters.Deregister(name)
}

// run bootstraps one spoke, retrying with capped exponential backoff until it
// succeeds or the cluster leaves inventory.
func (w *spokeWirer) run(ctx context.Context, rec discovery.ClusterRecord) {
	const (
		baseDelay = 5 * time.Second
		maxDelay  = 5 * time.Minute
	)
	delay := baseDelay
	for {
		err := w.bootstrap(ctx, rec)
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
		telemetry.IncSpokeBootstrap(false)
		// Errors reference the credential Secret by name only; no secret
		// material ever reaches logs (design D5).
		setupLog.Error(err, "Failed to bootstrap spoke; will retry",
			"cluster", rec.Name, "retryAfter", delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay *= 2; delay > maxDelay {
			delay = maxDelay
		}
	}
}

// bootstrap performs one bootstrap attempt and, on success, registers the
// spoke's rotator-backed rest config with the cluster runtime manager.
func (w *spokeWirer) bootstrap(ctx context.Context, rec discovery.ClusterRecord) error {
	adminCfg, err := cluster.LoadAdminConfig(ctx, w.hub, rec.CredentialRef)
	if err != nil {
		return err
	}
	result, err := cluster.BootstrapSpoke(ctx, adminCfg, w.scope)
	if err != nil {
		return err
	}
	primed, err := result.Rotator.Config(ctx)
	if err != nil {
		return err
	}
	// Steady-state config: host/TLS of the spoke, no static credential;
	// every request authenticates with a fresh short-lived SA token.
	spokeCfg := rest.AnonymousClientConfig(primed)
	rotator := result.Rotator
	spokeCfg.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &rotatingAuth{rotator: rotator, next: rt}
	})
	w.clusters.Register(rec.Name, spokeCfg)
	telemetry.IncSpokeBootstrap(true)
	setupLog.Info("Bootstrapped spoke and registered runtime", "cluster", rec.Name)
	return nil
}

// RBAC needed by main wiring beyond the controllers' own markers: watching
// ClusterAPI Cluster objects for discovery. (Kubeconfig credential Secrets
// are covered by the request controller's secrets get;list;watch.)
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var discoveryProviderName string
	var hubName string
	var allowedKinds string
	var stripMetadataKeys stringListFlag
	var spokeResync time.Duration
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&discoveryProviderName, "discovery-provider", "cluster-api",
		"The cluster discovery provider to use (registered providers: cluster-api).")
	flag.StringVar(&hubName, "hub-name", "hub",
		"The source-cluster identity stamped onto replicas.")
	flag.StringVar(&allowedKinds, "allowed-kinds", "secrets,configmaps",
		"Comma-separated resource names enabled for replication (supported: secrets, configmaps).")
	flag.Var(&stripMetadataKeys, "strip-metadata-keys",
		"Additional label/annotation keys stripped from replicas and excluded from the source hash, "+
			"on top of the built-in foreign-ownership denylist. Comma-separated and repeatable; "+
			"an entry ending in \"/\" is a prefix match. Additive only — built-in entries cannot be removed.")
	flag.DurationVar(&spokeResync, "spoke-resync", 0,
		"Drift resync interval for spoke informers and periodic reconciles (0 uses the engine default of 10h).")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	allowlist, kindGVKs, rbacScope, err := parseAllowedKinds(allowedKinds)
	if err != nil {
		setupLog.Error(err, "Invalid --allowed-kinds")
		os.Exit(1)
	}

	// Process-wide: the renderer and the canonical hash share one denylist,
	// so this must be set before any reconciler runs.
	engine.SetExtraStrippedKeys(stripMetadataKeys)

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("Disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	hubCfg := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(hubCfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		// HA / single-writer semantics (observability-operations spec):
		// with --leader-elect, controller-runtime's lease-based election
		// guarantees exactly one instance runs the reconcilers, the
		// discovery provider, the cluster runtime manager, and the drift
		// detector (all leader-gated runnables). Standby replicas still
		// serve webhooks and probes and take over on lease expiry; state is
		// recovered from CRs and status inventory, so failover neither
		// duplicates nor orphans replicas.
		LeaderElection:   enableLeaderElection,
		LeaderElectionID: "8180352a.r8r.io",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	// The signal-handler context bounds everything outside the manager's own
	// runnable lifecycle (spoke bootstrap goroutines).
	ctx := ctrl.SetupSignalHandler()

	// Discovery provider, selected by name from the registry providers
	// register into (the capi import registers "cluster-api").
	provider, err := discovery.New(discoveryProviderName, discovery.Options{HubConfig: hubCfg})
	if err != nil {
		setupLog.Error(err, "Failed to construct discovery provider", "provider", discoveryProviderName)
		os.Exit(1)
	}

	// Cluster runtime manager: every spoke runtime carries the operator
	// scheme and a cache-wide managed-by label selector, so all spoke watches
	// (the engine's metadata-only drift informers) are filtered server-side
	// to operator-managed objects.
	managedSelector := labels.SelectorFromSet(labels.Set{request.ManagedByLabel: request.ManagedByValue})
	clusterManager := cluster.NewManager(cluster.WithRuntimeFactory(
		cluster.DefaultRuntimeFactory(func(o *crcluster.Options) {
			o.Scheme = scheme
			o.Cache.DefaultLabelSelector = managedSelector
		})))

	// Register -> bootstrap -> runtime flow: discovery events drive one-shot
	// spoke bootstrap (admin kubeconfig used once, then rotated SA tokens)
	// and runtime registration. Subscriptions must precede provider start.
	wirer := newSpokeWirer(ctx, mgr.GetAPIReader(), clusterManager, rbacScope)
	provider.Subscribe(wirer.Handle)

	// Discovery-health metrics, sourced from the provider itself at scrape
	// time. k8s_r8r_clusters counts registered *runtimes* and reads 0 both
	// when discovery is broken and when the fleet is empty;
	// k8s_r8r_discovery_up separates the two.
	telemetry.SetDiscoverySnapshot(func() telemetry.DiscoveryState {
		return telemetry.DiscoveryState{
			Provider: provider.Name(),
			Up:       provider.Watching(),
			Clusters: len(provider.List()),
		}
	})

	// Cluster connectivity + runtime-count metrics, sourced from the
	// runtime manager's snapshot at scrape time (0 unreachable, 1 degraded,
	// 2 reachable).
	telemetry.SetClusterSnapshot(func() map[string]float64 {
		snap := clusterManager.Snapshot()
		out := make(map[string]float64, len(snap))
		for name, conn := range snap {
			out[name] = telemetry.ConnectivityValue(string(conn.State))
		}
		return out
	})

	// Request controller (annotation shim): re-resolves targets on cluster
	// inventory changes via the discovery subscription.
	requestReconciler := &request.Reconciler{
		Inventory: provider,
		Allowlist: allowlist,
	}
	if err := requestReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to set up request controller")
		os.Exit(1)
	}
	provider.Subscribe(requestReconciler.ClusterEventHandler())

	// Replication engine: pushes replicas through SA-token clients from the
	// cluster runtime manager and hooks its lifecycle events for drift
	// informers and re-enqueues.
	// Deliberately the core/v1 recorder, not the (newer) events.k8s.io one:
	// only this recorder populates firstTimestamp/lastTimestamp/count, which
	// `kubectl get events --sort-by=.lastTimestamp` needs (issue #32).
	//nolint:staticcheck // SA1019: deliberate; GetEventRecorder omits the legacy timestamps.
	engineRecorder := mgr.GetEventRecorderFor("replication-engine")
	engineReconciler := &engine.Reconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		Recorder:      engineRecorder,
		Transport:     engine.NewPushTransport(clusterManager, "k8s-r8r"),
		Clusters:      engine.ProviderInventory{Provider: provider},
		ClusterEvents: clusterManager,
		Options: engine.Options{
			HubName:     hubName,
			DriftResync: spokeResync,
			KindGVKs:    kindGVKs,
		},
	}
	if err := engineReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to set up replication engine")
		os.Exit(1)
	}

	// Advisory admission webhook (design D6). Scaffold convention: disabled
	// only with ENABLE_WEBHOOKS=false (e.g. when running locally without
	// serving certificates).
	if os.Getenv("ENABLE_WEBHOOKS") != "false" {
		if err := r8rwebhook.Setup(mgr); err != nil {
			setupLog.Error(err, "Failed to set up admission webhook")
			os.Exit(1)
		}
	}

	// The provider and the cluster runtime manager run as manager runnables
	// (leader-gated, stopped with the manager).
	if err := mgr.Add(provider); err != nil {
		setupLog.Error(err, "Failed to add discovery provider to manager")
		os.Exit(1)
	}
	if err := mgr.Add(clusterManager); err != nil {
		setupLog.Error(err, "Failed to add cluster runtime manager to manager")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	// Readiness reflects exactly one thing: hub informers synced
	// (observability-operations spec). The check's only input is the hub
	// cache's sync state — spoke connectivity (cluster.Manager) is never
	// consulted, so an unreachable target cluster cannot flip readiness;
	// per-cluster outages surface in k8s_r8r_cluster_connectivity and
	// Replication status instead. The gate runs on every replica (no
	// leader election) so standbys are ready for failover.
	hubSynced := telemetry.NewInformerSync(mgr.GetCache())
	if err := mgr.Add(hubSynced); err != nil {
		setupLog.Error(err, "Failed to add informer-sync readiness gate")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", hubSynced.Check); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}
