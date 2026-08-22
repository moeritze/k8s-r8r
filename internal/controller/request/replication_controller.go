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

package request

import (
	"context"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
)

// replicationReconciler enforces operator ownership of the canonical layer:
// Replication objects without an owning source link (no controller owner
// reference) are hand-authored, get marked with the NotAuthoritative
// condition, and are never acted on (replication-request spec). It is
// registered by Reconciler.SetupWithManager.
type replicationReconciler struct {
	client.Client
}

// Reconcile marks hand-authored Replication objects NotAuthoritative and
// clears the marking from objects that (re)gained an owning source link.
// It never reconciles replication itself — that is the engine's job, and the
// engine equally ignores NotAuthoritative objects.
func (r *replicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	rep := &r8rv1alpha1.Replication{}
	if err := r.Get(ctx, req.NamespacedName, rep); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	changed := false
	if metav1.GetControllerOf(rep) == nil {
		changed = meta.SetStatusCondition(&rep.Status.Conditions, metav1.Condition{
			Type:               ConditionNotAuthoritative,
			Status:             metav1.ConditionTrue,
			Reason:             r8rv1alpha1.ReasonNotAuthoritative,
			Message:            "Replication was not materialized by the operator (no owning source); it will never be reconciled",
			ObservedGeneration: rep.Generation,
		})
		changed = meta.SetStatusCondition(&rep.Status.Conditions, metav1.Condition{
			Type:               r8rv1alpha1.ReplicationConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             r8rv1alpha1.ReasonNotAuthoritative,
			Message:            "hand-authored Replication objects are ignored; request replication by annotating the source object",
			ObservedGeneration: rep.Generation,
		}) || changed
	} else if meta.FindStatusCondition(rep.Status.Conditions, ConditionNotAuthoritative) != nil {
		changed = meta.RemoveStatusCondition(&rep.Status.Conditions, ConditionNotAuthoritative)
	}

	if !changed {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, r.Status().Update(ctx, rep)
}
