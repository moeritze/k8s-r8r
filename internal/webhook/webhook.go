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

// Package webhook implements the ADVISORY validating admission webhook for
// replication requests (design D6, admission-validation spec).
//
// One handler serves both Secrets and ConfigMaps (and any future allowlisted
// kind): it decodes only object METADATA — never payload data — parses the
// r8r.io/* request annotations, and pre-checks them against the
// ReplicationPolicy universe so users get apply-time feedback instead of
// discovering a denial later in Replication status.
//
// # Advisory, never authoritative
//
// The webhook is UX only. It is deployed with failurePolicy: Ignore and CEL
// matchConditions that scope it to objects carrying at least one r8r.io/
// annotation (see config/webhook/manifests.yaml). The controller re-evaluates
// policy authoritatively on every reconcile, so a bypassed, misconfigured, or
// unavailable webhook can never cause unauthorized replication — it only
// delays feedback. For the same reason every internal failure here (undecodable
// metadata, policy list error) FAILS OPEN with a warning rather than denying.
//
// # What is checked at admission time vs reconcile time
//
// Admission time (this package):
//
//   - Annotation well-formedness: r8r.io/replicate boolean, target-clusters
//     label selector parses (explicit "*" rejected per spec), target-namespaces
//     are valid namespace names, target-name is a valid object name. Malformed
//     values are DENIED with a message naming the exact annotation. Unknown
//     r8r.io/* keys produce a WARNING, not a rejection (typos should not block
//     writes the controller would process fine; the known-key set may also grow
//     across versions, and an older webhook must not reject newer requests).
//   - Policy satisfiability WITHOUT live cluster inventory: source namespace
//     (exact-name lists only — see below), source kind, target namespaces, and
//     joint satisfiability of the requested cluster selector with policy
//     clusterSelectors. A write is denied only when NO policy could ever allow
//     the request regardless of which clusters exist. Denial messages name the
//     failing dimension using the same identifiers as internal/policy.
//
// Reconcile time only (authoritative, internal/policy.Evaluate):
//
//   - Resolution of target-clusters against the actual discovery inventory and
//     per-(cluster, namespace) target decisions.
//   - sources.namespaceSelector matching: namespace labels are not fetched at
//     admission time, so a policy using a namespaceSelector is conservatively
//     treated as matching the source namespace (fail open).
//   - Everything else: conflict/revocation options, kind allowlist gating,
//     namespace creation, drift.
//
// # Non-request traffic
//
// Objects without any r8r.io/ annotation are admitted immediately (and, in a
// correctly deployed configuration, never even reach the webhook thanks to the
// matchConditions). Annotation REMOVAL is always admitted: on UPDATE the match
// condition and this handler look at the incoming object, so a write that
// strips the annotations passes through — cleanup is legitimate and handled by
// the controller.
package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
	"github.com/moeritze/k8s-r8r/internal/telemetry"
)

// Path is the HTTP path the validating webhook is served on. It must match
// clientConfig.service.path in config/webhook/manifests.yaml.
const Path = "/validate-replication-request"

// Setup registers the advisory replication-request validator on the manager's
// webhook server. Call it from main after the manager is constructed; it uses
// the manager's cached client to list ReplicationPolicies.
func Setup(mgr ctrl.Manager) error {
	mgr.GetWebhookServer().Register(Path, &crwebhook.Admission{Handler: NewHandler(mgr.GetClient())})
	return nil
}

// Handler is the admission.Handler for replication-request validation. It is
// kind-agnostic: one instance handles Secrets, ConfigMaps, and any future
// allowlisted kind, because it only reads object metadata.
type Handler struct {
	// reader lists ReplicationPolicy objects; in production this is the
	// manager's cached client.
	reader client.Reader
}

// NewHandler returns a Handler that pre-checks requests against the policies
// listed through reader.
func NewHandler(reader client.Reader) *Handler {
	return &Handler{reader: reader}
}

// Handle implements admission.Handler. See the package documentation for the
// exact admission-time contract. Responses never contain object payload data —
// only metadata (annotation values, names, namespaces) may appear in messages.
func (h *Handler) Handle(ctx context.Context, req admission.Request) admission.Response {
	if req.Operation != admissionv1.Create && req.Operation != admissionv1.Update {
		return admission.Allowed("only CREATE and UPDATE are validated")
	}

	// Decode metadata only. The raw object is a Secret or ConfigMap; we must
	// never touch (or risk echoing) its payload, so we unmarshal into
	// PartialObjectMetadata instead of the full type.
	var meta metav1.PartialObjectMetadata
	if err := json.Unmarshal(req.Object.Raw, &meta); err != nil {
		// Advisory webhook: fail open, never block on our own decode problems.
		return admission.Allowed("k8s-r8r: object metadata could not be decoded; advisory validation skipped").
			WithWarnings(fmt.Sprintf("k8s-r8r advisory validation skipped: decoding metadata: %v", err))
	}

	if !hasR8RAnnotations(meta.Annotations) {
		// Also enforced by the webhook configuration's matchConditions; kept
		// here so the handler is safe under any configuration. Covers
		// annotation removal on UPDATE: only the incoming object is inspected.
		return admission.Allowed("no r8r.io annotations present")
	}

	parsed, ferr := parseRequest(meta.Annotations, req.Namespace)
	warnings := warningsFor(parsed)
	if ferr != nil {
		telemetry.IncWebhookDenial("annotations")
		return admission.Denied(ferr.Error()).WithWarnings(warnings...)
	}

	if !parsed.optedIn {
		return admission.Allowed("replication not requested (r8r.io/replicate is not \"true\")").
			WithWarnings(warnings...)
	}

	var policies r8rv1alpha1.ReplicationPolicyList
	if err := h.reader.List(ctx, &policies); err != nil {
		// Advisory webhook: fail open on infrastructure errors.
		return admission.Allowed("k8s-r8r: ReplicationPolicy list failed; advisory validation skipped").
			WithWarnings(append(warnings,
				fmt.Sprintf("k8s-r8r advisory validation skipped: listing ReplicationPolicies: %v", err))...)
	}

	if d := checkPolicies(policies.Items, req.Kind.Kind, req.Namespace, parsed); d != nil {
		telemetry.IncWebhookDenial(d.dimension)
		return admission.Denied(fmt.Sprintf(
			"replication request denied (advisory pre-check; dimension %s): %s "+
				"The controller re-evaluates policy authoritatively at reconcile time.",
			d.dimension, d.message)).WithWarnings(warnings...)
	}

	return admission.Allowed("replication request passes advisory policy pre-check").
		WithWarnings(warnings...)
}

// warningsFor renders deterministic advisory warnings: one per unknown
// r8r.io/* annotation key, plus a hint for an empty cluster selector. Unknown
// keys warn instead of denying: see package docs.
func warningsFor(p *parsedRequest) []string {
	if p == nil {
		return nil
	}
	var out []string
	keys := append([]string(nil), p.unknownKeys...)
	slices.Sort(keys)
	for _, k := range keys {
		out = append(out, fmt.Sprintf("unknown annotation %q is ignored by k8s-r8r (known keys: %s, %s, %s, %s)",
			k, annReplicate, annTargetClusters, annTargetNamespaces, annTargetName))
	}
	if p.emptyClusterSelector {
		out = append(out, fmt.Sprintf("%s is empty: an empty selector selects no target clusters", annTargetClusters))
	}
	return out
}
