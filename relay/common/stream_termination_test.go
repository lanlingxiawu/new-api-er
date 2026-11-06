package common

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"

	hostcommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type terminationTimeoutSource string

func (s terminationTimeoutSource) RelayTimeoutKind() string { return string(s) }

func TestStreamTerminationTypedErrors(t *testing.T) {
	for _, tc := range []struct {
		name, class string
		err         error
	}{
		{"canceled", "canceled", context.Canceled},
		{"deadline", "deadline", context.DeadlineExceeded},
		{"network_deadline", "deadline", os.ErrDeadlineExceeded},
		{"eof", "eof", io.EOF},
		{"unexpected_eof", "unexpected_eof", io.ErrUnexpectedEOF},
		{"reset", "reset", fmt.Errorf("private prefix: %w", syscall.ECONNRESET)},
		{"pipe", "broken_pipe", syscall.EPIPE},
		{"short", "short_write", io.ErrShortWrite},
		{"closed", "closed", net.ErrClosed},
		{"closed_pipe", "closed", io.ErrClosedPipe},
		{"arbitrary_text", "other", errors.New("secret connection reset token=private")},
		{"ws_close", "closed", &websocket.CloseError{Code: 1006, Text: "private close reason"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewStreamTerminationRecorder(context.Background(), nil, 0, 0)
			r.Observe("downstream", "write", tc.err)
			d := r.Snapshot()
			require.Equal(t, tc.class, d.FirstObserved.ErrorClass)
			require.Equal(t, "downstream", d.FirstObserved.Side)
			require.Equal(t, "write", d.FirstObserved.Operation)
			require.GreaterOrEqual(t, d.FirstObserved.AfterMS, int64(0))
			raw, err := hostcommon.Marshal(d)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "private")
			require.NotContains(t, string(raw), "secret")
			if tc.name == "ws_close" {
				require.Equal(t, 1006, d.FirstObserved.WSCloseCode)
			}
			before := *d.FirstObserved
			r.Observe("gateway", "panic", errors.New("later"))
			require.Equal(t, before, *r.Snapshot().FirstObserved)
			d.FirstObserved.Side = "mutated"
			require.Equal(t, before, *r.Snapshot().FirstObserved)
		})
	}
}

func TestStreamTerminationContextAndCleanup(t *testing.T) {
	for _, kind := range []string{"", "response", "total", "idle", "response_timeout", "total_timeout", "idle_timeout"} {
		t.Run("context_"+kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			r := NewStreamTerminationRecorder(ctx, terminationTimeoutSource(kind), 12, 34)
			cancel()
			if strings.HasPrefix(kind, "idle") {
				r.ObserveTimeout(kind, 56)
			} else {
				r.ObserveUpstreamRead(io.ErrUnexpectedEOF)
			}
			d := r.Snapshot()
			side, op, class, seconds := "gateway", "timeout", "deadline", 12
			if kind == "" {
				side, op, class, seconds = "downstream", "request_cancel", "canceled", 0
			} else if strings.HasPrefix(kind, "total") {
				seconds = 34
			} else if strings.HasPrefix(kind, "idle") {
				seconds = 56
			}
			require.Equal(t, side, d.FirstObserved.Side)
			require.Equal(t, op, d.FirstObserved.Operation)
			require.Equal(t, class, d.FirstObserved.ErrorClass)
			require.Equal(t, seconds, d.FirstObserved.TimeoutSeconds)
			if kind != "" {
				require.Equal(t, strings.TrimSuffix(kind, "_timeout")+"_timeout", d.FirstObserved.TimeoutKind)
			}
		})
	}
	for _, err := range []error{io.EOF, io.ErrUnexpectedEOF, net.ErrClosed} {
		t.Run("cleanup_"+err.Error(), func(t *testing.T) {
			r := NewStreamTerminationRecorder(context.Background(), nil, 0, 0)
			r.ObserveUpstreamClose("private arbitrary reason")
			r.ObserveUpstreamRead(err)
			r.Observe("upstream", "read", err)
			d := r.Snapshot()
			require.Nil(t, d.FirstObserved)
			require.True(t, d.UpstreamReadAfterClose)
			require.Equal(t, "cleanup", d.UpstreamClose.ErrorClass)
			require.Equal(t, "gateway", d.UpstreamClose.Side)
		})
	}
	for _, completed := range []bool{false, true} {
		t.Run(fmt.Sprintf("eof_complete_%t", completed), func(t *testing.T) {
			r := NewStreamTerminationRecorder(context.Background(), nil, 0, 0)
			s := NewStreamSession(types.RelayFormatOpenAI)
			s.Termination = r
			r.ObserveUpstreamRead(io.EOF)
			require.Nil(t, r.Snapshot().FirstObserved, "raw EOF does not imply protocol failure")
			if completed {
				s.Complete()
			}
			s.EndRead(io.EOF)
			require.Equal(t, completed, r.Snapshot().FirstObserved == nil)
			require.Equal(t, completed, s.Snapshot().Complete)
		})
	}
}

func TestStreamTerminationNormalWebSocketClose(t *testing.T) {
	for _, code := range []int{1000, 1001} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			r := NewStreamTerminationRecorder(context.Background(), nil, 0, 0)
			err := &websocket.CloseError{Code: code, Text: "private close text"}
			r.ObserveUpstreamRead(err)
			require.Nil(t, r.Snapshot().FirstObserved)
			require.Equal(t, code, r.Snapshot().UpstreamRead.WSCloseCode)
			r.Observe("upstream", "read", err) // The owner may determine an interrupted turn.
			require.NotNil(t, r.Snapshot().FirstObserved)
		})
	}
}

func TestStreamTerminationConcurrentAndNil(t *testing.T) {
	r := NewStreamTerminationRecorder(context.Background(), nil, 0, 0)
	r.Observe("downstream", "flush", io.ErrClosedPipe)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.ObserveUpstreamClose("cleanup")
			r.ObserveUpstreamRead(io.ErrUnexpectedEOF)
			r.ObserveContext()
			_ = r.Snapshot()
		}()
	}
	wg.Wait()
	require.Equal(t, "flush", r.Snapshot().FirstObserved.Operation)
	var absent *StreamTerminationRecorder
	absent.Observe("upstream", "read", io.EOF)
	absent.ObserveContext()
	absent.ObserveTimeout("idle", 1)
	absent.ObserveUpstreamRead(io.EOF)
	absent.ObserveUpstreamClose("cleanup")
	require.Nil(t, absent.Snapshot())
}

type terminationBody struct {
	closed int
	err    error
}

func (b *terminationBody) Read(p []byte) (int, error) { return copy(p, "bytes"), b.err }
func (b *terminationBody) Close() error               { b.closed++; return nil }

func TestStreamTerminationCapturePreservesIO(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			capture := NewStreamResponseCapture(1)
			if enabled {
				capture.Termination = NewStreamTerminationRecorder(context.Background(), nil, 0, 0)
			}
			source := &terminationBody{err: io.ErrUnexpectedEOF}
			resp := &http.Response{StatusCode: 200, Body: source}
			capture.Observe(resp)
			buf := make([]byte, 5)
			n, err := resp.Body.Read(buf)
			require.Equal(t, 5, n)
			require.Equal(t, "bytes", string(buf))
			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			require.NoError(t, resp.Body.Close())
			require.NoError(t, resp.Body.Close())
			require.Equal(t, 1, source.closed)
			d := capture.Snapshot()
			require.Equal(t, enabled, d.Termination != nil)
			if enabled {
				require.Equal(t, "unexpected_eof", d.Termination.FirstObserved.ErrorClass)
				require.NotNil(t, d.Termination.UpstreamClose)
			}
		})
	}
}

func TestStreamTerminationWriterStages(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		for _, stage := range []string{"write", "short", "flush", "panic"} {
			t.Run(fmt.Sprintf("%s/disabled_%t", stage, disabled), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				s := NewStreamSession(types.RelayFormatOpenAI)
				s.Termination = NewStreamTerminationRecorder(context.Background(), nil, 0, 0)
				capture := NewStreamResponseCapture(1)
				capture.Termination = s.Termination
				var base gin.ResponseWriter = c.Writer
				switch stage {
				case "write":
					base = downstreamPartialWriter{base, 1, io.ErrClosedPipe}
				case "short":
					base = streamShortWriter{base, false}
				case "flush":
					base = streamFlushFailure{base}
				case "panic":
					base = streamShortWriter{base, true}
				}
				w := NewStreamWriter(NewDownstreamCaptureWriter(base, capture), s, nil)
				w.Header().Set("Content-Type", "text/event-stream")
				if disabled {
					s.Disable()
				}
				write := func() { _, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")) }
				if disabled && stage == "panic" {
					require.Panics(t, write)
				} else {
					write()
				}
				if stage == "flush" {
					require.Error(t, w.FlushError())
				}
				d := capture.Snapshot().Termination
				require.NotNil(t, d)
				require.NotNil(t, d.FirstObserved)
				op := stage
				if stage == "short" {
					op = "write"
				}
				require.Equal(t, op, d.FirstObserved.Operation)
				if stage == "panic" {
					require.Equal(t, "gateway", d.FirstObserved.Side)
				}
				require.False(t, s.Snapshot().Effective)
			})
		}
	}
}

func TestStreamTerminationProtocolClassification(t *testing.T) {
	for _, reason := range []string{"upstream_error", "upstream_protocol_error", "upstream_json_error", "response_conversion_error"} {
		t.Run(reason, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAI)
			s.Termination = NewStreamTerminationRecorder(context.Background(), nil, 0, 0)
			s.Fail(StreamEndReason(reason), errors.New("private cause"))
			d := s.Termination.Snapshot()
			side, class := "upstream", "protocol"
			if reason == "upstream_error" {
				class = "provider_error"
			}
			if strings.HasPrefix(reason, "response_") {
				side = "gateway"
			}
			require.Equal(t, side, d.FirstObserved.Side)
			require.Equal(t, class, d.FirstObserved.ErrorClass)
			require.Equal(t, StreamEndReason(reason), s.Snapshot().Reason)
		})
	}
}

type terminationClosingBody struct {
	reading, closed chan struct{}
}

func (b *terminationClosingBody) Read([]byte) (int, error) {
	close(b.reading)
	<-b.closed
	return 0, io.ErrClosedPipe
}
func (b *terminationClosingBody) Close() error { close(b.closed); return nil }

func TestStreamTerminationCloseUnblocksRead(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprint(canceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			capture := NewStreamResponseCapture(1)
			capture.Termination = NewStreamTerminationRecorder(ctx, nil, 0, 0)
			source := &terminationClosingBody{reading: make(chan struct{}), closed: make(chan struct{})}
			resp := &http.Response{StatusCode: 200, Body: source}
			capture.Observe(resp)
			readResult := make(chan error, 1)
			go func() { _, err := resp.Body.Read(make([]byte, 1)); readResult <- err }()
			<-source.reading
			if canceled {
				cancel()
			}
			require.NoError(t, resp.Body.Close())
			require.ErrorIs(t, <-readResult, io.ErrClosedPipe)
			d := capture.Snapshot().Termination
			require.True(t, d.UpstreamReadAfterClose)
			require.Equal(t, "closed", d.UpstreamRead.ErrorClass)
			if canceled {
				require.Equal(t, "downstream", d.FirstObserved.Side)
				require.Equal(t, "request_cancel", d.FirstObserved.Operation)
			} else {
				require.Nil(t, d.FirstObserved)
			}
		})
	}
}

func TestStreamTerminationOwnerDoesNotInventTransportRead(t *testing.T) {
	s := NewStreamSession(types.RelayFormatOpenAIRealtime)
	s.Termination = NewStreamTerminationRecorder(context.Background(), nil, 0, 0)
	s.Termination.Observe("upstream", "write", io.ErrClosedPipe)
	s.EndRead(io.ErrClosedPipe) // Existing Realtime uses this owner method after a send failure.
	d := s.Termination.Snapshot()
	require.Equal(t, "write", d.FirstObserved.Operation)
	require.Nil(t, d.UpstreamRead)
	require.Equal(t, StreamEndReason("upstream_read_error"), s.Snapshot().Reason, "existing protocol classification is unchanged")

	local := NewStreamSession(types.RelayFormatOpenAI)
	local.Termination = NewStreamTerminationRecorder(context.Background(), nil, 0, 0)
	local.FailRelay("upstream_protocol_error", errors.New("local conversion validation"))
	require.Equal(t, "gateway", local.Termination.Snapshot().FirstObserved.Side)
}

func TestStreamTerminationHTTPExchangeIsolation(t *testing.T) {
	for _, previousStatus := range []int{http.StatusServiceUnavailable, http.StatusOK} {
		for _, timing := range []string{"previous_closed", "previous_late_close", "previous_late_read"} {
			t.Run(fmt.Sprintf("%d/%s", previousStatus, timing), func(t *testing.T) {
				r := NewStreamTerminationRecorder(context.Background(), nil, 0, 0)
				s := NewStreamSession(types.RelayFormatOpenAI)
				s.Termination, s.ResponseGate = r, &StreamResponseGate{}
				capture := NewStreamResponseCapture(1)
				capture.Termination, capture.ResponseGate = r, s.ResponseGate
				previous := &http.Response{StatusCode: previousStatus, Body: &terminationBody{err: io.EOF}}
				s.ObserveTransport(previous, nil)
				capture.Observe(previous)
				buf := make([]byte, 5)
				if timing == "previous_closed" {
					_, err := previous.Body.Read(buf)
					require.ErrorIs(t, err, io.EOF)
					require.NoError(t, previous.Body.Close())
				}
				current := &http.Response{StatusCode: http.StatusOK, Body: &terminationBody{err: io.ErrUnexpectedEOF}}
				s.ObserveTransport(current, nil)
				capture.Observe(current)
				if timing == "previous_late_close" {
					require.NoError(t, previous.Body.Close())
				} else if timing == "previous_late_read" {
					_, err := previous.Body.Read(buf)
					require.ErrorIs(t, err, io.EOF)
				}
				_, err := current.Body.Read(buf)
				require.ErrorIs(t, err, io.ErrUnexpectedEOF)
				s.EndRead(err)
				d := capture.Snapshot().Termination
				wantIndex := uint64(1)
				if previousStatus == http.StatusOK {
					wantIndex = 2
				}
				require.Equal(t, wantIndex, d.CurrentResponse)
				require.NotNil(t, d.FirstObserved, "a previous exchange's cleanup cannot suppress the current failure")
				require.Equal(t, wantIndex, d.FirstObserved.ResponseIndex)
				require.Equal(t, "unexpected_eof", d.FirstObserved.ErrorClass)
				require.Equal(t, "unexpected_eof", d.UpstreamRead.ErrorClass)
				require.False(t, d.UpstreamReadAfterClose)
				require.Nil(t, d.UpstreamClose)
				before := r.Snapshot()
				capture.Observe(current) // Idempotent registration cannot reset the current evidence.
				require.Equal(t, before, r.Snapshot())
				require.NoError(t, current.Body.Close())
				require.NotNil(t, r.Snapshot().UpstreamClose)
			})
		}
	}
}

func TestStreamTerminationSessionCloseKeepsBodyExchange(t *testing.T) {
	r := NewStreamTerminationRecorder(context.Background(), nil, 0, 0)
	s := NewStreamSession(types.RelayFormatOpenAI)
	s.Termination = r
	capture := NewStreamResponseCapture(1)
	capture.Termination = r
	previous := &http.Response{StatusCode: 200, Body: &terminationBody{err: io.EOF}}
	capture.Observe(previous)
	s.ObserveHTTP(previous)
	current := &http.Response{StatusCode: 200, Body: &terminationBody{err: io.ErrUnexpectedEOF}}
	capture.Observe(current)
	// Deterministically reproduce the window after capture registered the new
	// response but before ObserveHTTP replaced the session's old closeBody.
	s.CloseUpstream()
	_, err := current.Body.Read(make([]byte, 5))
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	d := r.Snapshot()
	require.NotNil(t, d.FirstObserved)
	require.Equal(t, uint64(2), d.FirstObserved.ResponseIndex)
	require.Nil(t, d.UpstreamClose)
	require.False(t, d.UpstreamReadAfterClose)
}

func TestStreamTerminationRetryRetainsIndexedFirstObservation(t *testing.T) {
	r := NewStreamTerminationRecorder(context.Background(), nil, 0, 0)
	capture := NewStreamResponseCapture(1)
	capture.Termination = r
	previous := &http.Response{StatusCode: 200, Body: &terminationBody{err: io.ErrUnexpectedEOF}}
	capture.Observe(previous)
	_, err := previous.Body.Read(make([]byte, 5))
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	first := *r.Snapshot().FirstObserved
	require.Equal(t, uint64(1), first.ResponseIndex)
	current := &http.Response{StatusCode: 200, Body: &terminationBody{err: syscall.ECONNRESET}}
	capture.Observe(current)
	_, err = current.Body.Read(make([]byte, 5))
	require.ErrorIs(t, err, syscall.ECONNRESET)
	d := r.Snapshot()
	require.Equal(t, first, *d.FirstObserved)
	require.Equal(t, uint64(2), d.CurrentResponse)
	require.Equal(t, uint64(2), d.UpstreamRead.ResponseIndex)
	require.Equal(t, "reset", d.UpstreamRead.ErrorClass)
	require.Nil(t, d.UpstreamClose)
}

func TestStreamTerminationDuplicateCaptureAfterProtocolWrapper(t *testing.T) {
	r := NewStreamTerminationRecorder(context.Background(), nil, 0, 0)
	capture := NewStreamResponseCapture(1)
	capture.Termination = r
	s := NewStreamSession(types.RelayFormatOpenAI)
	s.Termination = r
	resp := &http.Response{StatusCode: 200, Body: &terminationBody{err: io.EOF}}
	capture.Observe(resp)
	s.ObserveHTTP(resp)
	wrapper := resp.Body
	r.ObserveTimeout("idle", 10)
	before := r.Snapshot()
	capture.Observe(resp)
	require.Same(t, wrapper, resp.Body)
	require.Equal(t, before, r.Snapshot())
	require.Equal(t, uint64(1), r.Snapshot().CurrentResponse)
	require.Empty(t, capture.Snapshot().PreviousResponses)
}
