package mokaai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"

	"github.com/gin-gonic/gin"
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

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

// ---- embeddingRequestOpenAI2Moka ----

func TestEmbeddingRequestOpenAI2Moka_InputForms(t *testing.T) {
	t.Run("string input", func(t *testing.T) {
		out := embeddingRequestOpenAI2Moka(dto.GeneralOpenAIRequest{Model: "m3e-base", Input: "hello"})
		require.Equal(t, []string{"hello"}, out.Input)
		assert.Equal(t, "m3e-base", out.Model)
	})
	t.Run("[]string input", func(t *testing.T) {
		out := embeddingRequestOpenAI2Moka(dto.GeneralOpenAIRequest{Input: []string{"a", "b"}})
		assert.Equal(t, []string{"a", "b"}, out.Input)
	})
	t.Run("[]interface{} input filters non-strings", func(t *testing.T) {
		out := embeddingRequestOpenAI2Moka(dto.GeneralOpenAIRequest{Input: []interface{}{"x", 42, "y"}})
		assert.Equal(t, []string{"x", "y"}, out.Input)
	})
	t.Run("unsupported input type yields nil slice", func(t *testing.T) {
		out := embeddingRequestOpenAI2Moka(dto.GeneralOpenAIRequest{Input: 123})
		assert.Nil(t, out.Input)
	})
}

// ---- embeddingResponseMoka2OpenAI ----

func TestEmbeddingResponseMoka2OpenAI(t *testing.T) {
	resp := &dto.EmbeddingResponse{
		Data: []dto.EmbeddingResponseItem{
			{Object: "embedding", Index: 0, Embedding: []float64{0.1, 0.2}},
			{Object: "embedding", Index: 1, Embedding: []float64{0.3}},
		},
		Usage: dto.Usage{PromptTokens: 5, TotalTokens: 5},
	}
	out := embeddingResponseMoka2OpenAI(resp)
	assert.Equal(t, "list", out.Object)
	assert.Equal(t, "baidu-embedding", out.Model)
	require.Len(t, out.Data, 2)
	assert.Equal(t, 0, out.Data[0].Index)
	assert.Equal(t, []float64{0.1, 0.2}, out.Data[0].Embedding)
	assert.Equal(t, 5, out.Usage.TotalTokens)
}

// ---- GetRequestURL ----

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}
	base := "https://moka.example"
	t.Run("m3e model uses embeddings suffix", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: base, UpstreamModelName: "m3e-large"}}
		got, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, base+"/embeddings", got)
	})
	t.Run("non-m3e model uses chat suffix", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: base, UpstreamModelName: "other-model"}}
		got, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, base+"/chat/", got)
	})
}

// ---- SetupRequestHeader ----

func TestSetupRequestHeader(t *testing.T) {
	c := newTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.ApiKey = "moka-key"
	h := http.Header{}
	require.NoError(t, (&Adaptor{}).SetupRequestHeader(c, &h, info))
	assert.Equal(t, "Bearer moka-key", h.Get("Authorization"))
}

// ---- ConvertOpenAIRequest ----

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	t.Run("nil returns error", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.ConvertOpenAIRequest(c, info, nil)
		require.Error(t, err)
	})
	t.Run("embeddings mode converts", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{}}
		out, err := a.ConvertOpenAIRequest(c, info, &dto.GeneralOpenAIRequest{Model: "m3e-base", Input: "hi"})
		require.NoError(t, err)
		er := out.(*dto.EmbeddingRequest)
		assert.Equal(t, []string{"hi"}, er.Input)
	})
	t.Run("non-embeddings mode returns error", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}
		_, err := a.ConvertOpenAIRequest(c, info, &dto.GeneralOpenAIRequest{Model: "x"})
		require.Error(t, err)
	})
}

// ---- ConvertEmbeddingRequest passthrough ----

func TestConvertEmbeddingRequest(t *testing.T) {
	c := newTestContext(httptest.NewRecorder())
	req := dto.EmbeddingRequest{Model: "m3e-base", Input: "hi"}
	out, err := (&Adaptor{}).ConvertEmbeddingRequest(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, req)
	require.NoError(t, err)
	assert.Equal(t, req, out)
}

// ---- DoResponse ----

func TestDoResponse_Embeddings(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newTestContext(rec)
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.5,0.6]}],"usage":{"prompt_tokens":3,"total_tokens":3}}`
	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, body), info)
	require.Nil(t, err)
	require.NotNil(t, usage)
	assert.Equal(t, 3, usage.(*dto.Usage).TotalTokens)
	out := rec.Body.String()
	assert.Contains(t, out, "baidu-embedding")
	assert.Contains(t, out, "embedding")
}

func TestDoResponse_EmbeddingsMalformed(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newTestContext(rec)
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, "not-json"), info)
	require.Nil(t, usage)
	require.NotNil(t, err)
}

func TestDoResponse_DefaultModeReturnsNil(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newTestContext(rec)
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, "{}"), info)
	assert.Nil(t, usage)
	assert.Nil(t, err)
}

// ---- getters and unimplemented ----

func TestGettersAndUnimplemented(t *testing.T) {
	a := &Adaptor{}
	assert.Equal(t, ChannelName, a.GetChannelName())
	assert.Equal(t, ModelList, a.GetModelList())

	c := newTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	a.Init(info)

	_, err := a.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{})
	assert.Error(t, err)
	_, err = a.ConvertAudioRequest(c, info, dto.AudioRequest{})
	assert.Error(t, err)
	_, err = a.ConvertImageRequest(c, info, dto.ImageRequest{})
	assert.Error(t, err)
	_, err = a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{})
	assert.Error(t, err)
	rr, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	assert.NoError(t, err)
	assert.Nil(t, rr)
}
