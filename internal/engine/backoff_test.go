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
	"time"

	"k8s.io/apimachinery/pkg/types"
)

func TestBackoffTracker_ExponentialWithCap(t *testing.T) {
	tr := newBackoffTracker(time.Second, 10*time.Second)
	nn := types.NamespacedName{Namespace: "app", Name: "rep"}

	want := []time.Duration{
		1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		10 * time.Second, 10 * time.Second,
	}
	for i, w := range want {
		if got := tr.Failure(nn, "spoke-a"); got != w {
			t.Errorf("failure %d: delay = %v, want %v", i+1, got, w)
		}
	}
}

// Design D9: failures are keyed per (replication, cluster), so one cluster's
// backoff never grows another's.
func TestBackoffTracker_PerClusterIndependence(t *testing.T) {
	tr := newBackoffTracker(time.Second, time.Minute)
	nn := types.NamespacedName{Namespace: "app", Name: "rep"}

	tr.Failure(nn, "spoke-a")
	tr.Failure(nn, "spoke-a")
	if got := tr.Failure(nn, "spoke-b"); got != time.Second {
		t.Errorf("fresh cluster delay = %v, want base", got)
	}
	other := types.NamespacedName{Namespace: "app", Name: "rep2"}
	if got := tr.Failure(other, "spoke-a"); got != time.Second {
		t.Errorf("fresh replication delay = %v, want base", got)
	}
}

func TestBackoffTracker_SuccessResetsAndForgetClears(t *testing.T) {
	tr := newBackoffTracker(time.Second, time.Minute)
	nn := types.NamespacedName{Namespace: "app", Name: "rep"}

	tr.Failure(nn, "spoke-a")
	tr.Failure(nn, "spoke-a")
	tr.Success(nn, "spoke-a")
	if got := tr.Failure(nn, "spoke-a"); got != time.Second {
		t.Errorf("delay after success = %v, want base", got)
	}

	tr.Failure(nn, "spoke-b")
	tr.Forget(nn)
	if got := tr.Failure(nn, "spoke-a"); got != time.Second {
		t.Errorf("delay after forget = %v, want base", got)
	}
	if got := tr.Failure(nn, "spoke-b"); got != time.Second {
		t.Errorf("delay after forget = %v, want base", got)
	}
}

func TestMinDelay(t *testing.T) {
	if got := minDelay(nil); got != 0 {
		t.Errorf("minDelay(nil) = %v", got)
	}
	if got := minDelay([]time.Duration{5 * time.Second, 2 * time.Second, 30 * time.Second}); got != 2*time.Second {
		t.Errorf("minDelay = %v, want 2s", got)
	}
}
