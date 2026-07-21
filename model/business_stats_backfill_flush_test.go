package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bsBackfillDay returns a unique day-aligned far-future stat_date.
func bsBackfillDay(t *testing.T) (statDate, createdAt int64) {
	t.Helper()
	base := int64(4_200_000_000)
	off := int64(nextTestID()-testIDBase) * businessStatsDaySeconds
	statDate = localDayStart(base + off)
	createdAt = statDate + 100
	require.Equal(t, statDate, localDayStart(createdAt))
	return
}

func TestBackfillStatAggregator_AddPlatform(t *testing.T) {
	agg := newBackfillStatAggregator()
	agg.addPlatform(nil) // no-op
	statDate := int64(4_200_000_000)
	ch := 55
	agg.addPlatform(&ConsumptionCost{ChannelId: ch, ChannelName: "x", RevenueQuota: 100, CostQuota: 40, CostRatio: 0.8, CreatedAt: statDate + 10})
	agg.addPlatform(&ConsumptionCost{ChannelId: ch, ChannelName: "y", RevenueQuota: 50, CostQuota: 10, CostRatio: 0.2, CreatedAt: statDate + 20})
	d := agg.platform[memPlatformKey(localDayStart(statDate+10), ch)]
	require.NotNil(t, d)
	assert.EqualValues(t, 150, d.RevenueQuota)
	assert.EqualValues(t, 50, d.CostQuota)
	assert.EqualValues(t, 2, d.RecordCount)
	assert.InDelta(t, 1.0, d.CostRatioSum, 1e-9)
	assert.Equal(t, "x", d.ChannelName)
}

func TestBackfillFlushCosts_EmptyAndBatchSizeDefault(t *testing.T) {
	requireDB(t)
	n, err := BackfillFlushCosts(context.Background(), nil, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestBackfillFlushCosts_IsolationAndIdempotency(t *testing.T) {
	requireDB(t)
	bsClearMemBuffers() // realtime buffers must stay untouched by backfill
	statDate, createdAt := bsBackfillDay(t)
	ch := nextTestID()
	logId := nextTestID()
	nilCostChan := nextTestID()
	t.Cleanup(func() {
		DB.Unscoped().Where("log_id = ?", logId).Delete(&ConsumptionCost{})
		DB.Unscoped().Where("channel_id IN ?", []int{ch, nilCostChan}).Delete(&ConsumptionCost{})
		DB.Unscoped().Where("stat_date = ? AND channel_id IN ?", statDate, []int{ch, nilCostChan}).Delete(&PlatformChannelDailyStat{})
		coveredDaysCache.Delete(statDate)
		DB.Unscoped().Where("stat_date = ?", statDate).Delete(&BusinessDailyStatsCoverage{})
	})

	costs := []*ConsumptionCost{
		{LogId: common.GetPointer(logId), ChannelId: ch, RevenueQuota: 100, CostQuota: 40, CostRatio: 0.8, CreatedAt: createdAt},
		{LogId: nil, ChannelId: nilCostChan, RevenueQuota: 30, CostQuota: 10, CostRatio: 0.3, CreatedAt: createdAt}, // no dedup key
	}
	inserted, err := BackfillFlushCosts(context.Background(), costs, 100)
	require.NoError(t, err)
	assert.Equal(t, 2, inserted)

	// ISOLATION: the live in-memory realtime buffer must remain empty — the
	// backfill accumulates into a LOCAL aggregator, never the shared buffers.
	memPlatformLock.Lock()
	assert.Empty(t, memPlatformBuf, "backfill must not pollute the realtime buffer")
	memPlatformLock.Unlock()

	var row PlatformChannelDailyStat
	require.NoError(t, DB.Where("stat_date = ? AND channel_id = ?", statDate, ch).First(&row).Error)
	assert.EqualValues(t, 100, row.RevenueQuota)
	assert.EqualValues(t, 1, row.RecordCount)

	// IDEMPOTENCY: re-running with the same log_id inserts nothing new and does
	// not double-count the daily aggregate for the deduplicated row.
	costs2 := []*ConsumptionCost{
		{LogId: common.GetPointer(logId), ChannelId: ch, RevenueQuota: 100, CostQuota: 40, CostRatio: 0.8, CreatedAt: createdAt},
	}
	inserted, err = BackfillFlushCosts(context.Background(), costs2, 100)
	require.NoError(t, err)
	assert.Equal(t, 0, inserted) // already present -> skipped

	require.NoError(t, DB.Where("stat_date = ? AND channel_id = ?", statDate, ch).First(&row).Error)
	assert.EqualValues(t, 100, row.RevenueQuota) // NOT doubled
	assert.EqualValues(t, 1, row.RecordCount)
}

func TestBackfillFlushPairs_IsolationAndIdempotency(t *testing.T) {
	requireDB(t)
	bsClearMemBuffers()
	statDate, createdAt := bsBackfillDay(t)
	ch := nextTestID()
	emp := nextTestID()
	cust := nextTestID()
	logId := nextTestID()
	t.Cleanup(func() {
		DB.Unscoped().Where("log_id = ?", logId).Delete(&ConsumptionCost{})
		DB.Unscoped().Where("log_id = ?", logId).Delete(&EmployeeCommissionLog{})
		DB.Unscoped().Where("stat_date = ? AND channel_id = ?", statDate, ch).Delete(&PlatformChannelDailyStat{})
		DB.Unscoped().Where("employee_user_id = ?", emp).Delete(&EmployeeCommissionDailyStat{})
		DB.Unscoped().Where("employee_user_id = ?", emp).Delete(&EmployeeCustomerCommissionDailyStat{})
		DB.Unscoped().Where("employee_user_id = ?", emp).Delete(&EmployeeCommissionResetPeriodDailyStat{})
		DB.Unscoped().Where("user_id = ?", emp).Delete(&EmployeeTierLevel{})
		coveredDaysCache.Delete(statDate)
		DB.Unscoped().Where("stat_date = ?", statDate).Delete(&BusinessDailyStatsCoverage{})
	})

	pairs := []*CostCommissionBackfillPair{
		{
			Cost:       &ConsumptionCost{LogId: common.GetPointer(logId), ChannelId: ch, RevenueQuota: 100, CostQuota: 40, CostRatio: 0.8, CreatedAt: createdAt},
			Commission: &EmployeeCommissionLog{LogId: common.GetPointer(logId), EmployeeUserId: emp, CustomerUserId: cust, RevenueQuota: 100, CostQuota: 40, ProfitQuota: 60, CommissionQuota: 6, CreatedAt: createdAt},
		},
	}
	inserted, err := BackfillFlushPairs(context.Background(), pairs, 100)
	require.NoError(t, err)
	assert.Equal(t, 1, inserted)

	// isolation
	memPlatformLock.Lock()
	assert.Empty(t, memPlatformBuf)
	memPlatformLock.Unlock()
	memCommissionLock.Lock()
	assert.Empty(t, memCommissionBuf)
	memCommissionLock.Unlock()

	var pRow PlatformChannelDailyStat
	require.NoError(t, DB.Where("stat_date = ? AND channel_id = ?", statDate, ch).First(&pRow).Error)
	assert.EqualValues(t, 100, pRow.RevenueQuota)
	var cRow EmployeeCommissionDailyStat
	require.NoError(t, DB.Where("employee_user_id = ?", emp).First(&cRow).Error)
	assert.EqualValues(t, 60, cRow.ProfitQuota)
	assert.EqualValues(t, 1, cRow.RecordCount)

	// idempotent re-run
	inserted, err = BackfillFlushPairs(context.Background(), pairs, 100)
	require.NoError(t, err)
	assert.Equal(t, 0, inserted)
	require.NoError(t, DB.Where("employee_user_id = ?", emp).First(&cRow).Error)
	assert.EqualValues(t, 60, cRow.ProfitQuota) // not doubled
	assert.EqualValues(t, 1, cRow.RecordCount)
}

func TestBackfillFlushPairs_Empty(t *testing.T) {
	requireDB(t)
	n, err := BackfillFlushPairs(context.Background(), nil, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestBackfillExistingCostLogIDs(t *testing.T) {
	requireDB(t)
	id1, id2 := nextTestID(), nextTestID()
	t.Cleanup(func() { DB.Unscoped().Where("log_id IN ?", []int{id1, id2}).Delete(&ConsumptionCost{}) })
	require.NoError(t, DB.Create(&ConsumptionCost{LogId: common.GetPointer(id1), ChannelId: 1, CreatedAt: 1}).Error)

	existing := backfillExistingCostLogIDs(context.Background(), []*ConsumptionCost{
		{LogId: common.GetPointer(id1)},
		{LogId: common.GetPointer(id2)},
		{LogId: nil},
	})
	_, has1 := existing[id1]
	_, has2 := existing[id2]
	assert.True(t, has1)
	assert.False(t, has2)

	// all-nil -> nil map
	assert.Nil(t, backfillExistingCostLogIDs(context.Background(), []*ConsumptionCost{{LogId: nil}}))
}

func TestGetCommissionRateSnapshotForDay(t *testing.T) {
	requireDB(t)
	statDate, createdAt := bsBackfillDay(t)
	emp := nextTestID()
	t.Cleanup(func() { DB.Unscoped().Where("employee_user_id = ?", emp).Delete(&EmployeeCommissionLog{}) })

	// two logs the same day; the later one carries the effective rate
	require.NoError(t, DB.Create(&EmployeeCommissionLog{EmployeeUserId: emp, CommissionRate: 0.10, CreatedAt: createdAt}).Error)
	require.NoError(t, DB.Create(&EmployeeCommissionLog{EmployeeUserId: emp, CommissionRate: 0.25, CreatedAt: createdAt + 50}).Error)

	rate, ok := GetCommissionRateSnapshotForDay(context.Background(), emp, statDate)
	require.True(t, ok)
	assert.InDelta(t, 0.25, rate, 1e-9)

	// no record for a different employee -> (0, false)
	rate, ok = GetCommissionRateSnapshotForDay(context.Background(), nextTestID(), statDate)
	assert.False(t, ok)
	assert.InDelta(t, 0, rate, 1e-9)
}

func TestBackfillExportedFilenameHelpers(t *testing.T) {
	// FallbackFilename / FallbackLogDir / BusinessDay* are exported wrappers.
	assert.Equal(t, fallbackFilename(FallbackFileBasename, "2099-01-01"), FallbackFilename("2099-01-01"))
	assert.Equal(t, resolveFallbackDir(), FallbackLogDir())
}
