package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestClaudeDiagnosticReadBoundary 验证普通日志读取移除私有诊断和历史策略原因，保留大整数及其他公开字段。
// 参数 t：当前测试上下文，用于断言、子测试与清理；无返回值，失败通过测试断言报告。
func TestClaudeDiagnosticReadBoundary(t *testing.T) {
	raw := `{"claude_diagnostic":{"error":"private","response_headers":{"Cookie":["private"]}},"claude_stream":{"usage_source":"upstream"},"keep":9007199254740993}`
	log := Log{Other: raw}
	require.NoError(t, log.AfterFind(nil))
	require.NotContains(t, log.Other, "private")
	require.Contains(t, log.Other, "9007199254740993")
	require.Contains(t, log.Other, "claude_diagnostic_available")
	b, err := common.Marshal(log)
	require.NoError(t, err)
	require.NotContains(t, string(b), "private")
	require.Contains(t, raw, "private")
	log.Other = `{"claude_diagnostic": broken`
	require.NoError(t, log.AfterFind(nil))
	require.Equal(t, "{}", log.Other)
	log.Other = `{"reject_reason":"private-policy","keep":1}`
	require.NoError(t, log.AfterFind(nil))
	require.NotContains(t, log.Other, "private-policy")
	require.Contains(t, log.Other, "claude_diagnostic_available")
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
	row := Log{RequestId: common.NewRequestId(), CreatedAt: common.GetTimestamp(), Other: `{"claude_diagnostic":{"error":"root-only-body"},"stream_status":{"status":"error"}}`}
	prior := Log{RequestId: row.RequestId, CreatedAt: row.CreatedAt, Other: `{"retry":true}`}
	require.NoError(t, db.Create(&prior).Error)
	require.NoError(t, db.Create(&row).Error)
	t.Cleanup(func() { db.Where("request_id = ? AND created_at = ?", row.RequestId, row.CreatedAt).Delete(&Log{}) })
	var read Log
	require.NoError(t, db.Where("id = ? AND request_id = ?", row.Id, row.RequestId).Take(&read).Error)
	require.NotContains(t, read.Other, "root-only-body")
	require.Contains(t, read.Other, "claude_diagnostic_available")
	detail, err := GetClaudeStreamDiagnostic(context.Background(), row.RequestId, row.CreatedAt)
	require.NoError(t, err)
	require.Contains(t, string(detail), "root-only-body")
	_, err = GetClaudeStreamDiagnostic(context.Background(), row.RequestId, row.CreatedAt+1)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	for _, attempt := range []int{1, 2} {
		payload, marshalErr := common.Marshal(map[string]interface{}{"claude_diagnostic": map[string]interface{}{"attempt": attempt, "error": "attempt-private"}})
		require.NoError(t, marshalErr)
		require.NoError(t, db.Create(&Log{RequestId: row.RequestId, CreatedAt: row.CreatedAt, Other: string(payload)}).Error)
	}
	detail, err = GetClaudeStreamDiagnostic(context.Background(), row.RequestId, row.CreatedAt, 2)
	require.NoError(t, err)
	require.Contains(t, string(detail), `"attempt":2`)
	_, err = GetClaudeStreamDiagnostic(context.Background(), row.RequestId, row.CreatedAt, 3)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
