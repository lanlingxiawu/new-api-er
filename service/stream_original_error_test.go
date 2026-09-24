package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/i18n"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/aws/smithy-go"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUnifiedStreamFailureLogMessage(t *testing.T) {
	for _, tc := range []struct {
		name, payload, want string
	}{
		{"openai", `{"error":{"message":"original api_key:secret (request id: upstream)","metadata":{"raw":"private"}},"private":"body-secret"}`, "original api_key:***"},
		{"blank nested message", `{"error":{"message":" \t"},"message":"top error"}`, "top error"},
		{"responses", `{"type":"response.failed","response":{"error":{"message":"response error"}}}`, "response error"},
		{"gemini", `{"error":{"code":503,"message":"gemini error"}}`, "gemini error"},
		{"ollama", `{"error":"ollama error"}`, "ollama error"},
		{"xunfei", `{"header":{"code":100,"message":"header error"}}`, "header error"},
		{"tencent", `{"Response":{"Error":{"Code":"bad","Message":"tencent error"}}}`, "tencent error"},
		{"minimax", `{"base_resp":{"status_code":100,"status_msg":"minimax error"}}`, "minimax error"},
		{"ali", `{"output":{"message":"ali error"}}`, "ali error"},
		{"blank native message", `{"base_resp":{"status_msg":" (request id: upstream)"},"output":{"message":"ali error"}}`, "ali error"},
		{"realtime", `{"response":{"status_details":{"error":{"message":"realtime error"}}}}`, "realtime error"},
		{"coze chat error", `{"status":"failed","last_error":{"code":123,"msg":"coze error api_key:secret (request id: upstream)"}}`, "coze error api_key:***"},
		{"coze with conflicting detail", `{"status":"failed","last_error":{"msg":"coze error"},"detail":[]}`, "coze error"},
		{"coze standard message priority", `{"message":"top error","last_error":{"msg":"coze error"}}`, "top error"},
		{"coze after blank top message", `{"message":" (request id: upstream)","last_error":{"msg":"coze error"}}`, "coze error"},
		{"coze blank message", `{"last_error":{"msg":" \t (request id: upstream)"}}`, ""},
		{"coze numeric message", `{"last_error":{"msg":123}}`, ""},
		{"coze object message", `{"last_error":{"msg":{"private":"body-secret"}}}`, ""},
		{"coze array message", `{"last_error":{"msg":["body-secret"]}}`, ""},
		{"coze null message", `{"last_error":{"msg":null}}`, ""},
		{"coze invalid JSON", `{"last_error":{"msg":"body-secret"},`, ""},
		{"invalid usage", `{"error":{"message":"original"},"usage":"invalid"}`, "original"},
		{"array detail", `{"error":{"message":"original"},"detail":[]}`, "original"},
		{"object detail", `{"error":{"message":"original"},"detail":{"raw":"private"}}`, "original"},
		{"numeric top message", `{"error":{"message":"original"},"message":42}`, "original"},
		{"invalid header", `{"error":{"message":"original"},"header":[]}`, "original"},
		{"invalid nested message", `{"error":{"message":{}},"message":[],"msg":"fallback","detail":false}`, "fallback"},
		{"string error priority", `{"error":"first","message":"second","detail":[]}`, "first"},
		{"blank error priority", `{"error":{"message":" (request id: stale)"},"message":"second","msg":"third","detail":[]}`, "second"},
		{"native with invalid message", `{"message":{},"base_resp":{"status_msg":"native"}}`, "native"},
		{"tencent with invalid detail", `{"Response":{"Error":{"Message":"tencent error"}},"detail":[]}`, "tencent error"},
		{"realtime with invalid message", `{"message":{},"response":{"status_details":{"error":{"message":"realtime error"}}}}`, "realtime error"},
		{"invalid JSON with message prefix", `{"error":{"message":"private"},`, ""},
		{"no string candidates", `{"error":{"message":{}},"message":[],"msg":42,"detail":false}`, ""},
		{"no message", `{"error":{"code":"bad"},"metadata":{"raw":"private"}}`, ""},
		{"blank message", `{"error":{"message":" (request id: upstream)"}}`, ""},
		{"numeric error", `{"error":400}`, ""},
		{"html", `<html>private</html>`, ""},
		{"empty", "", ""},
	} {
		for _, framing := range []string{"sse", "sdk"} {
			t.Run(tc.name+"/"+framing, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/fixture", nil)
				info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
				BeginStreamAttempt(c, info)
				info.StreamSession.ObserveTransport(&http.Response{StatusCode: http.StatusOK}, nil)
				if framing == "sse" {
					_ = info.StreamSession.ObserveFrame([]byte("event: error\ndata: " + tc.payload + "\n\n"))
				} else {
					_ = info.StreamSession.ObserveEvent("error", []byte(tc.payload))
				}
				FinalizeStreamUsage(c, info, nil)
				want := tc.want
				if want == "" {
					want = i18n.Translate(i18n.LangEn, i18n.MsgClaudeStreamFailed)
				}
				require.True(t, info.StreamResult.Failed)
				require.Equal(t, want, info.StreamResult.ErrorMessage)
				require.NotContains(t, info.StreamResult.ErrorMessage, "body-secret")
				require.NotContains(t, info.StreamResult.ErrorMessage, "private")
				body := rec.Body.String()
				FinalizeStreamUsage(c, info, nil)
				require.Equal(t, body, rec.Body.String(), "logging must not emit another error")
			})
		}
	}
}

func TestUnifiedStreamFailureLogLocalAndClientBoundaries(t *testing.T) {
	for _, scenario := range []string{"normal", "client", "read", "typed error", "hidden error", "hidden SDK error"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = httptest.NewRequest("POST", "/fixture", nil).WithContext(ctx)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
			BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: http.StatusOK}, nil)
			want := ""
			switch scenario {
			case "normal":
				info.StreamSession.Complete()
			case "client":
				cancel()
			case "read":
				info.StreamSession.EndRead(errors.New("private transport details"))
				want = i18n.Translate(i18n.LangEn, i18n.MsgClaudeStreamFailed)
			case "typed error", "hidden error":
				err := types.WithOpenAIError(types.OpenAIError{Message: "SDK explicit error"}, 500)
				want = "SDK explicit error"
				if scenario == "hidden error" {
					types.ErrOptionWithHideErrMsg("hidden")(err)
					want = i18n.Translate(i18n.LangEn, i18n.MsgClaudeStreamFailed)
				}
				info.StreamSession.FailRelay("upstream_error", err)
			case "hidden SDK error":
				err := types.NewOpenAIError(&smithy.GenericAPIError{Code: "provider_error", Message: "private SDK message"}, types.ErrorCodeAwsInvokeError, 500,
					types.ErrOptionWithUpstreamMessage("private SDK message"), types.ErrOptionWithHideErrMsg("hidden"))
				info.StreamSession.FailRelay("upstream_error", err)
				want = i18n.Translate(i18n.LangEn, i18n.MsgClaudeStreamFailed)
			}
			FinalizeStreamUsage(c, info, nil)
			require.Equal(t, want, info.StreamResult.ErrorMessage)
		})
	}
}

// TestUnifiedStreamSDKErrorCompatibility 用 t 验证原 SDK 载荷的协议匹配、形状筛选及 HTTP 响应门控。
func TestUnifiedStreamSDKErrorCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name             string            // 用例名。
		format, upstream types.RelayFormat // 下游协议与最终出站协议。
		payload          string            // SDK 原载荷，不含 SSE 外壳。
		preserve         bool              // 是否符合既有同协议错误透传契约。
	}{
		{"openai", types.RelayFormatOpenAI, types.RelayFormatOpenAI, `{"error":{"message":"original"},"usage":"invalid"}`, true},
		{"claude", types.RelayFormatClaude, types.RelayFormatClaude, `{"type":"error","error":{"message":"original"},"usage":"invalid"}`, true},
		{"gemini", types.RelayFormatGemini, types.RelayFormatGemini, `{"error":{"code":500,"message":"original"},"usage":"invalid"}`, true},
		{"responses", types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponses, `{"type":"response.failed","response":{"error":{"message":"original"}},"usage":"invalid"}`, true},
		{"cross protocol", types.RelayFormatOpenAI, types.RelayFormatClaude, `{"type":"error","error":{"message":"original"},"usage":"invalid"}`, false},
		{"native shape mismatch", types.RelayFormatOpenAI, types.RelayFormatOpenAI, `{"header":{"code":500,"message":"original"},"usage":"invalid"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/fixture", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: tc.format, FinalRequestRelayFormat: tc.upstream, ChannelMeta: &relaycommon.ChannelMeta{}}
			BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: http.StatusOK}, nil)
			require.Error(t, info.StreamSession.ObserveEvent("", []byte(tc.payload)))
			FinalizeStreamUsage(c, info, nil)
			if tc.preserve {
				require.Equal(t, "event: error\ndata: "+tc.payload+"\n\n", rec.Body.String())
			} else {
				require.NotContains(t, rec.Body.String(), "original")
				require.Contains(t, rec.Body.String(), "upstream_stream_error")
			}
		})
	}
	for _, status := range []int{0, 201, 401, 429, 500} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/fixture", nil)
		info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
		BeginStreamAttempt(c, info)
		if status != 0 {
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: status}, nil)
		}
		WriteStreamTerminalError(c, info, relaycommon.StreamSnapshot{ErrorPayload: []byte(`{"error":{"message":"original"}}`)})
		require.Empty(t, rec.Body.String(), "响应前或非 200 仍交给原流程")
	}
}

// streamErrorWriteProbe 记录终止写入的分块大小，验证 SSE 包装不先分配放大后的完整错误帧。
type streamErrorWriteProbe struct {
	gin.ResponseWriter      // 真实测试响应写入器。
	largest            int  // 单次 Write 接收到的最大字节数。
	writes             int  // 底层写入调用次数。
	flushes            int  // 网络刷新次数，失败后应保持为零。
	fail, short        bool // 模拟写错误或只接受前半段的短写。
}

// Write 记录本次 p 的大小；正常时转交，失败夹具返回短写或读写错误，供测试终止路径。
func (w *streamErrorWriteProbe) Write(p []byte) (int, error) {
	w.largest = max(w.largest, len(p))
	w.writes++
	if w.fail {
		if w.short {
			return w.ResponseWriter.Write(p[:len(p)/2])
		}
		return 0, io.ErrClosedPipe
	}
	return w.ResponseWriter.Write(p)
}

// Flush 记录刷新次数并转交，用于验证终止写失败后没有继续刷新。
func (w *streamErrorWriteProbe) Flush() {
	w.flushes++
	w.ResponseWriter.Flush()
}

// TestUnifiedStreamSDKMultilineBoundedWrite 用 t 验证大量换行的 SDK 错误使用固定缓冲包装，不聚合放大后的全帧。
func TestUnifiedStreamSDKMultilineBoundedWrite(t *testing.T) {
	payload := "{\n" + strings.Repeat("\n", 2048) + `"type":"error","error":{"message":"original"},"usage":"invalid"` + "\n}"
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/fixture", nil)
	probe := &streamErrorWriteProbe{ResponseWriter: c.Writer}
	c.Writer = probe
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{}}
	BeginStreamAttempt(c, info)
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: http.StatusOK}, nil)
	require.Error(t, info.StreamSession.ObserveEvent("", []byte(payload)))
	FinalizeStreamUsage(c, info, nil)
	require.LessOrEqual(t, probe.largest, 4096)
	event, data := relaycommon.StreamFramePayload(rec.Body.Bytes())
	require.Equal(t, "error", event)
	require.Equal(t, payload, string(data))
}

// TestUnifiedStreamSDKErrorWriteFailure 用 t 覆盖小错误尾部刷新、长行直写及换行缓冲写失败，均只尝试一次。
func TestUnifiedStreamSDKErrorWriteFailure(t *testing.T) {
	for _, padding := range []string{"", strings.Repeat(" ", 8192), strings.Repeat("\n", 2048)} {
		for _, short := range []bool{false, true} {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/fixture", nil)
			probe := &streamErrorWriteProbe{ResponseWriter: c.Writer, fail: true, short: short}
			c.Writer = probe
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{}}
			BeginStreamAttempt(c, info)
			info.StreamSession.ObserveTransport(&http.Response{StatusCode: http.StatusOK}, nil)
			payload := `{` + padding + `"type":"error","error":{"message":"original"},"usage":"invalid"}`
			require.Error(t, info.StreamSession.ObserveEvent("", []byte(payload)))
			FinalizeStreamUsage(c, info, nil)
			body := rec.Body.String()
			FinalizeStreamUsage(c, info, nil)
			require.Equal(t, 1, probe.writes)
			require.Zero(t, probe.flushes)
			require.Equal(t, body, rec.Body.String())
		}
	}
}
