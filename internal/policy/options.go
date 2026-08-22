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

package policy

import (
	"sort"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
)

// EffectiveOptions is the resolved option set that applies to a target when
// one or more policies permit it (see ResolveOptions).
type EffectiveOptions struct {
	// AllowNamespaceCreation is the OR across the matching policies: any one
	// policy granting namespace creation grants it for the target.
	AllowNamespaceCreation bool
	// AllowedConflictPolicies is the union of the matching policies' allowed
	// conflict policies, in canonical order (Fail, Adopt, Overwrite). Always
	// contains at least Fail.
	AllowedConflictPolicies []r8rv1alpha1.ConflictPolicy
	// RevocationPolicy is the most conservative revocation policy among the
	// matching policies: Retain beats Delete.
	RevocationPolicy r8rv1alpha1.RevocationPolicy
}

// conflictPolicyRank fixes the canonical output order of the conflict-policy
// union: Fail (always present, safest) first, then Adopt, then Overwrite.
var conflictPolicyRank = map[r8rv1alpha1.ConflictPolicy]int{
	r8rv1alpha1.ConflictPolicyFail:      0,
	r8rv1alpha1.ConflictPolicyAdopt:     1,
	r8rv1alpha1.ConflictPolicyOverwrite: 2,
}

// ResolveOptions combines the options of every policy that permits the same
// target into one effective option set:
//
//   - allowNamespaceCreation: OR — a capability granted by any matching policy
//     is granted.
//   - allowedConflictPolicies: union — same reasoning; Fail is always
//     included because it is every policy's implicit default.
//   - revocationPolicy: most conservative wins — Retain beats Delete. When
//     admins disagree about what happens to revoked replicas, retaining is
//     the non-destructive choice: a wrongly retained replica can still be
//     deleted by hand, while a wrongly deleted one may take workloads down
//     with it.
//
// Unset per-policy options take the API defaults before combining: conflict
// policies default to [Fail], revocationPolicy defaults to Delete. With an
// empty matched list ResolveOptions returns the safe defaults (no namespace
// creation, only Fail, Delete) — but an empty list means the target is denied
// and no options apply at all.
//
// The result is deterministic regardless of policy order.
func ResolveOptions(matched []r8rv1alpha1.ReplicationPolicy) EffectiveOptions {
	eff := EffectiveOptions{
		AllowedConflictPolicies: []r8rv1alpha1.ConflictPolicy{r8rv1alpha1.ConflictPolicyFail},
		RevocationPolicy:        r8rv1alpha1.RevocationPolicyDelete,
	}
	seen := map[r8rv1alpha1.ConflictPolicy]bool{r8rv1alpha1.ConflictPolicyFail: true}
	for i := range matched {
		opts := matched[i].Spec.Options
		if opts.AllowNamespaceCreation {
			eff.AllowNamespaceCreation = true
		}
		for _, cp := range opts.AllowedConflictPolicies {
			if !seen[cp] {
				seen[cp] = true
				eff.AllowedConflictPolicies = append(eff.AllowedConflictPolicies, cp)
			}
		}
		// Most conservative wins: Retain (non-destructive) beats Delete. An
		// unset value is the API default Delete and cannot override Retain.
		if opts.RevocationPolicy == r8rv1alpha1.RevocationPolicyRetain {
			eff.RevocationPolicy = r8rv1alpha1.RevocationPolicyRetain
		}
	}
	sort.Slice(eff.AllowedConflictPolicies, func(a, b int) bool {
		return conflictPolicyRank[eff.AllowedConflictPolicies[a]] <
			conflictPolicyRank[eff.AllowedConflictPolicies[b]]
	})
	return eff
}

// Revocation records one target whose permission was withdrawn between two
// evaluations. Controllers (task 3.3) act on these per the effective
// revocationPolicy resolved from the PREVIOUS decision's matched policies.
type Revocation struct {
	// Target is the replica slot that lost permission.
	Target Target
	// Previous is the earlier decision that allowed the target.
	Previous Decision
	// Current is the current decision denying the target, or nil when the
	// target is absent from the current evaluation entirely (e.g. it was
	// deselected by the request rather than denied by policy).
	Current *Decision
}

// DetectRevocations compares a previous evaluation result with the current one
// and returns every target that was allowed before but is no longer allowed
// now — either explicitly denied by the current evaluation or absent from it.
// Targets are keyed by (clusterName, namespace); results follow the previous
// result's order. This is the primitive the revocation flow (task 3.3) builds
// on; this package only detects, it never acts.
func DetectRevocations(previous, current Result) []Revocation {
	type key struct{ cluster, namespace string }
	currentByKey := make(map[key]*Decision, len(current.Decisions))
	for i := range current.Decisions {
		d := &current.Decisions[i]
		currentByKey[key{d.Target.ClusterName, d.Target.Namespace}] = d
	}

	var revoked []Revocation
	for _, prev := range previous.Decisions {
		if !prev.Allowed {
			continue
		}
		cur, present := currentByKey[key{prev.Target.ClusterName, prev.Target.Namespace}]
		if present && cur.Allowed {
			continue
		}
		rev := Revocation{Target: prev.Target, Previous: prev}
		if present {
			curCopy := *cur
			rev.Current = &curCopy
		}
		revoked = append(revoked, rev)
	}
	return revoked
}
