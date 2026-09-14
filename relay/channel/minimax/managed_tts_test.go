package minimax

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// nativeTTSFailedWriter 模拟音频首次写入失败，仅供客户端断开回归夹具使用。
type nativeTTSFailedWriter struct{ gin.ResponseWriter }

// Write 丢弃待写 p 并返回连接关闭错误，验证未交付音频不会成为有效内容。
func (w *nativeTTSFailedWriter) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }

// nativeTTSReadFailure 在已交付夹具正文之后返回底层读取错误，用于验证完整 JSON 不覆盖传输异常。
type nativeTTSReadFailure struct{}

// Read 不向 p 填入数据，模拟正文末尾连接被截断。
func (nativeTTSReadFailure) Read(p []byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// TestManagedNativeTTSResponse 覆盖原始观察、真实适配器转换和结算选择；t 只使用内存响应，不实际扣款。
func TestManagedNativeTTSResponse(t *testing.T) {
	for _, tc := range []struct {
		name       string // 夹具名称。
		body       string // 原始上游完整响应。
		reason     string // 预期上游错误类别；空字符串表示正常完成。
		clientGone bool   // 是否让音频写入返回连接关闭。
		readError  bool   // 已读原生 JSON 后是否返回非正常 EOF。
	}{
		{"hex", `{"data":{"audio":"534e44","status":2},"extra_info":{"usage_characters":9},"base_resp":{"status_code":0}}`, "", false, false},
		{"url", `{"data":{"audio":"https://fixture.invalid/audio"},"base_resp":{"status_code":0}}`, "", false, false},
		{"client_gone", `{"data":{"audio":"534e44","status":2},"base_resp":{"status_code":0}}`, "", true, false},
		{"empty_audio", `{"data":{"audio":""},"base_resp":{"status_code":0}}`, "upstream_protocol_error", false, false},
		{"missing_audio", `{"base_resp":{"status_code":0}}`, "upstream_protocol_error", false, false},
		{"pending_audio", `{"data":{"audio":"534e44","status":1},"base_resp":{"status_code":0}}`, "upstream_protocol_error", false, false},
		{"wrong_status_type", `{"data":{"audio":"534e44","status":"2"},"base_resp":{"status_code":0}}`, "upstream_protocol_error", false, false},
		{"missing_base_status", `{"data":{"audio":"534e44"}}`, "upstream_protocol_error", false, false},
		{"json_error", `{"data":`, "upstream_json_error", false, false},
		{"typed_json_error", `{"data":{"audio":"534e44","status":2},"extra_info":{"usage_characters":"9"},"base_resp":{"status_code":0}}`, "upstream_json_error", false, false},
		{"invalid_hex", `{"data":{"audio":"zzz","status":2},"base_resp":{"status_code":0}}`, "upstream_protocol_error", false, false},
		{"upstream_error", `{"base_resp":{"status_code":1002,"status_msg":"fixture"}}`, "upstream_error", false, false},
		{"sse_error", "event: error\ndata: {\"error\":{\"message\":\"fixture original\",\"type\":\"api_error\"}}\n\n", "upstream_error", false, false},
		{"read_error", `{"data":{"audio":"534e44","status":2},"base_resp":{"status_code":0}}`, "upstream_read_error", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)
			c.Header("Content-Type", "text/event-stream") // 模拟真实请求阶段预设的响应头。
			if tc.clientGone {
				c.Writer = &nativeTTSFailedWriter{c.Writer}
			}
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeMiniMax)
			req := &dto.AudioRequest{Model: "speech-02-hd", StreamFormat: "sse", Input: "fixture"}
			info := &relaycommon.RelayInfo{IsStream: req.IsStream(c.Request), DisablePing: true, RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeAudioSpeech, Request: req, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: req.Model}}
			service.BeginStreamAttempt(c, info)
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.body))}
			if tc.name == "sse_error" {
				resp.Header.Set("Content-Type", "text/event-stream")
			}
			if tc.readError {
				resp.Body = io.NopCloser(io.MultiReader(strings.NewReader(tc.body), nativeTTSReadFailure{}))
			}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			info.StreamSession.ObserveHTTP(resp)
			actual, apiErr := (&Adaptor{}).DoResponse(c, resp, info)
			usage, _ := actual.(*dto.Usage)
			if tc.reason == "" {
				require.Nil(t, apiErr)
				require.NotNil(t, usage)
			} else {
				require.NotNil(t, apiErr)
			}
			selected := service.FinalizeStreamUsage(c, info, usage)
			require.Equal(t, tc.reason, string(info.StreamSession.Snapshot().Reason))
			require.Equal(t, tc.reason != "", info.StreamResult.DiagnosticAvailable)
			require.Equal(t, tc.clientGone, info.StreamResult.ClientGone)
			switch {
			case tc.reason != "":
				require.True(t, info.StreamResult.Failed)
				require.Equal(t, "none", info.StreamResult.UsageSource)
				require.Zero(t, selected.TotalTokens)
				require.Equal(t, tc.body, string(info.StreamResult.Diagnostic.BodyHead))
				require.NotEmpty(t, rec.Body.String())
				if tc.name == "sse_error" {
					require.Equal(t, tc.body, rec.Body.String(), "上游原始 error 帧只发送一次且不改写")
				}
			case tc.clientGone:
				require.False(t, info.StreamResult.EffectiveContent)
				require.Equal(t, "none", info.StreamResult.UsageSource, "没有确认 token 用量时不补估用户断开的费用")
				require.Empty(t, rec.Body.String())
			case tc.name == "hex":
				require.False(t, info.StreamResult.Failed)
				require.True(t, info.StreamResult.EffectiveContent)
				require.Equal(t, "SND", rec.Body.String())
				require.Equal(t, 9, selected.TotalTokens, "正常完成保留原适配器用量")
			case tc.name == "url":
				require.False(t, info.StreamResult.Failed)
				require.Equal(t, "https://fixture.invalid/audio", rec.Header().Get("Location"))
				require.NotEqual(t, "text/event-stream", rec.Header().Get("Content-Type"), "跳转不沿用流式占位头")
			}
			other := map[string]any{}
			service.AppendStreamLogInfo(info, other)
			if tc.reason == "" {
				require.Nil(t, other["stream_diagnostic"].(relaycommon.StreamDiagnostic).BodyHead, "正常或用户断开不保存原始响应")
			}
		})
	}
}

// TestManagedNativeTTSRequest 让真实转换与 HTTP 包装接收本地成功响应，防止直调适配器漏测受管入口；t 不执行持久化结算。
func TestManagedNativeTTSRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"audio":"534e44","status":2},"base_resp":{"status_code":0}}`)
	}))
	defer srv.Close()
	for _, streamFormat := range []string{"", "sse"} {
		t.Run(streamFormat, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeMiniMax)
			req := &dto.AudioRequest{Model: "speech-02-hd", Input: "fixture", Voice: "fixture", ResponseFormat: "hex", StreamFormat: streamFormat}
			info := &relaycommon.RelayInfo{IsStream: req.IsStream(c.Request), DisablePing: true, RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeAudioSpeech, RequestURLPath: c.Request.URL.Path, OriginModelName: req.Model, Request: req, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, UpstreamModelName: req.Model}}
			service.BeginStreamAttempt(c, info)
			a := &Adaptor{}
			body, err := a.ConvertAudioRequest(c, info, *req)
			require.NoError(t, err)
			resp, err := a.DoRequest(c, info, body)
			require.NoError(t, err)
			usage, apiErr := a.DoResponse(c, resp.(*http.Response), info)
			require.Nil(t, apiErr)
			service.FinalizeStreamUsage(c, info, usage.(*dto.Usage))
			require.Equal(t, "SND", rec.Body.String())
			if streamFormat == "" {
				require.Nil(t, info.StreamResult)
			} else {
				require.False(t, info.StreamResult.Failed)
			}
		})
	}
}

// TestManagedNativeTTSIsolation 验证渠道/模式双重约束和非流式/开关关闭隔离；t 使用既有配置快照 API 并恢复原值。
func TestManagedNativeTTSIsolation(t *testing.T) {
	old := operation_setting.GetStreamErrorSetting().Enabled
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("stream_error_setting", map[string]string{"enabled": strconv.FormatBool(old)}))
	})
	for _, enabled := range []bool{true, false} {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("stream_error_setting", map[string]string{"enabled": strconv.FormatBool(enabled)}))
		for _, streaming := range []bool{false, true} {
			for _, channel := range []int{constant.ChannelTypeMiniMax, constant.ChannelTypeOpenAI} {
				for _, mode := range []int{relayconstant.RelayModeAudioSpeech, relayconstant.RelayModeChatCompletions} {
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)
					common.SetContextKey(c, constant.ContextKeyChannelType, channel)
					info := &relaycommon.RelayInfo{IsStream: streaming, RelayMode: mode, RelayFormat: types.RelayFormatOpenAI}
					service.BeginStreamAttempt(c, info)
					resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":{"audio":"534e44"},"base_resp":{"status_code":0}}`))}
					if enabled && streaming {
						info.StreamSession.ObserveTransport(resp, nil)
						info.StreamSession.ObserveHTTP(resp)
					} else {
						require.Nil(t, info.StreamSession)
					}
					_, err := io.ReadAll(resp.Body)
					wantSuccess := !enabled || !streaming || channel == constant.ChannelTypeMiniMax && mode == relayconstant.RelayModeAudioSpeech
					require.Equal(t, wantSuccess, err == nil)
					require.NoError(t, resp.Body.Close())
				}
			}
		}
	}
}
