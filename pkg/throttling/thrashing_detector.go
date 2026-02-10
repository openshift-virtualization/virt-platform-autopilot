/*
Copyright 2026 The KubeVirt Authors.

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

package throttling

import (
	"sync"
)

const (
	// ThrashingThreshold is the number of consecutive throttles before pausing
	// 3 throttles = resource has been modified 15 times in 1 minute (5 updates/min capacity)
	ThrashingThreshold = 3
)

// ThrashingDetector tracks persistent edit wars and signals when to pause reconciliation.
//
// Design Philosophy: Pause-with-Annotation
// When an edit war is detected (external actor repeatedly modifying resources):
// 1. Set annotation: platform.kubevirt.io/reconcile-paused="true"
// 2. Emit metric once (stable, no flapping)
// 3. Stop reconciliation until user removes annotation
// 4. Self-documenting: Git history shows what happened and when
//
// Why not exponential backoff?
// - Backoff delays the problem, doesn't solve it
// - Alerts still flap (metric increments on every retry)
// - Wastes reconciliation cycles
// - State lost on operator restart
//
// Benefits of pause-with-annotation:
// - Zero wasted cycles (reconciliation fully stopped)
// - Stable metrics and alerts (one increment, stays high)
// - Survives operator restarts (annotation persists)
// - Clear recovery path (remove annotation or fix conflict)
// - GitOps-friendly (self-documenting in Git history)
type ThrashingDetector struct {
	mu     sync.RWMutex
	states map[string]*thrashingState
}

type thrashingState struct {
	// consecutiveThrottles counts how many times we've been throttled in a row
	consecutiveThrottles int

	// metricEmitted tracks if we've already incremented the metric
	// We only increment once when threshold is reached
	metricEmitted bool
}

// NewThrashingDetector creates a new thrashing detector
func NewThrashingDetector() *ThrashingDetector {
	return &ThrashingDetector{
		states: make(map[string]*thrashingState),
	}
}

// RecordThrottle records that a resource was throttled by the token bucket.
// Returns true if the resource should be paused (threshold reached).
func (td *ThrashingDetector) RecordThrottle(key string) bool {
	td.mu.Lock()
	defer td.mu.Unlock()

	state, exists := td.states[key]
	if !exists {
		// First throttle for this resource
		state = &thrashingState{
			consecutiveThrottles: 1,
			metricEmitted:        false,
		}
		td.states[key] = state

		// Check if we've already hit threshold (shouldn't happen on first throttle)
		return state.consecutiveThrottles >= ThrashingThreshold
	}

	// Increment throttle count
	state.consecutiveThrottles++

	// Check if we've reached the threshold
	return state.consecutiveThrottles >= ThrashingThreshold
}

// RecordSuccess records that a resource was successfully reconciled.
// This resets the thrashing state for that resource.
func (td *ThrashingDetector) RecordSuccess(key string) {
	td.mu.Lock()
	defer td.mu.Unlock()

	// Clear thrashing state - edit war resolved!
	delete(td.states, key)
}

// ShouldEmitMetric checks if we should emit the thrashing metric for this resource.
// Returns true only once when threshold is first reached.
func (td *ThrashingDetector) ShouldEmitMetric(key string) bool {
	td.mu.Lock()
	defer td.mu.Unlock()

	state, exists := td.states[key]
	if !exists {
		return false
	}

	// Only emit if we've reached threshold and haven't emitted yet
	if state.consecutiveThrottles >= ThrashingThreshold && !state.metricEmitted {
		state.metricEmitted = true
		return true
	}

	return false
}

// GetAttempts returns the number of consecutive throttle attempts for a resource
func (td *ThrashingDetector) GetAttempts(key string) int {
	td.mu.RLock()
	defer td.mu.RUnlock()

	state, exists := td.states[key]
	if !exists {
		return 0
	}
	return state.consecutiveThrottles
}

// Reset clears the thrashing state for a specific resource
// Useful for testing or manual intervention
func (td *ThrashingDetector) Reset(key string) {
	td.mu.Lock()
	defer td.mu.Unlock()
	delete(td.states, key)
}

// ResetAll clears all thrashing states
// Useful for testing
func (td *ThrashingDetector) ResetAll() {
	td.mu.Lock()
	defer td.mu.Unlock()
	td.states = make(map[string]*thrashingState)
}
