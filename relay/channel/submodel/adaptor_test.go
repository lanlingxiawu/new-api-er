package submodel

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

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

func newInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://api.submodel.ai",
			ApiKey:         "sk-test",
		},
		RequestURLPath: "/v1/chat/completions",
	}
}

// ---- GetRequestURL ----

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}
	got, err := a.GetRequestURL(newInfo())
	require.NoError(t, err)
	assert.Equal(t, "https://api.submodel.ai/v1/chat/completions", got)
}

// ---- SetupRequestHeader ----

func TestSetupRequestHeader(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext()
	info := newInfo()
	info.ApiKey = "sk-test"
	h := http.Header{}
	err := a.SetupRequestHeader(c, &h, info)
	require.NoError(t, err)
	assert.Equal(t, "Bearer sk-test", h.Get("Authorization"))
}

// ---- ConvertOpenAIRequest (passthrough) ----

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext()
	info := newInfo()

	t.Run("nil request returns error", func(t *testing.T) {
		_, err := a.ConvertOpenAIRequest(c, info, nil)
		require.Error(t, err)
	})

	t.Run("valid request passes through unchanged", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Model:    "deepseek-ai/DeepSeek-V3.1",
			Messages: []dto.Message{{Role: "user", Content: "hi"}},
		}
		out, err := a.ConvertOpenAIRequest(c, info, req)
		require.NoError(t, err)
		// passthrough: the exact same pointer is returned.
		assert.Same(t, req, out)
	})
}

// ---- unsupported converters ----

func TestUnsupportedConverters(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext()
	info := newInfo()

	_, err := a.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{})
	assert.Error(t, err)
	_, err = a.ConvertClaudeRequest(c, info, &dto.ClaudeRequest{})
	assert.Error(t, err)
	_, err = a.ConvertAudioRequest(c, info, dto.AudioRequest{})
	assert.Error(t, err)
	_, err = a.ConvertImageRequest(c, info, dto.ImageRequest{})
	assert.Error(t, err)
	_, err = a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	assert.Error(t, err)
	_, err = a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{})
	assert.Error(t, err)
	_, err = a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{})
	assert.Error(t, err)
}

// ---- getters + Init ----

func TestGettersAndInit(t *testing.T) {
	a := &Adaptor{}
	assert.Equal(t, ChannelName, a.GetChannelName())
	assert.Equal(t, ModelList, a.GetModelList())
	assert.NotEmpty(t, a.GetModelList())
	// Init is a no-op but must not panic.
	a.Init(newInfo())
}
