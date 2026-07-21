package siliconflow

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertImageRequest_UsesExtraSpecificFields(t *testing.T) {
	req := dto.ImageRequest{
		Model:  "flux",
		Prompt: "a cat",
		Extra: map[string]json.RawMessage{
			"image_size": json.RawMessage(`"1024x1024"`),
			"batch_size": json.RawMessage(`3`),
			"seed":       json.RawMessage(`42`),
		},
	}
	out, err := (&Adaptor{}).ConvertImageRequest(nil, nil, req)
	require.NoError(t, err)
	sf := out.(*SFImageRequest)
	assert.Equal(t, "flux", sf.Model)
	assert.Equal(t, "a cat", sf.Prompt)
	assert.Equal(t, "1024x1024", sf.ImageSize)
	assert.Equal(t, uint(3), sf.BatchSize)
	assert.Equal(t, uint64(42), sf.Seed)
}

func TestConvertImageRequest_FallbackToOpenAISizeAndN(t *testing.T) {
	req := dto.ImageRequest{
		Model:  "flux",
		Prompt: "a dog",
		Size:   "512x512",
		N:      lo.ToPtr(uint(2)),
	}
	out, err := (&Adaptor{}).ConvertImageRequest(nil, nil, req)
	require.NoError(t, err)
	sf := out.(*SFImageRequest)
	assert.Equal(t, "512x512", sf.ImageSize)
	assert.Equal(t, uint(2), sf.BatchSize)
}

func TestConvertImageRequest_NoExtraNoOptions(t *testing.T) {
	req := dto.ImageRequest{Model: "flux", Prompt: "x"}
	out, err := (&Adaptor{}).ConvertImageRequest(nil, nil, req)
	require.NoError(t, err)
	sf := out.(*SFImageRequest)
	assert.Equal(t, "", sf.ImageSize)
	assert.Equal(t, uint(0), sf.BatchSize)
}

func TestConvertOpenAIRequest_FIMAddsEmptyUserMessage(t *testing.T) {
	prefix := any("def foo():")
	req := &dto.GeneralOpenAIRequest{Model: "deepseek-coder", Prefix: prefix}
	out, err := (&Adaptor{}).ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{}, req)
	require.NoError(t, err)
	converted := out.(*dto.GeneralOpenAIRequest)
	require.Len(t, converted.Messages, 1)
	assert.Equal(t, "user", converted.Messages[0].Role)
}

func TestConvertOpenAIRequest_FIMWithMessagesUnchanged(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model:    "deepseek-coder",
		Prefix:   any("x"),
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	out, err := (&Adaptor{}).ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{}, req)
	require.NoError(t, err)
	converted := out.(*dto.GeneralOpenAIRequest)
	require.Len(t, converted.Messages, 1)
	assert.Equal(t, "hi", converted.Messages[0].Content)
}

func TestConvertOpenAIRequest_NoFIMUnchanged(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{Model: "chat"}
	out, err := (&Adaptor{}).ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{}, req)
	require.NoError(t, err)
	converted := out.(*dto.GeneralOpenAIRequest)
	assert.Len(t, converted.Messages, 0)
}

func TestGetRequestURL_Rerank(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode:   constant.RelayModeRerank,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.siliconflow.cn"},
	}
	url, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api.siliconflow.cn/v1/rerank", url)
}

func TestGetRequestURL_Default(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode:      constant.RelayModeChatCompletions,
		RequestURLPath: "/v1/chat/completions",
		ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.siliconflow.cn"},
	}
	url, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api.siliconflow.cn/v1/chat/completions", url)
}

func TestSiliconflowRerankHandler_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", nil)

	payload := SFRerankResponse{
		Results: []dto.RerankResponseResult{{Index: 0, RelevanceScore: 0.9}},
		Meta:    SFMeta{Tokens: SFTokens{InputTokens: 12, OutputTokens: 3}},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}

	usage, apiErr := siliconflowRerankHandler(c, &relaycommon.RelayInfo{}, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 12, usage.PromptTokens)
	assert.Equal(t, 3, usage.CompletionTokens)
	assert.Equal(t, 15, usage.TotalTokens)
}

func TestSiliconflowRerankHandler_MalformedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte("not json"))),
	}
	usage, apiErr := siliconflowRerankHandler(c, &relaycommon.RelayInfo{}, resp)
	require.NotNil(t, apiErr)
	assert.Nil(t, usage)
}

func TestConvertRerankAndEmbeddingPassThrough(t *testing.T) {
	a := &Adaptor{}
	rr := dto.RerankRequest{Query: "q"}
	out, err := a.ConvertRerankRequest(nil, 0, rr)
	require.NoError(t, err)
	assert.Equal(t, rr, out)

	er := dto.EmbeddingRequest{Model: "m"}
	out2, err := a.ConvertEmbeddingRequest(nil, nil, er)
	require.NoError(t, err)
	assert.Equal(t, er, out2)
}

func TestUnimplemented(t *testing.T) {
	a := &Adaptor{}
	_, err := a.ConvertGeminiRequest(nil, nil, nil)
	assert.Error(t, err)
	_, err = a.ConvertOpenAIResponsesRequest(nil, nil, dto.OpenAIResponsesRequest{})
	assert.Error(t, err)
}

func TestMetadata(t *testing.T) {
	a := &Adaptor{}
	assert.Equal(t, "siliconflow", a.GetChannelName())
	assert.NotEmpty(t, a.GetModelList())
}
