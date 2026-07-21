package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Local fixtures for the cost/settlement ledger tables.
//
// ConsumptionCost carries NO DeletedAt column, so there is no soft-delete
// filter to exercise here (documented). Quota columns (RevenueQuota /
// CostQuota) are int64 in this table — not int32 — so the money math is tested
// at int64 magnitude (values at and beyond the int32 boundary must survive
// intact through SUM aggregation).
// ---------------------------------------------------------------------------

// mkCost inserts a ConsumptionCost row directly (bypassing the async buffer) and
// registers a hard-delete cleanup. Callers should give it a unique ChannelId so
// aggregations scope cleanly.
func mkCost(t *testing.T, mut func(c *ConsumptionCost)) *ConsumptionCost {
	t.Helper()
	requireDB(t)
	c := &ConsumptionCost{
		CreatedAt: time.Now().Unix(),
	}
	if mut != nil {
		mut(c)
	}
	require.NoError(t, DB.Create(c).Error)
	deleteByID(t, &ConsumptionCost{}, c.Id)
	return c
}

// cleanupCostsByChannel deletes every ConsumptionCost row for a channel on
// cleanup. Used for records that may reach the DB via the async buffer flush.
func cleanupCostsByChannel(t *testing.T, channelID int) {
	t.Helper()
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Where("channel_id = ?", channelID).Delete(&ConsumptionCost{})
		}
	})
}

// ---------------------------------------------------------------------------
// Create funcs — normalization (CreatedAt defaulting, LogId nil coercion).
// These enqueue into the async buffer; we assert the normalization the create
// path is responsible for, and scope cleanup by unique channel_id in case a
// later flush persists them.
// ---------------------------------------------------------------------------

func TestCreateConsumptionCostRecord_Normalization(t *testing.T) {
	requireDB(t)

	t.Run("defaults CreatedAt and keeps positive LogId", func(t *testing.T) {
		ch := nextTestID()
		cleanupCostsByChannel(t, ch)
		logID := nextTestID()
		rec := &ConsumptionCost{
			ChannelId:    ch,
			LogId:        &logID,
			RevenueQuota: 100,
			CostQuota:    40,
		}
		require.NoError(t, CreateConsumptionCostRecord(rec))
		assert.Greater(t, rec.CreatedAt, int64(0), "CreatedAt must be defaulted to now")
		require.NotNil(t, rec.LogId)
		assert.Equal(t, logID, *rec.LogId, "positive LogId preserved")
	})

	t.Run("nils out non-positive LogId", func(t *testing.T) {
		ch := nextTestID()
		cleanupCostsByChannel(t, ch)
		zero := 0
		rec := &ConsumptionCost{ChannelId: ch, LogId: &zero, CreatedAt: 123}
		require.NoError(t, CreateConsumptionCostRecord(rec))
		assert.Nil(t, rec.LogId, "LogId<=0 must be coerced to nil")
		assert.EqualValues(t, 123, rec.CreatedAt, "explicit CreatedAt preserved")
	})

	t.Run("CreateConsumptionCost delegates identically", func(t *testing.T) {
		ch := nextTestID()
		cleanupCostsByChannel(t, ch)
		neg := -5
		rec := &ConsumptionCost{ChannelId: ch, LogId: &neg}
		require.NoError(t, CreateConsumptionCost(rec))
		assert.Nil(t, rec.LogId)
		assert.Greater(t, rec.CreatedAt, int64(0))
	})
}

func TestCreateConsumptionCostAndCommissionLog(t *testing.T) {
	requireDB(t)

	t.Run("nil log_id always inserts (no idempotency key)", func(t *testing.T) {
		ch := nextTestID()
		cleanupCostsByChannel(t, ch)
		cost := &ConsumptionCost{ChannelId: ch, RevenueQuota: 10, CostQuota: 3}
		log := &EmployeeCommissionLog{ChannelId: ch}
		inserted, err := CreateConsumptionCostAndCommissionLog(cost, log)
		require.NoError(t, err)
		assert.True(t, inserted, "nil log_id => always enqueued")
		assert.Greater(t, cost.CreatedAt, int64(0))
		assert.Equal(t, cost.CreatedAt, log.CreatedAt, "log CreatedAt copied from cost")
		assert.Nil(t, cost.LogId)
		assert.Nil(t, log.LogId)
	})

	t.Run("duplicate log_id deduped (idempotency)", func(t *testing.T) {
		ch := nextTestID()
		cleanupCostsByChannel(t, ch)
		logID := nextTestID() // process-unique, so the in-memory dedup set is clean
		mk := func() (*ConsumptionCost, *EmployeeCommissionLog) {
			id := logID
			return &ConsumptionCost{ChannelId: ch, LogId: &id},
				&EmployeeCommissionLog{ChannelId: ch, LogId: &id}
		}
		c1, l1 := mk()
		first, err := CreateConsumptionCostAndCommissionLog(c1, l1)
		require.NoError(t, err)
		assert.True(t, first, "first occurrence of log_id enqueues")

		c2, l2 := mk()
		second, err := CreateConsumptionCostAndCommissionLog(c2, l2)
		require.NoError(t, err)
		assert.False(t, second, "second occurrence of same log_id is deduped")
	})

	t.Run("cost LogId reconciled to log LogId", func(t *testing.T) {
		ch := nextTestID()
		cleanupCostsByChannel(t, ch)
		logID := nextTestID()
		id := logID
		// cost has no log id but the commission log does -> cost must adopt it
		cost := &ConsumptionCost{ChannelId: ch}
		log := &EmployeeCommissionLog{ChannelId: ch, LogId: &id}
		inserted, err := CreateConsumptionCostAndCommissionLog(cost, log)
		require.NoError(t, err)
		assert.True(t, inserted)
		require.NotNil(t, cost.LogId)
		assert.Equal(t, logID, *cost.LogId, "cost.LogId reconciled to commission log_id")
	})
}

// ---------------------------------------------------------------------------
// Aggregation reads — GetConsumptionCostByChannel / GetConsumptionCostTotals.
//
// A sub-day, non-midnight-aligned range is routed by ResolveBusinessStatsQueryPlan
// straight to the ledger DetailRanges path (reads the ConsumptionCost table
// directly), independent of any daily-stat coverage. We scope assertions to a
// unique channel_id so exact sums hold regardless of other rows in the window.
// ---------------------------------------------------------------------------

// subDayLedgerWindow returns a [start,end] window guaranteed to hit the ledger
// detail path: start is one hour past a UTC day start, end stays within the day.
func subDayLedgerWindow() (int64, int64) {
	dayStart := time.Now().Unix() / 86400 * 86400
	start := dayStart + 3600
	end := start + 300
	return start, end
}

func TestGetConsumptionCostByChannel_ExactSums(t *testing.T) {
	requireDB(t)
	start, end := subDayLedgerWindow()
	ch := nextTestID()

	// three rows for our channel inside the window; one negative-revenue reversal
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.ChannelName = "chanA"
		c.CreatedAt = start + 10
		c.RevenueQuota = 1000
		c.CostQuota = 400
		c.CostRatio = 0.5
	})
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.ChannelName = "chanA"
		c.CreatedAt = start + 20
		c.RevenueQuota = 500
		c.CostQuota = 600 // loss row
		c.CostRatio = 1.5
	})
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.ChannelName = "chanA"
		c.CreatedAt = start + 30
		c.RevenueQuota = -200 // reversal
		c.CostQuota = -50
		c.CostRatio = 2.5
	})

	items, err := GetConsumptionCostByChannel(start, end)
	require.NoError(t, err)

	var mine *ConsumptionCostChannelStat
	for _, it := range items {
		if it.ChannelId == ch {
			mine = it
			break
		}
	}
	require.NotNil(t, mine, "our channel must appear in the by-channel rollup")
	assert.EqualValues(t, 1000+500-200, mine.TotalRevenue) // 1300
	assert.EqualValues(t, 400+600-50, mine.TotalCost)      // 950
	assert.EqualValues(t, 3, mine.RecordCount)
	assert.Equal(t, "chanA", mine.ChannelName, "snapshot channel name surfaces")
	// finalize: CostRatio = mean of per-row cost_ratio = (0.5+1.5+2.5)/3 = 1.5
	assert.InDelta(t, 1.5, mine.CostRatio, 1e-9)
}

func TestGetConsumptionCostByChannel_EmptyWindow(t *testing.T) {
	requireDB(t)
	start, end := subDayLedgerWindow()
	ch := nextTestID()
	// no rows for this channel -> it must be absent from the rollup
	items, err := GetConsumptionCostByChannel(start, end)
	require.NoError(t, err)
	for _, it := range items {
		assert.NotEqual(t, ch, it.ChannelId)
	}
}

func TestGetConsumptionCostTotals_MatchesByChannel(t *testing.T) {
	requireDB(t)
	start, end := subDayLedgerWindow()
	ch := nextTestID()
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.CreatedAt = start + 15
		c.RevenueQuota = 777
		c.CostQuota = 123
	})

	totals, err := GetConsumptionCostTotals(start, end)
	require.NoError(t, err)

	// Totals must equal the sum over the by-channel rollup (same data source),
	// and must include our contribution.
	items, err := GetConsumptionCostByChannel(start, end)
	require.NoError(t, err)
	var sumRev, sumCost, sumCnt int64
	for _, it := range items {
		sumRev += it.TotalRevenue
		sumCost += it.TotalCost
		sumCnt += it.RecordCount
	}
	assert.Equal(t, sumRev, totals.TotalRevenue)
	assert.Equal(t, sumCost, totals.TotalCost)
	assert.Equal(t, sumCnt, totals.RecordCount)
	assert.GreaterOrEqual(t, totals.TotalRevenue, int64(777))
	assert.GreaterOrEqual(t, totals.RecordCount, int64(1))
}

// int64 magnitude: two rows each at the int32 max must sum without truncation.
func TestGetConsumptionCostByChannel_Int32BoundaryNoTruncation(t *testing.T) {
	requireDB(t)
	start, end := subDayLedgerWindow()
	ch := nextTestID()
	const int32Max = int64(2147483647)
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.CreatedAt = start + 40
		c.RevenueQuota = int32Max
		c.CostQuota = int32Max
	})
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.CreatedAt = start + 50
		c.RevenueQuota = int32Max
		c.CostQuota = 1
	})

	items, err := GetConsumptionCostByChannel(start, end)
	require.NoError(t, err)
	var mine *ConsumptionCostChannelStat
	for _, it := range items {
		if it.ChannelId == ch {
			mine = it
		}
	}
	require.NotNil(t, mine)
	// 2*2147483647 = 4294967294 > int32 max -> proves int64 accumulation
	assert.EqualValues(t, int32Max*2, mine.TotalRevenue)
	assert.EqualValues(t, int32Max+1, mine.TotalCost)
}
