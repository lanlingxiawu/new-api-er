package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Shared helpers for the pricing-family tests (pricing.go, pricing_refresh.go,
// pricing_default.go, model_extra.go). Defined once here; reused across files
// in package model.
//
// The ratio_setting maps are process-global. To pin exact values without
// clobbering whatever the rest of the suite loaded, each override snapshots the
// current JSON, merges the new key, and restores the snapshot on cleanup.
// ---------------------------------------------------------------------------

func mergeRatioOverride(t *testing.T, dump func() string, load func(string) error, key string, val float64) {
	t.Helper()
	old := dump()
	m := map[string]float64{}
	require.NoError(t, common.Unmarshal([]byte(old), &m))
	m[key] = val
	b, err := common.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, load(string(b)))
	t.Cleanup(func() { _ = load(old) })
}

func setModelRatio(t *testing.T, model string, v float64) {
	mergeRatioOverride(t, ratio_setting.ModelRatio2JSONString, ratio_setting.UpdateModelRatioByJSONString, model, v)
}
func setModelPrice(t *testing.T, model string, v float64) {
	mergeRatioOverride(t, ratio_setting.ModelPrice2JSONString, ratio_setting.UpdateModelPriceByJSONString, model, v)
}
func setCompletionRatio(t *testing.T, model string, v float64) {
	mergeRatioOverride(t, ratio_setting.CompletionRatio2JSONString, ratio_setting.UpdateCompletionRatioByJSONString, model, v)
}
func setCacheRatio(t *testing.T, model string, v float64) {
	mergeRatioOverride(t, ratio_setting.CacheRatio2JSONString, ratio_setting.UpdateCacheRatioByJSONString, model, v)
}
func setCreateCacheRatio(t *testing.T, model string, v float64) {
	mergeRatioOverride(t, ratio_setting.CreateCacheRatio2JSONString, ratio_setting.UpdateCreateCacheRatioByJSONString, model, v)
}
func setImageRatio(t *testing.T, model string, v float64) {
	mergeRatioOverride(t, ratio_setting.ImageRatio2JSONString, ratio_setting.UpdateImageRatioByJSONString, model, v)
}
func setAudioRatio(t *testing.T, model string, v float64) {
	mergeRatioOverride(t, ratio_setting.AudioRatio2JSONString, ratio_setting.UpdateAudioRatioByJSONString, model, v)
}
func setAudioCompletionRatio(t *testing.T, model string, v float64) {
	mergeRatioOverride(t, ratio_setting.AudioCompletionRatio2JSONString, ratio_setting.UpdateAudioCompletionRatioByJSONString, model, v)
}

// mkAbilityModel creates an enabled channel whose abilities publish the given
// model names under the given group, so updatePricing() will pick them up.
// Abilities + channel are cleaned up automatically.
func mkAbilityModel(t *testing.T, group string, models string) *Channel {
	t.Helper()
	ch := mkChannel(t, func(c *Channel) {
		c.Group = group
		c.Models = models
		c.Status = common.ChannelStatusEnabled
		c.Type = 1 // OpenAI
	})
	require.NoError(t, ch.AddAbilities(nil))
	cleanupAbilities(t, ch.Id)
	return ch
}

// findPricing returns the pricing entry for a model name, or nil.
func findPricing(list []Pricing, model string) *Pricing {
	for i := range list {
		if list[i].ModelName == model {
			return &list[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// updatePricing: ratio path vs price path, all ratio pointers pinned.
// ---------------------------------------------------------------------------

func TestUpdatePricing_RatioAndPricePaths(t *testing.T) {
	requireDB(t)
	t.Cleanup(InvalidatePricingCache) // clear stale entries after the test

	grp := uniq("pgrp")
	mRatio := uniq("zzpricingalpha")
	mPrice := uniq("zzpricingbeta")
	mkAbilityModel(t, grp, mRatio+","+mPrice)

	// ratio-path model: no model price set -> ratio branch
	setModelRatio(t, mRatio, 8.5)
	setCompletionRatio(t, mRatio, 3.0)
	setCacheRatio(t, mRatio, 0.5)
	setCreateCacheRatio(t, mRatio, 2.0)
	setImageRatio(t, mRatio, 4.0)
	setAudioRatio(t, mRatio, 6.0)
	setAudioCompletionRatio(t, mRatio, 7.0)

	// price-path model: model price set -> QuotaType 1
	setModelPrice(t, mPrice, 12.0)

	RefreshPricing()
	list := GetPricing()

	pr := findPricing(list, mRatio)
	require.NotNil(t, pr, "ratio model should be in pricing")
	assert.Equal(t, 0, pr.QuotaType)
	assert.Equal(t, 8.5, pr.ModelRatio)
	assert.Equal(t, 3.0, pr.CompletionRatio)
	assert.Equal(t, 0.0, pr.ModelPrice)
	require.NotNil(t, pr.CacheRatio)
	assert.Equal(t, 0.5, *pr.CacheRatio)
	require.NotNil(t, pr.CreateCacheRatio)
	assert.Equal(t, 2.0, *pr.CreateCacheRatio)
	require.NotNil(t, pr.ImageRatio)
	assert.Equal(t, 4.0, *pr.ImageRatio)
	require.NotNil(t, pr.AudioRatio)
	assert.Equal(t, 6.0, *pr.AudioRatio)
	require.NotNil(t, pr.AudioCompletionRatio)
	assert.Equal(t, 7.0, *pr.AudioCompletionRatio)
	assert.Contains(t, pr.EnableGroup, grp)
	assert.NotEmpty(t, pr.SupportedEndpointTypes, "channel type 1 should yield endpoint types")

	pp := findPricing(list, mPrice)
	require.NotNil(t, pp, "price model should be in pricing")
	assert.Equal(t, 1, pp.QuotaType)
	assert.Equal(t, 12.0, pp.ModelPrice)
	assert.Equal(t, 0.0, pp.ModelRatio)
	assert.Equal(t, 0.0, pp.CompletionRatio)

	// PricingVersion is stamped on element 0 whenever the list is non-empty.
	require.NotEmpty(t, list)
	assert.Equal(t, "5a90f2b86c08bd983a9a2e6d66c255f4eaef9c4bc934386d2b6ae84ef0ff1f1f", list[0].PricingVersion)
}

// ---------------------------------------------------------------------------
// updatePricing: metadata from the models table (enabled/disabled + fields).
// ---------------------------------------------------------------------------

func TestUpdatePricing_ModelMetaAndStatus(t *testing.T) {
	requireDB(t)
	t.Cleanup(InvalidatePricingCache)

	grp := uniq("pgrp")
	mEnabled := uniq("zzmetaon")
	mDisabled := uniq("zzmetaoff")
	mkAbilityModel(t, grp, mEnabled+","+mDisabled)

	// enabled metadata row -> fields flow into pricing
	metaOn := &Model{
		ModelName:   mEnabled,
		Description: "desc-on",
		Icon:        "icon-on",
		Tags:        "tag-on",
		VendorID:    4242,
		Status:      1,
		NameRule:    NameRuleExact,
	}
	require.NoError(t, metaOn.Insert())
	deleteByID(t, &Model{}, metaOn.Id)

	// disabled metadata row (status != 1) -> excluded entirely
	metaOff := &Model{
		ModelName: mDisabled,
		Status:    2,
		NameRule:  NameRuleExact,
	}
	require.NoError(t, metaOff.Insert())
	deleteByID(t, &Model{}, metaOff.Id)

	RefreshPricing()
	list := GetPricing()

	on := findPricing(list, mEnabled)
	require.NotNil(t, on)
	assert.Equal(t, "desc-on", on.Description)
	assert.Equal(t, "icon-on", on.Icon)
	assert.Equal(t, "tag-on", on.Tags)
	assert.Equal(t, 4242, on.VendorID)

	assert.Nil(t, findPricing(list, mDisabled), "disabled model must be excluded from pricing")
}

// ---------------------------------------------------------------------------
// updatePricing: prefix NameRule metadata matches abilities by prefix.
// ---------------------------------------------------------------------------

func TestUpdatePricing_PrefixNameRule(t *testing.T) {
	requireDB(t)
	t.Cleanup(InvalidatePricingCache)

	grp := uniq("pgrp")
	prefix := uniq("zzpfx")
	fullModel := prefix + "-variant"
	mkAbilityModel(t, grp, fullModel)

	meta := &Model{
		ModelName:   prefix,
		Description: "prefix-desc",
		Status:      1,
		NameRule:    NameRulePrefix,
	}
	require.NoError(t, meta.Insert())
	deleteByID(t, &Model{}, meta.Id)

	RefreshPricing()
	pr := findPricing(GetPricing(), fullModel)
	require.NotNil(t, pr)
	assert.Equal(t, "prefix-desc", pr.Description, "prefix rule metadata should attach to the ability model")
}

// ---------------------------------------------------------------------------
// updatePricing: suffix + contains NameRule metadata matching.
// ---------------------------------------------------------------------------

func TestUpdatePricing_SuffixAndContainsNameRule(t *testing.T) {
	requireDB(t)
	t.Cleanup(InvalidatePricingCache)

	grp := uniq("pgrp")
	sfx := uniq("zzsfx")
	ctn := uniq("zzctn")
	suffixModel := "variant-" + sfx  // ends with sfx
	containsModel := "a-" + ctn + "-b" // contains ctn
	mkAbilityModel(t, grp, suffixModel+","+containsModel)

	metaSfx := &Model{ModelName: sfx, Description: "sfx-desc", Status: 1, NameRule: NameRuleSuffix}
	require.NoError(t, metaSfx.Insert())
	deleteByID(t, &Model{}, metaSfx.Id)

	metaCtn := &Model{ModelName: ctn, Description: "ctn-desc", Status: 1, NameRule: NameRuleContains}
	require.NoError(t, metaCtn.Insert())
	deleteByID(t, &Model{}, metaCtn.Id)

	RefreshPricing()
	list := GetPricing()

	sp := findPricing(list, suffixModel)
	require.NotNil(t, sp)
	assert.Equal(t, "sfx-desc", sp.Description)

	cp := findPricing(list, containsModel)
	require.NotNil(t, cp)
	assert.Equal(t, "ctn-desc", cp.Description)
}

// ---------------------------------------------------------------------------
// updatePricing: custom endpoint whose key equals an already-inferred endpoint
// exercises appendPricingEndpoint's "already present" short-circuit.
// ---------------------------------------------------------------------------

func TestUpdatePricing_CustomEndpointDedup(t *testing.T) {
	requireDB(t)
	t.Cleanup(InvalidatePricingCache)

	grp := uniq("pgrp")
	model := uniq("zzdedupmodel")
	mkAbilityModel(t, grp, model) // channel type 1 -> inferred endpoint "openai"

	// Custom endpoint keyed "openai" is already inferred -> append is a no-op,
	// but the override still updates the global endpoint map path.
	meta := &Model{
		ModelName: model,
		Status:    1,
		NameRule:  NameRuleExact,
		Endpoints: `{"openai":"/v1/custom-openai"}`,
	}
	require.NoError(t, meta.Insert())
	deleteByID(t, &Model{}, meta.Id)

	RefreshPricing()

	// "openai" must appear exactly once in the model's supported endpoints.
	count := 0
	for _, et := range GetModelSupportEndpointTypes(model) {
		if string(et) == "openai" {
			count++
		}
	}
	assert.Equal(t, 1, count, "duplicate custom endpoint key must not be appended twice")
}

// ---------------------------------------------------------------------------
// updatePricing: custom endpoints from models.Endpoints override defaults.
// ---------------------------------------------------------------------------

func TestUpdatePricing_CustomEndpoints(t *testing.T) {
	requireDB(t)
	t.Cleanup(InvalidatePricingCache)

	grp := uniq("pgrp")
	model := uniq("zzendpointmodel")
	strEP := uniq("zzstrep")
	objEP := uniq("zzobjep")
	mkAbilityModel(t, grp, model)

	meta := &Model{
		ModelName: model,
		Status:    1,
		NameRule:  NameRuleExact,
		Endpoints: `{"` + strEP + `":"/v1/zzstr","` + objEP + `":{"path":"/v1/zzobj","method":"get"}}`,
	}
	require.NoError(t, meta.Insert())
	deleteByID(t, &Model{}, meta.Id)

	RefreshPricing()

	// Custom endpoints appended to the model's supported endpoint types.
	types := GetModelSupportEndpointTypes(model)
	strs := make([]string, 0, len(types))
	for _, et := range types {
		strs = append(strs, string(et))
	}
	assert.Contains(t, strs, strEP)
	assert.Contains(t, strs, objEP)

	// Custom endpoints registered in the global endpoint map (string + object).
	epMap := GetSupportedEndpointMap()
	require.Contains(t, epMap, strEP)
	assert.Equal(t, "/v1/zzstr", epMap[strEP].Path)
	assert.Equal(t, "POST", epMap[strEP].Method)
	require.Contains(t, epMap, objEP)
	assert.Equal(t, "/v1/zzobj", epMap[objEP].Path)
	assert.Equal(t, "GET", epMap[objEP].Method, "method should be upper-cased")
}

// ---------------------------------------------------------------------------
// GetModelSupportEndpointTypes edge cases + GetPricing cache reuse + GetVendors.
// ---------------------------------------------------------------------------

func TestGetModelSupportEndpointTypes_Empty(t *testing.T) {
	assert.Empty(t, GetModelSupportEndpointTypes(""))
	assert.Empty(t, GetModelSupportEndpointTypes(uniq("no-such-model-zzz")))
}

func TestGetPricing_CacheAndInvalidate(t *testing.T) {
	requireDB(t)
	t.Cleanup(InvalidatePricingCache)

	// Warm the cache.
	first := GetPricing()
	require.NotNil(t, first)
	// Second call within a minute reuses the same slice header (no recompute).
	second := GetPricing()
	assert.Equal(t, len(first), len(second))

	// GetVendors triggers a refresh when needed and returns the vendor list.
	InvalidatePricingCache()
	vendors := GetVendors()
	assert.NotNil(t, vendors)

	// After invalidation the map is rebuilt on next access.
	InvalidatePricingCache()
	assert.NotNil(t, GetPricing())
}
