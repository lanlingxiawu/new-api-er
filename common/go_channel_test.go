package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSafeSendBool(t *testing.T) {
	// open channel with a receiver -> send succeeds, not closed
	ch := make(chan bool, 1)
	assert.False(t, SafeSendBool(ch, true))
	assert.True(t, <-ch)

	// closed channel -> recovers from panic, reports closed
	closed := make(chan bool)
	close(closed)
	assert.True(t, SafeSendBool(closed, true))
}

func TestSafeSendString(t *testing.T) {
	ch := make(chan string, 1)
	assert.False(t, SafeSendString(ch, "x"))
	assert.Equal(t, "x", <-ch)

	closed := make(chan string)
	close(closed)
	assert.True(t, SafeSendString(closed, "x"))
}

func TestSafeSendStringTimeout(t *testing.T) {
	// buffered channel accepts immediately -> returns true (sent)
	ch := make(chan string, 1)
	assert.True(t, SafeSendStringTimeout(ch, "x", 1))
	assert.Equal(t, "x", <-ch)

	// closed channel panics on send -> recover sets result false
	closed := make(chan string)
	close(closed)
	assert.False(t, SafeSendStringTimeout(closed, "x", 1))
}

func TestSafeSendStringTimeout_Expires(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 1s timeout path in -short mode")
	}
	// unbuffered channel with no receiver -> times out after 1s -> false
	ch := make(chan string)
	assert.False(t, SafeSendStringTimeout(ch, "x", 1))
}
