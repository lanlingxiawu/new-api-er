package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestResponsesWebSocketDialFailureWritesSameErrorLogAsHTTPRelay 确认 Responses WebSocket 握手失败时
// 与 HTTP 中转写出相同内容的错误日志：公开错误摘要附当前请求 id，不带上游原始响应体，公开层含
// request_path / error_type / error_code / status_code，渠道身份只保存在 admin_info。
func TestResponsesWebSocketDialFailureWritesSameErrorLogAsHTTPRelay(t *testing.T) {
	previousErrorLog := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLog })

	fixture := newResponsesWSBillingTest(t, `tier("output", c * 2)`, func(*websocket.Conn, *http.Request) {
		t.Error("the rejecting upstream must not accept a websocket connection")
	})
	rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream-private-body"}}`))
	}))
	t.Cleanup(rejecting.Close)
	var channel model.Channel
	require.NoError(t, model.DB.Where("name = ?", "responses-ws-upstream").First(&channel).Error)
	require.NoError(t, model.DB.Model(&channel).Update("base_url", rejecting.URL).Error)

	updates := make(chan struct{}, 4)
	require.NoError(t, model.DB.Callback().Update().After("gorm:commit_or_rollback_transaction").Register("responses-ws-error-log-refund", func(tx *gorm.DB) {
		if tx.Error == nil && tx.Statement.Table == "tokens" {
			select {
			case updates <- struct{}{}:
			default:
			}
		}
	}))
	require.NoError(t, fixture.client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"ws-billing","input":"hi","max_output_tokens":10}`)))
	rejection := readResponsesWSTestEvent(t, fixture.client)
	assert.Equal(t, "error", rejection["type"])
	assert.Equal(t, float64(http.StatusBadRequest), rejection["status"])

	// 预扣额度的退还是异步的；等它落库后再结束，避免后台任务使用已关闭的测试库。
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		require.NoError(t, model.DB.First(fixture.token, fixture.token.Id).Error)
		if fixture.token.RemainQuota == 3000 {
			break
		}
		select {
		case <-updates:
		case <-deadline.C:
			t.Fatal("dial failure did not refund the token reservation")
		}
	}
	fixture.closeAndWait(t)
	drainRelayLogsForTest(t)

	var logs []model.Log
	require.NoError(t, model.LOG_DB.Where("type = ? AND token_id = ?", model.LogTypeError, fixture.token.Id).Find(&logs).Error)
	require.Len(t, logs, 1)
	stored := logs[0]
	const requestID = "responses-ws-billing-ws-0"
	assert.Equal(t, channel.Id, stored.ChannelId)
	assert.Equal(t, requestID, stored.RequestId)
	assert.Equal(t, "ws-billing", stored.ModelName)
	assert.True(t, stored.IsStream)
	assert.True(t, strings.HasPrefix(stored.Content, "status_code=400, "), stored.Content)
	assert.True(t, strings.HasSuffix(stored.Content, "(request id: "+requestID+")"), stored.Content)
	assert.Equal(t, 1, strings.Count(stored.Content, "(request id:"), stored.Content)
	assert.NotContains(t, stored.Content, "upstream-private-body")

	other, err := common.StrToMap(stored.Other)
	require.NoError(t, err)
	assert.Equal(t, "/v1/responses", other["request_path"])
	assert.Equal(t, float64(http.StatusBadRequest), other["status_code"])
	assert.NotEmpty(t, other["error_type"])
	assert.NotEmpty(t, other["error_code"])
	for _, key := range []string{"channel_id", "channel_name", "channel_type", "stream_diagnostic"} {
		assert.NotContains(t, other, key)
	}
	adminInfo, ok := other["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{strconv.Itoa(channel.Id)}, adminInfo["use_channel"])
}
