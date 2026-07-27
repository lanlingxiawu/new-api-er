package coze

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	m.Run()
}

func newContext() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c, rec
}

func newResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// ---- convertCozeChatRequest ----

func TestConvertCozeChatRequest(t *testing.T) {
	c, _ := newContext()
	c.Set("bot_id", "bot-42")

	req := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{Role: "system", Content: "ignored"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "also ignored"},
		},
		Stream: lo.ToPtr(true),
	}
	out := convertCozeChatRequest(c, req)
	assert.Equal(t, "bot-42", out.BotId)
	assert.True(t, out.Stream)
	// only user messages are forwarded.
	require.Len(t, out.AdditionalMessages, 1)
	assert.Equal(t, "user", out.AdditionalMessages[0].Role)
	assert.Equal(t, "text", out.AdditionalMessages[0].ContentType)
	// user id defaults to the generated response id when absent.
	assert.NotEmpty(t, out.UserId)
}

func TestConvertCozeChatRequest_ExplicitUser(t *testing.T) {
	c, _ := newContext()
	req := dto.GeneralOpenAIRequest{
		User:     json.RawMessage("user-99"),
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	out := convertCozeChatRequest(c, req)
	assert.Equal(t, "user-99", string(out.UserId))
	assert.False(t, out.Stream)
}

// ---- ConvertOpenAIRequest ----

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	_, err := a.ConvertOpenAIRequest(c, info, nil)
	require.Error(t, err)

	out, err := a.ConvertOpenAIRequest(c, info, &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	assert.IsType(t, &CozeChatRequest{}, out)
}

// ---- GetRequestURL + SetupRequestHeader ----

func TestGetRequestURLAndHeader(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.coze.com"}}
	url, err := a.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api.coze.com/v3/chat", url)

	c, _ := newContext()
	info2 := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "tok"}}
	info2.ApiKey = "tok"
	h := http.Header{}
	require.NoError(t, a.SetupRequestHeader(c, &h, info2))
	assert.Equal(t, "Bearer tok", h.Get("Authorization"))
}

// ---- cozeChatHandler (non-stream) ----

func TestCozeChatHandler(t *testing.T) {
	a := &Adaptor{}

	t.Run("success builds OpenAI response from answer", func(t *testing.T) {
		c, rec := newContext()
		c.Set("coze_input_count", 4)
		c.Set("coze_output_count", 6)
		c.Set("coze_token_count", 10)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "coze-bot"}}
		body := `{"code":0,"msg":"","data":[{"type":"answer","content":"hello world","created_at":123}]}`
		usage, err := a.DoResponse(c, newResponse(http.StatusOK, body), info)
		require.Nil(t, err)
		require.NotNil(t, usage)
		out := rec.Body.String()
		assert.Contains(t, out, "hello world")
		assert.Equal(t, 10, usage.(*dto.Usage).TotalTokens)
	})

	t.Run("non-zero code returns error", func(t *testing.T) {
		c, _ := newContext()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		body := `{"code":700,"msg":"bot not found","data":[]}`
		usage, err := cozeChatHandler(c, info, newResponse(http.StatusOK, body))
		require.Nil(t, usage)
		require.NotNil(t, err)
		assert.Contains(t, err.Error(), "bot not found")
	})

	t.Run("malformed body returns error", func(t *testing.T) {
		c, _ := newContext()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
		usage, err := cozeChatHandler(c, info, newResponse(http.StatusOK, "not-json"))
		require.Nil(t, usage)
		require.NotNil(t, err)
	})
}

// ---- cozeChatStreamHandler + handleCozeEvent ----

func TestCozeChatStreamHandler(t *testing.T) {
	a := &Adaptor{}
	c, rec := newContext()
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "coze-bot"}}

	stream := strings.Join([]string{
		"event: conversation.message.delta",
		`data: {"role":"assistant","type":"answer","content":"Hi"}`,
		"",
		"event: conversation.chat.completed",
		`data: {"usage":{"token_count":10,"input_count":4,"output_count":6}}`,
		"",
	}, "\n")

	usage, err := a.DoResponse(c, newResponse(http.StatusOK, stream), info)
	require.Nil(t, err)
	require.NotNil(t, usage)
	body := rec.Body.String()
	assert.Contains(t, body, "Hi")
	assert.Contains(t, body, "chat.completion.chunk")
	assert.Contains(t, body, "[DONE]")
	assert.Equal(t, 10, usage.(*dto.Usage).TotalTokens)
	assert.Equal(t, 4, usage.(*dto.Usage).PromptTokens)
}

func TestHandleCozeEvent_ErrorEventDoesNotPanic(t *testing.T) {
	c, _ := newContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage := &dto.Usage{}
	var text string
	// error event is logged, not surfaced; must not panic or mutate usage.
	handleCozeEvent(c, "error", `{"code":123,"message":"boom"}`, &text, usage, "id-1", info)
	assert.Equal(t, 0, usage.TotalTokens)

	// malformed delta payload is swallowed.
	handleCozeEvent(c, "conversation.message.delta", "not-json", &text, usage, "id-1", info)
	assert.Empty(t, text)
}

// ---- checkIfChatComplete / getChatDetail / doRequest (mock coze server) ----

func newMockCozeServer(retrieveBody, listBody string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/chat/retrieve", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(retrieveBody))
	})
	mux.HandleFunc("/v3/chat/message/list", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(listBody))
	})
	return httptest.NewServer(mux)
}

func TestCheckIfChatCompleteAndDetail(t *testing.T) {
	a := &Adaptor{}

	t.Run("completed sets usage in context and detail returns response", func(t *testing.T) {
		srv := newMockCozeServer(
			`{"code":0,"data":{"status":"completed","usage":{"token_count":10,"output_count":6,"input_count":4}}}`,
			`{"code":0,"data":[{"type":"answer","content":"\"hi\""}]}`,
		)
		defer srv.Close()

		c, _ := newContext()
		c.Set("coze_conversation_id", "conv1")
		c.Set("coze_chat_id", "chat1")
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "tok"}}
		info.ApiKey = "tok"

		err, complete := checkIfChatComplete(a, c, info)
		require.NoError(t, err)
		require.True(t, complete)
		assert.Equal(t, 10, c.GetInt("coze_token_count"))
		assert.Equal(t, 4, c.GetInt("coze_input_count"))

		resp, err := getChatDetail(a, c, info)
		require.NoError(t, err)
		require.NotNil(t, resp)
		_ = resp.Body.Close()
	})

	t.Run("in-progress status is not complete", func(t *testing.T) {
		srv := newMockCozeServer(`{"code":0,"data":{"status":"in_progress"}}`, "")
		defer srv.Close()

		c, _ := newContext()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "tok"}}
		info.ApiKey = "tok"
		err, complete := checkIfChatComplete(a, c, info)
		require.NoError(t, err)
		assert.False(t, complete)
	})

	t.Run("failed status returns error", func(t *testing.T) {
		srv := newMockCozeServer(`{"code":0,"data":{"status":"failed"}}`, "")
		defer srv.Close()

		c, _ := newContext()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "tok"}}
		info.ApiKey = "tok"
		err, complete := checkIfChatComplete(a, c, info)
		require.Error(t, err)
		assert.False(t, complete)
		assert.Contains(t, err.Error(), "failed")
	})
}

// ---- getters + unimplemented ----

func TestGettersAndUnimplemented(t *testing.T) {
	a := &Adaptor{}
	assert.Equal(t, ChannelName, a.GetChannelName())
	assert.Equal(t, ModelList, a.GetModelList())

	c, _ := newContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, err := a.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{})
	assert.Error(t, err)
	_, err = a.ConvertAudioRequest(c, info, dto.AudioRequest{})
	assert.Error(t, err)
	_, err = a.ConvertClaudeRequest(c, info, &dto.ClaudeRequest{})
	assert.Error(t, err)
	_, err = a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{})
	assert.Error(t, err)
	_, err = a.ConvertImageRequest(c, info, dto.ImageRequest{})
	assert.Error(t, err)
	_, err = a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{})
	assert.Error(t, err)
	_, err = a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	assert.Error(t, err)
	a.Init(info)
}
