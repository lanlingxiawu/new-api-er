package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// realtimeToolInputBilling 保存每轮累计预留目标；其余资金接口复用内存替身，不操作真实余额。
type realtimeToolInputBilling struct {
	realtimeEndBilling       // 记录调用次数，处理器结束后交由测试线程读取。
	targets            []int // 按轮记录累计预留额度，不是逐轮实际扣费。
}

// Reserve 记录本次累计目标 target，再由既有内存替身统计调用。
func (b *realtimeToolInputBilling) Reserve(target int) error {
	b.targets = append(b.targets, target)
	return b.realtimeEndBilling.Reserve(target)
}

// TestRealtimeToolInputSelection 验证 t 中仅正常后续估算轮次恢复工具输入，确认用量及异常收费边界保持。
func TestRealtimeToolInputSelection(t *testing.T) {
	tools := []dto.RealTimeTool{{Type: "function", Name: "lookup", Description: "Look up a record"}, {Type: "function", Name: "weather", Description: "Get weather"}}
	for _, tc := range []struct {
		name, mode, source string // 用例名、上游结束/客户端状态及预期计费来源。
		first, noTools     bool   // 首轮边界及未配置工具的等价类。
	}{
		{"first missing", "complete", "estimated", true, false},
		{"later missing", "complete", "estimated", false, false},
		{"later null", "null", "estimated", false, false},
		{"later no tools", "complete", "estimated", false, true},
		{"confirmed positive", "confirmed", "upstream", false, false},
		{"confirmed zero", "zero", "mixed", false, false},
		{"upstream partial", "error", "estimated", false, false},
		{"upstream no delivery", "empty-error", "none", false, false},
		{"client no usage", "client", "none", false, false},
		{"client confirmed", "client-confirmed", "upstream", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = httptest.NewRequest("GET", "/v1/realtime", nil).WithContext(ctx)
			billing := &realtimeToolInputBilling{}
			info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime, IsFirstRequest: tc.first, RealtimeTools: tools, Billing: billing, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
			if tc.noTools {
				info.RealtimeTools = nil
			}
			info.PriceData.ModelRatio = 1
			info.PriceData.GroupRatioInfo.GroupRatio = 1
			service.BeginStreamAttempt(c, info)
			info.StreamSession.ObserveWebSocketHandshake(&http.Response{StatusCode: http.StatusSwitchingProtocols}, nil)
			if tc.mode != "empty-error" {
				info.StreamSession.CommitDelivery([]byte(`{"type":"response.text.delta","delta":"hello"}`))
				info.StreamSession.AddEstimatedOutput(2)
			}
			local := &dto.RealtimeUsage{InputTokens: 11, OutputTokens: 4, TotalTokens: 15}
			local.InputTokenDetails.TextTokens, local.InputTokenDetails.AudioTokens = 8, 3
			local.OutputTokenDetails.AudioTokens = 4
			before := *local
			switch tc.mode {
			case "complete":
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"type":"response.done","response":{"status":"completed"}}`)))
			case "null":
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"type":"response.done","response":{"status":"completed","usage":null}}`)))
			case "confirmed", "client-confirmed":
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"type":"response.done","response":{"status":"completed","usage":{"input_tokens":4,"output_tokens":2}}}`)))
				if tc.mode == "client-confirmed" {
					cancel()
				}
			case "zero":
				require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"type":"response.done","response":{"status":"completed","usage":{"input_tokens":0,"output_tokens":0}}}`)))
			case "client":
				cancel()
			default:
				info.StreamSession.EndRead(io.EOF)
			}
			wantInputText, wantInputAudio, wantOutputText, wantOutputAudio := 8, 3, 2, 4
			if tc.source == "none" {
				wantInputText, wantInputAudio, wantOutputText, wantOutputAudio = 0, 0, 0, 0
			} else if tc.mode == "zero" {
				wantInputText, wantInputAudio = 0, 0
			} else if tc.source == "upstream" {
				wantInputText, wantInputAudio, wantOutputText, wantOutputAudio = 4, 0, 2, 0
			} else if tc.mode == "complete" || tc.mode == "null" {
				toolTokens, audio, err := service.CountTokenRealtime(info, dto.RealtimeEvent{Type: dto.RealtimeEventTypeResponseDone}, info.UpstreamModelName)
				require.NoError(t, err)
				require.Zero(t, audio)
				if !tc.first && !tc.noTools {
					require.Positive(t, toolTokens)
				}
				wantInputText += toolTokens
			}
			a := newRealtimeStreamAccounting()
			require.NoError(t, a.finishRound(c, info, local))
			require.Equal(t, before, *local, "工具估算只补入选定用量，不污染原始局部计数")
			require.Equal(t, tc.source, info.StreamResult.UsageSource)
			require.Equal(t, wantInputText, a.usage.InputTokenDetails.TextTokens)
			require.Equal(t, wantInputAudio, a.usage.InputTokenDetails.AudioTokens)
			require.Equal(t, wantOutputText, a.usage.OutputTokenDetails.TextTokens)
			require.Equal(t, wantOutputAudio, a.usage.OutputTokenDetails.AudioTokens)
			require.Equal(t, wantInputText+wantInputAudio, a.usage.InputTokens)
			require.Equal(t, wantOutputText+wantOutputAudio, a.usage.OutputTokens)
			require.Equal(t, a.usage.InputTokens+a.usage.OutputTokens, a.usage.TotalTokens)
			require.Equal(t, a.usage.InputTokens, info.StreamFinalUsage.PromptTokens)
			require.Equal(t, a.usage.TotalTokens, info.StreamFinalUsage.TotalTokens)
			if tc.source == "estimated" {
				require.Equal(t, a.usage.InputTokens, info.GetEstimatePromptTokens())
				require.Equal(t, a.usage.InputTokens, a.estimated["input_tokens"])
				require.Equal(t, a.usage.OutputTokens, a.estimated["output_tokens"])
			} else if tc.source == "mixed" {
				require.Equal(t, a.usage.OutputTokens, a.estimated["output_tokens"])
				require.NotContains(t, a.estimated, "input_tokens")
			} else {
				require.Empty(t, a.estimated)
			}
			wantQuota := common.QuotaFromFloat(float64(wantInputText) + float64(wantOutputText)*ratio_setting.GetCompletionRatio(info.UpstreamModelName) + float64(wantInputAudio)*ratio_setting.GetAudioRatio(info.UpstreamModelName) + float64(wantOutputAudio)*ratio_setting.GetAudioRatio(info.UpstreamModelName)*ratio_setting.GetAudioCompletionRatio(info.UpstreamModelName))
			require.Equal(t, []int{wantQuota}, billing.targets)
			require.Equal(t, 1, billing.reserves)
			require.Zero(t, billing.settles)
			require.Zero(t, billing.refunds)
		})
	}
}

// TestRealtimeToolInputWebSocketRounds 用本地 WS 验证 t 中多轮工具配置和确认用量优先，原始消息与预留次数保持。
func TestRealtimeToolInputWebSocketRounds(t *testing.T) {
	const initial = `{"type":"session.update","session":{"tools":[{"type":"function","name":"lookup","description":"Look up a record","parameters":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}}]}}`
	const partial = `{"type":"session.update","session":{"instructions":"Be helpful"}}`
	const positive = `,"usage":{"input_tokens":7,"output_tokens":3}`
	const zero = `,"usage":{"input_tokens":0,"output_tokens":0}`
	for _, tc := range []struct {
		name, update string    // 第二轮客户端更新；第一轮配置工具，第三轮仅更改 instructions。
		usage        [3]string // 三轮 done 中的确认用量片段；空串表示缺失。
	}{
		{"omitted tools", partial, [3]string{}},
		{"null tools", `{"type":"session.update","session":{"tools":null}}`, [3]string{}},
		{"empty session", `{"type":"session.update","session":{}}`, [3]string{}},
		{"omitted session", `{"type":"session.update"}`, [3]string{}},
		{"null session", `{"type":"session.update","session":null}`, [3]string{}},
		{"non-update session", `{"type":"response.create","session":{"tools":[]}}`, [3]string{}},
		{"clear tools", `{"type":"session.update","session":{"tools":[]}}`, [3]string{}},
		{"replace tools", `{"type":"session.update","session":{"tools":[{"type":"function","name":"weather","description":"Get the weather for the requested city"}]}}`, [3]string{}},
		{"confirmed then estimated", partial, [3]string{positive, "", ""}},
		{"estimated then zero", partial, [3]string{"", zero, ""}},
		{"all confirmed", partial, [3]string{positive, positive, zero}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updates := []string{initial, tc.update, partial}
			var frames []string
			var expectedTools []dto.RealTimeTool
			var wantInputs, wantOutputs []int
			wantEstimatedInput, wantEstimatedOutput, wantConfirmedInput, wantConfirmedOutput := 0, 0, 0, 0
			for round, update := range updates {
				var event dto.RealtimeEvent
				require.NoError(t, common.Unmarshal([]byte(update), &event))
				if event.Type == dto.RealtimeEventTypeSessionUpdate && event.Session != nil && event.Session.Tools != nil {
					expectedTools = event.Session.Tools
				}
				counterInfo := &relaycommon.RelayInfo{IsFirstRequest: round == 0, RealtimeTools: expectedTools}
				input, _, err := service.CountTokenRealtime(counterInfo, event, "gpt-4o")
				require.NoError(t, err)
				toolTokens, _, err := service.CountTokenRealtime(counterInfo, dto.RealtimeEvent{Type: dto.RealtimeEventTypeResponseDone}, "gpt-4o")
				require.NoError(t, err)
				output := service.EstimateTokenByModel("gpt-4o", "hello")
				if tc.usage[round] == "" {
					input += toolTokens
					wantEstimatedInput += input
					wantEstimatedOutput += output
				} else {
					input, output = 0, 0
					if tc.usage[round] == positive {
						input, output = 7, 3
					}
					wantConfirmedInput += input
					wantConfirmedOutput += output
					if tc.usage[round] == zero {
						output = service.EstimateTokenByModel("gpt-4o", "hello")
						wantEstimatedOutput += output
					}
				}
				wantInputs = append(wantInputs, input)
				wantOutputs = append(wantOutputs, output)
				done := fmt.Sprintf(`{"type":"response.done","response":{"id":"tool-round-%d","status":"completed"%s}}`, round, tc.usage[round])
				// 每轮重复一次 done，既验证透传，又验证没有重复工具估算/预留。
				frames = append(frames, `{"type":"response.text.delta","delta":"hello"}`, done, done)
			}
			upstreamInputs := make(chan []string, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer ws.Close()
				_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
				var inputs []string
				defer func() { upstreamInputs <- inputs }()
				for round := range updates {
					_, raw, err := ws.ReadMessage()
					if err != nil {
						return
					}
					inputs = append(inputs, string(raw))
					for _, frame := range frames[round*3 : round*3+3] {
						if err := ws.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
							return
						}
					}
				}
				_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			}))
			defer upstream.Close()
			type result struct {
				usage   *dto.RealtimeUsage         // 单一处理器结束后交回的累计选定用量。
				outcome *relaycommon.StreamOutcome // 合并后的确认/估算来源与诊断。
				tools   []dto.RealTimeTool         // 最后一轮保留的连接局部工具配置。
				err     error                      // 本地握手错误，交给主测试线程断言。
			}
			results := make(chan result, 1)
			billing := &realtimeToolInputBilling{}
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
				info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime, IsFirstRequest: true, ClientWs: client, TargetWs: target, Billing: billing, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
				info.PriceData.ModelRatio = 1
				info.PriceData.GroupRatioInfo.GroupRatio = 1
				service.BeginStreamAttempt(c, info)
				info.StreamSession.ObserveWebSocketHandshake(handshake, nil)
				info.StreamSession.BindUpstream(target)
				_, usage := managedRealtimeHandler(c, info)
				results <- result{usage: usage, outcome: info.StreamResult, tools: info.RealtimeTools}
			}))
			defer proxy.Close()
			client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(proxy.URL, "http"), nil)
			require.NoError(t, err)
			defer client.Close()
			require.NoError(t, client.SetReadDeadline(time.Now().Add(3*time.Second)))
			var received []string
			for _, update := range updates {
				require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(update)))
				for range 3 {
					kind, raw, err := client.ReadMessage()
					require.NoError(t, err)
					require.Equal(t, websocket.TextMessage, kind)
					received = append(received, string(raw))
				}
			}
			require.Equal(t, frames, received)
			select {
			case got := <-results:
				require.NoError(t, got.err)
				require.False(t, got.outcome.Failed)
				require.False(t, got.outcome.DiagnosticAvailable)
				require.Equal(t, expectedTools, got.tools)
				require.Equal(t, wantEstimatedInput+wantConfirmedInput, got.usage.InputTokens)
				require.Equal(t, wantEstimatedOutput+wantConfirmedOutput, got.usage.OutputTokens)
				require.Equal(t, got.usage.InputTokens, got.usage.InputTokenDetails.TextTokens)
				require.Equal(t, got.usage.InputTokens+got.usage.OutputTokens, got.usage.TotalTokens)
				require.Equal(t, wantEstimatedInput, got.outcome.Diagnostic.EstimatedUsage["input_tokens"])
				require.Equal(t, wantConfirmedInput, got.outcome.Diagnostic.UsageEvidence["input_tokens"])
				wantSource := "estimated"
				if wantConfirmedInput > 0 || tc.usage[1] == zero {
					wantSource = "mixed"
				}
				require.Equal(t, wantSource, got.outcome.UsageSource)
				input, output := 0, 0
				var targets []int
				for round := range updates {
					input += wantInputs[round]
					output += wantOutputs[round]
					targets = append(targets, common.QuotaFromFloat(float64(input)+float64(output)*ratio_setting.GetCompletionRatio("gpt-4o")))
				}
				require.Equal(t, targets, billing.targets)
				require.Equal(t, len(updates), billing.reserves)
				require.Zero(t, billing.settles)
				require.Zero(t, billing.refunds)
			case <-time.After(3 * time.Second):
				require.FailNow(t, "managed Realtime did not finish")
			}
			select {
			case inputs := <-upstreamInputs:
				require.Equal(t, updates, inputs, "原始客户端消息不被工具状态维护改写")
			case <-time.After(3 * time.Second):
				require.FailNow(t, "fixture upstream did not finish")
			}
		})
	}
}
