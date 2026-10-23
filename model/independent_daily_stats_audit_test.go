package model

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// The fixture carries the columns used by all four writers. Its unique indexes
// are created with table-specific names so a shared PostgreSQL test schema's
// production index names cannot make AutoMigrate silently skip our indexes.
type auditDailyStatRow struct {
	Id              int
	StatDate        int64
	ResetStartedAt  int64
	ChannelId       int
	EmployeeUserId  int
	CustomerUserId  int
	ChannelName     string `gorm:"type:varchar(255)"`
	RevenueQuota    int64
	CostQuota       int64
	ProfitQuota     int64
	CommissionQuota int64
	RecordCount     int64
	CostRatioSum    float64
	LastCreatedAt   int64
}

func TestIndependentAuditLedgerRollbackAndRetry(t *testing.T) {
	requireDB(t)
	bsInitFallback(t)
	bsClearMemBuffers()
	t.Cleanup(bsClearMemBuffers)
	bsWithPipelineSetting(t, func(s *operation_setting.LedgerPipelineSetting) {
		s.CostOuterBatchSize, s.OuterBatchSize, s.InnerBatchSize = 1, 1, 1
		s.BufMaxEntries = 100000
	})
	bsWithRetrySetting(t, func(s *operation_setting.LedgerRetryQueueSetting) {
		s.StatUpsertMaxRetries, s.RetryQueueMaxEntries = 3, 100
	})
	day, channelID, logID := bsFarFutureDay(t), nextTestID(), nextTestID()
	t.Cleanup(func() {
		require.NoError(t, DB.Unscoped().Where("log_id = ?", logID).Delete(&ConsumptionCost{}).Error)
		require.NoError(t, DB.Unscoped().Where("stat_date = ? AND channel_id = ?", day, channelID).Delete(&PlatformChannelDailyStat{}).Error)
		coveredDaysCache.Delete(day)
		require.NoError(t, DB.Unscoped().Where("stat_date = ?", day).Delete(&BusinessDailyStatsCoverage{}).Error)
	})
	failStat, injected := true, 0
	const callback = "audit:daily-stat-create-failure"
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if failStat && tx.Statement.Table == "platform_channel_daily_stats" {
			injected++
			tx.AddError(errors.New("injected daily stat failure"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Create().Remove(callback)) })
	cost := &ConsumptionCost{LogId: common.GetPointer(logID), ChannelId: channelID, CreatedAt: day + 100, RevenueQuota: 100, CostQuota: 40, CostRatio: 0.4}
	BufferConsumptionCostRecord(cost)
	flushConsumptionCostLedger()
	require.Equal(t, 1, injected)
	var count int64
	require.NoError(t, DB.Model(&ConsumptionCost{}).Where("log_id = ?", logID).Count(&count).Error)
	require.Zero(t, count, "detail insert must roll back with the failed statistic")
	require.NoError(t, DB.Model(&PlatformChannelDailyStat{}).Where("stat_date = ? AND channel_id = ?", day, channelID).Count(&count).Error)
	require.Zero(t, count)
	retryQueueMu.Lock()
	backlog := len(costRetryQueue)
	retryQueueMu.Unlock()
	require.Equal(t, 1, backlog, "failed batch must remain available for retry")

	failStat = false
	flushCostRetryQueue()
	costLedgerFlushState = ledgerFlushState{} // duplicate delivery without a timing-based test
	BufferConsumptionCostRecord(cost)         // duplicate delivery must not double the aggregate
	flushConsumptionCostLedger()
	require.NoError(t, DB.Model(&ConsumptionCost{}).Where("log_id = ?", logID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	var stat PlatformChannelDailyStat
	require.NoError(t, DB.Where("stat_date = ? AND channel_id = ?", day, channelID).Take(&stat).Error)
	require.EqualValues(t, 100, stat.RevenueQuota)
	require.EqualValues(t, 40, stat.CostQuota)
	require.EqualValues(t, 1, stat.RecordCount)
}

func TestIndependentAuditLedgerSQLite(t *testing.T) {
	previousDB, previousLogDB := DB, LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis := common.RedisEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		InitColumnNames()
		common.RedisEnabled = previousRedis
		require.NoError(t, pool.Close())
	})
	require.NoError(t, db.AutoMigrate(&ConsumptionCost{}, &PlatformChannelDailyStat{}, &BusinessDailyStatsCoverage{}, &FinancialDetailTombstone{}))
	DB, LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	InitColumnNames()
	common.RedisEnabled = false
	t.Run("rollback_and_retry", TestIndependentAuditLedgerRollbackAndRetry)
}

// Exercise the actual delta upserts without queues, timers, or refund fixtures.
// Positive consumption followed by a reversal must preserve the net totals on
// every supported database. All tables are uniquely owned by this test run.
func TestIndependentAuditDailyStatsDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			pool, err := db.DB()
			require.NoError(t, err)
			pool.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, pool.Close()) })
			for _, test := range []struct {
				name  string
				key   string
				apply func(*gorm.DB, int64) error
			}{
				{"platform", "stat_date,channel_id", func(tx *gorm.DB, delta int64) error {
					return upsertPlatformDailyStatTx(tx, 1700000000, 7, "audit", delta, delta/2, 1, 1, 1700000001)
				}},
				{"commission", "stat_date,employee_user_id", func(tx *gorm.DB, delta int64) error {
					return upsertCommissionDailyStatTx(tx, 1700000000, 7, delta, delta/2, delta/2, delta/4, 1, 1700000001)
				}},
				{"customer", "stat_date,employee_user_id,customer_user_id", func(tx *gorm.DB, delta int64) error {
					return upsertCustomerCommissionDailyStatTx(tx, 1700000000, 7, 8, delta, delta/2, delta/2, delta/4, 1, 1700000001)
				}},
				{"reset_period", "reset_started_at,stat_date,employee_user_id", func(tx *gorm.DB, delta int64) error {
					return upsertCommissionResetPeriodDailyStatTx(tx, 1690000000, 1700000000, 7, delta, delta/2, delta/2, delta/4, 1, 1700000001)
				}},
			} {
				t.Run(test.name, func(t *testing.T) {
					table := fmt.Sprintf("audit_%s_%s", test.name, common.GetUUID())
					tx := db.Table(table)
					require.NoError(t, tx.AutoMigrate(&auditDailyStatRow{}))
					t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(table)) })
					// Identifiers are test constants plus a hex UUID; syntax is portable
					// across SQLite, MySQL and PostgreSQL and has no reserved columns.
					require.NoError(t, db.Exec(fmt.Sprintf("CREATE UNIQUE INDEX %s_key ON %s (%s)", table, table, test.key)).Error)
					require.NoError(t, test.apply(tx, 100), "first consumption")
					require.NoError(t, test.apply(tx, -40), "partial reversal")
					var result struct{ RevenueQuota, CostQuota, RecordCount int64 }
					require.NoError(t, tx.Select("revenue_quota", "cost_quota", "record_count").Take(&result).Error)
					require.Equal(t, int64(60), result.RevenueQuota)
					require.Equal(t, int64(30), result.CostQuota)
					require.Equal(t, int64(2), result.RecordCount)
					rollback := errors.New("audit transaction failure")
					require.ErrorIs(t, db.Transaction(func(transaction *gorm.DB) error {
						if err := test.apply(transaction.Table(table), 400); err != nil {
							return err
						}
						return rollback
					}), rollback)
					require.NoError(t, tx.Select("revenue_quota", "cost_quota", "record_count").Take(&result).Error)
					require.Equal(t, int64(60), result.RevenueQuota)
					require.Equal(t, int64(2), result.RecordCount)

					if dialect != "sqlite" {
						pool.SetMaxOpenConns(4)
						defer pool.SetMaxOpenConns(1)
					}
					var workers sync.WaitGroup
					errs := make(chan error, 8)
					for range 8 {
						workers.Go(func() {
							for range 10 {
								// Fresh statement per worker: GORM statements themselves
								// must not be shared while exercising concurrent updates.
								if err := test.apply(db.Table(table), 4); err != nil {
									errs <- err
									return
								}
							}
						})
					}
					workers.Wait()
					close(errs)
					for err := range errs {
						require.NoError(t, err)
					}
					require.NoError(t, tx.Select("revenue_quota", "cost_quota", "record_count").Take(&result).Error)
					require.Equal(t, int64(380), result.RevenueQuota)
					require.Equal(t, int64(190), result.CostQuota)
					require.Equal(t, int64(82), result.RecordCount)
					var detail auditDailyStatRow
					require.NoError(t, db.Table(table).Take(&detail).Error)
					require.Equal(t, int64(1700000001), detail.LastCreatedAt)
					if test.name == "platform" {
						require.Equal(t, "audit", detail.ChannelName)
						require.Equal(t, float64(82), detail.CostRatioSum)
						require.NoError(t, upsertPlatformDailyStatTx(db.Table(table), 1700000000, 7, "renamed", 0, 0, 0, 0, 1600000000))
						require.NoError(t, db.Table(table).Take(&detail).Error)
						require.Equal(t, "audit", detail.ChannelName)
						require.Equal(t, int64(1700000001), detail.LastCreatedAt)
					} else {
						require.Equal(t, int64(190), detail.ProfitQuota)
						require.Equal(t, int64(95), detail.CommissionQuota)
					}
					require.NoError(t, db.Table(table).Where("id = ?", detail.Id).Update("last_created_at", 1800000000).Error)
					if test.name == "platform" {
						require.NoError(t, db.Table(table).Where("id = ?", detail.Id).Update("channel_name", "").Error)
					}
					require.NoError(t, test.apply(db.Table(table), 0))
					require.NoError(t, db.Table(table).Take(&detail).Error)
					require.Equal(t, int64(1800000000), detail.LastCreatedAt)
					require.Equal(t, int64(380), detail.RevenueQuota)
					require.Equal(t, int64(83), detail.RecordCount)
					if test.name == "platform" {
						require.Equal(t, "audit", detail.ChannelName)
					}
				})
			}
		})
	}
}
