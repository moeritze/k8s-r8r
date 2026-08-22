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
	"sync"
	"time"
)

// DefaultEventCooldown is the per-(object, reason) repeat-suppression
// window used when NewEventLimiter is given a non-positive cooldown.
const DefaultEventCooldown = 5 * time.Minute

// EventLimiter de-duplicates Kubernetes events per (object, reason): a
// repeat of an identical message within the cooldown window is suppressed,
// so a flapping target cannot flood the event stream (observability spec,
// "Flapping target" scenario). A changed message for the same reason is
// always allowed through — new information is never suppressed.
//
// This complements (does not replace) the client-go event correlator's
// aggregation: the correlator still counts bursts; the limiter stops the
// engine from even emitting steady-state repeats.
type EventLimiter struct {
	cooldown time.Duration
	now      func() time.Time

	mu   sync.Mutex
	last map[eventKey]eventEntry
}

type eventKey struct {
	object string
	reason string
}

type eventEntry struct {
	message string
	at      time.Time
}

// NewEventLimiter builds a limiter; cooldown <= 0 selects
// DefaultEventCooldown.
func NewEventLimiter(cooldown time.Duration) *EventLimiter {
	if cooldown <= 0 {
		cooldown = DefaultEventCooldown
	}
	return &EventLimiter{
		cooldown: cooldown,
		now:      time.Now,
		last:     map[eventKey]eventEntry{},
	}
}

// Allow reports whether an event with this (object, reason, message) should
// be emitted now, recording it when allowed. object is any stable per-object
// key (e.g. the UID).
func (l *EventLimiter) Allow(object, reason, message string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := eventKey{object: object, reason: reason}
	now := l.now()
	if e, ok := l.last[k]; ok && e.message == message && now.Sub(e.at) < l.cooldown {
		return false
	}
	l.last[k] = eventEntry{message: message, at: now}
	return true
}

// Forget drops all state for an object (call when the object is deleted so
// the map stays bounded by live objects x reasons).
func (l *EventLimiter) Forget(object string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k := range l.last {
		if k.object == object {
			delete(l.last, k)
		}
	}
}
