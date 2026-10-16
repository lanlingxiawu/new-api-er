package controller

import (
	"bytes"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

// Branch audit: the same error row seen through the admin list, the owner list
// and the owner's export.

type branchAuditLogPage struct {
	Total int         `json:"total"`
	Items []model.Log `json:"items"`
}

func branchAuditDecodeLogPage(t *testing.T, resp apiResp) branchAuditLogPage {
	t.Helper()
	require.True(t, resp.Success, resp.Message)
	var page branchAuditLogPage
	require.NoError(t, common.Unmarshal(resp.Data, &page))
	return page
}

func branchAuditWithMasking(t *testing.T) {
	t.Helper()
	previous := operation_setting.GetRelayErrorDisplaySetting()
	t.Cleanup(func() { operation_setting.ReplaceRelayErrorDisplaySetting(previous) })
	operation_setting.ReplaceRelayErrorDisplaySetting(operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "Unavailable"})
}

// Admins keep the stored original (the design's troubleshooting basis) and the
// admin scope; the owner gets the projected, masked row.
func TestBranchAuditLogViews_AdminRawOwnerMasked(t *testing.T) {
	requireLogDB(t)
	branchAuditWithMasking(t)
	u := mkUser(t, nil)
	rid := uniq("rid-view")
	upstreamText := "quota of group vip-azure exhausted (request id: " + rid + ")"
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		UserId: u.Id, Username: u.Username, CreatedAt: time.Now().Unix(), Type: model.LogTypeError,
		Content: upstreamText, RequestId: rid, ChannelId: 7,
		Other: `{"error_type":"openai_error","error_code":"bad_response_status_code","status_code":429,"admin_info":{"use_channel":["7"]}}`,
	}).Error)
	t.Cleanup(func() { model.LOG_DB.Where("user_id = ?", u.Id).Delete(&model.Log{}) })

	adminCtx, adminRec := newCtx(t, "GET", "/api/log/?p=1&page_size=10&request_id="+rid, nil)
	asAdmin(adminCtx, nextTestID())
	GetAllLogs(adminCtx)
	adminPage := branchAuditDecodeLogPage(t, decodeResp(t, adminRec))
	require.Len(t, adminPage.Items, 1)
	assert.Equal(t, upstreamText, adminPage.Items[0].Content)
	assert.Contains(t, adminPage.Items[0].Other, "admin_info")
	assert.Equal(t, 7, adminPage.Items[0].ChannelId)

	userCtx, userRec := newCtx(t, "GET", "/api/log/self?p=1&page_size=10&request_id="+rid, nil)
	asUser(userCtx, u.Id)
	GetUserLogs(userCtx)
	userPage := branchAuditDecodeLogPage(t, decodeResp(t, userRec))
	require.Len(t, userPage.Items, 1)
	assert.Equal(t, "Unavailable (request id: "+rid+")", userPage.Items[0].Content)
	assert.NotContains(t, userPage.Items[0].Other, "admin_info")
	assert.NotContains(t, userPage.Items[0].Other, "vip-azure")
	assert.Equal(t, 1, userPage.Items[0].Id, "owners see display ids, not row ids")

	// Another user asking for the same request id gets nothing.
	otherCtx, otherRec := newCtx(t, "GET", "/api/log/self?p=1&page_size=10&request_id="+rid, nil)
	asUser(otherCtx, nextTestID())
	GetUserLogs(otherCtx)
	assert.Empty(t, branchAuditDecodeLogPage(t, decodeResp(t, otherRec)).Items)
}

func branchAuditExportRows(t *testing.T, body []byte) [][]string {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(body))
	require.NoError(t, err)
	defer f.Close()
	rows, err := f.GetRows("Sheet1")
	require.NoError(t, err)
	require.NotEmpty(t, rows, "header row")
	return rows[1:]
}

// The self export is locked to the caller (a username parameter cannot widen it),
// blanks the channel and retry columns, and accepts a span of exactly 30 days.
