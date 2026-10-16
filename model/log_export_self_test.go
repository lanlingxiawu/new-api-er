package model

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func selfExportFixture(t *testing.T) {
	t.Helper()
	if LOG_DB == nil {
		previous := LOG_DB
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "logs.db")), &gorm.Config{})
		require.NoError(t, err)
		require.NoError(t, db.AutoMigrate(&Log{}))
		LOG_DB = db
		t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close(); LOG_DB = previous })
	}
	previousRedis, previousEnabled := common.RDB, common.RedisEnabled
	server := miniredis.RunT(t)
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RedisEnabled = true
	t.Cleanup(func() { _ = common.RDB.Close(); common.RDB, common.RedisEnabled = previousRedis, previousEnabled })
	fastExportSettings(t, nil)
}

func TestSelfLogExportScopeAndPrivateColumns(t *testing.T) {
	selfExportFixture(t)
	own := mkExportLog(t, func(l *Log) {
		l.Content = "visible"
		l.Other = `{"admin_info":{"stream_error":"private-upstream-error"},"root_info":{"secret":"private-root"}}`
	})
	other := mkExportLog(t, func(l *Log) { l.CreatedAt = own.CreatedAt; l.Content = "other-user" })
	job := newExportJobFor("", own.CreatedAt-1, own.CreatedAt+1, []string{"content", "channel_id", "other_raw"})
	job.UserID, job.Filters.UserId = own.UserId, own.UserId
	job.SelfExport = true
	job.CreatorRole = common.RoleRootUser
	require.NoError(t, runExportJob(t, job))
	rows, _ := readCSVGz(t, job.Parts[0].Path)
	require.Len(t, rows, 2)
	require.Equal(t, []string{"visible"}, rows[1])
	require.NotEqual(t, other.UserId, job.Filters.UserId)

	job.Filters.UserId = other.UserId
	require.ErrorIs(t, runExportJob(t, job), ErrEmployeeExportAccess)
}

func TestExportBatchReleasesConnectionBeforeFileWrite(t *testing.T) {
	selfExportFixture(t)
	row := mkExportLog(t, nil)
	db, err := LOG_DB.DB()
	require.NoError(t, err)
	previous := db.Stats().MaxOpenConnections
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.SetMaxOpenConns(previous) })
	job := newExportJobFor(row.Username, row.CreatedAt-1, row.CreatedAt+1, []string{"created_at"})
	scanner := newLogExportScanner(job, []string{"id", "created_at"}, false, &rowCtx{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	batches := 0
	require.NoError(t, scanner.run(ctx, func(rows []*Log) error {
		if len(rows) == 0 {
			return nil
		}
		batches++
		require.Zero(t, db.Stats().InUse, "file generation must not retain a query connection")
		return db.PingContext(ctx)
	}))
	require.Positive(t, batches)
}

func TestExportTotalTimeoutIncludesResourceWait(t *testing.T) {
	selfExportFixture(t)
	operation_setting.GetLogExportSetting().BatchSleepMs = 60000
	operation_setting.GetLogExportSetting().TimeoutSec = 1
	row := mkExportLog(t, nil)
	job := newExportJobFor(row.Username, row.CreatedAt-1, row.CreatedAt+1, []string{"created_at"})
	require.NoError(t, CreateLogExportJob(job))
	t.Cleanup(func() { RemoveLogExportJobFiles(job); DeleteLogExportJob(job) })
	done := make(chan struct{})
	go func() { runLogExport(job); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		require.FailNow(t, "resource wait must respect the total task timeout")
	}
	require.Equal(t, LogExportStatusFailed, job.Status)
	require.Equal(t, "timeout", job.Error)
	require.Empty(t, job.Parts)
}

func TestSelfExportSQLiteQueryPlan(t *testing.T) {
	selfExportFixture(t)
	if LOG_DB.Dialector.Name() != "sqlite" {
		t.Skip("SQLite fallback plan; other dialects are verified separately")
	}
	var version string
	require.NoError(t, LOG_DB.Raw("SELECT sqlite_version()").Scan(&version).Error)
	var plan []struct{ Detail string }
	require.NoError(t, LOG_DB.Raw("EXPLAIN QUERY PLAN SELECT id, created_at FROM logs WHERE user_id = ? AND created_at >= ? AND created_at <= ? ORDER BY created_at, id LIMIT 1000", 42, 1, 100).Scan(&plan).Error)
	require.NotEmpty(t, plan)
	require.Contains(t, plan[0].Detail, "idx_log_export_user_time")
	t.Logf("SQLite %s: %s", version, plan[0].Detail)
}

func TestSelfLogExportMasksErrorDetails(t *testing.T) {
	selfExportFixture(t)
	previous := operation_setting.GetRelayErrorDisplaySetting()
	t.Cleanup(func() { operation_setting.ReplaceRelayErrorDisplaySetting(previous) })
	row := mkExportLog(t, func(l *Log) {
		l.Type = LogTypeError
		l.Content = "private upstream failure"
		l.Other = `{"admin_info":{"stream_error":"private stream failure"},"error_type":"upstream_error","error_code":"bad_response_status_code","status_code":403}`
	})
	for _, enabled := range []bool{true, false} {
		operation_setting.ReplaceRelayErrorDisplaySetting(operation_setting.RelayErrorDisplaySetting{Enabled: enabled, HideUpstreamErrors: true, DefaultMessage: "Try again later"})
		job := newExportJobFor("", row.CreatedAt-1, row.CreatedAt+1, []string{"content"})
		job.UserID, job.Filters.UserId, job.SelfExport = row.UserId, row.UserId, true
		require.NoError(t, runExportJob(t, job))
		rows, _ := readCSVGz(t, job.Parts[0].Path)
		require.Len(t, rows, 2)
		require.NotContains(t, rows[1][0], "private stream failure")
		if enabled {
			require.Contains(t, rows[1][0], "Try again later")
			require.NotContains(t, rows[1][0], "private upstream failure")
		} else {
			require.Equal(t, "private upstream failure", rows[1][0])
		}
	}
}
