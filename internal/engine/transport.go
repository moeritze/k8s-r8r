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
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ErrClusterUnavailable is returned (wrapped) by transports when the target
// cluster currently has no usable client — not registered, runtime not
// started, or deregistered. Callers treat it as a retryable per-target
// condition (reason ClusterUnreachable), never as a fatal error.
var ErrClusterUnavailable = errors.New("cluster client unavailable")

// Transport moves replicas to and from target clusters. It is the pluggable
// seam of design D2: the launch implementation pushes via SA-token clients
// from the cluster runtime manager; a future pull-agent transport implements
// the same interface without engine changes.
//
// All methods operate on unstructured objects (kind-agnostic pipeline) and
// address clusters by their discovered inventory name.
type Transport interface {
	// Apply creates or updates obj on the cluster (server-side apply
	// semantics: the engine owns the fields it writes).
	Apply(ctx context.Context, cluster string, obj *unstructured.Unstructured) error
	// Get reads the object identified by key into obj; obj must carry the
	// desired GroupVersionKind. Reads are live (never served from a hub
	// cache) so replica payloads are never cached on the hub (design D3).
	Get(ctx context.Context, cluster string, key client.ObjectKey, obj *unstructured.Unstructured) error
	// Delete removes obj from the cluster. NotFound errors are returned
	// verbatim so callers can treat them as success.
	Delete(ctx context.Context, cluster string, obj *unstructured.Unstructured) error
}

// ClientGetter resolves a cluster name to a live client. *cluster.Manager
// satisfies it: clients exist only for registered clusters whose runtime is
// started.
type ClientGetter interface {
	GetClient(name string) (client.Client, bool)
}

// PushTransport is the launch Transport: direct writes to spokes using the
// SA-token clients maintained by the cluster runtime manager (design D2/D5).
//
// Writes use server-side apply with the engine's field manager and forced
// ownership: replicas are wholly operator-managed, so on a field-manager
// conflict taking ownership is the correct resolution — a replica's payload
// must always converge to the source. Unstructured reads through
// controller-runtime cluster clients bypass the cache, honoring D3.
type PushTransport struct {
	// Clients resolves cluster names to clients (usually *cluster.Manager).
	Clients ClientGetter
	// FieldManager is the server-side-apply field manager name; empty
	// means "k8s-r8r".
	FieldManager string
}

// NewPushTransport builds a PushTransport over the given client source.
func NewPushTransport(clients ClientGetter, fieldManager string) *PushTransport {
	return &PushTransport{Clients: clients, FieldManager: fieldManager}
}

func (t *PushTransport) fieldManager() string {
	if t.FieldManager == "" {
		return ManagedByValue
	}
	return t.FieldManager
}

func (t *PushTransport) clientFor(cluster string) (client.Client, error) {
	c, ok := t.Clients.GetClient(cluster)
	if !ok || c == nil {
		return nil, fmt.Errorf("cluster %q: %w", cluster, ErrClusterUnavailable)
	}
	return c, nil
}

// Apply implements Transport via server-side apply with forced ownership.
func (t *PushTransport) Apply(ctx context.Context, cluster string, obj *unstructured.Unstructured) error {
	c, err := t.clientFor(cluster)
	if err != nil {
		return err
	}
	return c.Patch(ctx, obj, client.Apply, client.FieldOwner(t.fieldManager()), client.ForceOwnership)
}

// Get implements Transport with a live read.
func (t *PushTransport) Get(ctx context.Context, cluster string, key client.ObjectKey, obj *unstructured.Unstructured) error {
	c, err := t.clientFor(cluster)
	if err != nil {
		return err
	}
	return c.Get(ctx, key, obj)
}

// Delete implements Transport; NotFound is returned to the caller.
func (t *PushTransport) Delete(ctx context.Context, cluster string, obj *unstructured.Unstructured) error {
	c, err := t.clientFor(cluster)
	if err != nil {
		return err
	}
	return c.Delete(ctx, obj)
}
