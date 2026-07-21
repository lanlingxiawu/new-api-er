package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findExportRow returns the export row for a user id, or nil.
func findExportRow(rows []*CommissionMonthlyEmployeeRow, userId int) *CommissionMonthlyEmployeeRow {
	for _, r := range rows {
		if r.EmployeeUserId == userId {
			return r
		}
	}
	return nil
}

func TestGetCommissionMonthlyEmployeeExport_CurrentPeriod(t *testing.T) {
	requireDB(t)
	grp := uniq("exp")
	tier := ctMkTier(t, grp, 1, 0, 0.20)

	now := time.Now().Unix()
	period := ResolveCommissionMonthlyPeriod(now)
	statDate := localDayStart(now)

	// two employees with different profits, to verify profit-desc ordering.
	uHigh := mkUser(t, nil)
	uLow := mkUser(t, nil)
	ctMkEmployeeProfile(t, uHigh.Id, 1)
	ctMkEmployeeProfile(t, uLow.Id, 1)
	empCleanupStats(t, uHigh.Id)
	empCleanupStats(t, uLow.Id)
	const B = int64(1_600_000_000)
	ctMkTierLevel(t, uHigh.Id, tier.Id, B)
	ctMkTierLevel(t, uLow.Id, tier.Id, B)
	ctMkResetStat(t, B, statDate, uHigh.Id, usdToQuota(10), 0, usdToQuota(10), 0, 3) // $10 profit
	ctMkResetStat(t, B, statDate, uLow.Id, usdToQuota(2), 0, usdToQuota(2), 0, 1)    // $2 profit

	export, err := GetCommissionMonthlyEmployeeExport(period.PeriodStartAt, now)
	require.NoError(t, err)
	require.NotNil(t, export)
	assert.False(t, export.IsHistorical)

	high := findExportRow(export.Rows, uHigh.Id)
	low := findExportRow(export.Rows, uLow.Id)
	require.NotNil(t, high)
	require.NotNil(t, low)

	// profit + commission (= int64(profit*rate)) pinned exactly.
	assert.EqualValues(t, usdToQuota(10), high.ProfitQuota)
	assert.EqualValues(t, int64(float64(usdToQuota(10))*0.20), high.CommissionQuota) // 1_000_000
	assert.EqualValues(t, 3, high.RecordCount)
	assert.Equal(t, tier.Level, high.TierLevel)
	assert.Equal(t, tier.Rate, high.TierRate)
	assert.EqualValues(t, usdToQuota(2), low.ProfitQuota)
	assert.EqualValues(t, int64(float64(usdToQuota(2))*0.20), low.CommissionQuota) // 200_000

	// high profit sorts before low profit in the row slice.
	var idxHigh, idxLow = -1, -1
	for i, r := range export.Rows {
		if r.EmployeeUserId == uHigh.Id {
			idxHigh = i
		}
		if r.EmployeeUserId == uLow.Id {
			idxLow = i
		}
	}
	assert.Less(t, idxHigh, idxLow, "rows sorted by profit desc")
}

func TestGetCommissionMonthlyEmployeeExport_Historical(t *testing.T) {
	requireDB(t)
	grp := uniq("exph")
	tier := ctMkTier(t, grp, 1, 0, 0.20)

	now := time.Now().Unix()
	prev := ResolveCommissionMonthlyPeriod(now - 40*86400)
	// a stat row inside the previous period (2 days after its start).
	statDate := localDayStart(prev.PeriodStartAt + 2*86400)

	u := mkUser(t, nil)
	ctMkEmployeeProfile(t, u.Id, 1)
	empCleanupStats(t, u.Id)
	ctMkTierLevel(t, u.Id, tier.Id, 0)
	// historical aggregation ignores reset_started_at; use the natural bucket.
	ctMkResetStat(t, prev.PeriodStartAt, statDate, u.Id, usdToQuota(7), 0, usdToQuota(7), 0, 2)

	export, err := GetCommissionMonthlyEmployeeExport(prev.PeriodStartAt, 0)
	require.NoError(t, err)
	require.True(t, export.IsHistorical)

	row := findExportRow(export.Rows, u.Id)
	require.NotNil(t, row)
	assert.EqualValues(t, usdToQuota(7), row.ProfitQuota)
	assert.EqualValues(t, int64(float64(usdToQuota(7))*0.20), row.CommissionQuota)
}
