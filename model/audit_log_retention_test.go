package model

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
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

	total, err := CountOldAuditLog(ctx, LOG_DB, cutoff)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)

	// 分批删除：limit=1 时一次只删一行。PostgreSQL 会静默丢掉 DELETE 的 LIMIT，
	// 所以实现走「先取主键再按主键删」；这条断言就是那条约束的回归保护。
	deleted, err := DeleteOldAuditLogBatch(ctx, LOG_DB, cutoff, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	deleted, err = DeleteOldAuditLogBatch(ctx, LOG_DB, cutoff, 100)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	// 删空后再调用返回 0，让调用方的进度循环能收敛。
	deleted, err = DeleteOldAuditLogBatch(ctx, LOG_DB, cutoff, 100)
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

func TestAuditRetentionMigrationClickHouse(t *testing.T) {
	dsn := os.Getenv("AUDIT_TEST_CH_DSN")
	if dsn == "" {
		t.Skip("isolated ClickHouse required")
	}
	require.Contains(t, dsn, "/audit_test")
	t.Setenv("LOG_SQL_DSN", dsn)
	admin, err := OpenAuditSecurityDB(t.Context(), true)
	require.NoError(t, err)
	pool, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	originalDays := operation_setting.GetAuditLogSetting().RetentionDays
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("audit_log_setting", map[string]string{"retention_days": fmt.Sprint(originalDays)}))
	})
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprint("upgrade=", upgrade), func(t *testing.T) {
			database := "audit_test_retention_" + fmt.Sprint(time.Now().UnixNano())
			require.NoError(t, admin.Exec("CREATE DATABASE "+database).Error)
			t.Cleanup(func() { require.NoError(t, admin.Exec("DROP DATABASE "+database).Error) })
			u, err := url.Parse(dsn)
			require.NoError(t, err)
			u.Path = "/" + database
			t.Setenv("LOG_SQL_DSN", u.String())
			db, err := OpenAuditSecurityDB(t.Context(), true)
			require.NoError(t, err)
			p, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, p.Close()) })
			oldDB, oldKind := LOG_DB, common.LogDatabaseType()
			LOG_DB = db
			common.SetLogDatabaseType(common.DatabaseTypeClickHouse)
			t.Cleanup(func() { LOG_DB = oldDB; common.SetLogDatabaseType(oldKind) })
			require.NoError(t, config.GlobalConfig.UpdateFromMap("audit_log_setting", map[string]string{"retention_days": "0"}))
			if upgrade {
				require.NoError(t, db.Exec(`CREATE TABLE audit_logs (
					id Int64 DEFAULT 0, event_id String, user_id Int64, username String, actor_role Int32,
					created_at Int64, category String, action String, token_ref String,
					auth_method String, ip String, user_agent String, method String, route String,
					status Int32, success UInt8, request_id String, content String, other JSON
				) ENGINE = MergeTree() PARTITION BY toYYYYMM(toDateTime(created_at))
				ORDER BY (created_at, event_id) TTL toDateTime(created_at) + INTERVAL 180 DAY`).Error)
				require.NoError(t, db.Exec("SYSTEM STOP MERGES audit_logs").Error)
			}
			now := common.GetTimestamp()
			if upgrade {
				require.NoError(t, db.Exec("INSERT INTO audit_logs (event_id, created_at) VALUES ('legacy', ?)", now-181*86400).Error)
			}
			require.NoError(t, MigrateAuditLogs())
			var ddl string
			require.NoError(t, db.Raw("SHOW CREATE TABLE audit_logs").Scan(&ddl).Error)
			require.NotContains(t, ddl, "TTL ", "native TTL must not override configured retention")
			rec := &creatorMigrationLog{Interface: gormlogger.Default}
			LOG_DB = db.Session(&gorm.Session{Logger: rec})
			require.NoError(t, MigrateAuditLogs())
			require.Empty(t, rec.ddl, "second initialization must issue no DDL")
			LOG_DB = db
			require.NoError(t, db.Exec("INSERT INTO audit_logs (event_id, created_at) VALUES ('old', ?), ('recent', ?)", now-181*86400, now).Error)
			require.NoError(t, db.Exec("SYSTEM START MERGES audit_logs").Error)
			require.NoError(t, db.Exec("OPTIMIZE TABLE audit_logs FINAL").Error)
			var count int64
			require.NoError(t, db.Raw("SELECT count() FROM audit_logs").Scan(&count).Error)
			want := int64(2)
			if upgrade {
				want++
			}
			require.Equal(t, want, count)
			require.Zero(t, AuditLogCleanupTargetTimestamp())
			require.NoError(t, config.GlobalConfig.UpdateFromMap("audit_log_setting", map[string]string{"retention_days": "365"}))
			old, err := CountOldAuditLog(context.Background(), LOG_DB, AuditLogCleanupTargetTimestamp())
			require.NoError(t, err)
			require.Zero(t, old)
			require.NoError(t, config.GlobalConfig.UpdateFromMap("audit_log_setting", map[string]string{"retention_days": "30"}))
			removed, err := DeleteOldAuditLogBatch(context.Background(), LOG_DB, AuditLogCleanupTargetTimestamp(), 100)
			require.NoError(t, err)
			require.Equal(t, want-1, removed)
			require.NoError(t, db.Raw("SELECT count() FROM audit_logs WHERE event_id = 'recent'").Scan(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}
