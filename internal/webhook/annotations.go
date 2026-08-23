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

// Admission-time adapter over the shared r8r.io/* annotation parser
// (internal/annotations) — the same parser the request controller uses, so
// apply-time error messages and reconcile-time behavior can never diverge.
//
// The webhook's contract differs from the controller's in exactly two ways,
// both handled here before delegating to annotations.Parse:
//
//   - Unknown r8r.io/* keys WARN instead of denying (typos should not block
//     writes the controller would process fine, and an older webhook must not
//     reject requests using newer keys). They are filtered out before Parse,
//     which would reject them.
//   - The policy pre-check needs to know whether a cluster selector was
//     explicitly provided (absent/empty selectors skip the satisfiability
//     check), which Parse's never-nil selector does not expose.

import (
	"strings"

	"k8s.io/apimachinery/pkg/labels"

	"github.com/moeritze/k8s-r8r/internal/annotations"
)

// Annotation keys of the replication-request contract, aliased from the
// shared parser package.
const (
	annPrefix           = annotations.Prefix
	annReplicate        = annotations.KeyReplicate
	annTargetClusters   = annotations.KeyTargetClusters
	annTargetNamespaces = annotations.KeyTargetNamespaces
	annTargetName       = annotations.KeyTargetName

	// annSourceHash is written by the operator onto replicas; it is not a
	// request key but must not trigger an unknown-key warning.
	annSourceHash = annotations.KeySourceHash
)

// knownKeys is the closed set of r8r.io/* keys the shared parser accepts.
var knownKeys = map[string]bool{
	annReplicate:        true,
	annTargetClusters:   true,
	annTargetNamespaces: true,
	annTargetName:       true,
	annSourceHash:       true,
}

// parsedRequest is the validated form of the r8r.io/* annotations on one
// object, shaped for the admission-time policy pre-check.
type parsedRequest struct {
	// optedIn is true when r8r.io/replicate is exactly "true".
	optedIn bool
	// clusterSelector is the parsed target-clusters selector; nil when the
	// annotation is absent or empty (the satisfiability check is skipped).
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

// parseRequest validates the r8r.io/* annotations through the shared parser.
// It returns an error naming every offending annotation for malformed values;
// unknown r8r.io/* keys are collected on the result instead of failing.
// sourceNamespace is used as the target-namespace default. On error the
// partially parsed result is still returned (for warning rendering).
func parseRequest(ann map[string]string, sourceNamespace string) (*parsedRequest, error) {
	p := &parsedRequest{}

	// Filter unknown r8r.io/* keys: the shared parser rejects them (the
	// controller fails loudly on typos), but at admission time they only
	// warn — see the package comment.
	filtered := make(map[string]string, len(ann))
	for k, v := range ann {
		if strings.HasPrefix(k, annPrefix) && !knownKeys[k] {
			p.unknownKeys = append(p.unknownKeys, k)
			continue
		}
		filtered[k] = v
	}

	p.optedIn = annotations.Replicates(filtered)
	rawSelector, selectorPresent := filtered[annTargetClusters]
	if selectorPresent {
		p.clusterSelectorRaw = rawSelector
		p.emptyClusterSelector = strings.TrimSpace(rawSelector) == ""
	}

	req, err := annotations.Parse(filtered)
	if err != nil {
		return p, err
	}
	if req == nil {
		// Not opted in (replicate absent or "false"); all annotations are
		// valid — the handler admits without a policy pre-check.
		return p, nil
	}

	if selectorPresent && !p.emptyClusterSelector {
		p.clusterSelector = req.ClusterSelector
	}
	p.targetNamespaces = req.EffectiveNamespaces(sourceNamespace)
	p.targetName = req.TargetName
	return p, nil
}
