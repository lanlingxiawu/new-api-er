package xunfei

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// xunfeiTimeoutControl 模拟未到期、无截止时刻的请求级超时控制器。
type xunfeiTimeoutControl struct{}

func (xunfeiTimeoutControl) RelayTimeoutKind() string                { return "" }
func (xunfeiTimeoutControl) RelayTimeoutDeadline() (time.Time, bool) { return time.Time{}, false }

// 回归：受超时管理时，客户端一断开就关闭讯飞上游 WebSocket（上游照常生成并计费，本方拿不到用量而退款）。
// 现在只有我方时限关闭上游连接：客户端离开后仍读到完整回答与用量（与主分支一致）。
func TestXunfeiWebSocketSurvivesClientDisconnect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		time.Sleep(200 * time.Millisecond) // 上游在客户端离开之后才作答
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"header":{"code":0,"status":2},"payload":{"choices":{"status":2,"seq":0,"text":[{"content":"hi","role":"assistant","index":0}]},"usage":{"text":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}}}`))
	}))
	t.Cleanup(server.Close)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, clientGone := context.WithCancel(context.Background())
	t.Cleanup(clientGone)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, xunfeiTimeoutControl{})

	dataChan, stopChan, err := xunfeiMakeRequest(c, dto.GeneralOpenAIRequest{Model: "spark"}, "generalv3", "ws"+strings.TrimPrefix(server.URL, "http"), "app")
	require.NoError(t, err)
	time.AfterFunc(50*time.Millisecond, clientGone)
	select {
	case response := <-dataChan:
		require.Equal(t, 4, response.Payload.Usage.Text.TotalTokens)
	case <-stopChan:
		t.Fatal("the upstream connection was closed when the client left")
	case <-time.After(3 * time.Second):
		t.Fatal("no upstream response")
	}
}

// xunfeiClosingControl 模拟会在请求结束时执行结束回调的控制器。
type xunfeiClosingControl struct {
	xunfeiTimeoutControl
	mu    sync.Mutex
	hooks []func()
}

func (f *xunfeiClosingControl) AfterRelayClose(hook func()) (func() bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hooks = append(f.hooks, hook)
	return func() bool { return false }, true
}

func (f *xunfeiClosingControl) close() {
	f.mu.Lock()
	hooks := f.hooks
	f.hooks = nil
	f.mu.Unlock()
	for _, hook := range hooks {
		hook()
	}
}

// 回归（审查 M1）：旧讯飞流式路径在客户端断开时由 c.Stream 返回、不再读取 dataChan，
// 读取 goroutine 卡在向 dataChan 发送上，永远持有 goroutine 与上游 WebSocket。
// 现在请求结束（控制器 Close）即取消上游 context，goroutine 退出并关闭连接。
func TestXunfeiReaderExitsWhenRequestFinishes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"header":{"code":0,"status":1},"payload":{"choices":{"status":1,"seq":0,"text":[{"content":"hi","role":"assistant","index":0}]}}}`))
		_, _, _ = conn.ReadMessage() // 保持连接直到对端关闭
	}))
	t.Cleanup(server.Close)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, clientGone := context.WithCancel(context.Background())
	t.Cleanup(clientGone)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	control := &xunfeiClosingControl{}
	common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, control)

	_, stopChan, err := xunfeiMakeRequest(c, dto.GeneralOpenAIRequest{Model: "spark"}, "generalv3", "ws"+strings.TrimPrefix(server.URL, "http"), "app")
	require.NoError(t, err)
	clientGone() // 客户端离开，消费方不再读取 dataChan
	time.Sleep(100 * time.Millisecond)
	control.close() // 处理器返回，中间件关闭控制器
	select {
	case <-stopChan:
	case <-time.After(2 * time.Second):
		t.Fatal("the reader goroutine and the upstream WebSocket outlived the request")
	}
}
