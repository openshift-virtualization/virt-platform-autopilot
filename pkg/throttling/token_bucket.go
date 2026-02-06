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
	"fmt"
	"sync"
	"time"
)

const (
	// DefaultCapacity is the default token bucket capacity (number of updates allowed)
	DefaultCapacity = 5

	// DefaultWindow is the default time window for token refill
	DefaultWindow = 1 * time.Minute
)

// TokenBucket implements a token bucket for rate limiting
type TokenBucket struct {
	capacity int
	window   time.Duration
	buckets  map[string]*bucket
	mu       sync.RWMutex
}

// bucket represents a single token bucket for a resource
type bucket struct {
	tokens   int
	lastFill time.Time
}

// NewTokenBucket creates a new token bucket with default settings
func NewTokenBucket() *TokenBucket {
	return NewTokenBucketWithSettings(DefaultCapacity, DefaultWindow)
}

// NewTokenBucketWithSettings creates a token bucket with custom settings
func NewTokenBucketWithSettings(capacity int, window time.Duration) *TokenBucket {
	return &TokenBucket{
		capacity: capacity,
		window:   window,
		buckets:  make(map[string]*bucket),
	}
}

// Allow checks if an operation is allowed for the given key
// Returns true if operation is allowed, false if throttled
func (tb *TokenBucket) Allow(key string) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	b, exists := tb.buckets[key]
	if !exists {
		// First request for this key - create bucket with full capacity
		tb.buckets[key] = &bucket{
			tokens:   tb.capacity - 1, // Consume one token
			lastFill: time.Now(),
		}
		return true
	}

	// Check if window has expired and refill bucket
	now := time.Now()
	if now.Sub(b.lastFill) >= tb.window {
		b.tokens = tb.capacity
		b.lastFill = now
	}

	// Check if tokens available
	if b.tokens <= 0 {
		return false // Throttled
	}

	// Consume a token
	b.tokens--
	return true
}

// Record records an update for the given key
// This is an alias for Allow for semantic clarity
func (tb *TokenBucket) Record(key string) error {
	if !tb.Allow(key) {
		return &ThrottledError{
			Key:      key,
			Capacity: tb.capacity,
			Window:   tb.window,
		}
	}
	return nil
}

// Reset resets the bucket for a given key
func (tb *TokenBucket) Reset(key string) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	delete(tb.buckets, key)
}

// ResetAll clears all buckets
func (tb *TokenBucket) ResetAll() {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.buckets = make(map[string]*bucket)
}

// GetTokens returns the current token count for a key (for testing/debugging)
func (tb *TokenBucket) GetTokens(key string) int {
	tb.mu.RLock()
	defer tb.mu.RUnlock()

	b, exists := tb.buckets[key]
	if !exists {
		return tb.capacity
	}

	// Check if window expired
	if time.Since(b.lastFill) >= tb.window {
		return tb.capacity
	}

	return b.tokens
}

// ThrottledError is returned when an operation is throttled
type ThrottledError struct {
	Key      string
	Capacity int
	Window   time.Duration
}

func (e *ThrottledError) Error() string {
	return fmt.Sprintf("throttled: too many updates for %s (%d updates per %s)",
		e.Key, e.Capacity, e.Window)
}

// IsThrottled checks if an error is a ThrottledError
func IsThrottled(err error) bool {
	_, ok := err.(*ThrottledError)
	return ok
}

// MakeResourceKey creates a unique key for a Kubernetes resource
func MakeResourceKey(namespace, name, kind string) string {
	if namespace == "" {
		return fmt.Sprintf("%s/%s", kind, name)
	}
	return fmt.Sprintf("%s/%s/%s", kind, namespace, name)
}
