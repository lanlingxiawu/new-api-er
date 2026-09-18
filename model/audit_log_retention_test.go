package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// audit_logs 此前没有任何保留策略：每个带鉴权的后台请求都会写一行，
// 而 log_cleanup 系统任务与 ClickHouse TTL 都只覆盖 logs。
// 这组用例锁住「按 created_at 分批删」的清理原语与保留期换算。

func mkAuditRow(t *testing.T, userID int, createdAt int64) *AuditLog {
	t.Helper()
	requireLogDB(t)
	row := &AuditLog{
		EventId:   common.NewRequestId(),
		UserId:    userID,
		Username:  uniq("au"),
		ActorRole: common.RoleAdminUser,
		CreatedAt: createdAt,
		Category:  AuditCategoryAccessToken,
		Action:    "access_token.use",
		RequestId: common.NewRequestId(),
	}
	require.NoError(t, LOG_DB.Create(row).Error)
	id := row.Id
	t.Cleanup(func() {
		if LOG_DB != nil {
			LOG_DB.Where("id = ?", id).Delete(&AuditLog{})
		}
	})
	return row
}

// auditRetentionEpoch 是一个真实审计记录不可能落在其之前的时间点（2001-09-09）。
// 清理原语只能按 created_at 判定、无法按行归属收窄，所以用例把自己的样本行造在
// 这段无人区里，截止时间也取在这段区间内——共享的开发库里的真实审计记录不受影响
// （Rule 15.5：清理必须按行收窄）。
const auditRetentionEpoch = int64(1_000_000_000)

func TestAuditLogCleanup_CountAndDeleteByAge(t *testing.T) {
	requireLogDB(t)
	uid := nextTestID()
	auditCleanupUser(t, uid)

	cutoff := auditRetentionEpoch + 3600
	mkAuditRow(t, uid, auditRetentionEpoch)      // older than cutoff
	mkAuditRow(t, uid, auditRetentionEpoch+1800) // older than cutoff
	kept := mkAuditRow(t, uid, cutoff+1)

	ctx := context.Background()

	total, err := CountOldAuditLog(ctx, cutoff)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)

	// 分批删除：limit=1 时一次只删一行。PostgreSQL 会静默丢掉 DELETE 的 LIMIT，
	// 所以实现走「先取主键再按主键删」；这条断言就是那条约束的回归保护。
	deleted, err := DeleteOldAuditLogBatch(ctx, cutoff, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	deleted, err = DeleteOldAuditLogBatch(ctx, cutoff, 100)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	// 删空后再调用返回 0，让调用方的进度循环能收敛。
	deleted, err = DeleteOldAuditLogBatch(ctx, cutoff, 100)
	require.NoError(t, err)
	assert.EqualValues(t, 0, deleted)

	// 截止时间之后的行必须原样留下。
	assert.EqualValues(t, 1, countUserAuditLogs(t, uid))
	assert.Equal(t, kept.EventId, firstUserAuditLog(t, uid).EventId)
}

func TestAuditLogCleanup_TargetTimestampFollowsRetention(t *testing.T) {
	setting := operation_setting.GetAuditLogSetting()
	original := setting.RetentionDays
	t.Cleanup(func() { setting.RetentionDays = original })

	// 0 = 永久保留，不清理。
	setting.RetentionDays = 0
	assert.EqualValues(t, 0, AuditLogCleanupTargetTimestamp())

	setting.RetentionDays = 30
	target := AuditLogCleanupTargetTimestamp()
	assert.InDelta(t, float64(common.GetTimestamp()-30*24*60*60), float64(target), 5)

	// 低于下限的配置被夹紧，不会把仍在排障窗口内的记录删掉。
	setting.RetentionDays = 1
	target = AuditLogCleanupTargetTimestamp()
	expected := common.GetTimestamp() - int64(operation_setting.MinAuditLogRetentionDays)*24*60*60
	assert.InDelta(t, float64(expected), float64(target), 5)
}
