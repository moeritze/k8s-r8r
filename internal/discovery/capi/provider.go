// Package capi implements the ClusterAPI discovery provider.
//
// It watches ClusterAPI Cluster objects on the hub and translates them into
// discovery.ClusterRecords.
//
// # API version negotiation
//
// The watched version is NOT pinned. On Start the provider asks the hub's
// discovery API which versions of clusters.cluster.x-k8s.io are served and
// picks the first entry of supportedClusterVersions ("v1", "v1beta2",
// "v1beta1", most preferred first) that appears there, logging the choice
// once. CAPI moved its storage version to v1beta2 and schedules v1beta1
// unserved in 1.16; a pinned GVR would then wedge the informer in an endless
// 404 retry and report an empty fleet. When the resource IS served but at no
// supported version, the provider fails to start with an error naming the
// group/resource and the versions the server does serve — a loud, named
// failure instead of a silently empty inventory. When the resource is not
// served at all (ClusterAPI not installed, or installed after the operator)
// the provider retries instead, reporting itself as not watching so
// k8s_r8r_discovery_up reads 0 while it waits.
//
// The explicit list is deliberate: the group's preferredVersion would
// auto-adopt any future CAPI version, including one whose readiness
// condition vocabulary this provider has never been validated against.
//
// Records are built as:
//
//   - Name: the Cluster object's metadata.name (stable for the object's
//     lifetime). Fleets that run identically named Clusters in multiple
//     namespaces should scope the provider to one namespace via the
//     "namespace" setting to keep names unique.
//   - Labels: the Cluster object's metadata.labels.
//   - Ready: true only when the control plane is ready — the
//     ControlPlaneReady (v1beta1) or ControlPlaneAvailable (v1beta2
//     condition set) condition in .status.conditions has status "True".
//   - CredentialRef: the conventional ClusterAPI kubeconfig Secret
//     "<cluster-name>-kubeconfig" in the Cluster's namespace.
//
// Dependency note: this provider deliberately does NOT import
// sigs.k8s.io/cluster-api. Importing the CAPI module (even just for its API
// types) drags a large dependency graph into go.mod and couples our
// controller-runtime / k8s.io versions to whatever a given CAPI release was
// built against. Everything we need — labels, one status condition, and a
// name convention for the kubeconfig Secret — is read from unstructured
// objects via a dynamic informer, keeping go.mod lean and version-skew-free.
package capi

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sdiscovery "k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/moeritze/k8s-r8r/internal/discovery"
)

// ProviderName is the registry name selecting this provider in operator
// configuration.
const ProviderName = "cluster-api"

// SettingNamespace optionally restricts the watch to a single namespace.
// Empty (the default) watches all namespaces.
const SettingNamespace = "namespace"

// clusterGroupResource identifies ClusterAPI Cluster objects; the version is
// negotiated at Start (see resolveClusterGVR).
var clusterGroupResource = schema.GroupResource{
	Group:    "cluster.x-k8s.io",
	Resource: "clusters",
}

// supportedClusterVersions are the cluster.x-k8s.io versions this provider is
// validated against, most preferred first. The list is explicit rather than
// derived from the group's preferredVersion so that the versions whose
// readiness-condition vocabulary we understand (see readyConditionTypes) stay
// an auditable constant: a future CAPI version is adopted by a reviewed
// one-line change, never silently.
var supportedClusterVersions = []string{"v1", "v1beta2", "v1beta1"}

// cacheSyncTimeout bounds the initial informer sync. Without it a reflector
// retrying a retryable error (a 404 on an unserved version, a missing RBAC
// rule) blocks Start until process shutdown, turning a misconfiguration into
// a silent hang.
const cacheSyncTimeout = 2 * time.Minute

// negotiateRetry is how long the provider waits before re-asking the
// discovery API after a retryable failure (hub unreachable, ClusterAPI not
// installed yet).
const negotiateRetry = 30 * time.Second

// kubeconfigSecretSuffix is the ClusterAPI convention for the admin
// kubeconfig Secret name.
const kubeconfigSecretSuffix = "-kubeconfig"

// readyConditionTypes are the condition types (any one with status "True")
// that gate readiness: ControlPlaneReady is the v1beta1 condition,
// ControlPlaneAvailable its successor in the v1beta2 condition set. Both are
// accepted because either can be what .status.conditions carries, depending
// on the negotiated version and on which condition set the CAPI release
// promotes there — which is what keeps readiness evaluation independent of
// the version this provider negotiates. Both shapes expose "type" and
// "status" as strings at the same path, so controlPlaneReady reads them
// generically.
var readyConditionTypes = map[string]struct{}{
	"ControlPlaneReady":     {},
	"ControlPlaneAvailable": {},
}

func init() {
	discovery.MustRegister(ProviderName, func(o discovery.Options) (discovery.Provider, error) {
		if o.HubConfig == nil {
			return nil, fmt.Errorf("capi: provider requires a hub rest config")
		}
		dyn, err := dynamic.NewForConfig(o.HubConfig)
		if err != nil {
			return nil, fmt.Errorf("capi: building dynamic client: %w", err)
		}
		// The discovery client is built here but only *used* in Start:
		// construction happens before the manager's lifecycle exists, so a
		// hub that is briefly unreachable must not crash-loop the factory.
		disco, err := k8sdiscovery.NewDiscoveryClientForConfig(o.HubConfig)
		if err != nil {
			return nil, fmt.Errorf("capi: building discovery client: %w", err)
		}
		return New(dyn,
			WithDiscovery(disco),
			WithNamespace(o.Setting(SettingNamespace, "")),
		), nil
	})
}

// Option customizes a Provider.
type Option func(*Provider)

// WithNamespace restricts the watch to one namespace ("" = all).
func WithNamespace(ns string) Option {
	return func(p *Provider) { p.namespace = ns }
}

// WithResync sets the informer resync period (default 10m).
func WithResync(d time.Duration) Option {
	return func(p *Provider) { p.resync = d }
}

// WithDiscovery sets the discovery client used to negotiate the served
// cluster.x-k8s.io version. It is required: Start fails without one rather
// than falling back to a guessed version.
func WithDiscovery(d k8sdiscovery.DiscoveryInterface) Option {
	return func(p *Provider) { p.disco = d }
}

// Provider watches ClusterAPI Cluster objects and emits discovery events.
type Provider struct {
	dyn       dynamic.Interface
	disco     k8sdiscovery.DiscoveryInterface
	namespace string
	resync    time.Duration

	mu       sync.RWMutex
	handlers []discovery.EventHandler
	// records is keyed by "namespace/name" of the Cluster object.
	records map[string]discovery.ClusterRecord
	// watching reports whether the Cluster informer is established and
	// synced. It is the source of the discovery-health gauge: it
	// distinguishes "the provider is not running" from "the provider runs
	// and the fleet is empty".
	watching bool
}

// New builds a Provider on top of a dynamic client.
func New(dyn dynamic.Interface, opts ...Option) *Provider {
	p := &Provider{
		dyn:     dyn,
		resync:  10 * time.Minute,
		records: map[string]discovery.ClusterRecord{},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Name implements discovery.Provider.
func (p *Provider) Name() string { return ProviderName }

// Subscribe implements discovery.Provider. Must be called before Start.
func (p *Provider) Subscribe(h discovery.EventHandler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handlers = append(p.handlers, h)
}

// List implements discovery.Provider.
func (p *Provider) List() []discovery.ClusterRecord {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]discovery.ClusterRecord, 0, len(p.records))
	for _, r := range p.records {
		out = append(out, r.Clone())
	}
	return out
}

// Watching implements discovery.Provider: it reports whether the Cluster
// informer is established and synced. It is the provider's own health
// signal — false means discovery is not
// running at all, which is what distinguishes a broken provider from a
// genuinely empty fleet (both otherwise show zero clusters).
func (p *Provider) Watching() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.watching
}

// Start implements discovery.Provider. It negotiates the served Cluster API
// version, runs the Cluster informer, and blocks until ctx is done.
//
// Version negotiation happens here rather than at construction so a hub that
// is unreachable at process start is retried through the manager's lifecycle
// instead of crash-looping the factory. Two failure modes are deliberately
// separated:
//
//   - The Cluster resource exists but serves no supported version. Start
//     returns errUnsupportedVersions before the informer is created. As a
//     manager Runnable that stops the manager and restarts the pod with the
//     reason in its logs, which is the right answer to a structural
//     incompatibility between this build and the hub that will not resolve
//     itself. (This is unrelated to readiness, which stays hub-cache-only:
//     one unreachable spoke must never make the operator unready, but an
//     unusable inventory *source* is not one spoke.)
//   - The Cluster resource is absent, or the hub is unreachable. Both may
//     resolve on their own — ClusterAPI installed later, an API server
//     restarting — so Start retries instead of failing, and reports itself
//     as not watching (k8s_r8r_discovery_up == 0) with the reason logged
//     while it waits.
func (p *Provider) Start(ctx context.Context) error {
	gvr, err := p.negotiate(ctx)
	if err != nil {
		return err
	}
	if gvr.Empty() {
		// Stopped while waiting for the Cluster resource to appear.
		return nil
	}
	log.FromContext(ctx).Info("Negotiated ClusterAPI discovery version",
		"groupVersion", gvr.GroupVersion().String(), "resource", gvr.Resource)

	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(p.dyn, p.resync, p.namespace, nil)
	informer := factory.ForResource(gvr).Informer()
	if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    p.upsert,
		UpdateFunc: func(_, newObj any) { p.upsert(newObj) },
		DeleteFunc: p.remove,
	}); err != nil {
		return fmt.Errorf("capi: adding informer handler: %w", err)
	}
	factory.Start(ctx.Done())

	syncCtx, cancelSync := context.WithTimeout(ctx, cacheSyncTimeout)
	defer cancelSync()
	if !cache.WaitForCacheSync(syncCtx.Done(), informer.HasSynced) {
		if ctx.Err() != nil {
			// Shutdown during the initial sync is not a failure.
			return nil
		}
		return fmt.Errorf("capi: %s informer cache never synced within %s",
			gvr.GroupResource(), cacheSyncTimeout)
	}

	p.setWatching(true)
	defer p.setWatching(false)
	<-ctx.Done()
	return nil
}

func (p *Provider) setWatching(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.watching = v
}

// negotiate resolves the Cluster GVR, retrying the conditions that can
// resolve themselves (hub unreachable, Cluster CRD not installed yet) and
// returning the ones that cannot (no supported version served). It returns
// an empty GVR and a nil error when ctx ends while it is still waiting, so
// shutdown is not reported as a failure.
func (p *Provider) negotiate(ctx context.Context) (schema.GroupVersionResource, error) {
	var zero schema.GroupVersionResource
	logger := log.FromContext(ctx)
	for {
		gvr, err := resolveClusterGVR(p.disco)
		switch {
		case err == nil:
			return gvr, nil
		case errors.Is(err, errUnsupportedVersions), errors.Is(err, errNoDiscoveryClient):
			return zero, err
		}
		// Retryable: the resource may appear (ClusterAPI installed after
		// the operator) or the hub may come back. Not silent — the reason
		// is logged and Watching() stays false, so k8s_r8r_discovery_up
		// reads 0 for as long as this lasts.
		logger.Error(err, "ClusterAPI inventory unavailable, retrying",
			"resource", clusterGroupResource.String(), "retryAfter", negotiateRetry)
		select {
		case <-ctx.Done():
			return zero, nil
		case <-time.After(negotiateRetry):
		}
	}
}

// Sentinel errors distinguishing the fatal negotiation outcome from the
// retryable ones (see negotiate).
var (
	// errUnsupportedVersions: the Cluster resource is served, but at no
	// version this build understands. Structural, not transient.
	errUnsupportedVersions = errors.New("capi: no supported cluster.x-k8s.io version served")
	// errClusterResourceAbsent: the Cluster resource is not served at all
	// (ClusterAPI is not installed on the hub, or not installed yet).
	errClusterResourceAbsent = errors.New("capi: cluster.x-k8s.io clusters resource not served")
	// errNoDiscoveryClient: construction bug, not a cluster condition.
	errNoDiscoveryClient = errors.New("capi: no discovery client")
)

// resolveClusterGVR asks the API server which versions of
// clusters.cluster.x-k8s.io it serves and returns the most preferred
// supported one. When the resource is served only at versions this build
// does not support it fails loudly — naming the group/resource, the versions
// this build supports, and the versions the server serves — rather than
// letting an unserved version turn into an endlessly retried 404 and an
// apparently empty fleet (issue #28).
func resolveClusterGVR(d k8sdiscovery.DiscoveryInterface) (schema.GroupVersionResource, error) {
	var zero schema.GroupVersionResource
	if d == nil {
		return zero, fmt.Errorf("%w to negotiate the %s version", errNoDiscoveryClient, clusterGroupResource)
	}

	groups, err := d.ServerGroups()
	if err != nil {
		return zero, fmt.Errorf("capi: listing server API groups: %w", err)
	}

	// Versions the group advertises, then narrowed to those actually
	// listing the "clusters" resource: a group version may exist for other
	// resources without serving this one.
	var served []string
	for _, g := range groups.Groups {
		if g.Name != clusterGroupResource.Group {
			continue
		}
		for _, v := range g.Versions {
			ok, err := servesResource(d, v.GroupVersion, clusterGroupResource.Resource)
			if err != nil {
				return zero, err
			}
			if ok {
				served = append(served, v.Version)
			}
		}
	}

	for _, want := range supportedClusterVersions {
		if slices.Contains(served, want) {
			return clusterGroupResource.WithVersion(want), nil
		}
	}
	if len(served) == 0 {
		// ClusterAPI is not installed on this hub (or not yet): retryable,
		// and distinct from a version skew.
		return zero, fmt.Errorf("%w: %s not found in the discovery API",
			errClusterResourceAbsent, clusterGroupResource)
	}
	return zero, fmt.Errorf("%w: %s serves none of %v (served: %v)",
		errUnsupportedVersions, clusterGroupResource, supportedClusterVersions, served)
}

// servesResource reports whether the given group version lists the named
// resource. A NotFound on the group version means "not served", not a
// failure — the group list and the per-version resource list are two
// separate reads and can disagree during an upgrade.
func servesResource(d k8sdiscovery.DiscoveryInterface, groupVersion, resource string) (bool, error) {
	list, err := d.ServerResourcesForGroupVersion(groupVersion)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("capi: listing resources for %s: %w", groupVersion, err)
	}
	for _, r := range list.APIResources {
		if r.Name == resource {
			return true, nil
		}
	}
	return false, nil
}

// upsert handles informer add/update events.
func (p *Provider) upsert(obj any) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}
	record := recordFromCluster(u)
	key := u.GetNamespace() + "/" + u.GetName()

	p.mu.Lock()
	prev, existed := p.records[key]
	if existed && recordsEqual(prev, record) {
		p.mu.Unlock()
		return
	}
	p.records[key] = record.Clone()
	handlers := append([]discovery.EventHandler(nil), p.handlers...)
	p.mu.Unlock()

	typ := discovery.EventRegister
	if existed {
		typ = discovery.EventUpdate
	}
	emit(handlers, discovery.Event{Type: typ, Record: record})
}

// remove handles informer delete events (including tombstones).
func (p *Provider) remove(obj any) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}
	key := u.GetNamespace() + "/" + u.GetName()

	p.mu.Lock()
	record, existed := p.records[key]
	if !existed {
		p.mu.Unlock()
		return
	}
	delete(p.records, key)
	handlers := append([]discovery.EventHandler(nil), p.handlers...)
	p.mu.Unlock()

	record.Ready = false
	emit(handlers, discovery.Event{Type: discovery.EventDeregister, Record: record})
}

func emit(handlers []discovery.EventHandler, e discovery.Event) {
	for _, h := range handlers {
		h(e)
	}
}

// recordFromCluster translates an unstructured ClusterAPI Cluster into a
// discovery record.
func recordFromCluster(u *unstructured.Unstructured) discovery.ClusterRecord {
	labels := map[string]string{}
	maps.Copy(labels, u.GetLabels())
	return discovery.ClusterRecord{
		Name:   u.GetName(),
		Labels: labels,
		Ready:  controlPlaneReady(u),
		CredentialRef: discovery.CredentialRef{
			Namespace: u.GetNamespace(),
			Name:      u.GetName() + kubeconfigSecretSuffix,
		},
	}
}

// controlPlaneReady reports whether any recognized control-plane readiness
// condition in .status.conditions has status "True".
func controlPlaneReady(u *unstructured.Unstructured) bool {
	conditions, found, err := unstructured.NestedSlice(u.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, c := range conditions {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := cond["type"].(string)
		if _, recognized := readyConditionTypes[typ]; !recognized {
			continue
		}
		if status, _ := cond["status"].(string); status == "True" {
			return true
		}
	}
	return false
}

func recordsEqual(a, b discovery.ClusterRecord) bool {
	if a.Name != b.Name || a.Ready != b.Ready || a.CredentialRef != b.CredentialRef {
		return false
	}
	if len(a.Labels) != len(b.Labels) {
		return false
	}
	for k, v := range a.Labels {
		if b.Labels[k] != v {
			return false
		}
	}
	return true
}
