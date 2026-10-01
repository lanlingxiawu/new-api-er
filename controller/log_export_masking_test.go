package controller

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

// exportedDetails returns the 详情 (last) column of every data row.
func exportedDetails(t *testing.T, body []byte) []string {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(body))
	require.NoError(t, err)
	defer f.Close()
	rows, err := f.GetRows("Sheet1")
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	var details []string
	for _, row := range rows[1:] {
		if len(row) > 0 {
			details = append(details, row[len(row)-1])
		}
	}
	return details
}

// A user's own export is the log list in another form; with masking on it must
// not hand back the upstream's original error text the list view hides.
func TestExportUserLogs_MasksUpstreamErrorsForUser(t *testing.T) {
	requireLogDB(t)
	u := mkUser(t, nil)
	now := time.Now().Unix()
	upstreamText := "token quota is not enough, token remain quota: $0.000002 (request id: up-1)"
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		UserId: u.Id, Username: u.Username, CreatedAt: now, Type: model.LogTypeError,
		Content: upstreamText, RequestId: "rid-export-1",
		Other: `{"error_type":"openai_error","error_code":"bad_response_status_code","status_code":403}`,
	}).Error)
	t.Cleanup(func() { model.LOG_DB.Where("user_id = ?", u.Id).Delete(&model.Log{}) })

	previous := operation_setting.GetRelayErrorDisplaySetting()
	t.Cleanup(func() { operation_setting.ReplaceRelayErrorDisplaySetting(previous) })
	target := fmt.Sprintf("/api/log/self/export?start_timestamp=%d&end_timestamp=%d", now-60, now+60)

	operation_setting.ReplaceRelayErrorDisplaySetting(operation_setting.RelayErrorDisplaySetting{Enabled: true, HideUpstreamErrors: true, DefaultMessage: "服务暂时不可用"})
	ctx, rec := newCtx(t, "GET", target, nil)
	asUser(ctx, u.Id)
	ExportUserLogs(ctx)
	details := exportedDetails(t, rec.Body.Bytes())
	require.Len(t, details, 1)
	assert.Equal(t, "服务暂时不可用 (request id: rid-export-1)", details[0])
	assert.False(t, strings.Contains(strings.Join(details, "\n"), "remain quota"))

	operation_setting.ReplaceRelayErrorDisplaySetting(operation_setting.RelayErrorDisplaySetting{Enabled: false, HideUpstreamErrors: true})
	ctx2, rec2 := newCtx(t, "GET", target, nil)
	asUser(ctx2, u.Id)
	ExportUserLogs(ctx2)
	assert.Equal(t, []string{upstreamText}, exportedDetails(t, rec2.Body.Bytes()), "switched off, the export is unchanged")
}
