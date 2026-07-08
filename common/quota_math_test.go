package common

import (
	"math"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

// 2000 quota per call * n=18446744073686646784 overflows int64; the constant
// below reproduces that oversized product for the saturation checks.
const overflowingProduct = 2000 * 1.8446744073686647e19

// TestQuotaFromFloat guards the billing invariant that oversized quota
// products (e.g. price multiplied by a huge user-supplied count) saturate
// instead of wrapping into a negative charge (credit). QuotaFromFloat
// truncates toward zero.
func TestQuotaFromFloat(t *testing.T) {
	assert.Equal(t, 42, QuotaFromFloat(42.4))
	assert.Equal(t, 42, QuotaFromFloat(42.9))
	assert.Equal(t, -42, QuotaFromFloat(-42.9))
	assert.Equal(t, MaxQuota, QuotaFromFloat(overflowingProduct))
	assert.Equal(t, MinQuota, QuotaFromFloat(-overflowingProduct))
	assert.Equal(t, MaxQuota, QuotaFromFloat(math.Inf(1)))
	assert.Equal(t, MinQuota, QuotaFromFloat(math.Inf(-1)))
	assert.Equal(t, 0, QuotaFromFloat(math.NaN()))
}

// TestQuotaRound checks half-away-from-zero rounding with the same
// saturation policy.
func TestQuotaRound(t *testing.T) {
	assert.Equal(t, 42, QuotaRound(41.5))
	assert.Equal(t, 43, QuotaRound(42.5))
	assert.Equal(t, -43, QuotaRound(-42.5))
	assert.Equal(t, MaxQuota, QuotaRound(overflowingProduct))
	assert.Equal(t, MinQuota, QuotaRound(-overflowingProduct))
	assert.Equal(t, 0, QuotaRound(math.NaN()))
}

// TestQuotaFromDecimal checks the decimal entry point rounds and saturates
// consistently with the float variants.
func TestQuotaFromDecimal(t *testing.T) {
	assert.Equal(t, 43, QuotaFromDecimal(decimal.NewFromFloat(42.5)))
	assert.Equal(t, 42, QuotaFromDecimal(decimal.NewFromFloat(41.7)))
	assert.Equal(t, MaxQuota, QuotaFromDecimal(decimal.NewFromInt(2000).Mul(decimal.NewFromFloat(1.8446744073686647e19))))
	assert.Equal(t, MinQuota, QuotaFromDecimal(decimal.NewFromInt(-2000).Mul(decimal.NewFromFloat(1.8446744073686647e19))))
}

// TestQuotaFromFloatChecked verifies the clamp descriptor is nil in range and
// carries the correct kind/clamped value on saturation, so billing callers can
// audit the event.
func TestQuotaFromFloatChecked(t *testing.T) {
	quota, clamp := QuotaFromFloatChecked(42.9)
	assert.Equal(t, 42, quota)
	assert.Nil(t, clamp)

	quota, clamp = QuotaFromFloatChecked(overflowingProduct)
	assert.Equal(t, MaxQuota, quota)
	if assert.NotNil(t, clamp) {
		assert.Equal(t, "QuotaFromFloat", clamp.Op)
		assert.Equal(t, QuotaClampOverflow, clamp.Kind)
		assert.Equal(t, MaxQuota, clamp.Clamped)
	}

	quota, clamp = QuotaFromFloatChecked(-overflowingProduct)
	assert.Equal(t, MinQuota, quota)
	if assert.NotNil(t, clamp) {
		assert.Equal(t, QuotaClampUnderflow, clamp.Kind)
		assert.Equal(t, MinQuota, clamp.Clamped)
	}

	quota, clamp = QuotaFromFloatChecked(math.NaN())
	assert.Equal(t, 0, quota)
	if assert.NotNil(t, clamp) {
		assert.Equal(t, QuotaClampNaN, clamp.Kind)
		assert.Equal(t, 0, clamp.Clamped)
	}
}

// TestQuotaRoundChecked verifies the rounding entry point reports clamps the
// same way.
func TestQuotaRoundChecked(t *testing.T) {
	quota, clamp := QuotaRoundChecked(42.5)
	assert.Equal(t, 43, quota)
	assert.Nil(t, clamp)

	quota, clamp = QuotaRoundChecked(overflowingProduct)
	assert.Equal(t, MaxQuota, quota)
	if assert.NotNil(t, clamp) {
		assert.Equal(t, "QuotaRound", clamp.Op)
		assert.Equal(t, QuotaClampOverflow, clamp.Kind)
	}
}

// TestQuotaFromDecimalChecked verifies the decimal entry point reports clamps.
func TestQuotaFromDecimalChecked(t *testing.T) {
	quota, clamp := QuotaFromDecimalChecked(decimal.NewFromFloat(41.7))
	assert.Equal(t, 42, quota)
	assert.Nil(t, clamp)

	quota, clamp = QuotaFromDecimalChecked(decimal.NewFromInt(2000).Mul(decimal.NewFromFloat(1.8446744073686647e19)))
	assert.Equal(t, MaxQuota, quota)
	if assert.NotNil(t, clamp) {
		assert.Equal(t, "QuotaFromDecimal", clamp.Op)
		assert.Equal(t, QuotaClampOverflow, clamp.Kind)
	}
}

// TestRoundProductToQuota guards the commission/cost settlement invariant: a
// base quota multiplied by a ratio/rate must round half-away-from-zero with
// decimal precision AND saturate to the int32 quota policy bound, so an
// oversized cost_ratio / commission rate can never persist an out-of-range or
// wrapped commission/cost quota. Shared by the ledger, admin pages, and export
// so all three round identically.
func TestRoundProductToQuota(t *testing.T) {
	// In-range, half-away-from-zero rounding (1000 * 0.0555 = 55.5 -> 56).
	assert.Equal(t, int64(56), RoundProductToQuota(1000, 0.0555))
	assert.Equal(t, int64(-56), RoundProductToQuota(-1000, 0.0555))
	// Exact and zero cases.
	assert.Equal(t, int64(50), RoundProductToQuota(1000, 0.05))
	assert.Equal(t, int64(0), RoundProductToQuota(1000, 0))
	assert.Equal(t, int64(0), RoundProductToQuota(0, 0.05))
	// Oversized ratio saturates instead of overflowing int32.
	assert.Equal(t, int64(MaxQuota), RoundProductToQuota(1_000_000_000, 1000))
	assert.Equal(t, int64(MinQuota), RoundProductToQuota(-1_000_000_000, 1000))
}

// TestRoundProductToQuotaChecked verifies the settlement helper surfaces the
// clamp descriptor (Op="QuotaFromDecimal") so billing callers can audit an
// oversized-ratio saturation event.
func TestRoundProductToQuotaChecked(t *testing.T) {
	q, clamp := RoundProductToQuotaChecked(1000, 0.0555)
	assert.Equal(t, int64(56), q)
	assert.Nil(t, clamp)

	q, clamp = RoundProductToQuotaChecked(1_000_000_000, 1000)
	assert.Equal(t, int64(MaxQuota), q)
	if assert.NotNil(t, clamp) {
		assert.Equal(t, "QuotaFromDecimal", clamp.Op)
		assert.Equal(t, QuotaClampOverflow, clamp.Kind)
		assert.Equal(t, MaxQuota, clamp.Clamped)
	}

	q, clamp = RoundProductToQuotaChecked(-1_000_000_000, 1000)
	assert.Equal(t, int64(MinQuota), q)
	if assert.NotNil(t, clamp) {
		assert.Equal(t, QuotaClampUnderflow, clamp.Kind)
	}
}
