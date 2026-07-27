package claudemessages

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- StopReasonClaudeToOpenAI ---

func TestStopReasonMapping(t *testing.T) {
	cases := map[string]string{
		"end_turn":      "stop",
		"stop_sequence": "stop",
		"max_tokens":    "length",
		"tool_use":      "tool_calls",
		"refusal":       "content_filter",
		"weird":         "weird", // default passthrough
	}
	for in, want := range cases {
		assert.Equal(t, want, StopReasonClaudeToOpenAI(in), "reason=%s", in)
	}
}

// --- StreamResponseClaude2OpenAI ---

func TestStream_MessageStart(t *testing.T) {
	resp := &dto.ClaudeResponse{
		Type:    "message_start",
		Message: &dto.ClaudeMediaMessage{Id: "msg_1", Model: "claude-x"},
	}
	out := StreamResponseClaude2OpenAI(resp)
	require.NotNil(t, out)
	assert.Equal(t, "chat.completion.chunk", out.Object)
	assert.Equal(t, "msg_1", out.Id)
	assert.Equal(t, "claude-x", out.Model)
	require.Len(t, out.Choices, 1)
	assert.Equal(t, "assistant", out.Choices[0].Delta.Role)
	assert.Equal(t, "", out.Choices[0].Delta.GetContentString())
}

func TestStream_MessageStartNilMessage(t *testing.T) {
	resp := &dto.ClaudeResponse{Type: "message_start", Model: "top-model"}
	out := StreamResponseClaude2OpenAI(resp)
	require.NotNil(t, out)
	assert.Equal(t, "top-model", out.Model)
	assert.Equal(t, "", out.Id)
}

func TestStream_ContentBlockStartText(t *testing.T) {
	resp := &dto.ClaudeResponse{
		Type:         "content_block_start",
		ContentBlock: &dto.ClaudeMediaMessage{Type: "text", Text: strptr("hi")},
	}
	out := StreamResponseClaude2OpenAI(resp)
	require.NotNil(t, out)
	require.Len(t, out.Choices, 1)
	assert.Equal(t, "hi", out.Choices[0].Delta.GetContentString())
}

func TestStream_ContentBlockStartToolUse(t *testing.T) {
	resp := &dto.ClaudeResponse{
		Type:         "content_block_start",
		Index:        kitutil.GetPointer(2),
		ContentBlock: &dto.ClaudeMediaMessage{Type: "tool_use", Id: "toolu_5", Name: "fn"},
	}
	out := StreamResponseClaude2OpenAI(resp)
	require.NotNil(t, out)
	require.Len(t, out.Choices, 1)
	tcs := out.Choices[0].Delta.ToolCalls
	require.Len(t, tcs, 1)
	require.NotNil(t, tcs[0].Index)
	assert.Equal(t, 2, *tcs[0].Index)
	assert.Equal(t, "toolu_5", tcs[0].ID)
	assert.Equal(t, "fn", tcs[0].Function.Name)
	// Content nil'd out when tools present.
	assert.Nil(t, out.Choices[0].Delta.Content)
}

func TestStream_ContentBlockStartNilBlockReturnsNil(t *testing.T) {
	resp := &dto.ClaudeResponse{Type: "content_block_start"}
	assert.Nil(t, StreamResponseClaude2OpenAI(resp))
}

func TestStream_ContentBlockDeltaText(t *testing.T) {
	resp := &dto.ClaudeResponse{
		Type:  "content_block_delta",
		Delta: &dto.ClaudeMediaMessage{Type: "text_delta", Text: strptr("chunk")},
	}
	out := StreamResponseClaude2OpenAI(resp)
	require.NotNil(t, out)
	require.Len(t, out.Choices, 1)
	assert.Equal(t, "chunk", out.Choices[0].Delta.GetContentString())
}

func TestStream_ContentBlockDeltaInputJson(t *testing.T) {
	resp := &dto.ClaudeResponse{
		Type:  "content_block_delta",
		Index: kitutil.GetPointer(1),
		Delta: &dto.ClaudeMediaMessage{Type: "input_json_delta", PartialJson: strptr(`{"a":1}`)},
	}
	out := StreamResponseClaude2OpenAI(resp)
	require.NotNil(t, out)
	tcs := out.Choices[0].Delta.ToolCalls
	require.Len(t, tcs, 1)
	require.NotNil(t, tcs[0].Index)
	assert.Equal(t, 1, *tcs[0].Index)
	assert.Equal(t, `{"a":1}`, tcs[0].Function.Arguments)
	assert.Nil(t, out.Choices[0].Delta.Content)
}

func TestStream_ContentBlockDeltaSignature(t *testing.T) {
	resp := &dto.ClaudeResponse{
		Type:  "content_block_delta",
		Delta: &dto.ClaudeMediaMessage{Type: "signature_delta"},
	}
	out := StreamResponseClaude2OpenAI(resp)
	require.NotNil(t, out)
	require.NotNil(t, out.Choices[0].Delta.ReasoningContent)
	assert.Equal(t, "\n", *out.Choices[0].Delta.ReasoningContent)
}

func TestStream_ContentBlockDeltaThinking(t *testing.T) {
	resp := &dto.ClaudeResponse{
		Type:  "content_block_delta",
		Delta: &dto.ClaudeMediaMessage{Type: "thinking_delta", Thinking: strptr("reasoning...")},
	}
	out := StreamResponseClaude2OpenAI(resp)
	require.NotNil(t, out)
	require.NotNil(t, out.Choices[0].Delta.ReasoningContent)
	assert.Equal(t, "reasoning...", *out.Choices[0].Delta.ReasoningContent)
}

func TestStream_ContentBlockDeltaNilDelta(t *testing.T) {
	resp := &dto.ClaudeResponse{Type: "content_block_delta"}
	out := StreamResponseClaude2OpenAI(resp)
	require.NotNil(t, out)
	require.Len(t, out.Choices, 1)
	assert.Nil(t, out.Choices[0].Delta.Content)
}

func TestStream_MessageDeltaWithStopReason(t *testing.T) {
	resp := &dto.ClaudeResponse{
		Type:  "message_delta",
		Delta: &dto.ClaudeMediaMessage{StopReason: strptr("end_turn")},
	}
	out := StreamResponseClaude2OpenAI(resp)
	require.NotNil(t, out)
	require.NotNil(t, out.Choices[0].FinishReason)
	assert.Equal(t, "stop", *out.Choices[0].FinishReason)
}

func TestStream_MessageDeltaStopReasonNullStringSkipped(t *testing.T) {
	// Literal "null" maps (default passthrough) to "null" and is skipped.
	resp := &dto.ClaudeResponse{
		Type:  "message_delta",
		Delta: &dto.ClaudeMediaMessage{StopReason: strptr("null")},
	}
	out := StreamResponseClaude2OpenAI(resp)
	require.NotNil(t, out)
	assert.Nil(t, out.Choices[0].FinishReason)
}

func TestStream_MessageDeltaNilDelta(t *testing.T) {
	resp := &dto.ClaudeResponse{Type: "message_delta"}
	out := StreamResponseClaude2OpenAI(resp)
	require.NotNil(t, out)
	assert.Nil(t, out.Choices[0].FinishReason)
}

func TestStream_MessageStopReturnsNil(t *testing.T) {
	assert.Nil(t, StreamResponseClaude2OpenAI(&dto.ClaudeResponse{Type: "message_stop"}))
}

func TestStream_UnknownTypeReturnsNil(t *testing.T) {
	assert.Nil(t, StreamResponseClaude2OpenAI(&dto.ClaudeResponse{Type: "ping"}))
}

// --- ResponseClaude2OpenAI ---

func TestResponse_TextOnly(t *testing.T) {
	resp := &dto.ClaudeResponse{
		Id:         "msg_1",
		Model:      "claude-x",
		StopReason: "end_turn",
		Content: []dto.ClaudeMediaMessage{
			{Type: "text", Text: strptr("hello world")},
		},
	}
	out := ResponseClaude2OpenAI(resp)
	require.NotNil(t, out)
	assert.Equal(t, "msg_1", out.Id)
	assert.Equal(t, "claude-x", out.Model)
	assert.Equal(t, "chat.completion", out.Object)
	require.Len(t, out.Choices, 1)
	assert.Equal(t, "assistant", out.Choices[0].Role)
	assert.Equal(t, "hello world", out.Choices[0].StringContent())
	assert.Equal(t, "stop", out.Choices[0].FinishReason)
}

func TestResponse_ToolUse(t *testing.T) {
	resp := &dto.ClaudeResponse{
		Id:         "msg_2",
		StopReason: "tool_use",
		Content: []dto.ClaudeMediaMessage{
			{Type: "tool_use", Id: "toolu_1", Name: "fn", Input: map[string]any{"x": 1}},
		},
	}
	out := ResponseClaude2OpenAI(resp)
	require.NotNil(t, out)
	assert.Equal(t, "tool_calls", out.Choices[0].FinishReason)
	tcs := out.Choices[0].Message.ParseToolCalls()
	require.Len(t, tcs, 1)
	assert.Equal(t, "toolu_1", tcs[0].ID)
	assert.Equal(t, "fn", tcs[0].Function.Name)
	assert.JSONEq(t, `{"x":1}`, tcs[0].Function.Arguments)
}

func TestResponse_ThinkingBlock(t *testing.T) {
	resp := &dto.ClaudeResponse{
		Id:         "msg_3",
		StopReason: "end_turn",
		Content: []dto.ClaudeMediaMessage{
			{Type: "thinking", Thinking: strptr("deep thoughts")},
			{Type: "text", Text: strptr("answer")},
		},
	}
	out := ResponseClaude2OpenAI(resp)
	require.NotNil(t, out)
	assert.Equal(t, "answer", out.Choices[0].StringContent())
	require.NotNil(t, out.Choices[0].Message.ReasoningContent)
	assert.Equal(t, "deep thoughts", *out.Choices[0].Message.ReasoningContent)
}

func TestResponse_FirstBlockThinkingSetsResponseThinking(t *testing.T) {
	// Content[0].Thinking populates responseThinking before the loop.
	resp := &dto.ClaudeResponse{
		Id: "msg_4",
		Content: []dto.ClaudeMediaMessage{
			{Type: "text", Text: strptr("txt"), Thinking: strptr("preview")},
		},
	}
	out := ResponseClaude2OpenAI(resp)
	require.NotNil(t, out)
	require.NotNil(t, out.Choices[0].Message.ReasoningContent)
	assert.Equal(t, "preview", *out.Choices[0].Message.ReasoningContent)
}

func TestResponse_EmptyContent(t *testing.T) {
	resp := &dto.ClaudeResponse{Id: "msg_5", Model: "m", StopReason: "end_turn"}
	out := ResponseClaude2OpenAI(resp)
	require.NotNil(t, out)
	require.Len(t, out.Choices, 1)
	assert.Equal(t, "", out.Choices[0].StringContent())
	assert.Nil(t, out.Choices[0].Message.ReasoningContent)
	assert.Equal(t, "stop", out.Choices[0].FinishReason)
}

// --- UsageFromClaudeAPIUsage / UsageFromClaudeUsage math ---

func TestUsageFromClaudeAPIUsage_Nil(t *testing.T) {
	u := UsageFromClaudeAPIUsage(nil)
	require.NotNil(t, u)
	assert.Equal(t, dto.Usage{}, *u)
}

func TestUsageFromClaudeAPIUsage_NoSplit(t *testing.T) {
	in := &dto.ClaudeUsage{
		InputTokens:              100,
		CacheReadInputTokens:     20,
		CacheCreationInputTokens: 30,
		OutputTokens:             50,
	}
	u := UsageFromClaudeAPIUsage(in)
	require.NotNil(t, u)
	// total input = input(100) + cacheRead(20) + cacheCreation(30) = 150
	assert.Equal(t, 150, u.PromptTokens)
	assert.Equal(t, 150, u.InputTokens)
	assert.Equal(t, 50, u.CompletionTokens)
	assert.Equal(t, 200, u.TotalTokens)
	assert.Equal(t, 20, u.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 30, u.PromptTokensDetails.CacheWriteTokens)
	assert.Equal(t, "openai", u.UsageSemantic)
	assert.Equal(t, "anthropic", u.UsageSource)
	// NormalizeCacheCreationSplit(30,0,0) => 5m gets remainder.
	assert.Equal(t, 30, u.ClaudeCacheCreation5mTokens)
	assert.Equal(t, 0, u.ClaudeCacheCreation1hTokens)
}

func TestUsageFromClaudeAPIUsage_WithSplitCacheCreationExceedsTotal(t *testing.T) {
	// split(5m+1h)=15 > 0, and CachedCreationTokens(30) > split => cacheCreation=30
	in := &dto.ClaudeUsage{
		InputTokens:              100,
		CacheReadInputTokens:     20,
		CacheCreationInputTokens: 30,
		OutputTokens:             50,
		CacheCreation:            &dto.ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 10, Ephemeral1hInputTokens: 5},
	}
	u := UsageFromClaudeAPIUsage(in)
	require.NotNil(t, u)
	assert.Equal(t, 150, u.PromptTokens) // 100+20+30
	assert.Equal(t, 30, u.PromptTokensDetails.CacheWriteTokens)
	// Normalize(30,10,5): remainder=15 => 5m=25, 1h=5
	assert.Equal(t, 25, u.ClaudeCacheCreation5mTokens)
	assert.Equal(t, 5, u.ClaudeCacheCreation1hTokens)
}

func TestUsageFromClaudeAPIUsage_SplitUsedWhenLargerThanTotal(t *testing.T) {
	// split(25) >= CachedCreationTokens(10) => cacheCreation=split=25
	in := &dto.ClaudeUsage{
		InputTokens:              100,
		CacheReadInputTokens:     20,
		CacheCreationInputTokens: 10,
		OutputTokens:             50,
		CacheCreation:            &dto.ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 20, Ephemeral1hInputTokens: 5},
	}
	u := UsageFromClaudeAPIUsage(in)
	require.NotNil(t, u)
	// total input = 100 + 20 + 25 = 145
	assert.Equal(t, 145, u.PromptTokens)
	assert.Equal(t, 25, u.PromptTokensDetails.CacheWriteTokens)
}

func TestUsageFromClaudeUsage_Nil(t *testing.T) {
	u := UsageFromClaudeUsage(nil)
	require.NotNil(t, u)
	assert.Equal(t, dto.Usage{}, *u)
}

func TestUsageFromClaudeUsage_ZeroUsage(t *testing.T) {
	u := UsageFromClaudeUsage(&dto.Usage{})
	require.NotNil(t, u)
	assert.Equal(t, 0, u.PromptTokens)
	assert.Equal(t, 0, u.TotalTokens)
	assert.Equal(t, "openai", u.UsageSemantic)
}

// --- BuildMessageDeltaPatchUsage ---

func TestBuildMessageDeltaPatch_NilInfo(t *testing.T) {
	resp := &dto.ClaudeResponse{Usage: &dto.ClaudeUsage{OutputTokens: 5}}
	out := BuildMessageDeltaPatchUsage(resp, nil)
	require.NotNil(t, out)
	assert.Equal(t, 5, out.OutputTokens)
}

func TestBuildMessageDeltaPatch_NilInfoUsage(t *testing.T) {
	resp := &dto.ClaudeResponse{Usage: &dto.ClaudeUsage{OutputTokens: 5}}
	out := BuildMessageDeltaPatchUsage(resp, &ClaudeResponseInfo{})
	require.NotNil(t, out)
	assert.Equal(t, 5, out.OutputTokens)
}

func TestBuildMessageDeltaPatch_FillsFromInfo(t *testing.T) {
	resp := &dto.ClaudeResponse{Usage: &dto.ClaudeUsage{OutputTokens: 5}}
	info := &ClaudeResponseInfo{Usage: &dto.Usage{PromptTokens: 40}}
	info.Usage.PromptTokensDetails.CachedTokens = 8
	info.Usage.PromptTokensDetails.CachedCreationTokens = 12
	out := BuildMessageDeltaPatchUsage(resp, info)
	require.NotNil(t, out)
	assert.Equal(t, 40, out.InputTokens)
	assert.Equal(t, 8, out.CacheReadInputTokens)
	assert.Equal(t, 12, out.CacheCreationInputTokens)
}

func TestBuildMessageDeltaPatch_DoesNotOverrideExisting(t *testing.T) {
	resp := &dto.ClaudeResponse{Usage: &dto.ClaudeUsage{
		InputTokens:              99,
		CacheReadInputTokens:     7,
		CacheCreationInputTokens: 6,
	}}
	info := &ClaudeResponseInfo{Usage: &dto.Usage{PromptTokens: 40}}
	info.Usage.PromptTokensDetails.CachedTokens = 8
	info.Usage.PromptTokensDetails.CachedCreationTokens = 12
	out := BuildMessageDeltaPatchUsage(resp, info)
	require.NotNil(t, out)
	assert.Equal(t, 99, out.InputTokens)
	assert.Equal(t, 7, out.CacheReadInputTokens)
	assert.Equal(t, 6, out.CacheCreationInputTokens)
}

func TestBuildMessageDeltaPatch_CacheCreationFromInfoCreatesStruct(t *testing.T) {
	resp := &dto.ClaudeResponse{Usage: &dto.ClaudeUsage{CacheCreationInputTokens: 30}}
	info := &ClaudeResponseInfo{Usage: &dto.Usage{
		ClaudeCacheCreation5mTokens: 10,
		ClaudeCacheCreation1hTokens: 5,
	}}
	out := BuildMessageDeltaPatchUsage(resp, info)
	require.NotNil(t, out)
	require.NotNil(t, out.CacheCreation)
	// Normalize(30,10,5): remainder=15 => 5m=25, 1h=5
	assert.Equal(t, 25, out.CacheCreation.Ephemeral5mInputTokens)
	assert.Equal(t, 5, out.CacheCreation.Ephemeral1hInputTokens)
}

func TestBuildMessageDeltaPatch_ExistingCacheCreationUsed(t *testing.T) {
	resp := &dto.ClaudeResponse{Usage: &dto.ClaudeUsage{
		CacheCreationInputTokens: 30,
		CacheCreation:            &dto.ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 12, Ephemeral1hInputTokens: 3},
	}}
	info := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	out := BuildMessageDeltaPatchUsage(resp, info)
	require.NotNil(t, out)
	require.NotNil(t, out.CacheCreation)
	// Normalize(30,12,3): remainder=15 => 5m=27, 1h=3
	assert.Equal(t, 27, out.CacheCreation.Ephemeral5mInputTokens)
	assert.Equal(t, 3, out.CacheCreation.Ephemeral1hInputTokens)
}

func TestBuildMessageDeltaPatch_NoCacheCreationStructWhenZero(t *testing.T) {
	resp := &dto.ClaudeResponse{Usage: &dto.ClaudeUsage{OutputTokens: 1}}
	info := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	out := BuildMessageDeltaPatchUsage(resp, info)
	require.NotNil(t, out)
	assert.Nil(t, out.CacheCreation)
}

func TestBuildMessageDeltaPatch_NilResponseUsage(t *testing.T) {
	resp := &dto.ClaudeResponse{}
	info := &ClaudeResponseInfo{Usage: &dto.Usage{PromptTokens: 11}}
	out := BuildMessageDeltaPatchUsage(resp, info)
	require.NotNil(t, out)
	assert.Equal(t, 11, out.InputTokens)
}

// --- PatchClaudeMessageDeltaUsageData / setMessageDeltaUsageInt ---

func TestPatch_EmptyData(t *testing.T) {
	assert.Equal(t, "", PatchClaudeMessageDeltaUsageData("", &dto.ClaudeUsage{InputTokens: 5}))
}

func TestPatch_NilUsage(t *testing.T) {
	assert.Equal(t, `{"a":1}`, PatchClaudeMessageDeltaUsageData(`{"a":1}`, nil))
}

func TestPatch_SetsMissingFields(t *testing.T) {
	data := `{"type":"message_delta","usage":{}}`
	usage := &dto.ClaudeUsage{
		InputTokens:              100,
		CacheReadInputTokens:     20,
		CacheCreationInputTokens: 30,
		CacheCreation:            &dto.ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 25, Ephemeral1hInputTokens: 5},
	}
	out := PatchClaudeMessageDeltaUsageData(data, usage)
	assert.JSONEq(t, `{"type":"message_delta","usage":{"input_tokens":100,"cache_read_input_tokens":20,"cache_creation_input_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":25,"ephemeral_1h_input_tokens":5}}}`, out)
}

func TestPatch_DoesNotOverwriteExistingPositive(t *testing.T) {
	data := `{"usage":{"input_tokens":999}}`
	out := PatchClaudeMessageDeltaUsageData(data, &dto.ClaudeUsage{InputTokens: 100})
	assert.JSONEq(t, `{"usage":{"input_tokens":999}}`, out)
}

func TestPatch_SkipsNonPositiveLocalValues(t *testing.T) {
	data := `{"usage":{}}`
	out := PatchClaudeMessageDeltaUsageData(data, &dto.ClaudeUsage{InputTokens: 0})
	assert.JSONEq(t, `{"usage":{}}`, out)
}

func TestPatch_NoCacheCreationBlockWhenNil(t *testing.T) {
	data := `{"usage":{}}`
	out := PatchClaudeMessageDeltaUsageData(data, &dto.ClaudeUsage{InputTokens: 10})
	assert.JSONEq(t, `{"usage":{"input_tokens":10}}`, out)
}

func TestSetMessageDeltaUsageInt_OverwritesZeroUpstream(t *testing.T) {
	// upstream present but 0 => patched.
	out := setMessageDeltaUsageInt(`{"usage":{"input_tokens":0}}`, "usage.input_tokens", 50)
	assert.JSONEq(t, `{"usage":{"input_tokens":50}}`, out)
}

func TestSetMessageDeltaUsageInt_SjsonErrorReturnsData(t *testing.T) {
	// An empty path makes sjson.Set fail; the original data is returned unchanged.
	assert.Equal(t, `{"usage":{}}`, setMessageDeltaUsageInt(`{"usage":{}}`, "", 5))
}

// --- unexported helper nil-guards (unreachable via public callers) ---

func TestCacheCreationTokensForOpenAIUsage_Nil(t *testing.T) {
	assert.Equal(t, 0, cacheCreationTokensForOpenAIUsage(nil))
}

func TestClaudeBillingUsageFromSemanticUsage_Nil(t *testing.T) {
	assert.Nil(t, claudeBillingUsageFromSemanticUsage(nil))
}

// --- FormatClaudeResponseInfo ---

func TestFormat_NilInfo(t *testing.T) {
	assert.False(t, FormatClaudeResponseInfo(&dto.ClaudeResponse{Type: "message_start"}, nil, nil))
}

func TestFormat_MessageStartInitsUsageAndIds(t *testing.T) {
	info := &ClaudeResponseInfo{}
	resp := &dto.ClaudeResponse{
		Type: "message_start",
		Message: &dto.ClaudeMediaMessage{
			Id:    "msg_1",
			Model: "claude-x",
			Usage: &dto.ClaudeUsage{InputTokens: 100, CacheReadInputTokens: 20, CacheCreationInputTokens: 30, OutputTokens: 5},
		},
	}
	oai := &dto.ChatCompletionsStreamResponse{}
	ok := FormatClaudeResponseInfo(resp, oai, info)
	assert.True(t, ok)
	assert.Equal(t, "msg_1", info.ResponseId)
	assert.Equal(t, "claude-x", info.Model)
	require.NotNil(t, info.Usage)
	assert.Equal(t, 100, info.Usage.PromptTokens)
	assert.Equal(t, 20, info.Usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 30, info.Usage.PromptTokensDetails.CachedCreationTokens)
	assert.Equal(t, 5, info.Usage.CompletionTokens)
	assert.Equal(t, "anthropic", info.Usage.UsageSemantic)
	// oaiResponse patched from info.
	assert.Equal(t, "msg_1", oai.Id)
	assert.Equal(t, "claude-x", oai.Model)
}

func TestFormat_MessageStartNilMessageUsage(t *testing.T) {
	info := &ClaudeResponseInfo{}
	resp := &dto.ClaudeResponse{Type: "message_start", Message: &dto.ClaudeMediaMessage{Id: "m", Model: "x"}}
	ok := FormatClaudeResponseInfo(resp, nil, info)
	assert.True(t, ok)
	assert.Equal(t, "m", info.ResponseId)
	assert.Equal(t, 0, info.Usage.PromptTokens)
}

func TestFormat_ContentBlockDeltaAppendsText(t *testing.T) {
	info := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	FormatClaudeResponseInfo(&dto.ClaudeResponse{
		Type:  "content_block_delta",
		Delta: &dto.ClaudeMediaMessage{Text: strptr("abc")},
	}, nil, info)
	FormatClaudeResponseInfo(&dto.ClaudeResponse{
		Type:  "content_block_delta",
		Delta: &dto.ClaudeMediaMessage{Thinking: strptr("def")},
	}, nil, info)
	assert.Equal(t, "abcdef", info.ResponseText.String())
}

func TestFormat_ContentBlockDeltaNilDelta(t *testing.T) {
	info := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	ok := FormatClaudeResponseInfo(&dto.ClaudeResponse{Type: "content_block_delta"}, nil, info)
	assert.True(t, ok)
	assert.Equal(t, "", info.ResponseText.String())
}

func TestFormat_MessageDeltaSetsUsageAndDone(t *testing.T) {
	info := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	resp := &dto.ClaudeResponse{
		Type: "message_delta",
		Usage: &dto.ClaudeUsage{
			InputTokens:              100,
			CacheReadInputTokens:     20,
			CacheCreationInputTokens: 30,
			OutputTokens:             40,
			CacheCreation:            &dto.ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 25, Ephemeral1hInputTokens: 5},
		},
	}
	ok := FormatClaudeResponseInfo(resp, nil, info)
	assert.True(t, ok)
	assert.True(t, info.Done)
	assert.Equal(t, 100, info.Usage.PromptTokens)
	assert.Equal(t, 40, info.Usage.CompletionTokens)
	assert.Equal(t, 140, info.Usage.TotalTokens)
	assert.Equal(t, 25, info.Usage.ClaudeCacheCreation5mTokens)
	assert.Equal(t, 5, info.Usage.ClaudeCacheCreation1hTokens)
}

func TestFormat_MessageDeltaNilUsageStillDone(t *testing.T) {
	info := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	ok := FormatClaudeResponseInfo(&dto.ClaudeResponse{Type: "message_delta"}, nil, info)
	assert.True(t, ok)
	assert.True(t, info.Done)
}

func TestFormat_ContentBlockStartNoop(t *testing.T) {
	info := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	ok := FormatClaudeResponseInfo(&dto.ClaudeResponse{Type: "content_block_start"}, nil, info)
	assert.True(t, ok)
}

func TestFormat_UnknownTypeReturnsFalse(t *testing.T) {
	info := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	ok := FormatClaudeResponseInfo(&dto.ClaudeResponse{Type: "ping"}, nil, info)
	assert.False(t, ok)
}

func TestFormat_NilUsageIsInitialized(t *testing.T) {
	info := &ClaudeResponseInfo{}
	FormatClaudeResponseInfo(&dto.ClaudeResponse{Type: "content_block_start"}, nil, info)
	assert.NotNil(t, info.Usage)
}
