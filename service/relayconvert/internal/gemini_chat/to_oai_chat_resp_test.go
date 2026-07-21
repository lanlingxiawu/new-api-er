package geminichat

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sp(s string) *string { return &s }

// --- UsageFromGeminiMetadata ---

func TestUsage_NilMetadataNoFallback(t *testing.T) {
	assert.Nil(t, UsageFromGeminiMetadata(nil, 0))
}

func TestUsage_NilMetadataWithFallback(t *testing.T) {
	u := UsageFromGeminiMetadata(nil, 42)
	require.NotNil(t, u)
	assert.Equal(t, 42, u.PromptTokens)
	assert.Equal(t, 42, u.PromptTokensDetails.TextTokens)
}

func TestUsage_Basic(t *testing.T) {
	md := &dto.GeminiUsageMetadata{
		PromptTokenCount:     100,
		CandidatesTokenCount: 30,
		ThoughtsTokenCount:   10,
		TotalTokenCount:      140,
	}
	u := UsageFromGeminiMetadata(md, 0)
	require.NotNil(t, u)
	assert.Equal(t, 100, u.PromptTokens)
	assert.Equal(t, 40, u.CompletionTokens) // candidates + thoughts
	assert.Equal(t, 140, u.TotalTokens)
	assert.Equal(t, 10, u.CompletionTokenDetails.ReasoningTokens)
	// prompt>0 and no text/audio details => TextTokens defaulted to prompt.
	assert.Equal(t, 100, u.PromptTokensDetails.TextTokens)
}

func TestUsage_ToolUsePromptAdded(t *testing.T) {
	md := &dto.GeminiUsageMetadata{
		PromptTokenCount:        50,
		ToolUsePromptTokenCount: 25,
		CandidatesTokenCount:    5,
	}
	u := UsageFromGeminiMetadata(md, 0)
	require.NotNil(t, u)
	assert.Equal(t, 75, u.PromptTokens)
}

func TestUsage_ZeroPromptUsesFallback(t *testing.T) {
	md := &dto.GeminiUsageMetadata{CandidatesTokenCount: 5}
	u := UsageFromGeminiMetadata(md, 77)
	require.NotNil(t, u)
	assert.Equal(t, 77, u.PromptTokens)
}

func TestUsage_CachedTokens(t *testing.T) {
	md := &dto.GeminiUsageMetadata{
		PromptTokenCount:        100,
		CachedContentTokenCount: 20,
		CandidatesTokenCount:    5,
	}
	u := UsageFromGeminiMetadata(md, 0)
	require.NotNil(t, u)
	assert.Equal(t, 20, u.PromptTokensDetails.CachedTokens)
}

func TestUsage_ModalityDetails(t *testing.T) {
	md := &dto.GeminiUsageMetadata{
		PromptTokenCount:     100,
		CandidatesTokenCount: 50,
		PromptTokensDetails: []dto.GeminiPromptTokensDetails{
			{Modality: "TEXT", TokenCount: 60},
			{Modality: "AUDIO", TokenCount: 30},
			{Modality: "IMAGE", TokenCount: 10},
			{Modality: "VIDEO", TokenCount: 5}, // unknown => ignored
		},
		ToolUsePromptTokensDetails: []dto.GeminiPromptTokensDetails{
			{Modality: "AUDIO", TokenCount: 7},
			{Modality: "IMAGE", TokenCount: 3},
			{Modality: "TEXT", TokenCount: 2},
		},
		CandidatesTokensDetails: []dto.GeminiPromptTokensDetails{
			{Modality: "IMAGE", TokenCount: 11},
			{Modality: "AUDIO", TokenCount: 12},
			{Modality: "TEXT", TokenCount: 13},
			{Modality: "VIDEO", TokenCount: 99}, // unknown => ignored
		},
	}
	u := UsageFromGeminiMetadata(md, 0)
	require.NotNil(t, u)
	assert.Equal(t, 62, u.PromptTokensDetails.TextTokens)   // 60+2
	assert.Equal(t, 37, u.PromptTokensDetails.AudioTokens)  // 30+7
	assert.Equal(t, 13, u.PromptTokensDetails.ImageTokens)  // 10+3
	assert.Equal(t, 13, u.CompletionTokenDetails.TextTokens)
	assert.Equal(t, 12, u.CompletionTokenDetails.AudioTokens)
	assert.Equal(t, 11, u.CompletionTokenDetails.ImageTokens)
	// TextTokens already set => not overwritten with prompt total.
	assert.Equal(t, 62, u.PromptTokensDetails.TextTokens)
}

func TestUsage_CompletionDerivedFromTotal(t *testing.T) {
	// CandidatesTokenCount + ThoughtsTokenCount == 0 but Total>0 => derive.
	md := &dto.GeminiUsageMetadata{
		PromptTokenCount: 30,
		TotalTokenCount:  100,
	}
	u := UsageFromGeminiMetadata(md, 0)
	require.NotNil(t, u)
	assert.Equal(t, 70, u.CompletionTokens) // 100-30
}

func TestUsage_TextTokensNotDefaultedWhenAudioPresent(t *testing.T) {
	md := &dto.GeminiUsageMetadata{
		PromptTokenCount:     100,
		CandidatesTokenCount: 5,
		PromptTokensDetails: []dto.GeminiPromptTokensDetails{
			{Modality: "AUDIO", TokenCount: 40},
		},
	}
	u := UsageFromGeminiMetadata(md, 0)
	require.NotNil(t, u)
	// AudioTokens>0 => TextTokens not defaulted.
	assert.Equal(t, 0, u.PromptTokensDetails.TextTokens)
	assert.Equal(t, 40, u.PromptTokensDetails.AudioTokens)
}

// --- ResponseGeminiChat2OpenAI ---

func TestResp_TextCandidate(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Index:        0,
				FinishReason: sp("STOP"),
				Content:      dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "hello"}}},
			},
		},
	}
	out := ResponseGeminiChat2OpenAI("id-1", 123, resp)
	require.NotNil(t, out)
	assert.Equal(t, "id-1", out.Id)
	assert.Equal(t, int64(123), out.Created)
	require.Len(t, out.Choices, 1)
	assert.Equal(t, "hello", out.Choices[0].StringContent())
	assert.Equal(t, constant.FinishReasonStop, out.Choices[0].FinishReason)
}

func TestResp_FinishReasonMapping(t *testing.T) {
	cases := map[string]string{
		"STOP":               constant.FinishReasonStop,
		"MAX_TOKENS":         constant.FinishReasonLength,
		"SAFETY":             constant.FinishReasonContentFilter,
		"RECITATION":         constant.FinishReasonContentFilter,
		"BLOCKLIST":          constant.FinishReasonContentFilter,
		"PROHIBITED_CONTENT": constant.FinishReasonContentFilter,
		"SPII":               constant.FinishReasonContentFilter,
		"OTHER":              constant.FinishReasonContentFilter,
		"SOMETHING_NEW":      constant.FinishReasonContentFilter, // default
	}
	for reason, want := range cases {
		resp := &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{
				{FinishReason: sp(reason), Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "x"}}}},
			},
		}
		out := ResponseGeminiChat2OpenAI("i", 1, resp)
		require.Len(t, out.Choices, 1)
		assert.Equal(t, want, out.Choices[0].FinishReason, "reason=%s", reason)
	}
}

func TestResp_NilFinishReasonKeepsDefaultStop(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "x"}}}},
		},
	}
	out := ResponseGeminiChat2OpenAI("i", 1, resp)
	assert.Equal(t, constant.FinishReasonStop, out.Choices[0].FinishReason)
}

func TestResp_ImageInlineData(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "AAAA"}},
			}}},
		},
	}
	out := ResponseGeminiChat2OpenAI("i", 1, resp)
	assert.Equal(t, "![image](data:image/png;base64,AAAA)", out.Choices[0].StringContent())
}

func TestResp_NonImageInlineData(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{InlineData: &dto.GeminiInlineData{MimeType: "audio/mp3", Data: "BBBB"}},
			}}},
		},
	}
	out := ResponseGeminiChat2OpenAI("i", 1, resp)
	assert.Equal(t, "[media](data:audio/mp3;base64,BBBB)", out.Choices[0].StringContent())
}

func TestResp_FunctionCallToolCalls(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{FunctionCall: &dto.FunctionCall{FunctionName: "fn", Arguments: map[string]any{"a": 1}}},
			}}},
		},
	}
	out := ResponseGeminiChat2OpenAI("i", 1, resp)
	assert.Equal(t, constant.FinishReasonToolCalls, out.Choices[0].FinishReason)
	tcs := out.Choices[0].Message.ParseToolCalls()
	require.Len(t, tcs, 1)
	assert.True(t, strings.HasPrefix(tcs[0].ID, "call_"))
	assert.Equal(t, "fn", tcs[0].Function.Name)
	assert.JSONEq(t, `{"a":1}`, tcs[0].Function.Arguments)
}

func TestResp_Thought(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{Thought: true, Text: "thinking"},
				{Text: "answer"},
			}}},
		},
	}
	out := ResponseGeminiChat2OpenAI("i", 1, resp)
	require.NotNil(t, out.Choices[0].Message.ReasoningContent)
	assert.Equal(t, "thinking", *out.Choices[0].Message.ReasoningContent)
	assert.Equal(t, "answer", out.Choices[0].StringContent())
}

func TestResp_ExecutableCodeAndResult(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{ExecutableCode: &dto.GeminiPartExecutableCode{Language: "python", Code: "print(1)"}},
				{CodeExecutionResult: &dto.GeminiPartCodeExecutionResult{Output: "1"}},
			}}},
		},
	}
	out := ResponseGeminiChat2OpenAI("i", 1, resp)
	content := out.Choices[0].StringContent()
	assert.Contains(t, content, "```python\nprint(1)\n```")
	assert.Contains(t, content, "```output\n1\n```")
}

func TestResp_NewlineOnlyTextSkipped(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{Text: "\n"},
				{Text: "real"},
			}}},
		},
	}
	out := ResponseGeminiChat2OpenAI("i", 1, resp)
	assert.Equal(t, "real", out.Choices[0].StringContent())
}

func TestResp_EmptyParts(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Index: 3, Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{}}},
		},
	}
	out := ResponseGeminiChat2OpenAI("i", 1, resp)
	require.Len(t, out.Choices, 1)
	assert.Equal(t, 3, out.Choices[0].Index)
	assert.Equal(t, "", out.Choices[0].StringContent())
}

func TestResp_NoCandidates(t *testing.T) {
	out := ResponseGeminiChat2OpenAI("i", 1, &dto.GeminiChatResponse{})
	require.NotNil(t, out)
	assert.Len(t, out.Choices, 0)
}

func TestResp_ToolCallOverridesFinishReason(t *testing.T) {
	// FinishReason STOP present, but a function call forces tool_calls.
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				FinishReason: sp("STOP"),
				Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
					{FunctionCall: &dto.FunctionCall{FunctionName: "fn", Arguments: map[string]any{}}},
				}},
			},
		},
	}
	out := ResponseGeminiChat2OpenAI("i", 1, resp)
	assert.Equal(t, constant.FinishReasonToolCalls, out.Choices[0].FinishReason)
}

func TestResp_MultipleTextPartsSeparatedByNewline(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{Text: "line1"},
				{Text: "line2"},
			}}},
		},
	}
	out := ResponseGeminiChat2OpenAI("i", 1, resp)
	assert.Equal(t, "line1\nline2", out.Choices[0].StringContent())
}

// --- StreamResponseGeminiChat2OpenAI ---

func TestStream_StopSetsIsStopAndNilFinish(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{FinishReason: sp("STOP"), Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "hi"}}}},
		},
	}
	out, isStop := StreamResponseGeminiChat2OpenAI(resp)
	require.NotNil(t, out)
	assert.True(t, isStop)
	require.Len(t, out.Choices, 1)
	// FinishReason cleared to nil for STOP.
	assert.Nil(t, out.Choices[0].FinishReason)
	assert.Equal(t, "hi", out.Choices[0].Delta.GetContentString())
}

func TestStream_MaxTokensFinishReason(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{FinishReason: sp("MAX_TOKENS"), Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "x"}}}},
		},
	}
	out, isStop := StreamResponseGeminiChat2OpenAI(resp)
	assert.False(t, isStop)
	require.NotNil(t, out.Choices[0].FinishReason)
	assert.Equal(t, constant.FinishReasonLength, *out.Choices[0].FinishReason)
}

func TestStream_SafetyFinishReason(t *testing.T) {
	for _, reason := range []string{"SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "OTHER", "NEW_ONE"} {
		resp := &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{
				{FinishReason: sp(reason), Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "x"}}}},
			},
		}
		out, _ := StreamResponseGeminiChat2OpenAI(resp)
		require.NotNil(t, out.Choices[0].FinishReason)
		assert.Equal(t, constant.FinishReasonContentFilter, *out.Choices[0].FinishReason, "reason=%s", reason)
	}
}

func TestStream_ImageInlineData(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "AAAA"}},
			}}},
		},
	}
	out, _ := StreamResponseGeminiChat2OpenAI(resp)
	assert.Equal(t, "![image](data:image/png;base64,AAAA)", out.Choices[0].Delta.GetContentString())
}

func TestStream_NonImageInlineDataIgnored(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{InlineData: &dto.GeminiInlineData{MimeType: "audio/mp3", Data: "BBBB"}},
			}}},
		},
	}
	out, _ := StreamResponseGeminiChat2OpenAI(resp)
	// Non-image inline data produces no content in stream mode.
	assert.Equal(t, "", out.Choices[0].Delta.GetContentString())
}

func TestStream_FunctionCallToolCalls(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{FunctionCall: &dto.FunctionCall{FunctionName: "fn", Arguments: map[string]any{"a": 1}}},
			}}},
		},
	}
	out, _ := StreamResponseGeminiChat2OpenAI(resp)
	require.NotNil(t, out.Choices[0].FinishReason)
	assert.Equal(t, constant.FinishReasonToolCalls, *out.Choices[0].FinishReason)
	tcs := out.Choices[0].Delta.ToolCalls
	require.Len(t, tcs, 1)
	require.NotNil(t, tcs[0].Index)
	assert.Equal(t, 0, *tcs[0].Index)
	assert.Equal(t, "fn", tcs[0].Function.Name)
}

func TestStream_Thought(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{Thought: true, Text: "reasoning"},
			}}},
		},
	}
	out, _ := StreamResponseGeminiChat2OpenAI(resp)
	assert.Equal(t, "reasoning", out.Choices[0].Delta.GetReasoningContent())
	assert.Equal(t, "", out.Choices[0].Delta.GetContentString())
}

func TestStream_ExecutableCodeAndResult(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{ExecutableCode: &dto.GeminiPartExecutableCode{Language: "go", Code: "x"}},
				{CodeExecutionResult: &dto.GeminiPartCodeExecutionResult{Output: "y"}},
			}}},
		},
	}
	out, _ := StreamResponseGeminiChat2OpenAI(resp)
	content := out.Choices[0].Delta.GetContentString()
	assert.Contains(t, content, "```go\nx\n```")
	assert.Contains(t, content, "```output\ny\n```")
}

func TestStream_NewlineOnlyTextSkipped(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{Text: "\n"},
				{Text: "real"},
			}}},
		},
	}
	out, _ := StreamResponseGeminiChat2OpenAI(resp)
	assert.Equal(t, "real", out.Choices[0].Delta.GetContentString())
}

func TestStream_NoCandidates(t *testing.T) {
	out, isStop := StreamResponseGeminiChat2OpenAI(&dto.GeminiChatResponse{})
	require.NotNil(t, out)
	assert.False(t, isStop)
	assert.Len(t, out.Choices, 0)
	assert.Equal(t, "chat.completion.chunk", out.Object)
}

func TestResp_UnmarshalableFunctionArgsDropsToolCall(t *testing.T) {
	// Arguments contains a channel which cannot be marshaled => geminiResponseToolCall
	// returns nil and the tool call is not appended. FinishReason still flips to
	// tool_calls because the FunctionCall branch runs before the marshal.
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{FunctionCall: &dto.FunctionCall{FunctionName: "fn", Arguments: make(chan int)}},
			}}},
		},
	}
	out := ResponseGeminiChat2OpenAI("i", 1, resp)
	require.Len(t, out.Choices, 1)
	assert.Equal(t, constant.FinishReasonToolCalls, out.Choices[0].FinishReason)
	assert.Empty(t, out.Choices[0].Message.ParseToolCalls())
}

func TestStream_UnmarshalableFunctionArgsDropsToolCall(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
				{FunctionCall: &dto.FunctionCall{FunctionName: "fn", Arguments: make(chan int)}},
			}}},
		},
	}
	out, _ := StreamResponseGeminiChat2OpenAI(resp)
	require.Len(t, out.Choices, 1)
	require.NotNil(t, out.Choices[0].FinishReason)
	assert.Equal(t, constant.FinishReasonToolCalls, *out.Choices[0].FinishReason)
	assert.Empty(t, out.Choices[0].Delta.ToolCalls)
}

func TestStream_IndexPreserved(t *testing.T) {
	resp := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{Index: 2, Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: "x"}}}},
		},
	}
	out, _ := StreamResponseGeminiChat2OpenAI(resp)
	assert.Equal(t, 2, out.Choices[0].Index)
}
