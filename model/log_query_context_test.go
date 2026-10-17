package model

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Prefer the project database. SQLite is only a functional fallback; it cannot
// validate PostgreSQL execution plans, server cancellation or lock contention.
func setupLogQueryContextDB(t *testing.T) {
	t.Helper()
	if LOG_DB != nil {
		return
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}))
	LOG_DB = db
	t.Cleanup(func() {
		LOG_DB = nil
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
}

func TestLogQueryContextCancelledBeforeDatabase(t *testing.T) {
	setupLogQueryContextDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	queries := 0
	name := "test:cancelled_log_query"
	require.NoError(t, LOG_DB.Callback().Query().Before("gorm:query").Register(name, func(*gorm.DB) { queries++ }))
	t.Cleanup(func() { require.NoError(t, LOG_DB.Callback().Query().Remove(name)) })
	_, _, err := GetUserLogsWithContext(ctx, 1, 0, 1, 2, "", "", 0, 10, 0, "", "", "")
	require.ErrorIs(t, err, context.Canceled)
	_, err = GetLogByTokenIdWithContext(ctx, 1)
	require.ErrorIs(t, err, context.Canceled)
	_, _, err = GetAllLogsWithContext(ctx, 0, 1, 2, "", "", "", 0, 10, 0, 0, "", "", "")
	require.ErrorIs(t, err, context.Canceled)
	_, err = SumUsedQuotaWithContext(ctx, 0, 1, 2, "", "", "", 0, "")
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, queries)
}

func TestLogQueryContextCancellationAfterCountStopsPage(t *testing.T) {
	setupLogQueryContextDB(t)
	row := mkLogRow(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queries := 0
	name := "test:cancel_after_log_count"
	require.NoError(t, LOG_DB.Callback().Query().After("gorm:query").Register(name, func(tx *gorm.DB) {
		queries++
		require.NoError(t, tx.Error)
		_, hasDeadline := tx.Statement.Context.Deadline()
		require.True(t, hasDeadline)
		cancel()
	}))
	t.Cleanup(func() { require.NoError(t, LOG_DB.Callback().Query().Remove(name)) })
	_, _, err := GetUserLogsWithContext(ctx, row.UserId, 0, 0, 0, "", "", 0, 10, 0, "", "", "")
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, queries, "cancelled COUNT must not start a page query")
}

func TestLogQueryContextCancellationAfterSumStopsRPM(t *testing.T) {
	setupLogQueryContextDB(t)
	row := mkLogRow(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queries := 0
	name := "test:cancel_after_log_sum"
	require.NoError(t, LOG_DB.Callback().Row().After("gorm:row").Register(name, func(tx *gorm.DB) {
		queries++
		_, hasDeadline := tx.Statement.Context.Deadline()
		require.True(t, hasDeadline)
		cancel()
	}))
	t.Cleanup(func() { require.NoError(t, LOG_DB.Callback().Row().Remove(name)) })
	_, err := SumUsedQuotaWithContext(ctx, 0, 0, 0, "", row.Username, "", 0, "")
	require.Error(t, err)
	require.Equal(t, 1, queries, "cancelled SUM must not start RPM/TPM")
}

func TestLogQueryContextCancelledCacheDoesNotRestartQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := countLogsWithHourCache(ctx, 0, 1, 3600, false, func() *gorm.DB {
		require.Fail(t, "cancelled cache query must not invoke fallback")
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	_, err = SumLogStatRange(ctx, 1, 3600)
	require.ErrorIs(t, err, context.Canceled)
}

func TestLogQueryContextBudget(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ctx, done := logQueryContext(parent)
	defer done()
	want, _ := parent.Deadline()
	got, ok := ctx.Deadline()
	require.True(t, ok)
	require.Equal(t, want, got)
	ctx2, done2 := logQueryContext(context.Background())
	defer done2()
	deadline, ok := ctx2.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(operation_setting.GetLogQuerySetting().GetQueryTimeout()), deadline, time.Second)
}

func TestLogQueryContextCacheCancellationDoesNotFallback(t *testing.T) {
	setupLogQueryContextDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	name := "test:cancel_log_cache_scan"
	require.NoError(t, LOG_DB.Callback().Row().After("gorm:row").Register(name, func(*gorm.DB) { cancel() }))
	t.Cleanup(func() { require.NoError(t, LOG_DB.Callback().Row().Remove(name)) })
	start := logStatTestWindow(t)
	_, err := countLogsWithHourCache(ctx, 0, start, start+3599, false, func() *gorm.DB {
		require.Fail(t, "cache cancellation must not restart a full-range COUNT")
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestLogQueryContextListSharesDeadline(t *testing.T) {
	setupLogQueryContextDB(t)
	row := mkLogRow(t, nil)
	var deadlines []time.Time
	name := "test:log_query_shared_deadline"
	require.NoError(t, LOG_DB.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
		deadline, ok := tx.Statement.Context.Deadline()
		require.True(t, ok)
		deadlines = append(deadlines, deadline)
	}))
	t.Cleanup(func() { require.NoError(t, LOG_DB.Callback().Query().Remove(name)) })
	_, total, err := GetUserLogs(row.UserId, 0, 0, 0, "", "", 0, 10, 0, "", "", "")
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, deadlines, 2)
	require.Equal(t, deadlines[0], deadlines[1])
}

func TestLogQueryContextPreservesResults(t *testing.T) {
	setupLogQueryContextDB(t)
	row := mkLogRow(t, func(l *Log) {
		l.TokenId = nextTestID()
		l.ModelName = "context-test-model"
		l.Quota = 17
		l.PromptTokens = 3
		l.CompletionTokens = 5
	})
	ctx := context.Background()
	logs, total, err := GetUserLogsWithContext(ctx, row.UserId, LogTypeConsume, row.CreatedAt, row.CreatedAt, row.ModelName, "", 0, 10, 0, "", "", "")
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, logs, 1)
	logs, err = GetLogByTokenIdWithContext(ctx, row.TokenId)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	stat, err := SumUsedQuotaWithContext(ctx, 0, row.CreatedAt, row.CreatedAt, "", row.Username, "", 0, "")
	require.NoError(t, err)
	require.Equal(t, Stat{Quota: 17, Rpm: 1, Tpm: 8}, stat)
	_, total, err = GetUserLogsWithContext(ctx, row.UserId, LogTypeError, row.CreatedAt, row.CreatedAt, "", "", 0, 10, 0, "", "", "")
	require.NoError(t, err)
	require.Zero(t, total)
	logs, total, err = GetAllLogsWithContext(ctx, LogTypeConsume, row.CreatedAt, row.CreatedAt, row.ModelName, row.Username, "", 0, 10, 0, 0, "", "", "")
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, logs, 1)
}
