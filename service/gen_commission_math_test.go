package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// employee_commission.go — pure decimal cost/commission math. BILLING-CRITICAL.
// No DB. Values pinned to EXACT expected integers.
// ===========================================================================

// --- calcCostQuota: cost = revenue / groupRatio * costRatio (round 0) -------

func TestCommmath_CalcCostQuota_Basic(t *testing.T) {
	// revenue 1000, groupRatio 2 => base 500, costRatio 0.5 => 250
	assert.Equal(t, int64(250), calcCostQuota(1000, 2.0, 0.5))
}

func TestCommmath_CalcCostQuota_GroupRatioOneIdentity(t *testing.T) {
	assert.Equal(t, int64(300), calcCostQuota(1000, 1.0, 0.3))
}

func TestCommmath_CalcCostQuota_ZeroGroupRatioIsFree(t *testing.T) {
	// groupRatio 0 => free model => cost 0 regardless of costRatio
	assert.Equal(t, int64(0), calcCostQuota(9999, 0.0, 0.9))
}

func TestCommmath_CalcCostQuota_RoundingHalfAway(t *testing.T) {
	// revenue 3, groupRatio 1, costRatio 0.5 => 1.5 -> round 0 -> 2
	assert.Equal(t, int64(2), calcCostQuota(3, 1.0, 0.5))
	// revenue 1, groupRatio 1, costRatio 0.4 => 0.4 -> 0
	assert.Equal(t, int64(0), calcCostQuota(1, 1.0, 0.4))
}

func TestCommmath_CalcCostQuota_NegativeRevenueRefundCase(t *testing.T) {
	// refund reversal: negative revenue yields negative cost
	assert.Equal(t, int64(-250), calcCostQuota(-1000, 2.0, 0.5))
}

// --- calcCommissionQuota: profit * rate (round 0) with ±1 floor -------------

func TestCommmath_CalcCommissionQuota_PositiveBasic(t *testing.T) {
	assert.Equal(t, int64(100), calcCommissionQuota(1000, 0.1))
}

func TestCommmath_CalcCommissionQuota_ZeroProfitOrRate(t *testing.T) {
	assert.Equal(t, int64(0), calcCommissionQuota(0, 0.5))
	assert.Equal(t, int64(0), calcCommissionQuota(1000, 0))
	assert.Equal(t, int64(0), calcCommissionQuota(1000, -0.1))
}

func TestCommmath_CalcCommissionQuota_PositiveFloorOne(t *testing.T) {
	// profit 1, rate 0.001 => 0.001 rounds to 0 but positive floor -> 1
	assert.Equal(t, int64(1), calcCommissionQuota(1, 0.001))
}

func TestCommmath_CalcCommissionQuota_NegativeFloorMinusOne(t *testing.T) {
	// negative profit that rounds to zero must still reverse at least -1
	assert.Equal(t, int64(-1), calcCommissionQuota(-1, 0.001))
}

func TestCommmath_CalcCommissionQuota_NegativeProfitScaled(t *testing.T) {
	assert.Equal(t, int64(-50), calcCommissionQuota(-500, 0.1))
}

func TestCommmath_CalcCommissionQuota_RoundingHalfAway(t *testing.T) {
	// profit 5, rate 0.5 => 2.5 -> round away -> 3
	assert.Equal(t, int64(3), calcCommissionQuota(5, 0.5))
}

func TestCommmath_CalcCommissionQuota_ExportedMatchesPrivate(t *testing.T) {
	assert.Equal(t, calcCommissionQuota(1234, 0.15), CalcCommissionQuota(1234, 0.15))
}

// --- calcSettlementCommissionQuota: zero-profit short circuit ---------------

func TestCommmath_SettlementCommission_ZeroProfitNoCommission(t *testing.T) {
	assert.Equal(t, int64(0), calcSettlementCommissionQuota(1000, 0, 0.5))
}

func TestCommmath_SettlementCommission_DelegatesForNonZeroProfit(t *testing.T) {
	assert.Equal(t, calcCommissionQuota(400, 0.25), calcSettlementCommissionQuota(1000, 400, 0.25))
	assert.Equal(t, int64(100), calcSettlementCommissionQuota(1000, 400, 0.25))
}

// --- optionalLogId ----------------------------------------------------------

func TestCommmath_OptionalLogId(t *testing.T) {
	assert.Nil(t, optionalLogId(0))
	assert.Nil(t, optionalLogId(-5))
	got := optionalLogId(42)
	require.NotNil(t, got)
	assert.Equal(t, 42, *got)
}

// --- fallback payload builders ---------------------------------------------

func TestCommmath_CostFallbackPayload(t *testing.T) {
	cost := &model.ConsumptionCost{UserId: 3, RevenueQuota: 100}
	p := businessStatsCostFallbackPayload(cost)
	assert.Same(t, cost, p["cost"])
	assert.Len(t, p, 1)
}

func TestCommmath_CostCommissionFallbackPayload_WithCommission(t *testing.T) {
	cost := &model.ConsumptionCost{UserId: 3}
	commLog := &model.EmployeeCommissionLog{EmployeeUserId: 9}
	p := businessStatsCostCommissionFallbackPayload(cost, commLog, 55, 200)
	assert.Same(t, cost, p["cost"])
	assert.Same(t, commLog, p["commission"])
	delta, ok := p["employee_delta"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 9, delta["user_id"])
	assert.Equal(t, int64(55), delta["commission_delta"])
	assert.Equal(t, int64(200), delta["profit_delta"])
}

func TestCommmath_CostCommissionFallbackPayload_NilCommission(t *testing.T) {
	cost := &model.ConsumptionCost{UserId: 3}
	p := businessStatsCostCommissionFallbackPayload(cost, nil, 0, 0)
	assert.Same(t, cost, p["cost"])
	_, hasComm := p["commission"]
	assert.False(t, hasComm)
	_, hasDelta := p["employee_delta"]
	assert.False(t, hasDelta)
}
