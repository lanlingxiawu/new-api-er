package common

import (
	"errors"
	"math"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// quota_math is billing-critical: quota columns are int32 in the DB, so an
// oversized product must SATURATE (clamp to the int32 range) rather than wrap
// around and flip a charge into a credit. These tests pin the saturation +
// rounding + strict/error policy at every boundary.

// ---------------------------------------------------------------------------
// saturateQuota (white-box) — the shared clamp core.
// ---------------------------------------------------------------------------

func TestSaturateQuota_InRangeTruncatesTowardZero(t *testing.T) {
	q, clamp := saturateQuota(5.9, "op")
	assert.Equal(t, 5, q, "in-range value is truncated toward zero via int()")
	assert.Nil(t, clamp, "no clamp for in-range values")

	q, clamp = saturateQuota(-5.9, "op")
	assert.Equal(t, -5, q, "truncation toward zero for negatives")
	assert.Nil(t, clamp)
}

func TestSaturateQuota_NaN(t *testing.T) {
	q, clamp := saturateQuota(math.NaN(), "QuotaFromFloat")
	assert.Equal(t, 0, q, "NaN falls back to 0")
	require.NotNil(t, clamp)
	assert.Equal(t, QuotaClampNaN, clamp.Kind)
	assert.Equal(t, 0, clamp.Clamped)
	assert.Equal(t, "QuotaFromFloat", clamp.Op)
}

func TestSaturateQuota_OverflowBoundary(t *testing.T) {
	// value == MaxQuota triggers the >= overflow branch (inclusive).
	q, clamp := saturateQuota(float64(MaxQuota), "op")
	assert.Equal(t, MaxQuota, q)
	require.NotNil(t, clamp)
	assert.Equal(t, QuotaClampOverflow, clamp.Kind)

	// Just inside the boundary stays un-clamped.
	q, clamp = saturateQuota(float64(MaxQuota)-1, "op")
	assert.Equal(t, MaxQuota-1, q)
	assert.Nil(t, clamp)

	// Far above.
	q, clamp = saturateQuota(1e18, "op")
	assert.Equal(t, MaxQuota, q)
	require.NotNil(t, clamp)
	assert.Equal(t, QuotaClampOverflow, clamp.Kind)
}

func TestSaturateQuota_UnderflowBoundary(t *testing.T) {
	q, clamp := saturateQuota(float64(MinQuota), "op")
	assert.Equal(t, MinQuota, q)
	require.NotNil(t, clamp)
	assert.Equal(t, QuotaClampUnderflow, clamp.Kind)

	q, clamp = saturateQuota(float64(MinQuota)+1, "op")
	assert.Equal(t, MinQuota+1, q)
	assert.Nil(t, clamp)
}

// ---------------------------------------------------------------------------
// QuotaClamp.Error / AuditMap — nil-safety + shape.
// ---------------------------------------------------------------------------

func TestQuotaClamp_ErrorNilSafe(t *testing.T) {
	var c *QuotaClamp
	assert.Equal(t, "", c.Error())
}

func TestQuotaClamp_ErrorString(t *testing.T) {
	c := &QuotaClamp{Op: "QuotaFromFloat", Kind: QuotaClampOverflow, Original: 1e18, Clamped: MaxQuota}
	msg := c.Error()
	assert.Contains(t, msg, "QuotaFromFloat")
	assert.Contains(t, msg, "overflow")
}

func TestQuotaClamp_AuditMap(t *testing.T) {
	var nilClamp *QuotaClamp
	assert.Nil(t, nilClamp.AuditMap())

	c := &QuotaClamp{Op: "QuotaRound", Kind: QuotaClampNaN, Original: math.NaN(), Clamped: 0}
	m := c.AuditMap()
	require.NotNil(t, m)
	assert.Equal(t, "QuotaRound", m["op"])
	assert.Equal(t, QuotaClampNaN, m["kind"])
	assert.Equal(t, 0, m["clamped"])
	_, hasOriginal := m["original"]
	assert.True(t, hasOriginal)
}

// ---------------------------------------------------------------------------
// QuotaFromFloat family — truncation.
// ---------------------------------------------------------------------------

func TestQuotaFromFloat(t *testing.T) {
	assert.Equal(t, 5, QuotaFromFloat(5.9), "truncates toward zero")
	assert.Equal(t, -5, QuotaFromFloat(-5.9))
	assert.Equal(t, MaxQuota, QuotaFromFloat(1e18), "overflow saturates, does NOT wrap")
	assert.Equal(t, MinQuota, QuotaFromFloat(-1e18))
	assert.Equal(t, 0, QuotaFromFloat(math.NaN()))
}

func TestQuotaFromFloatChecked(t *testing.T) {
	q, clamp := QuotaFromFloatChecked(10.7)
	assert.Equal(t, 10, q)
	assert.Nil(t, clamp)

	q, clamp = QuotaFromFloatChecked(1e18)
	assert.Equal(t, MaxQuota, q)
	require.NotNil(t, clamp)
	assert.Equal(t, "QuotaFromFloat", clamp.Op)
}

func TestQuotaFromFloatStrict(t *testing.T) {
	q, err := QuotaFromFloatStrict(10.7)
	require.NoError(t, err)
	assert.Equal(t, 10, q)

	q, err = QuotaFromFloatStrict(1e18)
	require.Error(t, err, "clamped value must fail-fast, not reach billing")
	assert.Equal(t, 0, q, "strict returns 0 on clamp")
	// The error IS the typed *QuotaClamp.
	var clamp *QuotaClamp
	require.True(t, errors.As(err, &clamp))
	assert.Equal(t, QuotaClampOverflow, clamp.Kind)
}

// ---------------------------------------------------------------------------
// QuotaRound family — half-away-from-zero.
// ---------------------------------------------------------------------------

func TestQuotaRound(t *testing.T) {
	assert.Equal(t, 5, QuotaRound(5.4))
	assert.Equal(t, 6, QuotaRound(5.5), "half away from zero rounds up")
	assert.Equal(t, -6, QuotaRound(-5.5), "half away from zero rounds down for negatives")
	assert.Equal(t, MaxQuota, QuotaRound(1e18))
}

func TestQuotaRoundChecked(t *testing.T) {
	q, clamp := QuotaRoundChecked(5.5)
	assert.Equal(t, 6, q)
	assert.Nil(t, clamp)

	q, clamp = QuotaRoundChecked(1e18)
	assert.Equal(t, MaxQuota, q)
	require.NotNil(t, clamp)
	assert.Equal(t, "QuotaRound", clamp.Op)
}

func TestQuotaRoundStrict(t *testing.T) {
	q, err := QuotaRoundStrict(5.5)
	require.NoError(t, err)
	assert.Equal(t, 6, q)

	q, err = QuotaRoundStrict(-1e18)
	require.Error(t, err)
	assert.Equal(t, 0, q)
	var clamp *QuotaClamp
	require.True(t, errors.As(err, &clamp))
	assert.Equal(t, QuotaClampUnderflow, clamp.Kind)
}

// ---------------------------------------------------------------------------
// QuotaFromDecimal family — decimal rounded (half away from zero) then clamped.
// ---------------------------------------------------------------------------

func TestQuotaFromDecimal(t *testing.T) {
	assert.Equal(t, 6, QuotaFromDecimal(decimal.NewFromFloat(5.5)), "decimal rounded half away from zero")
	assert.Equal(t, 5, QuotaFromDecimal(decimal.NewFromFloat(5.4)))
	assert.Equal(t, MaxQuota, QuotaFromDecimal(decimal.NewFromInt(1).Shift(18)), "overflow saturates")
	assert.Equal(t, MinQuota, QuotaFromDecimal(decimal.NewFromInt(-1).Shift(18)))
}

func TestQuotaFromDecimalChecked(t *testing.T) {
	q, clamp := QuotaFromDecimalChecked(decimal.NewFromFloat(2.5))
	assert.Equal(t, 3, q)
	assert.Nil(t, clamp)

	q, clamp = QuotaFromDecimalChecked(decimal.NewFromInt(1).Shift(18))
	assert.Equal(t, MaxQuota, q)
	require.NotNil(t, clamp)
	assert.Equal(t, "QuotaFromDecimal", clamp.Op)
	assert.Equal(t, QuotaClampOverflow, clamp.Kind)
}
