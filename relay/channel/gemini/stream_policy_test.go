package gemini

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

// TestUnifiedStreamGeminiPolicyStop 验证策略反馈沿用旧终态和计量；t 控制原生及转换流夹具，不访问资金数据库。
func TestUnifiedStreamGeminiPolicyStop(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatGemini, types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatOpenAIResponses} {
		for _, withUsage := range []bool{false, true} {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1beta/models/gemini:streamGenerateContent", nil)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: format, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-flash"}}
			info.SetEstimatePromptTokens(11)
			service.BeginStreamAttempt(c, info)
			body := `{"promptFeedback":{"blockReason":"SAFETY"}}`
			if withUsage {
				body = `{"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":17,"totalTokenCount":17}}`
			}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + body + "\n\n"))}
			info.StreamSession.ObserveTransport(resp, nil)
			info.StreamSession.ObserveHTTP(resp)
			handler := GeminiChatStreamHandler
			if format == types.RelayFormatGemini {
				handler = GeminiTextGenerationStreamHandler
			} else if format == types.RelayFormatOpenAIResponses {
				handler = GeminiResponsesStreamHandler
			}
			u, err := handler(c, info, resp)
			require.Nil(t, err)
			final := service.FinalizeStreamUsage(c, info, u)
			require.Same(t, u, final, "正常策略终态应保留渠道原有用量对象")
			require.False(t, info.StreamResult.Failed, rec.Body.String())
			require.False(t, info.StreamResult.DiagnosticAvailable)
			require.Equal(t, "gemini_block_reason=SAFETY", common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason))
			require.NotContains(t, rec.Body.String(), "event: error")
			other := service.GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 1, 1, 1)
			require.Equal(t, "gemini_block_reason=SAFETY", other["reject_reason"])
			require.NotContains(t, other, "stream_diagnostic_available")
			diagnostic := other["stream_diagnostic"].(relaycommon.StreamDiagnostic)
			require.Empty(t, diagnostic.BodyHead)
			require.Empty(t, diagnostic.ResponseHeaders)
			if format == types.RelayFormatGemini {
				require.Contains(t, rec.Body.String(), body)
			}
			if withUsage {
				require.Equal(t, 17, final.PromptTokens)
			}
		}
	}
}
