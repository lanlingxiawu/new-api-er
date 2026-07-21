package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// log.go: export-text helpers, deprecated endpoints, and handler input guards.

func TestLogTypeText(t *testing.T) {
	cases := map[int]string{
		model.LogTypeTopup:   "充值",
		model.LogTypeConsume: "消费",
		model.LogTypeManage:  "管理",
		model.LogTypeSystem:  "系统",
		model.LogTypeError:   "错误",
		model.LogTypeRefund:  "退款",
		9999:                 "未知",
	}
	for in, want := range cases {
		assert.Equalf(t, want, logTypeText(in), "type=%d", in)
	}
}

func TestRetryChainText(t *testing.T) {
	// Empty / non-JSON / missing keys -> empty string.
	assert.Equal(t, "", retryChainText(""))
	assert.Equal(t, "", retryChainText("not-json"))
	assert.Equal(t, "", retryChainText(`{"foo":"bar"}`))
	assert.Equal(t, "", retryChainText(`{"admin_info":{}}`))
	assert.Equal(t, "", retryChainText(`{"admin_info":{"use_channel":[]}}`))

	// Valid retry chain joins with "->".
	got := retryChainText(`{"admin_info":{"use_channel":[1,2,3]}}`)
	assert.Equal(t, "1->2->3", got)
}

func TestSearchLogs_Deprecated(t *testing.T) {
	ctx, rec := newCtx(t, "GET", "/api/log/search", nil)
	SearchAllLogs(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)

	ctx2, rec2 := newCtx(t, "GET", "/api/log/self/search", nil)
	SearchUserLogs(ctx2)
	resp2 := decodeResp(t, rec2)
	assert.False(t, resp2.Success)
}

func TestGetLogByKey_NoTokenId(t *testing.T) {
	ctx, rec := newCtx(t, "GET", "/api/log/token", nil)
	// token_id not set -> 0 -> invalid token
	GetLogByKey(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

func TestDeleteHistoryLogs_MissingTimestamp(t *testing.T) {
	ctx, rec := newCtx(t, "DELETE", "/api/log/", nil)
	DeleteHistoryLogs(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}

func TestExportUserLogs_InvalidUser(t *testing.T) {
	ctx, rec := newCtx(t, "GET", "/api/log/self/export", nil)
	// no id -> 401 defensive guard
	ExportUserLogs(ctx)
	assert.Equal(t, 401, rec.Code)
	var out map[string]any
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, false, out["success"])
}

func TestExportLogsExcel_TimeRangeGuards(t *testing.T) {
	// Missing time range.
	ctx, rec := newCtx(t, "GET", "/api/log/export", nil)
	exportLogsExcel(ctx, 0)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)

	// end < start.
	ctx2, rec2 := newCtx(t, "GET", "/api/log/export?start_timestamp=200&end_timestamp=100", nil)
	exportLogsExcel(ctx2, 0)
	resp2 := decodeResp(t, rec2)
	assert.False(t, resp2.Success)

	// span > 1 month.
	ctx3, rec3 := newCtx(t, "GET", "/api/log/export?start_timestamp=1&end_timestamp=99999999", nil)
	exportLogsExcel(ctx3, 0)
	resp3 := decodeResp(t, rec3)
	assert.False(t, resp3.Success)
}

func TestGetEmployeeCustomerLogs_NotEmployee(t *testing.T) {
	requireDB(t)
	// A fresh common user is not an enabled employee.
	u := mkUser(t, nil)
	ctx, rec := newCtx(t, "GET", "/api/log/employee", nil)
	asUser(ctx, u.Id)
	GetEmployeeCustomerLogs(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)

	ctx2, rec2 := newCtx(t, "GET", "/api/log/employee/stat", nil)
	asUser(ctx2, u.Id)
	GetEmployeeCustomerLogsStat(ctx2)
	resp2 := decodeResp(t, rec2)
	assert.False(t, resp2.Success)
}

func TestGetAllLogs_EmptyFilterSucceeds(t *testing.T) {
	requireLogDB(t)
	// Narrow the query to a non-existent username so the result set is empty
	// and deterministic while still exercising the model query path.
	ctx, rec := newCtx(t, "GET", "/api/log/?username="+uniq("nouser")+"&p=1&page_size=10", nil)
	GetAllLogs(ctx)
	resp := decodeResp(t, rec)
	assert.True(t, resp.Success)
}

func TestGetUserLogs_EmptyFilterSucceeds(t *testing.T) {
	requireLogDB(t)
	ctx, rec := newCtx(t, "GET", "/api/log/self?p=1&page_size=10", nil)
	asUser(ctx, nextTestID())
	GetUserLogs(ctx)
	resp := decodeResp(t, rec)
	assert.True(t, resp.Success)
}

func TestGetLogsStat_And_SelfStat(t *testing.T) {
	requireLogDB(t)
	ctx, rec := newCtx(t, "GET", "/api/log/stat?username="+uniq("nouser"), nil)
	GetLogsStat(ctx)
	resp := decodeResp(t, rec)
	assert.True(t, resp.Success)

	ctx2, rec2 := newCtx(t, "GET", "/api/log/self/stat", nil)
	ctx2.Set("username", uniq("nouser"))
	GetLogsSelfStat(ctx2)
	resp2 := decodeResp(t, rec2)
	assert.True(t, resp2.Success)
}
