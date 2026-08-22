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

// Package policy implements the pure, deterministic evaluation core for
// ReplicationPolicy objects (design D4).
//
// Semantics:
//
//   - Default deny: with no ReplicationPolicy objects present, every target is
//     denied.
//   - Allowlist only: a target is permitted only if at least one single policy
//     matches ALL dimensions — source namespace, source kind, target cluster
//     labels, target namespace. There are no deny rules.
//   - Union across policies: multiple policies combine by union, but dimensions
//     are never mixed across policies. If policy A allows the source namespace
//     and policy B allows the target cluster, and neither allows both, the
//     target is denied.
//
// Dimension details:
//
//   - Source namespace: a policy's sources may allowlist namespaces by exact
//     name (sources.namespaces) and/or by label selector
//     (sources.namespaceSelector). When both are set, EITHER matching suffices
//     — the two are alternative ways to name the same allowlist, not an
//     intersection. A nil namespaceSelector means only the exact-name list
//     applies; an empty (non-nil) selector matches every namespace.
//   - Target cluster: standard LabelSelector semantics — an empty
//     targets.clusterSelector matches ALL discovered clusters.
//   - Target namespace: targets.namespaces is a plain allowlist of exact
//     names. An empty list allows NOTHING (empty allowlist semantics; the CRD
//     schema enforces MinItems=1, but the library is strict on its own).
//   - An invalid label selector fails closed: the dimension does not match.
//
// All functions in this package are pure: they take data in and return data
// out, make no API calls, and are fully deterministic (matched policy names
// are sorted, decisions preserve input target order).
package policy

import (
	"fmt"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	r8rv1alpha1 "github.com/moritzroeseler/k8s-r8r/api/v1alpha1"
)

// Denied-dimension identifiers reported in Decision.DeniedDimension. They name
// the first dimension (in evaluation order) on which the closest-matching
// policy failed, or DimensionNoPolicies when no policies exist at all.
const (
	// DimensionSourceNamespace: no policy allowlists the source namespace.
	DimensionSourceNamespace = "sourceNamespace"
	// DimensionSourceKind: a policy allowlists the source namespace but none
	// of those policies allowlists the source kind.
	DimensionSourceKind = "sourceKind"
	// DimensionTargetCluster: a policy matches the source but none of those
	// policies selects the target cluster.
	DimensionTargetCluster = "targetCluster"
	// DimensionTargetNamespace: a policy matches source and cluster but none
	// of those policies allowlists the target namespace.
	DimensionTargetNamespace = "targetNamespace"
	// DimensionNoPolicies: no ReplicationPolicy objects exist (default deny).
	DimensionNoPolicies = "noPolicies"
)

// dimensionRank orders dimensions by evaluation order within a policy. Denial
// reporting uses it to surface the furthest-progressing policy's failure,
// which is the most actionable dimension for the user.
var dimensionRank = map[string]int{
	DimensionNoPolicies:      0,
	DimensionSourceNamespace: 1,
	DimensionSourceKind:      2,
	DimensionTargetCluster:   3,
	DimensionTargetNamespace: 4,
}

// Source is the policy-relevant metadata of the source (hub) object requesting
// replication.
type Source struct {
	// Kind of the source object (e.g. "Secret").
	Kind string
	// Namespace the source object lives in on the hub.
	Namespace string
	// NamespaceLabels are the labels of that namespace, used for
	// sources.namespaceSelector matching.
	NamespaceLabels map[string]string
}

// Target is one concrete replica slot the request resolved to: a discovered
// cluster (with its inventory labels) and a namespace on it.
type Target struct {
	// ClusterName is the discovered inventory name of the target cluster.
	ClusterName string
	// ClusterLabels are the labels on the discovered cluster inventory, used
	// for targets.clusterSelector matching.
	ClusterLabels map[string]string
	// Namespace is the target namespace on that cluster.
	Namespace string
}

// Request is one replication request flattened for policy evaluation: the
// source plus every resolved target tuple.
type Request struct {
	// Source describes the object requesting replication.
	Source Source
	// Targets are the resolved (cluster, namespace) slots to evaluate. Each is
	// decided independently.
	Targets []Target
}

// Decision is the policy verdict for a single target.
type Decision struct {
	// Target is the replica slot this decision is about.
	Target Target
	// Allowed reports whether at least one single policy permitted the target
	// on all dimensions.
	Allowed bool
	// MatchedPolicies lists the names of every policy that fully permits this
	// target, sorted lexicographically. Empty when denied. Feed this to
	// ResolveOptions to compute effective options.
	MatchedPolicies []string
	// DeniedDimension names the failing dimension when Allowed is false: one
	// of the Dimension* constants. It is the furthest dimension any single
	// policy reached before failing, so it points at the most specific gap.
	// Empty when allowed.
	DeniedDimension string
	// Reason is a human-readable explanation naming the failing dimension
	// (for webhook messages and status conditions) or the permitting policies.
	Reason string
}

// Result aggregates the per-target decisions of one evaluation, in input
// target order.
type Result struct {
	// Decisions holds one entry per requested target, in request order.
	Decisions []Decision
}

// Allowed returns the decisions for targets that were permitted, in input
// order.
func (r Result) Allowed() []Decision {
	return r.filter(true)
}

// Denied returns the decisions for targets that were denied, in input order.
func (r Result) Denied() []Decision {
	return r.filter(false)
}

func (r Result) filter(allowed bool) []Decision {
	var out []Decision
	for _, d := range r.Decisions {
		if d.Allowed == allowed {
			out = append(out, d)
		}
	}
	return out
}

// Evaluate decides every target of the request against the given policies.
// It is pure and deterministic: no API calls, decisions in input target order,
// matched policy names sorted. With an empty policy list everything is denied
// (default deny).
func Evaluate(req Request, policies []r8rv1alpha1.ReplicationPolicy) Result {
	res := Result{Decisions: make([]Decision, 0, len(req.Targets))}
	for _, tgt := range req.Targets {
		res.Decisions = append(res.Decisions, decide(req.Source, tgt, policies))
	}
	return res
}

// decide evaluates a single target tuple against all policies.
func decide(src Source, tgt Target, policies []r8rv1alpha1.ReplicationPolicy) Decision {
	if len(policies) == 0 {
		return Decision{
			Target:          tgt,
			Allowed:         false,
			DeniedDimension: DimensionNoPolicies,
			Reason:          "denied by default: no ReplicationPolicy objects exist",
		}
	}

	var matched []string
	// Track the furthest dimension any single policy reached before failing,
	// so the denial names the most specific gap.
	bestDim := DimensionSourceNamespace
	bestPolicy := ""
	for i := range policies {
		p := &policies[i]
		failedDim, ok := matchPolicy(p, src, tgt)
		if ok {
			matched = append(matched, p.Name)
			continue
		}
		if dimensionRank[failedDim] >= dimensionRank[bestDim] {
			bestDim = failedDim
			bestPolicy = p.Name
		}
	}

	if len(matched) > 0 {
		sort.Strings(matched)
		return Decision{
			Target:          tgt,
			Allowed:         true,
			MatchedPolicies: matched,
			Reason:          fmt.Sprintf("allowed by %s", policyList(matched)),
		}
	}

	return Decision{
		Target:          tgt,
		Allowed:         false,
		DeniedDimension: bestDim,
		Reason:          denialReason(bestDim, bestPolicy, src, tgt),
	}
}

// matchPolicy checks one policy against one (source, target) tuple. It returns
// ok=true when every dimension matches; otherwise the first failing dimension
// in evaluation order (sourceNamespace, sourceKind, targetCluster,
// targetNamespace).
func matchPolicy(p *r8rv1alpha1.ReplicationPolicy, src Source, tgt Target) (failedDim string, ok bool) {
	if !matchSourceNamespace(p.Spec.Sources, src) {
		return DimensionSourceNamespace, false
	}
	if !containsString(p.Spec.Sources.Kinds, src.Kind) {
		return DimensionSourceKind, false
	}
	if !matchClusterSelector(p.Spec.Targets.ClusterSelector, tgt.ClusterLabels) {
		return DimensionTargetCluster, false
	}
	if !containsString(p.Spec.Targets.Namespaces, tgt.Namespace) {
		return DimensionTargetNamespace, false
	}
	return "", true
}

// matchSourceNamespace implements the source-namespace dimension: the exact
// Namespaces list and the NamespaceSelector are alternatives — either match
// suffices when both are set. A nil selector means only the exact list
// applies; an empty (non-nil) selector matches all namespaces. Both empty/nil
// means the policy allowlists no source namespaces.
func matchSourceNamespace(s r8rv1alpha1.PolicySources, src Source) bool {
	if containsString(s.Namespaces, src.Namespace) {
		return true
	}
	if s.NamespaceSelector == nil {
		return false
	}
	return selectorMatches(s.NamespaceSelector, src.NamespaceLabels)
}

// matchClusterSelector implements the target-cluster dimension with standard
// LabelSelector semantics: an empty selector matches all clusters.
func matchClusterSelector(sel metav1.LabelSelector, clusterLabels map[string]string) bool {
	return selectorMatches(&sel, clusterLabels)
}

// selectorMatches converts a LabelSelector and matches it against a label set.
// An invalid selector fails closed (never matches) so a malformed policy can
// not widen permissions.
func selectorMatches(sel *metav1.LabelSelector, lbls map[string]string) bool {
	s, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		return false
	}
	return s.Matches(labels.Set(lbls))
}

func containsString(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func policyList(names []string) string {
	if len(names) == 1 {
		return fmt.Sprintf("policy %q", names[0])
	}
	return fmt.Sprintf("policies %v", names)
}

// denialReason renders the human-readable denial message for the reported
// dimension. closest is the name of the policy that progressed furthest; it is
// empty only when no policy passed even the source-namespace dimension.
func denialReason(dim, closest string, src Source, tgt Target) string {
	switch dim {
	case DimensionSourceNamespace:
		return fmt.Sprintf(
			"denied: no ReplicationPolicy allowlists source namespace %q (sourceNamespace)",
			src.Namespace)
	case DimensionSourceKind:
		return fmt.Sprintf(
			"denied: no ReplicationPolicy matching source namespace %q allowlists kind %q (sourceKind, closest policy %q)",
			src.Namespace, src.Kind, closest)
	case DimensionTargetCluster:
		return fmt.Sprintf(
			"denied: no single ReplicationPolicy matching the source permits target cluster %q (targetCluster, closest policy %q)",
			tgt.ClusterName, closest)
	case DimensionTargetNamespace:
		return fmt.Sprintf(
			"denied: no single ReplicationPolicy matching source and cluster allowlists target namespace %q on cluster %q (targetNamespace, closest policy %q)",
			tgt.Namespace, tgt.ClusterName, closest)
	default:
		return "denied by default: no ReplicationPolicy permits this target"
	}
}
