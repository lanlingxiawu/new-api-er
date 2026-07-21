package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestInMemoryRateLimiter_WithinLimit(t *testing.T) {
	l := &InMemoryRateLimiter{}
	l.Init(0) // no background reaper

	// max 2 requests within a long window
	assert.True(t, l.Request("k", 2, 3600))  // creates queue
	assert.True(t, l.Request("k", 2, 3600))  // appends (len 1 < 2)
	assert.False(t, l.Request("k", 2, 3600)) // full, oldest still within window
}

func TestInMemoryRateLimiter_SlidesWhenOldestExpired(t *testing.T) {
	l := &InMemoryRateLimiter{}
	l.Init(0)

	// duration 0 => the oldest timestamp is always "expired", so the window
	// slides and the request is admitted even when the queue is full.
	assert.True(t, l.Request("k", 1, 0))
	assert.True(t, l.Request("k", 1, 0))
	assert.True(t, l.Request("k", 1, 0))
}

func TestInMemoryRateLimiter_SeparateKeys(t *testing.T) {
	l := &InMemoryRateLimiter{}
	l.Init(0)
	assert.True(t, l.Request("a", 1, 3600))
	assert.True(t, l.Request("b", 1, 3600)) // independent key
	assert.False(t, l.Request("a", 1, 3600))
}

func TestInMemoryRateLimiter_InitIdempotent(t *testing.T) {
	l := &InMemoryRateLimiter{}
	l.Init(time.Minute)
	// second Init must not reset the store or panic
	l.Request("k", 5, 3600)
	l.Init(time.Minute)
	assert.NotNil(t, l.store)
}
