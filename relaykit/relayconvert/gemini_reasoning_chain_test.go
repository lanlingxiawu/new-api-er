package relayconvert

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Registry chains that go through the OpenAI chat pivot with Gemini on one
// side. The Gemini-to-OpenAI step feeds Claude and Responses targets too, so a
// Gemini control mapped onto the pivot must not reach a target that rejects it.

func geminiChainInfo(from types.RelayFormat, upstreamModel string) *convmeta.Values {
	return &convmeta.Values{
		ChannelMetaAttached: true,
		UpstreamModelName:   upstreamModel,
		ConversionChain:     []types.RelayFormat{from},
		Options:             &convmeta.Options{},
	}
}

func geminiChainRequest(t *testing.T, body string) *dto.GeminiChatRequest {
	t.Helper()
	var req dto.GeminiChatRequest
	require.NoError(t, json.Unmarshal([]byte(body), &req))
	return &req
}

// Gemini client on a Claude channel: thinkingConfig used to be dropped and the
// request worked. It must stay dropped — Claude thinking needs temperature 1,
// budget < max_tokens and a thinking-capable model, none of which hold here.
func TestConvertRequestGeminiToClaudeDoesNotEnableThinking(t *testing.T) {
	for _, budget := range []string{"0", "1024", "-1"} {
		t.Run("budget "+budget, func(t *testing.T) {
			req := geminiChainRequest(t, `{
				"contents": [{"role": "user", "parts": [{"text": "hi"}]}],
				"generationConfig": {"temperature": 0.5, "maxOutputTokens": 16, "presencePenalty": 0.5,
					"thinkingConfig": {"thinkingBudget": `+budget+`}}
			}`)
			info := geminiChainInfo(types.RelayFormatGemini, "claude-sonnet-4-5")

			result, err := ConvertRequest(nil, info, types.RelayFormatClaude, req)
			require.NoError(t, err)
			claudeReq, ok := result.Value.(*dto.ClaudeRequest)
			require.True(t, ok, "%T", result.Value)
			assert.Nil(t, claudeReq.Thinking)
			require.NotNil(t, claudeReq.Temperature)
			assert.Equal(t, 0.5, *claudeReq.Temperature)
			require.NotNil(t, claudeReq.MaxTokens)
			assert.Equal(t, uint(16), *claudeReq.MaxTokens)
			assert.Empty(t, info.GetReasoningEffort())
		})
	}
}

// Gemini client on a Responses channel: reasoning is only sent to models that
// accept it, with a value they accept.
func TestConvertRequestGeminiToResponsesReasoningEffort(t *testing.T) {
	for _, tc := range []struct {
		model, budget string
		want          string
	}{
		{"gpt-4o", "1024", ""},
		{"gpt-4o", "0", ""},
		{"gpt-5", "0", "minimal"},
		{"gpt-5.1", "0", "none"},
		{"o3", "0", "low"},
		{"gpt-5", "20000", "high"},
	} {
		t.Run(tc.model+"/"+tc.budget, func(t *testing.T) {
			req := geminiChainRequest(t, `{
				"contents": [{"role": "user", "parts": [{"text": "hi"}]}],
				"generationConfig": {"thinkingConfig": {"thinkingBudget": `+tc.budget+`}}
			}`)
			info := geminiChainInfo(types.RelayFormatGemini, tc.model)

			result, err := ConvertRequest(nil, info, types.RelayFormatOpenAIResponses, req)
			require.NoError(t, err)
			responsesReq, ok := result.Value.(*dto.OpenAIResponsesRequest)
			require.True(t, ok, "%T", result.Value)
			if tc.want == "" {
				assert.Nil(t, responsesReq.Reasoning)
				return
			}
			require.NotNil(t, responsesReq.Reasoning)
			assert.Equal(t, tc.want, responsesReq.Reasoning.Effort)
		})
	}
}

// Responses client on a Gemini channel goes through the same pivot: explicit
// top_p 0 is kept and reasoning.effort none turns thinking off.
func TestConvertRequestResponsesToGeminiZeroValuesAndReasoning(t *testing.T) {
	topP := 0.0
	req := &dto.OpenAIResponsesRequest{
		Model:     "gemini-2.5-flash",
		Input:     mustRawMessage(t, "hi"),
		TopP:      &topP,
		Reasoning: &dto.Reasoning{Effort: "none"},
	}
	info := geminiChainInfo(types.RelayFormatOpenAIResponses, "gemini-2.5-flash")

	result, err := ConvertRequest(nil, info, types.RelayFormatGemini, req)
	require.NoError(t, err)
	geminiReq, ok := result.Value.(*dto.GeminiChatRequest)
	require.True(t, ok, "%T", result.Value)
	require.NotNil(t, geminiReq.GenerationConfig.TopP)
	assert.Equal(t, 0.0, *geminiReq.GenerationConfig.TopP)
	require.NotNil(t, geminiReq.GenerationConfig.ThinkingConfig)
	require.NotNil(t, geminiReq.GenerationConfig.ThinkingConfig.ThinkingBudget)
	assert.Equal(t, 0, *geminiReq.GenerationConfig.ThinkingConfig.ThinkingBudget)
	assert.Equal(t, "none", info.GetReasoningEffort())
}

// Claude client on a Gemini channel: the Claude-to-OpenAI step carries no
// penalties, and the OpenAI-to-Gemini step never adds them.
func TestConvertRequestOpenAIToGeminiDropsPenalties(t *testing.T) {
	presence, frequency := 0.5, -0.25
	req := &dto.GeneralOpenAIRequest{
		Model:            "gemini-2.5-flash",
		Messages:         []dto.Message{{Role: "user", Content: "hi"}},
		PresencePenalty:  &presence,
		FrequencyPenalty: &frequency,
	}
	result, err := ConvertRequest(nil, geminiChainInfo(types.RelayFormatOpenAI, "gemini-2.5-flash"), types.RelayFormatGemini, req)
	require.NoError(t, err)
	geminiReq, ok := result.Value.(*dto.GeminiChatRequest)
	require.True(t, ok, "%T", result.Value)
	assert.Nil(t, geminiReq.GenerationConfig.PresencePenalty)
	assert.Nil(t, geminiReq.GenerationConfig.FrequencyPenalty)
}
