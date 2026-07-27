package perplexity

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestContext(rec http.ResponseWriter) *gin.Context {
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c
}

func newResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func infoWith() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
}

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

// ---- requestOpenAI2Perplexity ----

func TestRequestOpenAI2Perplexity_FieldMapping(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model:               "sonar",
		Stream:              lo.ToPtr(true),
		Temperature:         lo.ToPtr(0.4),
		TopP:                lo.ToPtr(0.7),
		FrequencyPenalty:    lo.ToPtr(0.1),
		PresencePenalty:     lo.ToPtr(0.2),
		SearchRecencyFilter: json.RawMessage(`"week"`),
		SearchMode:          json.RawMessage(`"web"`),
		Messages: []dto.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hi"},
		},
	}
	out := requestOpenAI2Perplexity(req)
	assert.Equal(t, "sonar", out.Model)
	require.Len(t, out.Messages, 2)
	assert.Equal(t, "system", out.Messages[0].Role)
	assert.Equal(t, "user", out.Messages[1].Role)
	assert.Equal(t, "hi", out.Messages[1].StringContent())
	require.NotNil(t, out.TopP)
	assert.InDelta(t, 0.7, *out.TopP, 1e-9)
	assert.JSONEq(t, `"week"`, string(out.SearchRecencyFilter))
	assert.JSONEq(t, `"web"`, string(out.SearchMode))
	// MaxTokens nil in source -> nil in output
	assert.Nil(t, out.MaxTokens)
}

func TestRequestOpenAI2Perplexity_MaxTokens(t *testing.T) {
	t.Run("max_tokens propagated", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{MaxTokens: lo.ToPtr(uint(256))}
		out := requestOpenAI2Perplexity(req)
		require.NotNil(t, out.MaxTokens)
		assert.Equal(t, uint(256), *out.MaxTokens)
	})
	t.Run("max_completion_tokens propagated", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{MaxCompletionTokens: lo.ToPtr(uint(128))}
		out := requestOpenAI2Perplexity(req)
		require.NotNil(t, out.MaxTokens)
		assert.Equal(t, uint(128), *out.MaxTokens)
	})
	t.Run("neither set leaves nil", func(t *testing.T) {
		out := requestOpenAI2Perplexity(dto.GeneralOpenAIRequest{})
		assert.Nil(t, out.MaxTokens)
	})
}

// ---- ConvertOpenAIRequest ----

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	info := infoWith()
	t.Run("nil returns error", func(t *testing.T) {
		_, err := a.ConvertOpenAIRequest(c, info, nil)
		require.Error(t, err)
	})
	t.Run("TopP >= 1 clamped to 0.99", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hi"}}, TopP: lo.ToPtr(1.5)}
		out, err := a.ConvertOpenAIRequest(c, info, req)
		require.NoError(t, err)
		pr := out.(*dto.GeneralOpenAIRequest)
		require.NotNil(t, pr.TopP)
		assert.InDelta(t, 0.99, *pr.TopP, 1e-9)
	})
	t.Run("TopP below 1 preserved", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hi"}}, TopP: lo.ToPtr(0.3)}
		out, err := a.ConvertOpenAIRequest(c, info, req)
		require.NoError(t, err)
		pr := out.(*dto.GeneralOpenAIRequest)
		require.NotNil(t, pr.TopP)
		assert.InDelta(t, 0.3, *pr.TopP, 1e-9)
	})
}

// ---- GetRequestURL ----

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}
	t.Run("chat completions", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.perplexity.ai"}}
		got, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, "https://api.perplexity.ai/chat/completions", got)
	})
	t.Run("responses mode", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeResponses, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.perplexity.ai"}}
		got, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, "https://api.perplexity.ai/v1/responses", got)
	})
}

// ---- SetupRequestHeader ----

func TestSetupRequestHeader(t *testing.T) {
	c := newTestContext(httptest.NewRecorder())
	info := infoWith()
	info.ApiKey = "ppl-key"
	h := http.Header{}
	require.NoError(t, (&Adaptor{}).SetupRequestHeader(c, &h, info))
	assert.Equal(t, "Bearer ppl-key", h.Get("Authorization"))
}

// ---- ConvertOpenAIResponsesRequest / ConvertRerankRequest ----

func TestConvertOpenAIResponsesRequest(t *testing.T) {
	c := newTestContext(httptest.NewRecorder())
	req := dto.OpenAIResponsesRequest{Model: "sonar"}
	out, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(c, infoWith(), req)
	require.NoError(t, err)
	assert.Equal(t, req, out)
}

func TestConvertRerankRequest(t *testing.T) {
	c := newTestContext(httptest.NewRecorder())
	out, err := (&Adaptor{}).ConvertRerankRequest(c, 0, dto.RerankRequest{})
	require.NoError(t, err)
	assert.Nil(t, out)
}

// ---- DoResponse (delegates to openai handler) ----

func TestDoResponse_NonStreamSuccess(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newTestContext(rec)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "sonar"}}
	body := `{"id":"cmpl-1","object":"chat.completion","model":"sonar","choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`
	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, body), info)
	require.Nil(t, err)
	require.NotNil(t, usage)
	assert.Equal(t, 8, usage.(*dto.Usage).TotalTokens)
	assert.Contains(t, rec.Body.String(), "answer")
}

func TestDoResponse_Error(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newTestContext(rec)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "sonar"}}
	body := `{"error":{"message":"invalid model","type":"invalid_request_error"}}`
	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusBadRequest, body), info)
	require.Nil(t, usage)
	require.NotNil(t, err)
}

// ---- ConvertClaudeRequest (delegates to openai) ----

func TestConvertClaudeRequest_Delegates(t *testing.T) {
	c := newTestContext(httptest.NewRecorder())
	info := infoWith()
	req := &dto.ClaudeRequest{
		Model:     "sonar",
		MaxTokens: lo.ToPtr(uint(100)),
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
	}
	out, err := (&Adaptor{}).ConvertClaudeRequest(c, info, req)
	require.NoError(t, err)
	assert.NotNil(t, out)
}

// ---- getters and unimplemented ----

func TestGettersAndUnimplemented(t *testing.T) {
	a := &Adaptor{}
	assert.Equal(t, ChannelName, a.GetChannelName())
	assert.Equal(t, ModelList, a.GetModelList())

	c := newTestContext(httptest.NewRecorder())
	info := infoWith()
	a.Init(info)

	_, err := a.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{})
	assert.Error(t, err)
	_, err = a.ConvertAudioRequest(c, info, dto.AudioRequest{})
	assert.Error(t, err)
	_, err = a.ConvertImageRequest(c, info, dto.ImageRequest{})
	assert.Error(t, err)
	_, err = a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{})
	assert.Error(t, err)
}
