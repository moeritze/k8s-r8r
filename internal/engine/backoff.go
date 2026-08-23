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
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// backoffKey identifies one (replication, target cluster) pair. Keying
// per-cluster — not per replication — is design D9: one slow or unreachable
// cluster backs off independently without delaying the rest of the fanout,
// and future sharding-by-cluster requires no re-keying.
type backoffKey struct {
	replication types.NamespacedName
	cluster     string
}

// backoffTracker tracks consecutive per-(replication, cluster) failures and
// yields exponentially growing retry delays (base·2^(n-1), capped at max).
// The reconciler feeds the minimum delay across currently failing targets
// into RequeueAfter, so the soonest-due target sets the requeue.
type backoffTracker struct {
	base time.Duration
	max  time.Duration

	mu       sync.Mutex
	failures map[backoffKey]int
}

func newBackoffTracker(base, max time.Duration) *backoffTracker {
	return &backoffTracker{base: base, max: max, failures: map[backoffKey]int{}}
}

// Failure records a failed attempt for the pair and returns the delay before
// the next attempt.
func (t *backoffTracker) Failure(replication types.NamespacedName, cluster string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := backoffKey{replication, cluster}
	t.failures[k]++
	return t.delayLocked(t.failures[k])
}

// Success clears the failure streak for the pair.
func (t *backoffTracker) Success(replication types.NamespacedName, cluster string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failures, backoffKey{replication, cluster})
}

// Forget drops all state for a replication (used when it is deleted).
func (t *backoffTracker) Forget(replication types.NamespacedName) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k := range t.failures {
		if k.replication == replication {
			delete(t.failures, k)
		}
	}
}

// delayLocked computes base·2^(n-1) capped at max; n is the consecutive
// failure count (n >= 1).
func (t *backoffTracker) delayLocked(n int) time.Duration {
	d := t.base
	for i := 1; i < n; i++ {
		d *= 2
		if d >= t.max {
			return t.max
		}
	}
	if d > t.max {
		return t.max
	}
	return d
}
