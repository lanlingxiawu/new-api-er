package oaichat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// --- NormalizeCacheCreationSplit -------------------------------------------

func TestNormalizeCacheCreationSplit(t *testing.T) {
	tests := []struct {
		name                   string
		total, t5m, t1h        int
		want5m, want1h         int
	}{
		{name: "positive remainder folds into 5m", total: 10, t5m: 3, t1h: 2, want5m: 8, want1h: 2},
		{name: "negative remainder clamped to zero", total: 3, t5m: 5, t1h: 1, want5m: 5, want1h: 1},
		{name: "exact split no remainder", total: 5, t5m: 3, t1h: 2, want5m: 3, want1h: 2},
		{name: "all zero", total: 0, t5m: 0, t1h: 0, want5m: 0, want1h: 0},
		{name: "only total", total: 7, t5m: 0, t1h: 0, want5m: 7, want1h: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g5, g1 := NormalizeCacheCreationSplit(tt.total, tt.t5m, tt.t1h)
			assert.Equal(t, tt.want5m, g5)
			assert.Equal(t, tt.want1h, g1)
		})
	}
}

// --- buildClaudeUsageFromOpenAIUsage ---------------------------------------

func TestBuildClaudeUsage_Nil(t *testing.T) {
	assert.Nil(t, buildClaudeUsageFromOpenAIUsage(nil))
}

func TestBuildClaudeUsage_Basic(t *testing.T) {
	u := buildClaudeUsageFromOpenAIUsage(&dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 20,
		TotalTokens:      120,
	})
	require.NotNil(t, u)
	assert.Equal(t, 100, u.InputTokens)
	assert.Equal(t, 20, u.OutputTokens)
	assert.Equal(t, 0, u.CacheReadInputTokens)
	assert.Equal(t, 0, u.CacheCreationInputTokens)
	assert.Nil(t, u.CacheCreation)
	require.NotNil(t, u.BillingUsage)
	assert.Equal(t, dto.BillingUsageSemanticOpenAI, u.BillingUsage.Semantic)
}

func TestBuildClaudeUsage_CacheWriteClampsNegativeInput(t *testing.T) {
	// PromptTokens - cached - cacheCreation goes negative; clamp to 0.
	u := buildClaudeUsageFromOpenAIUsage(&dto.Usage{
		PromptTokens:     3619,
		CompletionTokens: 36,
		TotalTokens:      3655,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:     2921,
			CacheWriteTokens: 3616,
		},
	})
	require.NotNil(t, u)
	assert.Equal(t, 0, u.InputTokens)
	assert.Equal(t, 2921, u.CacheReadInputTokens)
	assert.Equal(t, 3616, u.CacheCreationInputTokens)
	assert.Equal(t, 36, u.OutputTokens)
}

func TestBuildClaudeUsage_CacheWritePositiveRemainder(t *testing.T) {
	u := buildClaudeUsageFromOpenAIUsage(&dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 10,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:     100,
			CacheWriteTokens: 200,
		},
	})
	require.NotNil(t, u)
	// 1000 - 100 - 200 = 700
	assert.Equal(t, 700, u.InputTokens)
	assert.Equal(t, 100, u.CacheReadInputTokens)
	assert.Equal(t, 200, u.CacheCreationInputTokens)
}

func TestBuildClaudeUsage_CacheCreationSplitPopulated(t *testing.T) {
	u := buildClaudeUsageFromOpenAIUsage(&dto.Usage{
		PromptTokens:     500,
		CompletionTokens: 10,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 30,
		},
		ClaudeCacheCreation5mTokens: 10,
		ClaudeCacheCreation1hTokens: 5,
	})
	require.NotNil(t, u)
	require.NotNil(t, u.CacheCreation)
	// remainder = 30 - 10 - 5 = 15 -> 5m = 10 + 15 = 25, 1h = 5
	assert.Equal(t, 25, u.CacheCreation.Ephemeral5mInputTokens)
	assert.Equal(t, 5, u.CacheCreation.Ephemeral1hInputTokens)
}

func TestBuildClaudeUsage_PrefersExistingClaudeBillingUsage(t *testing.T) {
	claudeUsage := &dto.ClaudeUsage{InputTokens: 42, OutputTokens: 7}
	src := &dto.Usage{
		PromptTokens: 1, CompletionTokens: 1,
		BillingUsage: &dto.BillingUsage{
			Source:      dto.BillingUsageSourceClaudeMessages,
			Semantic:    dto.BillingUsageSemanticAnthropic,
			ClaudeUsage: claudeUsage,
		},
	}
	u := buildClaudeUsageFromOpenAIUsage(src)
	require.NotNil(t, u)
	// Returned directly from the pre-existing Anthropic billing usage.
	assert.Equal(t, 42, u.InputTokens)
	assert.Equal(t, 7, u.OutputTokens)
}

func TestBuildClaudeUsage_ReusesOpenAIBillingUsage(t *testing.T) {
	existing := dto.NewOpenAIChatBillingUsage(&dto.Usage{PromptTokens: 9, CompletionTokens: 3})
	src := &dto.Usage{
		PromptTokens:     50,
		CompletionTokens: 8,
		BillingUsage:     existing,
	}
	u := buildClaudeUsageFromOpenAIUsage(src)
	require.NotNil(t, u)
	require.NotNil(t, u.BillingUsage)
	require.NotNil(t, u.BillingUsage.OpenAIUsage)
	// The pre-existing OpenAI billing usage (9/3) is reused rather than rebuilt.
	assert.Equal(t, 9, u.BillingUsage.OpenAIUsage.PromptTokens)
	assert.Equal(t, 3, u.BillingUsage.OpenAIUsage.CompletionTokens)
}

// --- ResponseOpenAI2Claude (non-stream) ------------------------------------

func TestResponseOpenAI2Claude_TextOnly(t *testing.T) {
	resp := ResponseOpenAI2Claude(&dto.OpenAITextResponse{
		Id:    "id1",
		Model: "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{
			{Message: dto.Message{Role: "assistant", Content: "hello"}, FinishReason: "stop"},
		},
		Usage: dto.Usage{PromptTokens: 11, CompletionTokens: 5, TotalTokens: 16},
	}, nil)

	assert.Equal(t, "message", resp.Type)
	assert.Equal(t, "assistant", resp.Role)
	assert.Equal(t, "end_turn", resp.StopReason)
	require.Len(t, resp.Content, 1)
	assert.Equal(t, "text", resp.Content[0].Type)
	assert.Equal(t, "hello", resp.Content[0].GetText())
	require.NotNil(t, resp.Usage)
	assert.Equal(t, 11, resp.Usage.InputTokens)
	assert.Equal(t, 5, resp.Usage.OutputTokens)
}

func TestResponseOpenAI2Claude_ToolUseInputVariants(t *testing.T) {
	tests := []struct {
		name string
		args string
		want map[string]interface{}
	}{
		{name: "object", args: `{"q":"x"}`, want: map[string]interface{}{"q": "x"}},
		{name: "empty", args: "", want: map[string]interface{}{}},
		{name: "invalid", args: "{", want: map[string]interface{}{}},
		{name: "null", args: "null", want: map[string]interface{}{}},
		{name: "array", args: `["x"]`, want: map[string]interface{}{}},
		{name: "whitespace", args: "   ", want: map[string]interface{}{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := dto.Message{Role: "assistant"}
			msg.SetToolCalls([]dto.ToolCallRequest{
				{ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: tt.args}},
			})
			resp := ResponseOpenAI2Claude(&dto.OpenAITextResponse{
				Id:    "id1",
				Model: "gpt-test",
				Choices: []dto.OpenAITextResponseChoice{
					{Message: msg, FinishReason: "tool_calls"},
				},
			}, nil)
			// No text content -> only the tool_use block (empty text with no
			// tool calls path is not taken because there IS a tool call).
			require.Len(t, resp.Content, 1)
			assert.Equal(t, "tool_use", resp.Content[0].Type)
			assert.Equal(t, tt.want, resp.Content[0].Input)
			assert.Equal(t, "tool_use", resp.StopReason)
		})
	}
}

func TestResponseOpenAI2Claude_TextAndToolTogether(t *testing.T) {
	msg := dto.Message{Role: "assistant", Content: "let me check"}
	msg.SetToolCalls([]dto.ToolCallRequest{
		{ID: "c1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: `{"q":"x"}`}},
	})
	resp := ResponseOpenAI2Claude(&dto.OpenAITextResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{{Message: msg, FinishReason: "tool_calls"}},
	}, nil)
	require.Len(t, resp.Content, 2)
	assert.Equal(t, "text", resp.Content[0].Type)
	assert.Equal(t, "tool_use", resp.Content[1].Type)
}

func TestResponseOpenAI2Claude_EmptyMessageStillEmitsTextBlock(t *testing.T) {
	// No text and no tool calls -> the (len(toolCalls)==0) branch emits an
	// empty text block.
	resp := ResponseOpenAI2Claude(&dto.OpenAITextResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{
			{Message: dto.Message{Role: "assistant", Content: ""}, FinishReason: "stop"},
		},
	}, nil)
	require.Len(t, resp.Content, 1)
	assert.Equal(t, "text", resp.Content[0].Type)
	assert.Equal(t, "", resp.Content[0].GetText())
}

// --- stopReasonOpenAI2Claude -----------------------------------------------

func TestStopReasonOpenAI2Claude(t *testing.T) {
	cases := map[string]string{
		"stop":           "end_turn",
		"length":         "max_tokens",
		"tool_calls":     "tool_use",
		"content_filter": "refusal",
		"weird":          "weird", // pass-through default
	}
	for in, want := range cases {
		assert.Equal(t, want, stopReasonOpenAI2Claude(in), "input=%s", in)
	}
}

// --- generateStopBlock -----------------------------------------------------

func TestGenerateStopBlock(t *testing.T) {
	b := generateStopBlock(4)
	assert.Equal(t, "content_block_stop", b.Type)
	assert.Equal(t, 4, b.GetIndex())
}

// --- StreamResponseOpenAI2Claude -------------------------------------------

func newClaudeStreamInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{
			LastMessagesType: relaycommon.LastMessageTypeNone,
		},
	}
}

func streamChunkContent(content string) *dto.ChatCompletionsStreamResponse {
	return &dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: ptr(content)}},
		},
	}
}

func TestStreamClaude_NilInfoInitializes(t *testing.T) {
	// Passing nil info must not panic; it lazily builds the convert state.
	out := StreamResponseOpenAI2Claude(streamChunkContent("hi"), nil)
	require.NotEmpty(t, out)
	assert.Equal(t, "message_start", out[0].Type)
}

func TestStreamClaude_DoneShortCircuits(t *testing.T) {
	info := newClaudeStreamInfo()
	info.ClaudeConvertInfo.Done = true
	assert.Nil(t, StreamResponseOpenAI2Claude(streamChunkContent("hi"), info))
}

func TestStreamClaude_MessageStartWithTextThenThinkingThenToolThenFinish(t *testing.T) {
	info := newClaudeStreamInfo()

	// First chunk: message_start + text block start + delta.
	textResp := StreamResponseOpenAI2Claude(streamChunkContent("hello"), info)
	require.Len(t, textResp, 3)
	assert.Equal(t, "message_start", textResp[0].Type)
	assert.Equal(t, "content_block_start", textResp[1].Type)
	assert.Equal(t, 0, textResp[1].GetIndex())
	assert.Equal(t, "text", textResp[1].ContentBlock.Type)
	assert.Equal(t, "content_block_delta", textResp[2].Type)

	// Second chunk: reasoning -> stop text, advance, start thinking.
	thinkResp := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr("thinking")}},
		},
	}, info)
	require.Len(t, thinkResp, 3)
	assert.Equal(t, "content_block_stop", thinkResp[0].Type)
	assert.Equal(t, 0, thinkResp[0].GetIndex())
	assert.Equal(t, "content_block_start", thinkResp[1].Type)
	assert.Equal(t, 1, thinkResp[1].GetIndex())
	assert.Equal(t, "thinking", thinkResp[1].ContentBlock.Type)
	assert.Equal(t, "content_block_delta", thinkResp[2].Type)

	// Third chunk: tool call -> stop thinking, advance, start tool_use.
	toolResp := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
				{Index: ptr(0), ID: "call_1", Type: "function", Function: dto.FunctionResponse{Name: "lookup", Arguments: `{"q":"x"}`}},
			}}},
		},
	}, info)
	require.Len(t, toolResp, 3)
	assert.Equal(t, "content_block_stop", toolResp[0].Type)
	assert.Equal(t, 1, toolResp[0].GetIndex())
	assert.Equal(t, "content_block_start", toolResp[1].Type)
	assert.Equal(t, 2, toolResp[1].GetIndex())
	assert.Equal(t, "tool_use", toolResp[1].ContentBlock.Type)
	assert.Equal(t, "content_block_delta", toolResp[2].Type)

	// Fourth chunk: finish_reason + usage -> stop, message_delta, message_stop.
	finishResp := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{{FinishReason: ptr("tool_calls")}},
		Usage:   &dto.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10},
	}, info)
	require.Len(t, finishResp, 3)
	assert.Equal(t, "content_block_stop", finishResp[0].Type)
	assert.Equal(t, 2, finishResp[0].GetIndex())
	assert.Equal(t, "message_delta", finishResp[1].Type)
	assert.Equal(t, "tool_use", *finishResp[1].Delta.StopReason)
	require.NotNil(t, finishResp[1].Usage)
	assert.Equal(t, "message_stop", finishResp[2].Type)
	assert.True(t, info.ClaudeConvertInfo.Done)
}

func TestStreamClaude_MessageStartToolCallFirstChunk(t *testing.T) {
	// First chunk is itself a tool call: message_start then content_block_start
	// (tool_use) at index 0, plus the input_json_delta for its first args.
	info := newClaudeStreamInfo()
	out := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
				{Index: ptr(0), ID: "call_1", Type: "function", Function: dto.FunctionResponse{Name: "lookup", Arguments: `{"q":`}},
			}}},
		},
	}, info)
	require.Len(t, out, 3)
	assert.Equal(t, "message_start", out[0].Type)
	assert.Equal(t, "content_block_start", out[1].Type)
	assert.Equal(t, "tool_use", out[1].ContentBlock.Type)
	assert.Equal(t, "content_block_delta", out[2].Type)
	assert.Equal(t, "input_json_delta", out[2].Delta.Type)
}

func TestStreamClaude_MessageStartToolCallNoDeltaFallbackFirstToolCall(t *testing.T) {
	// IsToolCall true but Choices[0].Delta has no tool calls exposed the way the
	// primary path expects -> GetFirstToolCall fallback. Simulate by using a
	// tool call with empty arguments so no input_json_delta is appended.
	info := newClaudeStreamInfo()
	out := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
				{Index: ptr(0), ID: "call_1", Type: "function", Function: dto.FunctionResponse{Name: "lookup", Arguments: ""}},
			}}},
		},
	}, info)
	require.Len(t, out, 2)
	assert.Equal(t, "message_start", out[0].Type)
	assert.Equal(t, "content_block_start", out[1].Type)
}

func TestStreamClaude_FirstChunkReasoningThenFinish(t *testing.T) {
	// message_start chunk that also carries reasoning content AND finish reason.
	info := newClaudeStreamInfo()
	out := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta:        dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr("hmm")},
				FinishReason: ptr("stop"),
			},
		},
		Usage: &dto.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}, info)
	// message_start, thinking start, thinking delta, content_block_stop,
	// message_delta, message_stop.
	require.GreaterOrEqual(t, len(out), 5)
	assert.Equal(t, "message_start", out[0].Type)
	assert.Equal(t, "message_stop", out[len(out)-1].Type)
	assert.True(t, info.ClaudeConvertInfo.Done)
}

func TestStreamClaude_UsageOnlyChunkAfterStart(t *testing.T) {
	info := newClaudeStreamInfo()
	// prime message_start with text
	StreamResponseOpenAI2Claude(streamChunkContent("hi"), info)
	// usage-only chunk (no choices)
	out := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Usage: &dto.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
	}, info)
	require.NotEmpty(t, out)
	// content_block_stop for open text, then message_delta (end_turn default), message_stop
	assert.Equal(t, "content_block_stop", out[0].Type)
	assert.Equal(t, "message_delta", out[1].Type)
	assert.Equal(t, "end_turn", *out[1].Delta.StopReason)
	assert.Equal(t, "message_stop", out[2].Type)
	assert.True(t, info.ClaudeConvertInfo.Done)
}

func TestStreamClaude_FinishReasonDeferredUntilUsage(t *testing.T) {
	info := newClaudeStreamInfo()
	StreamResponseOpenAI2Claude(streamChunkContent("hi"), info)
	// finish_reason chunk with NO usage -> deferred, returns empty (waiting).
	out := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{{FinishReason: ptr("stop")}},
	}, info)
	assert.Empty(t, out)
	assert.Equal(t, "stop", info.FinishReason)
	assert.False(t, info.ClaudeConvertInfo.Done)

	// Follow-up usage-only chunk finalizes.
	out2 := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Usage: &dto.Usage{PromptTokens: 3, CompletionTokens: 1, TotalTokens: 4},
	}, info)
	require.NotEmpty(t, out2)
	assert.Equal(t, "message_stop", out2[len(out2)-1].Type)
}

func TestStreamClaude_ParallelToolCallsMultipleBlocks(t *testing.T) {
	info := newClaudeStreamInfo()
	// message_start with text first.
	StreamResponseOpenAI2Claude(streamChunkContent("hi"), info)
	// Now a chunk with two parallel tool calls at offsets 0 and 1.
	out := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
				{Index: ptr(0), ID: "call_a", Type: "function", Function: dto.FunctionResponse{Name: "a", Arguments: `{"x":1}`}},
				{Index: ptr(1), ID: "call_b", Type: "function", Function: dto.FunctionResponse{Name: "b", Arguments: `{"y":2}`}},
			}}},
		},
	}, info)
	// Expect: content_block_stop (text idx0), start a, delta a, start b, delta b.
	require.GreaterOrEqual(t, len(out), 5)
	assert.Equal(t, "content_block_stop", out[0].Type)
	assert.Equal(t, relaycommon.LastMessageTypeTools, info.ClaudeConvertInfo.LastMessagesType)
	assert.Equal(t, 1, info.ClaudeConvertInfo.ToolCallMaxIndexOffset)

	// Finalize with usage: two stop blocks for the two open tool blocks.
	fin := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{{FinishReason: ptr("tool_calls")}},
		Usage:   &dto.Usage{PromptTokens: 4, CompletionTokens: 4, TotalTokens: 8},
	}, info)
	stops := 0
	for _, r := range fin {
		if r.Type == "content_block_stop" {
			stops++
		}
	}
	assert.Equal(t, 2, stops)
}

func TestStreamClaude_ToolCallWithoutExplicitIndexUsesLoopIndex(t *testing.T) {
	info := newClaudeStreamInfo()
	StreamResponseOpenAI2Claude(streamChunkContent("hi"), info)
	out := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
				{ID: "call_a", Type: "function", Function: dto.FunctionResponse{Name: "a", Arguments: `{"x":1}`}},
			}}},
		},
	}, info)
	require.NotEmpty(t, out)
	assert.Equal(t, relaycommon.LastMessageTypeTools, info.ClaudeConvertInfo.LastMessagesType)
}

func TestStreamClaude_EmptyDeltaProducesNoContentBlock(t *testing.T) {
	info := newClaudeStreamInfo()
	StreamResponseOpenAI2Claude(streamChunkContent("hi"), info)
	// A subsequent chunk with empty content and empty reasoning -> isEmpty path,
	// no content_block_delta appended.
	out := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: ptr("")}},
		},
	}, info)
	assert.Empty(t, out)
}

func TestStreamClaude_UsageFromConvertInfoFallbackOnNoChoicesChunk(t *testing.T) {
	info := newClaudeStreamInfo()
	// Seed usage into ClaudeConvertInfo so a usage-less, choice-less chunk can
	// finalize from the fallback.
	info.ClaudeConvertInfo.Usage = &dto.Usage{PromptTokens: 6, CompletionTokens: 2, TotalTokens: 8}
	StreamResponseOpenAI2Claude(streamChunkContent("hi"), info)
	// A chunk with no choices and no chunk-level usage: the len(choices)==0
	// branch falls back to ClaudeConvertInfo.Usage and finalizes.
	out := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
	}, info)
	require.NotEmpty(t, out)
	assert.Equal(t, "message_stop", out[len(out)-1].Type)
	assert.True(t, info.ClaudeConvertInfo.Done)
}

func TestStreamClaude_ElseBranchFinishAlwaysDefersWhenChunkUsageNil(t *testing.T) {
	info := newClaudeStreamInfo()
	// Even with ConvertInfo.Usage set, a finish chunk that carries choices but no
	// chunk-level usage defers (returns empty) — the fallback only applies to the
	// no-choices / message_start finalization paths.
	info.ClaudeConvertInfo.Usage = &dto.Usage{PromptTokens: 6, CompletionTokens: 2, TotalTokens: 8}
	StreamResponseOpenAI2Claude(streamChunkContent("hi"), info)
	out := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{{FinishReason: ptr("stop")}},
	}, info)
	assert.Empty(t, out)
	assert.False(t, info.ClaudeConvertInfo.Done)
}

// --- supplementary branch coverage -----------------------------------------

func TestStreamClaude_MainPathTextAfterThinking(t *testing.T) {
	// message_start via reasoning, then a plain text delta on the main path
	// (exercises the main-path text branch that starts a new text block).
	info := newClaudeStreamInfo()
	StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr("t")}},
		},
	}, info)
	out := StreamResponseOpenAI2Claude(streamChunkContent("body"), info)
	// content_block_stop (thinking), content_block_start (text), content_block_delta
	require.GreaterOrEqual(t, len(out), 3)
	assert.Equal(t, "content_block_stop", out[0].Type)
	assert.Equal(t, "content_block_start", out[1].Type)
	assert.Equal(t, "text", out[1].ContentBlock.Type)
	assert.Equal(t, relaycommon.LastMessageTypeText, info.ClaudeConvertInfo.LastMessagesType)
}

func TestStreamClaude_MainPathThinkingAfterText(t *testing.T) {
	// message_start via text, then a reasoning delta on the main path.
	info := newClaudeStreamInfo()
	StreamResponseOpenAI2Claude(streamChunkContent("hi"), info)
	out := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr("why")}},
		},
	}, info)
	require.GreaterOrEqual(t, len(out), 3)
	assert.Equal(t, "content_block_stop", out[0].Type)
	assert.Equal(t, "content_block_start", out[1].Type)
	assert.Equal(t, "thinking", out[1].ContentBlock.Type)
}

func TestStreamClaude_TextThenToolThenTextClosesToolGroup(t *testing.T) {
	// text -> tool (main path) -> text: the last text closes the open tool
	// group via stopOpenBlocksAndAdvance's tool branch.
	info := newClaudeStreamInfo()
	StreamResponseOpenAI2Claude(streamChunkContent("hi"), info)
	StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
				{Index: ptr(0), ID: "c1", Type: "function", Function: dto.FunctionResponse{Name: "a", Arguments: `{"x":1}`}},
			}}},
		},
	}, info)
	require.Equal(t, relaycommon.LastMessageTypeTools, info.ClaudeConvertInfo.LastMessagesType)
	out := StreamResponseOpenAI2Claude(streamChunkContent("more"), info)
	assert.Equal(t, "content_block_stop", out[0].Type)
	assert.Equal(t, relaycommon.LastMessageTypeText, info.ClaudeConvertInfo.LastMessagesType)
}

func TestStreamClaude_FirstChunkContentAndFinishNoUsage(t *testing.T) {
	// message_start chunk carrying content AND a finish reason but no usage:
	// no message_delta is emitted (usage nil) yet message_stop still closes it.
	info := newClaudeStreamInfo()
	out := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "id1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta:        dto.ChatCompletionsStreamResponseChoiceDelta{Content: ptr("done")},
				FinishReason: ptr("stop"),
			},
		},
	}, info)
	require.NotEmpty(t, out)
	assert.Equal(t, "message_stop", out[len(out)-1].Type)
	for _, r := range out {
		assert.NotEqual(t, "message_delta", r.Type, "no usage -> no message_delta")
	}
	assert.True(t, info.ClaudeConvertInfo.Done)
}
