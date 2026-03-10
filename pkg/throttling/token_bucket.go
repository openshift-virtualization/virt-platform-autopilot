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
	// DefaultCapacity is the default token bucket capacity (burst of updates allowed)
	DefaultCapacity = 10

	// DefaultWindow is the default time window for token refill
	// Tokens accumulate continuously at rate = capacity/window (1 token per 6s with defaults)
	DefaultWindow = 1 * time.Minute

	// DefaultTTL is the time after which unused bucket entries are cleaned up
	// This prevents memory leaks from deleted resources
	DefaultTTL = 1 * time.Hour
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
	tokens       int
	lastFill     time.Time
	lastAccessed time.Time // Track when bucket was last used for TTL cleanup
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

	now := time.Now()
	b, exists := tb.buckets[key]
	if !exists {
		// First request for this key - create bucket with full capacity
		tb.buckets[key] = &bucket{
			tokens:       tb.capacity - 1, // Consume one token
			lastFill:     now,
			lastAccessed: now,
		}
		return true
	}

	// Update last accessed time
	b.lastAccessed = now

	// Rate-based refill: tokens accumulate continuously at rate = capacity/window.
	// This avoids the harsh "all-or-nothing" reset of a fixed window: instead of
	// blocking for up to a full minute after exhaustion, tokens trickle back
	// every (window/capacity) seconds (e.g. every 6s with defaults).
	tokenDuration := tb.window / time.Duration(tb.capacity)
	if tokenDuration > 0 {
		elapsed := now.Sub(b.lastFill)
		tokensToAdd := int(elapsed / tokenDuration)
		if tokensToAdd > 0 {
			b.tokens += tokensToAdd
			if b.tokens > tb.capacity {
				b.tokens = tb.capacity
			}
			// Advance lastFill only by the time that produced whole tokens,
			// so fractional time carries over to the next call.
			b.lastFill = b.lastFill.Add(time.Duration(tokensToAdd) * tokenDuration)
		}
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

// GetTokens returns the current token count for a key (for testing/debugging).
// Reflects tokens that have accumulated since the last Allow call.
func (tb *TokenBucket) GetTokens(key string) int {
	tb.mu.RLock()
	defer tb.mu.RUnlock()

	b, exists := tb.buckets[key]
	if !exists {
		return tb.capacity
	}

	// Include tokens accumulated since the last Allow call
	tokenDuration := tb.window / time.Duration(tb.capacity)
	tokens := b.tokens
	if tokenDuration > 0 {
		elapsed := time.Since(b.lastFill)
		tokens += int(elapsed / tokenDuration)
		if tokens > tb.capacity {
			tokens = tb.capacity
		}
	}
	return tokens
}

// CleanupStale removes bucket entries that haven't been accessed for longer than ttl
// This prevents memory leaks from deleted resources
// Returns the number of entries removed
func (tb *TokenBucket) CleanupStale(ttl time.Duration) int {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	removed := 0

	for key, b := range tb.buckets {
		if now.Sub(b.lastAccessed) > ttl {
			delete(tb.buckets, key)
			removed++
		}
	}

	return removed
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
