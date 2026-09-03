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
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
)

// Additional per-target status reasons the engine emits beyond the ones
// declared in the API package.
const (
	// ReasonNamespaceMissing: the target namespace does not exist and no
	// matching policy allows creating it.
	ReasonNamespaceMissing = "NamespaceMissing"
	// ReasonClusterUnreachable: the target cluster currently has no usable
	// client; the target is retried with per-cluster backoff.
	ReasonClusterUnreachable = "ClusterUnreachable"
	// ReasonApplyFailed: writing the replica failed for a reason other
	// than conflict/namespace/reachability.
	ReasonApplyFailed = "ApplyFailed"
	// ReasonSourceMissing: the Replication's source object is gone (or its
	// UID changed) while the Replication still exists.
	ReasonSourceMissing = "SourceMissing"
	// ReasonAllTargetsReady is the Ready=True reason.
	ReasonAllTargetsReady = "AllTargetsReady"
	// ReasonNoTargets is the Ready=False reason for a Replication that
	// resolved to zero targets. Re-exported from the API package so engine
	// code reads uniformly against the local reason space.
	ReasonNoTargets = r8rv1alpha1.ReasonNoTargets

	// ConditionPolicyRevoked is set True while at least one target is
	// revoked with revocationPolicy Retain: its replicas stay but are no
	// longer updated (replication-policy spec, task 3.3).
	ConditionPolicyRevoked = "PolicyRevoked"

	// maxNonReadyTargets caps status.nonReadyTargets (design D8); the
	// remainder is summed in nonReadyOverflow.
	maxNonReadyTargets = 20
)

// targetState is the engine's in-memory per-slot outcome for one reconcile.
// Only non-ready states materialize in status detail (design D8).
type targetState struct {
	Cluster   string
	Namespace string
	Name      string
	Ready     bool
	// Desired marks slots that the spec currently resolves to (they count
	// toward summary.desiredTargets); GC-pending leftovers are tracked as
	// non-ready but not desired.
	Desired bool
	Reason  string
	Message string
}

// reasonPriority orders reasons for picking the aggregate Ready=False reason:
// the most actionable/severe reason wins.
var reasonPriority = []string{
	r8rv1alpha1.ReasonConflict,
	r8rv1alpha1.ReasonPolicyRevoked,
	r8rv1alpha1.ReasonPolicyDenied,
	ReasonNamespaceMissing,
	ReasonClusterUnreachable,
	ReasonApplyFailed,
	ReasonSourceMissing,
}

// buildStatus assembles the next ReplicationStatus from per-slot outcomes,
// enforcing design D8: summary counts, non-ready detail capped at
// maxNonReadyTargets with an overflow counter, and stable transition times
// (an entry keeps its LastTransitionTime while its reason is unchanged, so
// unchanged statuses stay deep-equal and writes can be skipped).
//
// Ready is derived here and only here: True when every desired target is
// ready, False when any target failed, and False/NoTargets when a live
// Replication has no desired targets at all. Conditions owned by other
// controllers (TargetsResolved, NotAuthoritative) are carried over untouched.
func buildStatus(
	rep *r8rv1alpha1.Replication,
	sourceHash string,
	states []targetState,
	inventory []r8rv1alpha1.InventoryEntry,
	revokedRetained bool,
	now metav1.Time,
) r8rv1alpha1.ReplicationStatus {
	prev := rep.Status
	next := r8rv1alpha1.ReplicationStatus{
		SourceHash: sourceHash,
		Inventory:  inventory,
		Conditions: append([]metav1.Condition(nil), prev.Conditions...),
	}

	prevTransitions := map[SlotKey]metav1.Time{}
	prevReasons := map[SlotKey]string{}
	for _, t := range prev.NonReadyTargets {
		k := SlotKey{Cluster: t.ClusterName, Namespace: t.Namespace, Name: t.Name}
		prevTransitions[k] = t.LastTransitionTime
		prevReasons[k] = t.Reason
	}

	var desired, ready, failed int32
	var firstReason string
	for _, s := range states {
		if s.Desired {
			desired++
		}
		if s.Ready {
			ready++
			continue
		}
		failed++
		if len(next.NonReadyTargets) < maxNonReadyTargets {
			entry := r8rv1alpha1.NonReadyTarget{
				ClusterName:        s.Cluster,
				Namespace:          s.Namespace,
				Name:               s.Name,
				Reason:             s.Reason,
				Message:            s.Message,
				LastTransitionTime: now,
			}
			k := SlotKey{Cluster: s.Cluster, Namespace: s.Namespace, Name: s.Name}
			if prevReasons[k] == s.Reason {
				entry.LastTransitionTime = prevTransitions[k]
			}
			next.NonReadyTargets = append(next.NonReadyTargets, entry)
		} else {
			next.NonReadyOverflow++
		}
		if firstReason == "" {
			firstReason = s.Reason
		}
	}
	next.Summary = r8rv1alpha1.TargetSummary{
		DesiredTargets: desired,
		ReadyTargets:   ready,
		FailedTargets:  failed,
	}

	readyCond := metav1.Condition{
		Type:               r8rv1alpha1.ReplicationConditionReady,
		ObservedGeneration: rep.Generation,
	}
	switch {
	case failed == 0 && desired == 0 && rep.DeletionTimestamp.IsZero():
		// Replication was requested but resolved to nothing: zero targets
		// means zero failures, which must not read as success (issue #27).
		// A denied policy, a revoked one, and a typo'd cluster selector all
		// land here, and the TargetsResolved condition written by the request
		// controller says which. Excluded while the object is being deleted:
		// a Replication on its way out legitimately has no desired targets.
		readyCond.Status = metav1.ConditionFalse
		readyCond.Reason = ReasonNoTargets
		readyCond.Message = "no targets resolved; nothing is being replicated"
	case failed == 0:
		readyCond.Status = metav1.ConditionTrue
		readyCond.Reason = ReasonAllTargetsReady
		readyCond.Message = fmt.Sprintf("%d/%d targets ready", ready, desired)
	default:
		readyCond.Status = metav1.ConditionFalse
		readyCond.Reason = dominantReason(states, firstReason)
		readyCond.Message = fmt.Sprintf("%d/%d targets ready, %d not ready", ready, desired, failed)
	}
	meta.SetStatusCondition(&next.Conditions, readyCond)

	if revokedRetained {
		meta.SetStatusCondition(&next.Conditions, metav1.Condition{
			Type:               ConditionPolicyRevoked,
			Status:             metav1.ConditionTrue,
			Reason:             r8rv1alpha1.ReasonPolicyRevoked,
			ObservedGeneration: rep.Generation,
			Message:            "policy no longer permits one or more targets; existing replicas are retained but no longer updated",
		})
	} else {
		meta.RemoveStatusCondition(&next.Conditions, ConditionPolicyRevoked)
	}

	return next
}

// dominantReason picks the aggregate Ready=False reason: the highest-priority
// reason present among non-ready states, falling back to the first one seen.
func dominantReason(states []targetState, fallback string) string {
	present := map[string]bool{}
	for _, s := range states {
		if !s.Ready {
			present[s.Reason] = true
		}
	}
	for _, r := range reasonPriority {
		if present[r] {
			return r
		}
	}
	if fallback != "" {
		return fallback
	}
	return ReasonApplyFailed
}
