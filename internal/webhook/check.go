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

package webhook

// Admission-time policy pre-check. This mirrors the dimension order and
// identifiers of internal/policy (sourceNamespace -> sourceKind ->
// targetCluster -> targetNamespace) but checks only what is decidable WITHOUT
// live cluster inventory, and fails open wherever it cannot decide:
//
//   - sources.namespaceSelector is treated as matching (namespace labels are
//     not available at admission time);
//   - the cluster dimension is checked only for SATISFIABILITY: could any
//     cluster label set match both the requested selector and a policy's
//     clusterSelector? Undecidable constructs (Gt/Lt) count as satisfiable.
//
// The authoritative per-target evaluation is internal/policy.Evaluate at
// reconcile time. A denial here therefore means: no ReplicationPolicy could
// EVER allow this request, no matter which clusters are discovered.

import (
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
	"github.com/moeritze/k8s-r8r/internal/policy"
)

// denial names the first policy dimension (in evaluation order) on which no
// policy can possibly allow the request. dimension uses the identifiers of
// internal/policy (Dimension* constants).
type denial struct {
	dimension string
	message   string
}

// checkPolicies runs the advisory dimension checks described in the package
// docs. It returns nil when at least one policy could allow the request for
// every requested target namespace, and a denial naming the failing dimension
// otherwise. Messages contain only metadata (names, annotation values), never
// object payload.
func checkPolicies(policies []r8rv1alpha1.ReplicationPolicy, kind, sourceNamespace string, req *parsedRequest) *denial {
	if len(policies) == 0 {
		return &denial{
			dimension: policy.DimensionNoPolicies,
			message:   "no ReplicationPolicy objects exist; replication is denied by default.",
		}
	}

	// Dimension 1: source namespace (exact-name lists; namespaceSelector fails
	// open — see package docs).
	var nsMatched []*r8rv1alpha1.ReplicationPolicy
	for i := range policies {
		p := &policies[i]
		if containsString(p.Spec.Sources.Namespaces, sourceNamespace) || p.Spec.Sources.NamespaceSelector != nil {
			nsMatched = append(nsMatched, p)
		}
	}
	if len(nsMatched) == 0 {
		return &denial{
			dimension: policy.DimensionSourceNamespace,
			message:   fmt.Sprintf("no ReplicationPolicy allowlists source namespace %q.", sourceNamespace),
		}
	}

	// Dimension 2: source kind, among the namespace-matching policies.
	var kindMatched []*r8rv1alpha1.ReplicationPolicy
	for _, p := range nsMatched {
		if containsString(p.Spec.Sources.Kinds, kind) {
			kindMatched = append(kindMatched, p)
		}
	}
	if len(kindMatched) == 0 {
		return &denial{
			dimension: policy.DimensionSourceKind,
			message: fmt.Sprintf("no ReplicationPolicy matching source namespace %q allowlists kind %q.",
				sourceNamespace, kind),
		}
	}

	// Dimensions 3+4 per requested target namespace. Note the order inversion
	// versus reconcile-time evaluation: without inventory the namespace list is
	// the decidable dimension, so it is checked first and satisfiability of the
	// cluster selectors second.
	for _, tns := range req.targetNamespaces {
		var tnsMatched []*r8rv1alpha1.ReplicationPolicy
		for _, p := range kindMatched {
			if containsString(p.Spec.Targets.Namespaces, tns) {
				tnsMatched = append(tnsMatched, p)
			}
		}
		if len(tnsMatched) == 0 {
			return &denial{
				dimension: policy.DimensionTargetNamespace,
				message: fmt.Sprintf("no ReplicationPolicy matching the source allowlists target namespace %q.",
					tns),
			}
		}

		if req.clusterSelector == nil {
			continue
		}
		satisfiable := false
		for _, p := range tnsMatched {
			psel, err := metav1.LabelSelectorAsSelector(&p.Spec.Targets.ClusterSelector)
			if err != nil {
				// Invalid policy selector fails closed, consistent with
				// internal/policy.
				continue
			}
			if jointlySatisfiable(req.clusterSelector, psel) {
				satisfiable = true
				break
			}
		}
		if !satisfiable {
			return &denial{
				dimension: policy.DimensionTargetCluster,
				message: fmt.Sprintf("requested cluster selector %q cannot match any cluster permitted by a "+
					"ReplicationPolicy that also allows target namespace %q.",
					req.clusterSelectorRaw, tns),
			}
		}
	}

	return nil
}

// jointlySatisfiable reports whether some cluster label set could match both
// selectors at once. It is conservative: it detects only definite per-key
// contradictions (disjoint equality sets, required value forbidden, Exists vs
// DoesNotExist) and otherwise returns true, so uncertainty always fails open.
func jointlySatisfiable(a, b labels.Selector) bool {
	reqsA, okA := a.Requirements()
	reqsB, okB := b.Requirements()
	if !okA || !okB {
		// Non-selectable (labels.Nothing()) never matches anything.
		return false
	}

	type keyState struct {
		// positive is the intersection of allowed values (nil = unconstrained).
		positive map[string]bool
		// negative is the union of forbidden values.
		negative     map[string]bool
		mustExist    bool
		mustNotExist bool
	}
	states := map[string]*keyState{}
	state := func(key string) *keyState {
		s, ok := states[key]
		if !ok {
			s = &keyState{}
			states[key] = s
		}
		return s
	}

	for _, r := range append(append(labels.Requirements{}, reqsA...), reqsB...) {
		s := state(r.Key())
		switch r.Operator() {
		case selection.Equals, selection.DoubleEquals, selection.In:
			s.mustExist = true
			vals := map[string]bool{}
			for _, v := range r.Values().List() {
				vals[v] = true
			}
			if s.positive == nil {
				s.positive = vals
			} else {
				for v := range s.positive {
					if !vals[v] {
						delete(s.positive, v)
					}
				}
			}
		case selection.NotEquals, selection.NotIn:
			if s.negative == nil {
				s.negative = map[string]bool{}
			}
			for _, v := range r.Values().List() {
				s.negative[v] = true
			}
		case selection.Exists:
			s.mustExist = true
		case selection.DoesNotExist:
			s.mustNotExist = true
		case selection.GreaterThan, selection.LessThan:
			// Numeric comparisons: treated as satisfiable (fail open).
			s.mustExist = true
		}
	}

	for _, s := range states {
		if s.mustNotExist && (s.mustExist || s.positive != nil) {
			return false
		}
		if s.positive != nil {
			remaining := 0
			for v := range s.positive {
				if !s.negative[v] {
					remaining++
				}
			}
			if remaining == 0 {
				return false
			}
		}
	}
	return true
}

func containsString(list []string, v string) bool {
	return slices.Contains(list, v)
}
