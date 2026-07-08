package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
)

// calcCostQuota / calcCommissionQuota feed int64 cost_quota / commission_quota
// columns that are ultimately reconciled against int32 quota policy. Before the
// billing hardening these used raw decimal.IntPart() / float multiplication,
// so an oversized cost_ratio / commission rate (or group_ratio) could persist
// an out-of-range or wrapped quota. These tests pin the current contract:
// decimal-precise, half-away-from-zero rounding with int32 saturation.

func TestCalcCostQuota(t *testing.T) {
	// Normal: cost = revenue / group_ratio * cost_ratio.
	assert.Equal(t, int64(500), calcCostQuota(1000, 1.0, 0.5))
	// group_ratio removes the customer-facing markup before applying cost.
	assert.Equal(t, int64(500), calcCostQuota(2000, 2.0, 0.5))
	// group_ratio == 0 means a free model: base cost is zero regardless of cost_ratio.
	assert.Equal(t, int64(0), calcCostQuota(1000, 0, 0.5))
	// cost_ratio == 0 yields zero cost.
	assert.Equal(t, int64(0), calcCostQuota(1000, 1.0, 0))
	// Decimal rounding, half away from zero (1000/3 = 333.33 -> 333).
	assert.Equal(t, int64(333), calcCostQuota(1000, 3.0, 1.0))
	// The regression: an oversized cost_ratio saturates instead of overflowing
	// int32 into a negative/wrapped cost.
	assert.Equal(t, int64(common.MaxQuota), calcCostQuota(2_000_000_000, 1.0, 1000))
}

func TestCalcCommissionQuota(t *testing.T) {
	// Zero profit or non-positive rate produce no commission.
	assert.Equal(t, int64(0), calcCommissionQuota(0, 0.05))
	assert.Equal(t, int64(0), calcCommissionQuota(1000, 0))
	assert.Equal(t, int64(0), calcCommissionQuota(1000, -0.05))
	// Normal positive commission.
	assert.Equal(t, int64(50), calcCommissionQuota(1000, 0.05))
	// Half-away-from-zero rounding (1000 * 0.0555 = 55.5 -> 56).
	assert.Equal(t, int64(56), calcCommissionQuota(1000, 0.0555))
	// Negative profit (refund reversal) yields negative commission.
	assert.Equal(t, int64(-50), calcCommissionQuota(-1000, 0.05))
	// Floor guard: a tiny positive product rounds to 0 but must reserve 1 quota.
	assert.Equal(t, int64(1), calcCommissionQuota(1, 0.0001))
	// Floor guard for reversal: a tiny negative product reverses at least 1 quota.
	assert.Equal(t, int64(-1), calcCommissionQuota(-1, 0.0001))
	// The regression: an oversized rate saturates instead of overflowing int32.
	assert.Equal(t, int64(common.MaxQuota), calcCommissionQuota(2_000_000_000, 1000))
}

func TestCalcSettlementCommissionQuota(t *testing.T) {
	// Zero profit short-circuits before rate evaluation.
	assert.Equal(t, int64(0), calcSettlementCommissionQuota(1000, 0, 0.05))
	// Otherwise delegates to calcCommissionQuota with the profit and rate.
	assert.Equal(t, int64(50), calcSettlementCommissionQuota(1000, 1000, 0.05))
	assert.Equal(t, int64(-50), calcSettlementCommissionQuota(1000, -1000, 0.05))
}
