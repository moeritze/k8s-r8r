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
	"errors"
	"fmt"
	"sync"
	"time"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
	"github.com/moeritze/k8s-r8r/internal/cluster"
	"github.com/moeritze/k8s-r8r/internal/discovery"
	"github.com/moeritze/k8s-r8r/internal/policy"
	"github.com/moeritze/k8s-r8r/internal/telemetry"
)

// Field-index names the engine registers on the hub cache. Prefixed so they
// never collide with indexes other controllers register on the same types.
const (
	sourceUIDIndex        = "engine.sourceRef.uid"
	inventoryClusterIndex = "engine.inventory.cluster"
	targetClusterIndex    = "engine.targets.cluster"
)

// errNamespaceMissing marks the "namespace absent and creation not allowed"
// per-target condition (reason NamespaceMissing).
var errNamespaceMissing = errors.New("target namespace does not exist and no matching policy allows namespace creation")

// ClusterInventory answers whether a cluster is still part of discovery
// inventory, and with which labels. The engine uses it for reconcile-time
// policy evaluation (cluster labels) and to distinguish "temporarily
// unreachable" (retry with backoff) from "gone from discovery" (release
// inventory entries with a ClusterGone event).
type ClusterInventory interface {
	// Lookup returns the discovery record for a cluster; ok is false when
	// the cluster is no longer discovered.
	Lookup(name string) (discovery.ClusterRecord, bool)
}

// ProviderInventory adapts a discovery.Provider to ClusterInventory.
type ProviderInventory struct {
	// Provider is the discovery provider to snapshot.
	Provider discovery.Provider
}

// Lookup implements ClusterInventory over Provider.List.
func (p ProviderInventory) Lookup(name string) (discovery.ClusterRecord, bool) {
	for _, rec := range p.Provider.List() {
		if rec.Name == name {
			return rec, true
		}
	}
	return discovery.ClusterRecord{}, false
}

// ClusterEvents is the lifecycle-hook surface of the cluster runtime
// manager. *cluster.Manager satisfies it; the engine uses it to wire drift
// informers and to re-enqueue affected Replications when clusters come and
// go.
type ClusterEvents interface {
	OnClusterReady(func(name string, rt cluster.Runtime))
	OnClusterGone(func(name string))
}

// Options tunes the engine. The zero value selects safe defaults.
type Options struct {
	// HubName is the source-cluster identity written to replicas
	// (LabelSourceCluster). Default "hub".
	HubName string
	// FieldManager is the server-side-apply field manager. Default
	// "k8s-r8r".
	FieldManager string
	// DriftResync is the metadata-informer resync fallback interval and
	// the periodic reconcile interval for healthy Replications. Default
	// 10h.
	DriftResync time.Duration
	// BackoffBase / BackoffMax bound the per-(replication, cluster) retry
	// backoff (design D9). Defaults 5s / 5m.
	BackoffBase time.Duration
	BackoffMax  time.Duration
	// KindGVKs maps allowlisted source kinds to their GroupVersionKind.
	// The pipeline itself is kind-agnostic: adding an entry here (plus the
	// request-side allowlist) enables a new kind with no code changes.
	// Default: Secret and ConfigMap (core/v1).
	KindGVKs map[string]schema.GroupVersionKind
}

// withDefaults returns o with unset fields defaulted.
func (o Options) withDefaults() Options {
	if o.HubName == "" {
		o.HubName = "hub"
	}
	if o.FieldManager == "" {
		o.FieldManager = ManagedByValue
	}
	if o.DriftResync <= 0 {
		o.DriftResync = defaultDriftResync
	}
	if o.BackoffBase <= 0 {
		o.BackoffBase = 5 * time.Second
	}
	if o.BackoffMax <= 0 {
		o.BackoffMax = 5 * time.Minute
	}
	if o.KindGVKs == nil {
		o.KindGVKs = map[string]schema.GroupVersionKind{
			"Secret":    {Group: "", Version: "v1", Kind: "Secret"},
			"ConfigMap": {Group: "", Version: "v1", Kind: "ConfigMap"},
		}
	}
	return o
}

// Reconciler is the replication engine: it reconciles Replication objects by
// fanning their source out to every policy-permitted resolved target through
// the Transport, re-evaluating policy authoritatively on every reconcile
// (task 3.3), repairing drift, handling conflicts (design D7), ensuring
// namespaces, and garbage-collecting replicas from the status inventory so
// none are ever orphaned.
//
// Secret safety: no condition, event, or log message produced here ever
// contains object payloads — only names, namespaces, reasons, and content
// hashes.
type Reconciler struct {
	// Client is the hub client.
	Client client.Client
	// Scheme is the hub scheme.
	Scheme *runtime.Scheme
	// Recorder emits events on Replication objects; nil disables events.
	//
	// This is deliberately the core/v1 recorder (client-go tools/record, i.e.
	// manager.GetEventRecorderFor) and not the events.k8s.io one: only the
	// core recorder populates firstTimestamp/lastTimestamp/count, which the
	// universal `kubectl get events --sort-by=.lastTimestamp` idiom needs
	// (issue #32).
	Recorder record.EventRecorder
	// Transport moves replicas to and from target clusters.
	Transport Transport
	// Clusters resolves discovery inventory (labels + gone detection).
	Clusters ClusterInventory
	// ClusterEvents wires drift informers to cluster runtime lifecycle;
	// optional (nil in tests without live spokes).
	ClusterEvents ClusterEvents
	// Options tunes behavior; zero value = defaults.
	Options Options

	renderer Renderer
	drift    *DriftDetector
	backoff  *backoffTracker
	// limiter rate-limits events per (Replication, reason) so flapping
	// targets cannot flood the event stream (observability spec).
	limiter *telemetry.EventLimiter

	initOnce sync.Once
	mu       sync.Mutex
	// lastResults caches the previous policy evaluation per Replication
	// UID for policy.DetectRevocations. Lost on restart; the reconciler
	// then synthesizes the previous result from the inventory (a recorded
	// replica implies its target was allowed), so revocation detection
	// survives restarts.
	lastResults map[types.UID]policy.Result
}

// init prepares internal state; callable from both SetupWithManager and
// Reconcile so unit tests can drive Reconcile directly.
func (r *Reconciler) init() {
	r.initOnce.Do(func() {
		r.Options = r.Options.withDefaults()
		r.renderer = Renderer{HubName: r.Options.HubName}
		r.backoff = newBackoffTracker(r.Options.BackoffBase, r.Options.BackoffMax)
		r.limiter = telemetry.NewEventLimiter(0)
		r.lastResults = map[types.UID]policy.Result{}
	})
}

// +kubebuilder:rbac:groups=r8r.io,resources=replications,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=r8r.io,resources=replications/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=r8r.io,resources=replications/finalizers,verbs=update
// +kubebuilder:rbac:groups=r8r.io,resources=replicationpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets;configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile drives one Replication to its desired fan-out state.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.init()
	logger := log.FromContext(ctx)

	rep := &r8rv1alpha1.Replication{}
	if err := r.Client.Get(ctx, req.NamespacedName, rep); err != nil {
		if apierrors.IsNotFound(err) {
			r.backoff.Forget(req.NamespacedName)
			telemetry.ForgetReplicas(req.String())
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !rep.DeletionTimestamp.IsZero() {
		return r.reconcileDeletion(ctx, req.NamespacedName, rep)
	}

	// Hand-authored Replication objects are marked NotAuthoritative by the
	// request controller and never reconciled (replication-request spec);
	// this is a defensive re-check.
	if cond := apimeta.FindStatusCondition(rep.Status.Conditions, r8rv1alpha1.ReplicationConditionReady); cond != nil &&
		cond.Reason == r8rv1alpha1.ReasonNotAuthoritative {
		return ctrl.Result{}, nil
	}

	// The engine finalizer guards inventory-backed cleanup: the
	// Replication cannot vanish while replicas it created still exist.
	if !controllerutil.ContainsFinalizer(rep, FinalizerName) {
		controllerutil.AddFinalizer(rep, FinalizerName)
		if err := r.Client.Update(ctx, rep); err != nil {
			return ctrl.Result{}, err
		}
	}

	gvk, ok := r.Options.KindGVKs[rep.Spec.SourceRef.Kind]
	if !ok {
		// Kind fell off the allowlist; report and wait — the request
		// controller owns removal.
		return r.finishSimple(ctx, rep, ReasonApplyFailed,
			fmt.Sprintf("source kind %q is not on the engine allowlist", rep.Spec.SourceRef.Kind), time.Minute)
	}

	src := &unstructured.Unstructured{}
	src.SetGroupVersionKind(gvk)
	err := r.Client.Get(ctx, client.ObjectKey{
		Namespace: rep.Spec.SourceRef.Namespace,
		Name:      rep.Spec.SourceRef.Name,
	}, src)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	if apierrors.IsNotFound(err) || src.GetUID() != rep.Spec.SourceRef.UID {
		// Source gone or replaced by a different incarnation. Deletion
		// of the Replication (and thus replica GC) is the request
		// controller's call; the engine reports and waits.
		return r.finishSimple(ctx, rep, ReasonSourceMissing,
			"source object not found or its UID changed", time.Minute)
	}

	var policyList r8rv1alpha1.ReplicationPolicyList
	if err := r.Client.List(ctx, &policyList); err != nil {
		return ctrl.Result{}, err
	}

	// Reconcile-time policy evaluation is authoritative (design D4): every
	// reconcile re-decides every target.
	srcMeta := policy.Source{
		Kind:            rep.Spec.SourceRef.Kind,
		Namespace:       rep.Spec.SourceRef.Namespace,
		NamespaceLabels: r.namespaceLabels(ctx, rep.Spec.SourceRef.Namespace),
	}
	slots, preq := r.flattenTargets(rep, src.GetName(), srcMeta)
	result := policy.Evaluate(preq, policyList.Items)

	// Revocation detection (task 3.3): policy.DetectRevocations against
	// the previous in-memory evaluation catches fresh withdrawals with
	// their matched-policy provenance; the inventory recompute below makes
	// revocation handling stateless — a denied target that still has
	// recorded replicas is (still) revoked, across operator restarts and
	// on every subsequent reconcile (so Retain keeps retaining and Delete
	// keeps retrying).
	prev, _ := r.previousResult(rep.UID)
	revocations := policy.DetectRevocations(prev, result)
	r.storeResult(rep.UID, result)

	inv := append([]r8rv1alpha1.InventoryEntry(nil), rep.Status.Inventory...)
	revocations = addInventoryRevocations(revocations, result, inv)
	retainedKeys, revokedDeleteNS, revokedRetained := r.applyRevocations(rep, revocations, policyList.Items, srcMeta, inv)

	srcHash := SourceHash(src)
	nn := req.NamespacedName
	var states []targetState
	var delays []time.Duration
	keep := map[SlotKey]bool{}
	for k := range retainedKeys {
		keep[k] = true
	}

	// Persist planned inventory entries BEFORE creating anything, so a
	// crash between create and record can never lose track of a replica.
	planned := false
	for i, s := range slots {
		if !result.Decisions[i].Allowed {
			continue
		}
		key := SlotKey{Cluster: s.cluster, Namespace: s.namespace, Name: s.name, Group: gvk.Group, Kind: gvk.Kind}
		if !hasEntry(inv, key) {
			inv = append(inv, r8rv1alpha1.InventoryEntry{
				ClusterName: s.cluster,
				Namespace:   s.namespace,
				Name:        s.name,
				Group:       gvk.Group,
				Kind:        gvk.Kind,
			})
			planned = true
		}
	}
	if planned {
		rep.Status.Inventory = inv
		if err := r.Client.Status().Update(ctx, rep); err != nil {
			return ctrl.Result{}, err
		}
	}

	for i, s := range slots {
		dec := result.Decisions[i]
		if !dec.Allowed {
			states = append(states, r.deniedState(s, dec, retainedKeys, revokedDeleteNS, inv, gvk))
			continue
		}
		keep[SlotKey{Cluster: s.cluster, Namespace: s.namespace, Name: s.name, Group: gvk.Group, Kind: gvk.Kind}] = true
		eff := policy.ResolveOptions(policiesByName(policyList.Items, dec.MatchedPolicies))
		st, delay := r.applyTarget(ctx, nn, rep, src, gvk, s, eff, &inv)
		states = append(states, st)
		if delay > 0 {
			delays = append(delays, delay)
		}
	}

	// Garbage collection: every inventory entry not kept is deleted from
	// its cluster, or released with a ClusterGone event when the cluster
	// has left discovery entirely.
	gcStates, gcDelays := r.collectGarbage(ctx, nn, rep, &inv, keep)
	states = append(states, gcStates...)
	delays = append(delays, gcDelays...)

	// Per-cluster replica gauges (observability spec): attribute this
	// Replication's per-cluster tallies; the collector sums across all
	// Replications at scrape time.
	telemetry.ObserveReplicas(nn.String(), replicaCountsByCluster(states))

	next := buildStatus(rep, srcHash, states, inv, revokedRetained, metav1.Now())
	prevReady := apimeta.FindStatusCondition(rep.Status.Conditions, r8rv1alpha1.ReplicationConditionReady)
	if err := r.writeStatusIfChanged(ctx, rep, next); err != nil {
		return ctrl.Result{}, err
	}
	r.emitTransitionEvents(rep, prevReady,
		apimeta.FindStatusCondition(next.Conditions, r8rv1alpha1.ReplicationConditionReady))

	res := ctrl.Result{RequeueAfter: r.Options.DriftResync}
	if d := minDelay(delays); d > 0 {
		res.RequeueAfter = d
	}
	logger.V(1).Info("reconciled replication",
		"desired", next.Summary.DesiredTargets,
		"ready", next.Summary.ReadyTargets,
		"failed", next.Summary.FailedTargets)
	return res, nil
}

// slotInfo is one flattened desired replica slot.
type slotInfo struct {
	cluster   string
	namespace string
	name      string
}

// flattenTargets expands spec.resolvedTargets into slots (with resolved
// replica names) and the parallel policy request; slot i corresponds to
// Decisions[i] of the evaluation.
func (r *Reconciler) flattenTargets(rep *r8rv1alpha1.Replication, sourceName string, src policy.Source) ([]slotInfo, policy.Request) {
	var slots []slotInfo
	preq := policy.Request{Source: src}
	for _, tgt := range rep.Spec.ResolvedTargets {
		name := tgt.TargetName
		if name == "" {
			name = sourceName
		}
		var clusterLabels map[string]string
		if r.Clusters != nil {
			if rec, ok := r.Clusters.Lookup(tgt.ClusterName); ok {
				clusterLabels = rec.Labels
			}
		}
		for _, ns := range tgt.Namespaces {
			slots = append(slots, slotInfo{cluster: tgt.ClusterName, namespace: ns, name: name})
			preq.Targets = append(preq.Targets, policy.Target{
				ClusterName:   tgt.ClusterName,
				ClusterLabels: clusterLabels,
				Namespace:     ns,
			})
		}
	}
	return slots, preq
}

// nsKey identifies a (cluster, namespace) pair for revocation bookkeeping.
type nsKey struct{ cluster, namespace string }

// applyRevocations resolves the effective revocationPolicy for every
// detected revocation that still has inventoried replicas, marking retained
// slots (Retain) and emitting events for slots whose replicas will be
// deleted (Delete; the actual deletion happens in the GC phase because the
// slots are simply not kept).
func (r *Reconciler) applyRevocations(
	rep *r8rv1alpha1.Replication,
	revocations []policy.Revocation,
	policies []r8rv1alpha1.ReplicationPolicy,
	src policy.Source,
	inv []r8rv1alpha1.InventoryEntry,
) (retained map[SlotKey]bool, revokedDelete map[nsKey]bool, anyRetained bool) {
	retained = map[SlotKey]bool{}
	revokedDelete = map[nsKey]bool{}
	for _, rev := range revocations {
		// Current == nil means the target left the request's resolved
		// selection — plain GC, not a policy revocation.
		if rev.Current == nil {
			continue
		}
		entries := entriesInNamespace(inv, rev.Target.ClusterName, rev.Target.Namespace)
		if len(entries) == 0 {
			continue
		}
		switch r.effectiveRevocationPolicy(policies, rev, src) {
		case r8rv1alpha1.RevocationPolicyRetain:
			anyRetained = true
			telemetry.IncRevocation("retain")
			for _, e := range entries {
				retained[KeyForEntry(e)] = true
			}
			r.event(rep, "Warning", r8rv1alpha1.ReasonPolicyRevoked, fmt.Sprintf(
				"policy no longer permits target %s/%s; retaining %d replica(s) without further updates (revocationPolicy Retain)",
				rev.Target.ClusterName, rev.Target.Namespace, len(entries)))
		case r8rv1alpha1.RevocationPolicyDelete:
			telemetry.IncRevocation("delete")
			revokedDelete[nsKey{rev.Target.ClusterName, rev.Target.Namespace}] = true
			r.event(rep, "Warning", r8rv1alpha1.ReasonPolicyRevoked, fmt.Sprintf(
				"policy no longer permits target %s/%s; deleting %d replica(s) (revocationPolicy Delete)",
				rev.Target.ClusterName, rev.Target.Namespace, len(entries)))
		}
	}
	return retained, revokedDelete, anyRetained
}

// effectiveRevocationPolicy resolves which revocationPolicy governs a
// revoked target. Preference order:
//
//  1. The policies that permitted the target in the previous evaluation
//     (Revocation.Previous.MatchedPolicies), resolved via
//     policy.ResolveOptions — the policies that granted the permission
//     decide what happens when it is withdrawn.
//  2. After an operator restart the previous matched policies are unknown;
//     fall back to every current policy whose source dimensions still match
//     the source (most conservative wins via ResolveOptions).
//  3. With no candidates at all: the API default, Delete.
func (r *Reconciler) effectiveRevocationPolicy(
	policies []r8rv1alpha1.ReplicationPolicy,
	rev policy.Revocation,
	src policy.Source,
) r8rv1alpha1.RevocationPolicy {
	if len(rev.Previous.MatchedPolicies) > 0 {
		if subset := policiesByName(policies, rev.Previous.MatchedPolicies); len(subset) > 0 {
			return policy.ResolveOptions(subset).RevocationPolicy
		}
	}
	var subset []r8rv1alpha1.ReplicationPolicy
	for i := range policies {
		one := []r8rv1alpha1.ReplicationPolicy{policies[i]}
		res := policy.Evaluate(policy.Request{Source: src, Targets: []policy.Target{rev.Target}}, one)
		d := res.Decisions[0]
		if d.Allowed ||
			d.DeniedDimension == policy.DimensionTargetCluster ||
			d.DeniedDimension == policy.DimensionTargetNamespace {
			subset = append(subset, policies[i])
		}
	}
	if len(subset) == 0 {
		return r8rv1alpha1.RevocationPolicyDelete
	}
	return policy.ResolveOptions(subset).RevocationPolicy
}

// deniedState renders the per-slot state for a policy-denied slot,
// distinguishing fresh denials from revocations in progress.
func (r *Reconciler) deniedState(
	s slotInfo,
	dec policy.Decision,
	retained map[SlotKey]bool,
	revokedDelete map[nsKey]bool,
	inv []r8rv1alpha1.InventoryEntry,
	gvk schema.GroupVersionKind,
) targetState {
	st := targetState{Cluster: s.cluster, Namespace: s.namespace, Name: s.name, Desired: true}
	key := SlotKey{Cluster: s.cluster, Namespace: s.namespace, Name: s.name, Group: gvk.Group, Kind: gvk.Kind}
	switch {
	case retained[key]:
		st.Reason = r8rv1alpha1.ReasonPolicyRevoked
		st.Message = "permission withdrawn; replica retained but no longer updated (revocationPolicy Retain)"
	case revokedDelete[nsKey{s.cluster, s.namespace}] && hasEntry(inv, key):
		st.Reason = r8rv1alpha1.ReasonPolicyRevoked
		st.Message = "permission withdrawn; deleting replica (revocationPolicy Delete)"
	default:
		st.Reason = r8rv1alpha1.ReasonPolicyDenied
		st.Message = dec.Reason
		telemetry.IncPolicyDenial(dec.DeniedDimension)
	}
	return st
}

// applyTarget converges one allowed slot: namespace ensure, conflict
// classification, and the actual apply. It returns the slot state and a
// backoff delay (>0 when the slot failed retryably). Per-target errors never
// abort other targets — the caller just collects states.
func (r *Reconciler) applyTarget(
	ctx context.Context,
	nn types.NamespacedName,
	rep *r8rv1alpha1.Replication,
	src *unstructured.Unstructured,
	gvk schema.GroupVersionKind,
	s slotInfo,
	eff policy.EffectiveOptions,
	inv *[]r8rv1alpha1.InventoryEntry,
) (targetState, time.Duration) {
	st := targetState{Cluster: s.cluster, Namespace: s.namespace, Name: s.name, Desired: true}
	slotKey := SlotKey{Cluster: s.cluster, Namespace: s.namespace, Name: s.name, Group: gvk.Group, Kind: gvk.Kind}
	fail := func(reason, msg string, retry bool) (targetState, time.Duration) {
		st.Reason, st.Message = reason, msg
		if retry {
			return st, r.backoff.Failure(nn, s.cluster)
		}
		// Non-retryable outcomes (conflict Fail, namespace missing) are
		// definitive no-writes: nothing was created, so the planned
		// inventory placeholder is dropped again.
		*inv = removeEntry(*inv, slotKey)
		return st, 0
	}

	if err := r.ensureNamespace(ctx, s.cluster, s.namespace, eff.AllowNamespaceCreation); err != nil {
		switch {
		case errors.Is(err, errNamespaceMissing):
			st.Reason, st.Message = ReasonNamespaceMissing, err.Error()
			*inv = removeEntry(*inv, slotKey)
			return st, r.backoff.Failure(nn, s.cluster)
		case errors.Is(err, ErrClusterUnavailable):
			return fail(ReasonClusterUnreachable, err.Error(), true)
		default:
			return fail(ReasonApplyFailed, "ensuring namespace: "+err.Error(), true)
		}
	}

	desired, hash := r.renderer.Render(src, s.namespace, s.name)

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(gvk)
	err := r.Transport.Get(ctx, s.cluster, client.ObjectKey{Namespace: s.namespace, Name: s.name}, existing)
	switch {
	case apierrors.IsNotFound(err):
		if applyErr := r.Transport.Apply(ctx, s.cluster, desired); applyErr != nil {
			return fail(classifyTransportErr(applyErr), applyErr.Error(), true)
		}
	case err != nil:
		return fail(classifyTransportErr(err), err.Error(), true)
	default:
		decision := DecideConflict(existing, src.GetUID(), hash, eff.AllowedConflictPolicies)
		switch decision.Action {
		case ActionFail:
			telemetry.IncConflict("fail")
			r.event(rep, "Warning", r8rv1alpha1.ReasonConflict,
				fmt.Sprintf("target %s: %s", replicaRef(s.cluster, s.namespace, s.name), decision.Message))
			return fail(r8rv1alpha1.ReasonConflict, decision.Message, false)
		case ActionAdopt:
			telemetry.IncConflict("adopt")
			patch := r.renderer.AdoptPatch(src, gvk, s.namespace, s.name, hash)
			if applyErr := r.Transport.Apply(ctx, s.cluster, patch); applyErr != nil {
				return fail(classifyTransportErr(applyErr), applyErr.Error(), true)
			}
			r.event(rep, "Normal", "Adopted",
				fmt.Sprintf("adopted existing object %s (content hash equal)", replicaRef(s.cluster, s.namespace, s.name)))
		case ActionOverwrite:
			telemetry.IncConflict("overwrite")
			if applyErr := r.applyWithRecreate(ctx, s.cluster, desired); applyErr != nil {
				return fail(classifyTransportErr(applyErr), applyErr.Error(), true)
			}
			r.event(rep, "Warning", "ConflictOverwritten",
				fmt.Sprintf("took over unmanaged object %s (conflict policy Overwrite)", replicaRef(s.cluster, s.namespace, s.name)))
		case ActionApply:
			// Our own replica. Skip the write when both the hash
			// annotation and the actual content already match — the
			// common healthy path stays write-free.
			//
			// Two sub-cases need the write, and they are not the same
			// event (observability-operations: drift correction is
			// observable):
			//
			//   - observed != hash: the replica's *content* diverged
			//     from the source. Someone (or something) rewrote the
			//     replica on the spoke, so the write is a corrective
			//     restore worth an event and a counter.
			//   - observed == hash but the stored annotation differs:
			//     the content is already right and only the engine's
			//     own bookkeeping annotation is stale. That is a
			//     metadata repair, not drift — deliberately silent, see
			//     the comment below.
			observed := SourceHash(existing)
			corrected := observed != hash
			if existing.GetAnnotations()[AnnotationSourceHash] != hash || corrected {
				if applyErr := r.applyWithRecreate(ctx, s.cluster, desired); applyErr != nil {
					return fail(classifyTransportErr(applyErr), applyErr.Error(), true)
				}
				// A stale annotation over unchanged content is what a
				// change to the hashing rules produces fleet-wide on
				// upgrade; counting it would turn an operator rollout
				// into a fleet-wide tamper alarm. Only real content
				// divergence is reported, so a non-zero
				// k8s_r8r_drift_corrections_total always means a
				// replica's payload was actually wrong.
				if corrected {
					telemetry.IncDriftCorrection(s.cluster)
					// Hashes only — never the diverging content
					// itself (secret-safe telemetry).
					r.event(rep, "Warning", "DriftCorrected",
						fmt.Sprintf("restored replica %s: observed content %s, expected %s",
							replicaRef(s.cluster, s.namespace, s.name), observed, hash))
				}
			}
		}
	}

	r.backoff.Success(nn, s.cluster)
	*inv = upsertEntry(*inv, r8rv1alpha1.InventoryEntry{
		ClusterName:     s.cluster,
		Namespace:       s.namespace,
		Name:            s.name,
		Group:           gvk.Group,
		Kind:            gvk.Kind,
		LastAppliedHash: hash,
	})
	if r.drift != nil {
		r.drift.EnsureWatch(s.cluster, gvk)
	}
	st.Ready = true
	return st, 0
}

// applyWithRecreate applies the desired object and, on an immutable-field
// rejection (Invalid), falls back to delete+recreate. It is only called for
// objects the engine owns or is explicitly overwriting (design risk:
// "Adopt/Overwrite semantics on immutable fields").
func (r *Reconciler) applyWithRecreate(ctx context.Context, clusterName string, desired *unstructured.Unstructured) error {
	err := r.Transport.Apply(ctx, clusterName, desired.DeepCopy())
	if err == nil || !apierrors.IsInvalid(err) {
		return err
	}
	if delErr := r.Transport.Delete(ctx, clusterName, desired.DeepCopy()); delErr != nil && !apierrors.IsNotFound(delErr) {
		return delErr
	}
	return r.Transport.Apply(ctx, clusterName, desired.DeepCopy())
}

// ensureNamespace guarantees the target namespace exists, creating it
// (labeled managed-by) only when policy allows. Created namespaces are never
// deleted by the engine.
func (r *Reconciler) ensureNamespace(ctx context.Context, clusterName, namespace string, allowCreate bool) error {
	ns := &unstructured.Unstructured{}
	ns.SetGroupVersionKind(namespaceGVK)
	err := r.Transport.Get(ctx, clusterName, client.ObjectKey{Name: namespace}, ns)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	if !allowCreate {
		return fmt.Errorf("namespace %q: %w", namespace, errNamespaceMissing)
	}
	return r.Transport.Apply(ctx, clusterName, namespacePayload(namespace))
}

// collectGarbage deletes or releases every inventory entry not in keep,
// mutating inv. Unreachable clusters yield retryable states with backoff;
// clusters gone from discovery release their entries with a ClusterGone
// event instead of blocking forever (replication-engine spec).
func (r *Reconciler) collectGarbage(
	ctx context.Context,
	nn types.NamespacedName,
	rep *r8rv1alpha1.Replication,
	inv *[]r8rv1alpha1.InventoryEntry,
	keep map[SlotKey]bool,
) ([]targetState, []time.Duration) {
	plan := PlanGC(*inv, keep, r.clusterGone)
	var states []targetState
	var delays []time.Duration

	for _, e := range plan.Release {
		r.event(rep, "Warning", "ClusterGone", fmt.Sprintf(
			"cluster %q left discovery inventory; releasing replica %s/%s without cleanup",
			e.ClusterName, e.Namespace, e.Name))
		*inv = removeEntry(*inv, KeyForEntry(e))
	}

	for _, e := range plan.Delete {
		gvk, ok := r.gvkForEntry(e)
		if !ok {
			states = append(states, targetState{
				Cluster: e.ClusterName, Namespace: e.Namespace, Name: e.Name,
				Reason:  ReasonApplyFailed,
				Message: fmt.Sprintf("cannot delete replica: kind %q (group %q) is not on the engine allowlist", e.Kind, e.Group),
			})
			continue
		}
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(gvk)
		obj.SetNamespace(e.Namespace)
		obj.SetName(e.Name)

		// Safety gate: only delete objects that carry this replication's
		// ownership marks. A planned-but-never-applied inventory entry
		// (e.g. a conflict placeholder) must never cause deletion of an
		// object the engine does not manage.
		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(gvk)
		getErr := r.Transport.Get(ctx, e.ClusterName, client.ObjectKey{Namespace: e.Namespace, Name: e.Name}, existing)
		switch {
		case apierrors.IsNotFound(getErr):
			// Nothing there — the entry is stale; release it.
			r.backoff.Success(nn, e.ClusterName)
			*inv = removeEntry(*inv, KeyForEntry(e))
			continue
		case getErr != nil:
			states = append(states, targetState{
				Cluster: e.ClusterName, Namespace: e.Namespace, Name: e.Name,
				Reason:  classifyTransportErr(getErr),
				Message: "checking replica before delete: " + getErr.Error(),
			})
			delays = append(delays, r.backoff.Failure(nn, e.ClusterName))
			continue
		case !IsManagedReplica(existing.GetLabels(), rep.Spec.SourceRef.UID):
			// Not ours — never touch it; just drop the entry.
			*inv = removeEntry(*inv, KeyForEntry(e))
			continue
		}

		err := r.Transport.Delete(ctx, e.ClusterName, obj)
		if err != nil && !apierrors.IsNotFound(err) {
			states = append(states, targetState{
				Cluster: e.ClusterName, Namespace: e.Namespace, Name: e.Name,
				Reason:  classifyTransportErr(err),
				Message: "deleting replica: " + err.Error(),
			})
			delays = append(delays, r.backoff.Failure(nn, e.ClusterName))
			continue
		}
		r.backoff.Success(nn, e.ClusterName)
		*inv = removeEntry(*inv, KeyForEntry(e))
		r.event(rep, "Normal", "CleanedUp",
			fmt.Sprintf("cleaned up replica %s", replicaRef(e.ClusterName, e.Namespace, e.Name)))
	}
	return states, delays
}

// reconcileDeletion is the finalizer path: the Replication is being deleted,
// so every inventoried replica is cleaned from reachable clusters (or
// released via ClusterGone) before the finalizer is removed.
func (r *Reconciler) reconcileDeletion(ctx context.Context, nn types.NamespacedName, rep *r8rv1alpha1.Replication) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(rep, FinalizerName) {
		return ctrl.Result{}, nil
	}

	inv := append([]r8rv1alpha1.InventoryEntry(nil), rep.Status.Inventory...)
	states, delays := r.collectGarbage(ctx, nn, rep, &inv, map[SlotKey]bool{})

	if len(inv) == 0 {
		rep.Status.Inventory = nil
		controllerutil.RemoveFinalizer(rep, FinalizerName)
		if err := r.Client.Update(ctx, rep); err != nil {
			return ctrl.Result{}, err
		}
		r.backoff.Forget(nn)
		r.forgetResult(rep.UID)
		telemetry.ForgetReplicas(nn.String())
		r.limiter.Forget(string(rep.UID))
		return ctrl.Result{}, nil
	}

	next := buildStatus(rep, rep.Status.SourceHash, states, inv, false, metav1.Now())
	if err := r.writeStatusIfChanged(ctx, rep, next); err != nil {
		return ctrl.Result{}, err
	}
	res := ctrl.Result{RequeueAfter: r.Options.BackoffMax}
	if d := minDelay(delays); d > 0 {
		res.RequeueAfter = d
	}
	return res, nil
}

// finishSimple writes a single-condition status (no per-slot processing) and
// requeues after the given delay.
func (r *Reconciler) finishSimple(ctx context.Context, rep *r8rv1alpha1.Replication, reason, message string, requeue time.Duration) (ctrl.Result, error) {
	states := []targetState{{Reason: reason, Message: message}}
	next := buildStatus(rep, rep.Status.SourceHash, states, rep.Status.Inventory, false, metav1.Now())
	if err := r.writeStatusIfChanged(ctx, rep, next); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// writeStatusIfChanged persists the status only when it differs (design D8:
// no status churn when nothing changed).
func (r *Reconciler) writeStatusIfChanged(ctx context.Context, rep *r8rv1alpha1.Replication, next r8rv1alpha1.ReplicationStatus) error {
	if apiequality.Semantic.DeepEqual(rep.Status, next) {
		return nil
	}
	rep.Status = next
	return r.Client.Status().Update(ctx, rep)
}

// clusterGone reports whether a cluster has left discovery inventory.
func (r *Reconciler) clusterGone(name string) bool {
	if r.Clusters == nil {
		return false
	}
	_, ok := r.Clusters.Lookup(name)
	return !ok
}

// namespaceLabels reads the labels of a hub namespace (metadata-only,
// best-effort: policy namespace selectors simply see no labels when the read
// fails).
func (r *Reconciler) namespaceLabels(ctx context.Context, namespace string) map[string]string {
	pom := &metav1.PartialObjectMetadata{}
	pom.SetGroupVersionKind(namespaceGVK)
	if err := r.Client.Get(ctx, client.ObjectKey{Name: namespace}, pom); err != nil {
		return nil
	}
	return pom.Labels
}

// gvkForEntry resolves the GVK to delete an inventory entry with, via the
// kind allowlist.
func (r *Reconciler) gvkForEntry(e r8rv1alpha1.InventoryEntry) (schema.GroupVersionKind, bool) {
	for _, gvk := range r.Options.KindGVKs {
		if gvk.Kind == e.Kind && gvk.Group == e.Group {
			return gvk, true
		}
	}
	return schema.GroupVersionKind{}, false
}

// event emits an event on the Replication when a recorder is configured.
// Event messages never contain payload data. Emission is rate-limited per
// (Replication, reason): identical repeats within the limiter cooldown are
// coalesced so flapping targets cannot flood the event stream.
func (r *Reconciler) event(rep *r8rv1alpha1.Replication, eventType, reason, message string) {
	if r.Recorder == nil {
		return
	}
	if r.limiter != nil && !r.limiter.Allow(string(rep.UID), reason, message) {
		return
	}
	r.Recorder.Eventf(rep, eventType, reason, "%s", message)
}

// emitTransitionEvents turns Ready-condition transitions into lifecycle
// events (observability spec: replicated / denied / no targets), so events
// fire on state changes rather than on every reconcile.
//
// Because Ready is False for a Replication with no desired targets, the
// Replicated event now only fires when something was actually replicated: a
// denied request no longer manufactures a "0/0 targets ready" success event
// on every reconcile.
func (r *Reconciler) emitTransitionEvents(rep *r8rv1alpha1.Replication, prev, next *metav1.Condition) {
	if next == nil {
		return
	}
	switch {
	case next.Status == metav1.ConditionTrue &&
		(prev == nil || prev.Status != metav1.ConditionTrue):
		r.event(rep, "Normal", "Replicated", next.Message)
	case next.Status == metav1.ConditionFalse &&
		(next.Reason == r8rv1alpha1.ReasonPolicyDenied ||
			next.Reason == r8rv1alpha1.ReasonNoTargets) &&
		(prev == nil || prev.Reason != next.Reason):
		r.event(rep, "Warning", next.Reason, next.Message)
	}
}

// replicaCountsByCluster tallies per-target-cluster desired/ready/failed
// counts for the replica gauges. Slot-less states (no cluster) are skipped.
func replicaCountsByCluster(states []targetState) map[string]telemetry.ReplicaCounts {
	out := map[string]telemetry.ReplicaCounts{}
	for _, s := range states {
		if s.Cluster == "" {
			continue
		}
		c := out[s.Cluster]
		if s.Desired {
			c.Desired++
		}
		if s.Ready {
			c.Ready++
		} else {
			c.Failed++
		}
		out[s.Cluster] = c
	}
	return out
}

func (r *Reconciler) previousResult(uid types.UID) (policy.Result, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, ok := r.lastResults[uid]
	return res, ok
}

func (r *Reconciler) storeResult(uid types.UID, res policy.Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastResults[uid] = res
}

func (r *Reconciler) forgetResult(uid types.UID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.lastResults, uid)
}

// addInventoryRevocations augments freshly detected revocations with
// synthetic ones recomputed from recorded state: every currently denied
// target that still has inventoried replicas is treated as revoked, whether
// or not the in-memory previous evaluation saw the withdrawal happen. This
// keeps revocation handling correct across operator restarts and on every
// reconcile after the first (Retain keeps retaining instead of falling
// through to garbage collection). Synthetic entries carry no matched-policy
// provenance; effectiveRevocationPolicy falls back to source-matching
// policies for them.
func addInventoryRevocations(revocations []policy.Revocation, current policy.Result, inv []r8rv1alpha1.InventoryEntry) []policy.Revocation {
	covered := map[nsKey]bool{}
	for _, rev := range revocations {
		covered[nsKey{rev.Target.ClusterName, rev.Target.Namespace}] = true
	}
	for i := range current.Decisions {
		dec := current.Decisions[i]
		k := nsKey{dec.Target.ClusterName, dec.Target.Namespace}
		if dec.Allowed || covered[k] {
			continue
		}
		if len(entriesInNamespace(inv, dec.Target.ClusterName, dec.Target.Namespace)) == 0 {
			continue
		}
		covered[k] = true
		revocations = append(revocations, policy.Revocation{
			Target:   dec.Target,
			Previous: policy.Decision{Target: dec.Target, Allowed: true},
			Current:  &dec,
		})
	}
	return revocations
}

// policiesByName filters policies to those named; unknown names are skipped
// (the policy may have been deleted since the previous evaluation).
func policiesByName(policies []r8rv1alpha1.ReplicationPolicy, names []string) []r8rv1alpha1.ReplicationPolicy {
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	var out []r8rv1alpha1.ReplicationPolicy
	for i := range policies {
		if nameSet[policies[i].Name] {
			out = append(out, policies[i])
		}
	}
	return out
}

// hasEntry reports whether inv contains an entry with the given key.
func hasEntry(inv []r8rv1alpha1.InventoryEntry, key SlotKey) bool {
	for _, e := range inv {
		if KeyForEntry(e) == key {
			return true
		}
	}
	return false
}

// classifyTransportErr maps a transport error to a per-target status reason.
func classifyTransportErr(err error) string {
	if errors.Is(err, ErrClusterUnavailable) {
		return ReasonClusterUnreachable
	}
	return ReasonApplyFailed
}

// minDelay returns the smallest positive delay, or 0 when none.
func minDelay(delays []time.Duration) time.Duration {
	var m time.Duration
	for _, d := range delays {
		if d > 0 && (m == 0 || d < m) {
			m = d
		}
	}
	return m
}

// SetupWithManager wires the engine into the hub manager:
//
//   - reconciles Replication objects (controller "replication-engine"),
//   - watches ReplicationPolicy (any change re-enqueues all Replications —
//     reconcile-time enforcement is authoritative),
//   - watches allowlisted source kinds metadata-only, mapping source events
//     to their Replications via a field index on spec.sourceRef.uid,
//   - runs the DriftDetector and feeds its events in as a channel source,
//   - hooks cluster runtime lifecycle (OnClusterReady/OnClusterGone) to
//     manage drift informers and re-enqueue affected Replications.
//
// Wiring note: for server-side label filtering of the spoke metadata
// watches, construct the cluster runtimes with
// cache.Options.DefaultLabelSelector = {app.kubernetes.io/managed-by:
// k8s-r8r} (see DriftDetector).
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.init()

	indexer := mgr.GetFieldIndexer()
	if err := indexer.IndexField(context.Background(), &r8rv1alpha1.Replication{}, sourceUIDIndex,
		func(o client.Object) []string {
			rep, ok := o.(*r8rv1alpha1.Replication)
			if !ok || rep.Spec.SourceRef.UID == "" {
				return nil
			}
			return []string{string(rep.Spec.SourceRef.UID)}
		}); err != nil {
		return fmt.Errorf("engine: indexing %s: %w", sourceUIDIndex, err)
	}
	if err := indexer.IndexField(context.Background(), &r8rv1alpha1.Replication{}, inventoryClusterIndex,
		func(o client.Object) []string {
			rep, ok := o.(*r8rv1alpha1.Replication)
			if !ok {
				return nil
			}
			return clusterSet(rep.Status.Inventory, nil)
		}); err != nil {
		return fmt.Errorf("engine: indexing %s: %w", inventoryClusterIndex, err)
	}
	if err := indexer.IndexField(context.Background(), &r8rv1alpha1.Replication{}, targetClusterIndex,
		func(o client.Object) []string {
			rep, ok := o.(*r8rv1alpha1.Replication)
			if !ok {
				return nil
			}
			return clusterSet(nil, rep.Spec.ResolvedTargets)
		}); err != nil {
		return fmt.Errorf("engine: indexing %s: %w", targetClusterIndex, err)
	}

	hub := mgr.GetClient()
	r.drift = NewDriftDetector(func(ctx context.Context, sourceUID, sourceNamespace string) []client.ObjectKey {
		var list r8rv1alpha1.ReplicationList
		if err := hub.List(ctx, &list,
			client.InNamespace(sourceNamespace),
			client.MatchingFields{sourceUIDIndex: sourceUID}); err != nil {
			return nil
		}
		keys := make([]client.ObjectKey, 0, len(list.Items))
		for i := range list.Items {
			keys = append(keys, client.ObjectKeyFromObject(&list.Items[i]))
		}
		return keys
	}, r.Options.DriftResync)
	if err := mgr.Add(r.drift); err != nil {
		return fmt.Errorf("engine: adding drift detector: %w", err)
	}

	if r.ClusterEvents != nil {
		r.ClusterEvents.OnClusterReady(func(name string, rt cluster.Runtime) {
			r.drift.ClusterReady(name, rt.GetCache())
			r.enqueueForCluster(name)
		})
		r.ClusterEvents.OnClusterGone(func(name string) {
			r.drift.ClusterGone(name)
			r.enqueueForCluster(name)
		})
	}

	b := ctrl.NewControllerManagedBy(mgr).
		Named("replication-engine").
		For(&r8rv1alpha1.Replication{}).
		Watches(&r8rv1alpha1.ReplicationPolicy{}, handler.EnqueueRequestsFromMapFunc(r.mapPolicy)).
		WatchesRawSource(source.Channel[client.Object](r.drift.Events(), &handler.EnqueueRequestForObject{}))
	for _, gvk := range r.Options.KindGVKs {
		pom := &metav1.PartialObjectMetadata{}
		pom.SetGroupVersionKind(gvk)
		b = b.WatchesMetadata(pom, handler.EnqueueRequestsFromMapFunc(r.mapSource))
	}
	return b.Complete(r)
}

// mapSource maps a source-object event to the Replications that fan it out.
func (r *Reconciler) mapSource(ctx context.Context, obj client.Object) []reconcile.Request {
	var list r8rv1alpha1.ReplicationList
	if err := r.Client.List(ctx, &list,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{sourceUIDIndex: string(obj.GetUID())}); err != nil {
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
	}
	return reqs
}

// mapPolicy re-enqueues every Replication on any policy change: policy is
// re-evaluated authoritatively at reconcile time (design D4), so a tightened
// policy triggers revocation on the next reconcile it causes.
func (r *Reconciler) mapPolicy(ctx context.Context, _ client.Object) []reconcile.Request {
	var list r8rv1alpha1.ReplicationList
	if err := r.Client.List(ctx, &list); err != nil {
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
	}
	return reqs
}

// enqueueForCluster pushes reconcile requests for every Replication that
// targets or inventories the given cluster (used by cluster ready/gone
// hooks).
func (r *Reconciler) enqueueForCluster(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	seen := map[client.ObjectKey]bool{}
	for _, idx := range []string{targetClusterIndex, inventoryClusterIndex} {
		var list r8rv1alpha1.ReplicationList
		if err := r.Client.List(ctx, &list, client.MatchingFields{idx: name}); err != nil {
			continue
		}
		for i := range list.Items {
			key := client.ObjectKeyFromObject(&list.Items[i])
			if !seen[key] {
				seen[key] = true
				r.drift.Enqueue(key)
			}
		}
	}
}

// clusterSet collects the distinct cluster names of inventory entries and/or
// resolved targets, for field indexing.
func clusterSet(inv []r8rv1alpha1.InventoryEntry, targets []r8rv1alpha1.ResolvedTarget) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range inv {
		if !seen[e.ClusterName] {
			seen[e.ClusterName] = true
			out = append(out, e.ClusterName)
		}
	}
	for _, t := range targets {
		if !seen[t.ClusterName] {
			seen[t.ClusterName] = true
			out = append(out, t.ClusterName)
		}
	}
	return out
}
