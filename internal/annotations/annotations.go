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

// Package annotations implements the `r8r.io/*` annotation contract that
// expresses replication requests on source objects (replication-request spec).
//
// The contract:
//
//   - `r8r.io/replicate: "true"` — opts the object in ("false" opts out).
//   - `r8r.io/target-clusters: "<label selector>"` — selects target clusters
//     by labels on discovered cluster inventory. An absent or empty selector
//     selects NO clusters: replication always requires an explicit cluster
//     selection, and the wildcard `*` is not supported (select everything
//     deliberately with an empty-but-present match on a label all clusters
//     carry, or a selector like `env` set on the fleet).
//   - `r8r.io/target-namespaces: "<comma-separated list>"` — target
//     namespaces; when omitted the request defaults to the source object's
//     namespace (see Request.EffectiveNamespaces).
//   - `r8r.io/target-name: "<name>"` — optional explicit replica name
//     override; automatic renaming never occurs (design D7).
//
// This package is pure and dependency-light on purpose: the request controller
// and the admission webhook share Parse so that apply-time error messages and
// reconcile-time behavior can never diverge. Error messages name the offending
// annotation key precisely; the webhook surfaces them verbatim.
package annotations

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Annotation keys of the request contract.
const (
	// Prefix is the annotation namespace of the operator. Every request
	// annotation lives under it.
	Prefix = "r8r.io/"

	// KeyReplicate opts an object in ("true") or out ("false").
	KeyReplicate = "r8r.io/replicate"

	// KeyTargetClusters holds the label selector string that selects target
	// clusters from discovered inventory. Absent/empty selects no clusters.
	KeyTargetClusters = "r8r.io/target-clusters"

	// KeyTargetNamespaces holds the comma-separated target namespace list.
	// Absent/empty defaults to the source namespace.
	KeyTargetNamespaces = "r8r.io/target-namespaces"

	// KeyTargetName optionally overrides the replica name on targets. Must be
	// a valid DNS-1123 subdomain.
	KeyTargetName = "r8r.io/target-name"

	// KeySourceHash is written by the engine onto replicas (not a request
	// annotation). Parse ignores it so that objects carrying it are never
	// rejected as malformed.
	KeySourceHash = "r8r.io/source-hash"
)

// Accepted values of KeyReplicate.
const (
	// ValueTrue opts the object in.
	ValueTrue = "true"
	// ValueFalse explicitly opts the object out.
	ValueFalse = "false"
)

// ignoredKeys are `r8r.io/` annotations that are part of the operator's data
// plane rather than the request contract; Parse tolerates them silently.
var ignoredKeys = map[string]bool{
	KeySourceHash: true,
}

// requestKeys is the closed set of request-contract annotation keys.
var requestKeys = map[string]bool{
	KeyReplicate:        true,
	KeyTargetClusters:   true,
	KeyTargetNamespaces: true,
	KeyTargetName:       true,
}

// Request is a parsed, validated replication request. It carries no defaults
// that depend on the source object; apply EffectiveNamespaces with the source
// namespace to obtain the final namespace list.
type Request struct {
	// ClusterSelector selects target clusters by inventory labels. Never nil:
	// when `r8r.io/target-clusters` is absent or empty this is
	// labels.Nothing() — no clusters are selected (explicit selection is
	// required by contract).
	ClusterSelector labels.Selector

	// ClusterSelectorString is the raw selector string as written by the
	// user, for events and status messages.
	ClusterSelectorString string

	// TargetNamespaces is the parsed namespace list. Empty means "default to
	// the source namespace" — use EffectiveNamespaces.
	TargetNamespaces []string

	// TargetName is the optional explicit replica name override; empty means
	// replicas keep the source's name.
	TargetName string
}

// EffectiveNamespaces returns the target namespaces with the contract default
// applied: when the request names none, the source's own namespace is used.
func (r *Request) EffectiveNamespaces(sourceNamespace string) []string {
	if len(r.TargetNamespaces) > 0 {
		return r.TargetNamespaces
	}
	return []string{sourceNamespace}
}

// HasRequest reports whether any request-contract annotation is present,
// regardless of validity. Watch predicates and the kind-allowlist gate use it
// as a cheap "does this object talk to us at all" check.
func HasRequest(ann map[string]string) bool {
	for k := range ann {
		if requestKeys[k] {
			return true
		}
	}
	return false
}

// Replicates reports whether the annotations opt the object in
// (`r8r.io/replicate: "true"`), without validating the rest of the contract.
// Controllers use it to decide the teardown path even when Parse fails.
func Replicates(ann map[string]string) bool {
	return ann[KeyReplicate] == ValueTrue
}

// Parse validates the full annotation contract and returns the request.
//
// Return values:
//   - (nil, nil): the object does not request replication — either no request
//     annotations are present, or `r8r.io/replicate` is absent or "false".
//     Other request annotations may be staged; they are still validated.
//   - (req, nil): the object opts in and all annotations are valid.
//   - (nil, err): at least one annotation is malformed. The error names every
//     offending annotation key (webhook-grade messages).
//
// Unknown `r8r.io/`-prefixed annotation keys are rejected so typos
// (e.g. `r8r.io/target-cluster`) fail loudly instead of silently selecting
// nothing. Operator data-plane keys (`r8r.io/source-hash`) are exempt.
func Parse(ann map[string]string) (*Request, error) {
	var errs []error

	for k := range ann {
		if strings.HasPrefix(k, Prefix) && !requestKeys[k] && !ignoredKeys[k] {
			errs = append(errs, fmt.Errorf(
				"annotation %q: unknown r8r.io annotation (valid keys: %s, %s, %s, %s)",
				k, KeyReplicate, KeyTargetClusters, KeyTargetNamespaces, KeyTargetName))
		}
	}

	optIn := false
	if raw, ok := ann[KeyReplicate]; ok {
		switch raw {
		case ValueTrue:
			optIn = true
		case ValueFalse:
			// Explicit opt-out.
		default:
			errs = append(errs, fmt.Errorf(
				"annotation %q: expected \"true\" or \"false\", got %q", KeyReplicate, raw))
		}
	}

	req := &Request{ClusterSelector: labels.Nothing()}

	if raw, ok := ann[KeyTargetClusters]; ok {
		sel, err := parseClusterSelector(raw)
		if err != nil {
			errs = append(errs, err)
		} else {
			req.ClusterSelector = sel
			req.ClusterSelectorString = strings.TrimSpace(raw)
		}
	}

	if raw, ok := ann[KeyTargetNamespaces]; ok {
		nss, err := parseTargetNamespaces(raw)
		if err != nil {
			errs = append(errs, err)
		} else {
			req.TargetNamespaces = nss
		}
	}

	if raw, ok := ann[KeyTargetName]; ok {
		name := strings.TrimSpace(raw)
		if name != "" {
			if msgs := validation.IsDNS1123Subdomain(name); len(msgs) > 0 {
				errs = append(errs, fmt.Errorf(
					"annotation %q: invalid name %q: %s",
					KeyTargetName, name, strings.Join(msgs, "; ")))
			} else {
				req.TargetName = name
			}
		}
	}

	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}
	if !optIn {
		return nil, nil
	}
	return req, nil
}

// parseClusterSelector parses the target-clusters selector string. An empty
// (or whitespace-only) value selects no clusters, and `*` is rejected by
// contract: cluster selection must always be an explicit label selector.
func parseClusterSelector(raw string) (labels.Selector, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return labels.Nothing(), nil
	}
	if trimmed == "*" {
		return nil, fmt.Errorf(
			"annotation %q: wildcard \"*\" is not supported; select clusters with an explicit label selector",
			KeyTargetClusters)
	}
	sel, err := labels.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("annotation %q: invalid label selector %q: %v",
			KeyTargetClusters, trimmed, err)
	}
	return sel, nil
}

// parseTargetNamespaces parses the comma-separated namespace list. Every entry
// must be a valid DNS-1123 label (Kubernetes namespace name) and must not
// repeat; an empty value means "use the contract default" (nil result).
func parseTargetNamespaces(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	seen := make(map[string]bool, len(parts))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		ns := strings.TrimSpace(p)
		if ns == "" {
			return nil, fmt.Errorf(
				"annotation %q: empty namespace entry in %q", KeyTargetNamespaces, raw)
		}
		if msgs := validation.IsDNS1123Label(ns); len(msgs) > 0 {
			return nil, fmt.Errorf(
				"annotation %q: invalid namespace %q: %s",
				KeyTargetNamespaces, ns, strings.Join(msgs, "; "))
		}
		if seen[ns] {
			return nil, fmt.Errorf(
				"annotation %q: duplicate namespace %q", KeyTargetNamespaces, ns)
		}
		seen[ns] = true
		out = append(out, ns)
	}
	return out, nil
}
