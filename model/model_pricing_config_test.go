package model

import (
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestModelPricingDraftAtomicity(t *testing.T) {
	usePricingOptionsDB(t)
	snapshot, err := GetModelPricingSnapshot([]string{"draft-new", "draft-other"})
	require.NoError(t, err)
	require.Len(t, snapshot.Entries, 2)
	draft := PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": `tier("base", p * 0 + c * 2)`}
	changes := []ModelPricingChange{{ModelName: "draft-new", ExpectedVersion: snapshot.EmptyVersion, Pricing: draft}}
	require.NoError(t, UpdateModelPricing(changes))
	require.Equal(t, int64(1), GetPricingConfigVersion())
	require.ErrorIs(t, UpdateModelPricing(changes), ErrModelPricingConflict)
	after, err := GetModelPricingSnapshot([]string{"draft-new"})
	require.NoError(t, err)
	require.Equal(t, draft, after.Entries[0].Configured)
	require.Error(t, UpdateModelPricing([]ModelPricingChange{
		{ModelName: "draft-other", ExpectedVersion: snapshot.EmptyVersion, Pricing: PricingValues{"ModelPrice": float64(0)}},
		{ModelName: "draft-new", ExpectedVersion: after.Entries[0].Version, Pricing: PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": "bad("}},
	}))
	untouched, err := GetModelPricingSnapshot([]string{"draft-other"})
	require.NoError(t, err)
	require.Empty(t, untouched.Entries[0].Configured)
}

func TestModelPricingLegacyWritersAndGuard(t *testing.T) {
	usePricingOptionsDB(t)
	require.NoError(t, UpdateOptionsBulk(map[string]string{"billing_setting.billing_mode": `{"draft":"tiered_expr"}`, "billing_setting.billing_expr": `{"draft":"p * 2"}`}))
	require.Equal(t, int64(1), GetPricingConfigVersion())
	before, err := GetModelPricingSnapshot([]string{"draft"})
	require.NoError(t, err)
	require.NoError(t, UpdateOption("billing_setting.billing_expr", `{"draft":"p * 3"}`))
	require.Equal(t, int64(2), GetPricingConfigVersion())
	change := ModelPricingChange{ModelName: "draft", ExpectedVersion: before.Entries[0].Version, Pricing: before.Entries[0].Configured}
	require.ErrorIs(t, UpdateModelPricing([]ModelPricingChange{change}), ErrModelPricingConflict)
	current, err := GetModelPricingSnapshot([]string{"draft"})
	require.NoError(t, err)
	change.ExpectedVersion = current.Entries[0].Version
	rejected := errors.New("rejected by guard")
	require.ErrorIs(t, UpdateModelPricingWithGuard([]ModelPricingChange{change}, func(_ []ModelPricingEntry, _ []ModelPricingChange) error { return rejected }), rejected)
	require.Equal(t, int64(2), GetPricingConfigVersion())
}

func TestValidateModelPricingBoundaries(t *testing.T) {
	for _, draft := range []PricingValues{
		{"ModelPrice": float64(-1)}, {"ModelPrice": nil}, {"unknown": float64(1)},
		{"billing_setting.billing_mode": "tiered_expr"},
		{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": `tier("request", unknown_billing_function(1))`},
	} {
		require.Error(t, ValidateModelPricing("draft", draft))
	}
	require.NoError(t, ValidateModelPricing("draft", PricingValues{"ModelPrice": float64(0)}))
	require.NoError(t, ValidateModelPricing("draft", PricingValues{}))
}

func TestModelPricingPreviewRemovesSavedCompletionOverride(t *testing.T) {
	usePricingOptionsDB(t)
	require.NoError(t, UpdateOption("CompletionRatio", `{"custom-model":9}`))
	preview, err := PreviewModelPricing("custom-model", PricingValues{"ModelRatio": float64(1)})
	require.NoError(t, err)
	require.Equal(t, float64(1), preview["CompletionRatio"])
}

func TestModelPricingDefaultsAreNotConfiguredAndReset(t *testing.T) {
	usePricingOptionsDB(t)
	before, err := GetModelPricingSnapshot([]string{"gpt-4o"})
	require.NoError(t, err)
	require.Equal(t, before.Entries[0].Effective["ModelRatio"], before.Entries[0].Configured["ModelRatio"])
	require.Contains(t, before.Entries[0].Effective, "ModelRatio")
	require.NoError(t, UpdateModelPricing([]ModelPricingChange{{ModelName: "gpt-4o", ExpectedVersion: before.Entries[0].Version, Pricing: PricingValues{"ModelRatio": float64(99)}}}))
	changed, err := GetModelPricingSnapshot([]string{"gpt-4o"})
	require.NoError(t, err)
	require.NoError(t, UpdateModelPricing([]ModelPricingChange{{ModelName: "gpt-4o", ExpectedVersion: changed.Entries[0].Version, Reset: true}}))
	reset, err := GetModelPricingSnapshot([]string{"gpt-4o"})
	require.NoError(t, err)
	require.Equal(t, before.Entries[0].Effective["ModelRatio"], reset.Entries[0].Effective["ModelRatio"])
}

func TestModelPricingConversionPreservesZeros(t *testing.T) {
	usePricingOptionsDB(t)
	require.NoError(t, DB.AutoMigrate(&Channel{}, &Ability{}, &Model{}))
	result, err := PreviewModelPricingConversion("custom-model", PricingValues{"ModelRatio": float64(1), "CompletionRatio": float64(0), "CacheRatio": float64(0)})
	require.NoError(t, err)
	require.Empty(t, result.UnsupportedReason)
	require.Contains(t, result.Expression, "c * 0")
	require.Contains(t, result.Expression, "cr * 0")
	fixed, err := PreviewModelPricingConversion("custom-model", PricingValues{"ModelPrice": float64(1)})
	require.NoError(t, err)
	require.Empty(t, fixed.UnsupportedReason)
	require.Contains(t, fixed.Expression, "fixed(1)")
}

func TestModelPricingOptionMapsAtomicExpressionAndCAS(t *testing.T) {
	usePricingOptionsDB(t)
	updates := map[string]string{"billing_setting.billing_mode": `{"cas":"tiered_expr"}`, "billing_setting.billing_expr": `{"cas":"p * 2"}`}
	expected := map[string]string{"billing_setting.billing_mode": "{}", "billing_setting.billing_expr": "{ }"}
	require.NoError(t, UpdateModelPricingOptionMaps(updates, expected))
	require.Equal(t, int64(1), GetPricingConfigVersion())
	require.ErrorIs(t, UpdateModelPricingOptionMaps(updates, expected), ErrModelPricingConflict)
	require.Error(t, UpdateModelPricingOptionMaps(map[string]string{"billing_setting.billing_expr": "{}"}, map[string]string{"billing_setting.billing_expr": updates["billing_setting.billing_expr"]}))
	require.Equal(t, int64(1), GetPricingConfigVersion())
	require.Error(t, UpdateModelPricingOptionMaps(map[string]string{"QuotaPerUnit": "1"}, map[string]string{"QuotaPerUnit": "1"}))
	require.Error(t, UpdateModelPricingOptionMaps(updates, map[string]string{}))
}

func TestModelPricingOptionMapsUnpersistedDefaultCAS(t *testing.T) {
	usePricingOptionsDB(t)
	defaults := ratio_setting.GetDefaultPricingMaps()["ModelRatio"]
	encoded, err := common.Marshal(defaults)
	require.NoError(t, err)
	require.NoError(t, UpdateModelPricingOptionMaps(map[string]string{"ModelRatio": string(encoded)}, map[string]string{"ModelRatio": string(encoded)}))
}

func TestModelPricingRejectsIneffectiveLockedCompletionEdit(t *testing.T) {
	usePricingOptionsDB(t)
	name := "claude-sonnet-4-20250514"
	snapshot, err := GetModelPricingSnapshot([]string{name})
	require.NoError(t, err)
	require.True(t, snapshot.Entries[0].CompletionLocked)
	require.Error(t, UpdateModelPricing([]ModelPricingChange{{ModelName: name, ExpectedVersion: snapshot.Entries[0].Version, Pricing: PricingValues{"ModelRatio": float64(1), "CompletionRatio": float64(999)}}}))
	require.Equal(t, int64(0), GetPricingConfigVersion())
}

func TestModelPricingGuardReceivesLockedDatabaseVersion(t *testing.T) {
	usePricingOptionsDB(t)
	require.NoError(t, DB.Create(&Option{Key: PricingConfigVersionKey, Value: "7"}).Error)
	require.Equal(t, int64(0), GetPricingConfigVersion())
	denied := errors.New("guard denied")
	err := UpdateModelPricingWithGuard([]ModelPricingChange{{ModelName: "remote-cas", ExpectedVersion: ModelPricingVersion(PricingValues{}), Pricing: PricingValues{"ModelPrice": float64(1)}}}, func(current []ModelPricingEntry, _ []ModelPricingChange) error {
		require.Equal(t, int64(7), current[0].PricingVersion)
		return denied
	})
	require.ErrorIs(t, err, denied)
}

func TestModelPricingSnapshotTokenPriceFactor(t *testing.T) {
	usePricingOptionsDB(t)
	previous := common.QuotaPerUnit
	common.QuotaPerUnit = 250000
	t.Cleanup(func() { common.QuotaPerUnit = previous })
	snapshot, err := GetModelPricingSnapshot([]string{"factor-model"})
	require.NoError(t, err)
	require.Equal(t, float64(4), snapshot.TokenPriceFactor)
}
