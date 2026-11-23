package service

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func auditCleanupWorkflowDB(t *testing.T) {
	t.Helper()
	cleanupWorkflowDB(t)
	if dsn := os.Getenv("CLEANUP_WORKFLOW_LOG_DSN"); dsn != "" {
		isolated, cleanup, err := isolatedServiceTestDSN(dsn, "audit_cleanup_log")
		require.NoError(t, err)
		t.Cleanup(cleanup)
		t.Setenv("LOG_SQL_DSN", isolated)
		db, err := model.OpenAuditLogCleanupDB(t.Context())
		require.NoError(t, err)
		pool, err := db.DB()
		require.NoError(t, err)
		model.LOG_DB = db
		t.Cleanup(func() { require.NoError(t, pool.Close()) })
	}
	require.NoError(t, model.LOG_DB.AutoMigrate(&model.AuditLog{}, &model.Log{}))
	require.NoError(t, model.DB.AutoMigrate(&model.SecurityAlert{}))
	original := operation_setting.GetAuditLogSetting().RetentionDays
	operation_setting.GetAuditLogSetting().RetentionDays = 0
	t.Cleanup(func() { operation_setting.GetAuditLogSetting().RetentionDays = original })
}

func executeLogCleanup(t *testing.T, ctx context.Context, task *model.SystemTask) *model.SystemTask {
	t.Helper()
	claimed, ok, err := model.ClaimSystemTask(task.ID, task.Type, "audit-cleanup-test", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, ok)
	runLogCleanupTask(ctx, claimed, "audit-cleanup-test")
	finished, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	return finished
}

func TestAuditLogCleanupWorkflow(t *testing.T) {
	auditCleanupWorkflowDB(t)
	cutoff := common.GetTimestamp() - 3600
	rows := make([]model.AuditLog, 103)
	for i := range rows {
		rows[i] = model.AuditLog{EventId: common.NewRequestId(), CreatedAt: cutoff - 1, ActorRole: common.RoleRootUser}
	}
	rows[101].CreatedAt = cutoff
	rows[102].CreatedAt = cutoff + 1
	require.NoError(t, model.LOG_DB.Create(&rows).Error)
	usage := model.Log{CreatedAt: cutoff - 1}
	require.NoError(t, model.LOG_DB.Create(&usage).Error)
	alert := model.SecurityAlert{FirstSeen: cutoff - 1, LastSeen: cutoff - 1}
	require.NoError(t, model.DB.Create(&alert).Error)

	task, err := StartLogCleanupTask(cutoff, LogCleanupScopeAudit)
	require.NoError(t, err)
	var payload LogCleanupPayload
	require.NoError(t, task.DecodePayload(&payload))
	require.Zero(t, payload.TargetTimestamp, "old runners must reject this task before touching usage logs")
	require.Equal(t, cutoff, payload.AuditTargetTimestamp)
	finished := executeLogCleanup(t, t.Context(), task)
	require.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)
	var result LogCleanupResult
	require.NoError(t, common.UnmarshalJsonStr(finished.Result, &result))
	require.EqualValues(t, 101, result.DeletedCount)
	require.Zero(t, result.AuditDeletedCount)
	var state LogCleanupState
	require.NoError(t, finished.DecodeState(&state))
	require.Equal(t, 100, state.Progress)
	require.Zero(t, state.Remaining)
	var remaining []model.AuditLog
	require.NoError(t, model.LOG_DB.Order("created_at").Find(&remaining).Error)
	require.Len(t, remaining, 2)
	require.Equal(t, cutoff, remaining[0].CreatedAt)
	require.NoError(t, model.LOG_DB.First(&model.Log{}, usage.Id).Error)
	require.NoError(t, model.DB.First(&model.SecurityAlert{}, alert.ID).Error)
	pool, err := model.LOG_DB.DB()
	require.NoError(t, err)
	require.Zero(t, pool.Stats().InUse)

	// Running the same cutoff again is a successful no-op, even with retention disabled.
	task, err = StartLogCleanupTask(cutoff, LogCleanupScopeAudit)
	require.NoError(t, err)
	finished = executeLogCleanup(t, t.Context(), task)
	require.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)
	require.NoError(t, common.UnmarshalJsonStr(finished.Result, &result))
	require.Zero(t, result.DeletedCount)
}

func TestAuditLogCleanupScopeConflicts(t *testing.T) {
	for _, scope := range []string{"", LogCleanupScopeAudit} {
		t.Run(scope, func(t *testing.T) {
			auditCleanupWorkflowDB(t)
			cutoff := common.GetTimestamp() - 3600
			task, err := StartLogCleanupTask(cutoff, scope)
			require.NoError(t, err)
			repeated, err := StartLogCleanupTask(cutoff, scope)
			require.NoError(t, err)
			require.Equal(t, task.TaskID, repeated.TaskID)
			other := LogCleanupScopeAudit
			if scope == LogCleanupScopeAudit {
				other = LogCleanupScopeUsage
				_, err = StartLogCleanupTask(cutoff-1, scope)
				require.ErrorIs(t, err, ErrCleanupBusy)
			}
			_, err = StartLogCleanupTask(cutoff, other)
			require.ErrorIs(t, err, ErrCleanupBusy)
		})
	}
}

func TestAuditLogCleanupCancellationAndInvalidPayload(t *testing.T) {
	for _, scenario := range []string{"cancelled", "expired", "unsafe-legacy-timestamp"} {
		t.Run(scenario, func(t *testing.T) {
			auditCleanupWorkflowDB(t)
			cutoff := common.GetTimestamp() - 3600
			row := model.AuditLog{EventId: common.NewRequestId(), CreatedAt: cutoff - 1}
			require.NoError(t, model.LOG_DB.Create(&row).Error)
			payload := LogCleanupPayload{Scope: LogCleanupScopeAudit, AuditTargetTimestamp: cutoff}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "unsafe-legacy-timestamp" {
				payload.TargetTimestamp = cutoff
			} else if scenario == "expired" {
				var expire context.CancelFunc
				ctx, expire = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer expire()
			} else {
				cancel()
			}
			task, err := model.CreateSystemTask(model.SystemTaskTypeLogCleanup, payload, LogCleanupState{})
			require.NoError(t, err)
			finished := executeLogCleanup(t, ctx, task)
			require.Equal(t, model.SystemTaskStatusFailed, finished.Status)
			require.Equal(t, "cleanup.failed", finished.Error)
			require.NoError(t, model.LOG_DB.First(&model.AuditLog{}, row.Id).Error)
		})
	}
}

func TestAuditLogCleanupValidation(t *testing.T) {
	for _, invalid := range []int64{0, -1, time.Now().Add(time.Hour).Unix()} {
		_, err := StartLogCleanupTask(invalid, LogCleanupScopeAudit)
		require.ErrorIs(t, err, ErrCleanupLogDate)
	}
	_, err := StartLogCleanupTask(1234, "unknown")
	require.ErrorIs(t, err, ErrCleanupScope)
	original := operation_setting.GetDataCleanupSetting().Enabled
	require.NoError(t, config.GlobalConfig.UpdateFromMap("data_cleanup_setting", map[string]string{"enabled": "false"}))
	t.Cleanup(func() {
		value := "false"
		if original {
			value = "true"
		}
		require.NoError(t, config.GlobalConfig.UpdateFromMap("data_cleanup_setting", map[string]string{"enabled": value}))
	})
	_, err = StartLogCleanupTask(1234, LogCleanupScopeAudit)
	require.ErrorIs(t, err, ErrCleanupDisabled)
}

func TestLogCleanupLegacyPayload(t *testing.T) {
	auditCleanupWorkflowDB(t)
	cutoff := common.GetTimestamp() - 3600
	usage := model.Log{CreatedAt: cutoff - 1}
	audit := model.AuditLog{EventId: common.NewRequestId(), CreatedAt: cutoff - 1}
	require.NoError(t, model.LOG_DB.Create(&usage).Error)
	require.NoError(t, model.LOG_DB.Create(&audit).Error)
	task, err := model.CreateSystemTask(model.SystemTaskTypeLogCleanup, LogCleanupPayload{TargetTimestamp: cutoff, BatchSize: 1}, LogCleanupState{})
	require.NoError(t, err)
	finished := executeLogCleanup(t, t.Context(), task)
	require.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)
	var count int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("id = ?", usage.Id).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, model.LOG_DB.First(&model.AuditLog{}, audit.Id).Error)
}

func TestAuditLogCleanupIndexPlan(t *testing.T) {
	auditCleanupWorkflowDB(t)
	cutoff := common.GetTimestamp() - 3600
	rows := make([]model.AuditLog, 10010)
	for i := range rows {
		rows[i] = model.AuditLog{EventId: common.NewRequestId(), CreatedAt: cutoff + 1}
	}
	for i := range 10 {
		rows[i].CreatedAt = cutoff - 1
	}
	require.NoError(t, model.LOG_DB.CreateInBatches(&rows, 200).Error)
	query := "SELECT id FROM audit_logs WHERE created_at < ? ORDER BY created_at, event_id LIMIT 100"
	var plan string
	switch model.LOG_DB.Dialector.Name() {
	case "postgres":
		require.NoError(t, model.LOG_DB.Exec("ANALYZE audit_logs").Error)
		require.NoError(t, model.LOG_DB.Raw("EXPLAIN (FORMAT JSON) "+query, cutoff).Scan(&plan).Error)
	case "mysql":
		require.NoError(t, model.LOG_DB.Raw("EXPLAIN FORMAT=JSON "+query, cutoff).Scan(&plan).Error)
	case "sqlite":
		var details []struct{ Detail string }
		require.NoError(t, model.LOG_DB.Raw("EXPLAIN QUERY PLAN "+query, cutoff).Scan(&details).Error)
		for _, detail := range details {
			plan += detail.Detail
		}
	default:
		t.Fatal("unexpected relational dialect")
	}
	plan = strings.ToLower(plan)
	// PostgreSQL may prefer the smaller created_at index plus a bounded sort
	// for a selective cutoff. Both existing indexes satisfy the time predicate.
	require.True(t, strings.Contains(plan, "idx_audit_time_event") || strings.Contains(plan, "idx_audit_logs_created_at"), plan)
	require.NotContains(t, plan, "seq scan")
	t.Logf("cleanup candidate index verified: %s: %s", model.LOG_DB.Dialector.Name(), plan)
}

func TestManualAuditCleanupClickHouse(t *testing.T) {
	dsn := os.Getenv("AUDIT_TEST_CH_DSN")
	if dsn == "" {
		t.Skip("isolated ClickHouse required")
	}
	require.Contains(t, dsn, "/audit_test")
	cleanupWorkflowDB(t)
	t.Setenv("LOG_SQL_DSN", dsn)
	admin, err := model.OpenAuditLogCleanupDB(t.Context())
	require.NoError(t, err)
	pool, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	database := "audit_test_manual_" + fmt.Sprint(time.Now().UnixNano())
	require.NoError(t, admin.Exec("CREATE DATABASE "+database).Error)
	t.Cleanup(func() { require.NoError(t, admin.Exec("DROP DATABASE "+database).Error) })
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.Path = "/" + database
	t.Setenv("LOG_SQL_DSN", u.String())
	db, err := model.OpenAuditLogCleanupDB(t.Context())
	require.NoError(t, err)
	logPool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, logPool.Close()) })
	model.LOG_DB = db
	originalKind := common.LogDatabaseType()
	common.SetLogDatabaseType(common.DatabaseTypeClickHouse)
	t.Cleanup(func() { common.SetLogDatabaseType(originalKind) })
	require.NoError(t, model.MigrateAuditLogs())
	cutoff := common.GetTimestamp() - 3600
	require.NoError(t, db.Exec("INSERT INTO audit_logs (event_id, created_at) VALUES ('old', ?), ('boundary', ?), ('new', ?)", cutoff-1, cutoff, cutoff+1).Error)
	task, err := StartLogCleanupTask(cutoff, LogCleanupScopeAudit)
	require.NoError(t, err)
	finished := executeLogCleanup(t, t.Context(), task)
	require.Equal(t, model.SystemTaskStatusSucceeded, finished.Status)
	var result LogCleanupResult
	require.NoError(t, common.UnmarshalJsonStr(finished.Result, &result))
	require.EqualValues(t, 1, result.DeletedCount)
	var remaining []string
	require.NoError(t, db.Model(&model.AuditLog{}).Order("created_at").Pluck("event_id", &remaining).Error)
	require.Equal(t, []string{"boundary", "new"}, remaining)
}
