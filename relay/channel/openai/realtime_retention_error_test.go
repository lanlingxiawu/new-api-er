package openai

import (
	"crypto/sha256"
	"encoding/base64"
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
	"github.com/tidwall/gjson"
)

// TestRealtimeResponseIdentityByteBound 用 t 验证身份只存定长摘要，空值、重复和 128 项淘汰保持原语义。
func TestRealtimeResponseIdentityByteBound(t *testing.T) {
	a := newRealtimeStreamAccounting()
	require.True(t, a.remember(""))
	require.True(t, a.remember(""))
	require.Empty(t, a.seen)
	id := gjson.Parse(`{"response":{"id":"r0"},"padding":"` + strings.Repeat("x", 1<<20) + `"}`).Get("response.id").String()
	require.True(t, a.remember(id))
	require.Equal(t, sha256.Sum256([]byte(id)), a.ids[0], "缓存中只存 32 字节摘要")
	require.False(t, a.remember(id))
	require.Equal(t, 1, a.next)
	for i := 1; i < 128; i++ {
		require.True(t, a.remember(fmt.Sprintf("r%d", i)))
	}
	require.Len(t, a.seen, 128)
	require.Zero(t, a.next)
	require.False(t, a.remember(id))
	require.True(t, a.remember(strings.Repeat("long-id", 1<<15)))
	require.Len(t, a.seen, 128)
	require.True(t, a.remember(id), "第 129 项插入后原首项已淘汰")
	require.False(t, a.remember("r127"))
	require.True(t, newRealtimeStreamAccounting().remember("r127"), "连接之间不共享去重")
}

// TestRealtimeOriginalErrorInvalidUsage 经 t 管理的本地 WS 验证非法用量不替换原错误，费用只采用先前确认且已交付的部分。
func TestRealtimeOriginalErrorInvalidUsage(t *testing.T) {
	for _, tc := range []struct {
		name, payload string // 用例名与上游原始 WS 消息，空白也参与逐字节比较。
	}{
		{"root usage", `{"type":"error","error":{"type":"server_error","message":"original"},"usage":"invalid"}`},
		{"negative usage", `{"type":"error","error":{"type":"server_error","message":"original"},"usage":{"input_tokens":999,"output_tokens":-1}}`},
		{"typed nested usage", `{"type":"error","error":{"type":"server_error","message":"original"},"response":{"usage":"invalid"}}`},
		{"failed response", `{"type":"response.done","response":{"id":"r","status":"failed","status_details":{"error":{"type":"server_error","message":"original"}},"usage":{"output_tokens":"invalid"}}}`},
		{"multiline", " \n{\r\n \"type\":\"error\",\n \"error\":{\"type\":\"server_error\",\"message\":\"original\"},\n \"usage\":\"invalid\"\r\n}\t "},
	} {
		for _, content := range []bool{false, true} {
			for _, capture := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/content=%v/capture=%v", tc.name, content, capture), func(t *testing.T) {
					frames := []string{`{"type":"response.created","response":{"id":"r","status":"in_progress","usage":{"input_tokens":10,"output_tokens":2}}}`}
					if content {
						frames = append(frames, `{"type":"response.text.delta","delta":"hello"}`)
					}
					frames = append(frames, tc.payload)
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
						if err != nil {
							return
						}
						defer ws.Close()
						for _, frame := range frames {
							if err := ws.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
								return
							}
						}
					}))
					defer upstream.Close()
					type result struct {
						usage   *dto.RealtimeUsage          // 最终选定用量，不调用真实扣款。
						outcome *relaycommon.StreamOutcome  // 原证据与失败分类。
						reason  relaycommon.StreamEndReason // 公开流状态的结束原因。
						body    *string                     // 采集器已接受的下游字节；实际入日志由既有日志入口负责。
						err     error                       // 将握手错误交回测试线程断言。
					}
					results := make(chan result, 1)
					proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						client, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
						if err != nil {
							results <- result{err: err}
							return
						}
						defer client.Close()
						target, handshake, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(upstream.URL, "http"), nil)
						if err != nil {
							results <- result{err: err}
							return
						}
						defer target.Close()
						c, _ := gin.CreateTestContext(w)
						c.Request = r
						info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime, ClientWs: client, TargetWs: target, Billing: &realtimeEndBilling{}, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
						service.BeginStreamAttempt(c, info)
						info.StreamSession.ObserveWebSocketHandshake(handshake, nil)
						info.StreamSession.BindUpstream(target)
						if !capture {
							info.StreamDiagnostic = nil
						}
						_, usage := managedRealtimeHandler(c, info)
						results <- result{usage: usage, outcome: info.StreamResult, reason: info.StreamStatus.EndReason, body: info.StreamDiagnostic.DownstreamBody()}
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
					}
					require.Equal(t, frames, received, "原错误（含空白）仅交付一次，不被本地错误替换")
					select {
					case got := <-results:
						require.NoError(t, got.err)
						require.NotNil(t, got.outcome)
						require.True(t, got.outcome.Failed)
						require.Equal(t, relaycommon.StreamEndReason("upstream_json_error"), got.reason)
						require.Equal(t, 10, got.outcome.Diagnostic.UsageEvidence["input_tokens"])
						require.Equal(t, 2, got.outcome.Diagnostic.UsageEvidence["output_tokens"])
						if capture {
							require.NotNil(t, got.body)
							body, err := base64.StdEncoding.DecodeString(*got.body)
							require.NoError(t, err)
							require.Equal(t, strings.Join(frames, ""), string(body), "诊断只记录实际成功发送的原字节")
						} else {
							require.Nil(t, got.body)
						}
						if content {
							require.Equal(t, 12, got.usage.TotalTokens)
						} else {
							require.Zero(t, got.usage.TotalTokens)
						}
					case <-time.After(time.Second):
						require.FailNow(t, "fixture did not finish")
					}
				})
			}
		}
	}
}
