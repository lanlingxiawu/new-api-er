package claude

import (
	"fmt"
	"io"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Synthetic Anthropic SSE follows the official schema cited in
// fork_vendor_fault_matrix_test.go. The JSON is complete but the tool block is
// deliberately still open: parsable arguments alone do not complete a call.
const forkFinalizerTool = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_finalizer\",\"name\":\"lookup_item\",\"input\":{}}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"item\\\":\\\"alpha\\\"}\"}}\n\n"

const forkFinalizerError = "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"synthetic upstream unavailable\"}}\n\n"

func forkRunManagedFinalizer(t *testing.T, format types.RelayFormat, body string) (*dto.Usage, *relaycommon.RelayInfo, []gjson.Result) {
	t.Helper()
	c, rec, resp, info := strictTestContext(io.NopCloser(strings.NewReader(body)))
	info.RelayFormat, info.UpstreamModelName, info.OriginModelName = format, forkClaudeModel, forkClaudeModel
	info.DisablePing, info.ShouldIncludeUsage = true, true
	resp.Header.Set("Content-Type", "text/event-stream")
	service.BeginStreamAttempt(c, info)
	require.NotNil(t, info.StreamSession)
	info.StreamSession.ObserveTransport(resp, nil)
	var usage *dto.Usage
	var apiErr *types.NewAPIError
	if format == types.RelayFormatOpenAIResponses {
		usage, apiErr = ClaudeResponsesStreamHandler(c, resp, info)
	} else {
		usage, apiErr = ClaudeStreamHandler(c, resp, info)
	}
	require.Nil(t, apiErr)
	usage = service.FinalizeStreamUsage(c, info, usage)
	wire := rec.Body.String()
	require.Same(t, usage, service.FinalizeStreamUsage(c, info, usage))
	require.Equal(t, wire, rec.Body.String(), "finalization must not send another error")
	var events []gjson.Result
	for frame := range strings.SplitSeq(wire, "\n\n") {
		_, data := relaycommon.StreamFramePayload([]byte(frame))
		if len(data) != 0 && string(data) != "[DONE]" {
			require.True(t, gjson.ValidBytes(data), "downstream frame must be JSON")
			events = append(events, gjson.ParseBytes(data))
		}
	}
	return usage, info, events
}

func TestForkManagedClaudeFinalizerRejectsUnfinishedTool(t *testing.T) {
	start := forkClaudeStart(`{"input_tokens":91,"output_tokens":0,"cache_read_input_tokens":20,"cache_creation_input_tokens":10}`)
	for _, fault := range []struct{ name, tail, reason string }{
		{"upstream_error", forkFinalizerError + forkClaudeClose(1) + forkClaudeFinish("tool_use", 100), "upstream_error"},
		{"eof", "", "upstream_incomplete"},
		{"malformed_json", "event: content_block_delta\ndata: {oops}\n\n", "upstream_json_error"},
		{"invalid_sequence", forkClaudeClose(7), "upstream_protocol_error"},
	} {
		for _, format := range []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI, types.RelayFormatGemini} {

			t.Run(fmt.Sprintf("%s/%s", fault.name, format), func(t *testing.T) {
				// Both streams deliver the same reasoning; an unclosed tool must
				// not increase the settled output estimate over that baseline.
				baseline, _, _ := forkRunManagedFinalizer(t, format, start+forkClaudeThinking(0)+forkFinalizerError)
				usage, info, events := forkRunManagedFinalizer(t, format, start+forkClaudeThinking(0)+forkFinalizerTool+fault.tail)
				require.True(t, info.StreamResult.Failed)
				require.False(t, info.StreamSession.ProtocolComplete())
				require.Equal(t, fault.reason, string(info.StreamStatus.EndReason))
				errors, doneTools, reasoningDeltas := 0, 0, 0
				for _, event := range events {
					if relaycommon.IsStreamErrorEvent("", []byte(event.Raw)) {
						errors++
					}
					if event.Get("type").String() == "response.function_call_arguments.done" || event.Get("type").String() == "response.output_item.done" && event.Get("item.type").String() == "function_call" {
						doneTools++
					}
					if event.Get("type").String() == "response.reasoning_summary_text.delta" || event.Get("choices.0.delta.reasoning_content").String() != "" {
						reasoningDeltas++
					}
					require.NotEqual(t, "response.completed", event.Get("type").String())
					require.Empty(t, event.Get("candidates.0.finishReason").String())
					require.Empty(t, event.Get("choices.0.finish_reason").String())
				}
				require.Equal(t, 1, errors)

				require.Zero(t, doneTools, "an open upstream tool must not become executable")
				require.Equal(t, baseline.CompletionTokens, usage.CompletionTokens, "no unfinished tool output tokens")

				if format != types.RelayFormatGemini {
					require.Positive(t, reasoningDeltas, "already delivered reasoning remains available")
					require.Equal(t, 91, usage.PromptTokens)
					require.Equal(t, 20, usage.PromptTokensDetails.CachedTokens)
					require.Equal(t, 10, usage.PromptTokensDetails.CachedCreationTokens)
				} else {
					require.False(t, info.StreamResult.EffectiveContent, "the conversion did not deliver reasoning or a completed tool")
					require.Equal(t, "none", info.StreamResult.UsageSource)
					require.Zero(t, usage.TotalTokens)
				}

			})

		}
	}
}

func TestForkManagedClaudeFinalizerCompleteTool(t *testing.T) {
	body := forkClaudeStart(`{"input_tokens":91,"output_tokens":0}`) + forkClaudeThinking(0) + forkFinalizerTool + forkClaudeClose(1) + forkClaudeFinish("tool_use", 17)
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI, types.RelayFormatGemini} {

		t.Run(fmt.Sprintf("%s", format), func(t *testing.T) {
			usage, info, events := forkRunManagedFinalizer(t, format, body)
			require.False(t, info.StreamResult.Failed)
			require.True(t, info.StreamSession.ProtocolComplete())
			require.True(t, info.StreamResult.EffectiveContent)
			require.Equal(t, 91, usage.PromptTokens)
			require.Equal(t, 17, usage.CompletionTokens)
			complete := 0
			for _, event := range events {
				require.False(t, relaycommon.IsStreamErrorEvent("", []byte(event.Raw)))
				switch format {
				case types.RelayFormatOpenAIResponses:
					if event.Get("type").String() == "response.completed" {
						complete++
						require.Equal(t, "completed", event.Get("response.status").String())
						calls := event.Get("response.output").Array()
						require.Len(t, calls, 2)
						require.Equal(t, "lookup_item", calls[1].Get("name").String())
						require.JSONEq(t, `{"item":"alpha"}`, calls[1].Get("arguments").String())
					}
				case types.RelayFormatOpenAI:
					if event.Get("choices.0.finish_reason").String() == "tool_calls" {
						complete++
					}
				case types.RelayFormatGemini:
					if event.Get("candidates.0.finishReason").String() == "STOP" {
						complete++
						require.Equal(t, "lookup_item", event.Get("candidates.0.content.parts.0.functionCall.name").String())
						require.JSONEq(t, `{"item":"alpha"}`, event.Get("candidates.0.content.parts.0.functionCall.args").Raw)
					}
				}
			}
			require.Equal(t, 1, complete)
		})

	}
}

func TestForkManagedClaudeFinalizerExplicitZero(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI, types.RelayFormatGemini} {

		t.Run(fmt.Sprintf("%s", format), func(t *testing.T) {
			body := forkClaudeStart(`{"input_tokens":0,"output_tokens":0}`) + strings.ReplaceAll(forkFinalizerTool, `"index":1`, `"index":0`) + forkFinalizerError
			usage, info, _ := forkRunManagedFinalizer(t, format, body)
			require.True(t, info.StreamResult.Failed)
			require.Zero(t, usage.PromptTokens)

			require.False(t, info.StreamResult.EffectiveContent)
			require.Zero(t, usage.CompletionTokens)

		})

	}
}

func TestForkClaudeResponsesUnmanagedFailureRemainsIncomplete(t *testing.T) {

	t.Run("stream", func(t *testing.T) {
		body := forkClaudeStart(`{"input_tokens":91,"output_tokens":0}`) + forkClaudeThinking(0) + forkFinalizerTool + forkFinalizerError
		c, rec, resp, info := strictTestContext(io.NopCloser(strings.NewReader(body)))
		info.RelayFormat, info.UpstreamModelName, info.DisablePing = types.RelayFormatOpenAIResponses, forkClaudeModel, true
		resp.Header.Set("Content-Type", "text/event-stream")
		usage, apiErr := ClaudeResponsesStreamHandler(c, resp, info)
		require.Nil(t, apiErr)
		require.False(t, info.StreamSession.Active())
		require.Equal(t, 91, usage.PromptTokens)
		require.Zero(t, usage.CompletionTokens)
		require.NotContains(t, rec.Body.String(), "event: response.completed")
		require.Contains(t, rec.Body.String(), "event: response.failed")
		tools := 0
		for frame := range strings.SplitSeq(rec.Body.String(), "\n\n") {
			_, data := relaycommon.StreamFramePayload([]byte(frame))
			event := gjson.ParseBytes(data)
			if event.Get("type").String() == "response.output_item.done" && event.Get("item.type").String() == "function_call" {
				tools++
				require.Equal(t, "incomplete", event.Get("item.status").String())
			}
		}
		require.Equal(t, 1, tools, "unmanaged handling retains upstream FailResponsesStream behavior")
	})

}
