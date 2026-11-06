package claude

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// These local fixtures reproduce the observed thinking-only stream shape;
// they contain no production prompt, response, credentials, or provider call.
const incidentThinkingStart = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n"
const incidentThinkingDelta = "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"Let me inspect the requested operation before answering.\"}}\n\n"
const incidentThinkingText = "Let me inspect the requested operation before answering."
const incidentMessageStop = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

// incidentTerminalReader models a transport returning an error either with its
// final data or on the next Read, without timers or external I/O.
type incidentTerminalReader struct {
	reader       *strings.Reader
	terminal     error
	withLastData bool
}

func (r *incidentTerminalReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if errors.Is(err, io.EOF) || r.withLastData && n > 0 && r.reader.Len() == 0 {
		return n, r.terminal
	}
	return n, err
}

// TestIncompleteStreamIncidentReproduction preserves genuine truncation failures
// while accepting an existing complete message_stop at a clean transport EOF.
func TestIncompleteStreamIncidentReproduction(t *testing.T) {
	thinkingPrefix := strictStart + incidentThinkingStart + incidentThinkingDelta
	complete := thinkingPrefix + strictStop
	throughUsage := strings.TrimSuffix(complete, incidentMessageStop)
	readFailure := errors.New("incident fixture transport read failure")
	estimatedOutput := service.EstimateTokenByModel("claude", incidentThinkingText)
	require.Positive(t, estimatedOutput)
	for _, tc := range []struct {
		name, body, mode, reason, diagnostic, source string
		output                                       int
	}{
		{"complete thinking lifecycle", complete, "ordinary", "done", "", "upstream", 7},
		{"thinking delta then clean EOF", thinkingPrefix, "ordinary", "upstream_incomplete", "upstream ended without a complete message_stop", "mixed", estimatedOutput},
		{"block stop and usage without message stop", throughUsage, "ordinary", "upstream_incomplete", "upstream ended without a complete message_stop", "upstream", 7},
		{"final JSON without newline", strings.TrimSuffix(complete, "\n\n"), "ordinary", "done", "", "upstream", 7},
		{"final JSON with one newline", strings.TrimSuffix(complete, "\n"), "ordinary", "done", "", "upstream", 7},
		{"final JSON with two newlines", complete, "ordinary", "done", "", "upstream", 7},
		{"complete final data and EOF same read", complete, "data_and_eof", "done", "", "upstream", 7},
		{"complete stream one byte reads", complete, "one_byte", "done", "", "upstream", 7},
		{"delimiterless final data and EOF same read", strings.TrimSuffix(complete, "\n\n"), "data_and_eof", "done", "", "upstream", 7},
		{"delimiterless terminal one byte reads", strings.TrimSuffix(complete, "\n\n"), "one_byte", "done", "", "upstream", 7},
		{"thinking then non EOF read error", thinkingPrefix, "read_error", "upstream_read_error", readFailure.Error(), "mixed", estimatedOutput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reader io.Reader = strings.NewReader(tc.body)
			switch tc.mode {
			case "data_and_eof":
				reader = &incidentTerminalReader{reader: strings.NewReader(tc.body), terminal: io.EOF, withLastData: true}
			case "one_byte":
				reader = &oneByteReader{reader: strings.NewReader(tc.body)}
			case "read_error":
				reader = &incidentTerminalReader{reader: strings.NewReader(tc.body), terminal: readFailure}
			}
			c, rec, resp, info := strictTestContext(io.NopCloser(reader))
			usage, apiErr := strictClaudeStream(c, resp, info)
			require.Nil(t, apiErr)
			require.Equal(t, tc.reason, string(info.StreamStatus.EndReason))
			require.Equal(t, tc.diagnostic, info.StreamResult.Diagnostic.Error)
			require.Equal(t, tc.source, info.StreamResult.UsageSource)
			require.True(t, info.StreamResult.EffectiveContent)
			require.Equal(t, 100, usage.PromptTokens)
			require.Equal(t, tc.output, usage.CompletionTokens)
			require.Equal(t, tc.reason == "done", info.StreamResult.Diagnostic.UsageFinal)
			errorCount := strings.Count(rec.Body.String(), "event: error\n")
			stopCount := strings.Count(rec.Body.String(), "event: message_stop\n")
			if tc.reason == "done" {
				require.Zero(t, errorCount)
				require.Equal(t, 1, stopCount)
				require.True(t, strings.HasSuffix(rec.Body.String(), "\n\n"), "the actual terminal event must reach the client's SSE parser")
			} else {
				require.Equal(t, 1, errorCount)
				require.Zero(t, stopCount, "incomplete streams must not receive a fabricated success terminator")
			}
			t.Logf("fixture_sha256=%x bytes=%d reader=%s end_reason=%s error=%q error_events=%d message_stop_events=%d usage_final=%t source=%s input=%d output=%d read_eof=%t read_error=%q", sha256.Sum256([]byte(tc.body)), len(tc.body), tc.mode, info.StreamStatus.EndReason, info.StreamResult.Diagnostic.Error, errorCount, stopCount, info.StreamResult.Diagnostic.UsageFinal, info.StreamResult.UsageSource, usage.PromptTokens, usage.CompletionTokens, info.StreamResult.Diagnostic.ReadEOF, info.StreamResult.Diagnostic.ReadError)
		})
	}
}

// TestIncompleteStreamIncidentManagedTimeout keeps a known local timeout
// separate from the EOF cases; it does not depend on wall-clock scheduling.
func TestIncompleteStreamIncidentManagedTimeout(t *testing.T) {
	body := strictStart + incidentThinkingStart + incidentThinkingDelta
	c, rec, resp, info := strictTestContext(io.NopCloser(strings.NewReader(body)))
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, &strictTimeoutControl{})
	usage, apiErr := strictClaudeStream(c, resp, info)
	require.Nil(t, apiErr)
	require.Equal(t, "timeout", string(info.StreamStatus.EndReason))
	require.Equal(t, context.Canceled.Error(), info.StreamResult.Diagnostic.Error)
	require.False(t, info.StreamResult.Diagnostic.UsageFinal)
	require.False(t, info.StreamResult.EffectiveContent)
	require.Equal(t, 1, strings.Count(rec.Body.String(), "event: error\n"))
	require.Zero(t, strings.Count(rec.Body.String(), "event: message_stop\n"))
	require.Contains(t, rec.Body.String(), "timeout_error")
	t.Logf("fixture_sha256=%x bytes=%d end_reason=%s error=%q error_events=1 message_stop_events=0 usage_final=%t source=%s input=%d output=%d", sha256.Sum256([]byte(body)), len(body), info.StreamStatus.EndReason, info.StreamResult.Diagnostic.Error, info.StreamResult.Diagnostic.UsageFinal, info.StreamResult.UsageSource, usage.PromptTokens, usage.CompletionTokens)
}

// TestIncompleteStreamIncidentHTTP reproduces transport EOF through a real
// loopback HTTP response. It identifies a trigger, not the production source
// that ended channel 460's response.
func TestIncompleteStreamIncidentHTTP(t *testing.T) {
	prefix := strictStart + incidentThinkingStart + incidentThinkingDelta
	for _, tc := range []struct {
		name, body, reason string
	}{
		{"complete thinking HTTP 200", prefix + strictStop, "done"},
		{"terminal without final delimiter HTTP 200", strings.TrimSuffix(prefix+strictStop, "\n\n"), "done"},
		{"thinking HTTP 200 then handler returns", prefix, "upstream_incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.body)
				w.(http.Flusher).Flush()
			}))
			t.Cleanup(server.Close)
			resp, err := server.Client().Get(server.URL)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			c, rec, _, info := strictTestContext(resp.Body)
			usage, apiErr := ClaudeStreamHandler(c, resp, info)
			require.Nil(t, apiErr)
			require.Equal(t, tc.reason, string(info.StreamStatus.EndReason))
			require.True(t, info.StreamResult.EffectiveContent)
			require.Equal(t, tc.reason == "done", info.StreamResult.Diagnostic.UsageFinal)
			errorCount := strings.Count(rec.Body.String(), "event: error\n")
			stopCount := strings.Count(rec.Body.String(), "event: message_stop\n")
			if tc.reason == "done" {
				require.Empty(t, info.StreamResult.Diagnostic.Error)
				require.Zero(t, errorCount)
				require.Equal(t, 1, stopCount)
				require.Equal(t, 7, usage.CompletionTokens)
			} else {
				require.Equal(t, "upstream ended without a complete message_stop", info.StreamResult.Diagnostic.Error)
				require.Equal(t, 1, errorCount)
				require.Zero(t, stopCount)
				require.True(t, info.StreamResult.Diagnostic.ReadEOF)
				require.Equal(t, service.EstimateTokenByModel("claude", incidentThinkingText), usage.CompletionTokens)
			}
			t.Logf("fixture_sha256=%x bytes=%d transport=loopback_http status=%d end_reason=%s error=%q error_events=%d message_stop_events=%d usage_final=%t source=%s input=%d output=%d read_eof=%t read_error=%q", sha256.Sum256([]byte(tc.body)), len(tc.body), resp.StatusCode, info.StreamStatus.EndReason, info.StreamResult.Diagnostic.Error, errorCount, stopCount, info.StreamResult.Diagnostic.UsageFinal, info.StreamResult.UsageSource, usage.PromptTokens, usage.CompletionTokens, info.StreamResult.Diagnostic.ReadEOF, info.StreamResult.Diagnostic.ReadError)
		})
	}
}

// TestIncompleteStreamGatewayEOFClassification distinguishes each hop's read
// result from the selected usage. It does not invoke settlement or a database.
func TestIncompleteStreamGatewayEOFClassification(t *testing.T) {
	original := operation_setting.ClaudeStreamUpstreamCompatibilityEnabled()
	require.NoError(t, config.GlobalConfig.UpdateFromMap("claude_stream_compat_setting", map[string]string{"enabled": "true"}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("claude_stream_compat_setting", map[string]string{"enabled": strconv.FormatBool(original)}))
	})
	empty := strictStart + incidentThinkingStart
	thinking := empty + incidentThinkingDelta
	residual := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\""
	for _, tc := range []struct {
		name, body, mode string
		effective        bool
	}{
		{"empty thinking start then clean EOF", empty, "clean_eof", false},
		{"thinking delta then clean EOF", thinking, "clean_eof", true},
		{"empty thinking start then unexpected EOF", empty, "unexpected_eof", false},
		{"thinking delta then unexpected EOF", thinking, "unexpected_eof", true},
		{"empty thinking with residual then unexpected EOF", empty + residual, "unexpected_eof", false},
		{"thinking delta with residual then unexpected EOF", thinking + residual, "unexpected_eof", true},
		{"gateway read error then normal HTTP end empty thinking", empty, "gateway_finishes_http", false},
		{"gateway read error then normal HTTP end thinking delta", thinking, "gateway_finishes_http", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reader io.Reader = strings.NewReader(tc.body)
			if tc.mode == "unexpected_eof" {
				reader = &incidentTerminalReader{reader: strings.NewReader(tc.body), terminal: io.ErrUnexpectedEOF, withLastData: true}
			}
			c, rec, resp, info := strictTestContext(io.NopCloser(reader))
			var earlierHopReadError chan error
			if tc.mode == "gateway_finishes_http" {
				// Model an intermediary that records a provider read failure, then
				// returns normally from its HTTP handler without forwarding an error.
				earlierHopReadError = make(chan error, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("X-New-Api-Version", "incident-fixture")
					w.Header().Set("X-Oneapi-Request-Id", "incident-upstream-fixture")
					_, err := io.Copy(w, &incidentTerminalReader{reader: strings.NewReader(tc.body), terminal: io.ErrUnexpectedEOF})
					earlierHopReadError <- err
					w.(http.Flusher).Flush()
				}))
				t.Cleanup(server.Close)
				var err error
				resp, err = server.Client().Get(server.URL)
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.Equal(t, "incident-fixture", resp.Header.Get("X-New-Api-Version"))
				require.Equal(t, "incident-upstream-fixture", resp.Header.Get("X-Oneapi-Request-Id"))
			}
			usage, apiErr := ClaudeStreamHandler(c, resp, info)
			require.Nil(t, apiErr)
			result := info.StreamResult
			diagnostic := result.Diagnostic
			require.True(t, result.Failed)
			require.False(t, result.ClientGone)
			require.Equal(t, tc.effective, result.EffectiveContent)
			require.True(t, result.ConfirmedUsage)
			require.False(t, diagnostic.UsageFinal)
			require.Equal(t, 100, diagnostic.UsageEvidence["input_tokens"], "upstream input evidence remains visible even when selected usage is zero")
			require.Equal(t, 0, diagnostic.UsageEvidence["output_tokens"])
			require.Equal(t, int64(len(tc.body)), diagnostic.ObservedBytes)
			require.Equal(t, tc.body, string(diagnostic.BodyHead)+string(diagnostic.BodyTail))
			require.Equal(t, 1, strings.Count(rec.Body.String(), "event: error\n"))
			require.Zero(t, strings.Count(rec.Body.String(), "event: message_stop\n"))
			if tc.mode == "unexpected_eof" {
				require.Equal(t, "upstream_read_error", string(info.StreamStatus.EndReason))
				require.Equal(t, io.ErrUnexpectedEOF.Error(), diagnostic.Error)
				require.Equal(t, io.ErrUnexpectedEOF.Error(), diagnostic.ReadError)
				require.False(t, diagnostic.ReadEOF)
			} else {
				require.Equal(t, "upstream_incomplete", string(info.StreamStatus.EndReason))
				require.Equal(t, "upstream ended without a complete message_stop", diagnostic.Error)
				require.Empty(t, diagnostic.ReadError)
				require.True(t, diagnostic.ReadEOF)
			}
			if tc.effective {
				require.Equal(t, "mixed", result.UsageSource)
				require.Equal(t, 100, usage.PromptTokens)
				require.Equal(t, service.EstimateTokenByModel("claude", incidentThinkingText), usage.CompletionTokens)
			} else {
				require.Equal(t, "none", result.UsageSource)
				require.Zero(t, usage.PromptTokens)
				require.Zero(t, usage.CompletionTokens)
				require.Zero(t, usage.TotalTokens)
			}
			require.False(t, result.SettlementAttempted, "this experiment verifies selection, not a balance change")
			if earlierHopReadError != nil {
				require.ErrorIs(t, <-earlierHopReadError, io.ErrUnexpectedEOF)
			}
			t.Logf("fixture_sha256=%x bytes=%d transport=%s end_reason=%s error=%q effective=%t usage_final=%t source=%s selected_input=%d selected_output=%d evidence_input=%d evidence_output=%d read_eof=%t read_error=%q settlement_attempted=%t", sha256.Sum256([]byte(tc.body)), len(tc.body), tc.mode, info.StreamStatus.EndReason, diagnostic.Error, result.EffectiveContent, diagnostic.UsageFinal, result.UsageSource, usage.PromptTokens, usage.CompletionTokens, diagnostic.UsageEvidence["input_tokens"], diagnostic.UsageEvidence["output_tokens"], diagnostic.ReadEOF, diagnostic.ReadError, result.SettlementAttempted)
		})
	}
}

func TestStrictEOFTerminalCompatibilityBoundaries(t *testing.T) {
	prefix := strictStart + incidentThinkingStart + incidentThinkingDelta
	throughUsage := strings.TrimSuffix(prefix+strictStop, incidentMessageStop)
	terminal := strings.TrimSuffix(incidentMessageStop, "\n\n")
	transportErr := errors.New("incident terminal transport failure")
	for _, tc := range []struct {
		name, body, reason string
		readError          error
	}{
		{"data only terminal", throughUsage + "data: {\"type\":\"message_stop\"}", "done", nil},
		{"terminal with one CRLF", throughUsage + terminal + "\r\n", "done", nil},
		{"terminal with multiline data", throughUsage + "event: message_stop\ndata: {\ndata: \"type\":\"message_stop\"\ndata: }", "done", nil},
		{"terminal with transport error", throughUsage + terminal, "upstream_read_error", transportErr},
		{"malformed terminal JSON", throughUsage + "event: message_stop\ndata: {\"type\":\"message_stop\"", "upstream_incomplete", nil},
		{"nonterminal residual", strings.TrimSuffix(throughUsage, "\n\n"), "upstream_incomplete", nil},
		{"missing message start", terminal, "upstream_protocol_error", nil},
		{"still open thinking block", prefix + terminal, "upstream_protocol_error", nil},
		{"missing stop reason", prefix + "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" + terminal, "upstream_protocol_error", nil},
		{"conflicting SSE event", throughUsage + "event: ping\ndata: {\"type\":\"message_stop\"}", "upstream_incomplete", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reader io.Reader = strings.NewReader(tc.body)
			if tc.readError != nil {
				reader = &incidentTerminalReader{reader: strings.NewReader(tc.body), terminal: tc.readError, withLastData: true}
			}
			c, rec, resp, info := strictTestContext(io.NopCloser(reader))
			_, apiErr := strictClaudeStream(c, resp, info)
			require.Nil(t, apiErr)
			require.Equal(t, tc.reason, string(info.StreamStatus.EndReason))
			diagnostic := info.StreamResult.Diagnostic
			require.Equal(t, tc.body, string(diagnostic.BodyHead)+string(diagnostic.BodyTail), "original capture must not gain a delimiter")
			if tc.reason == "done" {
				require.False(t, info.StreamResult.Failed)
				require.True(t, diagnostic.UsageFinal)
				require.NotContains(t, rec.Body.String(), "event: error\n")
				require.Contains(t, rec.Body.String(), "\"type\":\"message_stop\"")
				require.True(t, strings.HasSuffix(rec.Body.String(), "\n\n"))
			} else {
				require.True(t, info.StreamResult.Failed)
				require.False(t, diagnostic.UsageFinal)
				require.Equal(t, 1, strings.Count(rec.Body.String(), "event: error\n"))
				require.NotContains(t, rec.Body.String(), "\"type\":\"message_stop\"")
				if tc.readError != nil {
					require.Equal(t, tc.readError.Error(), diagnostic.Error)
					require.Equal(t, tc.readError.Error(), diagnostic.ReadError)
				}
			}
		})
	}
}

// incidentConfigReader changes the live setting after the response has taken
// its snapshot, without introducing sleeps or concurrent Gin access.
type incidentConfigReader struct {
	reader  *strings.Reader
	enabled bool
	changed bool
	err     error
}

func (r *incidentConfigReader) Read(p []byte) (int, error) {
	if !r.changed {
		r.changed = true
		r.err = config.GlobalConfig.UpdateFromMap("claude_stream_compat_setting", map[string]string{"enabled": strconv.FormatBool(r.enabled)})
	}
	return r.reader.Read(p)
}

func TestStrictEOFTerminalCompatibilitySnapshot(t *testing.T) {
	original := operation_setting.ClaudeStreamUpstreamCompatibilityEnabled()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("claude_stream_compat_setting", map[string]string{"enabled": strconv.FormatBool(original)}))
	})
	body := strings.TrimSuffix(strictStart+incidentThinkingStart+incidentThinkingDelta+strictStop, "\n\n")
	for _, enabled := range []bool{true, false} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			require.NoError(t, config.GlobalConfig.UpdateFromMap("claude_stream_compat_setting", map[string]string{"enabled": strconv.FormatBool(enabled)}))
			reader := &incidentConfigReader{reader: strings.NewReader(body), enabled: !enabled}
			c, rec, resp, info := strictTestContext(io.NopCloser(reader))
			_, apiErr := strictClaudeStream(c, resp, info)
			require.Nil(t, apiErr)
			require.NoError(t, reader.err)
			require.Equal(t, !enabled, operation_setting.ClaudeStreamUpstreamCompatibilityEnabled())
			if enabled {
				require.Equal(t, "done", string(info.StreamStatus.EndReason))
				require.Equal(t, 1, strings.Count(rec.Body.String(), "event: message_stop\n"))
			} else {
				require.Equal(t, "upstream_incomplete", string(info.StreamStatus.EndReason))
				require.NotContains(t, rec.Body.String(), "event: message_stop\n")
			}
		})
	}
}
