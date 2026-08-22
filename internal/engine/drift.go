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

package engine

import (
	"context"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
	"github.com/moeritze/k8s-r8r/internal/telemetry"
)

// defaultDriftResync is the fallback resync interval for spoke metadata
// informers: a full re-delivery of all managed-object metadata catches any
// watch event the hub missed.
const defaultDriftResync = 10 * time.Hour

// DriftDetector maintains, per connected target cluster, metadata-only
// informers for every replicated GVK (design D3/D7). Informers deliver
// PartialObjectMetadata only, so spoke secret payloads are never cached on
// the hub. Every add/update/delete of a managed replica enqueues the owning
// Replication(s); the reconciler then performs a live read and converges.
//
// Enqueueing on every event (rather than only on hash-annotation mismatch)
// is deliberate: a payload edit that leaves the source-hash annotation
// intact is invisible to metadata alone, but it still bumps the object's
// resourceVersion and produces an event — the reconcile that follows
// compares full content and repairs it. The engine's own applies echo one
// extra reconcile each, which no-ops and terminates.
//
// Server-side label filtering: the spoke caches are built by the cluster
// runtime factory; wiring SHOULD configure them with
// cache.Options.DefaultLabelSelector = {app.kubernetes.io/managed-by:
// k8s-r8r} so the watch streams only managed objects. The detector filters
// by label again in its handler, so an unfiltered cache is correct — just
// more chatty.
type DriftDetector struct {
	// lookup maps a source reference (uid + namespace) to the Replication
	// objects that fan it out.
	lookup func(ctx context.Context, sourceUID, sourceNamespace string) []client.ObjectKey
	resync time.Duration
	events chan event.GenericEvent

	mu       sync.Mutex
	ctx      context.Context //nolint:containedctx // runnable context, needed by GetInformer after Start
	clusters map[string]cache.Cache
	watched  map[string]map[schema.GroupVersionKind]bool
	gvks     map[schema.GroupVersionKind]bool
}

// NewDriftDetector builds a detector. lookup resolves (source UID, source
// namespace) — read from replica labels — to the owning Replication keys;
// resync <= 0 selects the default (10h).
func NewDriftDetector(lookup func(ctx context.Context, sourceUID, sourceNamespace string) []client.ObjectKey, resync time.Duration) *DriftDetector {
	if resync <= 0 {
		resync = defaultDriftResync
	}
	return &DriftDetector{
		lookup:   lookup,
		resync:   resync,
		events:   make(chan event.GenericEvent, 1024),
		clusters: map[string]cache.Cache{},
		watched:  map[string]map[schema.GroupVersionKind]bool{},
		gvks:     map[schema.GroupVersionKind]bool{},
	}
}

// Events exposes the channel the detector pushes Replication enqueue events
// into; it is wired into the controller as a channel source.
func (d *DriftDetector) Events() <-chan event.GenericEvent { return d.events }

// Start implements manager.Runnable: it installs pending informers and then
// blocks until ctx is done. Informer lifetimes are bound to their cluster
// runtime's context (managed by cluster.Manager), not to this one.
func (d *DriftDetector) Start(ctx context.Context) error {
	d.mu.Lock()
	d.ctx = ctx
	pending := make(map[string]cache.Cache, len(d.clusters))
	for name, c := range d.clusters {
		pending[name] = c
	}
	d.mu.Unlock()
	for name, c := range pending {
		d.installAll(name, c)
	}
	<-ctx.Done()
	return nil
}

// NeedLeaderElection implements manager.LeaderElectionRunnable: drift events
// only matter to the instance that reconciles.
func (d *DriftDetector) NeedLeaderElection() bool { return true }

// ClusterReady wires a cluster's cache in (called from the cluster manager's
// OnClusterReady hook) and installs informers for every known replicated
// GVK.
func (d *DriftDetector) ClusterReady(name string, c cache.Cache) {
	d.mu.Lock()
	d.clusters[name] = c
	d.watched[name] = map[schema.GroupVersionKind]bool{}
	started := d.ctx != nil
	d.mu.Unlock()
	if started {
		d.installAll(name, c)
	}
}

// ClusterGone drops a cluster's cache (called from OnClusterGone). The
// informers themselves die with the cluster runtime's context; this only
// forgets the references.
func (d *DriftDetector) ClusterGone(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.clusters, name)
	delete(d.watched, name)
}

// EnsureWatch registers a replicated GVK for drift watching on the given
// cluster (and, going forward, on every cluster that becomes ready). Safe to
// call on every reconcile; installation happens once per (cluster, GVK).
func (d *DriftDetector) EnsureWatch(cluster string, gvk schema.GroupVersionKind) {
	d.mu.Lock()
	d.gvks[gvk] = true
	c, haveCluster := d.clusters[cluster]
	started := d.ctx != nil
	installed := haveCluster && d.watched[cluster][gvk]
	d.mu.Unlock()
	if !started || !haveCluster || installed {
		return
	}
	d.install(cluster, c, gvk)
}

// installAll installs informers for every known GVK on one cluster.
func (d *DriftDetector) installAll(name string, c cache.Cache) {
	d.mu.Lock()
	gvks := make([]schema.GroupVersionKind, 0, len(d.gvks))
	for gvk := range d.gvks {
		gvks = append(gvks, gvk)
	}
	d.mu.Unlock()
	for _, gvk := range gvks {
		d.install(name, c, gvk)
	}
}

// install obtains the metadata-only informer for (cluster, gvk) and attaches
// the drift handler with the periodic resync fallback.
func (d *DriftDetector) install(cluster string, c cache.Cache, gvk schema.GroupVersionKind) {
	d.mu.Lock()
	if d.ctx == nil {
		d.mu.Unlock()
		return
	}
	ctx := d.ctx
	if w, ok := d.watched[cluster]; ok && w[gvk] {
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()

	pom := &metav1.PartialObjectMetadata{}
	pom.SetGroupVersionKind(gvk)
	inf, err := c.GetInformer(ctx, pom)
	if err != nil {
		log.FromContext(ctx).Error(err, "drift: getting metadata informer",
			"cluster", cluster, "gvk", gvk.String())
		return
	}
	if _, err := inf.AddEventHandlerWithResyncPeriod(&driftHandler{d: d, cluster: cluster}, d.resync); err != nil {
		log.FromContext(ctx).Error(err, "drift: adding event handler",
			"cluster", cluster, "gvk", gvk.String())
		return
	}
	d.mu.Lock()
	if _, ok := d.watched[cluster]; ok {
		d.watched[cluster][gvk] = true
	}
	d.mu.Unlock()
}

// Enqueue pushes an explicit reconcile request for a Replication into the
// drift channel (used by cluster ready/gone hooks). Drops the event when the
// channel is full — the periodic resync and per-target requeues are the
// safety net.
func (d *DriftDetector) Enqueue(key client.ObjectKey) {
	rep := &r8rv1alpha1.Replication{}
	rep.Namespace = key.Namespace
	rep.Name = key.Name
	select {
	case d.events <- event.GenericEvent{Object: rep}:
	default:
	}
}

// driftHandler adapts informer events on one cluster into Replication
// enqueues.
type driftHandler struct {
	d       *DriftDetector
	cluster string
}

// OnAdd implements cache.ResourceEventHandler.
func (h *driftHandler) OnAdd(obj interface{}, _ bool) { h.observe(obj) }

// OnUpdate implements cache.ResourceEventHandler.
func (h *driftHandler) OnUpdate(_, newObj interface{}) { h.observe(newObj) }

// OnDelete implements cache.ResourceEventHandler. Replica deletion must
// always enqueue so the reconciler can recreate the replica.
func (h *driftHandler) OnDelete(obj interface{}) {
	if tomb, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
		obj = tomb.Obj
	}
	h.observe(obj)
}

// observe filters for managed replicas and enqueues their owning
// Replication(s), identified via the source-ref labels (design D7).
func (h *driftHandler) observe(obj interface{}) {
	mo, ok := obj.(metav1.Object)
	if !ok {
		return
	}
	labels := mo.GetLabels()
	if labels[LabelManagedBy] != ManagedByValue {
		return
	}
	uid := labels[LabelSourceUID]
	srcNS := labels[LabelSourceNamespace]
	if uid == "" || srcNS == "" {
		return
	}
	telemetry.IncDriftEvent(h.cluster)
	h.d.mu.Lock()
	ctx := h.d.ctx
	h.d.mu.Unlock()
	if ctx == nil || ctx.Err() != nil {
		return
	}
	for _, key := range h.d.lookup(ctx, uid, srcNS) {
		h.d.Enqueue(key)
	}
}
