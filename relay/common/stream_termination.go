package common

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// StreamTerminationEvent describes only a locally observed typed signal. It
// never identifies the actor behind a peer/proxy or stores arbitrary error text.
type StreamTerminationEvent struct {
	Side           string `json:"side"`
	Operation      string `json:"operation"`
	ErrorClass     string `json:"error_class"`
	AfterMS        int64  `json:"after_ms"`
	TimeoutKind    string `json:"timeout_kind,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	WSCloseCode    int    `json:"ws_close_code,omitempty"`
	ResponseIndex  uint64 `json:"response_index,omitempty"`
}

// StreamTerminationDiagnostic has fixed slots, not an expanding event history.
type StreamTerminationDiagnostic struct {
	CurrentResponse        uint64                  `json:"current_response,omitempty"`
	FirstObserved          *StreamTerminationEvent `json:"first_observed,omitempty"`
	UpstreamRead           *StreamTerminationEvent `json:"upstream_read,omitempty"`
	UpstreamReadAfterClose bool                    `json:"upstream_read_after_close,omitempty"`
	UpstreamClose          *StreamTerminationEvent `json:"upstream_close,omitempty"`
}

// StreamTerminationRecorder is independent of protocol ownership and billing.
// Its short lock never spans an I/O call. A nil recorder is the disabled path.
type StreamTerminationRecorder struct {
	mu                                          sync.Mutex
	started                                     time.Time
	ctx                                         context.Context
	timeoutSource                               interface{ RelayTimeoutKind() string }
	responseSeconds, totalSeconds               int
	first, read, close                          StreamTerminationEvent
	hasFirst, hasRead, hasClose, readAfterClose bool
	currentResponse                             uint64
}

func NewStreamTerminationRecorder(ctx context.Context, timeoutSource interface{ RelayTimeoutKind() string }, responseSeconds, totalSeconds int) *StreamTerminationRecorder {
	return &StreamTerminationRecorder{started: time.Now(), ctx: ctx, timeoutSource: timeoutSource, responseSeconds: responseSeconds, totalSeconds: totalSeconds}
}

// ObserveContext records cancellation only when observed by an existing owner;
// it installs no watcher. Owned gateway timeouts take precedence over generic
// canceled contexts, and all other cancellations retain actor uncertainty.
func (r *StreamTerminationRecorder) ObserveContext() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observeContextLocked()
}

// observeContextLocked is shared by exchange-bound operations, so checking an
// old response's generation and observing cancellation remain atomic.
func (r *StreamTerminationRecorder) observeContextLocked() {
	if r.hasFirst || r.ctx == nil || r.ctx.Err() == nil {
		return
	}
	if r.timeoutSource != nil {
		kind, seconds := "", 0
		switch r.timeoutSource.RelayTimeoutKind() {
		case "response", "response_timeout":
			kind, seconds = "response_timeout", r.responseSeconds
		case "total", "total_timeout":
			kind, seconds = "total_timeout", r.totalSeconds
		}
		if kind != "" {
			r.first = r.event("gateway", "timeout", context.DeadlineExceeded)
			r.first.TimeoutKind, r.first.TimeoutSeconds = kind, max(0, seconds)
			r.hasFirst = true
			return
		}
	}
	r.first = r.event("downstream", "request_cancel", r.ctx.Err())
	r.hasFirst = true
}

// ObserveTimeout records the timer actually owned by the gateway. Callers of
// idle timers invoke it before their existing cancel/close, without a new timer.
func (r *StreamTerminationRecorder) ObserveTimeout(kind string, seconds int) {
	if r == nil {
		return
	}
	switch kind {
	case "response", "response_timeout":
		kind = "response_timeout"
	case "total", "total_timeout":
		kind = "total_timeout"
	case "idle", "idle_timeout":
		kind = "idle_timeout"
	default:
		kind = "unknown"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.hasFirst {
		r.first = r.event("gateway", "timeout", context.DeadlineExceeded)
		r.first.TimeoutKind, r.first.TimeoutSeconds = kind, max(0, seconds)
		r.hasFirst = true
	}
}

// Observe records an owning boundary's signal. An upstream read after cleanup
// is evidence of cleanup, not a new initiating failure. provider_error is a
// caller tag normalized into the protocol operation and provider_error class.
func (r *StreamTerminationRecorder) Observe(side, operation string, err error) {
	if r == nil || err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observeContextLocked()
	if r.hasFirst || (side == "upstream" && operation == "read" && r.hasClose) {
		return
	}
	r.first = r.event(side, operation, err)
	r.hasFirst = true
}

// ObserveUpstreamRead records raw terminal transport evidence. Ordinary EOF
// becomes a failure only when the protocol owner separately calls Observe.
func (r *StreamTerminationRecorder) ObserveUpstreamRead(err error) {
	r.observeUpstreamRead(err, 0, false)
}

// Bound body wrappers ignore late operations from an older SDK exchange.
func (r *StreamTerminationRecorder) observeUpstreamRead(err error, responseIndex uint64, bound bool) {
	if r == nil || err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if bound && responseIndex != r.currentResponse {
		return
	}
	r.observeContextLocked()
	if !r.hasRead {
		r.read = r.event("upstream", "read", err)
		r.hasRead, r.readAfterClose = true, r.hasClose
	}
	var ws *websocket.CloseError
	normalClose := errors.As(err, &ws) && (ws.Code == websocket.CloseNormalClosure || ws.Code == websocket.CloseGoingAway)
	if !r.hasFirst && !r.hasClose && !errors.Is(err, io.EOF) && !normalClose {
		r.first = r.event("upstream", "read", err)
		r.hasFirst = true
	}
}

// ObserveUpstreamClose must run before the existing close. reason is accepted
// for call-site clarity but never serialized: cleanup is not a causal claim.
func (r *StreamTerminationRecorder) ObserveUpstreamClose(reason string) {
	r.observeUpstreamClose(0, false)
}

func (r *StreamTerminationRecorder) observeUpstreamClose(responseIndex uint64, bound bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if bound && responseIndex != r.currentResponse {
		return
	}
	r.observeContextLocked()
	if !r.hasClose {
		r.close = r.event("gateway", "close", nil)
		r.close.ErrorClass = "cleanup"
		r.hasClose = true
	}
}

// beginHTTPResponse is called only when capture installs a new body wrapper.
// Attempt-wide first evidence survives with its response index; transport and
// cleanup slots refer only to the current exchange. Memory remains fixed.
func (r *StreamTerminationRecorder) beginHTTPResponse() uint64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.currentResponse++
	r.read, r.close = StreamTerminationEvent{}, StreamTerminationEvent{}
	r.hasRead, r.hasClose, r.readAfterClose = false, false, false
	return r.currentResponse
}

// event normalizes trusted call-site tags and classifies wrapped typed errors.
// The caller owns the recorder lock; no err.Error string is retained or parsed.
func (r *StreamTerminationRecorder) event(side, operation string, err error) StreamTerminationEvent {
	switch side {
	case "upstream", "downstream", "gateway":
	default:
		side = "unknown"
	}
	e := StreamTerminationEvent{Side: side, Operation: operation, ErrorClass: "other", AfterMS: max(0, time.Since(r.started).Milliseconds()), ResponseIndex: r.currentResponse}
	switch operation {
	case "provider_error":
		e.Operation, e.ErrorClass = "protocol", "provider_error"
		return e
	case "protocol", "panic":
		e.ErrorClass = operation
		return e
	case "timeout":
		e.ErrorClass = "deadline"
		return e
	case "request_cancel", "read", "write", "flush", "ping", "close":
	default:
		e.Operation = "unknown"
	}
	var ws *websocket.CloseError
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		e.ErrorClass = "canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		e.ErrorClass = "deadline"
	case errors.Is(err, io.EOF):
		e.ErrorClass = "eof"
	case errors.Is(err, io.ErrUnexpectedEOF):
		e.ErrorClass = "unexpected_eof"
	case errors.Is(err, syscall.ECONNRESET):
		e.ErrorClass = "reset"
	case errors.Is(err, syscall.EPIPE):
		e.ErrorClass = "broken_pipe"
	case errors.Is(err, io.ErrShortWrite):
		e.ErrorClass = "short_write"
	case errors.Is(err, net.ErrClosed), errors.Is(err, io.ErrClosedPipe), errors.Is(err, os.ErrClosed):
		e.ErrorClass = "closed"
	case errors.As(err, &ws):
		e.ErrorClass, e.WSCloseCode = "closed", ws.Code
	case errors.As(err, &netErr) && netErr.Timeout():
		e.ErrorClass = "deadline"
	}
	return e
}

// Snapshot copies each occupied slot, so async serialization cannot observe
// future mutations. No evidence yields nil and keeps normal legacy JSON small.
func (r *StreamTerminationRecorder) Snapshot() *StreamTerminationDiagnostic {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.hasFirst && !r.hasRead && !r.hasClose {
		return nil
	}
	d := &StreamTerminationDiagnostic{CurrentResponse: r.currentResponse, UpstreamReadAfterClose: r.readAfterClose}
	if r.hasFirst {
		v := r.first
		d.FirstObserved = &v
	}
	if r.hasRead {
		v := r.read
		d.UpstreamRead = &v
	}
	if r.hasClose {
		v := r.close
		d.UpstreamClose = &v
	}
	return d
}
