// Package discovery defines the pluggable cluster discovery layer.
//
// A discovery Provider turns a fleet-management system (ClusterAPI, Fleet,
// Rancher, a static kubeconfig list, ...) into replication target inventory.
// The contract is intentionally tiny: a provider emits ClusterRecords (stable
// name, labels, readiness, credential reference) plus register / update /
// deregister events, and can be asked for a point-in-time snapshot via List.
//
// Providers are selected by name through a Registry (see registry.go), so
// adding a new provider requires no changes to the engine, policy, or request
// layers: implement Provider, register a Factory, and pick the provider name
// in operator deployment configuration.
package discovery

import (
	"context"
	"maps"
)

// CredentialRef points at a kubeconfig-bearing Secret on the hub cluster that
// holds the provider's admin credential for a target cluster. The credential
// is used exactly once per bootstrap (see internal/cluster); steady-state
// traffic never uses it.
type CredentialRef struct {
	// Namespace of the Secret on the hub.
	Namespace string
	// Name of the Secret on the hub.
	Name string
}

// String renders the reference as "namespace/name". It never contains secret
// data and is safe to log.
func (r CredentialRef) String() string {
	return r.Namespace + "/" + r.Name
}

// ClusterRecord is a provider's view of one target cluster.
type ClusterRecord struct {
	// Name is the stable identity of the cluster. It must not change for
	// the lifetime of the cluster; all inventory, workqueue keys, and
	// runtime bookkeeping key off it.
	Name string
	// Labels are the selection labels for policy / request cluster
	// selectors.
	Labels map[string]string
	// Ready reports whether the cluster is a valid replication target
	// (for ClusterAPI: control plane ready). Runtimes are only started
	// for ready clusters.
	Ready bool
	// CredentialRef references the hub Secret holding the admin
	// kubeconfig used for one-shot spoke bootstrap.
	CredentialRef CredentialRef
}

// Clone returns a deep copy of the record (labels map not aliased).
func (r ClusterRecord) Clone() ClusterRecord {
	out := r
	if r.Labels != nil {
		out.Labels = make(map[string]string, len(r.Labels))
		maps.Copy(out.Labels, r.Labels)
	}
	return out
}

// EventType classifies a discovery event.
type EventType string

const (
	// EventRegister is emitted when a cluster first appears in the
	// provider's inventory.
	EventRegister EventType = "Register"
	// EventUpdate is emitted when an already-registered cluster's record
	// changes (labels, readiness, credential reference).
	EventUpdate EventType = "Update"
	// EventDeregister is emitted when a cluster leaves the provider's
	// inventory (e.g. the ClusterAPI Cluster object is deleted).
	EventDeregister EventType = "Deregister"
)

// Event is a single inventory change notification.
type Event struct {
	Type   EventType
	Record ClusterRecord
}

// EventHandler receives discovery events. Handlers are called synchronously
// from the provider's watch machinery and must not block for long.
type EventHandler func(Event)

// Provider is the pluggable discovery contract. Implementations watch their
// backing system and translate it into ClusterRecords and events.
//
// Usage: Subscribe handlers first, then Start. Start blocks until the context
// is cancelled (controller-runtime Runnable style).
type Provider interface {
	// Name returns the provider's registry name (e.g. "cluster-api").
	Name() string
	// Subscribe adds an event handler. Must be called before Start.
	Subscribe(EventHandler)
	// Start begins watching and blocks until ctx is done. Handlers
	// receive an EventRegister for every cluster present at startup.
	Start(ctx context.Context) error
	// List returns a snapshot of all currently known cluster records.
	List() []ClusterRecord
	// Watching reports whether the provider's inventory source is
	// established (for watch-based providers: the informer is running and
	// synced). It exists because List alone cannot distinguish a broken
	// provider from a fleet with no clusters — both return nothing — and
	// that ambiguity is exactly what makes a discovery outage invisible.
	// It is the source of the k8s_r8r_discovery_up metric.
	Watching() bool
}
