package openai

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// convertChatRequest runs ConvertOpenAIRequest over a request that sets every
// parameter the capability rules can strip, so each assertion below reflects a
// decision made by dto.GetOpenAIChatCapabilities rather than an absent field.
func convertChatRequest(t *testing.T, model string) (*dto.GeneralOpenAIRequest, *relaycommon.RelayInfo) {
	t.Helper()
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	request := &dto.GeneralOpenAIRequest{
		Model: model,
		Messages: []dto.Message{
			{Role: "system", Content: "you are a helpful assistant"},
			{Role: "user", Content: "hi"},
		},
		MaxTokens:   lo.ToPtr(uint(16)),
		Temperature: lo.ToPtr(0.7),
		TopP:        lo.ToPtr(0.9),
		LogProbs:    lo.ToPtr(true),
		TopLogProbs: lo.ToPtr(3),
	}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: model,
		},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(c, info, request)
	require.NoError(t, err)
	result, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok, "expected *dto.GeneralOpenAIRequest, got %T", converted)
	return result, info
}

// gpt-6-astra rejects max_tokens, temperature, top_p and logprobs; before the
// capability rules it only matched the literal "gpt-5" prefix and every one of
// those parameters was forwarded as-is, so the upstream returned 400.
func TestConvertOpenAIRequestGPT6AstraStripsUnsupportedParameters(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "gpt-6-astra-2026-09-03"} {
		t.Run(model, func(t *testing.T) {
			request, _ := convertChatRequest(t, model)

			assert.Nil(t, request.MaxTokens)
			require.NotNil(t, request.MaxCompletionTokens)
			assert.Equal(t, uint(16), *request.MaxCompletionTokens)
			assert.Nil(t, request.Temperature)
			assert.Nil(t, request.TopP)
			assert.Nil(t, request.LogProbs)
			assert.Nil(t, request.TopLogProbs)
			assert.Equal(t, "developer", request.Messages[0].Role)
			assert.Equal(t, "user", request.Messages[1].Role)
		})
	}
}

// The reasoning effort suffix has to be stripped before the model name is
// matched, otherwise "gpt-6-astra-high" is an unrecognized model and keeps the
// parameters the upstream rejects.
func TestConvertOpenAIRequestGPT6AstraResolvesEffortSuffix(t *testing.T) {
	request, info := convertChatRequest(t, "gpt-6-astra-high")

	assert.Equal(t, "gpt-6-astra", request.Model)
	assert.Equal(t, "gpt-6-astra", info.UpstreamModelName)
	assert.Equal(t, "high", request.ReasoningEffort)
	assert.Equal(t, "high", info.ReasoningEffort)
	assert.Nil(t, request.MaxTokens)
	require.NotNil(t, request.MaxCompletionTokens)
	assert.Equal(t, uint(16), *request.MaxCompletionTokens)
	assert.Nil(t, request.Temperature)
	assert.Equal(t, "developer", request.Messages[0].Role)
}

// gpt-5.1 defaults to no reasoning and still accepts sampling parameters, so
// only the max_completion_tokens and developer-role rules apply.
func TestConvertOpenAIRequestGPT51KeepsSamplingParameters(t *testing.T) {
	request, _ := convertChatRequest(t, "gpt-5.1")

	assert.Nil(t, request.MaxTokens)
	require.NotNil(t, request.MaxCompletionTokens)
	assert.Equal(t, uint(16), *request.MaxCompletionTokens)
	require.NotNil(t, request.Temperature)
	assert.Equal(t, 0.7, *request.Temperature)
	require.NotNil(t, request.TopP)
	assert.Equal(t, 0.9, *request.TopP)
	require.NotNil(t, request.LogProbs)
	assert.Equal(t, "developer", request.Messages[0].Role)
}

// Reasoning turns the sampling exception off for the same model.
func TestConvertOpenAIRequestGPT51WithEffortStripsSamplingParameters(t *testing.T) {
	request, info := convertChatRequest(t, "gpt-5.1-high")

	assert.Equal(t, "gpt-5.1", request.Model)
	assert.Equal(t, "high", info.ReasoningEffort)
	assert.Nil(t, request.Temperature)
	assert.Nil(t, request.TopP)
	assert.Nil(t, request.LogProbs)
	assert.Nil(t, request.TopLogProbs)
}

func TestConvertOpenAIRequestGPT5StripsSamplingParameters(t *testing.T) {
	request, _ := convertChatRequest(t, "gpt-5")

	assert.Nil(t, request.MaxTokens)
	require.NotNil(t, request.MaxCompletionTokens)
	assert.Nil(t, request.Temperature)
	assert.Nil(t, request.TopP)
	assert.Nil(t, request.LogProbs)
	assert.Equal(t, "developer", request.Messages[0].Role)
}

// o-series only drops temperature, and o1-mini keeps the system role.
func TestConvertOpenAIRequestO1MiniKeepsSystemRoleAndTopP(t *testing.T) {
	request, _ := convertChatRequest(t, "o1-mini")

	assert.Nil(t, request.MaxTokens)
	require.NotNil(t, request.MaxCompletionTokens)
	assert.Equal(t, uint(16), *request.MaxCompletionTokens)
	assert.Nil(t, request.Temperature)
	require.NotNil(t, request.TopP)
	require.NotNil(t, request.LogProbs)
	assert.Equal(t, "system", request.Messages[0].Role)
}

// Unrecognized models keep every parameter: neither an older generation nor an
// unknown gpt-6-astra variant may inherit the restrictions.
func TestConvertOpenAIRequestUnrecognizedModelsAreUntouched(t *testing.T) {
	for _, model := range []string{"gpt-4.1", "gpt-6-astra-pro", "gpt-7"} {
		t.Run(model, func(t *testing.T) {
			request, info := convertChatRequest(t, model)

			require.NotNil(t, request.MaxTokens)
			assert.Equal(t, uint(16), *request.MaxTokens)
			assert.Nil(t, request.MaxCompletionTokens)
			require.NotNil(t, request.Temperature)
			assert.Equal(t, 0.7, *request.Temperature)
			require.NotNil(t, request.TopP)
			require.NotNil(t, request.LogProbs)
			require.NotNil(t, request.TopLogProbs)
			assert.Equal(t, "system", request.Messages[0].Role)
			assert.Empty(t, info.ReasoningEffort)
		})
	}
}
