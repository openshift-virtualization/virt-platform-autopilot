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
	"testing"
)

func TestThrashingDetector_FirstThrottle(t *testing.T) {
	td := NewThrashingDetector()
	key := "default/test-cm/ConfigMap"

	// First throttle should not trigger pause
	shouldPause := td.RecordThrottle(key)
	if shouldPause {
		t.Error("first throttle should not trigger pause")
	}

	attempts := td.GetAttempts(key)
	if attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", attempts)
	}
}

func TestThrashingDetector_ThresholdReached(t *testing.T) {
	td := NewThrashingDetector()
	key := "default/test-cm/ConfigMap"

	// First two throttles should not trigger pause
	for i := 0; i < 2; i++ {
		shouldPause := td.RecordThrottle(key)
		if shouldPause {
			t.Errorf("throttle %d should not trigger pause", i+1)
		}
	}

	// Third throttle should trigger pause (threshold = 3)
	shouldPause := td.RecordThrottle(key)
	if !shouldPause {
		t.Error("third throttle should trigger pause")
	}

	attempts := td.GetAttempts(key)
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestThrashingDetector_SuccessReset(t *testing.T) {
	td := NewThrashingDetector()
	key := "default/test-cm/ConfigMap"

	// Record some throttles
	td.RecordThrottle(key)
	td.RecordThrottle(key)

	attempts := td.GetAttempts(key)
	if attempts != 2 {
		t.Errorf("expected 2 attempts before reset, got %d", attempts)
	}

	// Record success - should reset state
	td.RecordSuccess(key)

	attempts = td.GetAttempts(key)
	if attempts != 0 {
		t.Errorf("expected 0 attempts after reset, got %d", attempts)
	}

	// Next throttle should start from 1 again
	shouldPause := td.RecordThrottle(key)
	if shouldPause {
		t.Error("first throttle after reset should not trigger pause")
	}

	attempts = td.GetAttempts(key)
	if attempts != 1 {
		t.Errorf("expected 1 attempt after reset and new throttle, got %d", attempts)
	}
}

func TestThrashingDetector_MetricEmissionOnce(t *testing.T) {
	td := NewThrashingDetector()
	key := "default/test-cm/ConfigMap"

	// Before threshold, should not emit metric
	td.RecordThrottle(key)
	if td.ShouldEmitMetric(key) {
		t.Error("should not emit metric before threshold")
	}

	td.RecordThrottle(key)
	if td.ShouldEmitMetric(key) {
		t.Error("should not emit metric before threshold")
	}

	// Reach threshold
	td.RecordThrottle(key)

	// First check after threshold should emit
	if !td.ShouldEmitMetric(key) {
		t.Error("should emit metric when threshold is reached")
	}

	// Second check should NOT emit (already emitted)
	if td.ShouldEmitMetric(key) {
		t.Error("should not emit metric twice")
	}

	// Even after more throttles, should not emit again
	td.RecordThrottle(key)
	if td.ShouldEmitMetric(key) {
		t.Error("should not emit metric after already emitted")
	}
}

func TestThrashingDetector_MetricEmissionAfterReset(t *testing.T) {
	td := NewThrashingDetector()
	key := "default/test-cm/ConfigMap"

	// Reach threshold and emit metric
	for i := 0; i < 3; i++ {
		td.RecordThrottle(key)
	}
	td.ShouldEmitMetric(key) // Consume the emission

	// Reset state
	td.RecordSuccess(key)

	// Reach threshold again
	for i := 0; i < 3; i++ {
		td.RecordThrottle(key)
	}

	// Should emit metric again (new thrashing episode)
	if !td.ShouldEmitMetric(key) {
		t.Error("should emit metric again after reset and new threshold")
	}
}

func TestThrashingDetector_MultipleResources(t *testing.T) {
	td := NewThrashingDetector()
	key1 := "default/cm1/ConfigMap"
	key2 := "default/cm2/ConfigMap"

	// Throttle key1 twice
	td.RecordThrottle(key1)
	td.RecordThrottle(key1)

	// Throttle key2 once
	td.RecordThrottle(key2)

	// Check independent tracking
	if td.GetAttempts(key1) != 2 {
		t.Errorf("key1 should have 2 attempts, got %d", td.GetAttempts(key1))
	}
	if td.GetAttempts(key2) != 1 {
		t.Errorf("key2 should have 1 attempt, got %d", td.GetAttempts(key2))
	}

	// Reset key1
	td.RecordSuccess(key1)

	// key1 should be reset, key2 unchanged
	if td.GetAttempts(key1) != 0 {
		t.Errorf("key1 should have 0 attempts after reset, got %d", td.GetAttempts(key1))
	}
	if td.GetAttempts(key2) != 1 {
		t.Errorf("key2 should still have 1 attempt, got %d", td.GetAttempts(key2))
	}
}

func TestThrashingDetector_ConcurrentAccess(t *testing.T) {
	td := NewThrashingDetector()
	key := "default/test-cm/ConfigMap"

	var wg sync.WaitGroup
	concurrency := 100

	// Concurrent throttle recording
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			td.RecordThrottle(key)
		}()
	}

	wg.Wait()

	// Should have recorded all attempts without race conditions
	attempts := td.GetAttempts(key)
	if attempts != concurrency {
		t.Errorf("expected %d attempts, got %d", concurrency, attempts)
	}

	// Should definitely be paused (well over threshold)
	shouldPause := td.RecordThrottle(key)
	if !shouldPause {
		t.Error("should be paused after many concurrent throttles")
	}
}

func TestThrashingDetector_Reset(t *testing.T) {
	td := NewThrashingDetector()
	key := "default/test-cm/ConfigMap"

	// Record throttles
	td.RecordThrottle(key)
	td.RecordThrottle(key)
	td.RecordThrottle(key)

	if td.GetAttempts(key) != 3 {
		t.Errorf("expected 3 attempts before reset, got %d", td.GetAttempts(key))
	}

	// Reset using Reset() method
	td.Reset(key)

	if td.GetAttempts(key) != 0 {
		t.Errorf("expected 0 attempts after reset, got %d", td.GetAttempts(key))
	}
}

func TestThrashingDetector_ResetAll(t *testing.T) {
	td := NewThrashingDetector()

	// Create multiple resource states
	keys := []string{
		"default/cm1/ConfigMap",
		"default/cm2/ConfigMap",
		"default/cm3/ConfigMap",
	}

	for _, key := range keys {
		td.RecordThrottle(key)
		td.RecordThrottle(key)
	}

	// Verify all have state
	for _, key := range keys {
		if td.GetAttempts(key) != 2 {
			t.Errorf("key %s should have 2 attempts", key)
		}
	}

	// Reset all
	td.ResetAll()

	// Verify all are cleared
	for _, key := range keys {
		if td.GetAttempts(key) != 0 {
			t.Errorf("key %s should have 0 attempts after ResetAll", key)
		}
	}
}

func TestThrashingDetector_PauseStaysActive(t *testing.T) {
	td := NewThrashingDetector()
	key := "default/test-cm/ConfigMap"

	// Reach threshold
	for i := 0; i < 3; i++ {
		td.RecordThrottle(key)
	}

	// Should pause
	if !td.RecordThrottle(key) {
		t.Error("should pause at threshold")
	}

	// Should continue to pause on subsequent throttles
	if !td.RecordThrottle(key) {
		t.Error("should continue to pause after threshold")
	}

	if !td.RecordThrottle(key) {
		t.Error("should continue to pause after threshold")
	}
}

func TestThrashingDetector_NoStateForUnknownResource(t *testing.T) {
	td := NewThrashingDetector()
	unknownKey := "default/unknown/ConfigMap"

	// Should return 0 for unknown resource
	if td.GetAttempts(unknownKey) != 0 {
		t.Errorf("expected 0 attempts for unknown resource, got %d", td.GetAttempts(unknownKey))
	}

	// Should not emit metric for unknown resource
	if td.ShouldEmitMetric(unknownKey) {
		t.Error("should not emit metric for unknown resource")
	}

	// Reset of unknown resource should not panic
	td.Reset(unknownKey) // Should be no-op

	// RecordSuccess of unknown resource should not panic
	td.RecordSuccess(unknownKey) // Should be no-op
}
