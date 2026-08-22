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

// Minimal parser for the r8r.io/* replication-request annotation contract
// (replication-request spec).
//
// TODO(webhook): converge on the shared parser in internal/annotations once it
// lands (it is being introduced by the request-controller work). This local
// copy exists only so the webhook has no cross-package dependency race; it
// must implement identical semantics for the four request keys.

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Annotation keys of the replication-request contract.
const (
	annPrefix           = "r8r.io/"
	annReplicate        = "r8r.io/replicate"
	annTargetClusters   = "r8r.io/target-clusters"
	annTargetNamespaces = "r8r.io/target-namespaces"
	annTargetName       = "r8r.io/target-name"

	// annSourceHash is written by the operator onto replicas; it is not a
	// request key but must not trigger an unknown-key warning.
	annSourceHash = "r8r.io/source-hash"
)

// fieldError describes a malformed annotation value. Its message names the
// exact annotation key and the problem, per the admission-validation spec.
type fieldError struct {
	key    string
	detail string
}

func (e *fieldError) message() string {
	return fmt.Sprintf("annotation %q is invalid: %s", e.key, e.detail)
}

// parsedRequest is the validated form of the r8r.io/* annotations on one
// object.
type parsedRequest struct {
	// optedIn is true when r8r.io/replicate is exactly "true".
	optedIn bool
	// clusterSelector is the parsed target-clusters selector; nil when the
	// annotation is absent.
	clusterSelector labels.Selector
	// clusterSelectorRaw is the raw annotation value, for messages.
	clusterSelectorRaw string
	// emptyClusterSelector notes an explicitly empty target-clusters value
	// (selects no clusters per spec) so the handler can warn.
	emptyClusterSelector bool
	// targetNamespaces is the requested namespace list, defaulted to the
	// source namespace when the annotation is absent.
	targetNamespaces []string
	// targetName is the optional explicit replica name override.
	targetName string
	// unknownKeys lists r8r.io/* keys outside the known contract (warning,
	// not rejection).
	unknownKeys []string
}

// hasR8RAnnotations reports whether the object carries at least one r8r.io/
// annotation key — the same predicate as the webhook configuration's CEL
// matchCondition.
func hasR8RAnnotations(ann map[string]string) bool {
	for k := range ann {
		if strings.HasPrefix(k, annPrefix) {
			return true
		}
	}
	return false
}

// parseRequest validates the r8r.io/* annotations. It returns a fieldError
// naming the offending annotation for any malformed value; unknown r8r.io/*
// keys are collected on the result instead of failing. sourceNamespace is used
// as the target-namespace default. On error the partially parsed result is
// still returned (for warning rendering).
func parseRequest(ann map[string]string, sourceNamespace string) (*parsedRequest, *fieldError) {
	p := &parsedRequest{}

	for k := range ann {
		if !strings.HasPrefix(k, annPrefix) {
			continue
		}
		switch k {
		case annReplicate, annTargetClusters, annTargetNamespaces, annTargetName, annSourceHash:
		default:
			p.unknownKeys = append(p.unknownKeys, k)
		}
	}

	if v, ok := ann[annReplicate]; ok {
		switch v {
		case "true":
			p.optedIn = true
		case "false":
			// Explicitly not opted in.
		default:
			return p, &fieldError{annReplicate, fmt.Sprintf("must be %q or %q, got %q", "true", "false", v)}
		}
	}

	if v, ok := ann[annTargetClusters]; ok {
		p.clusterSelectorRaw = v
		trimmed := strings.TrimSpace(v)
		if trimmed == "*" {
			return p, &fieldError{annTargetClusters,
				`explicit "*" is not supported; select all clusters with an empty label selector on a ReplicationPolicy instead`}
		}
		if trimmed == "" {
			p.emptyClusterSelector = true
		}
		sel, err := labels.Parse(trimmed)
		if err != nil {
			return p, &fieldError{annTargetClusters, fmt.Sprintf("not a valid label selector: %v", err)}
		}
		p.clusterSelector = sel
	}

	if v, ok := ann[annTargetNamespaces]; ok {
		for _, raw := range strings.Split(v, ",") {
			ns := strings.TrimSpace(raw)
			if ns == "" {
				return p, &fieldError{annTargetNamespaces,
					fmt.Sprintf("contains an empty entry (value %q); expected a comma-separated list of namespace names", v)}
			}
			if errs := validation.IsDNS1123Label(ns); len(errs) > 0 {
				return p, &fieldError{annTargetNamespaces,
					fmt.Sprintf("%q is not a valid namespace name: %s", ns, strings.Join(errs, "; "))}
			}
			p.targetNamespaces = append(p.targetNamespaces, ns)
		}
	} else {
		// Default per spec: the source namespace.
		p.targetNamespaces = []string{sourceNamespace}
	}

	if v, ok := ann[annTargetName]; ok {
		if errs := validation.IsDNS1123Subdomain(v); len(errs) > 0 {
			return p, &fieldError{annTargetName,
				fmt.Sprintf("%q is not a valid object name: %s", v, strings.Join(errs, "; "))}
		}
		p.targetName = v
	}

	return p, nil
}
