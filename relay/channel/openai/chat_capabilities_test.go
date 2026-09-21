package openai

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
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

// convertChatRequestFromJSON starts from the raw client body so the pointer
// semantics of max_tokens (Rule 5) are exercised the same way TextHelper does:
// an explicit zero survives unmarshalling as a non-nil pointer.
func convertChatRequestFromJSON(t *testing.T, body string) *dto.GeneralOpenAIRequest {
	t.Helper()
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	var request dto.GeneralOpenAIRequest
	require.NoError(t, common.UnmarshalJsonStr(body, &request))
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeAzure,
			UpstreamModelName: request.Model,
		},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(c, info, &request)
	require.NoError(t, err)
	result, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok, "expected *dto.GeneralOpenAIRequest, got %T", converted)
	return result
}

// Azure rejects the request as soon as max_tokens is present, whatever its
// value, so an explicit zero must be dropped rather than forwarded. It is not
// carried over to max_completion_tokens either: upstream requires that to be >= 1.
func TestConvertOpenAIRequestDropsExplicitZeroMaxTokens(t *testing.T) {
	for _, model := range []string{"gpt-5.5", "gpt-6-astra", "o3"} {
		t.Run(model, func(t *testing.T) {
			request := convertChatRequestFromJSON(t,
				`{"model":"`+model+`","messages":[{"role":"user","content":"hi"}],"max_tokens":0}`)

			assert.Nil(t, request.MaxTokens)
			assert.Nil(t, request.MaxCompletionTokens)
		})
	}
}

// A client that already sends max_completion_tokens but keeps max_tokens for
// backwards compatibility must still have max_tokens removed, and the value it
// asked for must not be overwritten.
func TestConvertOpenAIRequestDropsMaxTokensWhenBothPresent(t *testing.T) {
	request := convertChatRequestFromJSON(t,
		`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}],"max_tokens":128,"max_completion_tokens":256}`)

	assert.Nil(t, request.MaxTokens)
	require.NotNil(t, request.MaxCompletionTokens)
	assert.Equal(t, uint(256), *request.MaxCompletionTokens)
}

// The ordinary case keeps working: max_tokens is renamed, value preserved.
func TestConvertOpenAIRequestRenamesMaxTokensFromClientBody(t *testing.T) {
	request := convertChatRequestFromJSON(t,
		`{"model":"gpt-5.6-terra","messages":[{"role":"user","content":"hi"}],"max_tokens":128}`)

	assert.Nil(t, request.MaxTokens)
	require.NotNil(t, request.MaxCompletionTokens)
	assert.Equal(t, uint(128), *request.MaxCompletionTokens)
}

// Unrecognized models are still left alone, including the zero value.
func TestConvertOpenAIRequestKeepsZeroMaxTokensForUnrecognizedModel(t *testing.T) {
	request := convertChatRequestFromJSON(t,
		`{"model":"gpt-4.1","messages":[{"role":"user","content":"hi"}],"max_tokens":0}`)

	require.NotNil(t, request.MaxTokens)
	assert.Equal(t, uint(0), *request.MaxTokens)
	assert.Nil(t, request.MaxCompletionTokens)
}

// maxTokensCase is one cell of the max_tokens x max_completion_tokens matrix.
// want* are the values expected on the upstream request; a nil want means the
// field must be absent.
type maxTokensCase struct {
	name    string
	body    string
	wantMT  *uint
	wantMCT *uint
}

func runMaxTokensMatrix(t *testing.T, model string, cases []maxTokensCase) {
	t.Helper()
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := convertChatRequestFromJSON(t,
				`{"model":"`+model+`","messages":[{"role":"user","content":"hi"}]`+testCase.body+`}`)

			if testCase.wantMT == nil {
				assert.Nil(t, request.MaxTokens)
			} else {
				require.NotNil(t, request.MaxTokens)
				assert.Equal(t, *testCase.wantMT, *request.MaxTokens)
			}
			if testCase.wantMCT == nil {
				assert.Nil(t, request.MaxCompletionTokens)
			} else {
				require.NotNil(t, request.MaxCompletionTokens)
				assert.Equal(t, *testCase.wantMCT, *request.MaxCompletionTokens)
			}
		})
	}
}

// Every combination of the two fields for a model that rejects max_tokens:
// absent / explicit zero / positive, on both sides. max_tokens never survives;
// max_completion_tokens is only filled in from it when the client left it at
// zero-or-absent and max_tokens carries a usable value.
func TestConvertOpenAIRequestMaxTokensMatrixForRestrictedModel(t *testing.T) {
	runMaxTokensMatrix(t, "gpt-5.5", []maxTokensCase{
		{name: "both absent", body: ``, wantMT: nil, wantMCT: nil},
		{name: "mct zero only", body: `,"max_completion_tokens":0`, wantMT: nil, wantMCT: lo.ToPtr(uint(0))},
		{name: "mct positive only", body: `,"max_completion_tokens":128`, wantMT: nil, wantMCT: lo.ToPtr(uint(128))},
		{name: "mt zero only", body: `,"max_tokens":0`, wantMT: nil, wantMCT: nil},
		{name: "mt zero + mct zero", body: `,"max_tokens":0,"max_completion_tokens":0`, wantMT: nil, wantMCT: lo.ToPtr(uint(0))},
		{name: "mt zero + mct positive", body: `,"max_tokens":0,"max_completion_tokens":128`, wantMT: nil, wantMCT: lo.ToPtr(uint(128))},
		{name: "mt one is the lower bound that carries over", body: `,"max_tokens":1`, wantMT: nil, wantMCT: lo.ToPtr(uint(1))},
		{name: "mt positive only", body: `,"max_tokens":64`, wantMT: nil, wantMCT: lo.ToPtr(uint(64))},
		{name: "mt positive + mct zero keeps legacy carry-over", body: `,"max_tokens":64,"max_completion_tokens":0`, wantMT: nil, wantMCT: lo.ToPtr(uint(64))},
		{name: "mt positive + mct positive keeps the client value", body: `,"max_tokens":64,"max_completion_tokens":128`, wantMT: nil, wantMCT: lo.ToPtr(uint(128))},
	})
}

// The same matrix on a model outside the capability rules must be the identity
// transform: nothing is renamed, dropped or carried over.
func TestConvertOpenAIRequestMaxTokensMatrixForUnrestrictedModel(t *testing.T) {
	runMaxTokensMatrix(t, "gpt-4.1", []maxTokensCase{
		{name: "both absent", body: ``, wantMT: nil, wantMCT: nil},
		{name: "mct zero only", body: `,"max_completion_tokens":0`, wantMT: nil, wantMCT: lo.ToPtr(uint(0))},
		{name: "mt zero only", body: `,"max_tokens":0`, wantMT: lo.ToPtr(uint(0)), wantMCT: nil},
		{name: "mt positive only", body: `,"max_tokens":64`, wantMT: lo.ToPtr(uint(64)), wantMCT: nil},
		{name: "both positive", body: `,"max_tokens":64,"max_completion_tokens":128`, wantMT: lo.ToPtr(uint(64)), wantMCT: lo.ToPtr(uint(128))},
	})
}

// o-series shares the max_completion_tokens rule but keeps its own role and
// sampling behaviour, so the zero value must be dropped there too.
func TestConvertOpenAIRequestMaxTokensMatrixForOSeries(t *testing.T) {
	runMaxTokensMatrix(t, "o1-mini", []maxTokensCase{
		{name: "mt zero only", body: `,"max_tokens":0`, wantMT: nil, wantMCT: nil},
		{name: "mt positive + mct positive", body: `,"max_tokens":64,"max_completion_tokens":128`, wantMT: nil, wantMCT: lo.ToPtr(uint(128))},
	})
}

// The reasoning-effort suffix is stripped before the capability lookup, so the
// max_tokens rule has to apply to the suffixed form as well.
func TestConvertOpenAIRequestDropsZeroMaxTokensWithEffortSuffix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	var request dto.GeneralOpenAIRequest
	require.NoError(t, common.UnmarshalJsonStr(
		`{"model":"gpt-6-astra-high","messages":[{"role":"system","content":"sys"},{"role":"user","content":"hi"}],"max_tokens":0}`,
		&request))
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeAzure,
			UpstreamModelName: request.Model,
		},
	}
	converted, err := (&Adaptor{}).ConvertOpenAIRequest(c, info, &request)
	require.NoError(t, err)
	result := converted.(*dto.GeneralOpenAIRequest)

	assert.Nil(t, result.MaxTokens)
	assert.Nil(t, result.MaxCompletionTokens)
	assert.Equal(t, "gpt-6-astra", result.Model)
	assert.Equal(t, "gpt-6-astra", info.UpstreamModelName)
	assert.Equal(t, "high", result.ReasoningEffort)
	assert.Equal(t, "developer", result.Messages[0].Role)
}

// Anthropic clients always send max_tokens because the Messages API requires
// it; that request reaches the same code through ConvertClaudeRequest, so the
// rename must happen on this entry point too.
func TestConvertClaudeRequestRenamesMaxTokensForRestrictedModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	var request dto.ClaudeRequest
	require.NoError(t, common.UnmarshalJsonStr(
		`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}],"max_tokens":64}`,
		&request))
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeAzure,
			UpstreamModelName: "gpt-5.5",
		},
	}
	converted, err := (&Adaptor{}).ConvertClaudeRequest(c, info, &request)
	require.NoError(t, err)
	result, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok, "expected *dto.GeneralOpenAIRequest, got %T", converted)

	assert.Nil(t, result.MaxTokens)
	require.NotNil(t, result.MaxCompletionTokens)
	assert.Equal(t, uint(64), *result.MaxCompletionTokens)
}
