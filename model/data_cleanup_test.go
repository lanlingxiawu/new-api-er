package model

import (
	"context"
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
		prefix = fmt.Sprintf("cleanup_%d_", time.Now().UnixNano())
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
			require.NoError(t, db.Migrator().DropTable(&ConsumptionCost{}, &EmployeeCommissionLog{}, &Log{}, &FinancialDetailTombstone{}, &DataCleanupRun{}, &SystemTask{}, &SystemTaskLock{}, &PlatformChannelDailyStat{}, &EmployeeCommissionDailyStat{}))
		}
		require.NoError(t, sqlDB.Close())
	})
	return db
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
