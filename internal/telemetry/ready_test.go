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
	"testing"
	"time"
)

// fakeWaiter blocks WaitForCacheSync until released.
type fakeWaiter struct {
	release chan struct{}
	result  bool
}

func (w *fakeWaiter) WaitForCacheSync(ctx context.Context) bool {
	select {
	case <-w.release:
		return w.result
	case <-ctx.Done():
		return false
	}
}

// Observability spec: readiness reflects hub informer sync — not ready
// before sync, ready after, and (by construction: the gate's only input is
// the hub cache waiter) never influenced by spoke connectivity.
func TestInformerSyncReadiness(t *testing.T) {
	w := &fakeWaiter{release: make(chan struct{}), result: true}
	s := NewInformerSync(w)

	if err := s.Check(nil); err == nil {
		t.Fatal("readiness must fail before informers synced")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Start(ctx)
	}()

	if err := s.Check(nil); err == nil {
		t.Error("readiness must still fail while sync is pending")
	}

	close(w.release)
	deadline := time.After(2 * time.Second)
	for {
		if err := s.Check(nil); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("readiness did not become OK after cache sync")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	<-done
}

func TestInformerSyncRunsOnAllReplicas(t *testing.T) {
	s := NewInformerSync(&fakeWaiter{})
	if s.NeedLeaderElection() {
		t.Error("readiness gate must run on non-leader replicas too")
	}
}
