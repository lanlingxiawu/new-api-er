package dify

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestStreamDifyDTOErrorOrigin 验证有效 JSON 中的字段类型错误进入上游诊断及原计费矩阵；t 为测试上下文。
func TestStreamDifyDTOErrorOrigin(t *testing.T) {
	for _, content := range []bool{false, true} {
		for _, passthrough := range []bool{false, true} {
			body := ""
			if content {
				body = "data: {\"event\":\"message\",\"answer\":\"hello world\"}\n\n"
			}
			body += "data: {\"event\":\"message\",\"answer\":123}\n\n"
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeDify)
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
			info.ChannelSetting.PassThroughBodyEnabled = passthrough
			info.SetEstimatePromptTokens(7)
			service.BeginStreamAttempt(c, info)
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamDiagnostic.Observe(resp)
			info.StreamSession.ObserveHTTP(resp)
			usage, apiErr := difyStreamHandler(c, info, resp)
			require.Nil(t, apiErr)
			selected := service.FinalizeStreamUsage(c, info, usage)
			require.Equal(t, relaycommon.StreamEndReason("upstream_json_error"), info.StreamSession.Snapshot().Reason)
			require.True(t, info.StreamResult.DiagnosticAvailable)
			require.False(t, info.StreamResult.ClientGone)
			require.Equal(t, content, info.StreamResult.EffectiveContent)
			if content {
				require.Equal(t, "estimated", info.StreamResult.UsageSource)
				require.Equal(t, 7, selected.PromptTokens)
				require.Positive(t, selected.CompletionTokens)
			} else {
				require.Equal(t, "none", info.StreamResult.UsageSource)
				require.Zero(t, selected.TotalTokens)
			}
			other := map[string]any{}
			service.AppendStreamLogInfo(info, other)
			require.Equal(t, true, other["stream_diagnostic_available"])
			diagnostic := other["stream_diagnostic"].(relaycommon.StreamDiagnostic)
			require.Equal(t, body, string(append(diagnostic.BodyHead, diagnostic.BodyTail...)))
			require.Contains(t, diagnostic.Error, "answer")
			require.Contains(t, rec.Body.String(), "upstream_stream_error")
			require.NotContains(t, rec.Body.String(), "[DONE]")
		}
	}
}
