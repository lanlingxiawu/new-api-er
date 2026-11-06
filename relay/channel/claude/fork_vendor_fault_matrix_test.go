package claude

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// These are local synthetic payloads following the vendor's event schema, not
// recordings of live requests. Event order, signatures, cumulative usage, and
// error events: https://platform.claude.com/docs/en/build-with-claude/streaming
const forkClaudeModel = "claude-sonnet-4-5-20250929"

func forkClaudeEvent(payload string) string {
	return "event: " + gjson.Get(payload, "type").String() + "\ndata: " + payload + "\n\n"
}

func forkClaudeStart(usage string) string {
	if usage != "" {
		usage = `,"usage":` + usage
	}
	return forkClaudeEvent(`{"type":"message_start","message":{"id":"msg_vendor_matrix","type":"message","role":"assistant","model":"` + forkClaudeModel + `","content":[],"stop_reason":null,"stop_sequence":null` + usage + `}}`)
}

func forkClaudeFinish(reason string, output int) string {
	return forkClaudeEvent(fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q,"stop_sequence":null},"usage":{"output_tokens":%d}}`, reason, output)) + forkClaudeEvent(`{"type":"message_stop"}`)
}

func forkClaudeText(index int) string {
	return forkClaudeEvent(fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, index)) +
		forkClaudeEvent(fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":"Hello, 世界 🌍"}}`, index))
}

func forkClaudeClose(index int) string {
	return forkClaudeEvent(fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index))
}

func forkClaudeThinking(index int) string {
	return forkClaudeEvent(fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"thinking","thinking":""}}`, index)) +
		forkClaudeEvent(fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"thinking_delta","thinking":"Let me check."}}`, index)) +
		forkClaudeEvent(fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"signature_delta","signature":"c3ludGhldGljLXNpZ25hdHVyZQ=="}}`, index)) + forkClaudeClose(index)
}

func forkClaudeTool(index int) string {
	return forkClaudeEvent(fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"tool_use","id":"toolu_matrix_%d","name":"get_weather","input":{}}}`, index, index)) +
		forkClaudeEvent(fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":"{\"location\":"}}`, index)) +
		forkClaudeEvent(fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":"\"東京\"}"}}`, index)) + forkClaudeClose(index)
}

// forkClaudeReadBody forces read boundaries through SSE fields and UTF-8 bytes;
// terminal can accompany the final bytes to cover io.Reader's (n > 0, err) rule.
type forkClaudeReadBody struct {
	data     []byte
	chunk    int
	terminal error
	closed   bool
}

func (r *forkClaudeReadBody) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := min(len(p), len(r.data))
	if r.chunk > 0 {
		n = min(n, r.chunk)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, r.terminal
	}
	return n, nil
}

func (r *forkClaudeReadBody) Close() error { r.closed = true; return nil }

func TestForkClaudeVendorValidStreams(t *testing.T) {
	start := forkClaudeStart(`{"input_tokens":21,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1}`)
	for _, tc := range []struct{ name, blocks, stop string }{
		{"text", forkClaudeText(0) + forkClaudeClose(0), "end_turn"},
		{"thinking_then_text", forkClaudeThinking(0) + forkClaudeText(1) + forkClaudeClose(1), "end_turn"},
		{"fragmented_tool", forkClaudeTool(0), "tool_use"},
		{"two_serial_tools", forkClaudeTool(0) + forkClaudeTool(1), "tool_use"},
		{"text_then_tool", forkClaudeText(0) + forkClaudeClose(0) + forkClaudeTool(1), "tool_use"},
	} {
		for _, chunk := range []int{1, 7, 4096} {
			t.Run(fmt.Sprintf("%s/read_%d", tc.name, chunk), func(t *testing.T) {
				body := start + forkClaudeEvent(`{"type":"ping"}`) + tc.blocks + forkClaudeFinish(tc.stop, 12)
				reader := &forkClaudeReadBody{data: []byte(body), chunk: chunk, terminal: io.EOF}
				c, rec, resp, info := strictTestContext(reader)
				info.UpstreamModelName = forkClaudeModel
				usage, apiErr := ClaudeStreamHandler(c, resp, info)
				require.Nil(t, apiErr)
				require.True(t, reader.closed)
				// Native forwarding keeps the existing input-usage patch on message_delta.
				require.Equal(t, strings.Replace(body, `"usage":{"output_tokens":12}`, `"usage":{"output_tokens":12,"input_tokens":21}`, 1), rec.Body.String())
				require.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
				require.False(t, info.StreamResult.Failed)
				require.True(t, info.StreamResult.EffectiveContent)
				require.Equal(t, "upstream", info.StreamResult.UsageSource)
				require.Equal(t, 21, usage.PromptTokens)
				require.Equal(t, 12, usage.CompletionTokens)
				require.Equal(t, 1, strings.Count(rec.Body.String(), "event: message_stop"))
			})
		}
	}
}

func TestForkClaudeVendorFaultMatrix(t *testing.T) {
	start := forkClaudeStart(`{"input_tokens":21,"output_tokens":0}`)
	text := forkClaudeText(0)
	for _, tc := range []struct {
		name, body, reason string
		effective          bool
		terminal           error
	}{
		{"no_bytes", "", "upstream_incomplete", false, nil},
		{"start_only", start, "upstream_incomplete", false, nil},
		{"text_then_clean_eof", start + text, "upstream_incomplete", true, nil},
		{"text_then_transport_error_same_read", start + text, "upstream_read_error", true, io.ErrUnexpectedEOF},
		{"half_json_frame", start + "event: content_block_start\ndata: {\"type\":", "upstream_incomplete", false, nil},
		{"half_json_transport_error", start + "event: content_block_start\ndata: {\"type\":", "upstream_read_error", false, io.ErrUnexpectedEOF},
		{"malformed_json_complete_frame", start + "event: content_block_start\ndata: {oops}\n\n", "upstream_json_error", false, nil},
		{"html_in_sse", "data: <html>upstream unavailable</html>\n\n", "upstream_json_error", false, nil},
		{"duplicate_start", start + start, "upstream_protocol_error", false, nil},
		{"delta_without_block", start + forkClaudeEvent(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"orphan"}}`), "upstream_protocol_error", false, nil},
		{"stop_without_block", start + forkClaudeClose(0), "upstream_protocol_error", false, nil},
		{"terminal_with_open_block", start + text + forkClaudeFinish("end_turn", 12), "upstream_protocol_error", true, nil},
		{"terminal_without_stop_reason", start + text + forkClaudeClose(0) + forkClaudeEvent(`{"type":"message_stop"}`), "upstream_protocol_error", true, nil},
		{"wrong_sse_event", start + "event: ping\ndata: {\"type\":\"message_stop\"}\n\n", "upstream_protocol_error", false, nil},
		{"known_event_missing_data", start + "event: content_block_start\n\n", "upstream_protocol_error", false, nil},
		{"negative_block_index", start + forkClaudeEvent(`{"type":"content_block_start","index":-1,"content_block":{"type":"text","text":""}}`), "upstream_protocol_error", false, nil},
		{"fractional_block_index", start + forkClaudeEvent(`{"type":"content_block_start","index":0.5,"content_block":{"type":"text","text":""}}`), "upstream_json_error", false, nil},
		{"thinking_delta_in_text_block", start + text + forkClaudeEvent(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"wrong block"}}`), "upstream_protocol_error", true, nil},
		{"signature_in_text_block", start + text + forkClaudeEvent(`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"opaque"}}`), "upstream_protocol_error", true, nil},
		{"reused_block_index", start + text + forkClaudeClose(0) + text, "upstream_protocol_error", true, nil},
		{"conflicting_stop_reason", start + text + forkClaudeClose(0) + forkClaudeEvent(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":12}}`) + forkClaudeFinish("max_tokens", 12), "upstream_protocol_error", true, nil},
		{"foreign_terminal_field_on_ping", start + forkClaudeEvent(`{"type":"ping","done":true}`), "upstream_incomplete", false, nil},
		{"incomplete_tool_with_tool_use_terminal", start + forkClaudeEvent(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_incomplete","name":"get_weather","input":{}}}`) + forkClaudeEvent(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"location\":"}}`) + forkClaudeClose(0) + forkClaudeFinish("tool_use", 12), "upstream_protocol_error", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &forkClaudeReadBody{data: []byte(tc.body), chunk: 3, terminal: tc.terminal}
			c, rec, resp, info := strictTestContext(reader)
			info.UpstreamModelName = forkClaudeModel
			usage, apiErr := ClaudeStreamHandler(c, resp, info)
			require.Nil(t, apiErr)
			require.True(t, reader.closed)
			require.Equal(t, tc.reason, string(info.StreamStatus.EndReason))
			require.True(t, info.StreamResult.Failed)
			require.Equal(t, tc.effective, info.StreamResult.EffectiveContent)
			require.GreaterOrEqual(t, usage.PromptTokens, 0)
			require.GreaterOrEqual(t, usage.CompletionTokens, 0)
			require.Equal(t, 1, strings.Count(rec.Body.String(), "event: error"))
			require.NotContains(t, rec.Body.String(), "event: message_stop")
			require.NotContains(t, rec.Body.String(), "[DONE]")
		})
	}
}

func TestForkClaudeVendorErrorPriority(t *testing.T) {
	for _, prefix := range []string{"", forkClaudeStart(`{"input_tokens":21,"output_tokens":1}`) + forkClaudeText(0)} {
		for _, named := range []bool{false, true} {
			t.Run(fmt.Sprintf("delivered_%t/named_%t", prefix != "", named), func(t *testing.T) {
				frame := forkClaudeEvent(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
				if !named {
					frame = strings.TrimPrefix(frame, "event: error\n")
				}
				c, rec, resp, info := strictTestContext(&forkClaudeReadBody{data: []byte(prefix + frame), terminal: io.ErrUnexpectedEOF})
				_, apiErr := ClaudeStreamHandler(c, resp, info)
				require.Nil(t, apiErr)
				require.Equal(t, "upstream_error", string(info.StreamStatus.EndReason), "a complete upstream error outranks a later transport failure")
				require.Equal(t, "Overloaded", info.StreamResult.ErrorMessage)
				require.Equal(t, prefix+frame, rec.Body.String(), "keep exactly the vendor error without an invented terminal")
			})
		}
	}
}

func TestForkClaudeVendorUndeliveredUsage(t *testing.T) {
	for _, tc := range []struct {
		name, usage           string
		prompt, output, cache int
		confirmed             bool
	}{
		{"missing", "", 0, 0, 0, false},
		{"explicit_zero", `{"input_tokens":0,"output_tokens":0}`, 0, 0, 0, true},
		{"input_only", `{"input_tokens":21}`, 21, 0, 0, true},
		{"initial_output", `{"input_tokens":21,"output_tokens":3}`, 21, 3, 0, true},
		{"cache_only", `{"cache_read_input_tokens":20}`, 0, 0, 20, true},
		{"negative_input", `{"input_tokens":-1,"output_tokens":3}`, 0, 0, 0, false},
		{"negative_output", `{"input_tokens":21,"output_tokens":-1}`, 0, 0, 0, false},
		{"oversized_input", `{"input_tokens":1000000001}`, 0, 0, 0, false},
	} {

		t.Run(fmt.Sprintf("%s", tc.name), func(t *testing.T) {
			body := forkClaudeStart(tc.usage) + forkClaudeEvent(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`)
			c, rec, resp, info := strictTestContext(io.NopCloser(strings.NewReader(body)))
			info.UpstreamModelName = forkClaudeModel
			usage, apiErr := ClaudeStreamHandler(c, resp, info)
			require.Nil(t, apiErr)
			require.False(t, info.StreamResult.EffectiveContent)
			require.True(t, info.StreamResult.Failed)
			require.Equal(t, tc.confirmed, info.StreamResult.ConfirmedUsage)
			require.Equal(t, "none", info.StreamResult.UsageSource)
			require.Zero(t, usage.TotalTokens)
			require.Zero(t, usage.PromptTokensDetails.CachedTokens)
			require.Equal(t, tc.prompt, info.StreamResult.Diagnostic.UsageEvidence["input_tokens"])
			require.Equal(t, tc.output, info.StreamResult.Diagnostic.UsageEvidence["output_tokens"])
			require.Equal(t, tc.cache, info.StreamResult.Diagnostic.UsageEvidence["cache_read_input_tokens"])
			require.Equal(t, 1, strings.Count(rec.Body.String(), "event: error"))
			require.NotContains(t, rec.Body.String(), "event: message_stop")
		})

	}
}

func TestForkClaudeVendorHTTPTruncation(t *testing.T) {
	prefix := forkClaudeStart(`{"input_tokens":21,"output_tokens":1}`) + forkClaudeText(0)
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprintf("complete_%t", complete), func(t *testing.T) {
			body := prefix
			if complete {
				body += forkClaudeClose(0) + forkClaudeFinish("end_turn", 12)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				length := len(body)
				if !complete {
					length += 100 // A real HTTP body shorter than Content-Length.
				}
				w.Header().Set("Content-Length", strconv.Itoa(length))
				for offset := 0; offset < len(body); offset += 7 {
					_, _ = io.WriteString(w, body[offset:min(offset+7, len(body))])
					w.(http.Flusher).Flush()
				}
			}))
			defer server.Close()
			resp, err := server.Client().Get(server.URL)
			require.NoError(t, err)
			c, rec, _, info := strictTestContext(nil)
			info.UpstreamModelName = forkClaudeModel
			usage, apiErr := ClaudeStreamHandler(c, resp, info)
			require.Nil(t, apiErr)
			require.Equal(t, 21, usage.PromptTokens)
			require.True(t, info.StreamResult.EffectiveContent)
			if complete {
				require.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
				require.Equal(t, strings.Replace(body, `"usage":{"output_tokens":12}`, `"usage":{"output_tokens":12,"input_tokens":21}`, 1), rec.Body.String())
			} else {
				require.Equal(t, "upstream_read_error", string(info.StreamStatus.EndReason))
				require.Contains(t, info.StreamResult.Diagnostic.Error, "unexpected EOF")
				require.NotContains(t, rec.Body.String(), "event: message_stop")
				require.Equal(t, 1, strings.Count(rec.Body.String(), "event: error"))
			}
		})
	}
}

type forkClaudeBlockedBody struct {
	reading, closed     chan struct{}
	readOnce, closeOnce sync.Once
}

func (b *forkClaudeBlockedBody) Read([]byte) (int, error) {
	b.readOnce.Do(func() { close(b.reading) })
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *forkClaudeBlockedBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func TestForkClaudeVendorSilentUpstreamCancellation(t *testing.T) {
	// Deterministic unbounded silence: cancel only after Read is blocked, without
	// sleeping or waiting for a real relay timeout. Closing must release the reader.
	body := &forkClaudeBlockedBody{reading: make(chan struct{}), closed: make(chan struct{})}
	c, rec, resp, info := strictTestContext(body)
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	defer body.Close()
	c.Request = c.Request.WithContext(ctx)
	info.UpstreamModelName = forkClaudeModel
	type result struct {
		usage *dto.Usage
		err   *types.NewAPIError
	}
	done := make(chan result, 1)
	go func() { usage, err := ClaudeStreamHandler(c, resp, info); done <- result{usage, err} }()
	select {
	case <-body.reading:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "upstream reader did not start")
	}
	cancel()
	select {
	case result := <-done:
		require.Nil(t, result.err)
		// User cancellation retains the existing estimated-input policy, distinct
		// from an undelivered upstream failure or a managed refund timeout.
		require.Equal(t, info.GetEstimatePromptTokens(), result.usage.PromptTokens)
		require.Zero(t, result.usage.CompletionTokens)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "cancel did not release the blocked upstream reader")
	}
	require.Equal(t, "client_gone", string(info.StreamStatus.EndReason))
	require.True(t, info.StreamResult.ClientGone)
	require.Equal(t, "estimated", info.StreamResult.UsageSource)
	require.Empty(t, rec.Body.String())
}
