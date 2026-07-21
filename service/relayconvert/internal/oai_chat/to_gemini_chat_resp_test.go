package oaichat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// --- ResponseOpenAI2Gemini (non-stream) ------------------------------------

func TestGeminiResp_TextToolFinishReasonAndUsage(t *testing.T) {
	msg := dto.Message{Role: "assistant", Content: "hello"}
	msg.SetToolCalls([]dto.ToolCallRequest{
		{ID: "c1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: `{"q":"x"}`}},
	})
	resp := ResponseOpenAI2Gemini(&dto.OpenAITextResponse{
		Model: "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{
			{Index: 2, Message: msg, FinishReason: "length"},
		},
		Usage: dto.Usage{PromptTokens: 11, CompletionTokens: 5, TotalTokens: 16},
	}, nil)

	assert.Equal(t, 11, resp.UsageMetadata.PromptTokenCount)
	assert.Equal(t, 5, resp.UsageMetadata.CandidatesTokenCount)
	assert.Equal(t, 16, resp.UsageMetadata.TotalTokenCount)
	require.NotNil(t, resp.UsageMetadata.BillingUsage)
	require.NotNil(t, resp.UsageMetadata.BillingUsage.OpenAIUsage)
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, int64(2), resp.Candidates[0].Index)
	require.NotNil(t, resp.Candidates[0].FinishReason)
	assert.Equal(t, "MAX_TOKENS", *resp.Candidates[0].FinishReason)
	require.Len(t, resp.Candidates[0].Content.Parts, 2)
	assert.Equal(t, "hello", resp.Candidates[0].Content.Parts[0].Text)
	require.NotNil(t, resp.Candidates[0].Content.Parts[1].FunctionCall)
	assert.Equal(t, "lookup", resp.Candidates[0].Content.Parts[1].FunctionCall.FunctionName)
	assert.Equal(t, map[string]interface{}{"q": "x"}, resp.Candidates[0].Content.Parts[1].FunctionCall.Arguments)
}

func TestGeminiResp_FinishReasonMapping(t *testing.T) {
	cases := map[string]string{
		"stop":           "STOP",
		"length":         "MAX_TOKENS",
		"content_filter": "SAFETY",
		"tool_calls":     "STOP",
		"something_else":  "STOP",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			resp := ResponseOpenAI2Gemini(&dto.OpenAITextResponse{
				Model: "gpt-test",
				Choices: []dto.OpenAITextResponseChoice{
					{Message: dto.Message{Role: "assistant", Content: "x"}, FinishReason: in},
				},
			}, nil)
			require.Len(t, resp.Candidates, 1)
			require.NotNil(t, resp.Candidates[0].FinishReason)
			assert.Equal(t, want, *resp.Candidates[0].FinishReason)
		})
	}
}

func TestGeminiResp_TotalTokensDerivedWhenZero(t *testing.T) {
	resp := ResponseOpenAI2Gemini(&dto.OpenAITextResponse{
		Model: "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{
			{Message: dto.Message{Role: "assistant", Content: "x"}, FinishReason: "stop"},
		},
		Usage: dto.Usage{PromptTokens: 4, CompletionTokens: 6, TotalTokens: 0},
	}, nil)
	// TotalTokens 0 -> derived as prompt+completion.
	assert.Equal(t, 10, resp.UsageMetadata.TotalTokenCount)
}

func TestGeminiResp_ToolCallInvalidArgsFallback(t *testing.T) {
	msg := dto.Message{Role: "assistant", Content: ""}
	msg.SetToolCalls([]dto.ToolCallRequest{
		{ID: "c1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: `{bad`}},
	})
	resp := ResponseOpenAI2Gemini(&dto.OpenAITextResponse{
		Model:   "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{{Message: msg, FinishReason: "tool_calls"}},
	}, nil)
	// Invalid args -> args={"arguments": raw}. No text part (empty content).
	parts := resp.Candidates[0].Content.Parts
	require.Len(t, parts, 1)
	require.NotNil(t, parts[0].FunctionCall)
	assert.Equal(t, map[string]interface{}{"arguments": `{bad`}, parts[0].FunctionCall.Arguments)
}

func TestGeminiResp_ToolCallEmptyArgs(t *testing.T) {
	msg := dto.Message{Role: "assistant", Content: ""}
	msg.SetToolCalls([]dto.ToolCallRequest{
		{ID: "c1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: ""}},
	})
	resp := ResponseOpenAI2Gemini(&dto.OpenAITextResponse{
		Model:   "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{{Message: msg, FinishReason: "tool_calls"}},
	}, nil)
	parts := resp.Candidates[0].Content.Parts
	require.Len(t, parts, 1)
	require.NotNil(t, parts[0].FunctionCall)
	assert.Equal(t, map[string]interface{}{}, parts[0].FunctionCall.Arguments)
}

func TestGeminiResp_PrefersExistingGeminiBillingUsage(t *testing.T) {
	src := dto.Usage{
		PromptTokens:     10,
		CompletionTokens: 2,
		TotalTokens:      12,
		BillingUsage: &dto.BillingUsage{
			Source:   dto.BillingUsageSourceGeminiChat,
			Semantic: dto.BillingUsageSemanticGemini,
			GeminiUsageMetadata: &dto.GeminiUsageMetadata{
				PromptTokenCount:     99,
				CandidatesTokenCount: 88,
				TotalTokenCount:      187,
			},
		},
	}
	resp := ResponseOpenAI2Gemini(&dto.OpenAITextResponse{
		Model:   "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{{Message: dto.Message{Role: "assistant", Content: "x"}, FinishReason: "stop"}},
		Usage:   src,
	}, nil)
	// The metadata pulled from the pre-existing Gemini billing usage wins.
	assert.Equal(t, 99, resp.UsageMetadata.PromptTokenCount)
	assert.Equal(t, 88, resp.UsageMetadata.CandidatesTokenCount)
	assert.Equal(t, 187, resp.UsageMetadata.TotalTokenCount)
}

// --- StreamResponseOpenAI2Gemini -------------------------------------------

func TestGeminiStream_ToolCallFinishReasonAndUsage(t *testing.T) {
	resp := StreamResponseOpenAI2Gemini(&dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Index:        1,
				FinishReason: ptr("tool_calls"),
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					ToolCalls: []dto.ToolCallResponse{
						{Type: "function", Function: dto.FunctionResponse{Name: "lookup", Arguments: `{"q":"x"}`}},
					},
				},
			},
		},
		Usage: &dto.Usage{PromptTokens: 13, CompletionTokens: 8, TotalTokens: 21},
	}, &relaycommon.RelayInfo{})
	require.NotNil(t, resp)
	assert.Equal(t, 13, resp.UsageMetadata.PromptTokenCount)
	assert.Equal(t, 8, resp.UsageMetadata.CandidatesTokenCount)
	assert.Equal(t, 21, resp.UsageMetadata.TotalTokenCount)
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, int64(1), resp.Candidates[0].Index)
	require.NotNil(t, resp.Candidates[0].FinishReason)
	assert.Equal(t, "STOP", *resp.Candidates[0].FinishReason)
	require.Len(t, resp.Candidates[0].Content.Parts, 1)
	require.NotNil(t, resp.Candidates[0].Content.Parts[0].FunctionCall)
}

func TestGeminiStream_TextDeltaNoUsage(t *testing.T) {
	resp := StreamResponseOpenAI2Gemini(&dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: ptr("hi")}},
		},
	}, &relaycommon.RelayInfo{})
	require.NotNil(t, resp)
	require.Len(t, resp.Candidates, 1)
	require.Len(t, resp.Candidates[0].Content.Parts, 1)
	assert.Equal(t, "hi", resp.Candidates[0].Content.Parts[0].Text)
	// No finish reason on this chunk.
	assert.Nil(t, resp.Candidates[0].FinishReason)
}

func TestGeminiStream_EmptyChunkReturnsNil(t *testing.T) {
	// No content and no finish reason -> skip (nil).
	resp := StreamResponseOpenAI2Gemini(&dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: ptr("")}},
		},
	}, &relaycommon.RelayInfo{})
	assert.Nil(t, resp)
}

func TestGeminiStream_FinishReasonMapping(t *testing.T) {
	cases := map[string]string{
		"stop":           "STOP",
		"length":         "MAX_TOKENS",
		"content_filter": "SAFETY",
		"tool_calls":     "STOP",
		"mystery":        "STOP",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			resp := StreamResponseOpenAI2Gemini(&dto.ChatCompletionsStreamResponse{
				Choices: []dto.ChatCompletionsStreamResponseChoice{
					{Index: 0, FinishReason: ptr(in), Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: ptr("x")}},
				},
			}, &relaycommon.RelayInfo{})
			require.NotNil(t, resp)
			require.NotNil(t, resp.Candidates[0].FinishReason)
			assert.Equal(t, want, *resp.Candidates[0].FinishReason)
		})
	}
}

func TestGeminiStream_NilInfoUsesZeroEstimate(t *testing.T) {
	resp := StreamResponseOpenAI2Gemini(&dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: ptr("hi")}},
		},
	}, nil)
	require.NotNil(t, resp)
	assert.Equal(t, 0, resp.UsageMetadata.PromptTokenCount)
}

func TestGeminiStream_ToolCallEmptyAndInvalidArgs(t *testing.T) {
	// empty args -> {}, invalid args -> {"arguments": raw}
	resp := StreamResponseOpenAI2Gemini(&dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
				{Type: "function", Function: dto.FunctionResponse{Name: "a", Arguments: ""}},
				{Type: "function", Function: dto.FunctionResponse{Name: "b", Arguments: `{bad`}},
			}}},
		},
	}, &relaycommon.RelayInfo{})
	require.NotNil(t, resp)
	parts := resp.Candidates[0].Content.Parts
	require.Len(t, parts, 2)
	assert.Equal(t, map[string]interface{}{}, parts[0].FunctionCall.Arguments)
	assert.Equal(t, map[string]interface{}{"arguments": `{bad`}, parts[1].FunctionCall.Arguments)
}

func TestGeminiStream_PrefersExistingGeminiBillingUsage(t *testing.T) {
	resp := StreamResponseOpenAI2Gemini(&dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: ptr("x")}},
		},
		Usage: &dto.Usage{
			PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2,
			BillingUsage: &dto.BillingUsage{
				Source:   dto.BillingUsageSourceGeminiChat,
				Semantic: dto.BillingUsageSemanticGemini,
				GeminiUsageMetadata: &dto.GeminiUsageMetadata{
					PromptTokenCount: 77, CandidatesTokenCount: 66, TotalTokenCount: 143,
				},
			},
		},
	}, &relaycommon.RelayInfo{})
	require.NotNil(t, resp)
	assert.Equal(t, 77, resp.UsageMetadata.PromptTokenCount)
	assert.Equal(t, 143, resp.UsageMetadata.TotalTokenCount)
}

// --- billing usage helpers -------------------------------------------------

func TestOpenAIBillingUsageFromUsage(t *testing.T) {
	assert.Nil(t, openAIBillingUsageFromUsage(nil))

	// Fresh usage -> new OAI billing usage.
	bu := openAIBillingUsageFromUsage(&dto.Usage{PromptTokens: 5, CompletionTokens: 2})
	require.NotNil(t, bu)
	assert.Equal(t, dto.BillingUsageSourceOAIChat, bu.Source)

	// Pre-existing OAI billing usage reused.
	existing := dto.NewOpenAIResponsesBillingUsage(&dto.Usage{PromptTokens: 9})
	reuse := openAIBillingUsageFromUsage(&dto.Usage{PromptTokens: 1, BillingUsage: existing})
	require.NotNil(t, reuse)
	require.NotNil(t, reuse.OpenAIUsage)
	assert.Equal(t, 9, reuse.OpenAIUsage.PromptTokens)
}

func TestGeminiBillingMetadataFromOpenAIUsage(t *testing.T) {
	// nil / no billing usage -> false
	_, ok := geminiBillingMetadataFromOpenAIUsage(nil)
	assert.False(t, ok)
	_, ok = geminiBillingMetadataFromOpenAIUsage(&dto.Usage{})
	assert.False(t, ok)

	// wrong source/semantic -> false
	_, ok = geminiBillingMetadataFromOpenAIUsage(&dto.Usage{
		BillingUsage: &dto.BillingUsage{
			Source:              dto.BillingUsageSourceOAIChat,
			GeminiUsageMetadata: &dto.GeminiUsageMetadata{PromptTokenCount: 1},
		},
	})
	assert.False(t, ok)

	// correct semantic -> true
	md, ok := geminiBillingMetadataFromOpenAIUsage(&dto.Usage{
		BillingUsage: &dto.BillingUsage{
			Semantic:            dto.BillingUsageSemanticGemini,
			GeminiUsageMetadata: &dto.GeminiUsageMetadata{PromptTokenCount: 42},
		},
	})
	require.True(t, ok)
	assert.Equal(t, 42, md.PromptTokenCount)
}
