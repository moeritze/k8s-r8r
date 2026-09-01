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

package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Repo-level RBAC audit for event recording: the manager's ClusterRole MUST
// grant create/patch on events in the "events.k8s.io" API group, in addition
// to the core "" group grant.
//
// The operator currently records through the core/v1 recorder
// (manager.GetEventRecorderFor → client-go tools/record), which only needs
// the "" group — the events.k8s.io grant is retained deliberately, and this
// test ratchets it into both RBAC sources so it cannot be dropped.
//
// Rationale: the events.k8s.io/v1 recorder (manager.GetEventRecorder →
// client-go tools/events) writes to the "events.k8s.io" group, and a missing
// grant there fails *silently* — every event write is rejected with 403 and
// swallowed by the recorder, so reconciliation keeps working while lifecycle
// announcements (Replicated, ClusterGone, PolicyRevoked, ...) simply never
// appear. That failure mode once broke the deregistration e2e test in CI and
// took real time to find. Keeping the grant means any future re-adoption of
// the newer events API — deliberate or accidental, e.g. a scaffolding
// regeneration — cannot silently regress into it.
//
// Do not remove this test or the grant "because we no longer use that API":
// the grant is cheap, and its absence is expensive precisely because it is
// invisible.
func TestEventsV1RBACGrantPresent(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{
		filepath.Join("config", "rbac", "role.yaml"),
		filepath.Join("charts", "k8s-r8r", "templates", "rbac.yaml"),
	} {
		path := filepath.Join(root, rel)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		if !hasEventsV1Rule(string(raw)) {
			t.Errorf("%s: no RBAC rule grants create+patch on events in the events.k8s.io API group; "+
				"the grant is kept deliberately so that any future switch back to the events.k8s.io/v1 recorder "+
				"(mgr.GetEventRecorder) cannot silently drop every operator event with a 403", rel)
		}
	}
}

// hasEventsV1Rule scans RBAC YAML for a rule block ("- apiGroups:" ... up to
// the next rule) that contains the events.k8s.io group, the events resource,
// and both the create and patch verbs. Line-based on purpose: the chart file
// contains Helm templating that defeats strict YAML parsing.
func hasEventsV1Rule(doc string) bool {
	var block []string
	check := func() bool {
		joined := strings.Join(block, "\n")
		return strings.Contains(joined, "events.k8s.io") &&
			strings.Contains(joined, "- events") &&
			strings.Contains(joined, "- create") &&
			strings.Contains(joined, "- patch")
	}
	for line := range strings.SplitSeq(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- apiGroups:") {
			if len(block) > 0 && check() {
				return true
			}
			block = block[:0]
		}
		if len(block) > 0 || strings.HasPrefix(strings.TrimSpace(line), "- apiGroups:") {
			block = append(block, line)
		}
	}
	return len(block) > 0 && check()
}
