package zhipu

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// closeNotifyRecorder adds the http.CloseNotifier interface that gin's
// c.Stream requires but httptest.ResponseRecorder does not implement.
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

// ---- getZhipuToken ----

func TestGetZhipuToken(t *testing.T) {
	t.Run("valid key returns signed token", func(t *testing.T) {
		tok := getZhipuToken("myid.mysecret")
		require.NotEmpty(t, tok)
		// JWT has three dot-separated segments.
		assert.Equal(t, 2, strings.Count(tok, "."))
	})

	t.Run("cache hit returns same token", func(t *testing.T) {
		key := "cacheid.cachesecret"
		first := getZhipuToken(key)
		second := getZhipuToken(key)
		require.NotEmpty(t, first)
		assert.Equal(t, first, second)
	})

	t.Run("invalid key without dot returns empty", func(t *testing.T) {
		assert.Equal(t, "", getZhipuToken("nodothere"))
	})

	t.Run("invalid key with too many segments returns empty", func(t *testing.T) {
		assert.Equal(t, "", getZhipuToken("a.b.c"))
	})
}

// ---- GetRequestURL ----

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}

	t.Run("non-stream uses invoke", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			IsStream: false,
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelBaseUrl:    "https://open.bigmodel.cn",
				UpstreamModelName: "chatglm_std",
			},
		}
		got, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, "https://open.bigmodel.cn/api/paas/v3/model-api/chatglm_std/invoke", got)
	})

	t.Run("stream uses sse-invoke", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			IsStream: true,
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelBaseUrl:    "https://open.bigmodel.cn",
				UpstreamModelName: "chatglm_std",
			},
		}
		got, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, "https://open.bigmodel.cn/api/paas/v3/model-api/chatglm_std/sse-invoke", got)
	})
}

// ---- SetupRequestHeader ----

func TestSetupRequestHeader(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "myid.mysecret"}}
	info.ApiKey = "myid.mysecret"
	h := http.Header{}
	err := a.SetupRequestHeader(c, &h, info)
	require.NoError(t, err)
	assert.NotEmpty(t, h.Get("Authorization"))
}

// ---- requestOpenAI2Zhipu / ConvertOpenAIRequest ----

func TestRequestOpenAI2Zhipu_SystemMessageExpansion(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{Role: "system", Content: "you are helpful"},
			{Role: "user", Content: "hi"},
		},
		Temperature: lo.ToPtr(0.7),
		TopP:        lo.ToPtr(0.5),
	}
	out := requestOpenAI2Zhipu(req)
	// system message becomes system + a synthetic user "Okay", then the user msg.
	require.Len(t, out.Prompt, 3)
	assert.Equal(t, "system", out.Prompt[0].Role)
	assert.Equal(t, "you are helpful", out.Prompt[0].Content)
	assert.Equal(t, "user", out.Prompt[1].Role)
	assert.Equal(t, "Okay", out.Prompt[1].Content)
	assert.Equal(t, "user", out.Prompt[2].Role)
	assert.Equal(t, "hi", out.Prompt[2].Content)
	assert.Equal(t, 0.5, out.TopP)
}

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	t.Run("nil request returns error", func(t *testing.T) {
		_, err := a.ConvertOpenAIRequest(c, info, nil)
		require.Error(t, err)
	})

	t.Run("TopP >= 1 is clamped to 0.99", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Messages: []dto.Message{{Role: "user", Content: "hi"}},
			TopP:     lo.ToPtr(1.5),
		}
		out, err := a.ConvertOpenAIRequest(c, info, req)
		require.NoError(t, err)
		zr := out.(*ZhipuRequest)
		assert.InDelta(t, 0.99, zr.TopP, 1e-9)
	})

	t.Run("TopP below 1 preserved", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{
			Messages: []dto.Message{{Role: "user", Content: "hi"}},
			TopP:     lo.ToPtr(0.3),
		}
		out, err := a.ConvertOpenAIRequest(c, info, req)
		require.NoError(t, err)
		zr := out.(*ZhipuRequest)
		assert.InDelta(t, 0.3, zr.TopP, 1e-9)
	})
}

// ---- response transforms ----

func TestResponseZhipu2OpenAI(t *testing.T) {
	resp := &ZhipuResponse{
		Success: true,
		Data: ZhipuResponseData{
			TaskId: "task-1",
			Choices: []ZhipuMessage{
				{Role: "assistant", Content: "\"partial\""},
				{Role: "assistant", Content: "\"final\""},
			},
			Usage: dto.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8},
		},
	}
	out := responseZhipu2OpenAI(resp)
	assert.Equal(t, "task-1", out.Id)
	require.Len(t, out.Choices, 2)
	// quotes are trimmed off the content.
	assert.Equal(t, "partial", out.Choices[0].Message.StringContent())
	assert.Equal(t, "", out.Choices[0].FinishReason)
	// only the last choice gets the stop finish reason.
	assert.Equal(t, "stop", out.Choices[1].FinishReason)
	assert.Equal(t, 8, out.Usage.TotalTokens)
}

func TestStreamResponseZhipu2OpenAI(t *testing.T) {
	out := streamResponseZhipu2OpenAI("hello world")
	require.Len(t, out.Choices, 1)
	assert.Equal(t, "chatglm", out.Model)
	assert.Equal(t, "hello world", out.Choices[0].Delta.GetContentString())
}

func TestStreamMetaResponseZhipu2OpenAI(t *testing.T) {
	meta := &ZhipuStreamMetaResponse{
		RequestId: "req-1",
		Usage:     dto.Usage{PromptTokens: 2, CompletionTokens: 4, TotalTokens: 6},
	}
	out, usage := streamMetaResponseZhipu2OpenAI(meta)
	assert.Equal(t, "req-1", out.Id)
	require.Len(t, out.Choices, 1)
	require.NotNil(t, out.Choices[0].FinishReason)
	assert.Equal(t, "stop", *out.Choices[0].FinishReason)
	require.NotNil(t, usage)
	assert.Equal(t, 6, usage.TotalTokens)
}

// ---- zhipuHandler (non-stream) ----

func TestZhipuHandler(t *testing.T) {
	a := &Adaptor{}

	t.Run("success writes OpenAI response", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"success":true,"code":200,"data":{"task_id":"t1","choices":[{"role":"assistant","content":"hi there"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}}`
		usage, err := a.DoResponse(c, newResponse(http.StatusOK, body), info)
		require.Nil(t, err)
		require.NotNil(t, usage)
		out := rec.Body.String()
		assert.Contains(t, out, "hi there")
		assert.Contains(t, out, "chat.completion")
	})

	t.Run("upstream failure returns error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"success":false,"code":400,"msg":"bad thing"}`
		usage, err := a.DoResponse(c, newResponse(http.StatusBadRequest, body), info)
		require.Nil(t, usage)
		require.NotNil(t, err)
		assert.Contains(t, err.ToOpenAIError().Message, "bad thing")
	})

	t.Run("malformed body returns error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, err := zhipuHandler(c, info, newResponse(http.StatusOK, "not-json"))
		require.Nil(t, usage)
		require.NotNil(t, err)
	})
}

// ---- zhipuStreamHandler ----

func TestZhipuStreamHandler(t *testing.T) {
	rec := newCloseNotifyRecorder()
	c := newTestContext(rec)
	c.Writer.(interface{ Flush() }).Flush()
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{}}

	stream := strings.Join([]string{
		"data: Hello",
		"data:  world",
		`meta: {"request_id":"r1","task_status":"finish","usage":{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12}}`,
		"",
	}, "\n")

	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, stream), info)
	require.Nil(t, err)
	require.NotNil(t, usage)
	body := rec.Body.String()
	assert.Contains(t, body, "Hello")
	assert.Contains(t, body, "data: [DONE]")
	assert.Equal(t, 12, usage.(*dto.Usage).TotalTokens)
}

// ---- trivial getters and unimplemented converters ----

func TestGettersAndUnimplemented(t *testing.T) {
	a := &Adaptor{}
	assert.Equal(t, ChannelName, a.GetChannelName())
	assert.Equal(t, ModelList, a.GetModelList())

	c := newTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

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

	a.Init(info)
}
