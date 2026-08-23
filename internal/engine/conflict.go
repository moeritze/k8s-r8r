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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
)

// ConflictAction is the engine's verdict about an object that already exists
// at a replica's intended name (design D7).
type ConflictAction string

const (
	// ActionApply: the existing object is this replication's own replica —
	// no conflict; apply normally.
	ActionApply ConflictAction = "Apply"
	// ActionFail: leave the existing object untouched and report a
	// Conflict condition for the target.
	ActionFail ConflictAction = "Fail"
	// ActionAdopt: add the engine's identity labels and hash annotation
	// without rewriting the payload (only on content-hash equality).
	ActionAdopt ConflictAction = "Adopt"
	// ActionOverwrite: take over the object, replacing its payload
	// (delete+recreate on immutable-field mismatch).
	ActionOverwrite ConflictAction = "Overwrite"
)

// ConflictDecision pairs the action with a human-readable, payload-free
// message for conditions/events.
type ConflictDecision struct {
	Action  ConflictAction
	Message string
}

// EffectiveConflictPolicy reduces the policy-granted conflict policies to the
// single policy the engine acts with: the strongest granted one, ranked
// Overwrite > Adopt > Fail, defaulting to Fail.
//
// Rationale (v1): ResolvedTarget carries no request-side conflict field yet,
// so there is nothing to intersect with — the effective policy is purely
// what admins granted via allowedConflictPolicies (default [Fail]). Granting
// Overwrite in a policy is therefore an explicit admin decision to have
// conflicts overwritten for the requests that policy permits. When a
// request-side field is added later, the effective policy becomes the
// requested one intersected with the grant, and this function only gains an
// argument — no contract change.
func EffectiveConflictPolicy(allowed []r8rv1alpha1.ConflictPolicy) r8rv1alpha1.ConflictPolicy {
	eff := r8rv1alpha1.ConflictPolicyFail
	for _, p := range allowed {
		switch p {
		case r8rv1alpha1.ConflictPolicyOverwrite:
			return r8rv1alpha1.ConflictPolicyOverwrite
		case r8rv1alpha1.ConflictPolicyAdopt:
			eff = r8rv1alpha1.ConflictPolicyAdopt
		case r8rv1alpha1.ConflictPolicyFail:
			// baseline; nothing to raise
		}
	}
	return eff
}

// DecideConflict classifies an existing object found at a replica's intended
// name and returns the action to take (design D7):
//
//   - the object carries this replication's ownership marks → ActionApply
//     (not a conflict at all);
//   - the object is managed by k8s-r8r for a DIFFERENT source → always
//     ActionFail: the engine never steals replicas from another replication,
//     regardless of the effective conflict policy;
//   - otherwise the effective conflict policy (EffectiveConflictPolicy over
//     the policy grants) decides: Fail reports, Adopt requires content-hash
//     equality, Overwrite takes over.
//
// Messages never contain object payloads — only names and hashes.
func DecideConflict(existing *unstructured.Unstructured, sourceUID types.UID, sourceHash string, allowed []r8rv1alpha1.ConflictPolicy) ConflictDecision {
	labels := existing.GetLabels()
	if IsManagedReplica(labels, sourceUID) {
		return ConflictDecision{Action: ActionApply}
	}
	if labels[LabelManagedBy] == ManagedByValue {
		return ConflictDecision{
			Action: ActionFail,
			Message: fmt.Sprintf(
				"existing object is managed by k8s-r8r for a different source (source-uid %q); refusing to take it over",
				labels[LabelSourceUID]),
		}
	}

	switch EffectiveConflictPolicy(allowed) {
	case r8rv1alpha1.ConflictPolicyOverwrite:
		return ConflictDecision{
			Action:  ActionOverwrite,
			Message: "taking over unmanaged object (conflict policy Overwrite)",
		}
	case r8rv1alpha1.ConflictPolicyAdopt:
		existingHash := SourceHash(existing)
		if existingHash == sourceHash {
			return ConflictDecision{
				Action:  ActionAdopt,
				Message: "adopting unmanaged object with identical content hash",
			}
		}
		return ConflictDecision{
			Action: ActionFail,
			Message: fmt.Sprintf(
				"unmanaged object exists and its content hash %s does not equal the source hash %s (Adopt requires identical content)",
				existingHash, sourceHash),
		}
	default:
		return ConflictDecision{
			Action:  ActionFail,
			Message: "unmanaged object already exists at the replica's name (conflict policy Fail)",
		}
	}
}
