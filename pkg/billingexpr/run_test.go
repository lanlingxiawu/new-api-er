package billingexpr_test

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runOK is a helper: run expr, require no error, return cost + trace.
func runOK(t *testing.T, expr string, p billingexpr.TokenParams) (float64, billingexpr.TraceResult) {
	t.Helper()
	cost, trace, err := billingexpr.RunExpr(expr, p)
	require.NoError(t, err)
	return cost, trace
}

// ---------------------------------------------------------------------------
// Token variables — each dimension priced independently (run.go env wiring).
// Values are pinned exactly; all inputs are integer-valued floats and the
// coefficients are exactly representable, so products/sums are exact < 2^53.
// ---------------------------------------------------------------------------

func TestRunExpr_EachTokenVariable(t *testing.T) {
	tests := []struct {
		name   string
		expr   string
		params billingexpr.TokenParams
		want   float64
	}{
		{"p only", "p * 2.5", billingexpr.TokenParams{P: 1000}, 2500},
		{"c only", "c * 15", billingexpr.TokenParams{C: 200}, 3000},
		{"cr cache read", "cr * 0.5", billingexpr.TokenParams{CR: 1000}, 500},
		{"cc cache creation", "cc * 3.75", billingexpr.TokenParams{CC: 400}, 1500},
		{"cc1h 1h cache", "cc1h * 6", billingexpr.TokenParams{CC1h: 100}, 600},
		{"img image input", "img * 2", billingexpr.TokenParams{Img: 250}, 500},
		{"img_o image output", "img_o * 30", billingexpr.TokenParams{ImgO: 10}, 300},
		{"ai audio input", "ai * 3.81", billingexpr.TokenParams{AI: 100}, 381},
		{"ao audio output", "ao * 15.11", billingexpr.TokenParams{AO: 100}, 1511},
		{"len used as value", "len * 1", billingexpr.TokenParams{Len: 12345}, 12345},
		{"all combined", "p*3 + c*15 + cr*0.3 + cc*3.75 + cc1h*6 + img*3 + img_o*30 + ai*10 + ao*40",
			billingexpr.TokenParams{P: 100, C: 50, CR: 20, CC: 8, CC1h: 4, Img: 2, ImgO: 1, AI: 5, AO: 3},
			100*3 + 50*15 + 20*0.3 + 8*3.75 + 4*6 + 2*3 + 1*30 + 5*10 + 3*40},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost, _ := runOK(t, tt.expr, tt.params)
			assert.InDelta(t, tt.want, cost, 1e-9)
		})
	}
}

// Absent (zero) sub-category tokens contribute nothing — a cache-aware
// expression run with zero cache tokens equals its p/c-only baseline.
func TestRunExpr_AbsentTokensAreZero(t *testing.T) {
	const e = `tier("b", p * 3 + c * 15 + cr * 0.3 + cc * 3.75 + img * 2 + ai * 5 + ao * 9)`
	cost, _ := runOK(t, e, billingexpr.TokenParams{P: 1000, C: 500})
	assert.InDelta(t, 1000*3+500*15, cost, 1e-9)
}

// ---------------------------------------------------------------------------
// tier() — trace side channel (run.go).
// ---------------------------------------------------------------------------

func TestRunExpr_TierTrace(t *testing.T) {
	t.Run("records name and cost", func(t *testing.T) {
		cost, trace := runOK(t, `tier("premium", p * 4)`, billingexpr.TokenParams{P: 250})
		assert.Equal(t, 1000.0, cost)
		assert.Equal(t, "premium", trace.MatchedTier)
		assert.Equal(t, 1000.0, trace.Cost)
	})

	t.Run("no tier call leaves trace empty", func(t *testing.T) {
		_, trace := runOK(t, "p * 0.5 + c", billingexpr.TokenParams{P: 100, C: 10})
		assert.Equal(t, "", trace.MatchedTier)
		assert.Equal(t, 0.0, trace.Cost)
	})

	t.Run("last tier wins when multiple branches", func(t *testing.T) {
		// Only the selected branch's tier() executes.
		_, trace := runOK(t, `len <= 100 ? tier("lo", p) : tier("hi", p*2)`, billingexpr.TokenParams{P: 50, Len: 500})
		assert.Equal(t, "hi", trace.MatchedTier)
	})
}

// ---------------------------------------------------------------------------
// header() — normalized, case-insensitive lookup (run.go + normalizeHeaders).
// ---------------------------------------------------------------------------

func TestRunExpr_HeaderFunction(t *testing.T) {
	req := billingexpr.RequestInput{Headers: map[string]string{
		"Anthropic-Beta": "  fast-mode  ",
		"X-Empty":        "",
		"":               "orphan",
	}}
	tests := []struct {
		name string
		expr string
		want float64
	}{
		{"case-insensitive match (trimmed value)", `header("anthropic-beta") == "fast-mode" ? 2 : 1`, 2},
		{"key trimmed + lowercased on lookup", `header("  ANTHROPIC-BETA  ") == "fast-mode" ? 2 : 1`, 2},
		{"missing header returns empty string", `header("x-missing") == "" ? 2 : 1`, 2},
		{"empty-value header dropped → empty", `header("x-empty") == "" ? 2 : 1`, 2},
		{"has() substring on header", `has(header("anthropic-beta"), "fast") ? 3 : 1`, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost, _, err := billingexpr.RunExprWithRequest(tt.expr, billingexpr.TokenParams{}, req)
			require.NoError(t, err)
			assert.Equal(t, tt.want, cost)
		})
	}
}

func TestRunExpr_NoHeadersMap(t *testing.T) {
	// normalizeHeaders(nil) → empty map; header() returns "".
	cost, _, err := billingexpr.RunExprWithRequest(`header("any") == "" ? 5 : 1`, billingexpr.TokenParams{}, billingexpr.RequestInput{})
	require.NoError(t, err)
	assert.Equal(t, 5.0, cost)
}

// ---------------------------------------------------------------------------
// param() — gjson body probe (run.go).
// ---------------------------------------------------------------------------

func TestRunExpr_ParamFunction(t *testing.T) {
	body := []byte(`{"service_tier":"fast","nested":{"flag":true},"arr":[1,2,3],"n":5}`)
	req := billingexpr.RequestInput{Body: body}
	tests := []struct {
		name string
		expr string
		want float64
	}{
		{"string equality", `param("service_tier") == "fast" ? 2 : 1`, 2},
		{"nested bool", `param("nested.flag") == true ? 3 : 1`, 3},
		{"array count via #", `param("arr.#") > 2 ? 4 : 1`, 4},
		{"numeric compare", `param("n") > 3 ? 7 : 1`, 7},
		{"missing path returns nil", `param("does.not.exist") == nil ? 9 : 1`, 9},
		{"empty path returns nil", `param("") == nil ? 9 : 1`, 9},
		{"whitespace path trimmed to empty → nil", `param("   ") == nil ? 9 : 1`, 9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost, _, err := billingexpr.RunExprWithRequest(tt.expr, billingexpr.TokenParams{}, req)
			require.NoError(t, err)
			assert.Equal(t, tt.want, cost)
		})
	}
}

func TestRunExpr_ParamEmptyBodyReturnsNil(t *testing.T) {
	cost, _, err := billingexpr.RunExprWithRequest(`param("x") == nil ? 1 : 0`, billingexpr.TokenParams{}, billingexpr.RequestInput{})
	require.NoError(t, err)
	assert.Equal(t, 1.0, cost)
}

// ---------------------------------------------------------------------------
// has() — substring predicate (run.go).
// ---------------------------------------------------------------------------

func TestRunExpr_HasFunction(t *testing.T) {
	tests := []struct {
		name string
		expr string
		req  billingexpr.RequestInput
		want float64
	}{
		{"match true", `has("fast-mode-2026", "fast") ? 1 : 0`, billingexpr.RequestInput{}, 1},
		{"no match false", `has("standard", "fast") ? 1 : 0`, billingexpr.RequestInput{}, 0},
		{"empty substr false", `has("anything", "") ? 1 : 0`, billingexpr.RequestInput{}, 0},
		{"nil source false", `has(param("missing"), "x") ? 1 : 0`, billingexpr.RequestInput{Body: []byte(`{}`)}, 0},
		{"numeric source stringified", `has(param("n"), "5") ? 1 : 0`, billingexpr.RequestInput{Body: []byte(`{"n":5}`)}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost, _, err := billingexpr.RunExprWithRequest(tt.expr, billingexpr.TokenParams{}, tt.req)
			require.NoError(t, err)
			assert.Equal(t, tt.want, cost)
		})
	}
}

// ---------------------------------------------------------------------------
// Math helpers (run.go): max, min, abs, ceil, floor.
// ---------------------------------------------------------------------------

func TestRunExpr_MathHelpers(t *testing.T) {
	tests := []struct {
		name string
		expr string
		p    billingexpr.TokenParams
		want float64
	}{
		{"max picks larger", "max(p, c)", billingexpr.TokenParams{P: 300, C: 500}, 500},
		{"min picks smaller", "min(p, c)", billingexpr.TokenParams{P: 300, C: 500}, 300},
		{"abs of negative diff", "abs(p - c)", billingexpr.TokenParams{P: 100, C: 250}, 150},
		{"ceil rounds up", "ceil(p / 1000)", billingexpr.TokenParams{P: 1500}, 2},
		{"floor rounds down", "floor(p / 1000)", billingexpr.TokenParams{P: 1500}, 1},
		{"ceil exact integer unchanged", "ceil(p / 1000)", billingexpr.TokenParams{P: 2000}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost, _ := runOK(t, tt.expr, tt.p)
			assert.Equal(t, tt.want, cost)
		})
	}
}

// ---------------------------------------------------------------------------
// Time helpers (run.go + timeInZone): valid / invalid / empty timezone.
// Deterministic assertions only use range invariants, never wall-clock values.
// ---------------------------------------------------------------------------

func TestRunExpr_TimeHelpers_Ranges(t *testing.T) {
	// All time functions return in-range values → these predicates are always
	// true regardless of when the test runs. Covers hour/minute/weekday/month/day.
	const e = `(hour("UTC") >= 0 && hour("UTC") <= 23) &&
		(minute("UTC") >= 0 && minute("UTC") <= 59) &&
		(weekday("UTC") >= 0 && weekday("UTC") <= 6) &&
		(month("UTC") >= 1 && month("UTC") <= 12) &&
		(day("UTC") >= 1 && day("UTC") <= 31) ? 1.0 : 0.0`
	cost, _ := runOK(t, e, billingexpr.TokenParams{})
	assert.Equal(t, 1.0, cost, "all time functions must return in-range values")
}

func TestRunExpr_TimeHelpers_TimezoneFallback(t *testing.T) {
	// Empty and invalid timezones both fall back to UTC (still valid 0-23 hour).
	for _, tz := range []string{"", "Not/AZone", "   "} {
		e := `hour("` + tz + `") >= 0 ? 1.0 : 2.0`
		cost, _ := runOK(t, e, billingexpr.TokenParams{})
		assert.Equalf(t, 1.0, cost, "tz=%q must fall back to UTC", tz)
	}
}

func TestRunExpr_TimeHelpers_ValidNamedZone(t *testing.T) {
	// A valid named zone loads without error; hour still in range.
	cost, _ := runOK(t, `hour("Asia/Shanghai") >= 0 ? 1.0 : 2.0`, billingexpr.TokenParams{})
	assert.Equal(t, 1.0, cost)
}

// ---------------------------------------------------------------------------
// Error paths (run.go runProgram): compile error and RUNTIME error.
// ---------------------------------------------------------------------------

func TestRunExpr_CompileErrorPropagates(t *testing.T) {
	cost, trace, err := billingexpr.RunExpr("(p + c", billingexpr.TokenParams{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expr compile error")
	assert.Equal(t, 0.0, cost)
	assert.Equal(t, billingexpr.TraceResult{}, trace)
}

func TestRunExpr_RuntimeErrorPropagates(t *testing.T) {
	// param("x") is nil (missing) → float64 * nil is a RUNTIME type error the
	// compile-time checker cannot catch (param returns interface{}).
	cost, _, err := billingexpr.RunExprWithRequest("p * param(\"x\")", billingexpr.TokenParams{P: 5}, billingexpr.RequestInput{Body: []byte(`{}`)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expr run error")
	assert.Equal(t, 0.0, cost)
}

// ---------------------------------------------------------------------------
// RunExprByHash variants (run.go) — hash-keyed cache lookup.
// ---------------------------------------------------------------------------

func TestRunExprByHash(t *testing.T) {
	const e = "p * 2 + c * 3"
	hash := billingexpr.ExprHashString(e)

	t.Run("by hash matches by string", func(t *testing.T) {
		got, _, err := billingexpr.RunExprByHash(e, hash, billingexpr.TokenParams{P: 10, C: 20})
		require.NoError(t, err)
		want, _, err := billingexpr.RunExpr(e, billingexpr.TokenParams{P: 10, C: 20})
		require.NoError(t, err)
		assert.Equal(t, want, got)
		assert.Equal(t, 10*2+20*3.0, got)
	})

	t.Run("by hash with request", func(t *testing.T) {
		exprS := `param("k") == "v" ? tier("t", p * 2) : tier("f", p)`
		h := billingexpr.ExprHashString(exprS)
		got, trace, err := billingexpr.RunExprByHashWithRequest(exprS, h, billingexpr.TokenParams{P: 100}, billingexpr.RequestInput{Body: []byte(`{"k":"v"}`)})
		require.NoError(t, err)
		assert.Equal(t, 200.0, got)
		assert.Equal(t, "t", trace.MatchedTier)
	})

	t.Run("compile error surfaces via hash path", func(t *testing.T) {
		bad := "(p + c"
		_, _, err := billingexpr.RunExprByHash(bad, billingexpr.ExprHashString(bad), billingexpr.TokenParams{})
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// Multiple request rules compose (header * param), as used by |||-appended
// request-rule multipliers documented in expr.md.
// ---------------------------------------------------------------------------

func TestRunExpr_RequestRuleComposition(t *testing.T) {
	cost, _, err := billingexpr.RunExprWithRequest(
		`tier("base", p * 5 + c * 25) * (has(header("anthropic-beta"), "fast-mode") ? 6 : 1)`,
		billingexpr.TokenParams{P: 100, C: 20},
		billingexpr.RequestInput{Headers: map[string]string{"Anthropic-Beta": "fast-mode"}},
	)
	require.NoError(t, err)
	base := 100*5 + 20*25.0
	assert.Equal(t, base*6, cost)
}
