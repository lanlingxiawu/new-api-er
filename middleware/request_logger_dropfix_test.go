package middleware

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
)

// Regression for D1/F26: the async request-log dispatch MUST be bounded and DROP
// under overload rather than block the relay goroutine or spawn unbounded
// goroutines (which previously exploded to 171k goroutines / 1.5 GB heap @
// c=500). Deterministically occupy the in-flight semaphore, then assert
// enqueueRequestLog returns immediately (does not block) and counts the drop.
func TestEnqueueRequestLog_DropsWhenInflightFull(t *testing.T) {
	orig := requestLogInflight
	requestLogInflight = make(chan struct{}, 1)
	requestLogInflight <- struct{}{} // occupy the only slot -> next enqueue must drop
	requestLogDropped.Store(0)
	t.Cleanup(func() {
		requestLogInflight = orig
		requestLogDropped.Store(0)
	})

	done := make(chan struct{})
	go func() {
		enqueueRequestLog(&model.RequestLog{}) // must hit the default (drop) branch
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("enqueueRequestLog blocked when in-flight cap was full — it must drop, never block the relay goroutine")
	}
	assert.Equal(t, int64(1), requestLogDropped.Load(), "a full in-flight cap must drop the log and increment the counter")
	assert.Equal(t, 1, len(requestLogInflight), "the occupied slot must be untouched by a dropped enqueue")
}

func TestRequestLogMaxInflight_ClampsToAtLeastOne(t *testing.T) {
	// Default is 1000; the helper must never return < 1 even if misconfigured.
	assert.GreaterOrEqual(t, requestLogMaxInflight(), 1)
	t.Setenv("REQUEST_LOG_MAX_INFLIGHT", "0")
	assert.Equal(t, 1, requestLogMaxInflight(), "0/negative must clamp to 1")
	t.Setenv("REQUEST_LOG_MAX_INFLIGHT", "2500")
	assert.Equal(t, 2500, requestLogMaxInflight())
}
