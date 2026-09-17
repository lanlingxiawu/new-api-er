package service

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
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
	c.Set(relaycommon.StreamResponseOnlyKey, true)
	for _, status := range []int{200, 503} {
		capture := relaycommon.NewStreamResponseCapture(status)
		c.Set(relaycommon.StreamResponseCaptureKey, capture)
		resp := &http.Response{StatusCode: status, Header: http.Header{"X-Private": {"private-header"}}, Body: io.NopCloser(strings.NewReader("private-response"))}
		capture.Observe(resp)
		var upstreamErr *types.NewAPIError
		if status != 200 {
			upstreamErr = RelayErrorHandler(c, resp, false)
		} else {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		other := model.NewLogOther()
		common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "private-policy")
		if upstreamErr != nil {
			AppendStreamErrorDiagnostic(c, other, upstreamErr)
			require.NotContains(t, StreamPublicErrorSummary(c, upstreamErr), "private")
		} else {
			info := &relaycommon.RelayInfo{StreamDiagnostic: capture, StreamRejectReason: "private-policy"}
			AppendStreamLogInfo(info, other)
		}
		fields := other.Snapshot()
		diagnostic := fields["stream_diagnostic"].(relaycommon.StreamDiagnostic)
		require.Equal(t, "private-response", string(diagnostic.BodyHead)+string(diagnostic.BodyTail))
		require.Equal(t, status, diagnostic.Attempt)
		require.Equal(t, "private-policy", diagnostic.RejectReason)
		require.Equal(t, "private-policy", logOtherAdmin(other)["reject_reason"])
		require.NotContains(t, fields, "stream_diagnostic_available", "legacy capture alone is not the new stream failure flow")
		require.NotContains(t, fields, "stream_result", "diagnostics alone must not activate strict billing")
	}
	plain := types.NewError(errors.New("private cause"), types.ErrorCodeBadResponseBody)
	require.NotContains(t, StreamPublicErrorSummary(c, plain), "private cause")
	status := relaycommon.NewStreamStatus()
	status.SetEndReason(relaycommon.StreamEndReasonScannerErr, errors.New("private stream cause"))
	status.RecordError("private soft error")
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, IsStream: true, StreamStatus: status, StreamDiagnostic: relaycommon.NewStreamResponseCapture(1)}
	other := model.NewLogOther()
	appendStreamStatus(info, other)
	public, err := common.Marshal(other)
	require.NoError(t, err)
	require.NotContains(t, string(public), "private")
	AppendStreamLogInfo(info, other)
	require.Equal(t, "private stream cause", other.Snapshot()["stream_diagnostic"].(relaycommon.StreamDiagnostic).Error)
}

// TestStreamDiagnosticResponsePersistence 验证新流程双日志入口仅在明确上游异常时保存原始响应；t 为测试上下文。
func TestStreamDiagnosticResponsePersistence(t *testing.T) {
	for _, kind := range []string{"pending", "normal", "client", "local", "eof", "read", "json", "protocol", "error", "upstream_then_client", "capture_off"} {
		t.Run(kind, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
			info := &relaycommon.RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
			BeginStreamAttempt(c, info)
			if kind == "capture_off" {
				info.StreamDiagnostic = nil
				c.Set(relaycommon.StreamResponseCaptureKey, (*relaycommon.StreamResponseCapture)(nil))
			}
			// 五次 SDK 交换覆盖主响应、历史响应及淘汰计数，长正文覆盖首尾截断。
			for i := 0; i < 5; i++ {
				body := strings.Repeat("private-head-"+strconv.Itoa(i)+"|", 100) + strings.Repeat("private-tail-"+strconv.Itoa(i)+"|", 100)
				resp := &http.Response{StatusCode: 200, Header: http.Header{"X-Private": {"private-header-" + strconv.Itoa(i)}}, Body: io.NopCloser(strings.NewReader(body))}
				info.StreamSession.ObserveTransport(resp, nil)
				info.StreamDiagnostic.Observe(resp)
				_, err := io.Copy(io.Discard, resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
			}
			common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "policy-reason")
			info.StreamRejectReason = "policy-reason"
			wantAvailable := true
			switch kind {
			case "pending":
				wantAvailable = false
			case "normal":
				info.StreamSession.Complete()
				wantAvailable = false
			case "client":
				cancel()
				wantAvailable = false
			case "local":
				info.StreamSession.FailRelay("upstream_read_error", errors.New("local conversion"))
				wantAvailable = false
			case "eof", "capture_off":
				info.StreamSession.EndRead(io.EOF)
			case "read", "upstream_then_client":
				info.StreamSession.EndRead(io.ErrUnexpectedEOF)
				if kind == "upstream_then_client" {
					cancel()
				}
			case "json", "protocol", "error":
				reason := "upstream_" + kind
				if kind != "error" {
					reason += "_error"
				}
				info.StreamSession.Fail(relaycommon.StreamEndReason(reason), errors.New("upstream fixture error"))
			}
			wantContent := wantAvailable && kind != "capture_off"
			errorOther := model.NewLogOther()
			AppendStreamErrorDiagnostic(c, errorOther, errors.New("private log cause"))
			errorSource := info.StreamDiagnostic.Snapshot()
			if wantAvailable {
				errorSource.Attempt = c.GetInt(relaycommon.StreamDiagnosticAttemptKey)
			}
			assertStreamResponseLogContent(t, errorOther, errorSource, wantContent)
			errorFields := errorOther.Snapshot()
			require.Equal(t, wantAvailable, errorFields["stream_diagnostic_available"] == true)
			require.Equal(t, "policy-reason", logOtherAdmin(errorOther)["reject_reason"])
			require.Equal(t, "private log cause", errorFields["stream_diagnostic"].(relaycommon.StreamDiagnostic).Error)
			if kind != "pending" {
				FinalizeStreamUsage(c, info, nil)
				// 详细证据与资金状态独立于响应内容过滤，使用固定哨兵检查没有连带删除。
				info.StreamResult.Diagnostic.UsageEvidence = map[string]int{"output_tokens": 17}
				info.StreamResult.Diagnostic.EstimatedUsage = map[string]int{"output_tokens": 9}
				info.StreamResult.Diagnostic.SettlementError = "settlement evidence"
			}
			before := info.StreamDiagnostic.Snapshot()
			other := model.NewLogOther()
			AppendStreamLogInfo(info, other)
			fields := other.Snapshot()
			source := before
			if info.StreamResult != nil {
				source = info.StreamResult.Diagnostic
				require.Same(t, info.StreamResult, fields["stream_result"])
				logged := fields["stream_diagnostic"].(relaycommon.StreamDiagnostic)
				require.Equal(t, source.UsageEvidence, logged.UsageEvidence)
				require.Equal(t, source.EstimatedUsage, logged.EstimatedUsage)
				require.Equal(t, source.SettlementError, logged.SettlementError)
			}
			assertStreamResponseLogContent(t, other, source, wantContent)
			require.Equal(t, wantAvailable, fields["stream_diagnostic_available"] == true)
			require.Equal(t, "policy-reason", logOtherAdmin(other)["reject_reason"])
			require.Equal(t, before, info.StreamDiagnostic.Snapshot(), "生成日志不修改后续终止/轮次可能使用的内存缓存")
			if kind != "capture_off" {
				require.Len(t, source.PreviousResponses, 3)
				require.NotEmpty(t, source.BodyHead)
				require.NotEmpty(t, source.PreviousResponses[0].BodyHead, "过滤日志副本不改写固定结果的历史响应")
			}
		})
	}
}

// assertStreamResponseLogContent 检查序列化后的主/历史响应及元数据；t 为测试上下文，other 为日志，source 为原快照，keep 指定是否保留内容。
func assertStreamResponseLogContent(t *testing.T, other *model.LogOther, source relaycommon.StreamDiagnostic, keep bool) {
	t.Helper()
	raw, err := common.Marshal(other)
	require.NoError(t, err)
	var stored struct {
		Diagnostic relaycommon.StreamDiagnostic `json:"stream_diagnostic"`
	}
	require.NoError(t, common.Unmarshal(raw, &stored))
	require.Equal(t, source.Attempt, stored.Diagnostic.Attempt)
	require.Equal(t, source.OmittedResponses, stored.Diagnostic.OmittedResponses)
	require.Len(t, stored.Diagnostic.PreviousResponses, len(source.PreviousResponses))
	want := append([]relaycommon.StreamResponseSnapshot{source.StreamResponseSnapshot}, source.PreviousResponses...)
	got := append([]relaycommon.StreamResponseSnapshot{stored.Diagnostic.StreamResponseSnapshot}, stored.Diagnostic.PreviousResponses...)
	for i := range want {
		require.Equal(t, want[i].StatusCode, got[i].StatusCode)
		require.Equal(t, want[i].ReadEOF, got[i].ReadEOF)
		require.Equal(t, want[i].ReadError, got[i].ReadError)
		if keep {
			require.Equal(t, want[i], got[i])
		} else {
			require.Empty(t, got[i].ResponseHeaders)
			require.Empty(t, got[i].BodyHead)
			require.Empty(t, got[i].BodyTail)
			if len(want[i].BodyHead) > 0 {
				require.NotContains(t, string(raw), base64.StdEncoding.EncodeToString(want[i].BodyHead))
				require.NotContains(t, string(raw), base64.StdEncoding.EncodeToString(want[i].BodyTail))
			}
		}
	}
	if !keep {
		require.NotContains(t, string(raw), "private-header-")
		require.Nil(t, stored.Diagnostic.DownstreamBodyBase64)
	}
}

// logOtherAdmin 返回日志字段 admin_info 作用域的快照；无管理员字段时返回 nil。
func logOtherAdmin(other *model.LogOther) map[string]any {
	adminInfo, _ := other.Snapshot()["admin_info"].(map[string]any)
	return adminInfo
}
