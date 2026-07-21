package billingexpr_test

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snapFor builds a v1 snapshot for an expression with the given ratios.
func snapFor(exprStr, estTier string, groupRatio, quotaPerUnit float64) *billingexpr.BillingSnapshot {
	return &billingexpr.BillingSnapshot{
		BillingMode:   "tiered_expr",
		ExprString:    exprStr,
		ExprHash:      billingexpr.ExprHashString(exprStr),
		GroupRatio:    groupRatio,
		EstimatedTier: estTier,
		QuotaPerUnit:  quotaPerUnit,
		ExprVersion:   1,
	}
}

// ---------------------------------------------------------------------------
// quotaConversion (v1) via ComputeTieredQuota: quota = cost/1e6 * QuotaPerUnit.
// ---------------------------------------------------------------------------

func TestComputeTieredQuota_ConversionFormula(t *testing.T) {
	// cost = p + c = 5000; quota = 5000/1e6 * 500000 = 2500.
	snap := snapFor(`tier("default", p + c)`, "default", 1.0, 500_000)
	res, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{P: 3000, C: 2000})
	require.NoError(t, err)
	assert.InDelta(t, 2500.0, res.ActualQuotaBeforeGroup, 1e-9)
	assert.Equal(t, 2500, res.ActualQuotaAfterGroup)
	assert.Equal(t, "default", res.MatchedTier)
	assert.False(t, res.CrossedTier)
	assert.Nil(t, res.Clamp)
}

func TestComputeTieredQuota_GroupRatioApplied(t *testing.T) {
	// cost = 1500; before = 750; after = round(750 * 2.0) = 1500.
	snap := snapFor(`tier("default", p + c)`, "default", 2.0, 500_000)
	res, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{P: 1000, C: 500})
	require.NoError(t, err)
	assert.InDelta(t, 750.0, res.ActualQuotaBeforeGroup, 1e-9)
	assert.Equal(t, 1500, res.ActualQuotaAfterGroup)
}

func TestComputeTieredQuota_ZeroTokens(t *testing.T) {
	snap := snapFor(`tier("default", p * 2 + c * 10)`, "default", 1.0, 500_000)
	res, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{})
	require.NoError(t, err)
	assert.Equal(t, 0.0, res.ActualQuotaBeforeGroup)
	assert.Equal(t, 0, res.ActualQuotaAfterGroup)
	assert.Nil(t, res.Clamp)
}

// ---------------------------------------------------------------------------
// Rounding at the settlement boundary (half-away-from-zero via QuotaRound).
// ---------------------------------------------------------------------------

func TestComputeTieredQuota_RoundingBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		coeff     string  // expr coefficient producing the fractional quota
		p         float64 // prompt tokens
		wantAfter int
		wantRaw   float64
	}{
		// quota = p*coeff/1e6*500000 = p*coeff*0.5
		{"0.75 rounds up to 1", "0.5", 3, 1, 0.75}, // 3*0.5*0.5 = 0.75 → 1
		{"0.6 rounds up to 1", "0.4", 3, 1, 0.6},   // 3*0.4*0.5 = 0.6 → 1
		{"0.5 rounds away to 1", "1", 1, 1, 0.5},   // 1*1*0.5 = 0.5 → 1 (half away)
		{"0.4 rounds down to 0", "0.4", 2, 0, 0.4}, // 2*0.4*0.5 = 0.4 → 0
		{"exact 2 stays 2", "2", 2, 2, 2},          // 2*2*0.5 = 2 → 2
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := snapFor(`tier("default", p * `+tt.coeff+`)`, "default", 1.0, 500_000)
			res, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{P: tt.p})
			require.NoError(t, err)
			assert.InDelta(t, tt.wantRaw, res.ActualQuotaBeforeGroup, 1e-9)
			assert.Equal(t, tt.wantAfter, res.ActualQuotaAfterGroup)
		})
	}
}

// ---------------------------------------------------------------------------
// Tier crossing detection (crossed = actual tier != estimated tier).
// ---------------------------------------------------------------------------

func TestComputeTieredQuota_TierCrossing(t *testing.T) {
	const e = `len <= 100000 ? tier("small", p * 1) : tier("large", p * 2)`

	t.Run("same tier → not crossed", func(t *testing.T) {
		snap := snapFor(e, "small", 1.0, 500_000)
		res, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{P: 100000, Len: 100000})
		require.NoError(t, err)
		assert.Equal(t, "small", res.MatchedTier)
		assert.False(t, res.CrossedTier)
		// 100000*1/1e6*500000 = 50000
		assert.Equal(t, 50000, res.ActualQuotaAfterGroup)
	})

	t.Run("estimated small, actual large → crossed", func(t *testing.T) {
		snap := snapFor(e, "small", 1.0, 500_000)
		res, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{P: 100001, Len: 100001})
		require.NoError(t, err)
		assert.Equal(t, "large", res.MatchedTier)
		assert.True(t, res.CrossedTier)
		// 100001*2/1e6*500000 = 100001
		assert.Equal(t, 100001, res.ActualQuotaAfterGroup)
	})
}

// ---------------------------------------------------------------------------
// Request-aware settlement: param() flips the tier and thus the quota.
// ---------------------------------------------------------------------------

func TestComputeTieredQuotaWithRequest_ProbeChangesQuota(t *testing.T) {
	const e = `param("fast") == true ? tier("fast", p * 4) : tier("normal", p * 2)`
	snap := snapFor(e, "normal", 1.0, 500_000)

	// No request body → normal tier: p*2 = 2000; quota = 1000.
	r1, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{P: 1000})
	require.NoError(t, err)
	assert.Equal(t, "normal", r1.MatchedTier)
	assert.Equal(t, 1000, r1.ActualQuotaAfterGroup)
	assert.False(t, r1.CrossedTier)

	// With fast=true → fast tier: p*4 = 4000; quota = 2000; crossed.
	r2, err := billingexpr.ComputeTieredQuotaWithRequest(snap, billingexpr.TokenParams{P: 1000}, billingexpr.RequestInput{Body: []byte(`{"fast":true}`)})
	require.NoError(t, err)
	assert.Equal(t, "fast", r2.MatchedTier)
	assert.Equal(t, 2000, r2.ActualQuotaAfterGroup)
	assert.True(t, r2.CrossedTier)
}

// ---------------------------------------------------------------------------
// Settlement error path: expression runtime error → err propagated, zero result.
// ---------------------------------------------------------------------------

func TestComputeTieredQuota_RunErrorPropagates(t *testing.T) {
	// param("x") missing → nil; p * nil is a runtime error.
	snap := snapFor(`p * param("x")`, "", 1.0, 500_000)
	res, err := billingexpr.ComputeTieredQuotaWithRequest(snap, billingexpr.TokenParams{P: 5}, billingexpr.RequestInput{Body: []byte(`{}`)})
	require.Error(t, err)
	assert.Equal(t, billingexpr.TieredResult{}, res, "error must yield the zero result")
}

func TestComputeTieredQuota_CompileErrorPropagates(t *testing.T) {
	snap := snapFor("(p + c", "", 1.0, 500_000)
	_, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{})
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// int32 saturation (billing-safety): oversized settlement clamps to MaxInt32
// and surfaces a Clamp event; underflow clamps to MinInt32; in-range = nil.
// A wraparound here would turn a charge into a credit — this is the guard.
// ---------------------------------------------------------------------------

func TestComputeTieredQuota_OverflowClamps(t *testing.T) {
	// cost = p * 1e9 = 1e18; before = 1e18/1e6*5e5 = 5e17 ≫ MaxInt32.
	snap := snapFor(`tier("base", p * 1000000000)`, "base", 1.0, 500_000)
	res, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{P: 1_000_000_000})
	require.NoError(t, err)

	assert.Equal(t, math.MaxInt32, res.ActualQuotaAfterGroup, "must clamp, never wrap negative")
	require.NotNil(t, res.Clamp, "clamp event must be surfaced for auditing")
	assert.Equal(t, common.QuotaClampOverflow, res.Clamp.Kind)
	assert.Equal(t, math.MaxInt32, res.Clamp.Clamped)
	assert.Equal(t, "QuotaRound", res.Clamp.Op)
}

func TestComputeTieredQuota_UnderflowClamps(t *testing.T) {
	// Negative group ratio drives a large-magnitude negative quota.
	snap := snapFor(`tier("base", p * 1000000000)`, "base", -1.0, 500_000)
	res, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{P: 1_000_000_000})
	require.NoError(t, err)
	assert.Equal(t, math.MinInt32, res.ActualQuotaAfterGroup)
	require.NotNil(t, res.Clamp)
	assert.Equal(t, common.QuotaClampUnderflow, res.Clamp.Kind)
}

func TestComputeTieredQuota_NaNClampsToZero(t *testing.T) {
	// cost = 0/0 = NaN → quota NaN → clamp to 0 with NaN kind.
	snap := snapFor(`tier("base", p / c)`, "base", 1.0, 500_000)
	res, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{P: 0, C: 0})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ActualQuotaAfterGroup)
	require.NotNil(t, res.Clamp)
	assert.Equal(t, common.QuotaClampNaN, res.Clamp.Kind)
}

func TestComputeTieredQuota_InRangeNoClamp(t *testing.T) {
	snap := snapFor(`tier("base", p * 2 + c * 10)`, "base", 1.0, 500_000)
	res, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{P: 1000, C: 500})
	require.NoError(t, err)
	assert.Nil(t, res.Clamp)
}
