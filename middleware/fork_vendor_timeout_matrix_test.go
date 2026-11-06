package middleware

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func forkVendorTimeoutContext(t *testing.T, parent context.Context, responseTimeout, totalTimeout time.Duration, mode string) (*gin.Context, *httptest.ResponseRecorder, *relayTimeoutControl) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	ctx, control := newRelayTimeoutControl(parent, responseTimeout, totalTimeout, mode)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, control)
	common.SetContextKey(c, constant.ContextKeyIsStream, true)
	writer := &relayTimeoutResponseWriter{ResponseWriter: c.Writer}
	writer.state.Store(&relayTimeoutWriterState{control: control, isStream: true})
	c.Writer = writer
	t.Cleanup(control.Close)
	return c, recorder, control
}

func forkAssertVendorTimeoutTerminal(t *testing.T, c *gin.Context, recorder *httptest.ResponseRecorder, control *relayTimeoutControl, info *relaycommon.RelayInfo, usage *dto.Usage) {
	t.Helper()
	body := recorder.Body.String()
	require.Equal(t, 1, strings.Count(body, "event: error\n"), body)
	require.NotContains(t, body, "data: [DONE]")
	require.NotContains(t, body, "event: message_stop\n")
	require.NotContains(t, body, `"finish_reason":"stop"`)
	require.NotContains(t, body, `"finish_reason":"tool_calls"`)
	result := info.StreamResult
	require.Same(t, usage, service.FinalizeStreamUsage(c, info, usage))
	require.Same(t, result, info.StreamResult)
	require.Equal(t, body, recorder.Body.String(), "finalization must not emit a second terminal")
	n, err := c.Writer.Write([]byte("data: {\"late\":true}\n\n"))
	require.Zero(t, n)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	n, err = c.Writer.WriteString("data: {\"late\":true}\n\n")
	require.Zero(t, n)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	c.Writer.Flush()
	require.Equal(t, body, recorder.Body.String(), "late output must remain blocked")
	require.True(t, control.lateWriteRejected.Load())
}

// The fixture pads business events with more pings than the handlers' one-frame
// reader queues. A read after the padded prefix therefore happens after those
// business events were consumed, without racing expiry against their delivery.
type forkStalledVendorBody struct {
	io.ReadCloser
	remaining int
	stalled   chan struct{}
	once      sync.Once
}

func (body *forkStalledVendorBody) Read(p []byte) (int, error) {
	if body.remaining == 0 {
		body.once.Do(func() { close(body.stalled) })
	}
	n, err := body.ReadCloser.Read(p)
	body.remaining = max(0, body.remaining-n)
	return n, err
}

// Fixtures follow https://platform.claude.com/docs/en/build-with-claude/streaming.
// All requests go to loopback. Both native and converted handlers run against
// the real timeout controller and transport cancellation, not an EOF substitute.
func TestForkVendorStallTimeoutBillingMatrix(t *testing.T) {
	const ping = "event: ping\ndata: {\"type\":\"ping\"}\n\n"
	const text = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n"
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		for _, mode := range []string{service.RelayStreamResponseTimeoutModeFirstOutput, service.RelayStreamResponseTimeoutModeIdle} {
			for _, phase := range []string{"headers_only", "heartbeat_only", "usage_only", "zero_usage", "text"} {
				for _, billing := range []string{"refund", "input", "charge"} {
					t.Run(fmt.Sprintf("%s/%s/%s/%s", format, mode, phase, billing), func(t *testing.T) {
						prefix := ""
						confirmed := phase == "usage_only" || phase == "zero_usage" || phase == "text"
						if confirmed {
							input, output := 20, 2
							if phase == "zero_usage" {
								input, output = 0, 0
							}
							prefix = fmt.Sprintf("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_fixture\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":%d,\"output_tokens\":%d}}}\n\n", input, output)
						}
						if phase == "heartbeat_only" {
							prefix = ping
						}
						if phase == "text" {
							prefix += text
						}
						if prefix != "" {
							prefix += strings.Repeat(ping, 4)
						}
						upstreamDone := make(chan struct{})
						release := make(chan struct{})
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							defer close(upstreamDone)
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = io.WriteString(w, prefix)
							w.(http.Flusher).Flush()
							select {
							case <-r.Context().Done():
							case <-release:
							}
						}))
						t.Cleanup(server.Close)
						t.Cleanup(func() { close(release) })
						c, recorder, control := forkVendorTimeoutContext(t, context.Background(), time.Hour, time.Hour, mode)
						common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, billing)
						info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: format, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-sonnet-4-5"}}
						info.SetEstimatePromptTokens(11)
						service.BeginStreamAttempt(c, info)
						req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"model":"claude-sonnet-4-5","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"Say hello"}]}`))
						require.NoError(t, err)
						req = service.BindRelayRequestContext(c, req)
						resp, err := service.RelayHTTPClient(c, server.Client()).Do(req)
						require.NoError(t, err)
						body := &forkStalledVendorBody{ReadCloser: resp.Body, remaining: len(prefix), stalled: make(chan struct{})}
						resp.Body = body
						info.StreamSession.ObserveTransport(resp, nil)
						triggerDone := make(chan struct{})
						go func() {
							defer close(triggerDone)
							select {
							case <-body.stalled:
							case <-release:
								return
							}
							if mode == service.RelayStreamResponseTimeoutModeIdle {
								control.mu.Lock()
								control.responseDeadline = time.Now().Add(-time.Second)
								control.mu.Unlock()
								control.expireResponse()
							} else {
								control.expireTotal()
							}
						}()
						// A generous watchdog is only for a broken cancellation path;
						// the experiment normally completes without elapsed-time waits.
						watchdog := time.AfterFunc(5*time.Second, control.Close)
						defer watchdog.Stop()
						usage, apiErr := claude.ClaudeStreamHandler(c, resp, info)
						require.Nil(t, apiErr)
						usage = service.FinalizeStreamUsage(c, info, usage)
						require.NotNil(t, info.StreamResult)
						require.True(t, info.StreamResult.Failed)
						require.False(t, info.StreamResult.ClientGone)
						require.Equal(t, relaycommon.StreamEndReasonTimeout, info.StreamStatus.EndReason)
						require.Equal(t, phase == "text", info.StreamResult.EffectiveContent)
						require.False(t, info.StreamResult.Diagnostic.UsageFinal)
						wantInput, wantOutput := 0, 0
						if billing != "refund" || phase == "text" {
							wantInput = 11
							if confirmed {
								wantInput = 20
							}
							if confirmed && (billing == "charge" || phase == "text") {
								wantOutput = 2
							}
						}
						if phase == "zero_usage" {
							wantInput, wantOutput = 0, 0
						}
						require.Equal(t, wantInput, usage.PromptTokens)
						require.Equal(t, wantOutput, usage.CompletionTokens)
						require.Same(t, usage, service.FinalizeStreamUsage(c, info, usage))
						forkAssertVendorTimeoutTerminal(t, c, recorder, control, info, usage)
						wantTextCount := 0
						if phase == "text" {
							wantTextCount = 1
						}
						require.Equal(t, wantTextCount, strings.Count(recorder.Body.String(), "hello"), "business output must be delivered exactly once")
						select {
						case <-triggerDone:
						case <-time.After(time.Second):
							require.FailNow(t, "deadline trigger leaked")
						}
						select {
						case <-upstreamDone:
						case <-time.After(time.Second):
							require.FailNow(t, "timeout did not cancel the upstream HTTP exchange")
						}
					})
				}
			}
		}
	}
}

// Real wall-clock expiry covers both the transport waiting for headers and a
// successful HTTP response whose body never starts. No provider is contacted.
func TestForkVendorRealTimerBeforeOutput(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		for _, mode := range []string{service.RelayStreamResponseTimeoutModeFirstOutput, service.RelayStreamResponseTimeoutModeIdle} {
			for _, headers := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/headers=%t", format, mode, headers), func(t *testing.T) {
					upstreamDone := make(chan struct{})
					release := make(chan struct{})
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						defer close(upstreamDone)
						// Draining the request lets net/http detect a disconnected
						// HTTP/1 peer even before the response headers are written.
						_, _ = io.Copy(io.Discard, r.Body)
						if headers {
							w.Header().Set("Content-Type", "text/event-stream")
							w.(http.Flusher).Flush()
						}
						select {
						case <-r.Context().Done():
						case <-release:
						}
					}))
					t.Cleanup(server.Close)
					t.Cleanup(func() { close(release) })
					c, recorder, control := forkVendorTimeoutContext(t, context.Background(), 100*time.Millisecond, time.Hour, mode)
					watchdog := time.AfterFunc(2*time.Second, control.Close)
					defer watchdog.Stop()
					info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: format, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-sonnet-4-5"}}
					common.SetContextKey(c, constant.ContextKeyUserNonStreamTimeoutBilling, "refund")
					service.BeginStreamAttempt(c, info)
					req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"stream":true}`))
					require.NoError(t, err)
					req = service.BindRelayRequestContext(c, req)
					resp, err := service.RelayHTTPClient(c, server.Client()).Do(req)
					info.StreamSession.ObserveTransport(resp, err)
					if headers {
						require.NoError(t, err)
						usage, apiErr := claude.ClaudeStreamHandler(c, resp, info)
						require.Nil(t, apiErr)
						usage = service.FinalizeStreamUsage(c, info, usage)
						require.True(t, info.StreamResult.Failed)
						require.False(t, info.StreamResult.ClientGone)
						require.False(t, info.StreamResult.EffectiveContent)
						require.Equal(t, relaycommon.StreamEndReasonTimeout, info.StreamStatus.EndReason)
						require.Zero(t, usage.TotalTokens)
						forkAssertVendorTimeoutTerminal(t, c, recorder, control, info, usage)
					} else {
						require.Error(t, err)
						require.Nil(t, resp)
						require.False(t, info.StreamSession.Active(), "no accepted HTTP response must leave the stream gate closed")
						require.Nil(t, service.FinalizeStreamUsage(c, info, nil))
						require.Nil(t, info.StreamResult)
						require.Empty(t, recorder.Body.String(), "the outer controller owns the pre-stream HTTP error")
						n, writeErr := c.Writer.Write([]byte("late output"))
						require.Zero(t, n)
						require.ErrorIs(t, writeErr, context.DeadlineExceeded)
						require.Empty(t, recorder.Body.String())
					}
					require.True(t, service.IsRelayRequestTimeout(c))
					require.Equal(t, relayTimeoutKindResponse, control.ExpiredKind())
					require.ErrorIs(t, context.Cause(c.Request.Context()), errRelayResponseTimeout)
					apiErr := service.RelayContextError(c)
					require.NotNil(t, apiErr)
					require.Equal(t, http.StatusGatewayTimeout, apiErr.StatusCode)
					require.True(t, watchdog.Stop(), "normal cancellation must finish before the watchdog")
					select {
					case <-upstreamDone:
					case <-time.After(time.Second):
						require.FailNow(t, "real timer did not release the upstream HTTP handler")
					}
				})
			}
		}
	}
}

const forkVendorClaudeStartedText = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_fixture\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":20,\"output_tokens\":2}}}\n\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n"

const forkVendorClaudeCompleted = forkVendorClaudeStartedText +
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
	"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":2}}\n\n" +
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

func TestForkVendorCompleteBeforeTimer(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		for _, mode := range []string{service.RelayStreamResponseTimeoutModeFirstOutput, service.RelayStreamResponseTimeoutModeIdle} {
			t.Run(fmt.Sprintf("%s/%s", format, mode), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, forkVendorClaudeCompleted)
					w.(http.Flusher).Flush()
				}))
				t.Cleanup(server.Close)
				c, recorder, control := forkVendorTimeoutContext(t, context.Background(), time.Second, time.Second, mode)
				watchdog := time.AfterFunc(2*time.Second, control.Close)
				defer watchdog.Stop()
				info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: format, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-sonnet-4-5"}}
				service.BeginStreamAttempt(c, info)
				req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"stream":true}`))
				require.NoError(t, err)
				req = service.BindRelayRequestContext(c, req)
				resp, err := service.RelayHTTPClient(c, server.Client()).Do(req)
				require.NoError(t, err)
				info.StreamSession.ObserveTransport(resp, nil)
				usage, apiErr := claude.ClaudeStreamHandler(c, resp, info)
				require.Nil(t, apiErr)
				require.True(t, watchdog.Stop(), "normal completion must finish before the watchdog")
				usage = service.FinalizeStreamUsage(c, info, usage)
				require.False(t, info.StreamResult.Failed)
				require.False(t, info.StreamResult.ClientGone)
				require.True(t, info.StreamResult.EffectiveContent)
				require.True(t, info.StreamResult.Diagnostic.UsageFinal)
				require.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
				require.Equal(t, 20, usage.PromptTokens)
				require.Equal(t, 2, usage.CompletionTokens)
				body := recorder.Body.String()
				require.Equal(t, 1, strings.Count(body, "hello"))
				require.NotContains(t, body, "event: error\n")
				terminal := "data: [DONE]"
				if format == types.RelayFormatClaude {
					terminal = "event: message_stop\n"
				}
				require.Equal(t, 1, strings.Count(body, terminal), body)
				require.Same(t, usage, service.FinalizeStreamUsage(c, info, usage))
				require.Equal(t, body, recorder.Body.String())
				// The outer middleware closes the controller when its handler
				// returns. Invoke stale callbacks directly after stopping the real
				// timers so correctness does not depend on sleep/CI scheduling.
				control.Close()
				require.False(t, control.responseTimer.Stop())
				require.False(t, control.totalTimer.Stop())
				control.expireResponse()
				control.expireTotal()
				require.Equal(t, relayTimeoutKindNone, control.ExpiredKind())
				require.False(t, service.IsRelayRequestTimeout(c))
				require.False(t, info.StreamResult.Failed)
				require.Equal(t, body, recorder.Body.String())
			})
		}
	}
}

func TestForkVendorClientCancellationIsNotTimeout(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		t.Run(string(format), func(t *testing.T) {
			prefix := forkVendorClaudeStartedText + strings.Repeat("event: ping\ndata: {\"type\":\"ping\"}\n\n", 4)
			upstreamDone := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(upstreamDone)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, prefix)
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			t.Cleanup(server.Close)
			t.Cleanup(func() { close(release) })
			clientContext, cancelClient := context.WithCancel(context.Background())
			defer cancelClient()
			c, recorder, control := forkVendorTimeoutContext(t, clientContext, time.Hour, time.Hour, service.RelayStreamResponseTimeoutModeIdle)
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: format, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-sonnet-4-5"}}
			service.BeginStreamAttempt(c, info)
			req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"stream":true}`))
			require.NoError(t, err)
			req = service.BindRelayRequestContext(c, req)
			resp, err := service.RelayHTTPClient(c, server.Client()).Do(req)
			require.NoError(t, err)
			body := &forkStalledVendorBody{ReadCloser: resp.Body, remaining: len(prefix), stalled: make(chan struct{})}
			resp.Body = body
			info.StreamSession.ObserveTransport(resp, nil)
			triggerDone := make(chan struct{})
			go func() {
				defer close(triggerDone)
				select {
				case <-body.stalled:
					cancelClient()
				case <-release:
				}
			}()
			// Managed upstream contexts are detached from client cancellation;
			// the handler must close its response body when the client goes away.
			watchdog := time.AfterFunc(2*time.Second, control.Close)
			defer watchdog.Stop()
			usage, apiErr := claude.ClaudeStreamHandler(c, resp, info)
			require.Nil(t, apiErr)
			require.True(t, watchdog.Stop(), "client cancellation must finish before the watchdog")
			usage = service.FinalizeStreamUsage(c, info, usage)
			require.True(t, info.StreamResult.Failed)
			require.True(t, info.StreamResult.ClientGone)
			require.True(t, info.StreamResult.EffectiveContent)
			require.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
			require.False(t, service.IsRelayRequestTimeout(c))
			require.Equal(t, relayTimeoutKindNone, control.ExpiredKind())
			require.ErrorIs(t, context.Cause(c.Request.Context()), context.Canceled)
			require.Equal(t, 20, usage.PromptTokens)
			require.Equal(t, 2, usage.CompletionTokens)
			output := recorder.Body.String()
			require.Equal(t, 1, strings.Count(output, "hello"))
			require.NotContains(t, output, "event: error\n")
			require.NotContains(t, output, "event: message_stop\n")
			require.NotContains(t, output, "data: [DONE]")
			require.Same(t, usage, service.FinalizeStreamUsage(c, info, usage))
			require.Equal(t, output, recorder.Body.String(), "client cancellation must not append a terminal")
			select {
			case <-triggerDone:
			case <-time.After(time.Second):
				require.FailNow(t, "client-cancel trigger leaked")
			}
			select {
			case <-upstreamDone:
			case <-time.After(time.Second):
				require.FailNow(t, "handler did not close the detached upstream exchange")
			}
		})
	}
}
