package model

import (
	"testing"

	"github.com/QuantumNous/new-api/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 设计 relay-timeout-cost-bearing.md §4：admin_info.timeout_absorbed 仅管理员可见——管理员导出有
// timeout_absorbed_quota_min 列，自助导出剔除该列，用户视图的 other 投影不含该记录。

const timeoutAbsorbedOther = `{"model_ratio":2,"admin_info":{"use_channel":["3"],"timeout_absorbed":{"mode":"refund","kind":"non_stream","input_tokens":40,"input_estimated":true,"received_output_tokens":0,"absorbed_quota_min":80}}}`

func TestLogExportTimeoutAbsorbedColumn(t *testing.T) {
	ctx := newTestRowCtx()
	assert.Equal(t, "80", renderOne(t, "timeout_absorbed_quota_min", &Log{Type: LogTypeError, Other: timeoutAbsorbedOther}, ctx))
	assert.Equal(t, "", renderOne(t, "timeout_absorbed_quota_min", &Log{Type: LogTypeConsume, Other: `{"admin_info":{"use_channel":["3"]}}`}, ctx))
	assert.Equal(t, "", renderOne(t, "timeout_absorbed_quota_min", &Log{Type: LogTypeConsume, Other: ``}, ctx))

	col, ok := LookupLogExportColumn("timeout_absorbed_quota_min")
	require.True(t, ok)
	assert.True(t, col.AdminOnly)
	assert.Equal(t, LogExportAudienceInternal, col.Audience)

	self, err := ResolveLogExportColumns([]string{"created_at", "timeout_absorbed_quota_min"}, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"created_at"}, self.Keys, "self export drops the column")
	assert.Equal(t, []string{"timeout_absorbed_quota_min"}, self.Dropped)

	require.NoError(t, i18n.Init())
	assert.NotEqual(t, LogExportColumnI18nKey("timeout_absorbed_quota_min"),
		i18n.Translate("en", LogExportColumnI18nKey("timeout_absorbed_quota_min")), "header must be translated")
}

func TestUserLogViewStripsTimeoutAbsorbed(t *testing.T) {
	user := formatLogOtherJSON(timeoutAbsorbedOther, logOtherVisibilityUser)
	assert.NotContains(t, user, "timeout_absorbed")
	assert.NotContains(t, user, "admin_info")
	assert.Contains(t, formatLogOtherJSON(timeoutAbsorbedOther, logOtherVisibilityAdmin), "timeout_absorbed")
}

// TestEmployeeLogViewStripsTimeoutAbsorbed 员工视图保留 admin_info（重试链等），但去掉平台成本记录，
// 其余字段保持原始 JSON（大整数不经 float64）。
func TestEmployeeLogViewStripsTimeoutAbsorbed(t *testing.T) {
	logs := []*Log{
		{Other: `{"amount":9007199254740993,"admin_info":{"use_channel":["3"],"multi_key_index":9007199254740993,"timeout_absorbed":{"mode":"refund","absorbed_quota_min":80}}}`},
		{Other: `{"admin_info":{"use_channel":["3"]}}`},
	}
	formatEmployeeLogs(logs)
	assert.NotContains(t, logs[0].Other, "timeout_absorbed")
	assert.Contains(t, logs[0].Other, `"use_channel":["3"]`)
	assert.Contains(t, logs[0].Other, `"multi_key_index":9007199254740993`)
	assert.Contains(t, logs[0].Other, `"amount":9007199254740993`)
	assert.Equal(t, `{"admin_info":{"use_channel":["3"]}}`, logs[1].Other, "untouched when there is nothing to hide")
}
