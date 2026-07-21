package billingexpr_test

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// QuotaRound (round.go → common.QuotaRound): half-away-from-zero + int32
// saturation. Billing rounding MUST be deterministic and never wrap.
//
// Boundary note: the clamp uses `value >= MaxQuota`, so the float exactly equal
// to MaxInt32 (2147483647.0) OVERFLOWS to MaxInt32; the largest non-clamped
// value is 2147483646. Documented in pkg-billingexpr.md.
// ---------------------------------------------------------------------------

func TestQuotaRound(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want int
	}{
		{"zero", 0, 0},
		{"round down below half", 0.4, 0},
		{"exact half rounds away", 0.5, 1},
		{"above half", 0.6, 1},
		{"1.5 → 2 (away)", 1.5, 2},
		{"2.5 → 3 (away, not banker's)", 2.5, 3},
		{"negative half away", -0.5, -1},
		{"negative above half", -0.6, -1},
		{"999.4999 → 999", 999.4999, 999},
		{"999.5 → 1000", 999.5, 1000},
		{"1e9+0.5 → 1e9+1", 1e9 + 0.5, 1e9 + 1},
		{"MinInt32 in range", -2147483647.0, -2147483647},
		{"huge positive saturates", 3.6893488147419103e19, math.MaxInt32},
		{"huge negative saturates", -3.6893488147419103e19, math.MinInt32},
		{"+Inf saturates to MaxInt32", math.Inf(1), math.MaxInt32},
		{"-Inf saturates to MinInt32", math.Inf(-1), math.MinInt32},
		{"NaN clamps to 0", math.NaN(), 0},
		{"exact MaxInt32 float clamps (>=)", 2147483647.0, math.MaxInt32},
		{"largest non-clamped", 2147483646.0, 2147483646},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, billingexpr.QuotaRound(tt.in))
		})
	}
}

// ---------------------------------------------------------------------------
// QuotaRoundStrict (round.go): in-range rounds cleanly; out-of-range returns a
// typed *QuotaClamp error and a zero quota (fail-fast for pre-consume).
// ---------------------------------------------------------------------------

func TestQuotaRoundStrict(t *testing.T) {
	t.Run("in range returns rounded, no error", func(t *testing.T) {
		q, err := billingexpr.QuotaRoundStrict(0.75)
		require.NoError(t, err)
		assert.Equal(t, 1, q)
	})

	t.Run("in-range half away", func(t *testing.T) {
		q, err := billingexpr.QuotaRoundStrict(2.5)
		require.NoError(t, err)
		assert.Equal(t, 3, q)
	})

	t.Run("overflow returns error and zero", func(t *testing.T) {
		q, err := billingexpr.QuotaRoundStrict(1e19)
		require.Error(t, err)
		assert.Equal(t, 0, q, "strict returns 0 on clamp so it never silently saturates")
		assert.Contains(t, err.Error(), "overflow")
	})

	t.Run("NaN returns error and zero", func(t *testing.T) {
		q, err := billingexpr.QuotaRoundStrict(math.NaN())
		require.Error(t, err)
		assert.Equal(t, 0, q)
	})
}
