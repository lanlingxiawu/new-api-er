package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestStreamResponseGateClaudeLegacy 使用本地上游验证原生 Claude 在转换/透传两条入口均保留旧失败路径。
// 参数 t 为测试上下文；只调用失败分支，不触达真实渠道或资金数据库。
func TestStreamResponseGateClaudeLegacy(t *testing.T) {
	for _, status := range []int{0, 401, 503} {
		for _, passthrough := range []bool{false, true} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if status == 0 {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						_ = conn.Close() // 未发送任何 HTTP 响应即断开。
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Error-Fixture", "not-collected")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"type":"error","error":{"type":"api_error","message":"fixture upstream error"}}`)
			}))
			t.Cleanup(srv.Close)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"claude","stream":true,"max_tokens":1,"messages":[]}`))
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAnthropic)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, srv.URL)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "claude")
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: passthrough})
			c.Set("status_code_mapping", `{"401":"403","503":"502"}`)
			info := &relaycommon.RelayInfo{IsStream: true, DisablePing: true, RelayFormat: types.RelayFormatClaude, OriginModelName: "claude", Request: &dto.ClaudeRequest{Model: "claude", Stream: common.GetPointer(true)}, ChannelMeta: &relaycommon.ChannelMeta{}}
			service.BeginStreamAttempt(c, info)
			apiErr := ClaudeHelper(c, info)
			require.NotNil(t, apiErr, "须交回控制器的原错误路径")
			if status == 0 {
				require.Equal(t, types.ErrorCodeDoRequestFailed, apiErr.GetErrorCode())
			} else {
				require.Equal(t, map[int]int{401: 403, 503: 502}[status], apiErr.StatusCode)
			}
			require.False(t, service.FinalizeStreamFailure(c, info, apiErr))
			require.False(t, c.GetBool(relaycommon.StreamHandledKey))
			require.Nil(t, info.StreamResult)
			require.Empty(t, rec.Body.String())
			other := model.NewLogOther()
			service.AppendStreamErrorDiagnostic(c, other, apiErr)
			require.Empty(t, other.Snapshot())
			require.Equal(t, relaycommon.StreamDiagnostic{}, info.StreamDiagnostic.Snapshot())
		}
	}
}
