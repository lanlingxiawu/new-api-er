package billing_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// saveBillingSetting snapshots the package-global billingSetting maps and
// restores them (as fresh independent maps) on test cleanup. Every test that
// mutates billingSetting must call this first so tests stay isolated.
func saveBillingSetting(t *testing.T) {
	t.Helper()
	origMode := billingSetting.BillingMode
	origExpr := billingSetting.BillingExpr
	t.Cleanup(func() {
		billingSetting.BillingMode = origMode
		billingSetting.BillingExpr = origExpr
	})
	// Start each test from a clean, non-shared pair of maps.
	billingSetting.BillingMode = make(map[string]string)
	billingSetting.BillingExpr = make(map[string]string)
}

// ---------------------------------------------------------------------------
// GetBillingMode
// ---------------------------------------------------------------------------

func TestGetBillingMode_Found(t *testing.T) {
	saveBillingSetting(t)
	billingSetting.BillingMode["gpt-4o"] = BillingModeTieredExpr

	assert.Equal(t, BillingModeTieredExpr, GetBillingMode("gpt-4o"))
}

func TestGetBillingMode_NotFoundFallsBackToRatio(t *testing.T) {
	saveBillingSetting(t)
	// Model absent from the map => default ratio mode.
	assert.Equal(t, BillingModeRatio, GetBillingMode("unknown-model"))
	assert.Equal(t, "ratio", GetBillingMode("unknown-model"))
}

func TestGetBillingMode_EmptyModelKey(t *testing.T) {
	saveBillingSetting(t)
	// An explicit empty-string entry is a distinct present key.
	billingSetting.BillingMode[""] = BillingModeTieredExpr
	assert.Equal(t, BillingModeTieredExpr, GetBillingMode(""))
}

// ---------------------------------------------------------------------------
// GetBillingExpr
// ---------------------------------------------------------------------------

func TestGetBillingExpr_Found(t *testing.T) {
	saveBillingSetting(t)
	billingSetting.BillingExpr["gpt-4o"] = "p + c"

	expr, ok := GetBillingExpr("gpt-4o")
	assert.True(t, ok)
	assert.Equal(t, "p + c", expr)
}

func TestGetBillingExpr_NotFound(t *testing.T) {
	saveBillingSetting(t)
	expr, ok := GetBillingExpr("missing")
	assert.False(t, ok)
	assert.Equal(t, "", expr)
}

func TestGetBillingExpr_PresentButEmptyValue(t *testing.T) {
	saveBillingSetting(t)
	// Present key with empty value: ok must be true, distinguishing it from absent.
	billingSetting.BillingExpr["empty"] = ""
	expr, ok := GetBillingExpr("empty")
	assert.True(t, ok)
	assert.Equal(t, "", expr)
}

// ---------------------------------------------------------------------------
// GetBillingModeCopy / GetBillingExprCopy — must return independent copies
// ---------------------------------------------------------------------------

func TestGetBillingModeCopy_IndependentFromSource(t *testing.T) {
	saveBillingSetting(t)
	billingSetting.BillingMode["a"] = BillingModeRatio
	billingSetting.BillingMode["b"] = BillingModeTieredExpr

	cp := GetBillingModeCopy()
	require.Equal(t, map[string]string{"a": BillingModeRatio, "b": BillingModeTieredExpr}, cp)

	// Mutating the copy must not affect the source.
	cp["a"] = "mutated"
	assert.Equal(t, BillingModeRatio, billingSetting.BillingMode["a"])
}

func TestGetBillingModeCopy_Empty(t *testing.T) {
	saveBillingSetting(t)
	cp := GetBillingModeCopy()
	assert.Empty(t, cp)
	assert.NotNil(t, cp)
}

func TestGetBillingExprCopy_IndependentFromSource(t *testing.T) {
	saveBillingSetting(t)
	billingSetting.BillingExpr["x"] = "p + c"

	cp := GetBillingExprCopy()
	require.Equal(t, map[string]string{"x": "p + c"}, cp)

	cp["x"] = "mutated"
	assert.Equal(t, "p + c", billingSetting.BillingExpr["x"])
}

func TestGetBillingExprCopy_Empty(t *testing.T) {
	saveBillingSetting(t)
	cp := GetBillingExprCopy()
	assert.Empty(t, cp)
	assert.NotNil(t, cp)
}

// ---------------------------------------------------------------------------
// GetPricingSyncData — decision/path coverage over the two len(...)>0 guards
// ---------------------------------------------------------------------------

func TestGetPricingSyncData_BothEmpty_ReturnsBaseOnly(t *testing.T) {
	saveBillingSetting(t)
	base := map[string]any{"foo": 1}

	out := GetPricingSyncData(base)
	assert.Equal(t, 1, out["foo"])
	_, hasMode := out[BillingModeField]
	_, hasExpr := out[BillingExprField]
	assert.False(t, hasMode)
	assert.False(t, hasExpr)
}

func TestGetPricingSyncData_OnlyModePopulated(t *testing.T) {
	saveBillingSetting(t)
	billingSetting.BillingMode["m"] = BillingModeTieredExpr

	out := GetPricingSyncData(map[string]any{})
	modes, ok := out[BillingModeField].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, BillingModeTieredExpr, modes["m"])
	_, hasExpr := out[BillingExprField]
	assert.False(t, hasExpr)
}

func TestGetPricingSyncData_OnlyExprPopulated(t *testing.T) {
	saveBillingSetting(t)
	billingSetting.BillingExpr["m"] = "p + c"

	out := GetPricingSyncData(map[string]any{})
	exprs, ok := out[BillingExprField].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "p + c", exprs["m"])
	_, hasMode := out[BillingModeField]
	assert.False(t, hasMode)
}

func TestGetPricingSyncData_BothPopulated_MergesWithBase(t *testing.T) {
	saveBillingSetting(t)
	billingSetting.BillingMode["m"] = BillingModeTieredExpr
	billingSetting.BillingExpr["m"] = "p + c"

	base := map[string]any{"base_key": "keep"}
	out := GetPricingSyncData(base)

	assert.Equal(t, "keep", out["base_key"])
	assert.Equal(t, map[string]string{"m": BillingModeTieredExpr}, out[BillingModeField])
	assert.Equal(t, map[string]string{"m": "p + c"}, out[BillingExprField])
}

func TestGetPricingSyncData_DoesNotMutateBase(t *testing.T) {
	saveBillingSetting(t)
	billingSetting.BillingMode["m"] = BillingModeRatio

	base := map[string]any{"a": 1}
	_ = GetPricingSyncData(base)
	// lo.Assign builds a new map; the caller's base stays untouched.
	_, injected := base[BillingModeField]
	assert.False(t, injected)
	assert.Len(t, base, 1)
}

func TestGetPricingSyncData_NilBase(t *testing.T) {
	saveBillingSetting(t)
	billingSetting.BillingMode["m"] = BillingModeRatio

	out := GetPricingSyncData(nil)
	require.NotNil(t, out)
	assert.Equal(t, map[string]string{"m": BillingModeRatio}, out[BillingModeField])
}

// ---------------------------------------------------------------------------
// SmokeTestExpr / smokeTestExpr
// ---------------------------------------------------------------------------

func TestSmokeTestExpr_ValidExpressionPasses(t *testing.T) {
	// p+c is >=0 for every non-negative token vector => no error.
	require.NoError(t, SmokeTestExpr("p + c"))
}

func TestSmokeTestExpr_ConstantZeroPasses(t *testing.T) {
	// result == 0 is not < 0, so the boundary passes.
	require.NoError(t, SmokeTestExpr("0"))
}

func TestSmokeTestExpr_UsesRequestAwareVariables(t *testing.T) {
	// len is exercised by the smoke vectors; a len-based expression must still
	// compile and evaluate non-negative across all vectors.
	require.NoError(t, SmokeTestExpr("len * 0.001 + p"))
}

func TestSmokeTestExpr_CompileErrorReturnsError(t *testing.T) {
	// Syntactically invalid expression => compile failure surfaces as error.
	err := SmokeTestExpr("p + ")
	require.Error(t, err)
}

func TestSmokeTestExpr_NegativeResultReturnsError(t *testing.T) {
	// A constant-negative expression trips the `result < 0` guard.
	err := SmokeTestExpr("-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "< 0")
}

func TestSmokeTestExpr_NegativeOnlyForLargeVectorsStillFails(t *testing.T) {
	// Non-negative for the {0,0} vector but negative once tokens grow: the loop
	// must reject it on a later vector, proving all vectors are evaluated.
	err := SmokeTestExpr("100 - p")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "< 0")
}

func TestSmokeTestExpr_PublicWrapperMatchesPrivate(t *testing.T) {
	// The exported wrapper simply delegates; behaviour must match.
	assert.Equal(t, smokeTestExpr("p + c") == nil, SmokeTestExpr("p + c") == nil)
	assert.Equal(t, smokeTestExpr("-1") == nil, SmokeTestExpr("-1") == nil)
}
