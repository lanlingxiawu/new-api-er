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
