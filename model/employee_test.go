package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// mkCommLog inserts a raw EmployeeCommissionLog (bypassing dedup/buffering).
func mkCommLog(t *testing.T, empUserId int, mut func(l *EmployeeCommissionLog)) *EmployeeCommissionLog {
	t.Helper()
	requireDB(t)
	l := &EmployeeCommissionLog{
		EmployeeUserId: empUserId,
		CustomerUserId: 0,
		ModelName:      "gpt-4o",
		ProfitQuota:    100,
		RevenueQuota:   100,
		CreatedAt:      time.Now().Unix(),
	}
	if mut != nil {
		mut(l)
	}
	require.NoError(t, DB.Create(l).Error)
	deleteByID(t, &EmployeeCommissionLog{}, l.Id)
	return l
}

// mkCoverage marks a day as covered in BusinessDailyStatsCoverage so plan-based
// queries read it from the daily-stat table instead of the ledger.
func mkCoverage(t *testing.T, statDate int64) {
	t.Helper()
	requireDB(t)
	// idempotent: skip if a coverage row for the day already exists.
	var existing BusinessDailyStatsCoverage
	if err := DB.Where("stat_date = ?", statDate).First(&existing).Error; err == nil {
		return
	}
	row := &BusinessDailyStatsCoverage{StatDate: statDate, CompletedAt: time.Now().Unix()}
	require.NoError(t, DB.Create(row).Error)
	deleteByID(t, &BusinessDailyStatsCoverage{}, row.Id)
}

// ---------------------------------------------------------------------------
// EmployeeProfile CRUD + cache
// ---------------------------------------------------------------------------

func TestEmployeeProfile_CreateAndLookup(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	emp := &EmployeeProfile{UserId: u.Id, Status: 1, Remark: uniq("r")}
	require.NoError(t, CreateEmployee(emp))
	deleteByID(t, &EmployeeProfile{}, emp.Id)
	t.Cleanup(func() {
		DB.Where("user_id = ?", u.Id).Delete(&UserExtension{})
		InvalidateEmployeeCache(u.Id)
	})
	require.NotZero(t, emp.Id)

	// GetEmployeeById
	got, err := GetEmployeeById(emp.Id)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, u.Id, got.UserId)

	// missing id -> (nil, nil)
	miss, err := GetEmployeeById(880_000_999)
	require.NoError(t, err)
	assert.Nil(t, miss)

	// GetEmployeeByUserId (+cache) and IsEmployee
	InvalidateEmployeeCache(u.Id)
	prof := GetEmployeeByUserId(u.Id)
	require.NotNil(t, prof)
	assert.True(t, IsEmployee(u.Id))
	// second call hits the cache path
	assert.NotNil(t, GetEmployeeByUserId(u.Id))

	// HasEmployeeProfile
	assert.True(t, HasEmployeeProfile(u.Id))
	assert.False(t, HasEmployeeProfile(880_000_998))

	// EnsureUserExtension was created by CreateEmployee
	var extCount int64
	require.NoError(t, DB.Model(&UserExtension{}).Where("user_id = ?", u.Id).Count(&extCount).Error)
	assert.EqualValues(t, 1, extCount)
}

func TestEmployeeProfile_UpdateDisableAndCache(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	emp := ctMkEmployeeProfile(t, u.Id, 1)

	// GetEmployeeByUserId caches the enabled profile.
	require.NotNil(t, GetEmployeeByUserId(u.Id))

	// UpdateEmployee (status stays 1, new remark) invalidates cache.
	emp.Remark = "updated"
	emp.Status = 1
	require.NoError(t, UpdateEmployee(emp))

	// DisableEmployee -> status 2 -> GetEmployeeByUserId (enabled-only) misses.
	require.NoError(t, DisableEmployee(emp.Id))
	assert.Nil(t, GetEmployeeByUserId(u.Id))
	assert.False(t, IsEmployee(u.Id))

	// HasEmployeeProfile still true (any status).
	assert.True(t, HasEmployeeProfile(u.Id))

	// UpdateEmployee on a missing row -> ErrRecordNotFound.
	err := UpdateEmployee(&EmployeeProfile{Id: 880_000_321})
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestGetEmployeeProfileStatusesByUserIds(t *testing.T) {
	requireDB(t)
	u1 := mkUser(t, nil)
	u2 := mkUser(t, nil)
	ctMkEmployeeProfile(t, u1.Id, 1)
	ctMkEmployeeProfile(t, u2.Id, 2)

	m, err := GetEmployeeProfileStatusesByUserIds([]int{u1.Id, u2.Id})
	require.NoError(t, err)
	assert.Equal(t, 1, m[u1.Id])
	assert.Equal(t, 2, m[u2.Id])

	// empty input short-circuits.
	empt, err := GetEmployeeProfileStatusesByUserIds(nil)
	require.NoError(t, err)
	assert.Empty(t, empt)
}

func TestGetAllEmployees_FilterAndSort(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(x *User) { x.Username = uniq("emp_search") })
	ctMkEmployeeProfile(t, u.Id, 1)

	// filter by user id -> exactly my row.
	rows, total, err := GetAllEmployees(1, 20, EmployeeFilter{UserId: u.Id})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	assert.Equal(t, u.Id, rows[0].UserId)

	// status filter (enabled) still returns it.
	rows, _, err = GetAllEmployees(1, 20, EmployeeFilter{UserId: u.Id, Status: 1})
	require.NoError(t, err)
	assert.Len(t, rows, 1)

	// a period-based sort exercises the reset-period rollup join without error.
	_, _, err = GetAllEmployees(1, 20, EmployeeFilter{UserId: u.Id, SortBy: "period_profit_quota", SortOrder: "asc"})
	require.NoError(t, err)
	_, _, err = GetAllEmployees(1, 20, EmployeeFilter{UserId: u.Id, SortBy: "current_commission_quota"})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// ChannelCostConfig + cost-ratio cache
// ---------------------------------------------------------------------------

func TestChannelCostConfig_CRUDAndCache(t *testing.T) {
	requireDB(t)
	ResetChannelCostCache()
	channelId := nextTestID()
	t.Cleanup(func() {
		DB.Where("channel_id = ?", channelId).Delete(&ChannelCostConfig{})
		ResetChannelCostCache()
	})

	// unconfigured -> default 1.0
	assert.Equal(t, 1.0, loadChannelCostRatioFromDB(channelId))
	assert.Equal(t, 1.0, GetChannelCostRatio(channelId))

	// upsert (create)
	require.NoError(t, UpsertChannelCostConfig(channelId, 0.6, "cheap"))
	assert.Equal(t, 0.6, loadChannelCostRatioFromDB(channelId))
	assert.Equal(t, 0.6, GetChannelCostRatio(channelId)) // cache invalidated on upsert

	// upsert (update existing)
	require.NoError(t, UpsertChannelCostConfig(channelId, 0.75, "less cheap"))
	assert.Equal(t, 0.75, GetChannelCostRatio(channelId))

	// GetAllChannelCostConfigs contains my channel.
	all, err := GetAllChannelCostConfigs()
	require.NoError(t, err)
	found := false
	for _, c := range all {
		if c.ChannelId == channelId {
			found = true
			assert.Equal(t, 0.75, c.CostRatio)
		}
	}
	assert.True(t, found)

	// delete -> back to default 1.0
	require.NoError(t, DeleteChannelCostConfig(channelId))
	assert.Equal(t, 1.0, GetChannelCostRatio(channelId))
}

func TestGetChannelCostRatio_Redis(t *testing.T) {
	enableRedis(t)
	requireDB(t)
	ResetChannelCostCache()
	channelId := nextTestID()
	t.Cleanup(func() {
		DB.Where("channel_id = ?", channelId).Delete(&ChannelCostConfig{})
		_ = common.RedisDelKey(channelCostRedisKey(channelId))
		ResetChannelCostCache()
	})

	assert.Contains(t, channelCostRedisKey(channelId), "channel_cost_ratio:")
	require.NoError(t, UpsertChannelCostConfig(channelId, 0.55, "r"))
	// first read populates Redis from DB; second read serves from Redis.
	assert.Equal(t, 0.55, GetChannelCostRatio(channelId))
	ResetChannelCostCache() // drop L1 so the Redis branch is exercised
	assert.Equal(t, 0.55, GetChannelCostRatio(channelId))
}

// ---------------------------------------------------------------------------
// CreateCommissionLog — log_id idempotency / dedup
// ---------------------------------------------------------------------------

func TestCreateCommissionLog_Idempotency(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	empCleanupStats(t, u.Id)
	t.Cleanup(func() { FlushBusinessStatBuffers() }) // drain mem buffers before row cleanup

	logId := nextTestID()
	base := func() *EmployeeCommissionLog {
		return &EmployeeCommissionLog{
			EmployeeUserId: u.Id,
			CustomerUserId: 0,
			LogId:          &logId,
			ModelName:      "gpt-4o",
			ProfitQuota:    100,
			RevenueQuota:   150,
			CostQuota:      50,
			CreatedAt:      time.Now().Unix(),
		}
	}

	inserted, err := CreateCommissionLog(base())
	require.NoError(t, err)
	assert.True(t, inserted, "first insert with a fresh log_id")

	// duplicate log_id -> deduped, not inserted.
	inserted, err = CreateCommissionLog(base())
	require.NoError(t, err)
	assert.False(t, inserted, "same log_id must be deduped")

	var count int64
	require.NoError(t, DB.Model(&EmployeeCommissionLog{}).Where("log_id = ?", logId).Count(&count).Error)
	assert.EqualValues(t, 1, count, "only one ledger row for the log_id")

	// log_id nil -> no idempotency key, always inserted.
	nilLog := base()
	nilLog.LogId = nil
	inserted, err = CreateCommissionLog(nilLog)
	require.NoError(t, err)
	assert.True(t, inserted)
}

// ---------------------------------------------------------------------------
// GetCommissionLogs + filters (ledger-count path)
// ---------------------------------------------------------------------------

func TestGetCommissionLogs_Filters(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	empCleanupStats(t, u.Id)
	model := uniq("mdl")
	now := time.Now().Unix()

	// two normal + one loss row, all with a unique model name.
	mkCommLog(t, u.Id, func(l *EmployeeCommissionLog) {
		l.ModelName = model
		l.ChannelId = 71
		l.ProfitQuota = 100
		l.CreatedAt = now
	})
	mkCommLog(t, u.Id, func(l *EmployeeCommissionLog) {
		l.ModelName = model
		l.ChannelId = 71
		l.ProfitQuota = 200
		l.CreatedAt = now
	})
	mkCommLog(t, u.Id, func(l *EmployeeCommissionLog) {
		l.ModelName = model
		l.ChannelId = 72
		l.ProfitQuota = -30 // loss
		l.CreatedAt = now
	})

	// model filter forces ledger count (canCountFromDaily=false).
	logs, total, err := GetCommissionLogs(CommissionLogFilter{
		EmployeeUserId: u.Id, ModelName: model, Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	assert.Len(t, logs, 3)

	// channel filter.
	_, total, err = GetCommissionLogs(CommissionLogFilter{
		EmployeeUserId: u.Id, ChannelId: 71, ModelName: model, Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)

	// loss-status filter.
	_, total, err = GetCommissionLogs(CommissionLogFilter{
		EmployeeUserId: u.Id, ModelName: model, LossStatus: "loss", Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)

	// normal-status filter.
	_, total, err = GetCommissionLogs(CommissionLogFilter{
		EmployeeUserId: u.Id, ModelName: model, LossStatus: "normal", Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)

	// time range excluding all rows.
	_, total, err = GetCommissionLogs(CommissionLogFilter{
		EmployeeUserId: u.Id, ModelName: model, StartTime: now + 1000, EndTime: now + 2000, Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 0, total)
}

// GetCommissionLogs with no detail filters uses the daily-stat count path.
// (The time-range branch delegates to the business_daily_stats plan machinery,
// which classifies whole days as covered=daily vs uncovered=ledger using UTC day
// boundaries; here we assert the wrappers execute, plus the exact zero-range sum.)
func TestGetCommissionLogs_DailyCountPath(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	empCleanupStats(t, u.Id)
	today := localDayStart(time.Now().Unix())
	rows := []EmployeeCommissionDailyStat{
		{StatDate: today - 86400, EmployeeUserId: u.Id, ProfitQuota: 10, RecordCount: 4},
		{StatDate: today, EmployeeUserId: u.Id, ProfitQuota: 20, RecordCount: 6},
	}
	for i := range rows {
		require.NoError(t, DB.Create(&rows[i]).Error)
		deleteByID(t, &EmployeeCommissionDailyStat{}, rows[i].Id)
	}
	mkCoverage(t, today-86400)
	mkCoverage(t, today)
	// a ledger row in the same window (counted by the ledger sub-range).
	mkCommLog(t, u.Id, func(l *EmployeeCommissionLog) { l.CreatedAt = time.Now().Unix() })

	// no time range -> count = SUM(record_count) from daily stats (4+6).
	_, total, err := GetCommissionLogs(CommissionLogFilter{EmployeeUserId: u.Id, Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.EqualValues(t, 10, total)

	// with a recent time range -> plan-based daily + ledger count wrappers run.
	_, total, err = GetCommissionLogs(CommissionLogFilter{
		EmployeeUserId: u.Id, StartTime: today - 2*86400, EndTime: time.Now().Unix(), Page: 1, PageSize: 10,
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, int64(1))
}

func TestGetCommissionTotals(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	empCleanupStats(t, u.Id)
	today := localDayStart(time.Now().Unix())
	row := EmployeeCommissionDailyStat{StatDate: today, EmployeeUserId: u.Id, RevenueQuota: 500, CostQuota: 200, ProfitQuota: 300, CommissionQuota: 30, RecordCount: 3}
	require.NoError(t, DB.Create(&row).Error)
	deleteByID(t, &EmployeeCommissionDailyStat{}, row.Id)
	mkCoverage(t, today)

	now := time.Now().Unix()
	mkCommLog(t, u.Id, func(l *EmployeeCommissionLog) { l.CreatedAt = now })

	// Deprecated aggregate over a recent window (daily covered + ledger uncovered).
	_, err := GetCommissionTotals(today-2*86400, now)
	require.NoError(t, err)

	// full query plan exercises the ledger-range branch without error.
	plan, err := ResolveBusinessStatsQueryPlan(now-2*86400, now)
	require.NoError(t, err)
	_, err = GetCommissionTotalsWithPlan(plan)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Reset-period stats aggregation
// ---------------------------------------------------------------------------

func TestGetCommissionResetPeriodStats(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	empCleanupStats(t, u.Id)
	const R = int64(1_600_700_000)
	// two rows in the same bucket -> summed.
	ctMkResetStat(t, R, R, u.Id, 100, 40, 60, 6, 1)
	ctMkResetStat(t, R, R+86400, u.Id, 200, 80, 120, 12, 2)

	items, total, err := GetCommissionResetPeriodStats(CommissionResetPeriodStatFilter{
		EmployeeUserId: u.Id, ResetStartedAt: R, Page: 1, PageSize: 20,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	assert.EqualValues(t, 180, items[0].ProfitQuota) // 60+120
	assert.EqualValues(t, 300, items[0].RevenueQuota)
	assert.EqualValues(t, 3, items[0].RecordCount)
	assert.InDelta(t, common.QuotaToUSD(180), items[0].TotalProfitUsd, 1e-9)

	// pagination past the end returns empty with the same total.
	items, total, err = GetCommissionResetPeriodStats(CommissionResetPeriodStatFilter{
		EmployeeUserId: u.Id, ResetStartedAt: R, Page: 5, PageSize: 20,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	assert.Empty(t, items)
}

func TestGetCurrentResetPeriodStatsByEmployeeUserIds(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	empCleanupStats(t, u.Id)
	const B = int64(1_600_800_000)
	ctMkTierLevel(t, u.Id, 0, B) // baseline anchors the current bucket to B
	ctMkResetStat(t, B, B, u.Id, 500, 200, 300, 30, 5)

	m, err := GetCurrentResetPeriodStatsByEmployeeUserIds([]int{u.Id})
	require.NoError(t, err)
	require.Contains(t, m, u.Id)
	assert.EqualValues(t, 300, m[u.Id].ProfitQuota)
	assert.EqualValues(t, B, m[u.Id].ResetStartedAt)
	assert.EqualValues(t, 5, m[u.Id].RecordCount)

	// empty input short-circuits.
	empt, err := GetCurrentResetPeriodStatsByEmployeeUserIds(nil)
	require.NoError(t, err)
	assert.Empty(t, empt)
}

// ---------------------------------------------------------------------------
// Calendar stats (current period, single employee)
// ---------------------------------------------------------------------------

func TestGetCommissionCalendarStats_CurrentSingleEmployee(t *testing.T) {
	requireDB(t)
	grp := uniq("cal")
	tier := ctMkTier(t, grp, 1, 0, 0.20)

	now := time.Now().Unix()
	statDate := localDayStart(now)
	u := mkUser(t, nil)
	ctMkEmployeeProfile(t, u.Id, 1)
	empCleanupStats(t, u.Id)
	const B = int64(1_600_000_000)
	ctMkTierLevel(t, u.Id, tier.Id, B)
	ctMkResetStat(t, B, statDate, u.Id, usdToQuota(10), 0, usdToQuota(10), 0, 4)

	stats, err := GetCommissionCalendarStats(now, now, u.Id)
	require.NoError(t, err)
	require.NotNil(t, stats)
	assert.False(t, stats.IsHistorical)
	assert.EqualValues(t, usdToQuota(10), stats.Summary.ProfitQuota)
	assert.EqualValues(t, 4, stats.Summary.RecordCount)
	// recalc = profit * current tier rate.
	assert.EqualValues(t, int64(float64(usdToQuota(10))*0.20), stats.Summary.RecalcCommissionQuota)
	assert.Equal(t, tier.Level, stats.EmployeeTierLevel)
	assert.Equal(t, tier.Rate, stats.EmployeeTierRate)

	// invalid range -> empty stats, no error.
	empty, err := GetCommissionCalendarStats(0, 0, u.Id)
	require.NoError(t, err)
	assert.Empty(t, empty.Days)
	empty, err = GetCommissionCalendarStats(now, now-10, u.Id)
	require.NoError(t, err)
	assert.Empty(t, empty.Days)
}

// ---------------------------------------------------------------------------
// Historical tier resolution from tier logs
// ---------------------------------------------------------------------------

func TestResolveEmployeeTiersAt(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	empCleanupStats(t, u.Id)

	// two tier changes: [t=100] 5->10 ; [t=200] 10->20
	mkTierLog := func(operatedAt, from, to int64) {
		l := &EmployeeTierLog{UserId: u.Id, FromTierId: from, ToTierId: to, Source: "auto", OperatedAt: operatedAt}
		require.NoError(t, DB.Create(l).Error)
		deleteByID64(t, &EmployeeTierLog{}, l.Id)
	}
	mkTierLog(100, 5, 10)
	mkTierLog(200, 10, 20)

	// after the last change -> latest to_tier_id.
	assert.EqualValues(t, 20, resolveEmployeeTierAt(u.Id, 250))
	// between changes -> the change in effect at that time.
	assert.EqualValues(t, 10, resolveEmployeeTierAt(u.Id, 150))
	// before the first change -> earliest from_tier_id.
	assert.EqualValues(t, 5, resolveEmployeeTierAt(u.Id, 50))

	// empty input.
	assert.Empty(t, resolveEmployeeTiersAt(nil, 100))
}

// ---------------------------------------------------------------------------
// Customer commission totals
// ---------------------------------------------------------------------------

func TestGetCustomerCommissionTotals(t *testing.T) {
	requireDB(t)
	emp := mkUser(t, nil)
	cust := mkUser(t, nil)
	t.Cleanup(func() {
		DB.Where("employee_user_id = ?", emp.Id).Delete(&EmployeeCustomerCommissionDailyStat{})
	})

	rows := []EmployeeCustomerCommissionDailyStat{
		{StatDate: 1_600_000_000, EmployeeUserId: emp.Id, CustomerUserId: cust.Id, CommissionQuota: 40, RecordCount: 1},
		{StatDate: 1_600_086_400, EmployeeUserId: emp.Id, CustomerUserId: cust.Id, CommissionQuota: 60, RecordCount: 1},
	}
	for i := range rows {
		require.NoError(t, DB.Create(&rows[i]).Error)
		deleteByID(t, &EmployeeCustomerCommissionDailyStat{}, rows[i].Id)
	}

	m, err := GetCustomerCommissionTotals(emp.Id, []int{cust.Id})
	require.NoError(t, err)
	assert.EqualValues(t, 100, m[cust.Id]) // 40+60

	// single-customer convenience wrapper.
	assert.EqualValues(t, 100, GetCustomerCommissionTotal(emp.Id, cust.Id))

	// guards: zero employee id / empty customers -> empty map.
	empt, err := GetCustomerCommissionTotals(0, []int{cust.Id})
	require.NoError(t, err)
	assert.Empty(t, empt)
	empt, err = GetCustomerCommissionTotals(emp.Id, nil)
	require.NoError(t, err)
	assert.Empty(t, empt)
}

// ---------------------------------------------------------------------------
// Per-employee daily-stat rollup
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Plan-based commission totals / per-employee stats (daily-stat aggregation)
// ---------------------------------------------------------------------------

func TestGetCommissionTotalsByEmployeeAndStats(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	empCleanupStats(t, u.Id)
	d1 := localDayStart(1_600_000_000)
	d2 := d1 + 86400
	rows := []EmployeeCommissionDailyStat{
		{StatDate: d1, EmployeeUserId: u.Id, RevenueQuota: 100, CostQuota: 40, ProfitQuota: 60, CommissionQuota: 6, RecordCount: 1},
		{StatDate: d2, EmployeeUserId: u.Id, RevenueQuota: 200, CostQuota: 80, ProfitQuota: 120, CommissionQuota: 12, RecordCount: 2},
	}
	for i := range rows {
		require.NoError(t, DB.Create(&rows[i]).Error)
		deleteByID(t, &EmployeeCommissionDailyStat{}, rows[i].Id)
	}

	// GetCommissionTotalsByEmployee scopes to the employee across full bounds.
	totals, err := GetCommissionTotalsByEmployee(u.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 180, totals.TotalProfit)
	assert.EqualValues(t, 300, totals.TotalRevenue)
	assert.EqualValues(t, 18, totals.TotalCommission)
	assert.EqualValues(t, 3, totals.RecordCount)

	// GetCommissionStatsByEmployee (plan-based) must contain my employee.
	stats, err := GetCommissionStatsByEmployee(0, 0, 0)
	require.NoError(t, err)
	var mine *CommissionEmployeeStat
	for _, s := range stats {
		if s.EmployeeUserId == u.Id {
			mine = s
		}
	}
	require.NotNil(t, mine)
	assert.EqualValues(t, 180, mine.TotalProfit)

	// GetCommissionSummary mirrors the same rollup.
	summary, err := GetCommissionSummary(0, 0)
	require.NoError(t, err)
	var found bool
	for _, s := range summary {
		if s.EmployeeUserId == u.Id {
			found = true
			assert.EqualValues(t, 180, s.TotalProfit)
		}
	}
	assert.True(t, found)

	// GetCommissionTotals over a plan sums across employees -> at least my share.
	plan, err := ResolveBusinessStatsDailyOnlyQueryPlan(d1, d2)
	require.NoError(t, err)
	planTotals, err := GetCommissionTotalsWithPlan(plan)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, planTotals.TotalProfit, int64(180))
}

func TestGetCommissionStatsByEmployeeIds(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	empCleanupStats(t, u.Id)
	rows := []EmployeeCommissionDailyStat{
		{StatDate: 1_600_000_000, EmployeeUserId: u.Id, RevenueQuota: 100, CostQuota: 40, ProfitQuota: 60, CommissionQuota: 6, RecordCount: 1},
		{StatDate: 1_600_086_400, EmployeeUserId: u.Id, RevenueQuota: 200, CostQuota: 80, ProfitQuota: 120, CommissionQuota: 12, RecordCount: 2},
	}
	for i := range rows {
		require.NoError(t, DB.Create(&rows[i]).Error)
		deleteByID(t, &EmployeeCommissionDailyStat{}, rows[i].Id)
	}

	stats, err := GetCommissionStatsByEmployeeIds([]int{u.Id})
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, u.Id, stats[0].EmployeeUserId)
	assert.EqualValues(t, 180, stats[0].TotalProfit) // 60+120
	assert.EqualValues(t, 300, stats[0].TotalRevenue)
	assert.EqualValues(t, 18, stats[0].TotalCommission)
	assert.EqualValues(t, 3, stats[0].RecordCount)
}
