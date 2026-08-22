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
	"testing"

	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
)

func entry(cluster, ns, name string) r8rv1alpha1.InventoryEntry {
	return r8rv1alpha1.InventoryEntry{
		ClusterName: cluster, Namespace: ns, Name: name, Kind: "Secret",
	}
}

// PlanGC decision table: kept slots survive, deselected slots on discovered
// clusters are deleted, entries on clusters gone from discovery are released
// (spec "Inventory and garbage collection", "Unreachable target during
// cleanup").
func TestPlanGC(t *testing.T) {
	inv := []r8rv1alpha1.InventoryEntry{
		entry("spoke-a", "app", "web-creds"),    // still desired
		entry("spoke-a", "old", "web-creds"),    // deselected, cluster alive
		entry("spoke-b", "app", "web-creds"),    // retained (revoked, Retain)
		entry("spoke-gone", "app", "web-creds"), // deselected, cluster gone
	}
	keep := map[SlotKey]bool{
		KeyForEntry(inv[0]): true,
		KeyForEntry(inv[2]): true,
	}
	gone := func(cluster string) bool { return cluster == "spoke-gone" }

	plan := PlanGC(inv, keep, gone)

	if len(plan.Delete) != 1 || plan.Delete[0].Namespace != "old" {
		t.Errorf("Delete = %+v, want exactly the deselected spoke-a/old entry", plan.Delete)
	}
	if len(plan.Release) != 1 || plan.Release[0].ClusterName != "spoke-gone" {
		t.Errorf("Release = %+v, want exactly the spoke-gone entry", plan.Release)
	}
}

// Every inventory entry must be accounted for: kept, deleted, or released —
// never silently dropped ("no code path may lose track of a created
// replica").
func TestPlanGC_AccountsForEveryEntry(t *testing.T) {
	inv := []r8rv1alpha1.InventoryEntry{
		entry("a", "x", "1"), entry("b", "y", "2"), entry("c", "z", "3"),
	}
	keep := map[SlotKey]bool{KeyForEntry(inv[1]): true}
	plan := PlanGC(inv, keep, func(c string) bool { return c == "c" })

	if got := len(plan.Delete) + len(plan.Release) + len(keep); got != len(inv) {
		t.Errorf("entries accounted: %d, want %d", got, len(inv))
	}
}

func TestUpsertRemoveEntries(t *testing.T) {
	var inv []r8rv1alpha1.InventoryEntry
	e1 := entry("spoke-a", "app", "web-creds")
	e1.LastAppliedHash = "sha256:aaa"

	inv = upsertEntry(inv, e1)
	if len(inv) != 1 {
		t.Fatalf("len = %d after first upsert", len(inv))
	}
	e1b := e1
	e1b.LastAppliedHash = "sha256:bbb"
	inv = upsertEntry(inv, e1b)
	if len(inv) != 1 || inv[0].LastAppliedHash != "sha256:bbb" {
		t.Fatalf("upsert did not update in place: %+v", inv)
	}

	inv = upsertEntry(inv, entry("spoke-b", "app", "web-creds"))
	inv = removeEntry(inv, KeyForEntry(e1))
	if len(inv) != 1 || inv[0].ClusterName != "spoke-b" {
		t.Fatalf("remove kept wrong entries: %+v", inv)
	}
}

func TestEntriesInNamespace(t *testing.T) {
	inv := []r8rv1alpha1.InventoryEntry{
		entry("spoke-a", "app", "one"),
		entry("spoke-a", "app", "two"),
		entry("spoke-a", "other", "one"),
		entry("spoke-b", "app", "one"),
	}
	got := entriesInNamespace(inv, "spoke-a", "app")
	if len(got) != 2 {
		t.Fatalf("entriesInNamespace returned %d entries, want 2", len(got))
	}
}
