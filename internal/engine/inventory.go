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
	r8rv1alpha1 "github.com/moeritze/k8s-r8r/api/v1alpha1"
)

// SlotKey identifies one replica slot: a concrete object on a concrete
// cluster. It is the join key between desired state (resolved targets ×
// namespaces, with the resolved replica name) and recorded state (the
// Replication inventory).
type SlotKey struct {
	Cluster   string
	Namespace string
	Name      string
	Group     string
	Kind      string
}

// KeyForEntry derives the SlotKey of an inventory entry.
func KeyForEntry(e r8rv1alpha1.InventoryEntry) SlotKey {
	return SlotKey{
		Cluster:   e.ClusterName,
		Namespace: e.Namespace,
		Name:      e.Name,
		Group:     e.Group,
		Kind:      e.Kind,
	}
}

// GCPlan is the outcome of diffing inventory against desired state:
//
//   - Delete: replicas that must be removed from their (still discovered)
//     clusters — deselected targets, renamed replicas, revoked targets under
//     revocationPolicy Delete, or everything on Replication deletion.
//   - Release: inventory entries whose cluster has left discovery entirely;
//     there is no credential or endpoint left to clean them with, so they are
//     released with a ClusterGone event instead of blocking forever
//     (replication-engine spec, "Unreachable target during cleanup").
type GCPlan struct {
	Delete  []r8rv1alpha1.InventoryEntry
	Release []r8rv1alpha1.InventoryEntry
}

// PlanGC computes the garbage-collection plan for one reconcile. keep holds
// the slots that must survive: currently desired (allowed) slots — whether or
// not their apply succeeded this round — plus revoked-but-retained slots.
// clusterGone reports whether a cluster has been removed from discovery
// inventory. Entries are never dropped silently: every entry is either kept,
// scheduled for deletion, or released via ClusterGone ("no code path may
// lose track of a created replica").
func PlanGC(inventory []r8rv1alpha1.InventoryEntry, keep map[SlotKey]bool, clusterGone func(cluster string) bool) GCPlan {
	var plan GCPlan
	for _, e := range inventory {
		if keep[KeyForEntry(e)] {
			continue
		}
		if clusterGone(e.ClusterName) {
			plan.Release = append(plan.Release, e)
			continue
		}
		plan.Delete = append(plan.Delete, e)
	}
	return plan
}

// upsertEntry adds or updates an inventory entry in place, returning the
// updated slice. Matching is by SlotKey; the hash is refreshed on update.
func upsertEntry(inv []r8rv1alpha1.InventoryEntry, entry r8rv1alpha1.InventoryEntry) []r8rv1alpha1.InventoryEntry {
	key := KeyForEntry(entry)
	for i := range inv {
		if KeyForEntry(inv[i]) == key {
			inv[i].LastAppliedHash = entry.LastAppliedHash
			return inv
		}
	}
	return append(inv, entry)
}

// removeEntry drops the entry with the given key, returning the updated
// slice.
func removeEntry(inv []r8rv1alpha1.InventoryEntry, key SlotKey) []r8rv1alpha1.InventoryEntry {
	out := inv[:0]
	for _, e := range inv {
		if KeyForEntry(e) != key {
			out = append(out, e)
		}
	}
	return out
}

// entriesInNamespace returns the inventory entries living in the given
// (cluster, namespace) pair — used to map a revoked policy target (which has
// no name component) to the concrete replica slots it covers.
func entriesInNamespace(inv []r8rv1alpha1.InventoryEntry, cluster, namespace string) []r8rv1alpha1.InventoryEntry {
	var out []r8rv1alpha1.InventoryEntry
	for _, e := range inv {
		if e.ClusterName == cluster && e.Namespace == namespace {
			out = append(out, e)
		}
	}
	return out
}
