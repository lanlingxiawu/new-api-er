package service

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestClaudeDiagnosticLegacyAndErrorLogPaths 验证兼容与非 200 路径保存私有响应证据，公开摘要不含底层原因，采集本身不启用严格计费。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeDiagnosticLegacyAndErrorLogPaths(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Set(relaycommon.ClaudeResponseOnlyKey, true)
	for _, status := range []int{200, 503} {
		capture := relaycommon.NewClaudeResponseCapture(status)
		c.Set(relaycommon.ClaudeResponseCaptureKey, capture)
		resp := &http.Response{StatusCode: status, Header: http.Header{"X-Private": {"private-header"}}, Body: io.NopCloser(strings.NewReader("private-response"))}
		capture.Observe(resp)
		var upstreamErr *types.NewAPIError
		if status != 200 {
			upstreamErr = RelayErrorHandler(c, resp, false)
		} else {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		other := map[string]interface{}{}
		common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "private-policy")
		if upstreamErr != nil {
			AppendClaudeErrorDiagnostic(c, other, upstreamErr)
			require.NotContains(t, ClaudePublicErrorSummary(c, upstreamErr), "private")
		} else {
			info := &relaycommon.RelayInfo{ClaudeDiagnostic: capture, ClaudeRejectReason: "private-policy"}
			AppendClaudeStreamLogInfo(info, other)
		}
		diagnostic := other["claude_diagnostic"].(relaycommon.ClaudeStreamDiagnostic)
		require.Equal(t, "private-response", string(diagnostic.BodyHead)+string(diagnostic.BodyTail))
		require.Equal(t, status, diagnostic.Attempt)
		require.Equal(t, "private-policy", diagnostic.RejectReason)
		require.NotContains(t, other, "reject_reason")
		require.NotContains(t, other, "claude_stream", "diagnostics alone must not activate strict billing")
	}
	plain := types.NewError(errors.New("private cause"), types.ErrorCodeBadResponseBody)
	require.NotContains(t, ClaudePublicErrorSummary(c, plain), "private cause")
	status := relaycommon.NewStreamStatus()
	status.SetEndReason(relaycommon.StreamEndReasonScannerErr, errors.New("private stream cause"))
	status.RecordError("private soft error")
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, IsStream: true, StreamStatus: status, ClaudeDiagnostic: relaycommon.NewClaudeResponseCapture(1)}
	other := map[string]interface{}{}
	appendStreamStatus(info, other)
	public, err := common.Marshal(other)
	require.NoError(t, err)
	require.NotContains(t, string(public), "private")
	AppendClaudeStreamLogInfo(info, other)
	require.Equal(t, "private stream cause", other["claude_diagnostic"].(relaycommon.ClaudeStreamDiagnostic).Error)
}
