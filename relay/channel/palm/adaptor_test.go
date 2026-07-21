package palm

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
	closed chan bool
}

func newCloseNotifyRecorder() *closeNotifyRecorder {
	return &closeNotifyRecorder{httptest.NewRecorder(), make(chan bool, 1)}
}

func (c *closeNotifyRecorder) CloseNotify() <-chan bool { return c.closed }

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

// ---- responsePaLM2OpenAI ----

func TestResponsePaLM2OpenAI(t *testing.T) {
	resp := &PaLMChatResponse{
		Candidates: []PaLMChatMessage{
			{Author: "1", Content: "first"},
			{Author: "1", Content: "second"},
		},
	}
	out := responsePaLM2OpenAI(resp)
	require.Len(t, out.Choices, 2)
	assert.Equal(t, "first", out.Choices[0].Message.StringContent())
	assert.Equal(t, "assistant", out.Choices[0].Message.Role)
	assert.Equal(t, "stop", out.Choices[0].FinishReason)
	assert.Equal(t, 1, out.Choices[1].Index)
}

func TestResponsePaLM2OpenAI_Empty(t *testing.T) {
	out := responsePaLM2OpenAI(&PaLMChatResponse{})
	assert.Len(t, out.Choices, 0)
}

// ---- streamResponsePaLM2OpenAI ----

func TestStreamResponsePaLM2OpenAI(t *testing.T) {
	t.Run("with candidate", func(t *testing.T) {
		out := streamResponsePaLM2OpenAI(&PaLMChatResponse{
			Candidates: []PaLMChatMessage{{Content: "hi"}},
		})
		require.Len(t, out.Choices, 1)
		assert.Equal(t, "hi", out.Choices[0].Delta.GetContentString())
		assert.Equal(t, "palm2", out.Model)
		assert.Equal(t, "chat.completion.chunk", out.Object)
		require.NotNil(t, out.Choices[0].FinishReason)
		assert.Equal(t, "stop", *out.Choices[0].FinishReason)
	})
	t.Run("no candidate leaves empty delta", func(t *testing.T) {
		out := streamResponsePaLM2OpenAI(&PaLMChatResponse{})
		require.Len(t, out.Choices, 1)
		assert.Equal(t, "", out.Choices[0].Delta.GetContentString())
	})
}

// ---- GetRequestURL / SetupRequestHeader ----

func TestGetRequestURL(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://palm.example"}}
	got, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://palm.example/v1beta2/models/chat-bison-001:generateMessage", got)
}

func TestSetupRequestHeader(t *testing.T) {
	c := newTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.ApiKey = "goog-key"
	h := http.Header{}
	require.NoError(t, (&Adaptor{}).SetupRequestHeader(c, &h, info))
	assert.Equal(t, "goog-key", h.Get("x-goog-api-key"))
}

// ---- ConvertOpenAIRequest ----

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	t.Run("nil returns error", func(t *testing.T) {
		_, err := a.ConvertOpenAIRequest(c, info, nil)
		require.Error(t, err)
	})
	t.Run("passthrough", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "PaLM-2"}
		out, err := a.ConvertOpenAIRequest(c, info, req)
		require.NoError(t, err)
		assert.Equal(t, req, out)
	})
}

// ---- palmHandler (non-stream) ----

func TestPalmHandler(t *testing.T) {
	a := &Adaptor{}
	t.Run("success writes OpenAI response", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "PaLM-2"}}
		body := `{"candidates":[{"author":"1","content":"palm answer"}]}`
		usage, err := a.DoResponse(c, newResponse(http.StatusOK, body), info)
		require.Nil(t, err)
		require.NotNil(t, usage)
		out := rec.Body.String()
		assert.Contains(t, out, "palm answer")
		assert.Greater(t, usage.(*dto.Usage).CompletionTokens, 0)
	})
	t.Run("upstream error returned", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"error":{"code":400,"message":"bad request","status":"INVALID_ARGUMENT"}}`
		usage, err := palmHandler(c, info, newResponse(http.StatusBadRequest, body))
		require.Nil(t, usage)
		require.NotNil(t, err)
		assert.Contains(t, err.ToOpenAIError().Message, "bad request")
	})
	t.Run("no candidates returns error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, err := palmHandler(c, info, newResponse(http.StatusOK, `{"candidates":[]}`))
		require.Nil(t, usage)
		require.NotNil(t, err)
	})
	t.Run("malformed body returns error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, err := palmHandler(c, info, newResponse(http.StatusOK, "not-json"))
		require.Nil(t, usage)
		require.NotNil(t, err)
	})
}

// ---- palmStreamHandler (via DoResponse) ----

func TestPalmStreamHandler(t *testing.T) {
	rec := newCloseNotifyRecorder()
	c := newTestContext(rec)
	c.Writer.(interface{ Flush() }).Flush()
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "PaLM-2"}}
	body := `{"candidates":[{"author":"1","content":"streamed content"}]}`
	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, body), info)
	require.Nil(t, err)
	require.NotNil(t, usage)
	out := rec.Body.String()
	assert.Contains(t, out, "streamed content")
	assert.Contains(t, out, "data: [DONE]")
	assert.Greater(t, usage.(*dto.Usage).CompletionTokens, 0)
}

func TestPalmStreamHandler_Malformed(t *testing.T) {
	rec := newCloseNotifyRecorder()
	c := newTestContext(rec)
	c.Writer.(interface{ Flush() }).Flush()
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "PaLM-2"}}
	// malformed body: handler logs and emits only [DONE], responseText stays empty
	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, "not-json"), info)
	require.Nil(t, err)
	require.NotNil(t, usage)
	assert.Contains(t, rec.Body.String(), "data: [DONE]")
	assert.Equal(t, 0, usage.(*dto.Usage).CompletionTokens)
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
	_, err = a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{})
	assert.Error(t, err)
	_, err = a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{})
	assert.Error(t, err)
	rr, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	assert.NoError(t, err)
	assert.Nil(t, rr)
}
