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

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
	"github.com/moeritze/k8s-r8r/internal/annotations"
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

// conflictPolicyStrength ranks the conflict policies by how much they let the
// engine do to an object it did not create: Fail touches nothing, Adopt takes
// ownership of byte-identical content, Overwrite replaces the payload.
// Unknown values rank as Fail, so nothing the engine cannot name is ever
// treated as a grant.
func conflictPolicyStrength(p r8rv1alpha1.ConflictPolicy) int {
	switch p {
	case r8rv1alpha1.ConflictPolicyOverwrite:
		return 2
	case r8rv1alpha1.ConflictPolicyAdopt:
		return 1
	case r8rv1alpha1.ConflictPolicyFail:
		return 0
	default:
		return 0
	}
}

// EffectiveConflictPolicy is the two-key turn of the conflict contract: the
// engine acts on the WEAKER of what the request asks for and what policy
// permits, ranked Overwrite > Adopt > Fail.
//
//   - requested is the source object's `r8r.io/conflict-policy` annotation
//     (absent means Fail — a request that says nothing consents to nothing).
//   - allowed is the union of `allowedConflictPolicies` across the policies
//     that permitted this target; its strongest member is the grant. The
//     union always contains Fail, so the grant is never weaker than Fail.
//
// Both keys must therefore turn before the engine touches an object it did
// not create: an admin grant alone no longer overwrites anything, and a
// request asking for Overwrite gets no more than its policies permit.
func EffectiveConflictPolicy(
	requested r8rv1alpha1.ConflictPolicy,
	allowed []r8rv1alpha1.ConflictPolicy,
) r8rv1alpha1.ConflictPolicy {
	grant := r8rv1alpha1.ConflictPolicyFail
	for _, p := range allowed {
		if conflictPolicyStrength(p) > conflictPolicyStrength(grant) {
			grant = p
		}
	}
	if conflictPolicyStrength(requested) < conflictPolicyStrength(grant) {
		return normalizeConflictPolicy(requested)
	}
	return grant
}

// normalizeConflictPolicy maps anything the engine cannot name onto Fail, so
// the returned policy is always one the switch in DecideConflict handles.
func normalizeConflictPolicy(p r8rv1alpha1.ConflictPolicy) r8rv1alpha1.ConflictPolicy {
	switch p {
	case r8rv1alpha1.ConflictPolicyOverwrite, r8rv1alpha1.ConflictPolicyAdopt:
		return p
	default:
		return r8rv1alpha1.ConflictPolicyFail
	}
}

// DecideConflict classifies an existing object found at a replica's intended
// name and returns the action to take (design D7):
//
//   - the object carries this replication's ownership marks → ActionApply
//     (not a conflict at all);
//   - the object is managed by k8s-r8r for a DIFFERENT source → always
//     ActionFail: the engine never steals replicas from another replication,
//     regardless of the effective conflict policy;
//   - otherwise the effective conflict policy decides: Fail reports, Adopt
//     requires content-hash equality, Overwrite takes over. The effective
//     policy is the WEAKER of the source's `r8r.io/conflict-policy` request
//     and the policy grant (EffectiveConflictPolicy) — both keys must turn.
//
// src is the hub source object; its UID identifies this replication's own
// replicas and its annotations carry the request-side key. Messages never
// contain object payloads — only names, hashes, and policy names.
func DecideConflict(
	existing *unstructured.Unstructured,
	src *unstructured.Unstructured,
	sourceHash string,
	allowed []r8rv1alpha1.ConflictPolicy,
) ConflictDecision {
	labels := existing.GetLabels()
	if IsManagedReplica(labels, src.GetUID()) {
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

	requested := annotations.RequestedConflictPolicy(src.GetAnnotations())
	switch EffectiveConflictPolicy(requested, allowed) {
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
			Action: ActionFail,
			Message: "unmanaged object already exists at the replica's name (effective conflict policy Fail): " +
				explainConflictKeys(requested, allowed),
		}
	}
}

// explainConflictKeys renders which of the two keys did not turn, so an
// operator reading a Conflict condition or event can tell a missing
// per-object opt-in apart from a policy that never granted the escalation.
// It names annotation keys and policy names only — never payload.
func explainConflictKeys(requested r8rv1alpha1.ConflictPolicy, allowed []r8rv1alpha1.ConflictPolicy) string {
	grant := r8rv1alpha1.ConflictPolicyFail
	for _, p := range allowed {
		if conflictPolicyStrength(p) > conflictPolicyStrength(grant) {
			grant = p
		}
	}
	requestedAsked := conflictPolicyStrength(requested) > 0
	granted := conflictPolicyStrength(grant) > 0

	switch {
	case !requestedAsked && !granted:
		return fmt.Sprintf(
			"the request does not set %s and no matching ReplicationPolicy permits more than %s",
			annotations.KeyConflictPolicy, r8rv1alpha1.ConflictPolicyFail)
	case !requestedAsked:
		return fmt.Sprintf(
			"policy permits up to %s, but the request does not set %s (absent means %s)",
			grant, annotations.KeyConflictPolicy, annotations.DefaultConflictPolicy)
	case !granted:
		return fmt.Sprintf(
			"the request asks for %s, but no matching ReplicationPolicy permits more than %s",
			requested, r8rv1alpha1.ConflictPolicyFail)
	default:
		// Both keys turned above Fail; the only way to land here is an Adopt
		// grant meeting an Adopt request on differing content, which the
		// Adopt branch reports instead. Keep a truthful fallback anyway.
		return fmt.Sprintf("the request asks for %s and policy permits up to %s", requested, grant)
	}
}
