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
	"testing"
	"time"
)

func TestEventLimiter(t *testing.T) {
	now := time.Now()
	l := NewEventLimiter(time.Minute)
	l.now = func() time.Time { return now }

	if !l.Allow("uid-1", "Replicated", "1/1 targets ready") {
		t.Error("first event must be allowed")
	}
	// Flapping target: identical repeats inside the cooldown are coalesced.
	if l.Allow("uid-1", "Replicated", "1/1 targets ready") {
		t.Error("identical repeat within cooldown must be suppressed")
	}
	// New information always passes.
	if !l.Allow("uid-1", "Replicated", "2/2 targets ready") {
		t.Error("changed message must be allowed")
	}
	// Different reason and different object are independent.
	if !l.Allow("uid-1", "Conflict", "1/1 targets ready") {
		t.Error("different reason must be allowed")
	}
	if !l.Allow("uid-2", "Replicated", "1/1 targets ready") {
		t.Error("different object must be allowed")
	}
	// After the cooldown the repeat passes again.
	now = now.Add(2 * time.Minute)
	if !l.Allow("uid-1", "Conflict", "1/1 targets ready") {
		t.Error("repeat after cooldown must be allowed")
	}
	// Forget clears an object's state entirely.
	l.Forget("uid-2")
	if !l.Allow("uid-2", "Replicated", "1/1 targets ready") {
		t.Error("event after Forget must be allowed")
	}
}

func TestEventLimiterDefaults(t *testing.T) {
	l := NewEventLimiter(0)
	if l.cooldown != DefaultEventCooldown {
		t.Errorf("cooldown = %v, want %v", l.cooldown, DefaultEventCooldown)
	}
}
