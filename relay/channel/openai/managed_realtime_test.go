package openai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestRealtimeStreamRoundAccounting 已完成轮次保留费用，后续无有效内容的失败轮次不追加收费。
// 参数 t：测试上下文；不建立实际 WebSocket，不操作真实额度。
func TestRealtimeStreamRoundAccounting(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/realtime", nil)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
	service.BeginStreamAttempt(c, info)
	info.StreamSession.ObserveWebSocketHandshake(&http.Response{StatusCode: http.StatusSwitchingProtocols}, nil)
	a := newRealtimeStreamAccounting()
	require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"type":"response.done","response":{"status":"completed","usage":{"input_tokens":10,"output_tokens":2,"input_token_details":{"text_tokens":10},"output_token_details":{"text_tokens":2}}}}`)))
	info.StreamSession.CommitDelivery([]byte(`{"type":"response.output_text.delta","delta":"hi"}`))
	require.NoError(t, a.finishRound(c, info, &dto.RealtimeUsage{}))
	require.Equal(t, 12, a.usage.TotalTokens)
	require.False(t, info.StreamResult.DiagnosticAvailable)
	info.StreamSession = relaycommon.NewStreamSession(types.RelayFormatOpenAIRealtime)
	info.StreamSession.ResponseGate = info.StreamResponseGate
	info.StreamResult = nil
	info.StreamFinalUsage = nil
	require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"usage":{"input_tokens":100,"output_tokens":5}}`)))
	info.StreamSession.EndRead(io.EOF)
	require.NoError(t, a.finishRound(c, info, &dto.RealtimeUsage{}))
	require.Equal(t, 12, a.usage.TotalTokens)
	require.True(t, info.StreamResult.DiagnosticAvailable)
	require.True(t, a.remember("r1"))
	require.False(t, a.remember("r1"))
}

// realtimeEndBilling 统计轮间收尾的资金调用，避免通过真实余额验证重复预留或退款。
type realtimeEndBilling struct {
	reserves, settles, refunds int           // 仅由处理器所有者累加，结果通过完成通道交回测试。
	reserveErr                 error         // 非 nil 时模拟已有预留失败补发路径。
	reserved                   chan struct{} // 每次预留完成的有界通知，不参与业务同步。
}

// Reserve 记录目标 target 的预留调用并返回夹具错误，不访问真实资金。
func (b *realtimeEndBilling) Reserve(target int) error {
	b.reserves++
	select {
	case b.reserved <- struct{}{}:
	default: // 额外调用仍计数，通知已满时不阻塞测试连接清理。
	}
	return b.reserveErr
}

// Settle 记录最终额度 quota 的结算次数，本处理器测试期望没有直接结算。
func (b *realtimeEndBilling) Settle(quota int) error { b.settles++; return nil }

// Refund 记录上下文 c 对应的退款调用，本处理器测试期望没有轮间退款。
func (b *realtimeEndBilling) Refund(c *gin.Context) { b.refunds++ }

// NeedsRefund 无参数，测试资金替身始终声明没有待退款资金。
func (b *realtimeEndBilling) NeedsRefund() bool { return false }

// GetPreConsumedQuota 无参数，返回零预扣，仅用于满足既有资金接口。
func (b *realtimeEndBilling) GetPreConsumedQuota() int { return 0 }

// realtimeEndTimeout 以原子标记模拟已确认的受管超时，允许测试线程取消标准 context 而不跨线程修改 Gin。
type realtimeEndTimeout struct {
	expired atomic.Bool // true 表示超时控制器已经触发，而非用户主动取消。
	writes  int         // 仅由处理器所有者统计专用错误写入通道调用。
}

// RelayTimeoutKind 无参数；已过期返回 total，未过期返回空串。
func (c *realtimeEndTimeout) RelayTimeoutKind() string {
	if c.expired.Load() {
		return "total"
	}
	return ""
}

// WriteTerminalError 同步执行 write 错误写入回调并记录一次调用，不恢复普通响应写入。
func (c *realtimeEndTimeout) WriteTerminalError(write func()) { c.writes++; write() }

// TestRealtimeStreamBetweenRoundsTermination 验证真实 WS 轮间异常只补一次 error，不重计或退回已完成轮次。
// 参数 t：测试上下文；通过本地握手、完成消息和预留通知协调，不连接真实上游或资金数据库。
func TestRealtimeStreamBetweenRoundsTermination(t *testing.T) {
	const originalError = `{"type":"error","error":{"type":"server_error","code":"fixture","message":"provider-original"}}`
	type terminationResult struct {
		usage      *dto.RealtimeUsage           // 所有完成轮次的选定用量。
		outcome    *relaycommon.StreamOutcome   // 最终公开状态与错误来源。
		diagnostic relaycommon.StreamDiagnostic // 经日志保存规则处理的私有诊断。
		reason     relaycommon.StreamEndReason  // 最终流状态原因。
		err        error                        // 测试代理建立连接时的失败。
	}
	for _, tc := range []struct {
		name, ending string // 用例名及上游/客户端结束方式。
		rounds       int    // 已正常完成的轮次数量。
		wantErrors   int    // 下游应收到的错误消息数。
		wantReserves int    // 预留次数；轮间故障不应额外调用。
		upstreamFail bool   // 是否生成上游异常诊断资格。
		clientGone   bool   // 是否应分类为客户端断开。
	}{
		{"abrupt after one", "abrupt", 1, 1, 1, true, false},
		{"abrupt after two", "abrupt", 2, 1, 2, true, false},
		{"normal close", "normal", 1, 0, 1, false, false},
		{"going away", "going away", 1, 0, 1, false, false},
		{"native error", "native error", 1, 1, 2, true, false},
		{"client close", "client close", 1, 0, 1, false, true},
		{"context canceled", "cancel", 1, 0, 1, false, true},
		{"managed timeout", "timeout", 1, 1, 1, true, false},
		{"reservation error", "reservation error", 1, 1, 1, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			billing := &realtimeEndBilling{reserved: make(chan struct{}, 4)}
			if tc.ending == "reservation error" {
				billing.reserveErr = errors.New("reserve fixture error")
			}
			timeout := &realtimeEndTimeout{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer ws.Close()
				for i := 0; i < tc.rounds; i++ {
					if err := ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.text.delta","delta":"hi"}`)); err != nil {
						return
					}
					data := fmt.Sprintf(`{"type":"response.done","response":{"id":"r%d","status":"completed","usage":{"input_tokens":10,"output_tokens":2}}}`, i)
					if err := ws.WriteMessage(websocket.TextMessage, []byte(data)); err != nil {
						return
					}
				}
				switch tc.ending {
				case "abrupt":
					return // 不发送关闭帧，模拟已完成轮次后的异常断链。
				case "normal":
					_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				case "going away":
					_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, ""))
				case "native error":
					_ = ws.WriteMessage(websocket.TextMessage, []byte(originalError))
				default:
					_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
					_, _, _ = ws.ReadMessage() // 由处理器关闭上游释放，不主动制造第二个终止原因。
				}
			}))
			defer upstream.Close()
			result := make(chan terminationResult, 1)
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				client, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer client.Close()
				target, handshake, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(upstream.URL, "http"), nil)
				if err != nil {
					result <- terminationResult{err: err}
					return
				}
				c, _ := gin.CreateTestContext(w)
				c.Request = r.WithContext(ctx)
				if tc.ending == "timeout" {
					common.SetContextKey(c, constant.ContextKeyRelayTimeoutControl, timeout)
				}
				info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime, ClientWs: client, TargetWs: target, Billing: billing, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
				service.BeginStreamAttempt(c, info)
				info.StreamSession.ObserveWebSocketHandshake(handshake, nil)
				info.StreamSession.BindUpstream(target)
				_, usage := managedRealtimeHandler(c, info)
				other := map[string]any{}
				service.AppendStreamLogInfo(info, other)
				result <- terminationResult{usage: usage, outcome: info.StreamResult, diagnostic: other["stream_diagnostic"].(relaycommon.StreamDiagnostic), reason: info.StreamStatus.EndReason}
			}))
			defer proxy.Close()
			client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(proxy.URL, "http"), nil)
			require.NoError(t, err)
			defer client.Close()
			_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
			var body strings.Builder
			errorsSeen, roundsSeen := 0, 0
			for {
				kind, raw, err := client.ReadMessage()
				if err != nil {
					break
				}
				require.Equal(t, websocket.TextMessage, kind)
				require.True(t, gjson.ValidBytes(raw), "Realtime 错误必须是 JSON，而非 SSE")
				body.Write(raw)
				switch gjson.GetBytes(raw, "type").String() {
				case "response.done":
					roundsSeen++
					if roundsSeen == tc.rounds && (tc.ending == "cancel" || tc.ending == "timeout" || tc.ending == "client close") {
						select {
						case <-billing.reserved: // 先确认 finishRound 已选定用量，随后触发轮间取消。
						case <-time.After(time.Second):
							require.FailNow(t, "completed round did not reserve")
						}
						if tc.ending == "client close" {
							_ = client.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
						} else {
							timeout.expired.Store(tc.ending == "timeout")
							cancel()
						}
					}
				case "error":
					errorsSeen++
					if tc.ending == "native error" {
						require.Equal(t, originalError, string(raw))
					} else {
						require.Equal(t, "upstream_stream_error", gjson.GetBytes(raw, "error.code").String())
						require.Equal(t, "server_error", gjson.GetBytes(raw, "error.type").String())
					}
				}
			}
			select {
			case got := <-result:
				require.NoError(t, got.err)
				require.Equal(t, tc.wantErrors, errorsSeen)
				require.Equal(t, tc.rounds, roundsSeen)
				require.Equal(t, tc.rounds*10, got.usage.InputTokens)
				require.Equal(t, tc.rounds*2, got.usage.OutputTokens)
				require.Equal(t, tc.rounds*12, got.usage.TotalTokens)
				require.Equal(t, tc.wantReserves, billing.reserves, "轮间错误不额外预留或虚构新计费轮次")
				require.Zero(t, billing.settles)
				require.Zero(t, billing.refunds)
				require.Equal(t, tc.clientGone, got.outcome.ClientGone)
				require.Equal(t, tc.upstreamFail || tc.clientGone || tc.ending == "reservation error", got.outcome.Failed)
				require.Equal(t, tc.upstreamFail, got.outcome.DiagnosticAvailable)
				require.Equal(t, "upstream", got.outcome.UsageSource)
				if tc.upstreamFail {
					require.True(t, got.outcome.Failed)
					require.NotEmpty(t, got.diagnostic.Error)
					require.NotNil(t, got.diagnostic.DownstreamBodyBase64)
					require.Equal(t, base64.StdEncoding.EncodeToString([]byte(body.String())), *got.diagnostic.DownstreamBodyBase64)
					require.Equal(t, tc.rounds*10, got.diagnostic.UsageEvidence["input_tokens"])
				} else {
					require.Nil(t, got.diagnostic.DownstreamBodyBase64)
				}
				if tc.ending == "timeout" {
					require.Equal(t, relaycommon.StreamEndReasonTimeout, got.reason)
					require.Equal(t, 1, timeout.writes)
				}
			case <-time.After(2 * time.Second):
				require.FailNow(t, "stream workers did not finish")
			}
		})
	}
}

// TestRealtimeStreamWebSocketRounds 验证多轮实际 WS 转发、不在第一轮关闭上游、失败原样透传且只累计可收费轮次。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestRealtimeStreamWebSocketRounds(t *testing.T) {
	for _, failedSecond := range []bool{false, true} {
		t.Run(map[bool]string{true: "failed second", false: "two complete"}[failedSecond], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer ws.Close()
				_ = ws.WriteMessage(1, []byte(`{"type":"response.text.delta","delta":"hi"}`))
				_ = ws.WriteMessage(1, []byte(`{"type":"response.done","response":{"id":"r1","status":"completed","usage":{"input_tokens":10,"output_tokens":2}}}`))
				if failedSecond {
					_ = ws.WriteMessage(1, []byte(`{"type":"response.done","response":{"id":"r2","status":"failed","usage":{"input_tokens":100,"output_tokens":9},"error":{"message":"provider-error"}}}`))
				} else {
					_ = ws.WriteMessage(1, []byte(`{"type":"response.text.delta","delta":"hello"}`))
					_ = ws.WriteMessage(1, []byte(`{"type":"response.done","response":{"id":"r2","status":"completed","usage":{"input_tokens":12,"output_tokens":3}}}`))
				}
				_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			}))
			defer upstream.Close()
			result := make(chan *dto.RealtimeUsage, 1)
			diagnostics := make(chan relaycommon.StreamDiagnostic, 1)
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				client, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				target, handshake, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(upstream.URL, "http"), nil)
				if err != nil {
					_ = client.Close()
					return
				}
				c, _ := gin.CreateTestContext(w)
				c.Request = r
				info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime, ClientWs: client, TargetWs: target, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
				service.BeginStreamAttempt(c, info)
				info.StreamSession.ObserveWebSocketHandshake(handshake, err)
				info.StreamSession.BindUpstream(target)
				_, usage := managedRealtimeHandler(c, info)
				other := map[string]any{}
				service.AppendStreamLogInfo(info, other)
				diagnostics <- other["stream_diagnostic"].(relaycommon.StreamDiagnostic)
				if info.StreamResult.DiagnosticAvailable != failedSecond {
					// 由主测试 goroutine 断言，避免 HTTP 工作者内 Fatal 中断连接清理。
					result <- nil
					return
				}
				result <- usage
			}))
			defer proxy.Close()
			client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(proxy.URL, "http"), nil)
			require.NoError(t, err)
			defer client.Close()
			_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
			var body strings.Builder
			for {
				_, b, err := client.ReadMessage()
				if err != nil {
					break
				}
				body.Write(b)
			}
			select {
			case u := <-result:
				diagnostic := <-diagnostics
				require.NotNil(t, u, "only the failed upstream round has diagnostic eligibility")
				if failedSecond {
					require.Contains(t, string(diagnostic.BodyHead)+string(diagnostic.BodyTail), "provider-error")
					require.NotNil(t, diagnostic.DownstreamBodyBase64)
					require.Equal(t, base64.StdEncoding.EncodeToString([]byte(body.String())), *diagnostic.DownstreamBodyBase64)
					require.Equal(t, 12, u.TotalTokens)
					require.Contains(t, body.String(), "provider-error")
					require.NotContains(t, body.String(), "upstream_stream_error")
				} else {
					require.Empty(t, diagnostic.ResponseHeaders)
					require.Empty(t, diagnostic.BodyHead)
					require.Empty(t, diagnostic.BodyTail)
					require.Nil(t, diagnostic.DownstreamBodyBase64)
					require.Equal(t, 27, u.TotalTokens)
					require.Contains(t, body.String(), "hello")
				}
			case <-time.After(2 * time.Second):
				require.FailNow(t, "stream workers did not finish")
			}
		})
	}
}

// TestRealtimeStreamEmptyClose 上游尚无完整轮次就正常关闭 WS 时，保留不完整流错误和零结算，而不是覆盖成成功。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestRealtimeStreamEmptyClose(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	}))
	defer upstream.Close()
	result := make(chan *relaycommon.StreamOutcome, 1)
	diagnostics := make(chan relaycommon.StreamDiagnostic, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		target, handshake, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(upstream.URL, "http"), nil)
		if err != nil {
			_ = client.Close()
			return
		}
		c, _ := gin.CreateTestContext(w)
		c.Request = r
		info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime, ClientWs: client, TargetWs: target, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
		service.BeginStreamAttempt(c, info)
		info.StreamSession.ObserveWebSocketHandshake(handshake, err)
		info.StreamSession.BindUpstream(target)
		_, _ = managedRealtimeHandler(c, info)
		other := map[string]any{}
		service.AppendStreamLogInfo(info, other)
		diagnostics <- other["stream_diagnostic"].(relaycommon.StreamDiagnostic)
		result <- info.StreamResult
	}))
	defer proxy.Close()
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(proxy.URL, "http"), nil)
	require.NoError(t, err)
	defer client.Close()
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := client.ReadMessage()
	require.NoError(t, err)
	require.Contains(t, string(raw), `"type":"error"`)
	select {
	case outcome := <-result:
		diagnostic := <-diagnostics
		require.NotNil(t, diagnostic.DownstreamBodyBase64)
		require.Equal(t, base64.StdEncoding.EncodeToString(raw), *diagnostic.DownstreamBodyBase64)
		require.True(t, outcome.Failed)
		require.Equal(t, "none", outcome.UsageSource)
		require.NotEmpty(t, outcome.Diagnostic.Error)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "stream workers did not finish")
	}
}
