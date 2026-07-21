package dify

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// closeNotifyRecorder adds the http.CloseNotifier interface that gin's
// c.Stream / streaming writers require but httptest.ResponseRecorder does not
// implement.
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
	c.Set("X-Oneapi-Request-Id", "test-req-id")
	return c
}

func newResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func newInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://dify.example.com",
			UpstreamModelName: "gpt-3.5-turbo",
			ApiKey:            "app-secret",
		},
	}
}

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	// StreamScannerHandler builds a time.NewTicker(StreamingTimeout); the env
	// default (common/init.go) is not applied under `go test`, so seed it here
	// to avoid a non-positive interval panic.
	constant.StreamingTimeout = 300
	// uploadDifyFile uses service.GetHttpClient(); initialise it so tests that
	// exercise the file-upload path don't hit a nil client.
	service.InitHttpClient()
	m.Run()
}

// ---- GetRequestURL: bot-type routing (decision/path coverage) ----

func TestGetRequestURL(t *testing.T) {
	cases := []struct {
		name    string
		botType int
		want    string
	}{
		{"workflow", BotTypeWorkFlow, "https://dify.example.com/v1/workflows/run"},
		{"completion", BotTypeCompletion, "https://dify.example.com/v1/completion-messages"},
		{"agent falls through to chat-messages", BotTypeAgent, "https://dify.example.com/v1/chat-messages"},
		{"chatflow default", BotTypeChatFlow, "https://dify.example.com/v1/chat-messages"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Adaptor{BotType: tc.botType}
			got, err := a.GetRequestURL(newInfo())
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// ---- Init sets chatflow default ----

func TestInit(t *testing.T) {
	a := &Adaptor{}
	a.Init(newInfo())
	assert.Equal(t, BotTypeChatFlow, a.BotType)
}

// ---- SetupRequestHeader ----

func TestSetupRequestHeader(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	info := newInfo()
	info.ApiKey = "app-secret"
	h := http.Header{}
	err := a.SetupRequestHeader(c, &h, info)
	require.NoError(t, err)
	assert.Equal(t, "Bearer app-secret", h.Get("Authorization"))
}

// ---- ConvertOpenAIRequest / requestOpenAI2Dify ----

func TestConvertOpenAIRequest_Nil(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	_, err := a.ConvertOpenAIRequest(c, newInfo(), nil)
	require.Error(t, err)
}

func TestConvertOpenAIRequest_MessageRoles(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	req := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{Role: "system", Content: "sys prompt"},
			{Role: "assistant", Content: "prior answer"},
			{Role: "user", Content: "hello"},
		},
	}
	out, err := a.ConvertOpenAIRequest(c, newInfo(), req)
	require.NoError(t, err)
	dr := out.(*DifyChatRequest)
	assert.Contains(t, dr.Query, "SYSTEM: \nsys prompt")
	assert.Contains(t, dr.Query, "ASSISTANT: \nprior answer")
	assert.Contains(t, dr.Query, "USER: \nhello")
	// non-stream default -> blocking
	assert.Equal(t, "blocking", dr.ResponseMode)
	assert.False(t, dr.AutoGenerateName)
	assert.NotNil(t, dr.Inputs)
}

func TestConvertOpenAIRequest_StreamMode(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	req := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
		Stream:   lo.ToPtr(true),
	}
	out, err := a.ConvertOpenAIRequest(c, newInfo(), req)
	require.NoError(t, err)
	dr := out.(*DifyChatRequest)
	assert.Equal(t, "streaming", dr.ResponseMode)
}

func TestConvertOpenAIRequest_ExplicitUser(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	req := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
		User:     []byte(`"alice"`),
	}
	out, err := a.ConvertOpenAIRequest(c, newInfo(), req)
	require.NoError(t, err)
	dr := out.(*DifyChatRequest)
	assert.Equal(t, "alice", dr.User)
}

func TestConvertOpenAIRequest_EmptyUserFallsBackToResponseID(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	req := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	out, err := a.ConvertOpenAIRequest(c, newInfo(), req)
	require.NoError(t, err)
	dr := out.(*DifyChatRequest)
	// GetResponseID is "chatcmpl-<reqid>" which is not valid JSON, so the
	// unmarshal fails and the code falls back to GetResponseID again.
	assert.True(t, strings.HasPrefix(dr.User, "chatcmpl-"))
}

func TestConvertOpenAIRequest_RemoteImageBecomesFile(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	req := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{
				Role: "user",
				Content: []any{
					map[string]any{"type": "text", "text": "look"},
					map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": "https://img.example.com/a.png"},
					},
				},
			},
		},
	}
	out, err := a.ConvertOpenAIRequest(c, newInfo(), req)
	require.NoError(t, err)
	dr := out.(*DifyChatRequest)
	require.Len(t, dr.Files, 1)
	assert.Equal(t, "remote_url", dr.Files[0].TransferMode)
	assert.Equal(t, "https://img.example.com/a.png", dr.Files[0].URL)
	assert.Contains(t, dr.Query, "USER: \nlook")
}

func TestConvertOpenAIRequest_LocalImageUploaded(t *testing.T) {
	// Mock the dify file-upload endpoint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/files/upload", r.URL.Path)
		assert.Equal(t, "Bearer app-secret", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"file-123"}`))
	}))
	defer srv.Close()

	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	info := newInfo()
	info.ChannelBaseUrl = srv.URL

	// 1x1 PNG, base64 data URL -> IsRemoteImage() == false -> upload path.
	dataURL := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	req := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{
				Role: "user",
				Content: []any{
					map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": dataURL},
					},
				},
			},
		},
	}
	out, err := a.ConvertOpenAIRequest(c, info, req)
	require.NoError(t, err)
	dr := out.(*DifyChatRequest)
	require.Len(t, dr.Files, 1)
	assert.Equal(t, "file-123", dr.Files[0].UploadFileId)
	assert.Equal(t, "local_file", dr.Files[0].TransferMode)
	assert.Equal(t, "image", dr.Files[0].Type)
}

func TestConvertOpenAIRequest_LocalImageInvalidBase64(t *testing.T) {
	a := &Adaptor{}
	c := newTestContext(httptest.NewRecorder())
	info := newInfo()

	req := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{
				Role: "user",
				Content: []any{
					map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": "data:image/png;base64,!!!not-base64!!!"},
					},
				},
			},
		},
	}
	out, err := a.ConvertOpenAIRequest(c, info, req)
	require.NoError(t, err)
	dr := out.(*DifyChatRequest)
	// decode fails -> uploadDifyFile returns nil -> no file appended.
	assert.Len(t, dr.Files, 0)
}

// ---- streamResponseDify2OpenAI (event routing) ----

func TestStreamResponseDify2OpenAI_Message(t *testing.T) {
	out := streamResponseDify2OpenAI(DifyChunkChatCompletionResponse{
		Event:  "message",
		Answer: "hello",
	})
	require.Len(t, out.Choices, 1)
	assert.Equal(t, "hello", out.Choices[0].Delta.GetContentString())
	assert.Equal(t, "dify", out.Model)
}

func TestStreamResponseDify2OpenAI_ThinkingMarkers(t *testing.T) {
	open := "<details style=\"color:gray;background-color: #f8f8f8;padding: 8px;border-radius: 4px;\" open> <summary> Thinking... </summary>\n"
	out := streamResponseDify2OpenAI(DifyChunkChatCompletionResponse{Event: "message", Answer: open})
	assert.Equal(t, "<think>", out.Choices[0].Delta.GetContentString())

	out = streamResponseDify2OpenAI(DifyChunkChatCompletionResponse{Event: "message", Answer: "</details>"})
	assert.Equal(t, "</think>", out.Choices[0].Delta.GetContentString())
}

func TestStreamResponseDify2OpenAI_WorkflowNode_DebugOff(t *testing.T) {
	old := constant.DifyDebug
	constant.DifyDebug = false
	defer func() { constant.DifyDebug = old }()

	out := streamResponseDify2OpenAI(DifyChunkChatCompletionResponse{
		Event: "workflow_finished",
		Data:  DifyData{WorkflowId: "wf1", Status: "succeeded"},
	})
	require.Len(t, out.Choices, 1)
	// debug off -> nothing set
	assert.Nil(t, out.Choices[0].Delta.ReasoningContent)
}

func TestStreamResponseDify2OpenAI_WorkflowNode_DebugOn(t *testing.T) {
	old := constant.DifyDebug
	constant.DifyDebug = true
	defer func() { constant.DifyDebug = old }()

	wf := streamResponseDify2OpenAI(DifyChunkChatCompletionResponse{
		Event: "workflow_finished",
		Data:  DifyData{WorkflowId: "wf1", Status: "succeeded"},
	})
	require.NotNil(t, wf.Choices[0].Delta.ReasoningContent)
	assert.Contains(t, *wf.Choices[0].Delta.ReasoningContent, "Workflow: wf1")
	assert.Contains(t, *wf.Choices[0].Delta.ReasoningContent, "succeeded")

	nd := streamResponseDify2OpenAI(DifyChunkChatCompletionResponse{
		Event: "node_finished",
		Data:  DifyData{NodeType: "llm", Status: "done"},
	})
	require.NotNil(t, nd.Choices[0].Delta.ReasoningContent)
	assert.Contains(t, *nd.Choices[0].Delta.ReasoningContent, "Node: llm")
	assert.Contains(t, *nd.Choices[0].Delta.ReasoningContent, "done")
}

// ---- difyHandler (non-stream) ----

func TestDifyHandler_Success(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newTestContext(rec)
	info := newInfo()
	body := `{"conversation_id":"conv-1","answer":"final answer","metadata":{"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}}`
	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, body), info)
	require.Nil(t, err)
	require.NotNil(t, usage)
	assert.Equal(t, 8, usage.(*dto.Usage).TotalTokens)
	out := rec.Body.String()
	assert.Contains(t, out, "final answer")
	assert.Contains(t, out, "chat.completion")
	assert.Contains(t, out, "conv-1")
}

func TestDifyHandler_MalformedBody(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newTestContext(rec)
	usage, err := difyHandler(c, newInfo(), newResponse(http.StatusOK, "not-json"))
	require.Nil(t, usage)
	require.NotNil(t, err)
}

// ---- difyStreamHandler ----

func TestDifyStreamHandler_MessageThenEnd(t *testing.T) {
	rec := newCloseNotifyRecorder()
	c := newTestContext(rec)
	info := newInfo()
	info.IsStream = true

	stream := strings.Join([]string{
		`data: {"event":"message","answer":"Hello"}`,
		`data: {"event":"message","answer":" world"}`,
		`data: {"event":"message_end","metadata":{"usage":{"prompt_tokens":4,"completion_tokens":6,"total_tokens":10}}}`,
		"",
	}, "\n")

	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, stream), info)
	require.Nil(t, err)
	require.NotNil(t, usage)
	assert.Equal(t, 10, usage.(*dto.Usage).TotalTokens)
	body := rec.Body.String()
	assert.Contains(t, body, "Hello")
	assert.Contains(t, body, "world")
	assert.Contains(t, body, "data: [DONE]")
}

func TestDifyStreamHandler_ErrorEvent(t *testing.T) {
	rec := newCloseNotifyRecorder()
	c := newTestContext(rec)
	info := newInfo()
	info.IsStream = true

	stream := strings.Join([]string{
		`data: {"event":"message","answer":"partial"}`,
		`data: {"event":"error"}`,
		"",
	}, "\n")

	// error event stops the stream; handler still returns usage (estimated).
	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, stream), info)
	require.Nil(t, err)
	require.NotNil(t, usage)
	body := rec.Body.String()
	assert.Contains(t, body, "partial")
}

func TestDifyStreamHandler_MalformedChunk(t *testing.T) {
	rec := newCloseNotifyRecorder()
	c := newTestContext(rec)
	info := newInfo()
	info.IsStream = true

	stream := strings.Join([]string{
		`data: {not valid json}`,
		"",
	}, "\n")

	usage, err := (&Adaptor{}).DoResponse(c, newResponse(http.StatusOK, stream), info)
	// malformed chunk triggers sr.Error but handler completes without a fatal error
	require.Nil(t, err)
	require.NotNil(t, usage)
}

// ---- getters / unimplemented converters ----

func TestGettersAndUnimplemented(t *testing.T) {
	a := &Adaptor{}
	assert.Equal(t, ChannelName, a.GetChannelName())
	assert.Equal(t, ModelList, a.GetModelList())

	c := newTestContext(httptest.NewRecorder())
	info := newInfo()

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

	// ConvertClaudeRequest is an intentional panic stub.
	assert.Panics(t, func() {
		_, _ = a.ConvertClaudeRequest(c, info, &dto.ClaudeRequest{})
	})
}
