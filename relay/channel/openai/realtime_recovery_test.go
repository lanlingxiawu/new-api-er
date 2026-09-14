package openai

import (
	"fmt"
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

// TestRealtimeStreamRecoverableEvents 通过实际本地 WS 验证取消、请求错误后继续下一轮及不误免收费；t 为测试上下文。
func TestRealtimeStreamRecoverableEvents(t *testing.T) {
	const recovery = `{"type":"error","error":{"type":"invalid_request_error","code":"response_cancel_not_active","message":"recoverable-fixture"}}`
	const fatal = `{"type":"error","error":{"type":"server_error","message":"fatal-fixture"}}`
	for _, tc := range []struct {
		name, status, reason      string // 第一轮真实终态及原因。
		recovery                  bool   // 是否在完成两轮之间插入可恢复错误。
		fatal                     bool   // 第二轮是否因真正错误结束。
		during, abrupt, idleClose bool   // 轮内错误、恢复后断链、请求错误后正常轮间关闭。
	}{
		{name: "client cancel", status: "cancelled", reason: "client_cancelled"},
		{name: "vad interrupt", status: "cancelled", reason: "turn_detected"},
		{name: "max tokens", status: "incomplete", reason: "max_output_tokens"},
		{name: "content filter", status: "incomplete", reason: "content_filter"},
		{name: "recoverable error", status: "completed", recovery: true},
		{name: "recover then fatal", status: "completed", recovery: true, fatal: true},
		{name: "recover inside round", status: "completed", during: true},
		{name: "recover then abrupt", status: "completed", recovery: true, abrupt: true},
		{name: "invalid create then idle close", status: "completed", recovery: true, idleClose: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := fmt.Sprintf(`{"type":"response.done","response":{"id":"r1","status":%q,"status_details":{"reason":%q},"usage":{"input_tokens":10,"output_tokens":2}}}`, tc.status, tc.reason)
			frames := []string{first, first} // 重复真实 done 只转发，不重复累计。
			if tc.during {
				frames = append([]string{`{"type":"response.text.delta","delta":"first-round"}`, recovery}, frames...)
			}
			if tc.recovery {
				frames = append(frames, recovery)
			}
			if tc.fatal {
				frames = append(frames, fatal)
			} else if !tc.abrupt && !tc.idleClose {
				frames = append(frames, `{"type":"response.text.delta","delta":"next-round"}`, `{"type":"response.done","response":{"id":"r2","status":"completed","usage":{"input_tokens":12,"output_tokens":3}}}`)
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer ws.Close()
				for i, frame := range frames {
					if tc.idleClose && i == 2 {
						_ = ws.SetReadDeadline(time.Now().Add(time.Second))
						if _, _, err := ws.ReadMessage(); err != nil {
							return
						} // 等到代理确实处理了待接受的 response.create。
					}
					if err := ws.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
						return
					}
				}
				if tc.abrupt {
					return
				}
				_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			}))
			defer upstream.Close()
			type result struct {
				usage    *dto.RealtimeUsage         // 唯一最终累计用量。
				outcome  *relaycommon.StreamOutcome // 是否错误标记连接失败。
				reserves int                        // 原逐轮预留次数。
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
			_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
			var received []string
			for {
				_, data, err := client.ReadMessage()
				if err != nil {
					break
				}
				received = append(received, string(data))
				if tc.idleClose && len(received) == 1 {
					require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","event_id":"invalid-create"}`)))
				}
			}
			if tc.abrupt {
				require.Len(t, received, len(frames)+1)
				require.Contains(t, received[len(frames)], "upstream_stream_error")
				received = received[:len(frames)]
			}
			require.Equal(t, frames, received, "恢复事件和取消状态原样交付，连接保留至下一轮")
			select {
			case got := <-results:
				want := 27
				if tc.fatal || tc.abrupt || tc.idleClose {
					want = 12
				}
				require.Equal(t, want, got.usage.TotalTokens, "无正文的正常取消轮次仍按确认量结算")
				reserves := 2
				if tc.abrupt || tc.idleClose {
					reserves = 1
				}
				require.Equal(t, reserves, got.reserves)
				require.Equal(t, tc.fatal || tc.abrupt, got.outcome.Failed)
				require.Equal(t, tc.fatal || tc.abrupt, got.outcome.DiagnosticAvailable)
			case <-time.After(time.Second):
				require.FailNow(t, "fixture did not finish")
			}
		})
	}
}
