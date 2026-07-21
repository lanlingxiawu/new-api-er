package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readResetStat fetches a single reset-period daily stat row by its unique key,
// returning nil when absent.
func readResetStat(t *testing.T, resetStartedAt, statDate int64, userId int) *EmployeeCommissionResetPeriodDailyStat {
	t.Helper()
	var row EmployeeCommissionResetPeriodDailyStat
	err := DB.Where("reset_started_at = ? AND stat_date = ? AND employee_user_id = ?", resetStartedAt, statDate, userId).First(&row).Error
	if err != nil {
		return nil
	}
	return &row
}

func TestSwitchCommissionPeriod_InvalidTarget(t *testing.T) {
	requireDB(t)
	_, err := SwitchCommissionPeriod(0, false, false, 0)
	assert.Error(t, err)
	_, err = SwitchCommissionPeriod(-5, true, true, 0)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// advanceCommissionBaselineOnly — takes an explicit id list, fully isolatable.
// ---------------------------------------------------------------------------

func TestAdvanceCommissionBaselineOnly(t *testing.T) {
	requireDB(t)
	const P = int64(500_000)
	u1 := mkUser(t, nil)
	u2 := mkUser(t, nil)
	empCleanupStats(t, u1.Id)
	empCleanupStats(t, u2.Id)
	// u1 below P -> advanced; u2 already at P -> not advanced (guard).
	ctMkTierLevel(t, u1.Id, 0, P-1)
	ctMkTierLevel(t, u2.Id, 0, P)

	n, err := advanceCommissionBaselineOnly(P, []int{u1.Id, u2.Id})
	require.NoError(t, err)
	assert.Equal(t, 1, n, "only the row with baseline < P is advanced")
	assert.EqualValues(t, P, reloadTierLevel(t, u1.Id).BaselineResetAt)
	assert.EqualValues(t, P, reloadTierLevel(t, u2.Id).BaselineResetAt)

	// idempotent second run advances nothing.
	n, err = advanceCommissionBaselineOnly(P, []int{u1.Id, u2.Id})
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

// ---------------------------------------------------------------------------
// rebucketDailyStatsForEmployees — pure-move and merge paths, fully isolatable.
// ---------------------------------------------------------------------------

func TestRebucketDailyStats_PureMove(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	empCleanupStats(t, u.Id)

	const oldReset = int64(400_000)
	const P = int64(450_000)
	pDayStart := commissionStatDayStart(P)
	// a row in the old bucket, dated on/after P's day -> should be re-hung to P.
	ctMkResetStat(t, oldReset, pDayStart, u.Id, 100, 40, 60, 6, 1)

	require.NoError(t, rebucketDailyStatsForEmployees(P, pDayStart, []int{u.Id}))

	// old bucket row is gone; a P-bucket row now holds the values.
	assert.Nil(t, readResetStat(t, oldReset, pDayStart, u.Id))
	moved := readResetStat(t, P, pDayStart, u.Id)
	require.NotNil(t, moved)
	assert.EqualValues(t, 60, moved.ProfitQuota)
	assert.EqualValues(t, 100, moved.RevenueQuota)
	assert.EqualValues(t, 1, moved.RecordCount)
}

func TestRebucketDailyStats_Merge(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	empCleanupStats(t, u.Id)

	const oldReset = int64(410_000)
	const P = int64(460_000)
	pDayStart := commissionStatDayStart(P)
	// existing P-bucket row AND an old-bucket row on the same (stat_date, emp)
	// -> amounts must be summed into the existing P row and the source deleted.
	ctMkResetStat(t, P, pDayStart, u.Id, 200, 80, 120, 10, 2)
	ctMkResetStat(t, oldReset, pDayStart, u.Id, 100, 40, 60, 6, 1)

	require.NoError(t, rebucketDailyStatsForEmployees(P, pDayStart, []int{u.Id}))

	assert.Nil(t, readResetStat(t, oldReset, pDayStart, u.Id), "merged source row deleted")
	merged := readResetStat(t, P, pDayStart, u.Id)
	require.NotNil(t, merged)
	assert.EqualValues(t, 300, merged.RevenueQuota) // 200+100
	assert.EqualValues(t, 120, merged.CostQuota)    // 80+40
	assert.EqualValues(t, 180, merged.ProfitQuota)  // 120+60
	assert.EqualValues(t, 3, merged.RecordCount)    // 2+1
}

func TestRebucketDailyStats_SkipsOutOfRangeAndPBucket(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	empCleanupStats(t, u.Id)

	const oldReset = int64(420_000)
	const P = int64(470_000)
	pDayStart := commissionStatDayStart(P)
	beforeDay := pDayStart - businessStatsDaySeconds // strictly before P's day
	// stat_date < pDayStart -> belongs to the previous period, must stay put.
	ctMkResetStat(t, oldReset, beforeDay, u.Id, 999, 0, 999, 9, 1)
	// a row already in the P bucket must be left untouched.
	ctMkResetStat(t, P, pDayStart, u.Id, 5, 0, 5, 1, 1)

	require.NoError(t, rebucketDailyStatsForEmployees(P, pDayStart, []int{u.Id}))

	stay := readResetStat(t, oldReset, beforeDay, u.Id)
	require.NotNil(t, stay, "row dated before P's day must not be re-hung")
	assert.EqualValues(t, 999, stay.ProfitQuota)

	pRow := readResetStat(t, P, pDayStart, u.Id)
	require.NotNil(t, pRow)
	assert.EqualValues(t, 5, pRow.ProfitQuota)

	// empty employee list -> no-op via the chunk wrapper.
	require.NoError(t, rebucketCurrentPeriodDailyStats(P, pDayStart, nil))
}

// ---------------------------------------------------------------------------
// SwitchCommissionPeriod end-to-end (baseline align + rebucket). The function
// scans ALL enabled employees; assertions are scoped to my employee.
// ---------------------------------------------------------------------------

func TestSwitchCommissionPeriod_AlignAndRebucket(t *testing.T) {
	requireDB(t)
	const P = int64(480_000)
	pDayStart := commissionStatDayStart(P)

	u := mkUser(t, nil)
	ctMkEmployeeProfile(t, u.Id, 1)
	empCleanupStats(t, u.Id)
	ctMkTierLevel(t, u.Id, 0, P-1) // baseline below P

	// old-bucket row within P's period should be re-hung to P.
	ctMkResetStat(t, P-1, pDayStart, u.Id, 70, 10, 60, 3, 1)

	// resetTiers=false, includePeriodData=true.
	_, err := SwitchCommissionPeriod(P, false, true, 9)
	require.NoError(t, err)

	// baseline aligned to P for my employee.
	assert.EqualValues(t, P, reloadTierLevel(t, u.Id).BaselineResetAt)
	// data rebucketed into P.
	moved := readResetStat(t, P, pDayStart, u.Id)
	require.NotNil(t, moved)
	assert.EqualValues(t, 60, moved.ProfitQuota)
}
