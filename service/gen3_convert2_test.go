package service

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// request_converter.go — facade delegations into service/relayconvert.
// ===========================================================================

func TestConvert2_ClaudeToOpenAIRequest(t *testing.T) {
	maxTokens := uint(256)
	claudeReq := dto.ClaudeRequest{
		Model:     "claude-3-5-sonnet",
		MaxTokens: &maxTokens,
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello world"},
		},
	}
	info := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatClaude,
		FinalRequestRelayFormat: types.RelayFormatOpenAI,
		OriginModelName:         "claude-3-5-sonnet",
	}
	out, err := ClaudeToOpenAIRequest(claudeReq, info)
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "claude-3-5-sonnet", out.Model)
	assert.NotEmpty(t, out.Messages)
}

func TestConvert2_GeminiToOpenAIRequest(t *testing.T) {
	geminiReq := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "hello gemini"}}},
		},
	}
	info := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatGemini,
		FinalRequestRelayFormat: types.RelayFormatOpenAI,
		OriginModelName:         "gemini-1.5-pro",
	}
	out, err := GeminiToOpenAIRequest(geminiReq, info)
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.NotEmpty(t, out.Messages)
}

func TestConvert2_ChatToResponsesAndBack(t *testing.T) {
	chatReq := &dto.GeneralOpenAIRequest{Model: "gpt-4o", Messages: []dto.Message{{Role: "user"}}}
	chatReq.Messages[0].SetStringContent("hi")
	respReq, err := ChatCompletionsRequestToResponsesRequest(chatReq)
	require.NoError(t, err)
	require.NotNil(t, respReq)
	assert.Equal(t, "gpt-4o", respReq.Model)
}

func TestConvert2_ConvertRequest_OpenAIIdentity(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model:    "gpt-4o",
		Messages: []dto.Message{{Role: "user"}},
	}
	req.Messages[0].SetStringContent("hi")
	info := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		FinalRequestRelayFormat: types.RelayFormatOpenAI,
		OriginModelName:         "gpt-4o",
	}
	result, err := ConvertRequest(nil, info, types.RelayFormatOpenAI, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Value)

	// ConvertRequestVia with an identity path.
	viaResult, err := ConvertRequestVia(nil, info, req, types.RelayFormatOpenAI)
	require.NoError(t, err)
	require.NotNil(t, viaResult)

	// ConvertRequestByID with an explicit converter id (OpenAI chat -> Responses).
	byIDResult, err := ConvertRequestByID(nil, info, "openai_chat_to_openai_responses", req)
	if err == nil {
		require.NotNil(t, byIDResult)
	}
}
