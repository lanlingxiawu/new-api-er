package model

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// Pure logic: tags, item build, margin, tag matching, cache keys, TTL.
// ===========================================================================

func TestValidConsumptionCostLedgerTag(t *testing.T) {
	for _, tag := range []string{
		"",
		ConsumptionCostLedgerTagReversal,
		ConsumptionCostLedgerTagLoss,
		ConsumptionCostLedgerTagProfit,
		ConsumptionCostLedgerTagZeroRevenue,
	} {
		assert.Truef(t, ValidConsumptionCostLedgerTag(tag), "tag %q should be valid", tag)
	}
	assert.False(t, ValidConsumptionCostLedgerTag("bogus"))
	assert.False(t, ValidConsumptionCostLedgerTag("PROFIT"))
}

func TestBuildConsumptionCostLedgerTags(t *testing.T) {
	cases := []struct {
		name    string
		rev     int64
		cost    int64
		want    []string
	}{
		{"zero revenue and cost", 0, 0, []string{ConsumptionCostLedgerTagZeroRevenue}},
		{"pure profit", 100, 40, []string{ConsumptionCostLedgerTagProfit}},
		{"loss", 40, 100, []string{ConsumptionCostLedgerTagLoss}},
		{"break-even non-zero", 100, 100, []string{}}, // profit==0 and not zero-revenue => no tag
		{"reversal negative revenue with loss", -100, 50, []string{ConsumptionCostLedgerTagLoss, ConsumptionCostLedgerTagReversal}},
		{"reversal negative revenue with profit", -100, -200, []string{ConsumptionCostLedgerTagProfit, ConsumptionCostLedgerTagReversal}},
		{"zero revenue but non-zero cost is a loss", 0, 10, []string{ConsumptionCostLedgerTagLoss}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := BuildConsumptionCostLedgerTags(ConsumptionCost{RevenueQuota: c.rev, CostQuota: c.cost})
			assert.Equal(t, c.want, got)
		})
	}
}

func TestBuildConsumptionCostLedgerItem(t *testing.T) {
	t.Run("profit and gross margin", func(t *testing.T) {
		logID := 42
		row := ConsumptionCost{
			Id: 7, LogId: &logID, UserId: 3, ChannelId: 9, ChannelName: "c",
			GroupName: "g", ModelName: "m", RevenueQuota: 1000, CostQuota: 400,
			GroupRatio: 1.2, CostRatio: 0.4, CreatedAt: 111,
		}
		it := BuildConsumptionCostLedgerItem(row)
		assert.EqualValues(t, 600, it.ProfitQuota) // 1000-400
		require.NotNil(t, it.GrossMargin)
		assert.InDelta(t, 0.6, *it.GrossMargin, 1e-9) // 600/1000
		assert.Equal(t, []string{ConsumptionCostLedgerTagProfit}, it.Tags)
		assert.Equal(t, &logID, it.LogId)
		assert.EqualValues(t, 7, it.Id)
	})

	t.Run("zero revenue => nil gross margin", func(t *testing.T) {
		it := BuildConsumptionCostLedgerItem(ConsumptionCost{RevenueQuota: 0, CostQuota: 0})
		assert.Nil(t, it.GrossMargin, "no margin when revenue is zero (division guard)")
		assert.EqualValues(t, 0, it.ProfitQuota)
		assert.Equal(t, []string{ConsumptionCostLedgerTagZeroRevenue}, it.Tags)
	})

	t.Run("negative revenue reversal => negative margin", func(t *testing.T) {
		it := BuildConsumptionCostLedgerItem(ConsumptionCost{RevenueQuota: -100, CostQuota: 0})
		require.NotNil(t, it.GrossMargin)
		assert.InDelta(t, 1.0, *it.GrossMargin, 1e-9) // (-100-0)/-100 = 1
		assert.EqualValues(t, -100, it.ProfitQuota)
	})
}

func TestMatchConsumptionCostLedgerTag(t *testing.T) {
	profitRow := ConsumptionCost{RevenueQuota: 100, CostQuota: 40}
	assert.True(t, matchConsumptionCostLedgerTag(profitRow, ""), "empty tag matches everything")
	assert.True(t, matchConsumptionCostLedgerTag(profitRow, ConsumptionCostLedgerTagProfit))
	assert.False(t, matchConsumptionCostLedgerTag(profitRow, ConsumptionCostLedgerTagLoss))

	reversalLoss := ConsumptionCost{RevenueQuota: -100, CostQuota: 50}
	assert.True(t, matchConsumptionCostLedgerTag(reversalLoss, ConsumptionCostLedgerTagReversal))
	assert.True(t, matchConsumptionCostLedgerTag(reversalLoss, ConsumptionCostLedgerTagLoss))
	assert.False(t, matchConsumptionCostLedgerTag(reversalLoss, ConsumptionCostLedgerTagProfit))
}

func TestShouldFilterConsumptionCostLedgerTagInApp(t *testing.T) {
	// Currently always false: all tag filters are pushed to SQL.
	for _, tag := range []string{"", ConsumptionCostLedgerTagProfit, ConsumptionCostLedgerTagReversal} {
		assert.False(t, shouldFilterConsumptionCostLedgerTagInApp(tag))
	}
}

func TestMergeAndFinalizeConsumptionCostLedgerStats(t *testing.T) {
	dst := &ConsumptionCostLedgerStats{RecordCount: 1, TotalRevenueQuota: 100, TotalCostQuota: 40, TotalProfitQuota: 60}
	src := &ConsumptionCostLedgerStats{RecordCount: 2, TotalRevenueQuota: 300, TotalCostQuota: 100, TotalProfitQuota: 200}
	mergeConsumptionCostLedgerStats(dst, src)
	assert.EqualValues(t, 3, dst.RecordCount)
	assert.EqualValues(t, 400, dst.TotalRevenueQuota)
	assert.EqualValues(t, 140, dst.TotalCostQuota)
	assert.EqualValues(t, 260, dst.TotalProfitQuota)

	// nil operands are no-ops
	mergeConsumptionCostLedgerStats(dst, nil)
	mergeConsumptionCostLedgerStats(nil, src)
	assert.EqualValues(t, 3, dst.RecordCount)

	finalizeConsumptionCostLedgerStats(dst)
	require.NotNil(t, dst.GrossMargin)
	assert.InDelta(t, 260.0/400.0, *dst.GrossMargin, 1e-9)

	zero := &ConsumptionCostLedgerStats{TotalRevenueQuota: 0, TotalProfitQuota: 5}
	finalizeConsumptionCostLedgerStats(zero)
	assert.Nil(t, zero.GrossMargin, "zero revenue => nil margin")
	finalizeConsumptionCostLedgerStats(nil) // no panic
}

func TestConsumptionCostLedgerAsyncStatsCacheKey(t *testing.T) {
	base := ConsumptionCostLedgerStatsFilter{}
	base.ChannelId = 5
	base.StartTime = 1000
	base.EndTime = 2000
	k1 := consumptionCostLedgerAsyncStatsCacheKey(base)
	assert.Contains(t, k1, "ledger:stats:v2:")

	// whitespace in model/group is trimmed => same key
	trimmed := base
	trimmed.ModelName = "  gpt  "
	base.ModelName = "gpt"
	assert.Equal(t, consumptionCostLedgerAsyncStatsCacheKey(base), consumptionCostLedgerAsyncStatsCacheKey(trimmed))

	// a differing field changes the key
	other := base
	other.ChannelId = 6
	assert.NotEqual(t, consumptionCostLedgerAsyncStatsCacheKey(base), consumptionCostLedgerAsyncStatsCacheKey(other))

	// lock key derives from the cache key suffix
	lk := consumptionCostLedgerAsyncStatsLockKey(k1)
	assert.Contains(t, lk, "ledger:stats:lock:v2:")
	assert.Equal(t, "ledger:stats:running:v2", consumptionCostLedgerAsyncStatsRunningKey())
}

func TestConsumptionCostLedgerAsyncStatsCacheTTL(t *testing.T) {
	now := time.Now().Unix()
	// range that includes today => live (short) TTL
	live := ConsumptionCostLedgerStatsFilter{}
	live.StartTime = now - 100
	live.EndTime = now + 100
	assert.Equal(t, consumptionCostLedgerAsyncStatsLiveCacheTTL, consumptionCostLedgerAsyncStatsCacheTTL(live))

	// old, closed range => history (long) TTL
	old := ConsumptionCostLedgerStatsFilter{}
	old.StartTime = now - 40*86400
	old.EndTime = now - 39*86400
	assert.Equal(t, consumptionCostLedgerAsyncStatsHistoryCacheTTL, consumptionCostLedgerAsyncStatsCacheTTL(old))
}

// ===========================================================================
// Filter builder: invalid tag path.
// ===========================================================================

func TestApplyConsumptionCostLedgerFilters_InvalidTag(t *testing.T) {
	requireDB(t)
	f := ConsumptionCostLedgerStatsFilter{}
	f.Tag = "not-a-tag"
	_, err := applyConsumptionCostLedgerFilters(DB.Model(&ConsumptionCost{}), f)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid tag")
}

// ===========================================================================
// DB-backed aggregation. Scoped to a unique channel_id => exact sums.
// ===========================================================================

// ledgerAggBase returns a stable base timestamp for a fresh unique channel.
func ledgerAggBase() int64 { return int64(1_600_000_000) }

func TestAggregateConsumptionCostLedgerStats_RangePath(t *testing.T) {
	requireDB(t)
	ch := nextTestID()
	base := ledgerAggBase()
	// span <= slice seconds (3600) forces the single-query range path
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.CreatedAt = base + 10
		c.RevenueQuota = 1000
		c.CostQuota = 400
	})
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.CreatedAt = base + 20
		c.RevenueQuota = 500
		c.CostQuota = 900 // loss
	})

	f := ConsumptionCostLedgerStatsFilter{}
	f.ChannelId = ch
	f.StartTime = base
	f.EndTime = base + 3600
	stats, err := AggregateConsumptionCostLedgerStats(context.Background(), f)
	require.NoError(t, err)
	assert.EqualValues(t, 2, stats.RecordCount)
	assert.EqualValues(t, 1500, stats.TotalRevenueQuota)   // 1000+500
	assert.EqualValues(t, 1300, stats.TotalCostQuota)      // 400+900
	assert.EqualValues(t, 200, stats.TotalProfitQuota)     // 1500-1300
	require.NotNil(t, stats.GrossMargin)
	assert.InDelta(t, 200.0/1500.0, *stats.GrossMargin, 1e-9)
}

func TestAggregateConsumptionCostLedgerStats_ChannelScopeNoTime(t *testing.T) {
	requireDB(t)
	ch := nextTestID()
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.CreatedAt = time.Now().Unix()
		c.RevenueQuota = 250
		c.CostQuota = 250 // profit 0 -> margin 0, not nil (revenue != 0)
	})
	f := ConsumptionCostLedgerStatsFilter{}
	f.ChannelId = ch // no time filter -> all rows for this unique channel
	stats, err := AggregateConsumptionCostLedgerStats(context.Background(), f)
	require.NoError(t, err)
	assert.EqualValues(t, 1, stats.RecordCount)
	assert.EqualValues(t, 0, stats.TotalProfitQuota)
	require.NotNil(t, stats.GrossMargin)
	assert.InDelta(t, 0.0, *stats.GrossMargin, 1e-9)
}

// FIXED: slice-boundary double count.
// aggregateConsumptionCostLedgerStatsBySlices now makes internal slice
// boundaries half-open [start, end) (created_at >= start AND created_at < end)
// while the last slice keeps the inclusive upper bound. A row whose created_at
// lands exactly on an internal slice boundary is therefore counted ONCE (by the
// later slice), matching the single-query range path. This test pins the
// corrected non-double-counted total.
func TestAggregateConsumptionCostLedgerStats_SliceBoundaryDoubleCount(t *testing.T) {
	requireDB(t)
	ch := nextTestID()
	base := ledgerAggBase()
	boundary := base + consumptionCostLedgerStatsSliceSeconds // exactly on slice 1/2 border
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.CreatedAt = boundary
		c.RevenueQuota = 100
		c.CostQuota = 40
	})

	// range path: span == 3600 (not > 3600) => single query, counts once.
	rangeF := ConsumptionCostLedgerStatsFilter{}
	rangeF.ChannelId = ch
	rangeF.StartTime = base
	rangeF.EndTime = base + consumptionCostLedgerStatsSliceSeconds
	rangeStats, err := AggregateConsumptionCostLedgerStats(context.Background(), rangeF)
	require.NoError(t, err)
	assert.EqualValues(t, 1, rangeStats.RecordCount, "range path counts the boundary row once")
	assert.EqualValues(t, 100, rangeStats.TotalRevenueQuota)

	// sliced path: span == 7200 (> 3600) => two slices sharing the boundary.
	sliceF := ConsumptionCostLedgerStatsFilter{}
	sliceF.ChannelId = ch
	sliceF.StartTime = base
	sliceF.EndTime = base + 2*consumptionCostLedgerStatsSliceSeconds
	sliceStats, err := AggregateConsumptionCostLedgerStats(context.Background(), sliceF)
	require.NoError(t, err)
	// FIXED: the boundary row is counted exactly once (half-open internal slices),
	// so the sliced path now matches the single-query range ground truth.
	assert.EqualValues(t, 1, sliceStats.RecordCount, "boundary row counted once across slices")
	assert.EqualValues(t, 100, sliceStats.TotalRevenueQuota, "revenue counted once")
}

// A non-boundary row is aggregated identically by both paths (control case).
func TestAggregateConsumptionCostLedgerStats_SlicesInteriorConsistent(t *testing.T) {
	requireDB(t)
	ch := nextTestID()
	base := ledgerAggBase()
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.CreatedAt = base + 100 // interior of slice 1
		c.RevenueQuota = 100
		c.CostQuota = 40
	})
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.CreatedAt = base + consumptionCostLedgerStatsSliceSeconds + 100 // interior of slice 2
		c.RevenueQuota = 300
		c.CostQuota = 100
	})
	f := ConsumptionCostLedgerStatsFilter{}
	f.ChannelId = ch
	f.StartTime = base
	f.EndTime = base + 2*consumptionCostLedgerStatsSliceSeconds
	stats, err := AggregateConsumptionCostLedgerStats(context.Background(), f)
	require.NoError(t, err)
	assert.EqualValues(t, 2, stats.RecordCount)
	assert.EqualValues(t, 400, stats.TotalRevenueQuota)
	assert.EqualValues(t, 140, stats.TotalCostQuota)
	assert.EqualValues(t, 260, stats.TotalProfitQuota)
}

// Covers the Id / UserId / ModelName / GroupName filter branches plus the
// invalid-tag error surfaced through AggregateConsumptionCostLedgerStats.
func TestAggregateConsumptionCostLedgerStats_AllFilterFields(t *testing.T) {
	requireDB(t)
	ch := nextTestID()
	uid := nextTestID()
	base := ledgerAggBase()
	row := mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.UserId = uid
		c.ModelName = "gpt-xyz"
		c.GroupName = "grp-xyz"
		c.CreatedAt = base + 5
		c.RevenueQuota = 800
		c.CostQuota = 300
	})

	f := ConsumptionCostLedgerStatsFilter{}
	f.Id = row.Id
	f.UserId = uid
	f.ChannelId = ch
	f.ModelName = "gpt-xyz"
	f.GroupName = "grp-xyz"
	stats, err := AggregateConsumptionCostLedgerStats(context.Background(), f)
	require.NoError(t, err)
	assert.EqualValues(t, 1, stats.RecordCount)
	assert.EqualValues(t, 500, stats.TotalProfitQuota)

	// non-matching model name yields zero rows
	f2 := f
	f2.ModelName = "other-model"
	stats2, err := AggregateConsumptionCostLedgerStats(context.Background(), f2)
	require.NoError(t, err)
	assert.EqualValues(t, 0, stats2.RecordCount)

	// invalid tag propagates as an error through the aggregate
	fBad := ConsumptionCostLedgerStatsFilter{}
	fBad.ChannelId = ch
	fBad.Tag = "bogus-tag"
	_, err = AggregateConsumptionCostLedgerStats(context.Background(), fBad)
	assert.Error(t, err)
}

// ===========================================================================
// getConsumptionCostLedgerStats decision paths (no Redis).
// ===========================================================================

func TestGetConsumptionCostLedgerStats_DecisionPaths(t *testing.T) {
	requireDB(t)
	require.False(t, common.RedisEnabled, "these paths assume Redis disabled")

	t.Run("log_id present => ready (aggregate)", func(t *testing.T) {
		ch := nextTestID()
		logID := nextTestID()
		mkCost(t, func(c *ConsumptionCost) {
			c.ChannelId = ch
			c.LogId = &logID
			c.CreatedAt = time.Now().Unix()
			c.RevenueQuota = 90
			c.CostQuota = 10
		})
		f := ConsumptionCostLedgerStatsFilter{}
		f.LogId = logID
		res, err := GetConsumptionCostLedgerStats(context.Background(), f)
		require.NoError(t, err)
		assert.Equal(t, ConsumptionCostLedgerStatsStatusReady, res.StatsStatus)
		require.NotNil(t, res.Stats)
		assert.EqualValues(t, 80, res.Stats.TotalProfitQuota)
		assert.Equal(t, consumptionCostLedgerAsyncStatsMaxRunning, res.StatsRunningLimit)
	})

	t.Run("bad time range => unsupported", func(t *testing.T) {
		f := ConsumptionCostLedgerStatsFilter{}
		f.StartTime = 0
		f.EndTime = 0
		res, err := GetConsumptionCostLedgerStats(context.Background(), f)
		require.NoError(t, err)
		assert.Equal(t, ConsumptionCostLedgerStatsStatusUnsupported, res.StatsStatus)
		assert.Nil(t, res.Stats)

		// end <= start also unsupported
		f2 := ConsumptionCostLedgerStatsFilter{}
		f2.StartTime = 2000
		f2.EndTime = 2000
		res2, err := GetConsumptionCostLedgerStats(context.Background(), f2)
		require.NoError(t, err)
		assert.Equal(t, ConsumptionCostLedgerStatsStatusUnsupported, res2.StatsStatus)
	})

	t.Run("time range with Redis disabled => ready (synchronous aggregate)", func(t *testing.T) {
		ch := nextTestID()
		base := ledgerAggBase()
		mkCost(t, func(c *ConsumptionCost) {
			c.ChannelId = ch
			c.CreatedAt = base + 5
			c.RevenueQuota = 1000
			c.CostQuota = 250
		})
		f := ConsumptionCostLedgerStatsFilter{}
		f.ChannelId = ch
		f.StartTime = base
		f.EndTime = base + 60
		res, err := GetConsumptionCostLedgerStats(context.Background(), f)
		require.NoError(t, err)
		assert.Equal(t, ConsumptionCostLedgerStatsStatusReady, res.StatsStatus)
		require.NotNil(t, res.Stats)
		assert.EqualValues(t, 750, res.Stats.TotalProfitQuota)
	})
}

// ===========================================================================
// ListConsumptionCostLedger: keyset pagination, ordering, limit, tag filters.
// ===========================================================================

func TestListConsumptionCostLedger_KeysetPagination(t *testing.T) {
	requireDB(t)
	ch := nextTestID()
	base := ledgerAggBase()
	// 3 rows with strictly decreasing created_at so ordering is unambiguous
	for i, ts := range []int64{base + 3, base + 2, base + 1} {
		rev := int64(100 * (i + 1))
		mkCost(t, func(c *ConsumptionCost) {
			c.ChannelId = ch
			c.CreatedAt = ts
			c.RevenueQuota = rev
			c.CostQuota = 10
		})
	}

	f := ConsumptionCostLedgerFilter{Limit: 2}
	f.ChannelId = ch
	page1, err := ListConsumptionCostLedger(context.Background(), f)
	require.NoError(t, err)
	require.Len(t, page1.Items, 2)
	assert.True(t, page1.HasMore)
	require.NotNil(t, page1.NextCursor)
	// desc order: newest (base+3) first
	assert.EqualValues(t, base+3, page1.Items[0].CreatedAt)
	assert.EqualValues(t, base+2, page1.Items[1].CreatedAt)
	assert.EqualValues(t, base+2, page1.NextCursor.CreatedAt)

	f2 := ConsumptionCostLedgerFilter{Limit: 2}
	f2.ChannelId = ch
	f2.CursorCreated = page1.NextCursor.CreatedAt
	f2.CursorId = page1.NextCursor.Id
	page2, err := ListConsumptionCostLedger(context.Background(), f2)
	require.NoError(t, err)
	require.Len(t, page2.Items, 1)
	assert.False(t, page2.HasMore)
	assert.Nil(t, page2.NextCursor)
	assert.EqualValues(t, base+1, page2.Items[0].CreatedAt)
}

func TestListConsumptionCostLedger_DefaultLimitAndProfit(t *testing.T) {
	requireDB(t)
	ch := nextTestID()
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.CreatedAt = ledgerAggBase() + 7
		c.RevenueQuota = 1000
		c.CostQuota = 400
	})
	f := ConsumptionCostLedgerFilter{} // Limit 0 -> defaults to 100
	f.ChannelId = ch
	page, err := ListConsumptionCostLedger(context.Background(), f)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.False(t, page.HasMore)
	assert.EqualValues(t, 600, page.Items[0].ProfitQuota)
	require.NotNil(t, page.Items[0].GrossMargin)
}

func TestListConsumptionCostLedger_TagFilters(t *testing.T) {
	requireDB(t)
	ch := nextTestID()
	base := ledgerAggBase()
	// profit, loss, zero-revenue, reversal rows
	mkCost(t, func(c *ConsumptionCost) { c.ChannelId = ch; c.CreatedAt = base + 1; c.RevenueQuota = 100; c.CostQuota = 40 })  // profit
	mkCost(t, func(c *ConsumptionCost) { c.ChannelId = ch; c.CreatedAt = base + 2; c.RevenueQuota = 40; c.CostQuota = 100 })  // loss
	mkCost(t, func(c *ConsumptionCost) { c.ChannelId = ch; c.CreatedAt = base + 3; c.RevenueQuota = 0; c.CostQuota = 0 })     // zero
	mkCost(t, func(c *ConsumptionCost) { c.ChannelId = ch; c.CreatedAt = base + 4; c.RevenueQuota = -50; c.CostQuota = 10 })  // reversal(+loss)

	count := func(tag string) int {
		f := ConsumptionCostLedgerFilter{Limit: 50}
		f.ChannelId = ch
		f.Tag = tag
		page, err := ListConsumptionCostLedger(context.Background(), f)
		require.NoError(t, err)
		return len(page.Items)
	}
	assert.Equal(t, 4, count(""), "no tag returns all")
	assert.Equal(t, 1, count(ConsumptionCostLedgerTagProfit), "profit: revenue>cost")
	// loss: revenue<cost -> the loss row (40<100) and the reversal row (-50<10)
	assert.Equal(t, 2, count(ConsumptionCostLedgerTagLoss))
	assert.Equal(t, 1, count(ConsumptionCostLedgerTagZeroRevenue))
	assert.Equal(t, 1, count(ConsumptionCostLedgerTagReversal), "reversal: revenue<0")

	// invalid tag surfaces an error
	fBad := ConsumptionCostLedgerFilter{Limit: 10}
	fBad.ChannelId = ch
	fBad.Tag = "nope"
	_, err := ListConsumptionCostLedger(context.Background(), fBad)
	assert.Error(t, err)
}

// listConsumptionCostLedgerWithInAppTagFilter is currently unreachable through
// ListConsumptionCostLedger (shouldFilterConsumptionCostLedgerTagInApp is always
// false), so we exercise it directly for coverage of the in-app batch/scan path.
func TestListConsumptionCostLedgerWithInAppTagFilter_Direct(t *testing.T) {
	requireDB(t)
	ch := nextTestID()
	base := ledgerAggBase()
	mkCost(t, func(c *ConsumptionCost) { c.ChannelId = ch; c.CreatedAt = base + 1; c.RevenueQuota = 100; c.CostQuota = 40 }) // profit
	mkCost(t, func(c *ConsumptionCost) { c.ChannelId = ch; c.CreatedAt = base + 2; c.RevenueQuota = 40; c.CostQuota = 100 }) // loss
	mkCost(t, func(c *ConsumptionCost) { c.ChannelId = ch; c.CreatedAt = base + 3; c.RevenueQuota = 200; c.CostQuota = 10 }) // profit

	tx := DB.Model(&ConsumptionCost{}).Where("channel_id = ?", ch)
	page, err := listConsumptionCostLedgerWithInAppTagFilter(tx, ConsumptionCostLedgerTagProfit, 50)
	require.NoError(t, err)
	require.Len(t, page.Items, 2, "only the two profit rows survive the in-app tag filter")
	for _, it := range page.Items {
		assert.Greater(t, it.RevenueQuota, it.CostQuota)
	}
}

// ===========================================================================
// FillConsumptionCostLedgerChannelNames: snapshot, resolve, negative cache.
// ===========================================================================

func TestFillConsumptionCostLedgerChannelNames(t *testing.T) {
	requireDB(t)

	t.Run("empty slice is a no-op", func(t *testing.T) {
		FillConsumptionCostLedgerChannelNames(nil, nil)
	})

	t.Run("snapshot name kept and cached", func(t *testing.T) {
		cache := map[int]string{}
		items := []ConsumptionCostLedgerItem{{ChannelId: 123, ChannelName: "snap"}}
		FillConsumptionCostLedgerChannelNames(items, cache)
		assert.Equal(t, "snap", items[0].ChannelName)
		assert.Equal(t, "snap", cache[123])
	})

	t.Run("resolve missing name from channels table", func(t *testing.T) {
		chRow := mkChannel(t, func(c *Channel) { c.Name = "resolved-name" })
		items := []ConsumptionCostLedgerItem{{ChannelId: chRow.Id, ChannelName: ""}}
		cache := map[int]string{}
		FillConsumptionCostLedgerChannelNames(items, cache)
		assert.Equal(t, "resolved-name", items[0].ChannelName)
	})

	t.Run("unresolvable id stays empty and is negatively cached", func(t *testing.T) {
		missing := nextTestID() // no such channel
		cache := map[int]string{}
		items := []ConsumptionCostLedgerItem{{ChannelId: missing, ChannelName: ""}}
		FillConsumptionCostLedgerChannelNames(items, cache)
		assert.Equal(t, "", items[0].ChannelName)
		val, ok := cache[missing]
		assert.True(t, ok, "negative cache placeholder inserted")
		assert.Equal(t, "", val)
	})

	t.Run("channel id zero skipped", func(t *testing.T) {
		items := []ConsumptionCostLedgerItem{{ChannelId: 0, ChannelName: ""}}
		cache := map[int]string{}
		FillConsumptionCostLedgerChannelNames(items, cache)
		_, ok := cache[0]
		assert.False(t, ok)
	})
}

// ===========================================================================
// Redis-backed async stats: cache round-trip, running-slot accounting,
// lock acquire/release, pending status on cache miss.
// ===========================================================================

func TestConsumptionCostLedgerAsyncStats_CacheRoundTrip(t *testing.T) {
	enableRedis(t)
	f := ConsumptionCostLedgerStatsFilter{}
	f.ChannelId = nextTestID()
	f.StartTime = 1000
	f.EndTime = 2000
	key := consumptionCostLedgerAsyncStatsCacheKey(f)
	t.Cleanup(func() { _ = common.RedisDel(key) })

	// miss
	_, ok := getCachedConsumptionCostLedgerAsyncStats(key)
	assert.False(t, ok)

	stats := &ConsumptionCostLedgerStats{RecordCount: 3, TotalRevenueQuota: 900, TotalCostQuota: 300, TotalProfitQuota: 600}
	setCachedConsumptionCostLedgerAsyncStats(key, f, stats)
	got, ok := getCachedConsumptionCostLedgerAsyncStats(key)
	require.True(t, ok)
	assert.EqualValues(t, 3, got.RecordCount)
	assert.EqualValues(t, 600, got.TotalProfitQuota)

	// nil stats is a no-op
	setCachedConsumptionCostLedgerAsyncStats(key+":x", f, nil)
	_, ok = getCachedConsumptionCostLedgerAsyncStats(key + ":x")
	assert.False(t, ok)
}

func TestConsumptionCostLedgerAsyncStats_LockAndRunningSlot(t *testing.T) {
	enableRedis(t)
	key := "ledger:stats:v2:" + uniq("lk")
	lockKey := consumptionCostLedgerAsyncStatsLockKey(key)
	t.Cleanup(func() {
		releaseConsumptionCostLedgerAsyncStatsLock(key)
		releaseConsumptionCostLedgerAsyncStatsRunningSlot(key)
		_ = common.RedisDel(lockKey)
	})

	// lock acquire is exclusive
	assert.True(t, acquireConsumptionCostLedgerAsyncStatsLock(key))
	assert.False(t, acquireConsumptionCostLedgerAsyncStatsLock(key), "second acquire blocked")
	releaseConsumptionCostLedgerAsyncStatsLock(key)
	assert.True(t, acquireConsumptionCostLedgerAsyncStatsLock(key), "released lock reacquirable")
	releaseConsumptionCostLedgerAsyncStatsLock(key)

	// running-slot accounting
	before := getConsumptionCostLedgerAsyncStatsRunningCount()
	assert.True(t, acquireConsumptionCostLedgerAsyncStatsRunningSlot(key))
	after := getConsumptionCostLedgerAsyncStatsRunningCount()
	assert.Equal(t, before+1, after)
	// re-acquiring the same key refreshes, does not add a new slot
	assert.True(t, acquireConsumptionCostLedgerAsyncStatsRunningSlot(key))
	assert.Equal(t, after, getConsumptionCostLedgerAsyncStatsRunningCount())
	releaseConsumptionCostLedgerAsyncStatsRunningSlot(key)
	assert.Equal(t, before, getConsumptionCostLedgerAsyncStatsRunningCount())
}

func TestGetConsumptionCostLedgerStats_RedisPendingThenReady(t *testing.T) {
	enableRedis(t)
	ch := nextTestID()
	base := ledgerAggBase()
	mkCost(t, func(c *ConsumptionCost) {
		c.ChannelId = ch
		c.CreatedAt = base + 5
		c.RevenueQuota = 1000
		c.CostQuota = 250
	})
	f := ConsumptionCostLedgerStatsFilter{}
	f.ChannelId = ch
	f.StartTime = base
	f.EndTime = base + 60
	key := consumptionCostLedgerAsyncStatsCacheKey(f)
	t.Cleanup(func() {
		_ = common.RedisDel(key)
		releaseConsumptionCostLedgerAsyncStatsLock(key)
		releaseConsumptionCostLedgerAsyncStatsRunningSlot(key)
	})

	// first call: cache miss -> pending, async job kicked off
	res, err := GetConsumptionCostLedgerStats(context.Background(), f)
	require.NoError(t, err)
	assert.Equal(t, ConsumptionCostLedgerStatsStatusPending, res.StatsStatus)
	assert.Nil(t, res.Stats)

	// wait for the async aggregate to populate the cache
	var ready ConsumptionCostLedgerStatsResult
	require.Eventually(t, func() bool {
		ready, err = GetConsumptionCostLedgerStats(context.Background(), f)
		return err == nil && ready.StatsStatus == ConsumptionCostLedgerStatsStatusReady
	}, 8*time.Second, 100*time.Millisecond, "async stats should become ready")
	require.NotNil(t, ready.Stats)
	assert.EqualValues(t, 750, ready.Stats.TotalProfitQuota)
}
