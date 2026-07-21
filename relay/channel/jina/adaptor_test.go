package jina

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
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

// ---- GetRequestURL ----

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}
	base := "https://api.jina.ai"
	t.Run("rerank mode", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeRerank, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: base}}
		got, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, base+"/v1/rerank", got)
	})
	t.Run("embeddings mode", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: base}}
		got, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, base+"/v1/embeddings", got)
	})
	t.Run("invalid mode returns error", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: base}}
		got, err := a.GetRequestURL(info)
		require.Error(t, err)
		assert.Equal(t, "", got)
	})
}

// ---- SetupRequestHeader ----

func TestSetupRequestHeader(t *testing.T) {
	c := newTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.ApiKey = "jina-key"
	h := http.Header{}
	require.NoError(t, (&Adaptor{}).SetupRequestHeader(c, &h, info))
	assert.Equal(t, "Bearer jina-key", h.Get("Authorization"))
}

// ---- Convert* passthroughs ----

func TestConvertOpenAIRequest(t *testing.T) {
	c := newTestContext(httptest.NewRecorder())
	req := &dto.GeneralOpenAIRequest{Model: "jina-clip-v1"}
	out, err := (&Adaptor{}).ConvertOpenAIRequest(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, req)
	require.NoError(t, err)
	assert.Equal(t, req, out)
}

func TestConvertRerankRequest(t *testing.T) {
	c := newTestContext(httptest.NewRecorder())
	req := dto.RerankRequest{Query: "q", Model: "jina-reranker-m0"}
	out, err := (&Adaptor{}).ConvertRerankRequest(c, constant.RelayModeRerank, req)
	require.NoError(t, err)
	assert.Equal(t, req, out)
}

func TestConvertEmbeddingRequest_ClearsEncodingFormat(t *testing.T) {
	c := newTestContext(httptest.NewRecorder())
	req := dto.EmbeddingRequest{Model: "jina-clip-v1", Input: "text", EncodingFormat: "base64"}
	out, err := (&Adaptor{}).ConvertEmbeddingRequest(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, req)
	require.NoError(t, err)
	er := out.(dto.EmbeddingRequest)
	assert.Equal(t, "", er.EncodingFormat)
	assert.Equal(t, "jina-clip-v1", er.Model)
}

// ---- DoResponse: rerank ----

func TestDoResponse_Rerank(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newTestContext(rec)
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeRerank, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"results":[{"index":0,"relevance_score":0.88},{"index":1,"relevance_score":0.42}],"usage":{"total_tokens":15}}`
	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, body), info)
	require.Nil(t, err)
	require.NotNil(t, usage)
	u := usage.(*dto.Usage)
	// RerankHandler sets PromptTokens = TotalTokens
	assert.Equal(t, 15, u.TotalTokens)
	assert.Equal(t, 15, u.PromptTokens)
	assert.Contains(t, rec.Body.String(), "relevance_score")
}

func TestDoResponse_RerankMalformed(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newTestContext(rec)
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeRerank, ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, "{bad"), info)
	require.Nil(t, usage)
	require.NotNil(t, err)
}

// ---- DoResponse: embeddings (delegates to openai handler) ----

func TestDoResponse_Embeddings(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newTestContext(rec)
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "jina-clip-v1"}}
	body := `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"model":"jina-clip-v1","usage":{"prompt_tokens":4,"total_tokens":4}}`
	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, body), info)
	require.Nil(t, err)
	require.NotNil(t, usage)
	assert.Equal(t, 4, usage.(*dto.Usage).TotalTokens)
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
}
