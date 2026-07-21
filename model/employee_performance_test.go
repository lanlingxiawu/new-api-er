package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Pure: calcManualPerfCommissionQuota — sign + ±1 floor + rounding.
// ---------------------------------------------------------------------------

func TestCalcManualPerfCommissionQuota(t *testing.T) {
	cases := []struct {
		name   string
		profit int64
		rate   float64
		want   int64
	}{
		{"zero profit", 0, 0.1, 0},
		{"zero rate", 100, 0, 0},
		{"negative rate", 100, -0.5, 0},
		{"exact", usdToQuota(5), 0.10, 250000},   // 2_500_000 * 0.10
		{"exact_15pct", usdToQuota(10), 0.15, 750000}, // 5_000_000 * 0.15
		{"tiny positive floors to +1", 3, 0.1, 1},   // 0.3 rounds to 0 -> +1
		{"tiny negative floors to -1", -3, 0.1, -1}, // -0.3 rounds to 0 -> -1
		{"rounds half", 5, 0.1, 1},                  // 0.5 -> Round(0)=1? decimal .Round(0) rounds half away
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, calcManualPerfCommissionQuota(c.profit, c.rate))
		})
	}
}

func readDailyStat(t *testing.T, statDate int64, userId int) *EmployeeCommissionDailyStat {
	t.Helper()
	var row EmployeeCommissionDailyStat
	if err := DB.Where("stat_date = ? AND employee_user_id = ?", statDate, userId).First(&row).Error; err != nil {
		return nil
	}
	return &row
}

// ---------------------------------------------------------------------------
// AddEmployeePerformance — current period, happy path + aggregation.
// ---------------------------------------------------------------------------

func TestAddEmployeePerformance_CurrentPeriod(t *testing.T) {
	requireDB(t)
	grp := uniq("perf")
	l1 := ctMkTier(t, grp, 1, 0, 0.10)
	_ = ctMkTier(t, grp, 2, 10, 0.20) // exists so reevaluate has a group ladder

	u := mkUser(t, nil)
	ctMkEmployeeProfile(t, u.Id, 1)
	empCleanupStats(t, u.Id)
	const B = int64(1_600_500_000)
	ctMkTierLevel(t, u.Id, l1.Id, B)

	profit := usdToQuota(5) // $5 -> stays L1
	res, err := AddEmployeePerformance(u.Id, profit, "bonus", 42, 0)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.IsHistorical)
	assert.EqualValues(t, profit, res.ProfitQuota)
	assert.EqualValues(t, 250000, res.CommissionQuota) // 2_500_000 * 0.10
	assert.Equal(t, 0.10, res.CommissionRate)
	assert.EqualValues(t, B, res.ResetStartedAt)

	// reset-period bucket aggregated: revenue=profit, cost=0.
	rp := readResetStat(t, res.ResetStartedAt, res.StatDate, u.Id)
	require.NotNil(t, rp)
	assert.EqualValues(t, profit, rp.ProfitQuota)
	assert.EqualValues(t, profit, rp.RevenueQuota)
	assert.EqualValues(t, 0, rp.CostQuota)
	assert.EqualValues(t, 250000, rp.CommissionQuota)
	assert.EqualValues(t, 1, rp.RecordCount)

	// cumulative daily stat also aggregated.
	ds := readDailyStat(t, res.StatDate, u.Id)
	require.NotNil(t, ds)
	assert.EqualValues(t, profit, ds.ProfitQuota)
	assert.EqualValues(t, 1, ds.RecordCount)

	// a sentinel commission-log row was written (log_id NULL).
	var logs []EmployeeCommissionLog
	require.NoError(t, DB.Where("employee_user_id = ? AND model_name = ?", u.Id, ManualPerformanceModelName).Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Nil(t, logs[0].LogId)
	assert.EqualValues(t, profit, logs[0].ProfitQuota)

	// stays L1 (profit $5 < L2 threshold $10).
	assert.Equal(t, l1.Id, reloadTierLevel(t, u.Id).TierId)
}

func TestAddEmployeePerformance_Errors(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	ctMkEmployeeProfile(t, u.Id, 1)
	empCleanupStats(t, u.Id)

	// zero profit rejected before any DB write.
	_, err := AddEmployeePerformance(u.Id, 0, "x", 1, 0)
	assert.ErrorContains(t, err, "zero")

	// unknown employee user id rejected.
	_, err = AddEmployeePerformance(880_000_123, 100, "x", 1, 0)
	assert.ErrorContains(t, err, "employee profile not found")
}

func TestAddEmployeePerformance_TriggersUpgrade(t *testing.T) {
	requireDB(t)
	grp := uniq("perfup")
	l1 := ctMkTier(t, grp, 1, 0, 0.10)
	l2 := ctMkTier(t, grp, 2, 10, 0.20)

	u := mkUser(t, nil)
	ctMkEmployeeProfile(t, u.Id, 1)
	empCleanupStats(t, u.Id)
	const B = int64(1_600_500_100)
	ctMkTierLevel(t, u.Id, l1.Id, B)

	// $15 of profit -> reevaluate promotes L1 -> L2 (current-period, bidirectional).
	_, err := AddEmployeePerformance(u.Id, usdToQuota(15), "big", 1, 0)
	require.NoError(t, err)
	assert.Equal(t, l2.Id, reloadTierLevel(t, u.Id).TierId)
}

func TestAddEmployeePerformance_HistoricalPeriod(t *testing.T) {
	requireDB(t)
	grp := uniq("perfhist")
	l1 := ctMkTier(t, grp, 1, 0, 0.10)

	u := mkUser(t, nil)
	ctMkEmployeeProfile(t, u.Id, 1)
	empCleanupStats(t, u.Id)
	ctMkTierLevel(t, u.Id, l1.Id, 0)

	now := time.Now().Unix()
	prev := ResolveCommissionMonthlyPeriod(now - 40*86400)

	profit := usdToQuota(8)
	res, err := AddEmployeePerformance(u.Id, profit, "backfill", 5, prev.PeriodStartAt)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsHistorical)
	assert.EqualValues(t, profit, res.ProfitQuota)
	// historical bucket == the natural period start.
	assert.EqualValues(t, prev.PeriodStartAt, res.ResetStartedAt)

	rp := readResetStat(t, res.ResetStartedAt, res.StatDate, u.Id)
	require.NotNil(t, rp)
	assert.EqualValues(t, profit, rp.ProfitQuota)

	// historical add must NOT reevaluate the current tier.
	assert.Equal(t, l1.Id, reloadTierLevel(t, u.Id).TierId)
}

// ---------------------------------------------------------------------------
// RevertEmployeePerformance — compensation nets aggregates to zero.
// ---------------------------------------------------------------------------

func TestRevertEmployeePerformance(t *testing.T) {
	requireDB(t)
	grp := uniq("rev")
	l1 := ctMkTier(t, grp, 1, 0, 0.10)

	u := mkUser(t, nil)
	ctMkEmployeeProfile(t, u.Id, 1)
	empCleanupStats(t, u.Id)
	const B = int64(1_600_500_200)
	ctMkTierLevel(t, u.Id, l1.Id, B)

	profit := usdToQuota(5)
	res, err := AddEmployeePerformance(u.Id, profit, "bonus", 1, 0)
	require.NoError(t, err)

	require.NoError(t, RevertEmployeePerformance(res.LogId, 9))

	// original row marked reverted (settle_status=2).
	var orig EmployeeCommissionLog
	require.NoError(t, DB.Where("id = ?", res.LogId).First(&orig).Error)
	assert.EqualValues(t, settleStatusReverted, orig.SettleStatus)

	// compensation row exists with negated amounts.
	var comp EmployeeCommissionLog
	require.NoError(t, DB.Where("employee_user_id = ? AND model_name = ?", u.Id, ManualPerformanceRevertModelName).First(&comp).Error)
	assert.EqualValues(t, -profit, comp.ProfitQuota)

	// aggregates net to zero.
	rp := readResetStat(t, res.ResetStartedAt, res.StatDate, u.Id)
	require.NotNil(t, rp)
	assert.EqualValues(t, 0, rp.ProfitQuota)
	assert.EqualValues(t, 0, rp.RevenueQuota)
	assert.EqualValues(t, 0, rp.RecordCount)
	assert.EqualValues(t, 0, rp.CommissionQuota)

	ds := readDailyStat(t, res.StatDate, u.Id)
	require.NotNil(t, ds)
	assert.EqualValues(t, 0, ds.ProfitQuota)
	assert.EqualValues(t, 0, ds.RecordCount)

	// double revert rejected.
	err = RevertEmployeePerformance(res.LogId, 9)
	assert.ErrorContains(t, err, "already reverted")
}

func TestRevertEmployeePerformance_Errors(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	ctMkEmployeeProfile(t, u.Id, 1)
	empCleanupStats(t, u.Id)

	// missing log id.
	err := RevertEmployeePerformance(880_000_777, 1)
	assert.ErrorContains(t, err, "adjustment not found")

	// a non-manual commission log cannot be reverted.
	log := &EmployeeCommissionLog{
		EmployeeUserId: u.Id,
		ModelName:      "gpt-4o",
		ProfitQuota:    100,
		CreatedAt:      time.Now().Unix(),
	}
	require.NoError(t, DB.Create(log).Error)
	deleteByID(t, &EmployeeCommissionLog{}, log.Id)
	err = RevertEmployeePerformance(log.Id, 1)
	assert.ErrorContains(t, err, "not a manual performance adjustment")
}

// ---------------------------------------------------------------------------
// reevaluateTierByPeriodProfit — bidirectional (up AND down), unlike auto.
// ---------------------------------------------------------------------------

func TestReevaluateTierByPeriodProfit_Downgrade(t *testing.T) {
	requireDB(t)
	grp := uniq("reeval")
	l1 := ctMkTier(t, grp, 1, 0, 0.05)
	_ = ctMkTier(t, grp, 2, 10, 0.10)
	l3 := ctMkTier(t, grp, 3, 20, 0.15)

	const B = int64(1_600_600_000)
	u := mkUser(t, nil)
	ctMkEmployeeProfile(t, u.Id, 1)
	empCleanupStats(t, u.Id)
	// currently L3 but only $5 period profit -> must fall to group floor L1.
	ctMkTierLevel(t, u.Id, l3.Id, B)
	ctMkResetStat(t, B, B, u.Id, usdToQuota(5), 0, usdToQuota(5), 0, 1)

	reevaluateTierByPeriodProfit(u.Id)
	assert.Equal(t, l1.Id, reloadTierLevel(t, u.Id).TierId, "manual reevaluate downgrades to floor")
}

func TestReevaluateTierByPeriodProfit_Upgrade(t *testing.T) {
	requireDB(t)
	grp := uniq("reevalup")
	l1 := ctMkTier(t, grp, 1, 0, 0.05)
	_ = ctMkTier(t, grp, 2, 10, 0.10)
	l3 := ctMkTier(t, grp, 3, 20, 0.15)

	const B = int64(1_600_600_100)
	u := mkUser(t, nil)
	ctMkEmployeeProfile(t, u.Id, 1)
	empCleanupStats(t, u.Id)
	ctMkTierLevel(t, u.Id, l1.Id, B)
	ctMkResetStat(t, B, B, u.Id, usdToQuota(20), 0, usdToQuota(20), 0, 1)

	reevaluateTierByPeriodProfit(u.Id)
	assert.Equal(t, l3.Id, reloadTierLevel(t, u.Id).TierId)
}

func TestReevaluateTierByPeriodProfit_OrphanAndUnbound(t *testing.T) {
	requireDB(t)
	grp := uniq("reevalorph")
	_ = ctMkTier(t, grp, 1, 0, 0.05)

	const B = int64(1_600_600_200)
	// orphaned tier -> group unknown -> skip (no change).
	u := mkUser(t, nil)
	ctMkEmployeeProfile(t, u.Id, 1)
	empCleanupStats(t, u.Id)
	orphan := int64(8_600_000_001)
	ctMkTierLevel(t, u.Id, orphan, B)
	ctMkResetStat(t, B, B, u.Id, usdToQuota(999), 0, usdToQuota(999), 0, 1)
	reevaluateTierByPeriodProfit(u.Id)
	assert.Equal(t, orphan, reloadTierLevel(t, u.Id).TierId)

	// unbound (tier_id=0) -> delegates to auto-upgrade; my group isn't 通用 so
	// it stays unbound (no 通用 ladder created by this test).
	u2 := mkUser(t, nil)
	ctMkEmployeeProfile(t, u2.Id, 1)
	empCleanupStats(t, u2.Id)
	ctMkTierLevel(t, u2.Id, 0, B)
	reevaluateTierByPeriodProfit(u2.Id)
	assert.EqualValues(t, 0, reloadTierLevel(t, u2.Id).TierId)
}
