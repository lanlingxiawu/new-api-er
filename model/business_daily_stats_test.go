package model

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// business_daily_stats.go — pure logic
// ---------------------------------------------------------------------------

func TestUnixDayStartEnd(t *testing.T) {
	// A known UTC day boundary: 1_700_000_000 = 2023-11-14T22:13:20Z.
	const ts = int64(1_700_000_000)
	start := unixDayStart(ts)
	end := unixDayEnd(ts)
	assert.Equal(t, ts/businessStatsDaySeconds*businessStatsDaySeconds, start)
	assert.Equal(t, start+businessStatsDaySeconds-1, end)
	assert.LessOrEqual(t, start, ts)
	assert.Less(t, ts, start+businessStatsDaySeconds)
	// idempotent on an already-aligned value
	assert.Equal(t, start, unixDayStart(start))
	assert.Equal(t, start+businessStatsDaySeconds-1, unixDayEnd(start))
}

func TestLocalDayStart(t *testing.T) {
	const ts = int64(1_700_000_000)
	d := localDayStart(ts)
	assert.LessOrEqual(t, d, ts)
	assert.Less(t, ts, d+businessStatsDaySeconds)
	// idempotent
	assert.Equal(t, d, localDayStart(d))
}

func TestCommissionMonthHelpers(t *testing.T) {
	// previous / next month wrap-around
	y, m := previousCommissionMonth(2024, time.January)
	assert.Equal(t, 2023, y)
	assert.Equal(t, time.December, m)
	y, m = previousCommissionMonth(2024, time.March)
	assert.Equal(t, 2024, y)
	assert.Equal(t, time.February, m)

	y, m = nextCommissionMonth(2024, time.December)
	assert.Equal(t, 2025, y)
	assert.Equal(t, time.January, m)
	y, m = nextCommissionMonth(2024, time.March)
	assert.Equal(t, 2024, y)
	assert.Equal(t, time.April, m)

	// days in month, including leap February
	assert.Equal(t, 31, commissionResetDaysInMonth(2024, time.January))
	assert.Equal(t, 29, commissionResetDaysInMonth(2024, time.February)) // leap
	assert.Equal(t, 28, commissionResetDaysInMonth(2023, time.February))
	assert.Equal(t, 30, commissionResetDaysInMonth(2024, time.April))
}

func TestResolveCommissionMonthlyPeriod(t *testing.T) {
	now := time.Now().Unix()
	p := ResolveCommissionMonthlyPeriod(now)
	assert.LessOrEqual(t, p.PeriodStartAt, now)
	assert.GreaterOrEqual(t, p.PeriodEndAt, now)
	assert.Greater(t, p.PeriodEndAt, p.PeriodStartAt)
	assert.NotEmpty(t, p.PeriodKey)
	assert.NotEmpty(t, p.Timezone)
}

func TestResolveNextCommissionPeriodBoundary(t *testing.T) {
	now := time.Now().Unix()
	p := ResolveNextCommissionPeriodBoundary(now)
	assert.Equal(t, now, p.PeriodStartAt)
	assert.Greater(t, p.PeriodEndAt, now)
}

func TestSplitDailyAggregatePlan(t *testing.T) {
	day := businessStatsDaySeconds

	t.Run("both zero -> aggregate all, no detail", func(t *testing.T) {
		plan := splitDailyAggregatePlan(0, 0)
		assert.True(t, plan.HasAggregate)
		assert.Empty(t, plan.DetailRanges)
	})

	t.Run("start aligned, end zero", func(t *testing.T) {
		startDay := int64(1_700_000_000) / day * day
		plan := splitDailyAggregatePlan(startDay, 0)
		assert.True(t, plan.HasAggregate)
		assert.Equal(t, startDay, plan.AggregateStartDate)
		assert.Empty(t, plan.DetailRanges)
	})

	t.Run("start mid-day produces a leading detail range", func(t *testing.T) {
		startDay := int64(1_700_000_000) / day * day
		start := startDay + 3600 // 1h into the day
		plan := splitDailyAggregatePlan(start, 0)
		require.Len(t, plan.DetailRanges, 1)
		assert.Equal(t, start, plan.DetailRanges[0].StartTime)
		assert.Equal(t, startDay+day-1, plan.DetailRanges[0].EndTime)
		// aggregate begins the next whole day
		assert.Equal(t, startDay+day, plan.AggregateStartDate)
	})

	t.Run("start and end within the same partial day -> detail only, no aggregate", func(t *testing.T) {
		startDay := int64(1_700_000_000) / day * day
		start := startDay + 3600
		end := startDay + 7200
		plan := splitDailyAggregatePlan(start, end)
		require.Len(t, plan.DetailRanges, 1)
		assert.Equal(t, start, plan.DetailRanges[0].StartTime)
		assert.Equal(t, end, plan.DetailRanges[0].EndTime)
		assert.False(t, plan.HasAggregate)
	})

	t.Run("end mid-day produces a trailing detail range", func(t *testing.T) {
		endDay := int64(1_700_000_000) / day * day
		end := endDay + 3600
		plan := splitDailyAggregatePlan(0, end)
		require.NotEmpty(t, plan.DetailRanges)
		last := plan.DetailRanges[len(plan.DetailRanges)-1]
		assert.Equal(t, endDay, last.StartTime)
		assert.Equal(t, end, last.EndTime)
	})
}

func TestNormalizeStatsPagination(t *testing.T) {
	cases := []struct {
		page, size            int
		wantPage, wantSize, wantOffset int
	}{
		{0, 0, 1, 20, 0},     // page<1 -> 1 ; size<1 -> 20
		{1, 20, 1, 20, 0},    // valid
		{3, 10, 3, 10, 20},   // offset (3-1)*10
		{2, 101, 2, 20, 20},  // size>100 -> 20 ; offset (2-1)*20
		{-5, -5, 1, 20, 0},   // negatives
		{1, 100, 1, 100, 0},  // size==100 boundary kept
	}
	for _, c := range cases {
		p, s, off := normalizeStatsPagination(c.page, c.size)
		assert.Equal(t, c.wantPage, p)
		assert.Equal(t, c.wantSize, s)
		assert.Equal(t, c.wantOffset, off)
	}
}

func TestIsDailyOnlyPlan(t *testing.T) {
	assert.False(t, isDailyOnlyPlan(nil))
	// covered but with uncovered -> not daily-only
	assert.False(t, isDailyOnlyPlan(&ResolvedQueryPlan{
		CoveredRanges:   []statDateRange{{1, 2}},
		UncoveredRanges: []statDateRange{{3, 4}},
	}))
	// covered, with detail -> not daily-only
	assert.False(t, isDailyOnlyPlan(&ResolvedQueryPlan{
		CoveredRanges: []statDateRange{{1, 2}},
		DetailRanges:  []statsTimeRange{{3, 4}},
	}))
	// no covered ranges -> not daily-only
	assert.False(t, isDailyOnlyPlan(&ResolvedQueryPlan{}))
	// covered only -> daily-only
	assert.True(t, isDailyOnlyPlan(&ResolvedQueryPlan{
		CoveredRanges: []statDateRange{{1, 2}},
	}))
}

func TestChannelSortExpression(t *testing.T) {
	assert.Equal(t, "channel_name", channelSortExpression("channel_name"))
	assert.Equal(t, "cost_ratio", channelSortExpression("cost_ratio"))
	assert.Equal(t, "total_revenue", channelSortExpression("consumption_quota"))
	assert.Equal(t, "total_cost", channelSortExpression("est_cost_quota"))
	assert.Equal(t, "gross_margin", channelSortExpression("est_gross_margin"))
	assert.Equal(t, "est_profit", channelSortExpression("anything_else"))
}

func TestSortConsumptionCostChannels(t *testing.T) {
	mk := func() []*ConsumptionCostChannelStat {
		return []*ConsumptionCostChannelStat{
			{ChannelId: 1, ChannelName: "b", TotalRevenue: 100, TotalCost: 40, CostRatio: 0.4},
			{ChannelId: 2, ChannelName: "a", TotalRevenue: 300, TotalCost: 250, CostRatio: 0.8},
			{ChannelId: 3, ChannelName: "c", TotalRevenue: 50, TotalCost: 60, CostRatio: 1.2},
		}
	}

	// default (est_profit) desc: profits 60, 50, -10 -> ids 1,2,3
	items := mk()
	sortConsumptionCostChannels(items, "", "desc")
	assert.Equal(t, []int{1, 2, 3}, idsOf(items))

	// est_profit asc
	items = mk()
	sortConsumptionCostChannels(items, "est_profit", "asc")
	assert.Equal(t, []int{3, 2, 1}, idsOf(items))

	// channel_name asc: a,b,c -> 2,1,3
	items = mk()
	sortConsumptionCostChannels(items, "channel_name", "asc")
	assert.Equal(t, []int{2, 1, 3}, idsOf(items))

	// cost_ratio desc: 1.2,0.8,0.4 -> 3,2,1
	items = mk()
	sortConsumptionCostChannels(items, "cost_ratio", "desc")
	assert.Equal(t, []int{3, 2, 1}, idsOf(items))

	// consumption_quota (revenue) desc: 300,100,50 -> 2,1,3
	items = mk()
	sortConsumptionCostChannels(items, "consumption_quota", "desc")
	assert.Equal(t, []int{2, 1, 3}, idsOf(items))

	// est_cost_quota desc: 250,60,40 -> 2,3,1
	items = mk()
	sortConsumptionCostChannels(items, "est_cost_quota", "desc")
	assert.Equal(t, []int{2, 3, 1}, idsOf(items))

	// est_gross_margin desc: margins 0.6, 0.166, -0.2 -> 1,2,3
	items = mk()
	sortConsumptionCostChannels(items, "est_gross_margin", "desc")
	assert.Equal(t, []int{1, 2, 3}, idsOf(items))
}

func idsOf(items []*ConsumptionCostChannelStat) []int {
	out := make([]int, len(items))
	for i, it := range items {
		out[i] = it.ChannelId
	}
	return out
}

func TestMergeConsumptionCostChannelStats(t *testing.T) {
	dst := map[int]*ConsumptionCostChannelStat{}
	mergeConsumptionCostChannelStats(dst, []*ConsumptionCostChannelStat{
		{ChannelId: 7, TotalRevenue: 100, TotalCost: 40, RecordCount: 2, CostRatioSum: 0.8, ChannelName: "seven"},
	})
	mergeConsumptionCostChannelStats(dst, []*ConsumptionCostChannelStat{
		{ChannelId: 7, TotalRevenue: 50, TotalCost: 10, RecordCount: 1, CostRatioSum: 0.2, ChannelName: "ignored"},
	})
	got := dst[7]
	require.NotNil(t, got)
	assert.EqualValues(t, 150, got.TotalRevenue)
	assert.EqualValues(t, 50, got.TotalCost)
	assert.EqualValues(t, 3, got.RecordCount)
	assert.InDelta(t, 1.0, got.CostRatioSum, 1e-9)
	// first non-empty name is kept
	assert.Equal(t, "seven", got.ChannelName)
}

func TestFinalizeConsumptionCostChannelStat(t *testing.T) {
	// average of the ratio sum
	item := &ConsumptionCostChannelStat{RecordCount: 4, CostRatioSum: 2.0}
	finalizeConsumptionCostChannelStat(item)
	assert.InDelta(t, 0.5, item.CostRatio, 1e-9)

	// zero records -> default ratio
	item = &ConsumptionCostChannelStat{RecordCount: 0, CostRatioSum: 5.0}
	finalizeConsumptionCostChannelStat(item)
	assert.InDelta(t, channelCostDefaultRatio, item.CostRatio, 1e-9)
}

func TestMergeCommissionEmployeeStats(t *testing.T) {
	dst := map[int]*CommissionEmployeeStat{}
	mergeCommissionEmployeeStats(dst, []*CommissionEmployeeStat{
		{EmployeeUserId: 5, TotalRevenue: 100, TotalCost: 40, TotalProfit: 60, TotalCommission: 6, RecordCount: 1},
	})
	mergeCommissionEmployeeStats(dst, []*CommissionEmployeeStat{
		{EmployeeUserId: 5, TotalRevenue: 200, TotalCost: 80, TotalProfit: 120, TotalCommission: 12, RecordCount: 2},
	})
	got := dst[5]
	require.NotNil(t, got)
	assert.EqualValues(t, 300, got.TotalRevenue)
	assert.EqualValues(t, 120, got.TotalCost)
	assert.EqualValues(t, 180, got.TotalProfit)
	assert.EqualValues(t, 18, got.TotalCommission)
	assert.EqualValues(t, 3, got.RecordCount)
}

func TestFallbackChannelDisplayName(t *testing.T) {
	assert.Equal(t, "", fallbackChannelDisplayName(0))
	assert.Equal(t, "", fallbackChannelDisplayName(-3))
	assert.Equal(t, "#42", fallbackChannelDisplayName(42))
}

func TestSafeDBContext(t *testing.T) {
	assert.Equal(t, context.Background(), safeDBContext(nil))
	ctx := context.WithValue(context.Background(), struct{}{}, 1)
	assert.Equal(t, ctx, safeDBContext(ctx))
}

// ---------------------------------------------------------------------------
// business_daily_stats.go — DB-backed
// ---------------------------------------------------------------------------

// bsFarFutureDay returns a unique, far-future stat_date aligned to a local
// business day (localDayStart, Asia/Shanghai by default) so that a record whose
// created_at falls inside the day aggregates to exactly this value. Using
// localDayStart keeps the helper correct regardless of the configured timezone.
func bsFarFutureDay(t *testing.T) int64 {
	t.Helper()
	base := int64(4_200_000_000) // ≈ 2103
	off := int64(nextTestID()-testIDBase) * businessStatsDaySeconds
	return localDayStart(base + off)
}

func cleanupPlatformDailyRow(t *testing.T, statDate int64, channelId int) {
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Where("stat_date = ? AND channel_id = ?", statDate, channelId).Delete(&PlatformChannelDailyStat{})
		}
	})
}

func cleanupCoverageRow(t *testing.T, statDate int64) {
	t.Cleanup(func() {
		coveredDaysCache.Delete(statDate)
		if DB != nil {
			DB.Unscoped().Where("stat_date = ?", statDate).Delete(&BusinessDailyStatsCoverage{})
		}
	})
}

func TestAddPlatformChannelDailyStatTx(t *testing.T) {
	requireDB(t)
	statDate := bsFarFutureDay(t)
	chId := nextTestID()
	// use a created_at inside the day
	createdAt := statDate + 100
	cleanupPlatformDailyRow(t, statDate, chId)
	cleanupCoverageRow(t, statDate)
	coveredDaysCache.Delete(statDate) // force the coverage upsert path

	rec1 := &ConsumptionCost{ChannelId: chId, ChannelName: "chan-x", RevenueQuota: 100, CostQuota: 40, CostRatio: 0.8, CreatedAt: createdAt}
	rec2 := &ConsumptionCost{ChannelId: chId, ChannelName: "", RevenueQuota: 50, CostQuota: 10, CostRatio: 0.2, CreatedAt: createdAt + 10}

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		if err := addPlatformChannelDailyStatTx(tx, rec1); err != nil {
			return err
		}
		return addPlatformChannelDailyStatTx(tx, rec2)
	}))

	var row PlatformChannelDailyStat
	require.NoError(t, DB.Where("stat_date = ? AND channel_id = ?", statDate, chId).First(&row).Error)
	// localDayStart(createdAt) must equal statDate for these to aggregate — assert it.
	require.Equal(t, statDate, localDayStart(createdAt))
	assert.EqualValues(t, 150, row.RevenueQuota)
	assert.EqualValues(t, 50, row.CostQuota)
	assert.EqualValues(t, 2, row.RecordCount)
	assert.InDelta(t, 1.0, row.CostRatioSum, 1e-9)
	assert.Equal(t, "chan-x", row.ChannelName) // COALESCE(NULLIF) keeps first non-empty
	assert.Equal(t, createdAt+10, row.LastCreatedAt)

	// coverage row was written
	var cov BusinessDailyStatsCoverage
	require.NoError(t, DB.Where("stat_date = ?", statDate).First(&cov).Error)
	assert.Equal(t, statDate, cov.StatDate)
}

func TestAddEmployeeCommissionDailyStatTx(t *testing.T) {
	requireDB(t)
	statDate := bsFarFutureDay(t)
	empId := nextTestID()
	createdAt := statDate + 100
	t.Cleanup(func() {
		DB.Unscoped().Where("stat_date = ? AND employee_user_id = ?", statDate, empId).Delete(&EmployeeCommissionDailyStat{})
	})

	log1 := &EmployeeCommissionLog{EmployeeUserId: empId, RevenueQuota: 100, CostQuota: 40, ProfitQuota: 60, CommissionQuota: 6, CreatedAt: createdAt}
	log2 := &EmployeeCommissionLog{EmployeeUserId: empId, RevenueQuota: 200, CostQuota: 80, ProfitQuota: 120, CommissionQuota: 12, CreatedAt: createdAt + 5}

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		if err := addEmployeeCommissionDailyStatTx(tx, log1); err != nil {
			return err
		}
		return addEmployeeCommissionDailyStatTx(tx, log2)
	}))

	var row EmployeeCommissionDailyStat
	require.NoError(t, DB.Where("stat_date = ? AND employee_user_id = ?", statDate, empId).First(&row).Error)
	assert.EqualValues(t, 300, row.RevenueQuota)
	assert.EqualValues(t, 120, row.CostQuota)
	assert.EqualValues(t, 180, row.ProfitQuota)
	assert.EqualValues(t, 18, row.CommissionQuota)
	assert.EqualValues(t, 2, row.RecordCount)
	assert.Equal(t, createdAt+5, row.LastCreatedAt)
}

func TestEnsureDailyCoverageTx_DedupCache(t *testing.T) {
	requireDB(t)
	statDate := bsFarFutureDay(t)
	cleanupCoverageRow(t, statDate)
	coveredDaysCache.Delete(statDate)

	// first call inserts
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ensureDailyCoverageTx(tx, statDate)
	}))
	var count int64
	require.NoError(t, DB.Model(&BusinessDailyStatsCoverage{}).Where("stat_date = ?", statDate).Count(&count).Error)
	assert.EqualValues(t, 1, count)

	// second call hits the process cache and is a no-op (still 1 row)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ensureDailyCoverageTx(tx, statDate)
	}))
	require.NoError(t, DB.Model(&BusinessDailyStatsCoverage{}).Where("stat_date = ?", statDate).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestGetStatDateBounds(t *testing.T) {
	requireDB(t)
	// insert two platform rows; the larger stat_date should be the global MAX
	// because it is a far-future value beyond any real data.
	d1 := bsFarFutureDay(t)
	d2 := d1 + 5*businessStatsDaySeconds
	ch := nextTestID()
	cleanupPlatformDailyRow(t, d1, ch)
	cleanupPlatformDailyRow(t, d2, ch)
	require.NoError(t, DB.Create(&PlatformChannelDailyStat{StatDate: d1, ChannelId: ch, RevenueQuota: 1}).Error)
	require.NoError(t, DB.Create(&PlatformChannelDailyStat{StatDate: d2, ChannelId: ch, RevenueQuota: 1}).Error)

	minDate, maxDate, err := getStatDateBounds(&PlatformChannelDailyStat{})
	require.NoError(t, err)
	assert.Equal(t, d2, maxDate) // my far-future row is the global max
	// minDate is the global minimum across the whole table (may be <=0 for
	// pre-existing rows); it must not exceed my smaller inserted day.
	assert.LessOrEqual(t, minDate, d1)
}

func TestGetConsumptionCostByChannelFromDaily(t *testing.T) {
	requireDB(t)
	d1 := bsFarFutureDay(t)
	d2 := d1 + businessStatsDaySeconds
	ch := nextTestID()
	cleanupPlatformDailyRow(t, d1, ch)
	cleanupPlatformDailyRow(t, d2, ch)
	require.NoError(t, DB.Create(&PlatformChannelDailyStat{StatDate: d1, ChannelId: ch, ChannelName: "cc", RevenueQuota: 100, CostQuota: 40, RecordCount: 2, CostRatioSum: 1.0}).Error)
	require.NoError(t, DB.Create(&PlatformChannelDailyStat{StatDate: d2, ChannelId: ch, ChannelName: "cc", RevenueQuota: 50, CostQuota: 30, RecordCount: 1, CostRatioSum: 0.5}).Error)

	rows, err := getConsumptionCostByChannelFromDaily(d1, d2, true)
	require.NoError(t, err)
	var mine *ConsumptionCostChannelStat
	for _, r := range rows {
		if r.ChannelId == ch {
			mine = r
		}
	}
	require.NotNil(t, mine)
	assert.EqualValues(t, 150, mine.TotalRevenue)
	assert.EqualValues(t, 70, mine.TotalCost)
	assert.EqualValues(t, 3, mine.RecordCount)
	assert.InDelta(t, 1.5, mine.CostRatioSum, 1e-9)
	assert.Equal(t, "cc", mine.ChannelName)

	// restricting to just d1 excludes d2's contribution
	rows, err = getConsumptionCostByChannelFromDaily(d1, d1, true)
	require.NoError(t, err)
	mine = nil
	for _, r := range rows {
		if r.ChannelId == ch {
			mine = r
		}
	}
	require.NotNil(t, mine)
	assert.EqualValues(t, 100, mine.TotalRevenue)
}

func TestGetCommissionStatsByEmployeeFromDaily(t *testing.T) {
	requireDB(t)
	d1 := bsFarFutureDay(t)
	d2 := d1 + businessStatsDaySeconds
	emp := nextTestID()
	t.Cleanup(func() {
		DB.Unscoped().Where("employee_user_id = ?", emp).Delete(&EmployeeCommissionDailyStat{})
	})
	require.NoError(t, DB.Create(&EmployeeCommissionDailyStat{StatDate: d1, EmployeeUserId: emp, RevenueQuota: 100, CostQuota: 40, ProfitQuota: 60, CommissionQuota: 6, RecordCount: 1}).Error)
	require.NoError(t, DB.Create(&EmployeeCommissionDailyStat{StatDate: d2, EmployeeUserId: emp, RevenueQuota: 200, CostQuota: 80, ProfitQuota: 120, CommissionQuota: 12, RecordCount: 2}).Error)

	rows, err := getCommissionStatsByEmployeeFromDaily(d1, d2, true)
	require.NoError(t, err)
	var mine *CommissionEmployeeStat
	for _, r := range rows {
		if r.EmployeeUserId == emp {
			mine = r
		}
	}
	require.NotNil(t, mine)
	assert.EqualValues(t, 300, mine.TotalRevenue)
	assert.EqualValues(t, 120, mine.TotalCost)
	assert.EqualValues(t, 180, mine.TotalProfit)
	assert.EqualValues(t, 18, mine.TotalCommission)
	assert.EqualValues(t, 3, mine.RecordCount)
}

func TestSplitCoveredDailyRanges(t *testing.T) {
	requireDB(t)
	// Build 5 consecutive days; mark days 0,1 and 3 as covered.
	base := bsFarFutureDay(t)
	days := []int64{base, base + businessStatsDaySeconds, base + 2*businessStatsDaySeconds, base + 3*businessStatsDaySeconds, base + 4*businessStatsDaySeconds}
	for _, d := range days {
		cleanupCoverageRow(t, d)
	}
	for _, d := range []int64{days[0], days[1], days[3]} {
		require.NoError(t, DB.Create(&BusinessDailyStatsCoverage{StatDate: d, CompletedAt: 1}).Error)
	}

	covered, uncovered, err := splitCoveredDailyRanges(days[0], days[4])
	require.NoError(t, err)
	// covered: [day0..day1] and [day3..day3]
	require.Len(t, covered, 2)
	assert.Equal(t, days[0], covered[0].StartDate)
	assert.Equal(t, days[1], covered[0].EndDate)
	assert.Equal(t, days[3], covered[1].StartDate)
	assert.Equal(t, days[3], covered[1].EndDate)
	// uncovered: [day2..day2] and [day4..day4]
	require.Len(t, uncovered, 2)
	assert.Equal(t, days[2], uncovered[0].StartDate)
	assert.Equal(t, days[2], uncovered[0].EndDate)
	assert.Equal(t, days[4], uncovered[1].StartDate)
	assert.Equal(t, days[4], uncovered[1].EndDate)

	// inverted range -> nil, nil
	c2, u2, err := splitCoveredDailyRanges(days[4], days[0])
	require.NoError(t, err)
	assert.Nil(t, c2)
	assert.Nil(t, u2)
}

func TestApplyStatDateRanges_EmptyMatchesNothing(t *testing.T) {
	requireDB(t)
	// empty ranges must produce a "1 = 0" predicate (zero rows)
	var count int64
	tx := applyStatDateRanges(DB.Model(&PlatformChannelDailyStat{}), nil)
	require.NoError(t, tx.Count(&count).Error)
	assert.EqualValues(t, 0, count)
}

func TestGetConsumptionCostByChannelWithPlan_DailyOnly(t *testing.T) {
	requireDB(t)
	d1 := bsFarFutureDay(t)
	ch := nextTestID()
	cleanupPlatformDailyRow(t, d1, ch)
	require.NoError(t, DB.Create(&PlatformChannelDailyStat{StatDate: d1, ChannelId: ch, ChannelName: "planchan", RevenueQuota: 500, CostQuota: 200, RecordCount: 4, CostRatioSum: 2.0}).Error)

	plan := &ResolvedQueryPlan{
		Context:       context.Background(),
		CoveredRanges: []statDateRange{{StartDate: d1, EndDate: d1}},
	}
	items, err := GetConsumptionCostByChannelWithPlan(plan)
	require.NoError(t, err)
	var mine *ConsumptionCostChannelStat
	for _, it := range items {
		if it.ChannelId == ch {
			mine = it
		}
	}
	require.NotNil(t, mine)
	assert.EqualValues(t, 500, mine.TotalRevenue)
	assert.EqualValues(t, 200, mine.TotalCost)
	assert.EqualValues(t, 4, mine.RecordCount)
	assert.InDelta(t, 0.5, mine.CostRatio, 1e-9) // 2.0 / 4

	// summary via daily-only fast path
	summary, err := GetConsumptionCostChannelSummaryWithPlan(plan)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, summary.Totals.TotalRevenue, int64(500))
	assert.GreaterOrEqual(t, summary.ProfitableChannelCount, 1)
}

func TestResolveBusinessStatsDailyOnlyQueryPlan(t *testing.T) {
	requireDB(t)
	// A row with a far-future stat_date makes maxDate our row.
	d := bsFarFutureDay(t)
	ch := nextTestID()
	cleanupPlatformDailyRow(t, d, ch)
	require.NoError(t, DB.Create(&PlatformChannelDailyStat{StatDate: d, ChannelId: ch, RevenueQuota: 1}).Error)

	// endTime before our day -> the plan clamps endDate < startDate -> empty covered ranges.
	plan, err := ResolveBusinessStatsDailyOnlyQueryPlan(0, d-10*businessStatsDaySeconds)
	require.NoError(t, err)
	require.NotNil(t, plan)
	// our future day is excluded, so it must not appear in covered ranges
	for _, r := range plan.CoveredRanges {
		assert.False(t, r.StartDate <= d && d <= r.EndDate)
	}

	// full range including our day -> covered range contains it
	plan, err = ResolveBusinessStatsDailyOnlyQueryPlan(0, 0)
	require.NoError(t, err)
	require.NotEmpty(t, plan.CoveredRanges)
	last := plan.CoveredRanges[len(plan.CoveredRanges)-1]
	assert.GreaterOrEqual(t, last.EndDate, d)
}

func TestGetConsumptionCostByChannelPageWithPlan_DailyOnly(t *testing.T) {
	requireDB(t)
	d := bsFarFutureDay(t)
	ch := nextTestID()
	// distinctive channel-name keyword to exercise applyChannelKeyword
	kw := uniq("kwchan")
	cleanupPlatformDailyRow(t, d, ch)
	require.NoError(t, DB.Create(&PlatformChannelDailyStat{StatDate: d, ChannelId: ch, ChannelName: kw, RevenueQuota: 400, CostQuota: 100, RecordCount: 4, CostRatioSum: 2.0}).Error)

	plan := &ResolvedQueryPlan{Context: context.Background(), CoveredRanges: []statDateRange{{StartDate: d, EndDate: d}}}
	rows, total, err := GetConsumptionCostByChannelPageWithPlan(plan, 1, 20, kw, "est_profit", "desc")
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	assert.Equal(t, ch, rows[0].ChannelId)
	assert.EqualValues(t, 400, rows[0].TotalRevenue)
	assert.Equal(t, kw, rows[0].ChannelName)
}

func TestGetCommissionStatsByEmployeePageWithPlan_DailyOnly(t *testing.T) {
	requireDB(t)
	d := bsFarFutureDay(t)
	emp := nextTestID()
	t.Cleanup(func() { DB.Unscoped().Where("employee_user_id = ?", emp).Delete(&EmployeeCommissionDailyStat{}) })
	require.NoError(t, DB.Create(&EmployeeCommissionDailyStat{StatDate: d, EmployeeUserId: emp, RevenueQuota: 300, CostQuota: 120, ProfitQuota: 180, CommissionQuota: 18, RecordCount: 3}).Error)

	plan := &ResolvedQueryPlan{Context: context.Background(), CoveredRanges: []statDateRange{{StartDate: d, EndDate: d}}}
	rows, total, err := GetCommissionStatsByEmployeePageWithPlan(plan, 1, 20)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, int64(1))
	var mine *CommissionEmployeeStat
	for _, r := range rows {
		if r.EmployeeUserId == emp {
			mine = r
		}
	}
	require.NotNil(t, mine)
	assert.EqualValues(t, 180, mine.TotalProfit)
	assert.EqualValues(t, 18, mine.TotalCommission)
}
