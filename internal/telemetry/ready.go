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
	"context"
	"errors"
	"net/http"
	"sync"
)

// SyncWaiter is the cache-sync surface of the HUB cache (satisfied by
// controller-runtime's cache.Cache). By construction the readiness gate can
// only ever consult this one waiter — spoke/cluster state has no way in, so
// an individual unreachable target cluster can never flip readiness
// (observability-operations spec, "One spoke down" scenario).
type SyncWaiter interface {
	WaitForCacheSync(ctx context.Context) bool
}

// InformerSync is a manager runnable plus healthz checker: readiness fails
// until the hub informers have synced once, then stays ready. It runs on
// every replica (no leader election) because standby replicas must also
// report ready to serve webhooks and be eligible for failover.
type InformerSync struct {
	waiter SyncWaiter

	once   sync.Once
	synced chan struct{}
}

// NewInformerSync builds the readiness gate over the hub cache.
func NewInformerSync(w SyncWaiter) *InformerSync {
	return &InformerSync{waiter: w, synced: make(chan struct{})}
}

// Start implements manager.Runnable: it blocks until the hub cache is
// synced, marks readiness, and then waits for shutdown.
func (s *InformerSync) Start(ctx context.Context) error {
	if s.waiter.WaitForCacheSync(ctx) {
		s.once.Do(func() { close(s.synced) })
	}
	<-ctx.Done()
	return nil
}

// NeedLeaderElection implements manager.LeaderElectionRunnable: readiness
// must not depend on leadership.
func (s *InformerSync) NeedLeaderElection() bool { return false }

// Check is a healthz.Checker for the readyz endpoint.
func (s *InformerSync) Check(_ *http.Request) error {
	select {
	case <-s.synced:
		return nil
	default:
		return errors.New("hub informers not yet synced")
	}
}
