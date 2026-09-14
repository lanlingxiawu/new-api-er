package openai

import (
	"context"
	"encoding/base64"
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
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// TestRealtimeEstimatedOutputSelection 验证 t 中输入/音频估算不覆盖已交付文本，确认零与客户端退款策略不变。
func TestRealtimeEstimatedOutputSelection(t *testing.T) {
	for _, input := range []int{0, 30} {
		for _, audio := range []int{0, 8} {
			for _, mode := range []string{"error", "completed", "confirmed-zero", "client"} {
				t.Run(fmt.Sprintf("input=%d/audio=%d/%s", input, audio, mode), func(t *testing.T) {
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					c.Request = httptest.NewRequest("GET", "/v1/realtime", nil).WithContext(ctx)
					info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
					service.BeginStreamAttempt(c, info)
					info.StreamSession.ObserveWebSocketHandshake(&http.Response{StatusCode: 101}, nil)
					info.StreamSession.CommitDelivery([]byte(`{"type":"response.text.delta","delta":"hello"}`))
					info.StreamSession.AddEstimatedOutput(2)
					local := &dto.RealtimeUsage{InputTokens: input, OutputTokens: audio, TotalTokens: input + audio}
					local.InputTokenDetails.TextTokens = input
					local.OutputTokenDetails.AudioTokens = audio
					switch mode {
					case "client":
						cancel()
					case "confirmed-zero":
						require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"usage":{"input_tokens":0,"output_tokens":0}}`)))
						info.StreamSession.EndRead(io.EOF)
					case "completed":
						require.NoError(t, info.StreamSession.ObserveEvent("", []byte(`{"type":"response.done","response":{"status":"completed"}}`)))
					default:
						info.StreamSession.EndRead(io.EOF)
					}
					a := newRealtimeStreamAccounting()
					require.NoError(t, a.finishRound(c, info, local))
					if mode == "client" || mode == "confirmed-zero" {
						require.Zero(t, a.usage.TotalTokens)
					} else {
						require.Equal(t, input, a.usage.InputTokens)
						require.Equal(t, 2, a.usage.OutputTokenDetails.TextTokens)
						require.Equal(t, audio, a.usage.OutputTokenDetails.AudioTokens)
						require.Equal(t, 2+audio, a.usage.OutputTokens)
						require.Equal(t, input+2+audio, a.usage.TotalTokens)
					}
				})
			}
		}
	}
}

// TestRealtimeDeliveredEstimateFixtures 经本地 WS 验证 t 中交付文本分片、转写、音频、工具去重及多轮清零；不访问真实上游或资金。
func TestRealtimeDeliveredEstimateFixtures(t *testing.T) {
	const fatal = `{"type":"error","error":{"type":"server_error","message":"fixture failed"}}`
	for _, tc := range []struct {
		name, kind          string // 用例名及文本事件类型；tool 表示一对相同调用的工具完成事件。
		input, audio, split bool   // 是否先送入指令、输出音频及逐字分片。
		rounds              int    // 先完成的轮数加最后失败轮次。
	}{
		{"text no input", "response.text.delta", false, false, true, 1},
		{"text with input", "response.text.delta", true, false, true, 1},
		{"output text", "response.output_text.delta", true, false, false, 1},
		{"transcript and audio", "response.audio_transcript.delta", true, true, true, 1},
		{"audio only", "", true, true, false, 1},
		{"two rounds", "response.text.delta", true, false, true, 2},
		{"tool completion pair", "tool", true, false, false, 1},
		{"tool identity reset", "tool", true, false, false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var frames []string
			audioTokens := 0
			for round := 0; round < tc.rounds; round++ {
				frames = append(frames, fmt.Sprintf(`{"type":"response.created","response":{"id":"r%d","status":"in_progress"}}`, round))
				if tc.kind == "tool" {
					// 两轮故意复用身份，验证重建会话后旧轮计量记录已释放。
					frames = append(frames,
						`{"type":"response.function_call_arguments.done","item_id":"item1","call_id":"call1","name":"lookup","arguments":"{}"}`,
						`{"type":"response.output_item.done","item":{"type":"function_call","id":"item1","call_id":"call1","name":"lookup","arguments":"{}"}}`)
				} else if tc.kind != "" {
					parts := []string{"hello"}
					if tc.split {
						parts = []string{"h", "e", "l", "l", "o"}
					}
					for _, part := range parts {
						frames = append(frames, fmt.Sprintf(`{"type":%q,"delta":%q}`, tc.kind, part))
					}
				}
				if tc.audio {
					data := base64.StdEncoding.EncodeToString(make([]byte, 4800))
					count, err := service.CountAudioTokenOutput(data, "pcm16")
					require.NoError(t, err)
					require.Positive(t, count)
					for _, kind := range []string{"response.audio.delta", "response.output_audio.delta"} {
						frames = append(frames, fmt.Sprintf(`{"type":%q,"delta":%q}`, kind, data))
						audioTokens += count
					}
				}
				if round+1 < tc.rounds {
					frames = append(frames, fmt.Sprintf(`{"type":"response.done","response":{"id":"r%d","status":"completed"}}`, round))
				}
			}
			frames = append(frames, fatal)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer ws.Close()
				_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
				if tc.input {
					if _, _, err := ws.ReadMessage(); err != nil {
						return
					}
				}
				for _, frame := range frames {
					if err := ws.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
						return
					}
				}
			}))
			defer upstream.Close()
			type result struct {
				usage   *dto.RealtimeUsage         // 处理器最终选定累计用量。
				outcome *relaycommon.StreamOutcome // 异常分类、估算来源与诊断。
				err     error                      // 本地握手失败时交回测试线程断言。
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
				info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIRealtime, ClientWs: client, TargetWs: target, OutputAudioFormat: "pcm16", Billing: &realtimeEndBilling{}, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
				service.BeginStreamAttempt(c, info)
				info.StreamSession.ObserveWebSocketHandshake(handshake, nil)
				info.StreamSession.BindUpstream(target)
				_, usage := managedRealtimeHandler(c, info)
				results <- result{usage: usage, outcome: info.StreamResult}
			}))
			defer proxy.Close()
			client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(proxy.URL, "http"), nil)
			require.NoError(t, err)
			defer client.Close()
			_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
			inputTokens := 0
			if tc.input {
				input := `{"type":"session.update","session":{"instructions":"Be helpful"}}`
				var event dto.RealtimeEvent
				require.NoError(t, common.Unmarshal([]byte(input), &event))
				inputTokens, _, err = service.CountTokenRealtime(&relaycommon.RelayInfo{}, event, "gpt-4o")
				require.NoError(t, err)
				require.Positive(t, inputTokens)
				require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(input)))
			}
			var received []string
			for {
				_, data, err := client.ReadMessage()
				if err != nil {
					break
				}
				received = append(received, string(data))
			}
			require.Equal(t, frames, received, "上游原 error 原样且只交付一次")
			select {
			case got := <-results:
				require.NoError(t, got.err)
				require.True(t, got.outcome.Failed)
				require.Equal(t, "estimated", got.outcome.UsageSource)
				textTokens := 0
				if tc.kind != "" {
					text := "hello"
					if tc.kind == "tool" {
						text = "lookup{}"
					}
					textTokens = service.EstimateTokenByModel("gpt-4o", text) * tc.rounds
				}
				require.Equal(t, inputTokens, got.usage.InputTokens)
				require.Equal(t, textTokens, got.usage.OutputTokenDetails.TextTokens)
				require.Equal(t, audioTokens, got.usage.OutputTokenDetails.AudioTokens)
				require.Equal(t, textTokens+audioTokens, got.usage.OutputTokens)
				require.Equal(t, inputTokens+textTokens+audioTokens, got.usage.TotalTokens)
				require.Equal(t, got.usage.OutputTokens, got.outcome.Diagnostic.EstimatedUsage["output_tokens"])
			case <-time.After(time.Second):
				require.FailNow(t, "fixture did not finish")
			}
		})
	}
}
