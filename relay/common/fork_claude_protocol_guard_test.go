package common

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

const forkGuardClaudeStart = `{"type":"message_start","message":{"id":"msg_guard","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250929","content":[],"usage":{"input_tokens":21,"output_tokens":0}}}`

func TestClaudeRequiredProtocolBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, event, payload, reason string
	}{
		{"chat", "", `{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":999}}`, "upstream_protocol_error"},
		{"responses", "", `{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":999}}}`, "upstream_protocol_error"},
		{"gemini", "", `{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":999}}`, "upstream_protocol_error"},
		{"spoofed ping", "", `{"type":"ping","choices":[]}`, "upstream_protocol_error"},
		{"snake case Gemini", "", `{"type":"ping","usage_metadata":{"promptTokenCount":999}}`, "upstream_protocol_error"},
		{"missing type", "", `{}`, "upstream_protocol_error"},
		{"empty type", "", `{"type":""}`, "upstream_protocol_error"},
		{"null type", "", `{"type":null}`, "upstream_protocol_error"},
		{"numeric type", "", `{"type":7}`, "upstream_protocol_error"},
		{"foreign DONE", "", `[DONE]`, "upstream_protocol_error"},
		{"future Claude event", "", `{"type":"future_event","extra":true}`, ""},
		{"Claude ping", "ping", `{"type":"ping"}`, ""},
		{"provider error", "", `{"error":{"type":"api_error","message":"fixture unavailable"}}`, "upstream_error"},
		{"named provider error", "error", `{"message":"fixture unavailable"}`, "upstream_error"},
		{"foreign failure is still failure", "", `{"type":"response.failed","response":{"status":"failed","error":{"message":"fixture unavailable"}}}`, "upstream_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatOpenAIResponses)
			s.RequireClaudeProtocol = true
			err := s.ObserveEvent(tc.event, []byte(tc.payload))
			if tc.reason == "upstream_protocol_error" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.reason, string(s.Snapshot().Reason))
			require.False(t, s.ProtocolComplete())
			require.Empty(t, s.Snapshot().Evidence, "foreign usage must not enter billing evidence")
		})
	}
	for _, tc := range []struct {
		format types.RelayFormat
		body   string
	}{
		{types.RelayFormatOpenAI, `{"choices":[{"index":0,"finish_reason":"stop"}]}`},
		{types.RelayFormatOpenAIResponses, `{"type":"response.completed","response":{"status":"completed"}}`},
		{types.RelayFormatGemini, `{"candidates":[{"index":0,"finishReason":"STOP"}]}`},
	} {
		t.Run("other upstream unchanged/"+string(tc.format), func(t *testing.T) {
			s := NewStreamSession(tc.format)
			require.NoError(t, s.ObserveEvent("", []byte(tc.body)))
			require.True(t, s.ProtocolComplete())
		})
	}
	t.Run("DONE after a real Claude terminator", func(t *testing.T) {
		s := NewStreamSession(types.RelayFormatOpenAI)
		s.RequireClaudeProtocol = true
		for _, event := range []string{forkGuardClaudeStart, `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, `{"type":"message_stop"}`, `[DONE]`} {
			require.NoError(t, s.ObserveEvent("", []byte(event)))
		}
		require.True(t, s.ProtocolComplete())
	})
}

const forkGuardClaudeTool = `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_guard","name":"get_weather","input":{}}}`
const forkGuardClaudeToolFragment = `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"location\":"}}`
const forkGuardClaudeClose = `{"type":"content_block_stop","index":0}`

func TestForkClaudeProtocolGuardToolCompletion(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		for _, reason := range []string{"max_tokens", "tool_use", "end_turn"} {
			t.Run(fmt.Sprintf("%s/%s", format, reason), func(t *testing.T) {
				s := NewStreamSession(format)
				for _, payload := range []string{forkGuardClaudeStart, forkGuardClaudeTool, forkGuardClaudeToolFragment, forkGuardClaudeClose} {
					require.NoError(t, s.ObserveEvent("", []byte(payload)))
				}
				err := s.ObserveEvent("", []byte(fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":12}}`, reason)))
				if reason != "max_tokens" {
					require.Error(t, err)
					require.False(t, s.ProtocolComplete())
					return
				}
				require.NoError(t, err)
				require.False(t, s.ProtocolComplete(), "a real message_stop is still required")
				require.NoError(t, s.ObserveEvent("", []byte(`{"type":"message_stop"}`)))
				require.True(t, s.ProtocolComplete())
				require.False(t, s.Snapshot().Effective, "validation cannot claim successful delivery")
			})
		}
	}
}

func TestForkClaudeProtocolGuardBlockValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prefix []string
		reject string
	}{
		{"missing_type", nil, `{"type":"content_block_start","index":0,"content_block":{}}`},
		{"missing_text", nil, `{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`},
		{"missing_thinking", nil, `{"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`},
		{"missing_identity", nil, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","input":{}}}`},
		{"fractional_index", nil, `{"type":"content_block_start","index":0.5,"content_block":{"type":"text","text":""}}`},
		{"negative_index", nil, `{"type":"content_block_start","index":-1,"content_block":{"type":"text","text":""}}`},
		{"non_string_fragment", []string{forkGuardClaudeTool}, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":1}}`},
		{"array_tool_input", []string{forkGuardClaudeTool, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"[]"}}`}, forkGuardClaudeClose},
		{"null_tool_input", []string{forkGuardClaudeTool, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"null"}}`}, forkGuardClaudeClose},
		{"server_tool_cannot_truncate", []string{strings.Replace(forkGuardClaudeTool, `"tool_use"`, `"server_tool_use"`, 1), forkGuardClaudeToolFragment}, forkGuardClaudeClose},
		{"new_block_after_truncated_tool", []string{forkGuardClaudeTool, forkGuardClaudeToolFragment, forkGuardClaudeClose}, strings.Replace(forkGuardClaudeTool, `"index":0`, `"index":1`, 1)},
		{"non_string_stop_reason", nil, `{"type":"message_delta","delta":{"stop_reason":5}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatClaude)
			require.NoError(t, s.ObserveEvent("", []byte(forkGuardClaudeStart)))
			for _, frame := range tc.prefix {
				require.NoError(t, s.ObserveEvent("", []byte(frame)))
			}
			require.Error(t, s.ObserveEvent("", []byte(tc.reject)))
			require.Equal(t, StreamEndReason("upstream_protocol_error"), s.Snapshot().Reason)
			require.False(t, s.ProtocolComplete())
		})
	}
}

func TestForkClaudeProtocolGuardStorageLifecycle(t *testing.T) {
	t.Run("closed blocks release budget", func(t *testing.T) {
		s := NewStreamSession(types.RelayFormatClaude)
		require.NoError(t, s.ObserveEvent("", []byte(forkGuardClaudeStart)))
		require.NoError(t, s.ObserveEvent("", []byte(forkGuardClaudeTool)))
		require.Equal(t, len("tool_use")+len("toolu_guard")+len("get_weather")+len("{}"), s.claudeBlockBytes)
		require.NoError(t, s.ObserveEvent("", []byte(forkGuardClaudeClose)))
		require.Empty(t, s.claudeBlocks)
		require.Zero(t, s.claudeBlockBytes)
	})
	t.Run("shared byte boundary without large allocation", func(t *testing.T) {
		s := NewStreamSession(types.RelayFormatClaude)
		require.NoError(t, s.ObserveEvent("", []byte(forkGuardClaudeStart)))
		require.NoError(t, s.ObserveEvent("", []byte(forkGuardClaudeTool)))
		// Simulate other active storage to exercise the exact accounting boundary.
		s.claudeBlockBytes = MaxStreamFrameBytes - 2
		require.NoError(t, s.ObserveEvent("", []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`)))
		require.Equal(t, MaxStreamFrameBytes, s.claudeBlockBytes)
		require.Error(t, s.ObserveEvent("", []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":" "}}`)))
		require.Equal(t, "{}", s.claudeBlocks[0].args.String(), "over-budget bytes were never appended")
	})
	t.Run("new transport clears pending tool state", func(t *testing.T) {
		s := NewStreamSession(types.RelayFormatClaude)
		s.ObserveTransport(&http.Response{StatusCode: 200}, nil)
		for _, frame := range []string{forkGuardClaudeStart, forkGuardClaudeTool, forkGuardClaudeToolFragment, forkGuardClaudeClose} {
			require.NoError(t, s.ObserveEvent("", []byte(frame)))
		}
		require.True(t, s.claudeTruncatedTool)
		s.ObserveTransport(&http.Response{StatusCode: 200}, nil)
		require.False(t, s.claudeTruncatedTool)
		require.Empty(t, s.claudeStopReason)
		require.Empty(t, s.claudeBlocks)
		require.Zero(t, s.claudeBlockBytes)
		require.Equal(t, -1, s.claudeLastIndex)
		for _, frame := range []string{forkGuardClaudeStart, forkGuardClaudeTool, forkGuardClaudeClose, `{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`, `{"type":"message_stop"}`} {
			require.NoError(t, s.ObserveEvent("", []byte(frame)))
		}
		require.True(t, s.ProtocolComplete())
	})
}

func TestForkClaudeProtocolGuardTerminalScope(t *testing.T) {
	for _, field := range []string{`"done":true`, `"is_end":true`, `"is_finished":true`, `"choices":[{"index":0,"finish_reason":"stop"}]`, `"candidates":[{"index":0,"finishReason":"STOP"}]`, `"promptFeedback":{"blockReason":"SAFETY"}`} {
		t.Run(field, func(t *testing.T) {
			s := NewStreamSession(types.RelayFormatClaude)
			require.NoError(t, s.ObserveEvent("", []byte(forkGuardClaudeStart)))
			require.NoError(t, s.ObserveEvent("", []byte(`{"type":"ping",`+field+`}`)))
			require.False(t, s.ProtocolComplete())
			s.EndRead(io.EOF)
			require.Equal(t, StreamEndReason("upstream_incomplete"), s.Snapshot().Reason)
		})
	}
}
