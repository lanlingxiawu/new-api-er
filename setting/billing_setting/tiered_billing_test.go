package billing_setting

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
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
	origPluginExpr := billingSetting.PluginBillingExpr
	t.Cleanup(func() {
		billingSetting.BillingMode = origMode
		billingSetting.BillingExpr = origExpr
		billingSetting.PluginBillingExpr = origPluginExpr
	})
	// Start each test from clean, non-shared maps.
	billingSetting.BillingMode = make(map[string]string)
	billingSetting.BillingExpr = make(map[string]string)
	billingSetting.PluginBillingExpr = make(map[string]string)
}

// withoutBuiltins drops built-in expression models, which the copy/sync
// accessors always merge in, so assertions only cover configured entries.
func withoutBuiltins(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for model, value := range values {
		if _, builtin := builtinBillingExpr[model]; builtin {
			continue
		}
		out[model] = value
	}
	return out
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
	require.Equal(t, map[string]string{"a": BillingModeRatio, "b": BillingModeTieredExpr}, withoutBuiltins(cp))

	// Mutating the copy must not affect the source.
	cp["a"] = "mutated"
	assert.Equal(t, BillingModeRatio, billingSetting.BillingMode["a"])
}

func TestGetBillingModeCopy_Empty(t *testing.T) {
	saveBillingSetting(t)
	cp := GetBillingModeCopy()
	assert.NotNil(t, cp)
	assert.Empty(t, withoutBuiltins(cp))
}

func TestGetBillingModeCopy_ExplicitModeOverridesBuiltin(t *testing.T) {
	saveBillingSetting(t)
	billingSetting.BillingMode["gpt-6-astra"] = BillingModeRatio

	assert.Equal(t, BillingModeRatio, GetBillingModeCopy()["gpt-6-astra"])
}

func TestGetBillingExprCopy_IndependentFromSource(t *testing.T) {
	saveBillingSetting(t)
	billingSetting.BillingExpr["x"] = "p + c"

	cp := GetBillingExprCopy()
	require.Equal(t, map[string]string{"x": "p + c"}, withoutBuiltins(cp))

	cp["x"] = "mutated"
	assert.Equal(t, "p + c", billingSetting.BillingExpr["x"])
}

func TestGetBillingExprCopy_Empty(t *testing.T) {
	saveBillingSetting(t)
	cp := GetBillingExprCopy()
	assert.NotNil(t, cp)
	assert.Empty(t, withoutBuiltins(cp))
}

// ---------------------------------------------------------------------------
// GetPricingSyncData — decision/path coverage over the two len(...)>0 guards
// ---------------------------------------------------------------------------

// syncedMap extracts a billing map from GetPricingSyncData output with the
// built-in expression models removed; absent keys yield an empty map.
func syncedMap(t *testing.T, out map[string]any, field string) map[string]string {
	t.Helper()
	raw, exists := out[field]
	if !exists {
		return map[string]string{}
	}
	values, ok := raw.(map[string]string)
	require.True(t, ok)
	return withoutBuiltins(values)
}

func TestGetPricingSyncData_BothEmpty_ReturnsBaseOnly(t *testing.T) {
	saveBillingSetting(t)
	base := map[string]any{"foo": 1}

	out := GetPricingSyncData(base)
	assert.Equal(t, 1, out["foo"])
	assert.Empty(t, syncedMap(t, out, BillingModeField))
	assert.Empty(t, syncedMap(t, out, BillingExprField))
}

func TestGetPricingSyncData_OnlyModePopulated(t *testing.T) {
	saveBillingSetting(t)
	billingSetting.BillingMode["m"] = BillingModeTieredExpr

	out := GetPricingSyncData(map[string]any{})
	assert.Equal(t, map[string]string{"m": BillingModeTieredExpr}, syncedMap(t, out, BillingModeField))
	assert.Empty(t, syncedMap(t, out, BillingExprField))
}

func TestGetPricingSyncData_OnlyExprPopulated(t *testing.T) {
	saveBillingSetting(t)
	billingSetting.BillingExpr["m"] = "p + c"

	out := GetPricingSyncData(map[string]any{})
	assert.Equal(t, map[string]string{"m": "p + c"}, syncedMap(t, out, BillingExprField))
	assert.Empty(t, syncedMap(t, out, BillingModeField))
}

func TestGetPricingSyncData_BothPopulated_MergesWithBase(t *testing.T) {
	saveBillingSetting(t)
	billingSetting.BillingMode["m"] = BillingModeTieredExpr
	billingSetting.BillingExpr["m"] = "p + c"

	base := map[string]any{"base_key": "keep"}
	out := GetPricingSyncData(base)

	assert.Equal(t, "keep", out["base_key"])
	assert.Equal(t, map[string]string{"m": BillingModeTieredExpr}, syncedMap(t, out, BillingModeField))
	assert.Equal(t, map[string]string{"m": "p + c"}, syncedMap(t, out, BillingExprField))
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
	assert.Equal(t, map[string]string{"m": BillingModeRatio}, syncedMap(t, out, BillingModeField))
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
	// A constant-negative expression trips the non-negative result guard.
	err := SmokeTestExpr("-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "result must be finite and non-negative")
}

func TestSmokeTestExpr_NegativeOnlyForLargeVectorsStillFails(t *testing.T) {
	// Non-negative for the {0,0} vector but negative once tokens grow: the loop
	// must reject it on a later vector, proving all vectors are evaluated.
	err := SmokeTestExpr("100 - p")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "result must be finite and non-negative")
}

func TestSmokeTestExpr_PublicWrapperMatchesPrivate(t *testing.T) {
	// The exported wrapper simply delegates; behaviour must match.
	assert.Equal(t, smokeTestExpr("p + c") == nil, SmokeTestExpr("p + c") == nil)
	assert.Equal(t, smokeTestExpr("-1") == nil, SmokeTestExpr("-1") == nil)
}

func TestSmokeTestTaskExprValidatesDeclaredUsageVectors(t *testing.T) {
	videoSchema := map[string]jsplugin.UsageFieldSchema{
		"seconds": {Type: "number", Unit: "second"},
		"mode":    {Enum: []string{"std", "pro"}},
		"quality": {Enum: []string{"sd", "hd"}},
	}

	tests := []struct {
		name          string
		schema        map[string]jsplugin.UsageFieldSchema
		expression    string
		expectedError string
	}{
		{
			name:          "fixed prices are not task usage prices",
			schema:        videoSchema,
			expression:    `true ? tier("normal", u("seconds") * 0.4) : tier("fixed", fixed(0.01))`,
			expectedError: "fixed pricing is not supported for task usage expressions",
		},
		{
			name:       "declared numeric and enum facts",
			schema:     videoSchema,
			expression: `u("mode") == "pro" ? tier("pro", u("seconds") * 0.8) : tier("std", u("seconds") * 0.4)`,
		},
		{
			name:          "undeclared literal key",
			schema:        videoSchema,
			expression:    `tier("base", u("clips") * 0.1)`,
			expectedError: `usage key "clips" is not declared`,
		},
		{
			name:          "negative duration boundary",
			schema:        videoSchema,
			expression:    fmt.Sprintf(`u("seconds") == %d ? -1 : 0`, relaycommon.MaxTaskDurationSeconds),
			expectedError: "result must be finite and non-negative",
		},
		{
			name:          "negative count boundary",
			schema:        map[string]jsplugin.UsageFieldSchema{"clips": {Type: "number", Unit: "count"}},
			expression:    fmt.Sprintf(`u("clips") == %d ? -1 : 0`, dto.MaxImageN),
			expectedError: "result must be finite and non-negative",
		},
		{
			name:          "negative token boundary",
			schema:        map[string]jsplugin.UsageFieldSchema{"tokens": {Type: "number", Unit: "token"}},
			expression:    fmt.Sprintf(`u("tokens") == %d ? -1 : 0`, common.MaxQuota),
			expectedError: "result must be finite and non-negative",
		},
		{
			name:          "negative credit boundary",
			schema:        map[string]jsplugin.UsageFieldSchema{"units": {Type: "number", Unit: "credit"}},
			expression:    fmt.Sprintf(`u("units") == %d ? -1 : 0`, common.MaxQuota),
			expectedError: "result must be finite and non-negative",
		},
		{
			name:          "negative enum combination",
			schema:        videoSchema,
			expression:    `u("mode") == "pro" && u("quality") == "hd" ? -1 : 0`,
			expectedError: "result must be finite and non-negative",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := SmokeTestTaskExpr(testCase.expression, testCase.schema)
			if testCase.expectedError == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, testCase.expectedError)
		})
	}
}

func TestSmokeTestTaskExprCapsOversizedEnumProductsAtLastCombination(t *testing.T) {
	schema := make(map[string]jsplugin.UsageFieldSchema, 7)
	condition := ""
	for index := range 7 {
		schema[fmt.Sprintf("enum_%d", index)] = jsplugin.UsageFieldSchema{Enum: []string{"first", "middle", "last"}}
		if condition != "" {
			condition += " && "
		}
		condition += fmt.Sprintf(`u("enum_%d") == "last"`, index)
	}

	err := SmokeTestTaskExpr(condition+" ? -1 : 0", schema)
	require.ErrorContains(t, err, "result must be finite and non-negative")
}

func TestSmokeTestExprRejectsTaskUsageWithoutSchema(t *testing.T) {
	err := SmokeTestExpr(`u("mode") == "std" ? 1 : 2`)
	require.Error(t, err)
	assert.ErrorContains(t, err, "mode")
	assert.ErrorContains(t, err, "no task plugin usage schema")

	require.NoError(t, SmokeTestExpr(`tier("base", p * 2 + c * 8)`))
}
