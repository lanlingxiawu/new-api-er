package zhipu_4v

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

func newTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
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

// ---- GetRequestURL ----

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}

	tests := []struct {
		name        string
		baseUrl     string
		relayFormat types.RelayFormat
		relayMode   int
		want        string
	}{
		{"default chat", "https://open.bigmodel.cn", "", relayconstant.RelayModeChatCompletions, "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
		{"embeddings", "https://open.bigmodel.cn", "", relayconstant.RelayModeEmbeddings, "https://open.bigmodel.cn/api/paas/v4/embeddings"},
		{"image generation", "https://open.bigmodel.cn", "", relayconstant.RelayModeImagesGenerations, "https://open.bigmodel.cn/api/paas/v4/images/generations"},
		{"claude format", "https://open.bigmodel.cn", types.RelayFormatClaude, relayconstant.RelayModeChatCompletions, "https://open.bigmodel.cn/api/anthropic/v1/messages"},
		{"empty base falls back to default", "", "", relayconstant.RelayModeChatCompletions, "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
		{"special base claude", "glm-coding-plan", types.RelayFormatClaude, relayconstant.RelayModeChatCompletions, "https://open.bigmodel.cn/api/anthropic/v1/messages"},
		{"special base chat", "glm-coding-plan", "", relayconstant.RelayModeChatCompletions, "https://open.bigmodel.cn/api/coding/paas/v4/chat/completions"},
		{"special base embeddings", "glm-coding-plan", "", relayconstant.RelayModeEmbeddings, "https://open.bigmodel.cn/api/coding/paas/v4/embeddings"},
		{"special base image", "glm-coding-plan", "", relayconstant.RelayModeImagesGenerations, "https://open.bigmodel.cn/api/coding/paas/v4/images/generations"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				RelayFormat: tt.relayFormat,
				RelayMode:   tt.relayMode,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: tt.baseUrl},
			}
			got, err := a.GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---- SetupRequestHeader ----

func TestSetupRequestHeader(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "secret"}}
	info.ApiKey = "secret"
	h := http.Header{}
	require.NoError(t, a.SetupRequestHeader(c, &h, info))
	assert.Equal(t, "Bearer secret", h.Get("Authorization"))
}

// ---- requestOpenAI2Zhipu ----

func TestRequestOpenAI2Zhipu_StringContent(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model:    "glm-4",
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	}
	out := requestOpenAI2Zhipu(req)
	require.Len(t, out.Messages, 1)
	assert.Equal(t, "glm-4", out.Model)
	assert.Equal(t, "hello", out.Messages[0].StringContent())
	assert.Nil(t, out.MaxTokens)
}

func TestRequestOpenAI2Zhipu_ImageBase64Stripping(t *testing.T) {
	raw := `{"model":"glm-4v","messages":[{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:image/png;base64,QUJD"}}]}]}`
	var req dto.GeneralOpenAIRequest
	require.NoError(t, json.Unmarshal([]byte(raw), &req))

	out := requestOpenAI2Zhipu(req)
	require.Len(t, out.Messages, 1)
	marshaled, err := json.Marshal(out.Messages[0].Content)
	require.NoError(t, err)
	// The data: prefix must be stripped, leaving only the base64 payload.
	assert.Contains(t, string(marshaled), "QUJD")
	assert.NotContains(t, string(marshaled), "data:image/png;base64,")
}

func TestRequestOpenAI2Zhipu_StopAndMaxTokens(t *testing.T) {
	t.Run("stop as string", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Messages:  []dto.Message{{Role: "user", Content: "hi"}},
			Stop:      "END",
			MaxTokens: lo.ToPtr(uint(128)),
		}
		out := requestOpenAI2Zhipu(req)
		assert.Equal(t, []string{"END"}, out.Stop)
		require.NotNil(t, out.MaxTokens)
		assert.Equal(t, uint(128), *out.MaxTokens)
	})

	t.Run("stop as slice", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Messages: []dto.Message{{Role: "user", Content: "hi"}},
			Stop:     []string{"a", "b"},
		}
		out := requestOpenAI2Zhipu(req)
		assert.Equal(t, []string{"a", "b"}, out.Stop)
	})
}

// ---- ConvertOpenAIRequest ----

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	t.Run("nil returns error", func(t *testing.T) {
		_, err := a.ConvertOpenAIRequest(c, info, nil)
		require.Error(t, err)
	})

	t.Run("TopP clamped", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Messages: []dto.Message{{Role: "user", Content: "hi"}},
			TopP:     lo.ToPtr(1.2),
		}
		out, err := a.ConvertOpenAIRequest(c, info, req)
		require.NoError(t, err)
		zr := out.(*dto.GeneralOpenAIRequest)
		require.NotNil(t, zr.TopP)
		assert.InDelta(t, 0.99, *zr.TopP, 1e-9)
	})
}

// ---- passthrough converters ----

func TestPassthroughConverters(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	claudeReq := &dto.ClaudeRequest{Model: "glm-4"}
	gotClaude, err := a.ConvertClaudeRequest(c, info, claudeReq)
	require.NoError(t, err)
	assert.Equal(t, claudeReq, gotClaude)

	imgReq := dto.ImageRequest{Model: "glm-4v", Prompt: "cat"}
	gotImg, err := a.ConvertImageRequest(c, info, imgReq)
	require.NoError(t, err)
	assert.Equal(t, imgReq, gotImg)

	embReq := dto.EmbeddingRequest{Model: "embedding-3"}
	gotEmb, err := a.ConvertEmbeddingRequest(c, info, embReq)
	require.NoError(t, err)
	assert.Equal(t, embReq, gotEmb)
}

// ---- zhipu4vImageHandler ----

func TestZhipu4vImageHandler(t *testing.T) {
	t.Run("success with b64_json passes through", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		rec := httptest.NewRecorder()
		c, _ = gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{StartTime: time.Unix(1700000000, 0), ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"created":1700000001,"data":[{"url":"https://example.com/a.png","b64_json":"QUJD"}]}`
		usage, err := zhipu4vImageHandler(c, newResponse(http.StatusOK, body), info)
		require.Nil(t, err)
		require.NotNil(t, usage)
		out := rec.Body.String()
		assert.Contains(t, out, `"b64_json":"QUJD"`)
		assert.Contains(t, out, `"created":1700000001`)
	})

	t.Run("created falls back to start time", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{StartTime: time.Unix(1700000000, 0), ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"data":[{"image_url":"https://example.com/b.png","b64_image":"WFla"}]}`
		usage, err := zhipu4vImageHandler(c, newResponse(http.StatusOK, body), info)
		require.Nil(t, err)
		require.NotNil(t, usage)
		out := rec.Body.String()
		assert.Contains(t, out, `"b64_json":"WFla"`)
		assert.Contains(t, out, `"created":1700000000`)
	})

	t.Run("upstream error returns error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{StartTime: time.Unix(1700000000, 0), ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"error":{"code":"1301","message":"content policy"}}`
		usage, err := zhipu4vImageHandler(c, newResponse(http.StatusBadRequest, body), info)
		require.Nil(t, usage)
		require.NotNil(t, err)
		assert.Contains(t, err.ToOpenAIError().Message, "content policy")
	})

	t.Run("malformed body returns error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{StartTime: time.Unix(1700000000, 0), ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, err := zhipu4vImageHandler(c, newResponse(http.StatusOK, "not-json"), info)
		require.Nil(t, usage)
		require.NotNil(t, err)
	})
}

// ---- getters and unimplemented ----

func TestGettersAndUnimplemented(t *testing.T) {
	a := &Adaptor{}
	assert.Equal(t, ChannelName, a.GetChannelName())
	assert.Equal(t, ModelList, a.GetModelList())

	c := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, err := a.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{})
	assert.Error(t, err)
	_, err = a.ConvertAudioRequest(c, info, dto.AudioRequest{})
	assert.Error(t, err)
	_, err = a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{})
	assert.Error(t, err)
	rr, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	assert.NoError(t, err)
	assert.Nil(t, rr)
	a.Init(info)
}
