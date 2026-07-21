package billingexpr_test

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Operator precedence & arithmetic (expr-lang grammar as consumed by billing).
// Exact expected values; billing must never mis-evaluate precedence.
// ---------------------------------------------------------------------------

func TestGrammar_OperatorPrecedence(t *testing.T) {
	tests := []struct {
		name string
		expr string
		p    billingexpr.TokenParams
		want float64
	}{
		{"mul before add", "p + c * 2", billingexpr.TokenParams{P: 10, C: 5}, 20},            // 10 + 10
		{"parens override", "(p + c) * 2", billingexpr.TokenParams{P: 10, C: 5}, 30},         // 15 * 2
		{"div before sub", "p - c / 2", billingexpr.TokenParams{P: 10, C: 4}, 8},             // 10 - 2
		{"left assoc sub", "p - c - 1", billingexpr.TokenParams{P: 10, C: 3}, 6},             // (10-3)-1
		{"chained mul", "p * 2 * 3", billingexpr.TokenParams{P: 5}, 30},                      // 5*2*3
		{"mixed", "p * 2 + c * 3 - 1", billingexpr.TokenParams{P: 4, C: 2}, 13},              // 8+6-1
		{"unary negation", "-p + c", billingexpr.TokenParams{P: 5, C: 20}, 15},               // -5+20
		{"modulo via numbers", "floor(p) - floor(p/3)*3", billingexpr.TokenParams{P: 10}, 1}, // 10 - 9
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost, _ := runOK(t, tt.expr, tt.p)
			assert.InDelta(t, tt.want, cost, 1e-9)
		})
	}
}

// ---------------------------------------------------------------------------
// Ternary & comparison operators — the backbone of tier conditions.
// ---------------------------------------------------------------------------

func TestGrammar_TernaryAndComparisons(t *testing.T) {
	tests := []struct {
		name string
		expr string
		p    billingexpr.TokenParams
		want float64
	}{
		{"le true branch", "len <= 100 ? 1 : 2", billingexpr.TokenParams{Len: 100}, 1},
		{"le false branch", "len <= 100 ? 1 : 2", billingexpr.TokenParams{Len: 101}, 2},
		{"lt boundary", "len < 100 ? 1 : 2", billingexpr.TokenParams{Len: 100}, 2},
		{"ge boundary", "len >= 100 ? 1 : 2", billingexpr.TokenParams{Len: 100}, 1},
		{"gt boundary", "len > 100 ? 1 : 2", billingexpr.TokenParams{Len: 100}, 2},
		{"eq", "p == 50 ? 1 : 2", billingexpr.TokenParams{P: 50}, 1},
		{"neq", "p != 50 ? 1 : 2", billingexpr.TokenParams{P: 50}, 2},
		{"nested ternary first", "p < 10 ? 1 : p < 20 ? 2 : 3", billingexpr.TokenParams{P: 5}, 1},
		{"nested ternary middle", "p < 10 ? 1 : p < 20 ? 2 : 3", billingexpr.TokenParams{P: 15}, 2},
		{"nested ternary last", "p < 10 ? 1 : p < 20 ? 2 : 3", billingexpr.TokenParams{P: 25}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost, _ := runOK(t, tt.expr, tt.p)
			assert.Equal(t, tt.want, cost)
		})
	}
}

// Boolean operators: && and || with short-circuit semantics, plus condition
// coverage — each sub-condition of a compound guard exercised independently.
func TestGrammar_BooleanConditionCoverage(t *testing.T) {
	const e = `p < 32000 && c < 200 ? tier("t1", p*2 + c*8) :
		p < 32000 && c >= 200 ? tier("t2", p*3 + c*14) :
		tier("t3", p*4 + c*16)`
	tests := []struct {
		name string
		p    billingexpr.TokenParams
		tier string
		want float64
	}{
		// a=p<32000, b=c<200
		{"a=T b=T → t1", billingexpr.TokenParams{P: 15000, C: 100}, "t1", 15000*2 + 100*8},
		{"a=T b=F → t2", billingexpr.TokenParams{P: 15000, C: 500}, "t2", 15000*3 + 500*14},
		{"a=F (b irrelevant) → t3", billingexpr.TokenParams{P: 50000, C: 100}, "t3", 50000*4 + 100*16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost, trace := runOK(t, e, tt.p)
			assert.Equal(t, tt.tier, trace.MatchedTier)
			assert.InDelta(t, tt.want, cost, 1e-6)
		})
	}
}

func TestGrammar_OrOperator(t *testing.T) {
	// Night-discount pattern with || — pin both branches by forcing the guard.
	on, _ := runOK(t, `(1 == 1 || 2 == 3) ? p * 0.5 : p`, billingexpr.TokenParams{P: 1000})
	assert.Equal(t, 500.0, on)
	off, _ := runOK(t, `(1 == 2 || 2 == 3) ? p * 0.5 : p`, billingexpr.TokenParams{P: 1000})
	assert.Equal(t, 1000.0, off)
}

// ---------------------------------------------------------------------------
// Division-by-zero: float semantics yield ±Inf (NO runtime error). This is a
// billing-relevant edge — an Inf cost later saturates at settlement.
// ---------------------------------------------------------------------------

func TestGrammar_FloatDivByZeroIsInf(t *testing.T) {
	cost, _, err := billingexpr.RunExpr("p / c", billingexpr.TokenParams{P: 5, C: 0})
	require.NoError(t, err, "float div-by-zero must NOT error (yields +Inf)")
	assert.True(t, math.IsInf(cost, 1), "5/0 → +Inf")

	// 0/0 → NaN
	nanCost, _, err := billingexpr.RunExpr("p / c", billingexpr.TokenParams{P: 0, C: 0})
	require.NoError(t, err)
	assert.True(t, math.IsNaN(nanCost), "0/0 → NaN")
}

// ---------------------------------------------------------------------------
// Verbatim examples from expr.md "Expression Examples" — the documented
// contract. Each must compile and evaluate to the hand-computed cost.
// ---------------------------------------------------------------------------

func TestGrammar_ExprMdExamples(t *testing.T) {
	t.Run("simple flat pricing", func(t *testing.T) {
		const e = `tier("base", p * 2.5 + c * 15 + cr * 0.25)`
		cost, trace := runOK(t, e, billingexpr.TokenParams{P: 1000, C: 200, CR: 400})
		assert.InDelta(t, 1000*2.5+200*15+400*0.25, cost, 1e-9)
		assert.Equal(t, "base", trace.MatchedTier)
	})

	t.Run("multi-tier Claude Sonnet using len — standard", func(t *testing.T) {
		const e = `len <= 200000
			? tier("standard", p * 3 + c * 15 + cr * 0.3 + cc * 3.75 + cc1h * 6)
			: tier("long_context", p * 6 + c * 22.5 + cr * 0.6 + cc * 7.5 + cc1h * 12)`
		// len drives the tier; p is lower (cache excluded) but must NOT flip the tier.
		p := billingexpr.TokenParams{P: 50000, C: 5000, Len: 300000, CR: 250000, CC: 1000, CC1h: 500}
		cost, trace := runOK(t, e, p)
		assert.Equal(t, "long_context", trace.MatchedTier, "len=300000 > 200000 → long_context despite low p")
		assert.InDelta(t, 50000*6+5000*22.5+250000*0.6+1000*7.5+500*12, cost, 1e-6)
	})

	t.Run("multi-tier standard branch", func(t *testing.T) {
		const e = `len <= 200000
			? tier("standard", p * 3 + c * 15 + cr * 0.3 + cc * 3.75 + cc1h * 6)
			: tier("long_context", p * 6 + c * 22.5 + cr * 0.6 + cc * 7.5 + cc1h * 12)`
		p := billingexpr.TokenParams{P: 10000, C: 2000, Len: 12000, CR: 500}
		cost, trace := runOK(t, e, p)
		assert.Equal(t, "standard", trace.MatchedTier)
		assert.InDelta(t, 10000*3+2000*15+500*0.3, cost, 1e-6)
	})

	t.Run("image model (cache/audio stay in p/c)", func(t *testing.T) {
		const e = `tier("base", p * 2 + c * 8 + img * 2.5)`
		cost, _ := runOK(t, e, billingexpr.TokenParams{P: 1000, C: 500, Img: 200})
		assert.InDelta(t, 1000*2+500*8+200*2.5, cost, 1e-9)
	})

	t.Run("multimodal with audio", func(t *testing.T) {
		const e = `tier("base", p * 0.43 + c * 3.06 + img * 0.78 + ai * 3.81 + ao * 15.11)`
		p := billingexpr.TokenParams{P: 1000, C: 500, Img: 100, AI: 50, AO: 20}
		cost, _ := runOK(t, e, p)
		assert.InDelta(t, 1000*0.43+500*3.06+100*0.78+50*3.81+20*15.11, cost, 1e-6)
	})
}

// ---------------------------------------------------------------------------
// len vs p — the cache-subtraction trap documented in expr.md: tier conditions
// must key off `len` (full context), not `p` (reduced by cache exclusion).
// ---------------------------------------------------------------------------

func TestGrammar_LenNotReducedByCache(t *testing.T) {
	const e = `len <= 200000 ? tier("standard", p * 3) : tier("long_context", p * 6)`

	// Heavy cache: p reduced to 50k (standard if keyed off p) but len=300k.
	_, trace := runOK(t, e, billingexpr.TokenParams{P: 50000, Len: 300000, CR: 250000})
	assert.Equal(t, "long_context", trace.MatchedTier)

	// Boundary: len exactly 200000 → standard (<=).
	_, trace = runOK(t, e, billingexpr.TokenParams{P: 100000, Len: 200000})
	assert.Equal(t, "standard", trace.MatchedTier)

	// Boundary+1: len 200001 → long_context.
	_, trace = runOK(t, e, billingexpr.TokenParams{P: 100000, Len: 200001})
	assert.Equal(t, "long_context", trace.MatchedTier)

	// len defaults to 0 when unset → standard.
	_, trace = runOK(t, e, billingexpr.TokenParams{P: 1000})
	assert.Equal(t, "standard", trace.MatchedTier)
}

// ---------------------------------------------------------------------------
// Zero / negative / huge token inputs (equivalence + boundary).
// ---------------------------------------------------------------------------

func TestGrammar_EdgeInputs(t *testing.T) {
	const e = `tier("b", p * 3 + c * 15)`

	t.Run("all zero → zero cost", func(t *testing.T) {
		cost, _ := runOK(t, e, billingexpr.TokenParams{})
		assert.Equal(t, 0.0, cost)
	})

	t.Run("negative tokens produce negative cost (no clamp at expr layer)", func(t *testing.T) {
		// The expr engine is pure arithmetic; sign handling is the caller's job.
		cost, _ := runOK(t, e, billingexpr.TokenParams{P: -100, C: -10})
		assert.Equal(t, -100*3.0+-10*15.0, cost)
	})

	t.Run("huge token count stays exact within float53", func(t *testing.T) {
		cost, _ := runOK(t, "p * 3", billingexpr.TokenParams{P: 1_000_000_000})
		assert.Equal(t, 3e9, cost)
	})
}
