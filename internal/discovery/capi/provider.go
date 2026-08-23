// Package capi implements the ClusterAPI discovery provider.
//
// It watches ClusterAPI Cluster objects (cluster.x-k8s.io/v1beta1) on the hub
// and translates them into discovery.ClusterRecords:
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
	"fmt"
	"maps"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"

	"github.com/moeritze/k8s-r8r/internal/discovery"
)

// ProviderName is the registry name selecting this provider in operator
// configuration.
const ProviderName = "cluster-api"

// SettingNamespace optionally restricts the watch to a single namespace.
// Empty (the default) watches all namespaces.
const SettingNamespace = "namespace"

// clusterGVR identifies ClusterAPI Cluster objects.
var clusterGVR = schema.GroupVersionResource{
	Group:    "cluster.x-k8s.io",
	Version:  "v1beta1",
	Resource: "clusters",
}

// kubeconfigSecretSuffix is the ClusterAPI convention for the admin
// kubeconfig Secret name.
const kubeconfigSecretSuffix = "-kubeconfig"

// readyConditionTypes are the condition types (any one with status "True")
// that gate readiness: ControlPlaneReady is the v1beta1 condition,
// ControlPlaneAvailable its v1beta2 successor surfaced on v1beta1 objects.
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
		return New(dyn, WithNamespace(o.Setting(SettingNamespace, ""))), nil
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

// Provider watches ClusterAPI Cluster objects and emits discovery events.
type Provider struct {
	dyn       dynamic.Interface
	namespace string
	resync    time.Duration

	mu       sync.RWMutex
	handlers []discovery.EventHandler
	// records is keyed by "namespace/name" of the Cluster object.
	records map[string]discovery.ClusterRecord
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

// Start implements discovery.Provider. It runs the Cluster informer and
// blocks until ctx is done.
func (p *Provider) Start(ctx context.Context) error {
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(p.dyn, p.resync, p.namespace, nil)
	informer := factory.ForResource(clusterGVR).Informer()
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    p.upsert,
		UpdateFunc: func(_, newObj any) { p.upsert(newObj) },
		DeleteFunc: p.remove,
	})
	if err != nil {
		return fmt.Errorf("capi: adding informer handler: %w", err)
	}
	factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return fmt.Errorf("capi: cluster informer cache never synced")
	}
	<-ctx.Done()
	return nil
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
