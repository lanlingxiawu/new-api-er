package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestClaudeDiagnosticReadBoundary 验证私有证据被移除、原因按 bb6317462 保留，且读取不推断诊断资格。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeDiagnosticReadBoundary(t *testing.T) {
	raw := `{"claude_diagnostic":{"error":"private","response_headers":{"Cookie":["private"]}},"claude_stream":{"usage_source":"upstream"},"keep":9007199254740993}`
	log := Log{Other: raw}
	require.NoError(t, log.AfterFind(nil))
	require.NotContains(t, log.Other, "private")
	require.Contains(t, log.Other, "9007199254740993")
	require.NotContains(t, log.Other, "diagnostic_available")
	b, err := common.Marshal(log)
	require.NoError(t, err)
	require.NotContains(t, string(b), "private")
	require.Contains(t, raw, "private")
	log.Other = `{"claude_diagnostic": broken`
	require.NoError(t, log.AfterFind(nil))
	require.Equal(t, "{}", log.Other)
	log.Other = `{"reject_reason":"private-policy","keep":1}`
	require.NoError(t, log.AfterFind(nil))
	require.Contains(t, log.Other, "private-policy")
	require.NotContains(t, log.Other, "diagnostic_available")
	for _, tc := range []struct {
		raw, reason string
		available   bool
	}{
		{`{"claude_diagnostic":{"reject_reason":"policy","error":"secret"},"claude_diagnostic_available":true}`, "policy", false},
		{`{"reject_reason":"top","claude_diagnostic":{"reject_reason":"nested","error":"secret"},"stream_diagnostic_available":true}`, "top", true},
		{`{"claude_diagnostic":null,"stream_diagnostic_available":false}`, "", false},
		{`{"claude_diagnostic":{},"stream_diagnostic_available":"true"}`, "", false},
	} {
		log.Other = tc.raw
		require.NoError(t, log.AfterFind(nil))
		var fields map[string]any
		require.NoError(t, common.UnmarshalJsonStr(log.Other, &fields))
		require.NotContains(t, fields, "claude_diagnostic")
		require.NotContains(t, fields, "claude_diagnostic_available")
		require.Equal(t, tc.available, fields["stream_diagnostic_available"] == true)
		if tc.reason != "" {
			require.Equal(t, tc.reason, fields["reject_reason"])
		}
	}
	log.Other = `{"reject_reason":"policy","admin_info":{"private":1},"audit_info":{"private":2}}`
	formatUserLogs([]*Log{&log}, 0)
	require.Contains(t, log.Other, `"reject_reason":"policy"`)
	require.NotContains(t, log.Other, "private")
}

// TestClaudeDiagnosticDatabaseProjection 验证标准 GORM 读取被过滤，而独立诊断投影按请求、时间、尝试编号正确读取私有数据。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeDiagnosticDatabaseProjection(t *testing.T) {
	old := LOG_DB
	db := LOG_DB
	if db == nil {
		var err error
		db, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		require.NoError(t, db.AutoMigrate(&Log{}))
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	LOG_DB = db
	t.Cleanup(func() { LOG_DB = old })
	row := Log{RequestId: common.NewRequestId(), CreatedAt: common.GetTimestamp(), Other: `{"claude_diagnostic":{"error":"root-only-body"},"stream_diagnostic_available":true,"stream_status":{"status":"error"}}`}
	prior := Log{RequestId: row.RequestId, CreatedAt: row.CreatedAt, Other: `{"retry":true}`}
	require.NoError(t, db.Create(&prior).Error)
	require.NoError(t, db.Create(&row).Error)
	t.Cleanup(func() { db.Where("request_id = ? AND created_at = ?", row.RequestId, row.CreatedAt).Delete(&Log{}) })
	var read Log
	require.NoError(t, db.Where("id = ? AND request_id = ?", row.Id, row.RequestId).Take(&read).Error)
	require.NotContains(t, read.Other, "root-only-body")
	require.Contains(t, read.Other, "stream_diagnostic_available")
	detail, err := GetStreamDiagnostic(context.Background(), row.RequestId, row.CreatedAt)
	require.NoError(t, err)
	require.Contains(t, string(detail), "root-only-body")
	_, err = GetStreamDiagnostic(context.Background(), row.RequestId, row.CreatedAt+1)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	for _, attempt := range []int{1, 2} {
		payload, marshalErr := common.Marshal(map[string]interface{}{"claude_diagnostic": map[string]interface{}{"attempt": attempt, "error": "attempt-private"}})
		require.NoError(t, marshalErr)
		require.NoError(t, db.Create(&Log{RequestId: row.RequestId, CreatedAt: row.CreatedAt, Other: string(payload)}).Error)
	}
	detail, err = GetStreamDiagnostic(context.Background(), row.RequestId, row.CreatedAt, 2)
	require.NoError(t, err)
	require.Contains(t, string(detail), `"attempt":2`)
	_, err = GetStreamDiagnostic(context.Background(), row.RequestId, row.CreatedAt, 3)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	// 同行同时存在新旧 envelope 时以新字段为准，普通读取仍剥离所有正文。
	newRow := Log{RequestId: row.RequestId, CreatedAt: row.CreatedAt, Other: `{"claude_diagnostic":{"attempt":4,"error":"old-private"},"stream_diagnostic":{"attempt":4,"error":"new-private","downstream_body_base64":"Ym9keQ=="},"stream_diagnostic_attempt":4,"stream_diagnostic_available":true}`}
	require.NoError(t, db.Create(&newRow).Error)
	detail, err = GetStreamDiagnostic(context.Background(), row.RequestId, row.CreatedAt, 4)
	require.NoError(t, err)
	require.Contains(t, string(detail), "new-private")
	require.Contains(t, string(detail), "Ym9keQ==")
	require.NotContains(t, string(detail), "old-private")
	read = Log{}
	require.NoError(t, db.Where("id = ?", newRow.Id).Take(&read).Error)
	require.NotContains(t, read.Other, "private")
	require.NotContains(t, read.Other, "Ym9keQ==")
	// 连接失败可先写渠道错误日志，再补发错误并写终止日志；同秒同尝试优先终止快照。
	for _, raw := range []string{
		`{"stream_diagnostic":{"attempt":5,"error":"early","downstream_body_base64":""}}`,
		`{"stream_result":{"failed":true},"stream_diagnostic":{"attempt":5,"error":"terminal","downstream_body_base64":"ZXJyb3I="}}`,
	} {
		require.NoError(t, db.Create(&Log{RequestId: row.RequestId, CreatedAt: row.CreatedAt, Other: raw}).Error)
	}
	detail, err = GetStreamDiagnostic(context.Background(), row.RequestId, row.CreatedAt, 5)
	require.NoError(t, err)
	require.Contains(t, string(detail), "ZXJyb3I=")
	require.NotContains(t, string(detail), "early")
}
