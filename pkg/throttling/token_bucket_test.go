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
	"testing"
	"time"
)

func TestTokenBucket_Allow(t *testing.T) {
	t.Run("first request always allowed", func(t *testing.T) {
		tb := NewTokenBucket()
		if !tb.Allow("test-key") {
			t.Error("First request should always be allowed")
		}
	})

	t.Run("respects capacity limit", func(t *testing.T) {
		tb := NewTokenBucketWithSettings(3, 1*time.Minute)
		key := "test-resource"

		// First 3 requests should succeed
		for i := 0; i < 3; i++ {
			if !tb.Allow(key) {
				t.Errorf("Request %d should be allowed (capacity=3)", i+1)
			}
		}

		// 4th request should be throttled
		if tb.Allow(key) {
			t.Error("Request 4 should be throttled (capacity=3)")
		}
	})

	t.Run("refills after window expires", func(t *testing.T) {
		tb := NewTokenBucketWithSettings(2, 100*time.Millisecond)
		key := "test-resource"

		// Consume all tokens
		tb.Allow(key)
		tb.Allow(key)

		// Should be throttled now
		if tb.Allow(key) {
			t.Error("Should be throttled after consuming all tokens")
		}

		// Wait for window to expire
		time.Sleep(150 * time.Millisecond)

		// Should be allowed again after refill
		if !tb.Allow(key) {
			t.Error("Should be allowed after window expiration")
		}
	})

	t.Run("different keys have independent buckets", func(t *testing.T) {
		tb := NewTokenBucketWithSettings(1, 1*time.Minute)

		// Consume token for key1
		tb.Allow("key1")
		if tb.Allow("key1") {
			t.Error("key1 should be throttled")
		}

		// key2 should still be allowed
		if !tb.Allow("key2") {
			t.Error("key2 should have independent bucket")
		}
	})
}

func TestTokenBucket_Record(t *testing.T) {
	t.Run("returns nil when allowed", func(t *testing.T) {
		tb := NewTokenBucket()
		err := tb.Record("test-key")
		if err != nil {
			t.Errorf("Record() should return nil when allowed, got %v", err)
		}
	})

	t.Run("returns ThrottledError when exhausted", func(t *testing.T) {
		tb := NewTokenBucketWithSettings(1, 1*time.Minute)
		key := "test-resource"

		// Consume the only token
		err := tb.Record(key)
		if err != nil {
			t.Errorf("First Record() should succeed, got %v", err)
		}

		// Next should be throttled
		err = tb.Record(key)
		if err == nil {
			t.Error("Record() should return error when throttled")
		}

		if !IsThrottled(err) {
			t.Errorf("Error should be ThrottledError, got %T", err)
		}

		throttledErr, ok := err.(*ThrottledError)
		if !ok {
			t.Fatal("Expected *ThrottledError")
		}

		if throttledErr.Key != key {
			t.Errorf("Expected key=%s, got %s", key, throttledErr.Key)
		}
		if throttledErr.Capacity != 1 {
			t.Errorf("Expected capacity=1, got %d", throttledErr.Capacity)
		}
	})
}

func TestTokenBucket_Reset(t *testing.T) {
	t.Run("resets specific key", func(t *testing.T) {
		tb := NewTokenBucketWithSettings(1, 1*time.Minute)

		// Exhaust key1
		tb.Allow("key1")
		if tb.Allow("key1") {
			t.Error("key1 should be throttled")
		}

		// Reset key1
		tb.Reset("key1")

		// Should be allowed again
		if !tb.Allow("key1") {
			t.Error("key1 should be allowed after reset")
		}
	})

	t.Run("doesn't affect other keys", func(t *testing.T) {
		tb := NewTokenBucketWithSettings(1, 1*time.Minute)

		// Exhaust both keys
		tb.Allow("key1")
		tb.Allow("key2")

		// Reset only key1
		tb.Reset("key1")

		// key1 should be allowed
		if !tb.Allow("key1") {
			t.Error("key1 should be allowed after reset")
		}

		// key2 should still be throttled
		if tb.Allow("key2") {
			t.Error("key2 should still be throttled")
		}
	})
}

func TestTokenBucket_ResetAll(t *testing.T) {
	tb := NewTokenBucketWithSettings(1, 1*time.Minute)

	// Exhaust multiple keys
	tb.Allow("key1")
	tb.Allow("key2")
	tb.Allow("key3")

	// All should be throttled
	if tb.Allow("key1") || tb.Allow("key2") || tb.Allow("key3") {
		t.Error("All keys should be throttled")
	}

	// Reset all
	tb.ResetAll()

	// All should be allowed again
	if !tb.Allow("key1") || !tb.Allow("key2") || !tb.Allow("key3") {
		t.Error("All keys should be allowed after ResetAll")
	}
}

func TestTokenBucket_GetTokens(t *testing.T) {
	t.Run("returns full capacity for new key", func(t *testing.T) {
		tb := NewTokenBucketWithSettings(5, 1*time.Minute)
		tokens := tb.GetTokens("new-key")
		if tokens != 5 {
			t.Errorf("Expected 5 tokens for new key, got %d", tokens)
		}
	})

	t.Run("returns correct count after consumption", func(t *testing.T) {
		tb := NewTokenBucketWithSettings(5, 1*time.Minute)
		key := "test-key"

		// Consume 2 tokens
		tb.Allow(key)
		tb.Allow(key)

		tokens := tb.GetTokens(key)
		if tokens != 3 {
			t.Errorf("Expected 3 tokens remaining, got %d", tokens)
		}
	})

	t.Run("returns full capacity after window expiration", func(t *testing.T) {
		tb := NewTokenBucketWithSettings(5, 100*time.Millisecond)
		key := "test-key"

		// Consume some tokens
		tb.Allow(key)
		tb.Allow(key)

		// Check before expiration
		if tb.GetTokens(key) != 3 {
			t.Error("Should have 3 tokens before expiration")
		}

		// Wait for window to expire
		time.Sleep(150 * time.Millisecond)

		// Should return full capacity
		tokens := tb.GetTokens(key)
		if tokens != 5 {
			t.Errorf("Expected full capacity (5) after expiration, got %d", tokens)
		}
	})
}

func TestMakeResourceKey(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		objName   string
		kind      string
		expected  string
	}{
		{
			name:      "namespaced resource",
			namespace: "default",
			objName:   "my-configmap",
			kind:      "ConfigMap",
			expected:  "ConfigMap/default/my-configmap",
		},
		{
			name:      "cluster-scoped resource",
			namespace: "",
			objName:   "my-node",
			kind:      "Node",
			expected:  "Node/my-node",
		},
		{
			name:      "resource with dashes",
			namespace: "openshift-cnv",
			objName:   "kubevirt-hyperconverged",
			kind:      "HyperConverged",
			expected:  "HyperConverged/openshift-cnv/kubevirt-hyperconverged",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MakeResourceKey(tt.namespace, tt.objName, tt.kind)
			if result != tt.expected {
				t.Errorf("MakeResourceKey() = %s, expected %s", result, tt.expected)
			}
		})
	}
}

func TestIsThrottled(t *testing.T) {
	t.Run("identifies ThrottledError", func(t *testing.T) {
		err := &ThrottledError{
			Key:      "test",
			Capacity: 5,
			Window:   1 * time.Minute,
		}
		if !IsThrottled(err) {
			t.Error("IsThrottled should return true for ThrottledError")
		}
	})

	t.Run("rejects other errors", func(t *testing.T) {
		err := &testError{}
		if IsThrottled(err) {
			t.Error("IsThrottled should return false for non-ThrottledError")
		}
	})

	t.Run("handles nil", func(t *testing.T) {
		if IsThrottled(nil) {
			t.Error("IsThrottled should return false for nil")
		}
	})
}

// testError is a custom error type for testing
type testError struct{}

func (e *testError) Error() string {
	return "test error"
}

// Benchmark tests
func BenchmarkTokenBucket_Allow(b *testing.B) {
	tb := NewTokenBucket()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tb.Allow("benchmark-key")
		if i%100 == 0 {
			tb.Reset("benchmark-key") // Reset periodically to avoid throttling
		}
	}
}

func BenchmarkTokenBucket_ConcurrentAccess(b *testing.B) {
	tb := NewTokenBucket()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			tb.Allow("concurrent-key")
			if i%50 == 0 {
				tb.Reset("concurrent-key")
			}
			i++
		}
	})
}

func TestTokenBucket_CleanupStale(t *testing.T) {
	t.Run("removes entries older than TTL", func(t *testing.T) {
		tb := NewTokenBucket()

		// Create some entries
		tb.Allow("active-key")
		tb.Allow("stale-key")

		// Wait a bit
		time.Sleep(10 * time.Millisecond)

		// Access active-key again to update lastAccessed
		tb.Allow("active-key")

		// Cleanup with very short TTL should remove stale-key but keep active-key
		removed := tb.CleanupStale(5 * time.Millisecond)

		if removed != 1 {
			t.Errorf("Expected 1 entry removed, got %d", removed)
		}

		// Verify stale-key was removed (new access creates new bucket)
		tb.mu.RLock()
		_, staleExists := tb.buckets["stale-key"]
		_, activeExists := tb.buckets["active-key"]
		tb.mu.RUnlock()

		if staleExists {
			t.Error("stale-key should have been removed")
		}
		if !activeExists {
			t.Error("active-key should still exist")
		}
	})

	t.Run("removes nothing when all entries are fresh", func(t *testing.T) {
		tb := NewTokenBucket()

		tb.Allow("key1")
		tb.Allow("key2")
		tb.Allow("key3")

		// Cleanup with very long TTL should remove nothing
		removed := tb.CleanupStale(1 * time.Hour)

		if removed != 0 {
			t.Errorf("Expected 0 entries removed, got %d", removed)
		}
	})

	t.Run("removes all entries when all are stale", func(t *testing.T) {
		tb := NewTokenBucket()

		tb.Allow("key1")
		tb.Allow("key2")

		// Wait for entries to become stale
		time.Sleep(10 * time.Millisecond)

		// Cleanup should remove both
		removed := tb.CleanupStale(5 * time.Millisecond)

		if removed != 2 {
			t.Errorf("Expected 2 entries removed, got %d", removed)
		}

		tb.mu.RLock()
		count := len(tb.buckets)
		tb.mu.RUnlock()

		if count != 0 {
			t.Errorf("Expected 0 buckets remaining, got %d", count)
		}
	})

	t.Run("handles empty bucket map", func(t *testing.T) {
		tb := NewTokenBucket()

		removed := tb.CleanupStale(1 * time.Hour)

		if removed != 0 {
			t.Errorf("Expected 0 entries removed from empty bucket, got %d", removed)
		}
	})
}
