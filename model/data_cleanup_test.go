package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// A private database is essential: cleanup tests must never delete a developer's
// historical records. External dialects can be selected with CLEANUP_TEST_DSN.
func cleanupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	var dialect gorm.Dialector = sqlite.Open(fmt.Sprintf("file:cleanup_%d?mode=memory&cache=shared", time.Now().UnixNano()))
	prefix := ""
	if dsn := os.Getenv("CLEANUP_TEST_DSN"); dsn != "" {
		// Leave room for long model names within MySQL's 64-character limit.
		prefix = fmt.Sprintf("cl_%x_", time.Now().UnixNano())
		if strings.HasPrefix(dsn, "postgres") {
			admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
			require.NoError(t, err)
			// PostgreSQL index names are schema-wide, so table prefixes alone
			// cannot isolate the production model's explicitly named indexes.
			schemaName := prefix + "data"
			require.NoError(t, admin.Exec(`CREATE SCHEMA "`+schemaName+`"`).Error)
			t.Cleanup(func() {
				require.NoError(t, admin.Exec(`DROP SCHEMA "`+schemaName+`" CASCADE`).Error)
				pool, e := admin.DB()
				require.NoError(t, e)
				require.NoError(t, pool.Close())
			})
			parsed, err := url.Parse(dsn)
			require.NoError(t, err)
			query := parsed.Query()
			query.Set("search_path", schemaName)
			parsed.RawQuery = query.Encode()
			dialect = postgres.Open(parsed.String())
			prefix = ""
		} else {
			dialect = mysql.Open(dsn)
		}
	}
	db, err := gorm.Open(dialect, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: prefix}})
	require.NoError(t, err)
	var version string
	versionQuery := "SELECT VERSION()"
	if db.Dialector.Name() == "sqlite" {
		versionQuery = "SELECT sqlite_version()"
	}
	require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
	t.Logf("database: %s %s", db.Dialector.Name(), version)
	require.NoError(t, db.AutoMigrate(&ConsumptionCost{}, &EmployeeCommissionLog{}, &Log{}, &FinancialDetailTombstone{}, &DataCleanupRun{}, &SystemTask{}, &SystemTaskLock{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		if prefix != "" {
			require.NoError(t, db.Migrator().DropTable(&ConsumptionCost{}, &EmployeeCommissionLog{}, &Log{}, &FinancialDetailTombstone{}, &DataCleanupRun{}, &SystemTask{}, &SystemTaskLock{}, &PlatformChannelDailyStat{}, &EmployeeCommissionDailyStat{}, &EmployeeCustomerCommissionDailyStat{}))
		}
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func TestErrorLogCleanupExistingIndexes(t *testing.T) {
	for _, mode := range []string{"error_time", "time", "id"} {
		t.Run(mode, func(t *testing.T) {
			db := cleanupTestDB(t)
			if mode != "error_time" {
				require.NoError(t, db.Migrator().DropIndex(&Log{}, "idx_cleanup_created_id"))
			}
			if mode == "id" {
				require.NoError(t, db.Migrator().DropIndex(&Log{}, "idx_created_at_id"))
			}
			logs := []Log{
				{Id: 1, CreatedAt: 10, Type: LogTypeConsume},
				{Id: 2, CreatedAt: 10, Type: LogTypeError},
				{Id: 3, CreatedAt: 10, Type: LogTypeManage},
				{Id: 4, CreatedAt: 0, Type: LogTypeError},
				{Id: 5, CreatedAt: -1, Type: LogTypeError},
				{Id: 6, CreatedAt: 100, Type: LogTypeError},
				{Id: 7, CreatedAt: 101, Type: LogTypeError},
			}
			require.NoError(t, db.Create(&logs).Error)
			require.NoError(t, db.Model(&Log{}).Where("id = ?", 4).Update("created_at", 0).Error)
			run, err := PrepareErrorLogCleanup(context.Background(), db, 100)
			require.NoError(t, err)
			require.Equal(t, mode, run.ScanMode)
			require.Equal(t, 2, run.Matched)
			require.True(t, run.CountExact)
			require.Zero(t, run.Scanned, "preview must not advance the execution cursor")
			require.NoError(t, db.Create(&Log{Id: 1000, CreatedAt: 9, Type: LogTypeError}).Error)
			for i := 0; ; i++ {
				require.Less(t, i, 20, "scan must exhaust its snapshot")
				batch, err := DeleteDataCleanupBatch(context.Background(), db, run, 1)
				require.NoError(t, err)
				if mode == "id" {
					require.LessOrEqual(t, batch.Scanned, 10)
				} else {
					require.LessOrEqual(t, batch.Scanned, 1)
				}
				require.LessOrEqual(t, batch.Deleted, int64(1))
				if batch.Done {
					break
				}
			}
			require.EqualValues(t, 2, run.Deleted)
			var remaining []int
			require.NoError(t, db.Model(&Log{}).Order("id").Pluck("id", &remaining).Error)
			require.Equal(t, []int{1, 3, 5, 6, 7, 1000}, remaining)
			for _, limit := range []int{0, 10001} {
				_, err := DeleteDataCleanupBatch(context.Background(), db, run, limit)
				require.Error(t, err)
			}
		})
	}
}

func TestErrorLogCleanupBoundedIDWindows(t *testing.T) {
	db := cleanupTestDB(t)
	logs := []Log{{Id: 131075, Type: LogTypeError, CreatedAt: 10}, {Id: 131076, Type: LogTypeConsume, CreatedAt: 10}, {Id: 131077, Type: LogTypeError, CreatedAt: 10}}
	require.NoError(t, db.Create(&logs).Error)
	run := DataCleanupRun{Kind: CleanupErrorLogs, Before: 20, MaxID: 131076, ErrorLogCleanupPlan: ErrorLogCleanupPlan{ScanMode: "id", ScanEndID: 131076}}
	for _, end := range []int{65536, 131072} {
		batch, err := DeleteDataCleanupBatch(context.Background(), db, &run, 100)
		require.NoError(t, err)
		require.False(t, batch.Done, "an empty window must not finish the snapshot")
		require.Zero(t, batch.Scanned)
		require.Zero(t, batch.Deleted)
		require.Equal(t, end, run.CursorID)
	}
	batch, err := DeleteDataCleanupBatch(context.Background(), db, &run, 100)
	require.NoError(t, err)
	require.True(t, batch.Done)
	require.Equal(t, 2, batch.Scanned)
	require.EqualValues(t, 1, batch.Deleted)
	require.Equal(t, run.MaxID, run.CursorID)
	require.EqualValues(t, 2, run.Scanned, "ID holes are not scanned records")
	var remaining []int
	require.NoError(t, db.Model(&Log{}).Order("id").Pluck("id", &remaining).Error)
	require.Equal(t, []int{131076, 131077}, remaining)
}

func TestErrorLogCleanupPrefetchedTailAndBounds(t *testing.T) {
	db := cleanupTestDB(t)
	logs := []Log{
		{Id: 1, Type: LogTypeConsume, CreatedAt: 10},
		{Id: 2, Type: LogTypeError, CreatedAt: 10},
		{Id: 3, Type: LogTypeError, CreatedAt: 9},
		{Id: 4, Type: LogTypeError, CreatedAt: 10},
		{Id: 5, Type: LogTypeManage, CreatedAt: 10},
		{Id: 6, Type: LogTypeError, CreatedAt: 8},
		{Id: 7, Type: LogTypeError, CreatedAt: 10},
		{Id: 8, Type: LogTypeError, CreatedAt: 20},
		{Id: 9, Type: LogTypeError, CreatedAt: -1},
		{Id: 10, Type: LogTypeConsume, CreatedAt: 21},
		{Id: 11, Type: LogTypeError, CreatedAt: 1},
	}
	require.NoError(t, db.Create(&logs).Error)
	run := DataCleanupRun{Kind: CleanupErrorLogs, Before: 20, MaxID: 10, ErrorLogCleanupPlan: ErrorLogCleanupPlan{ScanMode: "id", ScanEndID: 10}}
	for _, end := range []int{3, 6} {
		batch, err := DeleteDataCleanupBatch(context.Background(), db, &run, 2)
		require.NoError(t, err)
		require.EqualValues(t, 2, batch.Deleted)
		require.Equal(t, 3, batch.Scanned)
		require.False(t, batch.Done)
		require.Equal(t, end, run.CursorID, "unprocessed prefetched errors must remain ahead of the cursor")
	}
	batch, err := DeleteDataCleanupBatch(context.Background(), db, &run, 2)
	require.NoError(t, err)
	require.True(t, batch.Done)
	require.EqualValues(t, 5, run.Deleted)
	require.EqualValues(t, 10, run.Scanned)
	var ids []int
	require.NoError(t, db.Model(&Log{}).Order("id").Pluck("id", &ids).Error)
	require.Equal(t, []int{1, 5, 8, 9, 10, 11}, ids)
	// Even an empty range near the integer limit must advance without overflow.
	run.MaxID = int(^uint(0) >> 1)
	run.CursorID = run.MaxID - 5
	batch, err = DeleteDataCleanupBatch(context.Background(), db, &run, 500)
	require.NoError(t, err)
	require.True(t, batch.Done)
	require.Equal(t, run.MaxID, run.CursorID)
}

func TestErrorLogCleanupBoundedPreview(t *testing.T) {
	db := cleanupTestDB(t)
	for _, index := range []string{"idx_cleanup_created_id", "idx_created_at_id"} {
		require.NoError(t, db.Migrator().DropIndex(&Log{}, index))
	}
	require.NoError(t, db.Create(&Log{Id: 65537, Type: LogTypeError, CreatedAt: 10}).Error)
	run, err := PrepareErrorLogCleanup(context.Background(), db, 20)
	require.NoError(t, err)
	require.Equal(t, "id", run.ScanMode)
	require.Zero(t, run.Matched)
	require.False(t, run.CountExact, "an empty first window cannot prove there are no eligible logs")
	require.Zero(t, run.CursorID)
	batch, err := DeleteDataCleanupBatch(context.Background(), db, run, 100)
	require.NoError(t, err)
	require.False(t, batch.Done)
	batch, err = DeleteDataCleanupBatch(context.Background(), db, run, 100)
	require.NoError(t, err)
	require.True(t, batch.Done)
	require.EqualValues(t, 1, run.Deleted)
}

func TestErrorLogCleanupInvalidBatchOptions(t *testing.T) {
	db := cleanupTestDB(t)
	for _, options := range []ErrorLogCleanupBatchOptions{
		{DeleteLimit: 0, IDSpan: 65536}, {DeleteLimit: 10001, IDSpan: 65536},
		{DeleteLimit: 1, IDSpan: 0}, {DeleteLimit: 1, IDSpan: 65537},
	} {
		run := DataCleanupRun{Kind: CleanupErrorLogs, Before: 20, MaxID: 100, ErrorLogCleanupPlan: ErrorLogCleanupPlan{ScanMode: "id"}}
		original := run
		_, err := DeleteErrorLogCleanupBatch(context.Background(), db, &run, options)
		require.Error(t, err)
		require.Equal(t, original, run)
	}
	for _, cursor := range []int{-1, 101} {
		run := DataCleanupRun{Kind: CleanupErrorLogs, Before: 20, MaxID: 100, CursorID: cursor, ErrorLogCleanupPlan: ErrorLogCleanupPlan{ScanMode: "id"}}
		_, err := DeleteErrorLogCleanupBatch(context.Background(), db, &run, DefaultErrorLogCleanupBatchOptions(100))
		require.Error(t, err)
		require.Equal(t, cursor, run.CursorID)
	}
}

func TestErrorLogCleanupSeekExistingRecords(t *testing.T) {
	db := cleanupTestDB(t)
	logs := []Log{{Id: 1000000001, Type: LogTypeConsume, CreatedAt: 30}, {Id: 1000000010, Type: LogTypeError, CreatedAt: 10}}
	require.NoError(t, db.Create(&logs).Error)
	run := DataCleanupRun{Kind: CleanupErrorLogs, Before: 20, MaxID: 1000000010, ErrorLogCleanupPlan: ErrorLogCleanupPlan{ScanMode: "id"}}
	cursor, err := SeekErrorLogCleanupCursor(context.Background(), db, &run)
	require.NoError(t, err)
	require.Equal(t, 1000000000, cursor, "seek must include consumption and recent records")
	require.Zero(t, run.CursorID, "the caller must persist a seek before using it")
	run.CursorID = cursor
	batch, err := DeleteErrorLogCleanupBatch(context.Background(), db, &run, DefaultErrorLogCleanupBatchOptions(2000))
	require.NoError(t, err)
	require.True(t, batch.Done)
	require.EqualValues(t, 1, batch.Deleted)
	require.Equal(t, 2, batch.Scanned)
	cursor, err = SeekErrorLogCleanupCursor(context.Background(), db, &run)
	require.NoError(t, err)
	require.Equal(t, run.MaxID, cursor)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = SeekErrorLogCleanupCursor(ctx, db, &run)
	require.ErrorIs(t, err, context.Canceled)
}

func TestErrorLogCleanupLargeBatchAtomicity(t *testing.T) {
	for _, count := range []int{400, 401, 2000, 5000, 5001, 10000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			db := cleanupTestDB(t)
			logs := make([]Log, count+3)
			for i := range logs {
				logs[i] = Log{Id: i + 1, Type: LogTypeError, CreatedAt: 10}
			}
			logs[count].Type = LogTypeConsume
			logs[count+1].CreatedAt = 20
			require.NoError(t, db.CreateInBatches(logs, 200).Error)
			run := DataCleanupRun{Kind: CleanupErrorLogs, Before: 20, MaxID: count + 2, ErrorLogCleanupPlan: ErrorLogCleanupPlan{ScanMode: "id"}}
			original := run
			calls := 0
			injected := errors.New("second delete failed")
			require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("test:second_delete", func(tx *gorm.DB) {
				calls++
				if calls == 2 {
					tx.AddError(injected)
				}
			}))
			_, err := DeleteDataCleanupBatch(context.Background(), db, &run, count)
			if calls > 1 {
				require.ErrorIs(t, err, injected)
				require.Equal(t, original, run)
				var remaining int64
				require.NoError(t, db.Model(&Log{}).Count(&remaining).Error)
				require.EqualValues(t, count+3, remaining, "earlier DELETE statements must roll back")
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, db.Callback().Delete().Remove("test:second_delete"))
			if calls > 1 {
				_, err = DeleteDataCleanupBatch(context.Background(), db, &run, count)
				require.NoError(t, err)
			}
			require.EqualValues(t, count, run.Deleted)
			var ids []int
			require.NoError(t, db.Model(&Log{}).Order("id").Pluck("id", &ids).Error)
			require.Equal(t, []int{count + 1, count + 2, count + 3}, ids)
		})
	}
}

func TestErrorLogCleanupNullTimeIsNotEpoch(t *testing.T) {
	db := cleanupTestDB(t)
	for _, index := range []string{"idx_cleanup_created_id", "idx_created_at_id"} {
		require.NoError(t, db.Migrator().DropIndex(&Log{}, index))
	}
	logs := []Log{{Id: 1, Type: LogTypeError, CreatedAt: 10}, {Id: 2, Type: LogTypeError, CreatedAt: 10}, {Id: 3, Type: LogTypeConsume, CreatedAt: 10}}
	require.NoError(t, db.Create(&logs).Error)
	require.NoError(t, db.Model(&Log{}).Where("id = ?", 1).Update("created_at", nil).Error)
	require.NoError(t, db.Model(&Log{}).Where("id = ?", 2).Update("created_at", 0).Error)
	run, err := PrepareErrorLogCleanup(context.Background(), db, 20)
	require.NoError(t, err)
	require.Equal(t, "id", run.ScanMode)
	require.True(t, run.CountExact)
	require.Equal(t, 1, run.Matched, "NULL is protected, but Unix epoch zero is eligible")
	batch, err := DeleteDataCleanupBatch(context.Background(), db, run, 100)
	require.NoError(t, err)
	require.True(t, batch.Done)
	require.EqualValues(t, 1, batch.Deleted)
	var ids []int
	require.NoError(t, db.Model(&Log{}).Order("id").Pluck("id", &ids).Error)
	require.Equal(t, []int{1, 3}, ids)
}

func TestErrorLogCleanupBoundedIDQueryPlans(t *testing.T) {
	db := cleanupTestDB(t)
	logs := make([]Log, 12000)
	for i := range logs {
		logs[i] = Log{Id: i + 1, CreatedAt: int64(i/100 + 1), Type: LogTypeError}
	}
	require.NoError(t, db.CreateInBatches(logs, 100).Error)
	prefix := "EXPLAIN "
	if db.Dialector.Name() == "sqlite" {
		prefix = "EXPLAIN QUERY PLAN "
	} else if db.Dialector.Name() == "mysql" {
		// Recent MySQL versions default to TREE, whose DELETE output can be
		// an iterator-executor placeholder instead of an inspectable plan.
		prefix = "EXPLAIN FORMAT=TRADITIONAL "
	}
	var queries []struct {
		sql  string
		args []any
	}
	capture := func(tx *gorm.DB) {
		queries = append(queries, struct {
			sql  string
			args []any
		}{tx.Statement.SQL.String(), append([]any(nil), tx.Statement.Vars...)})
	}
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:cleanup_plan_query", capture))
	require.NoError(t, db.Callback().Delete().After("gorm:delete").Register("test:cleanup_plan_delete", capture))
	run := DataCleanupRun{Kind: CleanupErrorLogs, Before: 1000, MaxID: 8000, CursorID: 6000, ErrorLogCleanupPlan: ErrorLogCleanupPlan{ScanMode: "id"}}
	rollback := errors.New("rollback cleanup plan sample")
	err := db.Transaction(func(tx *gorm.DB) error {
		cursor, err := SeekErrorLogCleanupCursor(context.Background(), tx, &run)
		if err != nil {
			return err
		}
		require.Equal(t, run.CursorID, cursor)
		batch, err := DeleteDataCleanupBatch(context.Background(), tx, &run, 100)
		if err != nil {
			return err
		}
		require.EqualValues(t, 100, batch.Deleted)
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	require.NoError(t, db.Callback().Query().Remove("test:cleanup_plan_query"))
	require.NoError(t, db.Callback().Delete().Remove("test:cleanup_plan_delete"))
	require.Len(t, queries, 3, "inspect the seek, bounded scan and DELETE actually issued by the cleanup model")
	for _, query := range queries {
		var plan []map[string]any
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			if err := configureCleanupIndexScan(tx); err != nil {
				return err
			}
			return tx.Raw(prefix+query.sql, query.args...).Scan(&plan).Error
		}))
		text := strings.ToLower(fmt.Sprint(plan))
		t.Logf("bounded cleanup plan: %s", text)
		if db.Dialector.Name() == "mysql" {
			require.NotEmpty(t, plan)
			for _, row := range plan {
				require.Equal(t, "PRIMARY", fmt.Sprintf("%s", row["key"]))
				require.Equal(t, "range", fmt.Sprintf("%s", row["type"]))
			}
		}
		require.NotContains(t, text, "sort")
		require.NotContains(t, text, "bitmap")
		require.NotContains(t, text, "seq scan")
		require.True(t, strings.Contains(text, "primary") || strings.Contains(text, "pkey"), "must use the existing primary key: %s", text)
	}
}

// These wrappers execute real SQL and simulate only the lost commit acknowledgement.
type cleanupUnconfirmedCommitPool struct{ *sql.DB }

func (pool cleanupUnconfirmedCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := pool.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &cleanupUnconfirmedCommitTx{tx}, nil
}

type cleanupUnconfirmedCommitTx struct{ *sql.Tx }

func (tx cleanupUnconfirmedCommitTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	return context.DeadlineExceeded
}

func TestErrorLogCleanupUnconfirmedCommit(t *testing.T) {
	db := cleanupTestDB(t)
	require.NoError(t, db.Create(&Log{Id: 1, Type: LogTypeError, CreatedAt: 10}).Error)
	pool, err := db.DB()
	require.NoError(t, err)
	unconfirmed := db.Session(&gorm.Session{NewDB: true}).WithContext(context.Background())
	unconfirmed.ConnPool = cleanupUnconfirmedCommitPool{pool}
	unconfirmed.Statement.ConnPool = unconfirmed.ConnPool
	run := DataCleanupRun{Kind: CleanupErrorLogs, Before: 20, MaxID: 1, ErrorLogCleanupPlan: ErrorLogCleanupPlan{ScanMode: "id", ScanEndID: 1}}
	original := run
	batch, err := DeleteDataCleanupBatch(context.Background(), unconfirmed, &run, 100)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "commit")
	require.Equal(t, original, run)
	require.Zero(t, batch.Deleted)
	var count int64
	require.NoError(t, db.Model(&Log{}).Count(&count).Error)
	require.Zero(t, count, "the server committed despite the lost acknowledgement")
	batch, err = DeleteDataCleanupBatch(context.Background(), db, &run, 100)
	require.NoError(t, err)
	require.True(t, batch.Done)
	require.Zero(t, run.Deleted, "confirmed deletions must remain a lower bound")
}

func TestErrorLogCleanupSparsePreviewAndProgress(t *testing.T) {
	db := cleanupTestDB(t)
	require.NoError(t, db.Migrator().DropIndex(&Log{}, "idx_cleanup_created_id"))
	logs := make([]Log, 1003)
	for i := range logs {
		logs[i] = Log{Id: i + 1, CreatedAt: 10, Type: LogTypeConsume}
	}
	logs[1002].Type = LogTypeError
	require.NoError(t, db.CreateInBatches(logs, 100).Error)
	run, err := PrepareErrorLogCleanup(context.Background(), db, 20)
	require.NoError(t, err)
	require.Zero(t, run.Matched)
	require.False(t, run.CountExact, "a bounded preview cannot claim there are no errors")
	batch, err := DeleteDataCleanupBatch(context.Background(), db, run, 500)
	require.NoError(t, err)
	require.Equal(t, 500, batch.Scanned)
	require.Zero(t, batch.Deleted)
	require.Equal(t, 500, run.CursorID)
	batch, err = DeleteDataCleanupBatch(context.Background(), db, run, 500)
	require.NoError(t, err)
	require.Equal(t, 500, batch.Scanned)
	batch, err = DeleteDataCleanupBatch(context.Background(), db, run, 500)
	require.NoError(t, err)
	require.Equal(t, 3, batch.Scanned)
	require.EqualValues(t, 1, batch.Deleted)
	require.EqualValues(t, 1003, run.Scanned)
	batch, err = DeleteDataCleanupBatch(context.Background(), db, run, 500)
	require.NoError(t, err)
	require.Zero(t, batch.Scanned)
}

func TestErrorLogCleanupIndexAliasAndLostIndex(t *testing.T) {
	db := cleanupTestDB(t)
	require.NoError(t, db.Migrator().DropIndex(&Log{}, "idx_cleanup_created_id"))
	require.NoError(t, db.Migrator().RenameIndex(&Log{}, "idx_created_at_id", "cleanup_time_alias"))
	run, err := PrepareErrorLogCleanup(context.Background(), db, 20)
	require.NoError(t, err)
	require.Equal(t, "time", run.ScanMode)
	require.NoError(t, ValidateErrorLogCleanupPlan(context.Background(), db, run))
	require.NoError(t, db.Migrator().DropIndex(&Log{}, "cleanup_time_alias"))
	require.ErrorIs(t, ValidateErrorLogCleanupPlan(context.Background(), db, run), ErrCleanupScanIndex)
}

func TestErrorLogCleanupNoSchemaChange(t *testing.T) {
	db := cleanupTestDB(t)
	legacy := DataCleanupRun{ID: "legacy-cleanup", Kind: CleanupErrorLogs, Before: 100, MaxID: 10, CursorAt: 2, CursorID: 4, Deleted: 2, Scanned: 4, Status: "failed"}
	require.NoError(t, db.Create(&legacy).Error)
	for _, column := range []string{"scan_mode", "scan_end_at", "scan_end_id"} {
		require.False(t, db.Migrator().HasColumn(&DataCleanupRun{}, column), "scan state must stay in task JSON")
	}
	recorder := &cleanupDDLRecorder{Interface: db.Logger}
	for range 2 {
		require.NoError(t, db.Session(&gorm.Session{Logger: recorder}).AutoMigrate(&DataCleanupRun{}, &Log{}, &SystemTask{}, &SystemTaskLock{}))
	}
	require.Empty(t, recorder.statements, "existing cleanup tables must need no DDL")
	var upgraded DataCleanupRun
	require.NoError(t, db.First(&upgraded, "id = ?", legacy.ID).Error)
	require.Empty(t, upgraded.ScanMode)
	require.Zero(t, upgraded.ScanEndAt)
	require.Zero(t, upgraded.ScanEndID)
	require.Equal(t, legacy.CursorID, upgraded.CursorID)
	require.Equal(t, legacy.Deleted, upgraded.Deleted)
}

func TestErrorLogCleanupInvisibleMySQLIndex(t *testing.T) {
	db := cleanupTestDB(t)
	if db.Dialector.Name() != "mysql" {
		t.Skip("MySQL invisible-index behavior")
	}
	var version string
	require.NoError(t, db.Raw("SELECT VERSION()").Scan(&version).Error)
	if !strings.HasPrefix(version, "8.") {
		t.Skip("invisible indexes require MySQL 8")
	}
	require.NoError(t, db.Migrator().DropIndex(&Log{}, "idx_cleanup_created_id"))
	stmt := &gorm.Statement{DB: db}
	require.NoError(t, stmt.Parse(&Log{}))
	require.NoError(t, db.Exec("ALTER TABLE "+stmt.Quote(stmt.Table)+" ALTER INDEX idx_created_at_id INVISIBLE").Error)
	run, err := PrepareErrorLogCleanup(context.Background(), db, 100)
	require.NoError(t, err)
	require.Equal(t, "id", run.ScanMode)
}

func TestErrorLogCleanupPartialIndexAndCancellation(t *testing.T) {
	db := cleanupTestDB(t)
	if db.Dialector.Name() == "mysql" {
		t.Skip("MySQL does not support partial indexes")
	}
	require.NoError(t, db.Migrator().DropIndex(&Log{}, "idx_cleanup_created_id"))
	require.NoError(t, db.Migrator().DropIndex(&Log{}, "idx_created_at_id"))
	require.NoError(t, db.Exec("CREATE INDEX cleanup_partial ON logs (created_at,id) WHERE type=2").Error)
	run, err := PrepareErrorLogCleanup(context.Background(), db, 100)
	require.NoError(t, err)
	require.Equal(t, "id", run.ScanMode, "a partial index must not hide eligible errors")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = PrepareErrorLogCleanup(ctx, db, 100)
	require.ErrorIs(t, err, context.Canceled)
}

func TestErrorLogCleanupFailureAndRemovedSnapshotEnd(t *testing.T) {
	db := cleanupTestDB(t)
	require.NoError(t, db.Migrator().DropIndex(&Log{}, "idx_cleanup_created_id"))
	logs := []Log{{Id: 1, CreatedAt: 10, Type: LogTypeError}, {Id: 2, CreatedAt: 11, Type: LogTypeError}}
	require.NoError(t, db.Create(&logs).Error)
	run, err := PrepareErrorLogCleanup(context.Background(), db, 100)
	require.NoError(t, err)
	original := *run
	failure := errors.New("test delete failure")
	require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("cleanup_test_failure", func(tx *gorm.DB) { tx.AddError(failure) }))
	_, err = DeleteDataCleanupBatch(context.Background(), db, run, 1)
	require.ErrorIs(t, err, failure)
	require.Equal(t, original, *run, "rollback must not advance progress")
	require.NoError(t, db.Callback().Delete().Remove("cleanup_test_failure"))
	require.NoError(t, db.Where("id = ?", 2).Delete(&Log{}).Error)
	require.NoError(t, db.Create(&Log{Id: 3, CreatedAt: 11, Type: LogTypeError}).Error)
	batch, err := DeleteDataCleanupBatch(context.Background(), db, run, 500)
	require.NoError(t, err)
	require.Equal(t, 1, batch.Scanned)
	require.EqualValues(t, 1, batch.Deleted)
	batch, err = DeleteDataCleanupBatch(context.Background(), db, run, 500)
	require.NoError(t, err)
	require.Zero(t, batch.Scanned, "a deleted upper-bound row must not prevent exhaustion")
	var remaining Log
	require.NoError(t, db.First(&remaining, 3).Error)
}

func TestErrorLogCleanupExistingTimeIndexPlan(t *testing.T) {
	db := cleanupTestDB(t)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	var originalPlannerSettings string
	if db.Dialector.Name() == "postgres" {
		require.NoError(t, db.Raw("SELECT current_setting('enable_seqscan') || ',' || current_setting('enable_bitmapscan') || ',' || current_setting('enable_sort')").Scan(&originalPlannerSettings).Error)
	}
	require.NoError(t, db.Migrator().DropIndex(&Log{}, "idx_cleanup_created_id"))
	logs := make([]Log, 12000)
	for i := range logs {
		logs[i] = Log{Id: i + 1, CreatedAt: int64(i/100 + 1), Type: LogTypeConsume}
	}
	require.NoError(t, db.CreateInBatches(logs, 100).Error)
	stmt := db.Model(&Log{}).Select("id,created_at").Where("created_at >= ? AND created_at < ? AND created_at > ? AND created_at <= ?", 0, 1000, 50, 120).Order("created_at,id").Limit(100).Session(&gorm.Session{DryRun: true}).Find(&[]Log{}).Statement
	prefix := "EXPLAIN "
	if db.Dialector.Name() == "sqlite" {
		prefix = "EXPLAIN QUERY PLAN "
	}
	var plan []map[string]any
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := configureCleanupIndexScan(tx); err != nil {
			return err
		}
		return tx.Raw(prefix+stmt.SQL.String(), stmt.Vars...).Scan(&plan).Error
	}))
	t.Logf("bounded time-candidate plan: %v", plan)
	planText := strings.ToLower(fmt.Sprint(plan))
	require.NotContains(t, planText, "sort", "bounded candidates must not be obtained by sorting a large range")
	require.NotContains(t, planText, "bitmap")
	require.Contains(t, planText, "idx_created_at_id")
	run, err := PrepareErrorLogCleanup(context.Background(), db, 1000)
	require.NoError(t, err)
	run.CursorAt, run.CursorID = 50, 5000
	batch, err := DeleteDataCleanupBatch(context.Background(), db, run, 100)
	require.NoError(t, err)
	require.Equal(t, 100, batch.Scanned)
	require.Equal(t, 5100, run.CursorID)
	require.Zero(t, batch.Deleted)
	if db.Dialector.Name() == "postgres" {
		var restored string
		require.NoError(t, db.Raw("SELECT current_setting('enable_seqscan') || ',' || current_setting('enable_bitmapscan') || ',' || current_setting('enable_sort')").Scan(&restored).Error)
		require.Equal(t, originalPlannerSettings, restored, "maintenance planner settings must not escape the transaction")
	}
}

func TestFinancialCleanupSurvivesAllReplayWriters(t *testing.T) {
	db := cleanupTestDB(t)
	require.NoError(t, db.AutoMigrate(&PlatformChannelDailyStat{}, &EmployeeCommissionDailyStat{}))
	previous := DB
	DB = db
	bsClearMemBuffers()
	t.Cleanup(func() { DB = previous; bsClearMemBuffers() })
	logID := 123
	cost := &ConsumptionCost{LogId: &logID, CreatedAt: 10, ChannelId: 777, RevenueQuota: 100, CostQuota: 40}
	commission := &EmployeeCommissionLog{LogId: &logID, CreatedAt: 10, EmployeeUserId: 888, CommissionQuota: 6}
	require.NoError(t, db.Create(cost).Error)
	require.NoError(t, db.Create(commission).Error)
	platform := PlatformChannelDailyStat{StatDate: 1, ChannelId: 777, RevenueQuota: 100, CostQuota: 40, RecordCount: 1}
	employee := EmployeeCommissionDailyStat{StatDate: 1, EmployeeUserId: 888, CommissionQuota: 6, RecordCount: 1}
	require.NoError(t, db.Create(&platform).Error)
	require.NoError(t, db.Create(&employee).Error)
	for _, kind := range []string{CleanupLedger, CleanupCommission} {
		run := DataCleanupRun{ID: kind, Kind: kind, Before: 20, MaxID: 100}
		_, err := DeleteDataCleanupBatch(context.Background(), db, &run, 100)
		require.NoError(t, err)
	}
	n, err := BackfillFlushCosts(context.Background(), []*ConsumptionCost{cost}, 100)
	require.NoError(t, err)
	require.Zero(t, n)
	n, err = BackfillFlushPairs(context.Background(), []*CostCommissionBackfillPair{{Cost: cost, Commission: commission}}, 100)
	require.NoError(t, err)
	require.Zero(t, n)
	BufferConsumptionCostRecord(cost)
	flushConsumptionCostLedger()
	bufferCostAndCommissionLedger(cost, commission)
	flushCostAndCommissionLedger()
	costRetryQueue = []*costLedgerItem{{Cost: cost}}
	flushCostRetryQueue()
	pairRetryQueue = []*costCommissionLedgerPair{{Cost: cost, Commission: commission}}
	flushPairRetryQueue()
	inserted, err := CreateCommissionLog(commission)
	require.NoError(t, err)
	require.False(t, inserted)
	var remaining int64
	require.NoError(t, db.Model(&ConsumptionCost{}).Count(&remaining).Error)
	require.Zero(t, remaining)
	require.NoError(t, db.Model(&EmployeeCommissionLog{}).Count(&remaining).Error)
	require.Zero(t, remaining)
	var afterPlatform PlatformChannelDailyStat
	var afterEmployee EmployeeCommissionDailyStat
	require.NoError(t, db.First(&afterPlatform, platform.Id).Error)
	require.NoError(t, db.First(&afterEmployee, employee.Id).Error)
	require.Equal(t, platform, afterPlatform)
	require.Equal(t, employee, afterEmployee)
	require.Empty(t, memCommissionBuf)
	// Retaining the summaries must not inflate the remaining detail list count.
	taskID := "cleanup-done"
	require.NoError(t, db.Create(&DataCleanupRun{ID: "receipt", TaskID: &taskID, Kind: CleanupCommission, Before: 20, Deleted: 1, Status: "succeeded", UpdatedAt: 1}).Error)
	total, err := countCommissionLogs(CommissionLogFilter{StartTime: 1, EndTime: 20})
	require.NoError(t, err)
	require.Zero(t, total)
}

type cleanupDDLRecorder struct {
	gormlogger.Interface
	statements []string
}

func (rec *cleanupDDLRecorder) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	upper := strings.ToUpper(strings.TrimSpace(sql))
	if strings.HasPrefix(upper, "ALTER ") || strings.HasPrefix(upper, "CREATE ") || strings.HasPrefix(upper, "DROP ") {
		rec.statements = append(rec.statements, sql)
	}
	rec.Interface.Trace(ctx, begin, fc, err)
}

func TestDataCleanupMigrationAndPlan(t *testing.T) {
	db := cleanupTestDB(t)
	// Rehearse upgrading existing detail tables that lack the new indexes.
	for _, item := range []struct {
		table any
		index string
	}{{&ConsumptionCost{}, "idx_cost_cleanup_created_id"}, {&EmployeeCommissionLog{}, "idx_commission_cleanup_created_id"}, {&Log{}, "idx_cleanup_created_id"}} {
		require.NoError(t, db.Migrator().DropIndex(item.table, item.index))
	}
	models := []any{&ConsumptionCost{}, &EmployeeCommissionLog{}, &Log{}, &FinancialDetailTombstone{}, &DataCleanupRun{}}
	require.NoError(t, db.AutoMigrate(models...))
	recorder := &cleanupDDLRecorder{Interface: db.Logger}
	require.NoError(t, db.Session(&gorm.Session{Logger: recorder}).AutoMigrate(models...))
	require.Empty(t, recorder.statements, "second startup must not issue DDL")
	stmt := db.Model(&Log{}).Select("id").Where("type = ? AND created_at >= ? AND created_at < ?", LogTypeError, 1, 1000).Order("created_at,id").Limit(100).Session(&gorm.Session{DryRun: true}).Find(&[]Log{}).Statement
	plan := []map[string]any{}
	prefix := "EXPLAIN "
	if db.Dialector.Name() == "sqlite" {
		prefix = "EXPLAIN QUERY PLAN "
	}
	require.NoError(t, db.Raw(prefix+stmt.SQL.String(), stmt.Vars...).Scan(&plan).Error)
	t.Logf("cleanup query plan: %v", plan)
}

func TestFinancialCleanupVersionUsesTaskOrder(t *testing.T) {
	db := cleanupTestDB(t)
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
	firstTask, secondTask := "first-task", "second-task"
	// Two jobs can finish in one second; random IDs and preview times must not
	// hide the later job's progress from statistics and exports.
	runs := []DataCleanupRun{
		{ID: "z-first", TaskID: &firstTask, TaskSequence: 1, Kind: CleanupLedger, Before: 100, UpdatedAt: 10, Status: "succeeded"},
		{ID: "a-second", TaskID: &secondTask, TaskSequence: 2, Kind: CleanupLedger, Before: 50, UpdatedAt: 10, Status: "running"},
	}
	require.NoError(t, db.Create(&runs).Error)
	version, err := FinancialCleanupVersion(context.Background(), CleanupLedger, 1)
	require.NoError(t, err)
	require.Equal(t, "a-second:0:0:running", version)
	require.NoError(t, db.Model(&DataCleanupRun{}).Where("id = ?", "a-second").Update("deleted", 3).Error)
	next, err := FinancialCleanupVersion(context.Background(), CleanupLedger, 1)
	require.NoError(t, err)
	require.NotEqual(t, version, next)
	version, err = FinancialCleanupVersion(context.Background(), CleanupLedger, 75)
	require.NoError(t, err)
	require.Equal(t, "z-first:0:0:succeeded", version)
}

func TestFinancialCleanupInvalidatesUniqueRecordExport(t *testing.T) {
	db := cleanupTestDB(t)
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
	logID := 456
	cost := ConsumptionCost{LogId: &logID, CreatedAt: 10, RevenueQuota: 20}
	require.NoError(t, db.Create(&cost).Error)
	filter := ConsumptionCostLedgerCommonFilter{LogId: logID, StartTime: 1, EndTime: 20}
	rows, _, err := writeLedgerExportCSVSharded(context.Background(), "cleanup-export", filter, filepath.Join(t.TempDir(), "export.csv.gz"), func(int, int64) {
		taskID := "export-cleanup-task"
		require.NoError(t, db.Create(&DataCleanupRun{ID: "export-cleanup", TaskID: &taskID, TaskSequence: 1, Kind: CleanupLedger, Before: 20, Status: "running"}).Error)
	})
	require.EqualValues(t, 1, rows)
	require.EqualError(t, err, "cleanup.export_changed", "unique-record early return must also check cleanup version")
}

func TestDataCleanupStrictBoundaryAndSnapshot(t *testing.T) {
	db := cleanupTestDB(t)
	ctx := context.Background()
	for _, kind := range []string{CleanupLedger, CleanupCommission, CleanupErrorLogs} {
		t.Run(kind, func(t *testing.T) {
			var table any = &ConsumptionCost{}
			if kind == CleanupCommission {
				table = &EmployeeCommissionLog{}
			}
			if kind == CleanupErrorLogs {
				table = &Log{}
			}
			for i, at := range []int64{99, 100, 101, 98} {
				row := map[string]any{"id": i + 1, "created_at": at}
				if kind == CleanupErrorLogs {
					row["type"] = LogTypeError
				} else {
					row["log_id"] = i + 1
				}
				if kind == CleanupCommission {
					row["employee_id"] = 1
					row["employee_user_id"] = 1
					row["customer_user_id"] = 1
				}
				require.NoError(t, db.Model(table).Create(row).Error)
			}
			run := DataCleanupRun{ID: kind, Kind: kind, Before: 100, MaxID: 3}
			batch, err := DeleteDataCleanupBatch(ctx, db, &run, 100)
			require.NoError(t, err)
			require.EqualValues(t, 1, batch.Deleted)
			var ids []int
			require.NoError(t, db.Model(table).Order("id").Pluck("id", &ids).Error)
			require.Equal(t, []int{2, 3, 4}, ids)
			batch, err = DeleteDataCleanupBatch(ctx, db, &run, 100)
			require.NoError(t, err)
			require.Zero(t, batch.Deleted)
		})
	}
}

func TestDataCleanupPreservesOtherLogsAndManualCommission(t *testing.T) {
	db := cleanupTestDB(t)
	for i := 0; i < 7; i++ {
		require.NoError(t, db.Create(&Log{Type: i, CreatedAt: 10}).Error)
	}
	run := DataCleanupRun{ID: "errors", Kind: CleanupErrorLogs, Before: 20, MaxID: 100}
	batch, err := DeleteDataCleanupBatch(context.Background(), db, &run, 100)
	require.NoError(t, err)
	require.EqualValues(t, 1, batch.Deleted)
	var types []int
	require.NoError(t, db.Model(&Log{}).Order("type").Pluck("type", &types).Error)
	require.Equal(t, []int{0, 1, 2, 3, 4, 6}, types)
	valid := 13
	manual := EmployeeCommissionLog{CreatedAt: 10, ModelName: ManualPerformanceModelName}
	require.NoError(t, db.Create(&manual).Error)
	require.NoError(t, db.Create(&EmployeeCommissionLog{LogId: &valid, CreatedAt: 10, CommissionQuota: -5}).Error)
	run = DataCleanupRun{ID: "commission", Kind: CleanupCommission, Before: 20, MaxID: 100}
	batch, err = DeleteDataCleanupBatch(context.Background(), db, &run, 1)
	require.NoError(t, err)
	require.Zero(t, batch.Deleted)
	require.Equal(t, manual.Id, run.CursorID)
	batch, err = DeleteDataCleanupBatch(context.Background(), db, &run, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, batch.Deleted)
	require.NoError(t, db.First(&EmployeeCommissionLog{}, manual.Id).Error)
}

func TestDataCleanupOnlyDeletesSelectedDetails(t *testing.T) {
	db := cleanupTestDB(t)
	// Use overlapping IDs and old timestamps in every table. An accidentally
	// broadened deletion must not pass merely because unrelated rows are recent.
	protected := []any{
		&PlatformChannelDailyStat{Id: 1, StatDate: 1, ChannelId: 1, RevenueQuota: 100, CostQuota: 40, RecordCount: 1},
		&EmployeeCommissionDailyStat{Id: 1, StatDate: 1, EmployeeUserId: 1, RevenueQuota: 100, ProfitQuota: 60, CommissionQuota: 6, RecordCount: 1},
		&EmployeeCustomerCommissionDailyStat{Id: 1, StatDate: 1, EmployeeUserId: 1, CustomerUserId: 1, CommissionQuota: 6, RecordCount: 1},
		&EmployeeCommissionResetPeriodDailyStat{Id: 1, ResetStartedAt: 1, StatDate: 1, EmployeeUserId: 1, CommissionQuota: 6, RecordCount: 1},
		&BusinessDailyStatsCoverage{Id: 1, StatDate: 1, CompletedAt: 10},
		&BusinessStatsAppliedBatch{Id: 1, BatchKey: "protected", AppliedAt: 10},
		&QuotaData{Id: 1, UserID: 1, CreatedAt: 10, Quota: 100, Count: 1, TokenUsed: 200},
		&User{Id: 1, Username: "cleanup-protected", Quota: 313, UsedQuota: 100, AffQuota: 17, AffHistoryQuota: 23},
		&UserExtension{Id: 1, UserId: 1, CommissionTotalQuota: 60, CommissionPendingQuota: 20, CommissionSettledQuota: 40, ProfitTotalQuota: 100, RevenueCustomerCount: 1, CreatedAt: 10, UpdatedAt: 10},
		&EmployeeProfile{Id: 1, UserId: 1, TargetAmount: 100, CreatedAt: 10, UpdatedAt: 10},
	}
	require.NoError(t, db.AutoMigrate(protected...))
	// cleanupTestDB owns a private SQLite database, random MySQL table prefix,
	// or dedicated PostgreSQL schema. Only these test-owned objects are removed.
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(protected...)) })
	before := make([][]map[string]any, len(protected))
	for i, row := range protected {
		require.NoError(t, db.Create(row).Error)
		require.NoError(t, db.Model(row).Find(&before[i]).Error)
	}
	logID := 1
	details := []any{
		&ConsumptionCost{Id: 1, LogId: &logID, UserId: 1, CreatedAt: 10, RevenueQuota: 100},
		&EmployeeCommissionLog{Id: 1, LogId: &logID, EmployeeId: 1, EmployeeUserId: 1, CustomerUserId: 1, CreatedAt: 10, CommissionQuota: 6, SettleStatus: 1, SettledAt: 15},
		&Log{Id: 1, UserId: 1, CreatedAt: 10, Type: LogTypeError},
	}
	for _, row := range details {
		require.NoError(t, db.Create(row).Error)
	}
	for _, kind := range []string{"", "users", "platform_channel_daily_stats", "ledger; DROP TABLE users"} {
		run := DataCleanupRun{ID: "invalid", Kind: kind, Before: 20, MaxID: 1}
		_, err := DeleteDataCleanupBatch(context.Background(), db, &run, 100)
		require.Error(t, err)
	}
	for i, kind := range []string{CleanupLedger, CleanupCommission, CleanupErrorLogs} {
		run := DataCleanupRun{ID: kind, Kind: kind, Before: 20, MaxID: 1}
		batch, err := DeleteDataCleanupBatch(context.Background(), db, &run, 100)
		require.NoError(t, err)
		require.EqualValues(t, 1, batch.Deleted)
		for j, table := range details {
			require.True(t, db.Migrator().HasTable(table), "%s must preserve the detail table", kind)
			var count int64
			require.NoError(t, db.Model(table).Count(&count).Error)
			if j <= i {
				require.Zero(t, count)
			} else {
				require.EqualValues(t, 1, count, "%s must not delete a different kind of detail", kind)
			}
		}
		for j, table := range protected {
			var after []map[string]any
			require.NoError(t, db.Model(table).Find(&after).Error)
			require.Equal(t, before[j], after, "%s must preserve %T unchanged", kind, table)
		}
	}
}

func TestFinancialCleanupReplayAndRollback(t *testing.T) {
	db := cleanupTestDB(t)
	logID := 123
	cost := &ConsumptionCost{LogId: &logID, RevenueQuota: 100, CreatedAt: 10}
	require.NoError(t, db.Create(cost).Error)
	run := DataCleanupRun{ID: "cost", Kind: CleanupLedger, Before: 20, MaxID: cost.Id}
	_, err := DeleteDataCleanupBatch(context.Background(), db, &run, 100)
	require.NoError(t, err)
	var markers []FinancialDetailTombstone
	require.NoError(t, db.Find(&markers).Error)
	require.Len(t, markers, 1)
	kept, err := FilterRetainedFinancialDetails(db, CleanupLedger, []int{logID})
	require.NoError(t, err)
	require.Contains(t, kept, logID)
	// Cleaning the ledger must not pretend the commission was also cleaned.
	kept, err = FilterRetainedFinancialDetails(db, CleanupCommission, []int{logID})
	require.NoError(t, err)
	require.Empty(t, kept)
	other := 124
	require.NoError(t, db.Create(&ConsumptionCost{LogId: &other, CreatedAt: 10}).Error)
	require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("cleanup_failure", func(tx *gorm.DB) { tx.AddError(fmt.Errorf("injected delete failure")) }))
	run = DataCleanupRun{ID: "rollback", Kind: CleanupLedger, Before: 20, MaxID: 100}
	_, err = DeleteDataCleanupBatch(context.Background(), db, &run, 100)
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Model(&FinancialDetailTombstone{}).Where("log_id = ?", other).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Where("log_id = ?", other).First(&ConsumptionCost{}).Error)
}
