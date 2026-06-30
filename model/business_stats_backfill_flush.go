package model

// business_stats_backfill_flush.go — exported helpers that the backfill service uses
// to write batches of records directly into the ledger tables without going through
// the normal in-memory buffers/flush-loop.
//
// Rule 0: these functions are NOT called on the relay hot-path.  They are only
// invoked from a single admin-triggered goroutine with controlled batch sizes and
// inter-flush sleeps.

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BusinessDayStart returns the Unix timestamp of 00:00:00 (in the configured
// business-stats timezone) for the local calendar day that contains ts.
// This is the same computation used internally by localDayStart; it is exported
// so that the service layer can make day-boundary decisions without referencing
// private functions.
func BusinessDayStart(ts int64) int64 {
	return localDayStart(ts)
}

// backfillStatAggregator accumulates daily-stat deltas for a single backfill batch in
// LOCAL maps, fully isolated from the live relay in-memory buffers.  Writing into those
// shared buffers would make the admin backfill contend on the same locks the relay
// settlement path uses and have its deltas drained by the live flush loop — a
// shared-resource violation of Rule 0.  flush() instead writes the accumulated
// deltas straight to DB via the same upsert*Tx helpers the live flush loop uses.
//
// Tier upgrades ARE intentionally applied from backfilled profit: the backfill
// replays historical settlements and tier progression should reflect them.
// Upgrade checks are triggered after the reset-period daily stats are committed,
// by querying SUM(profit_quota) directly from employee_commission_reset_period_daily_stats.
type backfillStatAggregator struct {
	platform           map[string]*platformStatDelta
	commission         map[string]*commissionStatDelta
	customerCommission map[string]*customerCommissionStatDelta
	resetPeriodDaily   map[string]*commissionResetPeriodDailyDelta
	// tierLevelCache avoids repeated DB queries for the same employee within
	// a single flush batch. nil stored as value means "tried, got error/not found".
	tierLevelCache map[int]*EmployeeTierLevel
}

func newBackfillStatAggregator() *backfillStatAggregator {
	return &backfillStatAggregator{
		platform:           make(map[string]*platformStatDelta),
		commission:         make(map[string]*commissionStatDelta),
		customerCommission: make(map[string]*customerCommissionStatDelta),
		resetPeriodDaily:   make(map[string]*commissionResetPeriodDailyDelta),
		tierLevelCache:     make(map[int]*EmployeeTierLevel),
	}
}

// addPlatform mirrors bufferPlatformStatMem for the cost side of a backfilled record.
func (a *backfillStatAggregator) addPlatform(c *ConsumptionCost) {
	if c == nil {
		return
	}
	statDate := localDayStart(c.CreatedAt)
	key := memPlatformKey(statDate, c.ChannelId)
	d := a.platform[key]
	if d == nil {
		d = &platformStatDelta{StatDate: statDate, ChannelId: c.ChannelId}
		a.platform[key] = d
	}
	d.RevenueQuota += c.RevenueQuota
	d.CostQuota += c.CostQuota
	d.RecordCount++
	d.CostRatioSum += c.CostRatio
	if d.ChannelName == "" {
		d.ChannelName = c.ChannelName
	}
	if c.CreatedAt > d.LastCreatedAt {
		d.LastCreatedAt = c.CreatedAt
	}
}

// addCommission mirrors BufferCommissionDailyStat + BufferCommissionResetPeriodDailyStat for a
// backfilled commission log, accumulating into the local maps only.
func (a *backfillStatAggregator) addCommission(log *EmployeeCommissionLog) {
	if log == nil {
		return
	}
	statDate := localDayStart(log.CreatedAt)
	level, tried := a.tierLevelCache[log.EmployeeUserId]
	if !tried {
		var err error
		level, err = GetOrCreateTierLevel(log.EmployeeUserId, true)
		if err != nil {
			common.SysError("backfill addCommission: get tier level failed: " + err.Error())
			level = nil
		}
		a.tierLevelCache[log.EmployeeUserId] = level // nil stored as sentinel: skip re-query
	}
	resetAt := effectiveResetStartedAt(level, log.CreatedAt)

	// commission daily stat
	ck := memCommissionKey(statDate, log.EmployeeUserId)
	cd := a.commission[ck]
	if cd == nil {
		cd = &commissionStatDelta{StatDate: statDate, EmployeeUserId: log.EmployeeUserId}
		a.commission[ck] = cd
	}
	cd.RevenueQuota += log.RevenueQuota
	cd.CostQuota += log.CostQuota
	cd.ProfitQuota += log.ProfitQuota
	cd.CommissionQuota += log.CommissionQuota
	cd.RecordCount++
	if log.CreatedAt > cd.LastCreatedAt {
		cd.LastCreatedAt = log.CreatedAt
	}

	// customer commission daily stat
	if log.CustomerUserId > 0 {
		cck := memCustomerCommissionKey(statDate, log.EmployeeUserId, log.CustomerUserId)
		ccd := a.customerCommission[cck]
		if ccd == nil {
			ccd = &customerCommissionStatDelta{StatDate: statDate, EmployeeUserId: log.EmployeeUserId, CustomerUserId: log.CustomerUserId}
			a.customerCommission[cck] = ccd
		}
		ccd.RevenueQuota += log.RevenueQuota
		ccd.CostQuota += log.CostQuota
		ccd.ProfitQuota += log.ProfitQuota
		ccd.CommissionQuota += log.CommissionQuota
		ccd.RecordCount++
		if log.CreatedAt > ccd.LastCreatedAt {
			ccd.LastCreatedAt = log.CreatedAt
		}
	}

	// reset-period daily stat
	if resetAt > 0 {
		rdk := memCommissionResetPeriodDailyKey(resetAt, statDate, log.EmployeeUserId)
		rd := a.resetPeriodDaily[rdk]
		if rd == nil {
			rd = &commissionResetPeriodDailyDelta{ResetStartedAt: resetAt, StatDate: statDate, EmployeeUserId: log.EmployeeUserId}
			a.resetPeriodDaily[rdk] = rd
		}
		rd.RevenueQuota += log.RevenueQuota
		rd.CostQuota += log.CostQuota
		rd.ProfitQuota += log.ProfitQuota
		rd.CommissionQuota += log.CommissionQuota
		rd.RecordCount++
		if log.CreatedAt > rd.LastCreatedAt {
			rd.LastCreatedAt = log.CreatedAt
		}
	}

}

// flush writes all accumulated deltas directly to DB. Errors are logged and
// swallowed: the detail rows are already committed and any daily-stat divergence is
// recoverable by re-running the backfill. ctx carries the backfill's deadline so a
// stuck statement cannot hang the admin job.
//
// DB operation budget per flush call (N/M/K = unique stat keys, E = unique employees with profit):
//
//	1 transaction (N+M+K statements) + 1 tryAutoUpgradeTierBatch call (1 batch SUM query for all E employees)
func (a *backfillStatAggregator) flush(ctx context.Context) {
	// Wrap all stat-table upserts in one transaction to reduce round-trip overhead.
	if len(a.platform) > 0 || len(a.commission) > 0 || len(a.customerCommission) > 0 || len(a.resetPeriodDaily) > 0 {
		db := DB.WithContext(ctx)
		if err := db.Transaction(func(tx *gorm.DB) error {
			for _, d := range a.platform {
				if err := upsertPlatformDailyStatTx(tx, d.StatDate, d.ChannelId, d.ChannelName, d.RevenueQuota, d.CostQuota, d.RecordCount, d.CostRatioSum, d.LastCreatedAt); err != nil {
					return err
				}
			}
			for _, d := range a.commission {
				if err := upsertCommissionDailyStatTx(tx, d.StatDate, d.EmployeeUserId, d.RevenueQuota, d.CostQuota, d.ProfitQuota, d.CommissionQuota, d.RecordCount, d.LastCreatedAt); err != nil {
					return err
				}
			}
			for _, d := range a.customerCommission {
				if err := upsertCustomerCommissionDailyStatTx(tx, d.StatDate, d.EmployeeUserId, d.CustomerUserId, d.RevenueQuota, d.CostQuota, d.ProfitQuota, d.CommissionQuota, d.RecordCount, d.LastCreatedAt); err != nil {
					return err
				}
			}
			for _, d := range a.resetPeriodDaily {
				if err := upsertCommissionResetPeriodDailyStatTx(tx, d.ResetStartedAt, d.StatDate, d.EmployeeUserId, d.RevenueQuota, d.CostQuota, d.ProfitQuota, d.CommissionQuota, d.RecordCount, d.LastCreatedAt); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			common.SysError("backfill flush stats tx: " + err.Error())
		}
		// ensureDailyCoverage is in-memory only; called after the transaction commits.
		for _, d := range a.platform {
			ensureDailyCoverage(d.StatDate)
		}
	}

	// 对本批有正利润的员工触发等级升级检查（reset period daily stats 已写入后）。
	// 使用批量版本：1 次 SUM 查询替代 N 次串行查询。
	upgradeCandidates := make([]int, 0, len(a.resetPeriodDaily))
	seenUpgrade := make(map[int]struct{}, len(a.resetPeriodDaily))
	for _, d := range a.resetPeriodDaily {
		if d.ProfitQuota > 0 && d.EmployeeUserId > 0 {
			if _, ok := seenUpgrade[d.EmployeeUserId]; !ok {
				seenUpgrade[d.EmployeeUserId] = struct{}{}
				upgradeCandidates = append(upgradeCandidates, d.EmployeeUserId)
			}
		}
	}
	if len(upgradeCandidates) > 0 {
		tryAutoUpgradeTierBatch(upgradeCandidates)
	}
}

// BackfillFlushCosts batch-inserts a slice of ConsumptionCost records into the DB
// using ON CONFLICT(log_id) DO NOTHING for idempotency.  It mirrors the column
// select used by the normal flushConsumptionCostLedger to keep parity.
// After a successful insert it aggregates platform daily-stat deltas via a local
// backfillStatAggregator (isolated from the live relay buffers) only for records
// that were not already in the DB, avoiding double-counting on re-runs.
//
// It returns the number of records that were actually inserted (rows that ON
// CONFLICT DO NOTHING skipped because their log_id already existed are NOT
// counted), so callers can report an accurate "written to DB" total even on a
// re-run where most rows are duplicates.
//
// Idempotency note: ON CONFLICT only de-duplicates rows where log_id IS NOT NULL.
// Rows with log_id = NULL (circuit-breaker fired before the log record was written)
// have no unique key and can be inserted again on a re-run; their aggregation delta
// is therefore also re-applied on each re-run — an accepted limitation. Such rows
// are always counted as inserted.
//
// On DB error the entire batch is returned as an error with an inserted count of 0;
// the caller is responsible for counting those records as discarded.
func BackfillFlushCosts(ctx context.Context, costs []*ConsumptionCost, batchSize int) (int, error) {
	if len(costs) == 0 {
		return 0, nil
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	// Pre-identify which log_ids already exist so we can skip aggregation (and the
	// inserted count) for them after the insert.  ON CONFLICT DO NOTHING is
	// transparent to GORM — we cannot tell from RowsAffected which specific rows
	// were skipped in a batch, so we diff against this pre-insert snapshot instead.
	existing := backfillExistingCostLogIDs(ctx, costs)

	if err := DB.WithContext(ctx).Select(
		"LogId", "UserId", "ChannelId", "ChannelName", "GroupName", "ModelName",
		"RevenueQuota", "CostQuota", "GroupRatio", "CostRatio", "CreatedAt",
	).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "log_id"}},
		DoNothing: true,
	}).CreateInBatches(costs, batchSize).Error; err != nil {
		return 0, fmt.Errorf("BackfillFlushCosts: %w", err)
	}
	// Aggregate (into local maps, isolated from the live buffers) and count only
	// newly-inserted records.
	agg := newBackfillStatAggregator()
	inserted := 0
	for _, c := range costs {
		if c.LogId == nil {
			// NULL log_id: always inserted (no dedup key), always aggregate.
			agg.addPlatform(c)
			inserted++
			continue
		}
		if _, dup := existing[*c.LogId]; !dup {
			agg.addPlatform(c)
			inserted++
		}
	}
	agg.flush(ctx)
	return inserted, nil
}

// costCommissionBackfillPair groups a ConsumptionCost with its corresponding
// EmployeeCommissionLog for a single-transaction batch insert.
type CostCommissionBackfillPair struct {
	Cost       *ConsumptionCost
	Commission *EmployeeCommissionLog
}

// BackfillFlushPairs batch-inserts cost+commission pairs in a single transaction
// using ON CONFLICT DO NOTHING on both tables.  After a successful commit it
// aggregates daily-stat / employee-ext deltas via a local backfillStatAggregator
// (isolated from the live relay buffers, but tier upgrades still applied) only for
// pairs that were not already present in the DB, preventing double-counting on re-runs.
//
// It returns the number of pairs that were actually inserted (pairs whose cost
// log_id already existed and were skipped by ON CONFLICT are NOT counted), so the
// caller can report an accurate "written to DB" total even on a re-run.
//
// On DB error the entire batch is returned as an error with an inserted count of 0.
func BackfillFlushPairs(ctx context.Context, pairs []*CostCommissionBackfillPair, batchSize int) (int, error) {
	if len(pairs) == 0 {
		return 0, nil
	}
	if batchSize <= 0 {
		batchSize = 100
	}

	// Cost and commission share the same log_id and are inserted atomically, so
	// checking the cost table is sufficient to detect already-existing pairs.
	costs := make([]*ConsumptionCost, 0, len(pairs))
	for _, p := range pairs {
		costs = append(costs, p.Cost)
	}
	existing := backfillExistingCostLogIDs(ctx, costs)

	costBatch := costs
	commBatch := make([]*EmployeeCommissionLog, 0, len(pairs))
	for _, p := range pairs {
		commBatch = append(commBatch, p.Commission)
	}

	if err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Select(
			"LogId", "UserId", "ChannelId", "ChannelName", "GroupName", "ModelName",
			"RevenueQuota", "CostQuota", "GroupRatio", "CostRatio", "CreatedAt",
		).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "log_id"}},
			DoNothing: true,
		}).CreateInBatches(costBatch, batchSize).Error; err != nil {
			return err
		}
		if err := tx.Select(
			"EmployeeId", "EmployeeUserId", "CustomerUserId", "LogId", "ModelName", "ChannelId",
			"RevenueQuota", "CostQuota", "ProfitQuota", "CommissionQuota", "CommissionRate",
			"CostRatio", "GroupRatio", "CreatedAt",
		).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "log_id"}},
			DoNothing: true,
		}).CreateInBatches(commBatch, batchSize).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		return 0, fmt.Errorf("BackfillFlushPairs: %w", err)
	}

	// Aggregate deltas after successful commit (into local maps, isolated from the
	// live buffers) and count only the pairs that were newly inserted, skipping
	// pairs ON CONFLICT left untouched.
	agg := newBackfillStatAggregator()
	inserted := 0
	for _, p := range pairs {
		if c := p.Cost; c != nil && c.LogId != nil {
			if _, dup := existing[*c.LogId]; dup {
				continue // pair was already in DB; ON CONFLICT skipped it
			}
		}
		if p.Cost != nil {
			agg.addPlatform(p.Cost)
		}
		if p.Commission != nil {
			agg.addCommission(p.Commission)
		}
		inserted++
	}
	agg.flush(ctx)
	return inserted, nil
}

// backfillExistingCostLogIDs returns the set of non-nil log_ids that already exist
// in the consumption_costs table for the given slice of records.  The result is
// used by BackfillFlushCosts and BackfillFlushPairs to skip aggregation for records
// that ON CONFLICT DO NOTHING would silently skip — preventing double-counting
// aggregation deltas on backfill re-runs.
//
// Records with nil LogId are excluded from the query; callers must treat them as
// always-new (they have no deduplication key).
func backfillExistingCostLogIDs(ctx context.Context, costs []*ConsumptionCost) map[int]struct{} {
	ids := make([]int, 0, len(costs))
	for _, c := range costs {
		if c != nil && c.LogId != nil && *c.LogId > 0 {
			ids = append(ids, *c.LogId)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var found []int
	DB.WithContext(ctx).Model(&ConsumptionCost{}).Where("log_id IN ?", ids).Pluck("log_id", &found)
	result := make(map[int]struct{}, len(found))
	for _, id := range found {
		result[id] = struct{}{}
	}
	return result
}

// GetCommissionRateSnapshotForDay returns the commission_rate from the most
// recent EmployeeCommissionLog for (employeeUserId, statDate) — i.e., the rate
// that was in effect on that business calendar day.
//
// statDate must be the localDayStart timestamp for the day in question.
// Returns (rate, true) if a record is found, (0, false) if none exists.
func GetCommissionRateSnapshotForDay(ctx context.Context, employeeUserId int, statDate int64) (float64, bool) {
	statDateEnd := statDate + 24*60*60 // exclusive upper bound: start of next day
	var log EmployeeCommissionLog
	err := DB.WithContext(ctx).
		Where("employee_user_id = ? AND created_at >= ? AND created_at < ?", employeeUserId, statDate, statDateEnd).
		Order("created_at DESC").
		Limit(1).
		Select("commission_rate").
		First(&log).Error
	if err != nil {
		return 0, false
	}
	return log.CommissionRate, true
}

// FallbackFileBasename is the basename used by the circuit-breaker writer and
// therefore used by the backfill reader to locate the correct file.
const FallbackFileBasename = businessStatsFallbackFile

// FallbackFilename returns the file name (not full path) for the given date.
func FallbackFilename(date string) string {
	return fallbackFilename(FallbackFileBasename, date)
}

// FallbackLogDir returns the directory where fallback files are written and read.
// Delegates to resolveFallbackDir so the writer and reader always agree on the path.
func FallbackLogDir() string {
	return resolveFallbackDir()
}

// BusinessDayDate formats a Unix timestamp as a "2006-01-02" date string in the
// configured business timezone.
func BusinessDayDate(ts int64) string {
	cfg := operation_setting.GetCommissionTierResetSetting()
	loc, _ := operation_setting.ResolveCommissionTierResetLocation(cfg.Timezone)
	return time.Unix(ts, 0).In(loc).Format("2006-01-02")
}
