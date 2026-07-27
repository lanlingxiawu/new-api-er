package helper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newWriterContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, rec
}

// cancelledContext returns a gin.Context whose request context is already done.
func cancelledContext(t *testing.T) *gin.Context {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	return c
}

// ---------------------------------------------------------------------------
// FlushWriter / requestContextDone / SetEventStreamHeaders
// ---------------------------------------------------------------------------

func TestFlushWriter(t *testing.T) {
	t.Run("nil context returns nil", func(t *testing.T) {
		require.NoError(t, FlushWriter(nil))
	})
	t.Run("flushes recorder", func(t *testing.T) {
		c, _ := newWriterContext(t)
		require.NoError(t, FlushWriter(c))
	})
	t.Run("cancelled request context returns error", func(t *testing.T) {
		c := cancelledContext(t)
		err := FlushWriter(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "request context done")
	})
}

func TestRequestContextDone(t *testing.T) {
	require.False(t, requestContextDone(nil))
	c, _ := newWriterContext(t)
	require.False(t, requestContextDone(c))
	require.True(t, requestContextDone(cancelledContext(t)))
}

func TestSetEventStreamHeaders(t *testing.T) {
	c, _ := newWriterContext(t)
	SetEventStreamHeaders(c)
	require.Equal(t, "text/event-stream", c.Writer.Header().Get("Content-Type"))
	require.Equal(t, "no-cache", c.Writer.Header().Get("Cache-Control"))
	require.Equal(t, "no", c.Writer.Header().Get("X-Accel-Buffering"))

	// Second call is a no-op (guarded by event_stream_headers_set).
	c.Writer.Header().Set("Content-Type", "changed")
	SetEventStreamHeaders(c)
	require.Equal(t, "changed", c.Writer.Header().Get("Content-Type"))
}

// ---------------------------------------------------------------------------
// SSE writers
// ---------------------------------------------------------------------------

func TestStringData(t *testing.T) {
	t.Run("writes data line", func(t *testing.T) {
		c, rec := newWriterContext(t)
		require.NoError(t, StringData(c, "hello"))
		require.Contains(t, rec.Body.String(), "data: hello")
	})
	t.Run("nil writer returns error", func(t *testing.T) {
		var c *gin.Context = &gin.Context{}
		err := StringData(c, "x")
		require.Error(t, err)
	})
	t.Run("cancelled context returns error", func(t *testing.T) {
		c := cancelledContext(t)
		require.Error(t, StringData(c, "x"))
	})
}

func TestObjectData(t *testing.T) {
	t.Run("marshals and writes", func(t *testing.T) {
		c, rec := newWriterContext(t)
		require.NoError(t, ObjectData(c, map[string]string{"k": "v"}))
		require.Contains(t, rec.Body.String(), `"k":"v"`)
	})
	t.Run("nil object rejected", func(t *testing.T) {
		c, _ := newWriterContext(t)
		err := ObjectData(c, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "object is nil")
	})
}

func TestDone(t *testing.T) {
	c, rec := newWriterContext(t)
	Done(c)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestPingData(t *testing.T) {
	t.Run("writes ping", func(t *testing.T) {
		c, rec := newWriterContext(t)
		require.NoError(t, PingData(c))
		require.Contains(t, rec.Body.String(), ": PING")
	})
	t.Run("cancelled context returns error", func(t *testing.T) {
		require.Error(t, PingData(cancelledContext(t)))
	})
}

func TestClaudeData(t *testing.T) {
	c, rec := newWriterContext(t)
	require.NoError(t, ClaudeData(c, dto.ClaudeResponse{Type: "message_start"}))
	body := rec.Body.String()
	require.Contains(t, body, "event: message_start")
	require.Contains(t, body, "data: ")
}

func TestClaudeDataCancelled(t *testing.T) {
	c := cancelledContext(t)
	require.NoError(t, ClaudeData(c, dto.ClaudeResponse{Type: "x"}))
}

func TestClaudeChunkData(t *testing.T) {
	c, rec := newWriterContext(t)
	ClaudeChunkData(c, dto.ClaudeResponse{Type: "content_block_delta"}, `{"delta":"hi"}`)
	body := rec.Body.String()
	require.Contains(t, body, "event: content_block_delta")
	require.Contains(t, body, `data: {"delta":"hi"}`)

	// cancelled context short-circuits without writing.
	c2 := cancelledContext(t)
	ClaudeChunkData(c2, dto.ClaudeResponse{Type: "x"}, "y")
}

func TestResponseChunkData(t *testing.T) {
	c, rec := newWriterContext(t)
	require.NoError(t, ResponseChunkData(c, dto.ResponsesStreamResponse{Type: "response.output_text.delta"}, `{"d":1}`))
	body := rec.Body.String()
	require.Contains(t, body, "event: response.output_text.delta")
	require.Contains(t, body, `data: {"d":1}`)

	require.Error(t, ResponseChunkData(cancelledContext(t), dto.ResponsesStreamResponse{Type: "x"}, "y"))
}

// ---------------------------------------------------------------------------
// ID helpers
// ---------------------------------------------------------------------------

func TestGetResponseID(t *testing.T) {
	c, _ := newWriterContext(t)
	c.Set(common.RequestIdKey, "req123")
	require.Equal(t, "chatcmpl-req123", GetResponseID(c))
}

func TestGetLocalRealtimeID(t *testing.T) {
	c, _ := newWriterContext(t)
	c.Set(common.RequestIdKey, "req123")
	require.Equal(t, "evt_req123", GetLocalRealtimeID(c))
}

// ---------------------------------------------------------------------------
// Stream response builders
// ---------------------------------------------------------------------------

func TestGenerateStartEmptyResponse(t *testing.T) {
	fp := "fp_1"
	resp := GenerateStartEmptyResponse("id1", 100, "gpt-4o", &fp)
	require.Equal(t, "id1", resp.Id)
	require.Equal(t, "chat.completion.chunk", resp.Object)
	require.Equal(t, int64(100), resp.Created)
	require.Equal(t, "gpt-4o", resp.Model)
	require.Equal(t, &fp, resp.SystemFingerprint)
	require.Len(t, resp.Choices, 1)
	require.Equal(t, "assistant", resp.Choices[0].Delta.Role)
	require.NotNil(t, resp.Choices[0].Delta.Content)
	require.Equal(t, "", *resp.Choices[0].Delta.Content)
}

func TestGenerateStopResponse(t *testing.T) {
	resp := GenerateStopResponse("id2", 200, "gpt-4o", "stop")
	require.Equal(t, "id2", resp.Id)
	require.Nil(t, resp.SystemFingerprint)
	require.Len(t, resp.Choices, 1)
	require.NotNil(t, resp.Choices[0].FinishReason)
	require.Equal(t, "stop", *resp.Choices[0].FinishReason)
}

func TestGenerateFinalUsageResponse(t *testing.T) {
	usage := dto.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	resp := GenerateFinalUsageResponse("id3", 300, "gpt-4o", usage)
	require.Equal(t, "id3", resp.Id)
	require.NotNil(t, resp.Usage)
	require.Equal(t, 15, resp.Usage.TotalTokens)
	require.Empty(t, resp.Choices)
}

// ---------------------------------------------------------------------------
// WebSocket helpers
// ---------------------------------------------------------------------------

// wsPair spins up an httptest server that upgrades to a websocket, and returns
// the server-side conn plus the connected client. The caller writes via the
// server conn (mirroring WssString/WssObject usage) and reads on the client.
func wsPair(t *testing.T) (server *websocket.Conn, client *websocket.Conn) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	connCh := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		connCh <- conn
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	server = <-connCh
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	return server, client
}

func TestWssString(t *testing.T) {
	c, _ := newWriterContext(t)
	server, client := wsPair(t)
	require.NoError(t, WssString(c, server, "hello ws"))
	_, msg, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, "hello ws", string(msg))
}

func TestWssStringNilConn(t *testing.T) {
	c, _ := newWriterContext(t)
	err := WssString(c, nil, "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "websocket connection is nil")
}

func TestWssObject(t *testing.T) {
	c, _ := newWriterContext(t)
	server, client := wsPair(t)
	require.NoError(t, WssObject(c, server, map[string]int{"a": 1}))
	_, msg, err := client.ReadMessage()
	require.NoError(t, err)
	require.Contains(t, string(msg), `"a":1`)
}

func TestWssObjectNilConn(t *testing.T) {
	c, _ := newWriterContext(t)
	err := WssObject(c, nil, map[string]int{"a": 1})
	require.Error(t, err)
}

func TestWssError(t *testing.T) {
	c, _ := newWriterContext(t)
	c.Set(common.RequestIdKey, "r1")
	server, client := wsPair(t)
	WssError(c, server, types.OpenAIError{Message: "boom", Type: "invalid_request_error"})
	_, msg, err := client.ReadMessage()
	require.NoError(t, err)
	require.Contains(t, string(msg), "boom")
	require.Contains(t, string(msg), "evt_r1")
}

func TestWssErrorNilConn(t *testing.T) {
	c, _ := newWriterContext(t)
	// Must not panic with a nil connection.
	assert.NotPanics(t, func() {
		WssError(c, nil, types.OpenAIError{Message: "x"})
	})
}
