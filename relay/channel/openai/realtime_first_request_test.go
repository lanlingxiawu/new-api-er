package openai

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// TestRealtimeFirstRequestRecovery 验证首轮前请求错误不虚构失败轮次，新的回答或真正断链仍按原策略收尾；t 管理本地 WS。
func TestRealtimeFirstRequestRecovery(t *testing.T) {
	const created = `{"type":"session.created","session":{}}`
	const recovery = `{"type":"error","error":{"type":"invalid_request_error","message":"invalid first request"}}`
	const fatal = `{"type":"error","error":{"type":"server_error","message":"fixture fatal"}}`
	for _, tc := range []struct {
		name                        string // 子用例名。
		pending, started, completed bool   // 错误后是否重新请求、回答开始、回答完成。
		abrupt, fatal               bool   // 是否发生真实断链或上游原错误。
	}{
		{name: "normal idle close"},
		{name: "new pending request", pending: true},
		{name: "new response interrupted", pending: true, started: true},
		{name: "next response completes", pending: true, started: true, completed: true},
		{name: "real connection failure", abrupt: true},
		{name: "real upstream error", fatal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := []string{created, recovery}
			if tc.started {
				frames = append(frames, `{"type":"response.created","response":{"id":"r1","status":"in_progress"}}`, `{"type":"response.text.delta","delta":"hello"}`)
			}
			if tc.completed {
				frames = append(frames, `{"type":"response.done","response":{"id":"r1","status":"completed","usage":{"input_tokens":10,"output_tokens":2}}}`)
			}
			if tc.fatal {
				frames = append(frames, fatal)
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer ws.Close()
				_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
				for i, frame := range frames {
					if i == 1 {
						if _, _, err := ws.ReadMessage(); err != nil {
							return
						}
					}
					if err := ws.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
						return
					}
					if i == 1 && tc.pending {
						if _, _, err := ws.ReadMessage(); err != nil {
							return
						}
					}
				}
				if !tc.abrupt {
					_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				}
			}))
			defer upstream.Close()
			type result struct {
				usage    *dto.RealtimeUsage         // 最终累计计量，不执行真实资金结算。
				outcome  *relaycommon.StreamOutcome // 最终状态及诊断资格。
				reserves int                        // 轮次预留调用次数。
			}
			results := make(chan result, 1)
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				client, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer client.Close()
				target, handshake, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(upstream.URL, "http"), nil)
				if err != nil {
					return
				}
				defer target.Close()
				c, _ := gin.CreateTestContext(w)
				c.Request = r
				billing := &realtimeEndBilling{}
				info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime, ClientWs: client, TargetWs: target, Billing: billing, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
				service.BeginStreamAttempt(c, info)
				info.StreamSession.ObserveWebSocketHandshake(handshake, nil)
				info.StreamSession.BindUpstream(target)
				_, usage := managedRealtimeHandler(c, info)
				results <- result{usage, info.StreamResult, billing.reserves}
			}))
			defer proxy.Close()
			client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(proxy.URL, "http"), nil)
			require.NoError(t, err)
			defer client.Close()
			_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
			var received []string
			for {
				_, data, err := client.ReadMessage()
				if err != nil {
					break
				}
				received = append(received, string(data))
				if len(received) == 1 || len(received) == 2 && tc.pending {
					require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create"}`)))
				}
			}
			failed := tc.abrupt || tc.fatal || tc.pending && !tc.completed
			if failed && !tc.fatal {
				require.Len(t, received, len(frames)+1)
				require.Contains(t, received[len(frames)], "upstream_stream_error")
				received = received[:len(frames)]
			}
			require.Equal(t, frames, received, "原始请求错误只转发一次，正常空闲关闭不追加服务器错误")
			select {
			case got := <-results:
				require.Equal(t, failed, got.outcome.Failed)
				require.Equal(t, failed, got.outcome.DiagnosticAvailable)
				reserves := 0
				if failed || tc.completed {
					reserves = 1
				}
				require.Equal(t, reserves, got.reserves)
				if tc.completed {
					require.Equal(t, 12, got.usage.TotalTokens)
				}
				if !failed && !tc.completed {
					require.Zero(t, got.usage.TotalTokens)
					require.Equal(t, "none", got.outcome.UsageSource)
					require.Empty(t, got.outcome.Diagnostic.Error)
				}
			case <-time.After(time.Second):
				require.FailNow(t, "fixture did not finish")
			}
		})
	}
}
