package xai

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func infoWith() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
}

func TestConvertOpenAIRequest_Nil(t *testing.T) {
	_, err := (&Adaptor{}).ConvertOpenAIRequest(nil, infoWith(), nil)
	require.Error(t, err)
}

func TestConvertOpenAIRequest_SearchSuffix(t *testing.T) {
	info := infoWith()
	info.UpstreamModelName = "grok-3-search"
	req := &dto.GeneralOpenAIRequest{Model: "grok-3-search"}

	out, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, req)
	require.NoError(t, err)
	m, ok := out.(map[string]any)
	require.True(t, ok)
	sp, ok := m["search_parameters"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "on", sp["mode"])
	assert.Equal(t, "grok-3", info.UpstreamModelName)
}

func TestConvertOpenAIRequest_Grok3MiniHighEffort(t *testing.T) {
	info := infoWith()
	info.UpstreamModelName = "grok-3-mini-high"
	req := &dto.GeneralOpenAIRequest{
		Model:     "grok-3-mini-high",
		MaxTokens: lo.ToPtr(uint(100)),
	}

	out, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, req)
	require.NoError(t, err)
	converted := out.(*dto.GeneralOpenAIRequest)
	assert.Equal(t, "grok-3-mini", converted.Model)
	assert.Equal(t, "high", converted.ReasoningEffort)
	// max_tokens migrated to max_completion_tokens
	require.NotNil(t, converted.MaxCompletionTokens)
	assert.Equal(t, uint(100), *converted.MaxCompletionTokens)
	assert.Nil(t, converted.MaxTokens)
	assert.Equal(t, "high", info.ReasoningEffort)
}

func TestConvertOpenAIRequest_Grok3MiniLowEffort(t *testing.T) {
	info := infoWith()
	info.UpstreamModelName = "grok-3-mini-low"
	req := &dto.GeneralOpenAIRequest{Model: "grok-3-mini-low"}

	out, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, req)
	require.NoError(t, err)
	converted := out.(*dto.GeneralOpenAIRequest)
	assert.Equal(t, "grok-3-mini", converted.Model)
	assert.Equal(t, "low", converted.ReasoningEffort)
}

func TestConvertOpenAIRequest_PlainModelUnchanged(t *testing.T) {
	info := infoWith()
	info.UpstreamModelName = "grok-4-0709"
	req := &dto.GeneralOpenAIRequest{Model: "grok-4-0709"}

	out, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, req)
	require.NoError(t, err)
	converted := out.(*dto.GeneralOpenAIRequest)
	assert.Equal(t, "grok-4-0709", converted.Model)
	assert.Empty(t, converted.ReasoningEffort)
}

func TestConvertImageRequest_Defaults(t *testing.T) {
	req := dto.ImageRequest{Model: "grok-2-image", Prompt: "a cat"}
	out, err := (&Adaptor{}).ConvertImageRequest(nil, nil, req)
	require.NoError(t, err)
	img := out.(ImageRequest)
	assert.Equal(t, "grok-2-image", img.Model)
	assert.Equal(t, "a cat", img.Prompt)
	assert.Equal(t, 1, img.N) // default N=1 when nil
}

func TestConvertImageRequest_ExplicitN(t *testing.T) {
	req := dto.ImageRequest{Model: "grok-2-image", Prompt: "x", N: lo.ToPtr(uint(4)), ResponseFormat: "b64_json"}
	out, err := (&Adaptor{}).ConvertImageRequest(nil, nil, req)
	require.NoError(t, err)
	img := out.(ImageRequest)
	assert.Equal(t, 4, img.N)
	assert.Equal(t, "b64_json", img.ResponseFormat)
}

func TestConvertOpenAIResponsesRequest_FillsModelFromInfo(t *testing.T) {
	info := infoWith()
	info.UpstreamModelName = "grok-4-fast-reasoning"
	req := dto.OpenAIResponsesRequest{}
	out, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, req)
	require.NoError(t, err)
	converted := out.(dto.OpenAIResponsesRequest)
	assert.Equal(t, "grok-4-fast-reasoning", converted.Model)
}

func TestStreamResponseXAI2OpenAI_Nil(t *testing.T) {
	assert.Nil(t, streamResponseXAI2OpenAI(nil, &dto.Usage{}))
}

func TestStreamResponseXAI2OpenAI_CopiesFieldsAndUsage(t *testing.T) {
	in := &dto.ChatCompletionsStreamResponse{
		Id:      "id1",
		Object:  "chat.completion.chunk",
		Model:   "grok-3",
		Created: 123,
		Usage:   &dto.Usage{PromptTokens: 10, TotalTokens: 30},
	}
	usage := &dto.Usage{CompletionTokens: 20}
	out := streamResponseXAI2OpenAI(in, usage)
	require.NotNil(t, out)
	assert.Equal(t, "id1", out.Id)
	assert.Equal(t, "grok-3", out.Model)
	require.NotNil(t, out.Usage)
	assert.Equal(t, 20, out.Usage.CompletionTokens)
}

func TestXAIHandler_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	body := []byte(`{"id":"1","object":"chat.completion","model":"grok-3","choices":[],"usage":{"prompt_tokens":10,"total_tokens":25}}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}

	usage, apiErr := xAIHandler(c, infoWith(), resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 10, usage.PromptTokens)
	// completion = total - prompt
	assert.Equal(t, 15, usage.CompletionTokens)
	assert.Equal(t, 25, usage.TotalTokens)
}

func TestXAIHandler_MalformedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte("not json"))),
	}
	usage, apiErr := xAIHandler(c, infoWith(), resp)
	require.NotNil(t, apiErr)
	assert.Nil(t, usage)
}

func TestXAIStreamHandler_WithUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	info := infoWith()
	info.UpstreamModelName = "grok-3"

	chunk := `{"id":"1","object":"chat.completion.chunk","model":"grok-3","choices":[{"index":0,"delta":{"content":"hi"}}],"usage":{"prompt_tokens":5,"total_tokens":12}}`
	streamBody := []byte("data: " + chunk + "\n" + "data: [DONE]\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(streamBody)),
	}

	usage, apiErr := xAIStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 5, usage.PromptTokens)
	assert.Equal(t, 12, usage.TotalTokens)
	assert.Equal(t, 7, usage.CompletionTokens)
}

func TestGetRequestURL(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RequestURLPath: "/v1/chat/completions",
		ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.x.ai"},
	}
	url, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api.x.ai/v1/chat/completions", url)
}

func TestUnimplemented(t *testing.T) {
	a := &Adaptor{}
	_, err := a.ConvertGeminiRequest(nil, nil, nil)
	assert.Error(t, err)
	_, err = a.ConvertClaudeRequest(nil, nil, nil)
	assert.Error(t, err)
	_, err = a.ConvertAudioRequest(nil, nil, dto.AudioRequest{})
	assert.Error(t, err)
	_, err = a.ConvertEmbeddingRequest(nil, nil, dto.EmbeddingRequest{})
	assert.Error(t, err)
	// ConvertRerankRequest returns nil,nil
	out, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	assert.NoError(t, err)
	assert.Nil(t, out)
}

func TestMetadata(t *testing.T) {
	a := &Adaptor{}
	assert.Equal(t, "xai", a.GetChannelName())
	assert.NotEmpty(t, a.GetModelList())
}
