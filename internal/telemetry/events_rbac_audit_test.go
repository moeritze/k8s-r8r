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

// Repo-level RBAC audit for event recording. The operator records events
// through the events.k8s.io/v1 API (manager.GetEventRecorder →
// client-go tools/events), so the manager's ClusterRole MUST grant
// create/patch on events in the "events.k8s.io" API group — the core ""
// group grant alone does not cover it. Without the grant every event write
// is rejected with 403 and silently dropped by the recorder: reconciliation
// keeps working, but lifecycle announcements (Replicated, ClusterGone,
// PolicyRevoked, ...) never appear. This is exactly the failure mode that
// broke the deregistration e2e test in CI, so this test ratchets the grant
// into both RBAC sources.
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
				"the events.k8s.io/v1 recorder (mgr.GetEventRecorder) needs it or all operator events are dropped with 403", rel)
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
