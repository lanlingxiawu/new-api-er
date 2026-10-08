package service

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Regression (branch audit L1): costs are non-negative counters. Wrapping a
// valid positive decimal product into a negative int64 made
// recordChannelDailyUpstream silently discard it; saturating to MaxInt64
// instead would overflow at the next accumulator addition. Out-of-range costs
// are capped at maxEventCostQuota.
func TestBranchAuditUpstreamCostCannotWrapNegative(t *testing.T) {
	for _, tc := range []struct {
		base  int64
		ratio float64
	}{
		{math.MaxInt64, 2},
		{math.MaxInt64/2 + 1, 2},
		{math.MaxInt64, 100},
		{maxEventCostQuota, 1.5},
	} {
		t.Run(fmt.Sprintf("%d*%g", tc.base, tc.ratio), func(t *testing.T) {
			assert.Equal(t, maxEventCostQuota, upstreamQuota(tc.base, tc.ratio))
		})
	}
	assert.Equal(t, int64(150), upstreamQuota(100, 1.5), "in-range costs are unchanged")
}

func TestBranchAuditUpstreamBaseCannotWrapNegative(t *testing.T) {
	// A tiny but valid positive group ratio is accepted by the admin validator.
	assert.Equal(t, maxEventCostQuota, upstreamBaseQuota(priceDataWithGroupRatio(1e-18), 10, nil))
	snapshot := costCommissionSnapshot{Quota: 10, GroupRatio: 1e-18}
	assert.Equal(t, maxEventCostQuota, snapshot.upstreamBaseQuota())
}

func TestBranchAuditCommissionCostCannotBecomeCredit(t *testing.T) {
	assert.Equal(t, maxEventCostQuota, calcCostQuota(10, 1e-18, 1), "a positive revenue/cost input must never turn cost into a credit")
	assert.Equal(t, -maxEventCostQuota, calcCommissionQuota(math.MinInt64, 100), "a refund reversal is capped symmetrically")
	assert.Equal(t, maxEventCostQuota, calcCommissionQuota(math.MaxInt64, 100))
}

// Summing capped costs must stay far from int64 overflow: the daily
// accumulator and the BIGINT `cost_quota + ?` update add them up.
func TestBranchAuditCappedCostLeavesAccumulatorHeadroom(t *testing.T) {
	capped := upstreamQuota(math.MaxInt64, 2)
	assert.Equal(t, maxEventCostQuota, capped)
	const events = 8191
	assert.Less(t, capped, int64(math.MaxInt64/events), "8191 capped events must sum without overflow")
}
