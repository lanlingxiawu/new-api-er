package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageWithCurrentRequestId(t *testing.T) {
	withID, _ := gin.CreateTestContext(httptest.NewRecorder())
	withID.Set(common.RequestIdKey, "current")
	withoutID, _ := gin.CreateTestContext(httptest.NewRecorder())
	for _, test := range []struct {
		name    string
		c       *gin.Context
		message string
		want    string
	}{
		{name: "nil context strips stale ids", c: nil, message: "boom (request id: stale)", want: "boom"},
		{name: "context without id strips stale ids", c: withoutID, message: "boom (request id: stale)", want: "boom"},
		{name: "current id replaces stale ids", c: withID, message: "boom (request id: stale)", want: "boom (request id: current)"},
		{name: "current id appended", c: withID, message: "boom", want: "boom (request id: current)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, MessageWithCurrentRequestId(test.c, test.message))
		})
	}
}

// processChannelErrorLogFixture 构造一个已鉴权中转请求的上下文并打开错误日志开关；token_id 取唯一值，
// 返回的查询函数只读取本用例写入的错误日志并在结束时删除它们。
func processChannelErrorLogFixture(t *testing.T, tokenID int, requestID string) (*gin.Context, func() []model.Log) {
	t.Helper()
	previousErrorLog := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		constant.ErrorLogEnabled = previousErrorLog
		model.DrainRelayLogsSync(5 * time.Second)
		require.NoError(t, model.LOG_DB.Where("token_id = ?", tokenID).Delete(&model.Log{}).Error)
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, requestID)
	c.Set("id", 7)
	c.Set("token_name", "error-log-token")
	c.Set("token_id", tokenID)
	c.Set("original_model", "error-log-model")
	c.Set("group", "default")
	common.SetContextKey(c, constant.ContextKeyIsStream, true)
	return c, func() []model.Log {
		model.DrainRelayLogsSync(5 * time.Second)
		var logs []model.Log
		require.NoError(t, model.LOG_DB.Where("type = ? AND token_id = ?", model.LogTypeError, tokenID).Find(&logs).Error)
		return logs
	}
}

func TestProcessChannelErrorRecordsPublicSummaryWithCurrentRequestId(t *testing.T) {
	c, readLogs := processChannelErrorLogFixture(t, 91000101, "svc-current-request")
	apiErr := types.NewOpenAIError(errors.New("upstream failed api_key:review-secret (request id: upstream-stale)"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)

	ProcessChannelError(c, types.ChannelError{ChannelId: 101, ChannelName: "snapshot"}, apiErr, nil)

	logs := readLogs()
	require.Len(t, logs, 1)
	assert.Equal(t, 101, logs[0].ChannelId)
	assert.Equal(t, "svc-current-request", logs[0].RequestId)
	assert.True(t, logs[0].IsStream)
	assert.Equal(t, MessageWithCurrentRequestId(c, apiErr.MaskSensitiveErrorWithStatusCode()), logs[0].Content)
	assert.Contains(t, logs[0].Content, "(request id: svc-current-request)")
	assert.NotContains(t, logs[0].Content, "upstream-stale")
	assert.NotContains(t, logs[0].Content, "review-secret")
	other, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	assert.Equal(t, "/v1/chat/completions", other["request_path"])
	assert.Equal(t, float64(http.StatusBadGateway), other["status_code"])
	assert.Equal(t, string(types.ErrorCodeBadResponseStatusCode), other["error_code"])
	assert.NotContains(t, other, "stream_diagnostic")
}

func TestProcessChannelErrorResponseOnlyModeHidesCauseAndKeepsDiagnostic(t *testing.T) {
	c, readLogs := processChannelErrorLogFixture(t, 91000102, "svc-response-only")
	c.Set(relaycommon.StreamResponseOnlyKey, true)
	common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "policy-blocked")
	apiErr := types.NewOpenAIError(errors.New("low-level private cause"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)

	ProcessChannelError(c, types.ChannelError{ChannelId: 102}, apiErr, nil)

	logs := readLogs()
	require.Len(t, logs, 1)
	assert.Equal(t, "upstream response failed (status=502, code=bad_response_status_code) (request id: svc-response-only)", logs[0].Content)
	other, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	assert.NotContains(t, logs[0].Other, "low-level private cause")
	// 策略原因触发流式诊断：日志保存诊断尝试编号与策略原因。
	assert.Contains(t, other, "stream_diagnostic_attempt", logs[0].Other)
	assert.Equal(t, "policy-blocked", other["reject_reason"], logs[0].Other)
	adminInfo, ok := other["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "policy-blocked", adminInfo["reject_reason"])
	assert.NotContains(t, other, "stream_diagnostic_available")
}

func TestProcessChannelErrorSkipsErrorLogWhenDisabledOrNotRecorded(t *testing.T) {
	c, readLogs := processChannelErrorLogFixture(t, 91000103, "svc-skip")
	ProcessChannelError(c, types.ChannelError{ChannelId: 103}, nil, nil)
	ProcessChannelError(c, types.ChannelError{ChannelId: 103}, types.NewError(errors.New("not recorded"), types.ErrorCodeBadResponse, types.ErrOptionWithNoRecordErrorLog()), nil)
	constant.ErrorLogEnabled = false
	ProcessChannelError(c, types.ChannelError{ChannelId: 103}, types.NewError(errors.New("log disabled"), types.ErrorCodeBadResponse), nil)
	assert.Empty(t, readLogs())
}
