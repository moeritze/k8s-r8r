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

// Package request implements the annotation shim (design D1): it watches
// allowlisted source kinds for `r8r.io/*` annotations and materializes exactly
// one operator-owned Replication object per opted-in source.
//
// # Responsibilities
//
//   - Watch allowlisted kinds (default: Secret, ConfigMap) with metadata-only
//     watches; the request controller never reads source payloads.
//   - Parse and validate the annotation contract (internal/annotations).
//   - Resolve targets: annotation cluster selector × discovered cluster
//     inventory × ReplicationPolicy evaluation (internal/policy). Denied
//     targets are excluded from spec.resolvedTargets and reported: a
//     PolicyDenied Ready condition when nothing is allowed, plus a warning
//     event on the source naming the denied dimension.
//   - Mark hand-authored Replication objects (no owning source reference)
//     with a NotAuthoritative condition and never act on them.
//   - Emit an explanatory event on watched-but-not-allowlisted kinds that
//     carry request annotations, and take no further action.
//
// # Finalizer handshake
//
// The request controller and the replication engine coordinate teardown with
// two finalizers:
//
//  1. When the request controller materializes a Replication for a source, it
//     first adds the `r8r.io/finalizer` finalizer to the SOURCE object.
//  2. The engine adds its own finalizer to the REPLICATION object and only
//     removes it after every replica recorded in the inventory is cleaned up
//     (or released via ClusterGone).
//  3. When the source is deleted (or its annotations are removed), the source
//     enters/stays in a state where the request controller deletes the
//     Replication object. The engine's finalizer keeps the Replication alive
//     until the fleet is clean.
//  4. Only once the Replication object is FULLY GONE does the request
//     controller remove `r8r.io/finalizer` from the source, letting the
//     source's own deletion complete.
//
// This guarantees the source (and with it the request's identity) outlives
// all of its replicas, so cleanup can never be orphaned by a source deletion.
//
// # Re-resolution triggers
//
// Resolved targets are recomputed on every source event, on every
// ReplicationPolicy change, and whenever the cluster inventory changes. The
// inventory trigger is exposed as NotifyClusterInventoryChanged (or the
// discovery-shaped adapter ClusterEventHandler); manager wiring connects it to
// the discovery provider.
package request

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
	"github.com/moeritze/k8s-r8r/internal/annotations"
	"github.com/moeritze/k8s-r8r/internal/discovery"
	"github.com/moeritze/k8s-r8r/internal/policy"
)

// Finalizer is the finalizer the request controller places on annotated
// sources while an operator-owned Replication for them exists. See the
// package documentation for the teardown handshake with the engine.
const Finalizer = "r8r.io/finalizer"

// Labels stamped on operator-owned Replication objects. ManagedByValue on the
// ManagedByLabel marks operator ownership; the source labels link a
// Replication back to its source for list-based re-resolution.
const (
	// ManagedByLabel is the standard ownership label key.
	ManagedByLabel = "app.kubernetes.io/managed-by"
	// ManagedByValue identifies objects managed by this operator.
	ManagedByValue = "k8s-r8r"
	// SourceKindLabel holds the lowercased source kind (e.g. "secret").
	SourceKindLabel = "r8r.io/source-kind"
	// SourceNameLabel holds the source name, label-truncated when necessary.
	SourceNameLabel = "r8r.io/source-name"
)

// Event reasons emitted by the request controller. Events never contain
// source payloads — only object references and reasons.
const (
	// EventReasonKindNotEnabled explains that an annotated object's kind is
	// not on the replication allowlist.
	EventReasonKindNotEnabled = "KindNotEnabled"
	// EventReasonInvalidAnnotations reports a malformed annotation contract.
	EventReasonInvalidAnnotations = "InvalidAnnotations"
	// EventReasonPolicyDenied reports targets denied by policy, naming the
	// denied dimension via the policy decision's reason.
	EventReasonPolicyDenied = "PolicyDenied"
	// EventReasonNameConflict reports that the deterministic Replication name
	// for a source is occupied by an object the operator does not own.
	EventReasonNameConflict = "ReplicationNameConflict"
)

// actionMaterialize is the events-API action recorded on request-controller
// events: everything this controller does is part of materializing (or
// refusing to materialize) a request.
const actionMaterialize = "Materialize"

// ConditionNotAuthoritative is the condition type set on hand-authored
// Replication objects. Such objects are never reconciled (replication-request
// spec: canonical Replication objects are operator-owned).
const ConditionNotAuthoritative = "NotAuthoritative"

// ClusterInventory is the slice of the discovery layer the request controller
// needs: point-in-time snapshots of the discovered clusters. discovery.Provider
// satisfies it; tests substitute a stub.
type ClusterInventory interface {
	// List returns a snapshot of all currently known cluster records.
	List() []discovery.ClusterRecord
}

// DefaultAllowlist returns the launch kind allowlist: Secret and ConfigMap.
// The pipeline is kind-agnostic — extending the allowlist is configuration,
// not a code change.
func DefaultAllowlist() []schema.GroupVersionKind {
	return []schema.GroupVersionKind{
		{Group: "", Version: "v1", Kind: "Secret"},
		{Group: "", Version: "v1", Kind: "ConfigMap"},
	}
}

// Reconciler is the annotation-shim request controller. SetupWithManager
// registers one metadata-only source controller per watched kind plus the
// Replication authority controller (NotAuthoritative marking).
//
// Wiring contract (cmd/main.go):
//
//	rec := &request.Reconciler{Inventory: discoveryProvider}
//	if err := rec.SetupWithManager(mgr); err != nil { ... }
//	discoveryProvider.Subscribe(rec.ClusterEventHandler())
//
// Client, Scheme, and Recorder default from the manager when left nil.
type Reconciler struct {
	client.Client

	// Scheme is the manager's scheme; defaulted in SetupWithManager.
	Scheme *runtime.Scheme

	// Recorder emits events on sources and Replications; defaulted in
	// SetupWithManager.
	Recorder events.EventRecorder

	// Inventory supplies cluster snapshots for target resolution. Required.
	Inventory ClusterInventory

	// Allowlist is the set of kinds accepted for replication. Nil means
	// DefaultAllowlist(). Kinds are compared by full GroupVersionKind.
	Allowlist []schema.GroupVersionKind

	// WatchKinds optionally names additional kinds to watch WITHOUT enabling
	// them: annotated objects of these kinds only receive a KindNotEnabled
	// event. The effective watch set is Allowlist ∪ WatchKinds.
	WatchKinds []schema.GroupVersionKind

	// clusterTriggers holds one buffered channel per source controller;
	// NotifyClusterInventoryChanged pushes a re-resolution event into each.
	clusterTriggers []chan event.GenericEvent
}

// +kubebuilder:rbac:groups=r8r.io,resources=replications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=r8r.io,resources=replications/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=r8r.io,resources=replicationpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets;configmaps,verbs=get;list;watch;patch;update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// SetupWithManager registers all controllers of the annotation shim with the
// manager: one per watched source kind (metadata-only) and one for Replication
// authority marking. It must be called exactly once.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Inventory == nil {
		return errors.New("request.Reconciler: Inventory is required")
	}
	if r.Client == nil {
		r.Client = mgr.GetClient()
	}
	if r.Scheme == nil {
		r.Scheme = mgr.GetScheme()
	}
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorder("request-controller")
	}

	allow := r.Allowlist
	if allow == nil {
		allow = DefaultAllowlist()
	}
	allowed := make(map[schema.GroupVersionKind]bool, len(allow))
	watched := make([]schema.GroupVersionKind, 0, len(allow)+len(r.WatchKinds))
	for _, gvk := range allow {
		allowed[gvk] = true
		watched = append(watched, gvk)
	}
	for _, gvk := range r.WatchKinds {
		if !allowed[gvk] {
			watched = append(watched, gvk)
		}
	}

	kindNames := make([]string, 0, len(allow))
	for _, gvk := range allow {
		kindNames = append(kindNames, gvk.Kind)
	}
	slices.Sort(kindNames)

	for _, gvk := range watched {
		sr := &sourceReconciler{
			Reconciler:   r,
			gvk:          gvk,
			enabled:      allowed[gvk],
			enabledKinds: strings.Join(kindNames, ", "),
		}
		obj := &metav1.PartialObjectMetadata{}
		obj.SetGroupVersionKind(gvk)

		b := ctrl.NewControllerManagedBy(mgr).
			Named(controllerName(gvk)).
			For(obj, builder.WithPredicates(sourcePredicate()))
		if sr.enabled {
			trigger := make(chan event.GenericEvent, 1)
			r.clusterTriggers = append(r.clusterTriggers, trigger)
			remap := handler.EnqueueRequestsFromMapFunc(sr.mapAllSources)
			b = b.
				// Not Owns(): the owner-based handler cannot derive the owner
				// GVK from a metadata-only For type, so map explicitly from
				// the Replication's controller ownerReference.
				Watches(&r8rv1alpha1.Replication{},
					handler.EnqueueRequestsFromMapFunc(sr.mapReplicationToSource)).
				Watches(&r8rv1alpha1.ReplicationPolicy{}, remap).
				WatchesRawSource(source.Channel(trigger, remap))
		}
		if err := b.Complete(sr); err != nil {
			return fmt.Errorf("setting up %s request controller: %w", gvk.Kind, err)
		}
	}

	rr := &replicationReconciler{Client: r.Client}
	return ctrl.NewControllerManagedBy(mgr).
		Named("request-replication-authority").
		For(&r8rv1alpha1.Replication{}).
		Complete(rr)
}

// NotifyClusterInventoryChanged asks every source controller to re-resolve
// targets for all materialized requests against the current cluster
// inventory. It never blocks: a pending trigger coalesces with new ones.
// Before SetupWithManager it is a no-op.
func (r *Reconciler) NotifyClusterInventoryChanged() {
	for _, ch := range r.clusterTriggers {
		evt := event.GenericEvent{Object: &metav1.PartialObjectMetadata{}}
		select {
		case ch <- evt:
		default: // a re-resolution is already pending; coalesce
		}
	}
}

// ClusterEventHandler adapts NotifyClusterInventoryChanged to the discovery
// event contract so wiring can pass it to discovery.Provider.Subscribe.
func (r *Reconciler) ClusterEventHandler() discovery.EventHandler {
	return func(discovery.Event) { r.NotifyClusterInventoryChanged() }
}

// ReplicationName returns the deterministic name of the operator-owned
// Replication for a source: "<lowercase kind>-<source name>-<hash8>". The
// hash suffix disambiguates sources of different kinds sharing a name and
// keeps truncated long names collision-free.
func ReplicationName(kind, sourceName string) string {
	sum := sha256.Sum256([]byte(kind + "/" + sourceName))
	suffix := hex.EncodeToString(sum[:])[:8]
	base := strings.ToLower(kind) + "-" + sourceName
	const maxBase = 244 // 253 (DNS subdomain) - 1 dash - 8 hash chars
	if len(base) > maxBase {
		base = strings.TrimRight(base[:maxBase], "-.")
	}
	return base + "-" + suffix
}

// controllerName renders a unique controller name for a watched GVK.
func controllerName(gvk schema.GroupVersionKind) string {
	name := "request-" + strings.ToLower(gvk.Kind)
	if gvk.Group != "" {
		name += "-" + strings.ReplaceAll(gvk.Group, ".", "-")
	}
	return name
}

// sourcePredicate filters source events down to objects that participate in
// the annotation contract: request annotations present (before or after the
// change) or our finalizer set. Deletes are ignored — once the object is
// gone there is nothing left to act on (the finalizer path runs while the
// object still exists with a deletion timestamp).
func sourcePredicate() predicate.Funcs {
	relevant := func(o client.Object) bool {
		return annotations.HasRequest(o.GetAnnotations()) ||
			controllerutil.ContainsFinalizer(o, Finalizer)
	}
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool { return relevant(e.Object) },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return relevant(e.ObjectOld) || relevant(e.ObjectNew)
		},
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return true },
	}
}

// sourceReconciler reconciles one watched source kind. It works purely on
// object metadata (annotations, labels, finalizers) — source payloads are the
// engine's business.
type sourceReconciler struct {
	*Reconciler
	gvk          schema.GroupVersionKind
	enabled      bool
	enabledKinds string
}

// Reconcile drives one source object toward its materialized state: an
// operator-owned Replication with freshly resolved targets while the request
// stands, and a fully released source (no Replication, no finalizer) when the
// request is gone or the source is deleted.
func (s *sourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &metav1.PartialObjectMetadata{}
	obj.SetGroupVersionKind(s.gvk)
	if err := s.Get(ctx, req.NamespacedName, obj); err != nil {
		// Fully deleted sources need no action: the finalizer path completed
		// before deletion, and the ownerReference is the GC backstop.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	ann := obj.GetAnnotations()

	if !s.enabled {
		if annotations.HasRequest(ann) && obj.GetDeletionTimestamp().IsZero() {
			s.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, EventReasonKindNotEnabled,
				actionMaterialize, "kind %s is not enabled for replication; enabled kinds: %s",
				s.gvk.Kind, s.enabledKinds)
		}
		return ctrl.Result{}, nil
	}

	if !obj.GetDeletionTimestamp().IsZero() || !annotations.Replicates(ann) {
		return ctrl.Result{}, s.release(ctx, obj)
	}

	parsed, err := annotations.Parse(ann)
	if err != nil {
		// Malformed contract: surface the exact parser message and keep any
		// existing Replication untouched until the user fixes the typo —
		// tearing down replicas over a syntax error would be destructive.
		s.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, EventReasonInvalidAnnotations,
			actionMaterialize, "%s", err.Error())
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, s.materialize(ctx, obj, parsed)
}

// release implements the teardown half of the finalizer handshake: delete the
// owned Replication (if any) and, only once it is fully gone, remove the
// source finalizer. While the engine's finalizer keeps the Replication alive,
// the source stays blocked; the Owns watch retriggers on Replication deletion.
func (s *sourceReconciler) release(ctx context.Context, obj *metav1.PartialObjectMetadata) error {
	rep := &r8rv1alpha1.Replication{}
	key := types.NamespacedName{
		Namespace: obj.GetNamespace(),
		Name:      ReplicationName(s.gvk.Kind, obj.GetName()),
	}
	err := s.Get(ctx, key, rep)
	switch {
	case err == nil && s.ownedBySource(rep, obj.GetName()):
		if rep.DeletionTimestamp.IsZero() {
			if err := s.Delete(ctx, rep); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
		}
		// Still exists (engine finalizer may be draining replicas): keep the
		// source finalizer until the Replication object is fully gone.
		return nil
	case err != nil && !apierrors.IsNotFound(err):
		return err
	}

	// Replication fully gone (or the name is occupied by an object we do not
	// own, which means ours never existed): release the source.
	if controllerutil.ContainsFinalizer(obj, Finalizer) {
		orig := obj.DeepCopy()
		controllerutil.RemoveFinalizer(obj, Finalizer)
		return s.Patch(ctx, obj, client.MergeFromWithOptions(orig,
			client.MergeFromWithOptimisticLock{}))
	}
	return nil
}

// materialize resolves the request's targets and creates or updates the
// operator-owned Replication accordingly.
func (s *sourceReconciler) materialize(ctx context.Context,
	obj *metav1.PartialObjectMetadata, parsed *annotations.Request) error {
	// Add the finalizer BEFORE creating the Replication so a concurrent
	// source deletion can never orphan it (handshake step 1).
	if !controllerutil.ContainsFinalizer(obj, Finalizer) {
		orig := obj.DeepCopy()
		controllerutil.AddFinalizer(obj, Finalizer)
		if err := s.Patch(ctx, obj, client.MergeFromWithOptions(orig,
			client.MergeFromWithOptimisticLock{})); err != nil {
			return err
		}
	}

	resolved, denied, err := s.resolveTargets(ctx, obj, parsed)
	if err != nil {
		return err
	}
	if len(denied) > 0 {
		first := denied[0]
		msg := first.Reason
		if len(denied) > 1 {
			msg = fmt.Sprintf("%s (and %d more denied targets)", msg, len(denied)-1)
		}
		s.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, EventReasonPolicyDenied,
			actionMaterialize, "%s", msg)
	}

	rep := &r8rv1alpha1.Replication{}
	key := types.NamespacedName{
		Namespace: obj.GetNamespace(),
		Name:      ReplicationName(s.gvk.Kind, obj.GetName()),
	}
	err = s.Get(ctx, key, rep)
	switch {
	case apierrors.IsNotFound(err):
		rep = s.desiredReplication(obj, key.Name, resolved)
		if err := s.Create(ctx, rep); err != nil {
			if apierrors.IsAlreadyExists(err) {
				s.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, EventReasonNameConflict,
					actionMaterialize,
					"Replication %q already exists and is not owned by this %s; not materializing",
					key.Name, s.gvk.Kind)
				return nil
			}
			return err
		}
	case err != nil:
		return err
	case !s.ownedBySource(rep, obj.GetName()):
		s.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, EventReasonNameConflict,
			actionMaterialize,
			"Replication %q already exists and is not owned by this %s; not materializing",
			key.Name, s.gvk.Kind)
		return nil
	default:
		desired := s.desiredReplication(obj, key.Name, resolved)
		if !specEqual(rep, desired) {
			rep.Labels = desired.Labels
			rep.OwnerReferences = desired.OwnerReferences
			rep.Spec = desired.Spec
			if err := s.Update(ctx, rep); err != nil {
				return err
			}
		}
	}

	return s.reportDenial(ctx, key, resolved, denied)
}

// resolveTargets computes the request's fanout: discovered ready clusters
// matching the annotation selector, crossed with the effective namespaces,
// filtered through policy evaluation. It returns the allowed targets grouped
// per cluster and the denied policy decisions.
func (s *sourceReconciler) resolveTargets(ctx context.Context,
	obj *metav1.PartialObjectMetadata, parsed *annotations.Request,
) ([]r8rv1alpha1.ResolvedTarget, []policy.Decision, error) {
	clusters := s.Inventory.List()
	slices.SortFunc(clusters, func(a, b discovery.ClusterRecord) int {
		return strings.Compare(a.Name, b.Name)
	})

	namespaces := parsed.EffectiveNamespaces(obj.GetNamespace())
	var targets []policy.Target
	for _, c := range clusters {
		if !c.Ready || !parsed.ClusterSelector.Matches(labels.Set(c.Labels)) {
			continue
		}
		for _, ns := range namespaces {
			targets = append(targets, policy.Target{
				ClusterName:   c.Name,
				ClusterLabels: c.Labels,
				Namespace:     ns,
			})
		}
	}
	if len(targets) == 0 {
		return nil, nil, nil
	}

	nsLabels := map[string]string{}
	srcNS := &corev1.Namespace{}
	if err := s.Get(ctx, types.NamespacedName{Name: obj.GetNamespace()}, srcNS); err == nil {
		nsLabels = srcNS.Labels
	}

	var policies r8rv1alpha1.ReplicationPolicyList
	if err := s.List(ctx, &policies); err != nil {
		return nil, nil, err
	}

	result := policy.Evaluate(policy.Request{
		Source: policy.Source{
			Kind:            s.gvk.Kind,
			Namespace:       obj.GetNamespace(),
			NamespaceLabels: nsLabels,
		},
		Targets: targets,
	}, policies.Items)

	var resolved []r8rv1alpha1.ResolvedTarget
	byCluster := map[string]int{}
	for _, d := range result.Allowed() {
		idx, ok := byCluster[d.Target.ClusterName]
		if !ok {
			resolved = append(resolved, r8rv1alpha1.ResolvedTarget{
				ClusterName: d.Target.ClusterName,
				TargetName:  parsed.TargetName,
			})
			idx = len(resolved) - 1
			byCluster[d.Target.ClusterName] = idx
		}
		resolved[idx].Namespaces = append(resolved[idx].Namespaces, d.Target.Namespace)
	}
	return resolved, result.Denied(), nil
}

// desiredReplication builds the operator-owned Replication for a source.
func (s *sourceReconciler) desiredReplication(obj *metav1.PartialObjectMetadata,
	name string, resolved []r8rv1alpha1.ResolvedTarget) *r8rv1alpha1.Replication {
	controller := true
	return &r8rv1alpha1.Replication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: obj.GetNamespace(),
			Labels: map[string]string{
				ManagedByLabel:  ManagedByValue,
				SourceKindLabel: strings.ToLower(s.gvk.Kind),
				SourceNameLabel: labelSafeValue(obj.GetName()),
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: s.gvk.GroupVersion().String(),
				Kind:       s.gvk.Kind,
				Name:       obj.GetName(),
				UID:        obj.GetUID(),
				Controller: &controller,
			}},
		},
		Spec: r8rv1alpha1.ReplicationSpec{
			SourceRef: r8rv1alpha1.SourceReference{
				Kind:      s.gvk.Kind,
				Namespace: obj.GetNamespace(),
				Name:      obj.GetName(),
				UID:       obj.GetUID(),
			},
			Origin:          r8rv1alpha1.ReplicationOriginAnnotation,
			ResolvedTargets: resolved,
		},
	}
}

// reportDenial maintains the PolicyDenied condition on the Replication: set
// (Ready=False, reason PolicyDenied) when policy denied every resolved
// target, cleared again once targets are allowed. Status writes are skipped
// when nothing changed.
func (s *sourceReconciler) reportDenial(ctx context.Context, key types.NamespacedName,
	resolved []r8rv1alpha1.ResolvedTarget, denied []policy.Decision) error {
	rep := &r8rv1alpha1.Replication{}
	if err := s.Get(ctx, key, rep); err != nil {
		return client.IgnoreNotFound(err)
	}

	changed := false
	if len(resolved) == 0 && len(denied) > 0 {
		changed = meta.SetStatusCondition(&rep.Status.Conditions, metav1.Condition{
			Type:               r8rv1alpha1.ReplicationConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             r8rv1alpha1.ReasonPolicyDenied,
			Message:            denied[0].Reason,
			ObservedGeneration: rep.Generation,
		})
	} else if cond := meta.FindStatusCondition(rep.Status.Conditions,
		r8rv1alpha1.ReplicationConditionReady); cond != nil &&
		cond.Reason == r8rv1alpha1.ReasonPolicyDenied {
		// Denial lifted: drop the stale condition; the engine owns Ready now.
		changed = meta.RemoveStatusCondition(&rep.Status.Conditions,
			r8rv1alpha1.ReplicationConditionReady)
	}
	if !changed {
		return nil
	}
	return s.Status().Update(ctx, rep)
}

// ownedBySource reports whether the Replication's controller owner reference
// points at the given source (matched by group, kind, and name — the owning
// source link that separates operator-owned from hand-authored objects).
func (s *sourceReconciler) ownedBySource(rep *r8rv1alpha1.Replication, sourceName string) bool {
	ref := metav1.GetControllerOf(rep)
	if ref == nil {
		return false
	}
	gv, err := schema.ParseGroupVersion(ref.APIVersion)
	if err != nil {
		return false
	}
	return gv.Group == s.gvk.Group && ref.Kind == s.gvk.Kind && ref.Name == sourceName
}

// mapReplicationToSource enqueues the owning source of a Replication event
// when the controller ownerReference matches this controller's kind. It backs
// the finalizer handshake: a Replication deletion retriggers the source
// reconcile that releases the source finalizer.
func (s *sourceReconciler) mapReplicationToSource(_ context.Context, obj client.Object) []reconcile.Request {
	ref := metav1.GetControllerOf(obj)
	if ref == nil {
		return nil
	}
	gv, err := schema.ParseGroupVersion(ref.APIVersion)
	if err != nil || gv.Group != s.gvk.Group || ref.Kind != s.gvk.Kind {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{
		Namespace: obj.GetNamespace(), Name: ref.Name,
	}}}
}

// mapAllSources enqueues every source of this controller's kind that has a
// materialized Replication. It backs the cluster-inventory trigger and the
// ReplicationPolicy watch: both invalidate previously resolved targets.
func (s *sourceReconciler) mapAllSources(ctx context.Context, _ client.Object) []reconcile.Request {
	var list r8rv1alpha1.ReplicationList
	if err := s.List(ctx, &list, client.MatchingLabels{
		ManagedByLabel:  ManagedByValue,
		SourceKindLabel: strings.ToLower(s.gvk.Kind),
	}); err != nil {
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		ref := list.Items[i].Spec.SourceRef
		if ref.Kind != s.gvk.Kind {
			continue
		}
		reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: ref.Namespace, Name: ref.Name,
		}})
	}
	return reqs
}

// specEqual reports whether the existing Replication already matches the
// desired labels, owner references, and spec.
func specEqual(existing, desired *r8rv1alpha1.Replication) bool {
	return apiequality.Semantic.DeepEqual(existing.Spec, desired.Spec) &&
		apiequality.Semantic.DeepEqual(existing.Labels, desired.Labels) &&
		apiequality.Semantic.DeepEqual(existing.OwnerReferences, desired.OwnerReferences)
}

// labelSafeValue truncates a name to a valid label value (63 chars), keeping
// a hash suffix for uniqueness when truncation was necessary.
func labelSafeValue(name string) string {
	if len(name) <= 63 {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	return strings.TrimRight(name[:54], "-._") + "-" + hex.EncodeToString(sum[:])[:8]
}
