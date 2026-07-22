package controller

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/service"
)

// drainLimit must stay in sync with service.drainResponseBodyLimit (unexported).
// If that constant changes, update this literal.
const drainLimit = 512 * 1024

// countingReadCloser records bytes read and Close() invocations so tests can
// assert the drain/close contract precisely.
type countingReadCloser struct {
	data       []byte
	pos        int
	closeCount int
}

func (c *countingReadCloser) Read(p []byte) (int, error) {
	if c.pos >= len(c.data) {
		return 0, io.EOF
	}
	n := copy(p, c.data[c.pos:])
	c.pos += n
	return n, nil
}

func (c *countingReadCloser) Close() error {
	c.closeCount++
	return nil
}

func TestDrainAndCloseResponseBody_NilSafe(t *testing.T) {
	// Must not panic on nil response or nil body.
	service.DrainAndCloseResponseBody(nil)
	service.DrainAndCloseResponseBody(&http.Response{Body: nil})
}

func TestDrainAndCloseResponseBody_SmallBodyFullyDrainedAndClosed(t *testing.T) {
	body := &countingReadCloser{data: []byte(strings.Repeat("x", 1000))}
	resp := &http.Response{Body: body}

	service.DrainAndCloseResponseBody(resp)

	if body.closeCount != 1 {
		t.Fatalf("expected body closed exactly once, got %d", body.closeCount)
	}
	if body.pos != 1000 {
		t.Fatalf("expected full drain of 1000 bytes, drained %d", body.pos)
	}
}

func TestDrainAndCloseResponseBody_LargeBodyDrainCappedAtLimit(t *testing.T) {
	size := drainLimit + 256*1024 // exceeds the cap
	body := &countingReadCloser{data: make([]byte, size)}
	resp := &http.Response{Body: body}

	service.DrainAndCloseResponseBody(resp)

	if body.closeCount != 1 {
		t.Fatalf("expected body closed exactly once, got %d", body.closeCount)
	}
	if body.pos != drainLimit {
		t.Fatalf("expected drain capped at %d bytes, drained %d", drainLimit, body.pos)
	}
	if body.pos >= size {
		t.Fatalf("drain must not read the whole oversized body (read %d of %d)", body.pos, size)
	}
}
