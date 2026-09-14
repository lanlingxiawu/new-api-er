package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestStreamDiagnosticLogEligibility 覆盖来源、取消与无采集日志；t 为测试上下文，所有响应仅本地构造。
func TestStreamDiagnosticLogEligibility(t *testing.T) {
	for _, name := range []string{"normal", "eof", "client", "upstream_then_client", "local", "transport", "http", "no_dispatch"} {
		t.Run(name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
			BeginStreamAttempt(c, info)
			// 模拟关闭响应采集：资格必须仍由流程证据决定，而不是采集器是否存在。
			info.StreamDiagnostic = nil
			c.Set(relaycommon.StreamResponseCaptureKey, (*relaycommon.StreamResponseCapture)(nil))
			want := false
			if name != "transport" && name != "http" && name != "no_dispatch" {
				info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
			}
			switch name {
			case "normal":
				info.StreamSession.Complete()
			case "eof":
				info.StreamSession.EndRead(io.EOF)
				want = true
			case "client":
				cancel()
			case "upstream_then_client":
				info.StreamSession.EndRead(io.ErrUnexpectedEOF)
				cancel()
				want = true
			case "local":
				info.StreamSession.FailRelay("upstream_read_error", errors.New("local validation"))
			case "transport":
				info.StreamSession.ObserveTransport(nil, errors.New("connect"))
			case "http":
				info.StreamSession.ObserveTransport(&http.Response{StatusCode: 503}, nil)
			}
			if name == "transport" || name == "http" {
				other := map[string]any{}
				AppendStreamErrorDiagnostic(c, other, errors.New("attempt failed"))
				require.Empty(t, other, "非 200 / 连接失败保留原流程，无新诊断字段")
			}
			FinalizeStreamUsage(c, info, nil)
			other := map[string]any{}
			AppendStreamLogInfo(info, other)
			require.Equal(t, want, other["stream_diagnostic_available"] == true)
			require.NotContains(t, other, "claude_diagnostic_available")
			BeginStreamAttempt(c, info)
			require.False(t, info.StreamSession.Snapshot().DiagnosticAvailable(false))
		})
	}
}

// TestStreamRejectReasonBaseline 验证无流式/诊断状态时上下文策略原因仍生成顶层日志字段；t 为测试上下文。
func TestStreamRejectReasonBaseline(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "policy")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	other := GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 0, 0, 1)
	require.Equal(t, "policy", other["reject_reason"])
	require.NotContains(t, other, "stream_diagnostic_available")
	require.NotContains(t, other, "stream_diagnostic")
}

// TestStreamDiagnosticSwitches 验证实际开关、透传入口和无采集重试编号；t 为测试上下文，结束后恢复全局配置。
func TestStreamDiagnosticSwitches(t *testing.T) {
	old := *operation_setting.GetStreamErrorSetting()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("stream_error_setting", map[string]string{"enabled": strconv.FormatBool(old.Enabled), "capture_response": strconv.FormatBool(old.CaptureResponse)}))
	})
	for _, enabled := range []bool{false, true} {
		for _, capture := range []bool{false, true} {
			require.NoError(t, config.GlobalConfig.UpdateFromMap("stream_error_setting", map[string]string{"enabled": strconv.FormatBool(enabled), "capture_response": strconv.FormatBool(capture)}))
			for _, streaming := range []bool{false, true} {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
				info := &relaycommon.RelayInfo{IsStream: streaming, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
				info.ChannelSetting.PassThroughBodyEnabled = true
				for attempt := 1; attempt <= 2; attempt++ {
					BeginStreamAttempt(c, info)
					other := map[string]any{}
					if enabled && streaming {
						require.Equal(t, capture, info.StreamDiagnostic != nil)
						_, observesDownstream := info.StreamWriter.ResponseWriter.(*relaycommon.DownstreamCaptureWriter)
						require.Equal(t, capture, observesDownstream, "关闭采集时不安装下游观察器")
						info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
						info.StreamSession.EndRead(io.ErrUnexpectedEOF)
						AppendStreamErrorDiagnostic(c, other, io.ErrUnexpectedEOF)
						require.Equal(t, attempt, other["stream_diagnostic_attempt"])
						FinalizeStreamUsage(c, info, nil)
						AppendStreamLogInfo(info, other)
						require.Equal(t, attempt, other["stream_diagnostic_attempt"])
					} else {
						require.Nil(t, info.StreamSession)
						AppendStreamLogInfo(info, other)
					}
					require.Equal(t, enabled && streaming, other["stream_diagnostic_available"] == true)
				}
			}
		}
	}
}
