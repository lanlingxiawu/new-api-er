package types

import (
	"math"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// isValidOtherRatio — boundary / equivalence
// ---------------------------------------------------------------------------

func TestIsValidOtherRatio(t *testing.T) {
	cases := []struct {
		name  string
		ratio float64
		want  bool
	}{
		{"zero is invalid (boundary)", 0, false},
		{"negative is invalid", -1.5, false},
		{"tiny positive is valid", 1e-9, true},
		{"one is valid", 1.0, true},
		{"normal positive is valid", 2.5, true},
		{"positive infinity is invalid", math.Inf(1), false},
		{"negative infinity is invalid", math.Inf(-1), false},
		{"NaN is invalid", math.NaN(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isValidOtherRatio(tc.ratio))
		})
	}
}

// ---------------------------------------------------------------------------
// AddOtherRatio
// ---------------------------------------------------------------------------

func TestAddOtherRatio_ValidLazilyInitsMap(t *testing.T) {
	p := &PriceData{}
	require.Nil(t, p.otherRatios)

	p.AddOtherRatio("web_search", 2.0)
	require.NotNil(t, p.otherRatios)
	require.Equal(t, 2.0, p.otherRatios["web_search"])
}

func TestAddOtherRatio_InvalidSkipped(t *testing.T) {
	p := &PriceData{}
	p.AddOtherRatio("bad", math.NaN())
	p.AddOtherRatio("bad2", 0)
	p.AddOtherRatio("bad3", math.Inf(1))
	require.Nil(t, p.otherRatios, "invalid ratios must not init the map or be stored")
}

func TestAddOtherRatio_Overwrite(t *testing.T) {
	p := &PriceData{}
	p.AddOtherRatio("k", 2.0)
	p.AddOtherRatio("k", 3.0)
	require.Equal(t, 3.0, p.otherRatios["k"])
}

// ---------------------------------------------------------------------------
// ReplaceOtherRatios
// ---------------------------------------------------------------------------

func TestReplaceOtherRatios_ClearsThenSets(t *testing.T) {
	p := &PriceData{}
	p.AddOtherRatio("old", 5.0)

	ok := p.ReplaceOtherRatios(map[string]float64{"a": 2.0, "b": 3.0})
	require.True(t, ok)
	require.Equal(t, map[string]float64{"a": 2.0, "b": 3.0}, p.otherRatios)
	_, exists := p.otherRatios["old"]
	require.False(t, exists, "prior ratios cleared first")
}

func TestReplaceOtherRatios_AllInvalidReturnsFalse(t *testing.T) {
	p := &PriceData{}
	p.AddOtherRatio("old", 5.0)

	ok := p.ReplaceOtherRatios(map[string]float64{"a": 0, "b": math.NaN()})
	require.False(t, ok, "no valid ratios survive -> false")
	require.Empty(t, p.otherRatios)
}

func TestReplaceOtherRatios_EmptyMapReturnsFalse(t *testing.T) {
	p := &PriceData{}
	p.AddOtherRatio("old", 5.0)
	ok := p.ReplaceOtherRatios(map[string]float64{})
	require.False(t, ok)
	require.Empty(t, p.otherRatios)
}

func TestReplaceOtherRatios_NilMapReturnsFalse(t *testing.T) {
	p := &PriceData{}
	ok := p.ReplaceOtherRatios(nil)
	require.False(t, ok)
}

func TestReplaceOtherRatios_MixedKeepsOnlyValid(t *testing.T) {
	p := &PriceData{}
	ok := p.ReplaceOtherRatios(map[string]float64{"good": 2.0, "bad": 0})
	require.True(t, ok)
	require.Equal(t, map[string]float64{"good": 2.0}, p.otherRatios)
}

// ---------------------------------------------------------------------------
// HasOtherRatio
// ---------------------------------------------------------------------------

func TestHasOtherRatio(t *testing.T) {
	p := &PriceData{}
	require.False(t, p.HasOtherRatio("k"), "nil map -> false")

	p.AddOtherRatio("k", 2.0)
	require.True(t, p.HasOtherRatio("k"))
	require.False(t, p.HasOtherRatio("missing"))
}

func TestHasOtherRatio_InvalidStoredValueReturnsFalse(t *testing.T) {
	// Directly inject an invalid ratio (bypassing AddOtherRatio's guard) to
	// exercise HasOtherRatio's isValidOtherRatio check on a present key.
	p := &PriceData{otherRatios: map[string]float64{"k": math.NaN()}}
	require.False(t, p.HasOtherRatio("k"))
}

// ---------------------------------------------------------------------------
// OtherRatios
// ---------------------------------------------------------------------------

func TestOtherRatios_EmptyReturnsNil(t *testing.T) {
	p := &PriceData{}
	require.Nil(t, p.OtherRatios())
}

func TestOtherRatios_ReturnsCopy(t *testing.T) {
	p := &PriceData{}
	p.AddOtherRatio("a", 2.0)

	got := p.OtherRatios()
	require.Equal(t, map[string]float64{"a": 2.0}, got)

	// Mutating the returned map must not affect the source.
	got["a"] = 100
	require.Equal(t, 2.0, p.otherRatios["a"])
}

func TestOtherRatios_FiltersInvalid(t *testing.T) {
	p := &PriceData{otherRatios: map[string]float64{"good": 2.0, "bad": math.Inf(1)}}
	require.Equal(t, map[string]float64{"good": 2.0}, p.OtherRatios())
}

func TestOtherRatios_AllInvalidReturnsNil(t *testing.T) {
	p := &PriceData{otherRatios: map[string]float64{"bad": math.NaN(), "bad2": 0}}
	require.Nil(t, p.OtherRatios(), "non-empty backing map but all invalid -> nil")
}

// ---------------------------------------------------------------------------
// OtherRatioMultiplier
// ---------------------------------------------------------------------------

func TestOtherRatioMultiplier_Empty(t *testing.T) {
	p := &PriceData{}
	require.Equal(t, 1.0, p.OtherRatioMultiplier())
}

func TestOtherRatioMultiplier_SkipsOneAndInvalid(t *testing.T) {
	p := &PriceData{otherRatios: map[string]float64{
		"a":       2.0,
		"b":       3.0,
		"neutral": 1.0,          // skipped (== 1.0)
		"bad":     math.NaN(),   // skipped (invalid)
		"bad2":    math.Inf(1),  // skipped (invalid)
	}}
	require.Equal(t, 6.0, p.OtherRatioMultiplier())
}

func TestOtherRatioMultiplier_OnlyOnesYieldsOne(t *testing.T) {
	p := &PriceData{otherRatios: map[string]float64{"a": 1.0, "b": 1.0}}
	require.Equal(t, 1.0, p.OtherRatioMultiplier())
}

// ---------------------------------------------------------------------------
// ApplyOtherRatiosToFloat / RemoveOtherRatiosFromFloat
// ---------------------------------------------------------------------------

func TestApplyOtherRatiosToFloat(t *testing.T) {
	p := &PriceData{otherRatios: map[string]float64{"a": 2.0, "b": 5.0}}
	require.Equal(t, 100.0, p.ApplyOtherRatiosToFloat(10.0))
}

func TestApplyOtherRatiosToFloat_NoRatios(t *testing.T) {
	p := &PriceData{}
	require.Equal(t, 10.0, p.ApplyOtherRatiosToFloat(10.0))
}

func TestRemoveOtherRatiosFromFloat_RoundTrip(t *testing.T) {
	p := &PriceData{otherRatios: map[string]float64{"a": 2.0, "b": 5.0}}
	applied := p.ApplyOtherRatiosToFloat(10.0)
	require.InDelta(t, 10.0, p.RemoveOtherRatiosFromFloat(applied), 1e-9)
}

func TestRemoveOtherRatiosFromFloat_SkipsOneAndInvalid(t *testing.T) {
	p := &PriceData{otherRatios: map[string]float64{
		"a":       4.0,
		"neutral": 1.0,
		"bad":     math.NaN(),
	}}
	require.Equal(t, 5.0, p.RemoveOtherRatiosFromFloat(20.0))
}

// ---------------------------------------------------------------------------
// ApplyOtherRatiosToDecimal
// ---------------------------------------------------------------------------

func TestApplyOtherRatiosToDecimal(t *testing.T) {
	p := &PriceData{otherRatios: map[string]float64{"a": 2.0, "b": 5.0}}
	got := p.ApplyOtherRatiosToDecimal(decimal.NewFromFloat(10.0))
	require.True(t, got.Equal(decimal.NewFromFloat(100.0)), "got %s", got.String())
}

func TestApplyOtherRatiosToDecimal_SkipsOneAndInvalid(t *testing.T) {
	p := &PriceData{otherRatios: map[string]float64{
		"a":       3.0,
		"neutral": 1.0,
		"bad":     math.Inf(1),
	}}
	got := p.ApplyOtherRatiosToDecimal(decimal.NewFromFloat(10.0))
	require.True(t, got.Equal(decimal.NewFromFloat(30.0)), "got %s", got.String())
}

func TestApplyOtherRatiosToDecimal_NoRatiosUnchanged(t *testing.T) {
	p := &PriceData{}
	got := p.ApplyOtherRatiosToDecimal(decimal.NewFromFloat(7.0))
	require.True(t, got.Equal(decimal.NewFromFloat(7.0)))
}

// ---------------------------------------------------------------------------
// ToSetting
// ---------------------------------------------------------------------------

func TestToSetting_Formatting(t *testing.T) {
	p := &PriceData{
		ModelPrice:           0.5,
		ModelRatio:           2.0,
		CompletionRatio:      3.0,
		CacheRatio:           0.25,
		CacheCreationRatio:   1.25,
		CacheCreation5mRatio: 1.1,
		CacheCreation1hRatio: 1.2,
		ImageRatio:           4.0,
		AudioRatio:           5.0,
		AudioCompletionRatio: 6.0,
		UsePrice:             true,
		QuotaToPreConsume:    123,
		GroupRatioInfo:       GroupRatioInfo{GroupRatio: 0.9},
	}
	s := p.ToSetting()
	assert.Contains(t, s, "ModelPrice: 0.500000")
	assert.Contains(t, s, "ModelRatio: 2.000000")
	assert.Contains(t, s, "CompletionRatio: 3.000000")
	assert.Contains(t, s, "GroupRatio: 0.900000")
	assert.Contains(t, s, "UsePrice: true")
	assert.Contains(t, s, "QuotaToPreConsume: 123")
	assert.Contains(t, s, "ImageRatio: 4.000000")
	assert.Contains(t, s, "AudioRatio: 5.000000")
	assert.Contains(t, s, "AudioCompletionRatio: 6.000000")
}
