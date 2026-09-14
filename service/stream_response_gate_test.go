package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestStreamResponseGateLegacyFailure 验证未取得 200 时保留旧错误响应与退款所有权，不追加诊断或结算。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestStreamResponseGateLegacyFailure(t *testing.T) {
	for _, status := range []int{0, 101, 201, 204, 302, 401, 429, 500, 503} {
		for _, passthrough := range []bool{false, true} {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{}}
			info.ChannelSetting.PassThroughBodyEnabled = passthrough
			BeginStreamAttempt(c, info)
			if status != 0 {
				info.StreamSession.ObserveTransport(&http.Response{StatusCode: status}, nil)
			} else {
				info.StreamSession.ObserveTransport(nil, context.DeadlineExceeded)
			}
			// 映射后的错误状态为 200 也不作为真实成功响应证据。
			apiErr := types.NewOpenAIError(errors.New("original error"), types.ErrorCodeDoRequestFailed, http.StatusOK)
			require.False(t, FinalizeStreamFailure(c, info, apiErr))
			oldUsage := &dto.Usage{PromptTokens: 3}
			require.Same(t, oldUsage, FinalizeStreamUsage(c, info, oldUsage))
			WriteStreamTerminalError(c, info, relaycommon.StreamSnapshot{})
			require.Empty(t, rec.Body.String())
			require.Nil(t, info.StreamResult)
			require.False(t, c.GetBool(relaycommon.StreamHandledKey), "旧 defer 仍负责退款和错误响应")
			common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "independent policy")
			info.StreamRejectReason = "independent policy"
			other := map[string]any{}
			AppendStreamErrorDiagnostic(c, other, apiErr)
			AppendStreamLogInfo(info, other)
			for _, key := range []string{"stream_diagnostic", "stream_diagnostic_attempt", "stream_diagnostic_available", "stream_result"} {
				require.NotContains(t, other, key)
			}
			require.Equal(t, "independent policy", other["reject_reason"])
			c.Writer.Header().Set("Content-Type", "application/json")
			_, err := c.Writer.WriteString(`{"error":{"message":"original response"}}`)
			require.NoError(t, err)
			require.Equal(t, `{"error":{"message":"original response"}}`, rec.Body.String())
			require.Nil(t, info.StreamDiagnostic.DownstreamBody())
			require.True(t, common.IsPrivateStream(c.Request.Context()), "仍不采集请求内容")
		}
	}
}

// TestStreamResponseGateAttemptLifecycle 验证成功后 EOF 仍接管，渠道重试重新等待实际响应。
// 参数 t：测试上下文，承载断言与测试资源清理。
func TestStreamResponseGateAttemptLifecycle(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
	BeginStreamAttempt(c, info)
	require.False(t, info.StreamSession.Active())
	info.StreamSession.ObserveTransport(&http.Response{StatusCode: 200}, nil)
	info.StreamSession.EndRead(io.EOF)
	FinalizeStreamUsage(c, info, nil)
	require.True(t, info.StreamResult.DiagnosticAvailable)
	first := info.StreamResponseGate
	BeginStreamAttempt(c, info)
	require.NotSame(t, first, info.StreamResponseGate)
	require.False(t, info.StreamSession.Active())
	require.False(t, FinalizeStreamFailure(c, info, errors.New("dial failed")))
	require.Nil(t, info.StreamResult)
	other := map[string]any{}
	AppendStreamErrorDiagnostic(c, other, errors.New("dial failed"))
	require.Empty(t, other)
}
