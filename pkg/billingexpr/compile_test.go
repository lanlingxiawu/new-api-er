package billingexpr_test

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// ParseExprVersion — version-tag extraction (compile.go)
//
// Contract: only the literal "v1:" prefix is recognized. Everything else
// (no prefix, uppercase, "v2:", leading space) yields DefaultExprVersion=1 and
// the ORIGINAL string as the body. This is deliberate: v2 is not implemented,
// so a "v2:" expression keeps its prefix as body and fails to compile later.
// ---------------------------------------------------------------------------

func TestParseExprVersion(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantVersion int
		wantBody    string
		technique   string
	}{
		{"no prefix", `tier("b", p)`, 1, `tier("b", p)`, "equivalence: default"},
		{"v1 prefix stripped", "v1:p * 2", 1, "p * 2", "equivalence: recognized prefix"},
		{"v1 prefix only", "v1:", 1, "", "boundary: empty body"},
		{"empty string", "", 1, "", "boundary: empty input"},
		{"v2 prefix NOT stripped", "v2:p", 1, "v2:p", "equivalence: unrecognized version"},
		{"uppercase V1 not matched", "V1:p", 1, "V1:p", "equivalence: case-sensitive"},
		{"v10 not matched (prefix v1: fails)", "v10:p", 1, "v10:p", "boundary: prefix boundary"},
		{"leading space not matched", " v1:p", 1, " v1:p", "boundary: exact prefix"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotV, gotBody := billingexpr.ParseExprVersion(tt.in)
			assert.Equal(t, tt.wantVersion, gotV, "version")
			assert.Equal(t, tt.wantBody, gotBody, "body")
		})
	}
}

func TestDefaultExprVersion_IsOne(t *testing.T) {
	assert.Equal(t, 1, billingexpr.DefaultExprVersion)
}

// ---------------------------------------------------------------------------
// CompileFromCache / CompileFromCacheByHash — compile + cache (compile.go)
// ---------------------------------------------------------------------------

func TestCompileFromCache_ValidReturnsProgram(t *testing.T) {
	prog, err := billingexpr.CompileFromCache(`tier("base", p * 2.5 + c * 15)`)
	require.NoError(t, err)
	require.NotNil(t, prog)
}

func TestCompileFromCache_CacheHitReturnsSameProgram(t *testing.T) {
	billingexpr.InvalidateCache()
	const e = "p * 3 + c * 9"
	p1, err := billingexpr.CompileFromCache(e)
	require.NoError(t, err)
	p2, err := billingexpr.CompileFromCache(e)
	require.NoError(t, err)
	// Cache hit must return the exact same cached *vm.Program pointer.
	assert.Same(t, p1, p2, "second compile must return the cached program pointer")
}

func TestCompileFromCacheByHash_MatchesUnhashed(t *testing.T) {
	billingexpr.InvalidateCache()
	const e = "p * 4 + c * 8"
	hash := billingexpr.ExprHashString(e)
	p1, err := billingexpr.CompileFromCacheByHash(e, hash)
	require.NoError(t, err)
	p2, err := billingexpr.CompileFromCache(e)
	require.NoError(t, err)
	assert.Same(t, p1, p2, "hash-keyed and string-keyed compile share the cache entry")
}

func TestCompileFromCache_Errors(t *testing.T) {
	tests := []struct {
		name string
		expr string
	}{
		{"empty expression", ""},
		{"syntax garbage", "invalid +-+ syntax"},
		{"unknown variable", "xyz * 2"},
		{"unknown function", "frobnicate(p)"},
		{"unterminated paren", "tier(\"b\", p"},
		{"v2 prefix leaves colon", "v2:p * 2"},
		{"non-float result (string)", `header("x")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog, err := billingexpr.CompileFromCache(tt.expr)
			require.Error(t, err, "expected compile error")
			assert.Nil(t, prog)
			assert.Contains(t, err.Error(), "expr compile error", "error must be wrapped")
		})
	}
}

// TestCompileFromCache_EvictionResetsCache drives the maxCacheSize (256) branch:
// once the cache reaches capacity it is fully reset before inserting the next
// entry. We compile > 256 distinct expressions and confirm compilation keeps
// working (the reset branch executed without breaking correctness).
func TestCompileFromCache_EvictionResetsCache(t *testing.T) {
	billingexpr.InvalidateCache()
	for i := 0; i < 300; i++ {
		e := fmt.Sprintf("p * %d + c", i) // 300 distinct expressions
		prog, err := billingexpr.CompileFromCache(e)
		require.NoErrorf(t, err, "compile %d", i)
		require.NotNil(t, prog)
	}
	// A fresh compile still succeeds after the reset churn.
	prog, err := billingexpr.CompileFromCache("p + c")
	require.NoError(t, err)
	require.NotNil(t, prog)
}

// ---------------------------------------------------------------------------
// ExprVersion (compile.go)
// ---------------------------------------------------------------------------

func TestExprVersion(t *testing.T) {
	t.Run("empty returns default", func(t *testing.T) {
		assert.Equal(t, 1, billingexpr.ExprVersion(""))
	})
	t.Run("uncompiled falls back to ParseExprVersion", func(t *testing.T) {
		billingexpr.InvalidateCache()
		// Not compiled yet → ParseExprVersion path.
		assert.Equal(t, 1, billingexpr.ExprVersion("p * 7 + c"))
	})
	t.Run("compiled reads cached version", func(t *testing.T) {
		billingexpr.InvalidateCache()
		const e = "v1:p * 11"
		_, err := billingexpr.CompileFromCache(e)
		require.NoError(t, err)
		assert.Equal(t, 1, billingexpr.ExprVersion(e))
	})
}

// ---------------------------------------------------------------------------
// UsedVars — AST introspection primitive that drives p/c auto-exclusion.
//
// NOTE: the ACTUAL subtraction of sub-categories from p/c lives in the service
// layer (service/tiered_settle.go BuildTieredTokenParams). This package only
// exposes UsedVars, which reports EVERY identifier referenced — including the
// function name `tier` and token variables — for that layer to consult.
// ---------------------------------------------------------------------------

func TestUsedVars(t *testing.T) {
	billingexpr.InvalidateCache()

	t.Run("empty returns nil", func(t *testing.T) {
		assert.Nil(t, billingexpr.UsedVars(""))
	})

	t.Run("compile error returns nil", func(t *testing.T) {
		assert.Nil(t, billingexpr.UsedVars("xyz +-+"))
	})

	t.Run("detects only referenced token variables", func(t *testing.T) {
		// Uses cr but not img → img tokens must stay in p (per expr.md).
		vars := billingexpr.UsedVars("p * 3 + c * 15 + cr * 0.3")
		require.NotNil(t, vars)
		assert.True(t, vars["p"])
		assert.True(t, vars["c"])
		assert.True(t, vars["cr"])
		assert.False(t, vars["img"], "img not referenced → not excluded from p")
		assert.False(t, vars["cc"])
		assert.False(t, vars["ao"])
	})

	t.Run("all sub-category variables detected", func(t *testing.T) {
		vars := billingexpr.UsedVars(`tier("b", p + c + cr + cc + cc1h + img + img_o + ai + ao + len)`)
		require.NotNil(t, vars)
		for _, v := range []string{"p", "c", "cr", "cc", "cc1h", "img", "img_o", "ai", "ao", "len"} {
			assert.Truef(t, vars[v], "expected %q detected", v)
		}
	})

	t.Run("function names also appear as identifiers", func(t *testing.T) {
		vars := billingexpr.UsedVars(`tier("b", p * 2)`)
		require.NotNil(t, vars)
		assert.True(t, vars["p"])
		assert.True(t, vars["tier"], "tier() function name is an identifier node")
	})

	t.Run("cached result returned on second call", func(t *testing.T) {
		const e = "p * 2 + ao * 50"
		v1 := billingexpr.UsedVars(e)
		v2 := billingexpr.UsedVars(e)
		// same cached map instance
		assert.Equal(t, v1, v2)
		assert.True(t, v2["ao"])
	})
}

// ---------------------------------------------------------------------------
// InvalidateCache (compile.go)
// ---------------------------------------------------------------------------

func TestInvalidateCache_ForcesRecompile(t *testing.T) {
	const e = "p * 0.5 + c * 1.0"
	p1, err := billingexpr.CompileFromCache(e)
	require.NoError(t, err)

	billingexpr.InvalidateCache()

	p2, err := billingexpr.CompileFromCache(e)
	require.NoError(t, err)
	// After invalidation the program is recompiled → a different pointer.
	assert.NotSame(t, p1, p2, "invalidate must drop the cached program")

	// Results remain identical regardless of cache state.
	r1, _, err := billingexpr.RunExpr(e, billingexpr.TokenParams{P: 100, C: 50})
	require.NoError(t, err)
	billingexpr.InvalidateCache()
	r2, _, err := billingexpr.RunExpr(e, billingexpr.TokenParams{P: 100, C: 50})
	require.NoError(t, err)
	assert.Equal(t, r1, r2)
}
