package cohere

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
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

// ---- requestOpenAI2Cohere ----

func TestRequestOpenAI2Cohere_RolesAndMessage(t *testing.T) {
	common.CohereSafetySetting = "NONE"
	req := dto.GeneralOpenAIRequest{
		Model: "command-r",
		Messages: []dto.Message{
			{Role: "system", Content: "sys prompt"},
			{Role: "assistant", Content: "prior reply"},
			{Role: "tool", Content: "tool out"},
			{Role: "user", Content: "hello"},
		},
	}
	out := requestOpenAI2Cohere(req)
	assert.Equal(t, "command-r", out.Model)
	// user message becomes the top-level Message
	assert.Equal(t, "hello", out.Message)
	// non-user messages go to chat history with mapped roles
	require.Len(t, out.ChatHistory, 3)
	assert.Equal(t, "SYSTEM", out.ChatHistory[0].Role)
	assert.Equal(t, "CHATBOT", out.ChatHistory[1].Role)
	assert.Equal(t, "USER", out.ChatHistory[2].Role) // unknown role -> USER
	// max tokens defaults to 4000 when unset
	assert.Equal(t, uint(4000), out.MaxTokens)
	// safety mode omitted when NONE
	assert.Equal(t, "", out.SafetyMode)
}

func TestRequestOpenAI2Cohere_SafetyModeApplied(t *testing.T) {
	common.CohereSafetySetting = "CONTEXTUAL"
	defer func() { common.CohereSafetySetting = "NONE" }()
	req := dto.GeneralOpenAIRequest{
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
		MaxTokens: lo.ToPtr(uint(123)),
	}
	out := requestOpenAI2Cohere(req)
	assert.Equal(t, "CONTEXTUAL", out.SafetyMode)
	// explicit max tokens preserved
	assert.Equal(t, uint(123), out.MaxTokens)
}

func TestRequestOpenAI2Cohere_StreamFlag(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
		Stream:   lo.ToPtr(true),
	}
	out := requestOpenAI2Cohere(req)
	assert.True(t, out.Stream)
}

// ---- requestConvertRerank2Cohere ----

func TestRequestConvertRerank2Cohere(t *testing.T) {
	t.Run("nil top_n defaults to 1", func(t *testing.T) {
		out := requestConvertRerank2Cohere(dto.RerankRequest{Query: "q", Model: "rerank-english-v3.0"})
		assert.Equal(t, 1, out.TopN)
		assert.True(t, out.ReturnDocuments)
		assert.Equal(t, "q", out.Query)
	})
	t.Run("non-positive top_n clamped to 1", func(t *testing.T) {
		out := requestConvertRerank2Cohere(dto.RerankRequest{TopN: lo.ToPtr(0)})
		assert.Equal(t, 1, out.TopN)
	})
	t.Run("negative top_n clamped to 1", func(t *testing.T) {
		out := requestConvertRerank2Cohere(dto.RerankRequest{TopN: lo.ToPtr(-5)})
		assert.Equal(t, 1, out.TopN)
	})
	t.Run("positive top_n preserved", func(t *testing.T) {
		out := requestConvertRerank2Cohere(dto.RerankRequest{TopN: lo.ToPtr(7), Documents: []any{"a", "b"}})
		assert.Equal(t, 7, out.TopN)
		require.Len(t, out.Documents, 2)
	})
}

// ---- stopReasonCohere2OpenAI ----

func TestStopReasonCohere2OpenAI(t *testing.T) {
	assert.Equal(t, "stop", stopReasonCohere2OpenAI("COMPLETE"))
	assert.Equal(t, "max_tokens", stopReasonCohere2OpenAI("MAX_TOKENS"))
	assert.Equal(t, "OTHER", stopReasonCohere2OpenAI("OTHER"))
}

// ---- GetRequestURL ----

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}
	t.Run("rerank mode", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeRerank, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.cohere.ai"}}
		got, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, "https://api.cohere.ai/v1/rerank", got)
	})
	t.Run("chat mode", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.cohere.ai"}}
		got, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, "https://api.cohere.ai/v1/chat", got)
	})
}

// ---- SetupRequestHeader ----

func TestSetupRequestHeader(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.ApiKey = "secret-key"
	h := http.Header{}
	require.NoError(t, a.SetupRequestHeader(c, &h, info))
	assert.Equal(t, "Bearer secret-key", h.Get("Authorization"))
}

// ---- ConvertOpenAIRequest / ConvertRerankRequest ----

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	out, err := a.ConvertOpenAIRequest(c, info, req)
	require.NoError(t, err)
	_, ok := out.(*CohereRequest)
	assert.True(t, ok)
}

func TestConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	out, err := a.ConvertRerankRequest(c, constant.RelayModeRerank, dto.RerankRequest{Query: "q"})
	require.NoError(t, err)
	cr, ok := out.(*CohereRerankRequest)
	require.True(t, ok)
	assert.Equal(t, "q", cr.Query)
}

// ---- cohereHandler (non-stream) ----

func TestCohereHandler(t *testing.T) {
	a := &Adaptor{}
	t.Run("success maps to OpenAI text response", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command-r"}}
		body := `{"response_id":"r1","text":"the answer","finish_reason":"COMPLETE","meta":{"billed_units":{"input_tokens":11,"output_tokens":7}}}`
		usage, err := a.DoResponse(c, newResponse(http.StatusOK, body), info)
		require.Nil(t, err)
		require.NotNil(t, usage)
		u := usage.(*dto.Usage)
		assert.Equal(t, 11, u.PromptTokens)
		assert.Equal(t, 7, u.CompletionTokens)
		assert.Equal(t, 18, u.TotalTokens)
		out := rec.Body.String()
		assert.Contains(t, out, "the answer")
		assert.Contains(t, out, "chat.completion")
		assert.Contains(t, out, "\"finish_reason\":\"stop\"")
	})
	t.Run("malformed body returns error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, err := cohereHandler(c, info, newResponse(http.StatusOK, "not-json"))
		require.Nil(t, usage)
		require.NotNil(t, err)
	})
}

// ---- cohereRerankHandler ----

func TestCohereRerankHandler(t *testing.T) {
	t.Run("billed units present", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeRerank, ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"results":[{"index":0,"relevance_score":0.9}],"meta":{"billed_units":{"input_tokens":20,"output_tokens":0}}}`
		usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, body), info)
		require.Nil(t, err)
		u := usage.(*dto.Usage)
		assert.Equal(t, 20, u.PromptTokens)
		assert.Equal(t, 20, u.TotalTokens)
		assert.Contains(t, rec.Body.String(), "relevance_score")
	})
	t.Run("zero billed units falls back to estimate", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeRerank, ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"results":[{"index":0,"relevance_score":0.5}],"meta":{"billed_units":{"input_tokens":0,"output_tokens":0}}}`
		usage, err := cohereRerankHandler(c, newResponse(http.StatusOK, body), info)
		require.Nil(t, err)
		require.NotNil(t, usage)
		// estimate prompt tokens is 0 in a bare RelayInfo, so total is 0
		assert.Equal(t, 0, usage.CompletionTokens)
	})
	t.Run("malformed body returns error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c := newTestContext(rec)
		info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeRerank, ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, err := cohereRerankHandler(c, newResponse(http.StatusOK, "{bad"), info)
		require.Nil(t, usage)
		require.NotNil(t, err)
	})
}

// ---- cohereStreamHandler ----

func TestCohereStreamHandler(t *testing.T) {
	rec := newCloseNotifyRecorder()
	c := newTestContext(rec)
	c.Writer.(interface{ Flush() }).Flush()
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command-r"}}

	stream := strings.Join([]string{
		`{"is_finished":false,"event_type":"text-generation","text":"Hello"}`,
		`{"is_finished":false,"event_type":"text-generation","text":" world"}`,
		`{"is_finished":true,"event_type":"stream-end","finish_reason":"COMPLETE","response":{"meta":{"billed_units":{"input_tokens":4,"output_tokens":2}}}}`,
		"",
	}, "\n")

	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, stream), info)
	require.Nil(t, err)
	require.NotNil(t, usage)
	body := rec.Body.String()
	assert.Contains(t, body, "Hello")
	assert.Contains(t, body, "world")
	assert.Contains(t, body, "data: [DONE]")
	u := usage.(*dto.Usage)
	assert.Equal(t, 4, u.PromptTokens)
	assert.Equal(t, 2, u.CompletionTokens)
}

func TestCohereStreamHandler_NoBilledUnitsEstimates(t *testing.T) {
	rec := newCloseNotifyRecorder()
	c := newTestContext(rec)
	c.Writer.(interface{ Flush() }).Flush()
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command-r"}}

	stream := strings.Join([]string{
		`{"is_finished":false,"event_type":"text-generation","text":"hi there"}`,
		`{"is_finished":true,"event_type":"stream-end","finish_reason":"COMPLETE"}`,
		"",
	}, "\n")

	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, stream), info)
	require.Nil(t, err)
	require.NotNil(t, usage)
	// no billed units -> completion tokens estimated from response text (> 0)
	assert.Greater(t, usage.(*dto.Usage).CompletionTokens, 0)
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
}
