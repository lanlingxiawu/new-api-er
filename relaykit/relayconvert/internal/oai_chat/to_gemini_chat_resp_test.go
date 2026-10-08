package oaichat

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseOpenAI2GeminiMapsTextToolFinishReasonAndUsage(t *testing.T) {
	msg := dto.Message{
		Role:    "assistant",
		Content: "hello",
	}
	msg.SetToolCalls([]dto.ToolCallRequest{
		{
			ID:   "call_1",
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      "lookup",
				Arguments: `{"q":"x"}`,
			},
		},
	})

	resp := ResponseOpenAI2Gemini(&dto.OpenAITextResponse{
		Model: "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{
			{
				Index:        2,
				Message:      msg,
				FinishReason: "length",
			},
		},
		Usage: dto.Usage{
			PromptTokens:     11,
			CompletionTokens: 5,
			TotalTokens:      16,
		},
	}, nil)

	assert.Equal(t, 11, resp.UsageMetadata.PromptTokenCount)
	assert.Equal(t, 5, resp.UsageMetadata.CandidatesTokenCount)
	assert.Equal(t, 16, resp.UsageMetadata.TotalTokenCount)
	require.NotNil(t, resp.UsageMetadata.BillingUsage)
	require.NotNil(t, resp.UsageMetadata.BillingUsage.OpenAIUsage)
	assert.Equal(t, dto.BillingUsageSourceOAIChat, resp.UsageMetadata.BillingUsage.Source)
	assert.Equal(t, dto.BillingUsageSemanticOpenAI, resp.UsageMetadata.BillingUsage.Semantic)
	assert.Equal(t, 11, resp.UsageMetadata.BillingUsage.OpenAIUsage.PromptTokens)
	assert.Equal(t, 5, resp.UsageMetadata.BillingUsage.OpenAIUsage.CompletionTokens)
	assert.Equal(t, 16, resp.UsageMetadata.BillingUsage.OpenAIUsage.TotalTokens)
	assert.Nil(t, resp.UsageMetadata.BillingUsage.OpenAIUsage.BillingUsage)
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, int64(2), resp.Candidates[0].Index)
	require.NotNil(t, resp.Candidates[0].FinishReason)
	assert.Equal(t, "MAX_TOKENS", *resp.Candidates[0].FinishReason)
	require.Len(t, resp.Candidates[0].Content.Parts, 2)
	assert.Equal(t, "hello", resp.Candidates[0].Content.Parts[0].Text)
	require.NotNil(t, resp.Candidates[0].Content.Parts[1].FunctionCall)
	assert.Equal(t, "lookup", resp.Candidates[0].Content.Parts[1].FunctionCall.FunctionName)
	assert.Equal(t, map[string]any{"q": "x"}, resp.Candidates[0].Content.Parts[1].FunctionCall.Arguments)
}

func TestStreamResponseOpenAI2GeminiMapsToolCallFinishReasonAndUsage(t *testing.T) {
	resp := StreamResponseOpenAI2Gemini(&dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Index:        1,
				FinishReason: geminiRespPtr("tool_calls"),
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					ToolCalls: []dto.ToolCallResponse{
						{
							Type: "function",
							Function: dto.FunctionResponse{
								Name:      "lookup",
								Arguments: `{"q":"x"}`,
							},
						},
					},
				},
			},
		},
		Usage: &dto.Usage{
			PromptTokens:     13,
			CompletionTokens: 8,
			TotalTokens:      21,
		},
	}, &convmeta.Values{})

	require.NotNil(t, resp)
	assert.Equal(t, 13, resp.UsageMetadata.PromptTokenCount)
	assert.Equal(t, 8, resp.UsageMetadata.CandidatesTokenCount)
	assert.Equal(t, 21, resp.UsageMetadata.TotalTokenCount)
	require.NotNil(t, resp.UsageMetadata.BillingUsage)
	require.NotNil(t, resp.UsageMetadata.BillingUsage.OpenAIUsage)
	assert.Equal(t, 13, resp.UsageMetadata.BillingUsage.OpenAIUsage.PromptTokens)
	assert.Equal(t, 8, resp.UsageMetadata.BillingUsage.OpenAIUsage.CompletionTokens)
	require.Len(t, resp.Candidates, 1)
	assert.Equal(t, int64(1), resp.Candidates[0].Index)
	require.NotNil(t, resp.Candidates[0].FinishReason)
	assert.Equal(t, "STOP", *resp.Candidates[0].FinishReason)
	require.Len(t, resp.Candidates[0].Content.Parts, 1)
	require.NotNil(t, resp.Candidates[0].Content.Parts[0].FunctionCall)
	assert.Equal(t, "lookup", resp.Candidates[0].Content.Parts[0].FunctionCall.FunctionName)
	assert.Equal(t, map[string]any{"q": "x"}, resp.Candidates[0].Content.Parts[0].FunctionCall.Arguments)
}

func geminiRespPtr[T any](value T) *T {
	return &value
}

// D-6: a Gemini client on an OpenAI channel streams through this converter.
// With include_usage the real usage arrives in a final chunk whose choices are
// empty; the converter used to skip that chunk, so the client only ever saw
// the per-chunk estimate (prompt = local estimate, candidates = 0). Fixture is
// the report's O4 stream (upstream 2 prompt / 17 completion, 16 reasoning).
func TestStreamResponseOpenAI2GeminiEmitsFinalUsageOnlyChunk(t *testing.T) {
	info := &convmeta.Values{EstimatePromptTokens: 3}
	fixture := []string{
		`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		`{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`,
		`{"id":"c1","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":17,"total_tokens":19,"completion_tokens_details":{"reasoning_tokens":16}}}`,
	}
	var emitted []*dto.GeminiChatResponse
	for _, data := range fixture {
		var chunk dto.ChatCompletionsStreamResponse
		require.NoError(t, kitutil.Unmarshal([]byte(data), &chunk))
		if resp := StreamResponseOpenAI2Gemini(&chunk, info); resp != nil {
			emitted = append(emitted, resp)
		}
	}

	// The role-only opening chunk is still skipped.
	require.Len(t, emitted, 3)
	// Content and finish chunks carry the estimate, as before.
	assert.Equal(t, 3, emitted[1].UsageMetadata.PromptTokenCount)
	require.Len(t, emitted[1].Candidates, 1)
	require.NotNil(t, emitted[1].Candidates[0].FinishReason)
	assert.Equal(t, "MAX_TOKENS", *emitted[1].Candidates[0].FinishReason)

	final := emitted[2]
	assert.Empty(t, final.Candidates)
	assert.Equal(t, 2, final.UsageMetadata.PromptTokenCount)
	assert.Equal(t, 17, final.UsageMetadata.CandidatesTokenCount)
	assert.Equal(t, 19, final.UsageMetadata.TotalTokenCount)
	require.NotNil(t, final.UsageMetadata.BillingUsage)
	require.NotNil(t, final.UsageMetadata.BillingUsage.OpenAIUsage)
	assert.Equal(t, 17, final.UsageMetadata.BillingUsage.OpenAIUsage.CompletionTokens)

	body, err := kitutil.Marshal(final)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"candidates":[]`)
}

func TestStreamResponseOpenAI2GeminiSkipsEmptyChunks(t *testing.T) {
	info := &convmeta.Values{EstimatePromptTokens: 3}
	for name, data := range map[string]string{
		"no choices, no usage":       `{"choices":[]}`,
		"no choices, zero usage":     `{"choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
		"role-only chunk with usage": `{"choices":[{"index":0,"delta":{"role":"assistant"}}],"usage":{"prompt_tokens":2,"completion_tokens":0,"total_tokens":2}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var chunk dto.ChatCompletionsStreamResponse
			require.NoError(t, kitutil.Unmarshal([]byte(data), &chunk))
			assert.Nil(t, StreamResponseOpenAI2Gemini(&chunk, info))
		})
	}
}
