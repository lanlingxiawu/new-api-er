package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// keepModelRatioTable 在用例结束时还原进程内 ModelRatio 表：PatchPricingOptions 成功后
// updateOptionMap 会整表替换它，不还原会污染同一次运行里的其它用例。
func keepModelRatioTable(t *testing.T) {
	t.Helper()
	origin := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelRatioByJSONString(origin) })
}

func optionMapValue(key string) (string, bool) {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	value, ok := common.OptionMap[key]
	return value, ok
}

// 改价后未通过 validateModelPricing 的补丁整单拒绝：数据库、OptionMap 与版本号都保持原样。
func TestPatchPricingOptionsRejectsInvalidPricingWithoutWrite(t *testing.T) {
	for _, test := range []struct {
		name    string
		seed    map[string]string
		patches []PricingPatch
		model   string
	}{
		{
			name:    "negative ratio",
			seed:    map[string]string{"ModelRatio": `{"__invalid_probe__":1}`},
			patches: []PricingPatch{{OptionKey: "ModelRatio", Model: "__invalid_probe__", Expected: floatPtr(1), Value: floatPtr(-1)}},
			model:   "__invalid_probe__",
		},
		{
			name: "tiered expression mode without expression",
			seed: map[string]string{
				"ModelRatio":                   `{"__expr_probe__":1}`,
				"billing_setting.billing_mode": `{"__expr_probe__":"tiered_expr"}`,
			},
			patches: []PricingPatch{{OptionKey: "ModelRatio", Model: "__expr_probe__", Expected: floatPtr(1), Value: floatPtr(2)}},
			model:   "__expr_probe__",
		},
		{
			name: "one invalid model rolls back a valid model in the same request",
			seed: map[string]string{
				"ModelRatio":      `{"__valid_probe__":1}`,
				"CompletionRatio": `{"__invalid_probe__":2}`,
			},
			patches: []PricingPatch{
				{OptionKey: "ModelRatio", Model: "__valid_probe__", Expected: floatPtr(1), Value: floatPtr(3)},
				{OptionKey: "CompletionRatio", Model: "__invalid_probe__", Expected: floatPtr(2), Value: floatPtr(-0.5)},
			},
			model: "__invalid_probe__",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			usePricingOptionsDB(t)
			keepModelRatioTable(t)
			for key, value := range test.seed {
				seedPricingOption(t, key, value)
			}
			seedPricingOption(t, PricingConfigVersionKey, "4")

			version, applied, err := PatchPricingOptions(4, test.patches)
			require.ErrorIs(t, err, ErrPricingPatchInvalid)
			var invalid *PricingPatchInvalidError
			require.ErrorAs(t, err, &invalid)
			assert.Equal(t, test.model, invalid.Model)
			assert.NotNil(t, invalid.Unwrap())
			assert.NotErrorIs(t, err, ErrPricingVersionConflict)
			assert.Zero(t, version)
			assert.Nil(t, applied)

			for key, value := range test.seed {
				var stored Option
				require.NoError(t, DB.Where(commonKeyCol+" = ?", key).Take(&stored).Error)
				assert.JSONEq(t, value, stored.Value, "%s must not be written", key)
				inMemory, ok := optionMapValue(key)
				require.True(t, ok)
				assert.JSONEq(t, value, inMemory, "%s must not be refreshed in memory", key)
			}
			var versionRow Option
			require.NoError(t, DB.Where(commonKeyCol+" = ?", PricingConfigVersionKey).Take(&versionRow).Error)
			assert.Equal(t, "4", versionRow.Value)
			assert.Equal(t, int64(4), GetPricingConfigVersion())
		})
	}
}

// 成功改价后立即重建定价缓存：/api/pricing 读取的 pricingMap 不必等一分钟的惰性刷新。
func TestPatchPricingOptionsRefreshesPricingCacheAndBumpsVersion(t *testing.T) {
	db := usePricingOptionsDB(t)
	keepModelRatioTable(t)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}, &Model{}, &Vendor{}))
	t.Cleanup(InvalidatePricingCache)

	const modelName = "__refresh_probe__"
	channel := &Channel{Name: "pricing-refresh", Key: "fixture-key", Type: 1, Status: common.ChannelStatusEnabled, Group: "default", Models: modelName}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&Ability{Group: "default", Model: modelName, ChannelId: channel.Id, Enabled: true}).Error)
	seedPricingOption(t, "ModelRatio", `{"__refresh_probe__":1}`)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"__refresh_probe__":1}`))
	seedPricingOption(t, PricingConfigVersionKey, "2")

	RefreshPricing()
	require.InDelta(t, 1, pricingRatioFor(t, modelName), 1e-9)

	version, applied, err := PatchPricingOptions(2, []PricingPatch{
		{OptionKey: "ModelRatio", Model: modelName, Expected: floatPtr(1), Value: floatPtr(4)},
	})
	require.NoError(t, err)
	require.Len(t, applied["ModelRatio"], 1)
	assert.Equal(t, int64(3), version)
	assert.Equal(t, int64(3), GetPricingConfigVersion())
	assert.InDelta(t, 4, readPricingOptionValues(t, "ModelRatio")[modelName], 1e-9)
	assert.InDelta(t, 4, pricingRatioFor(t, modelName), 1e-9, "the pricing cache must be rebuilt right after the write")
}

// 无实际改动的补丁会经 FirstOrCreate 留下值为空的价格行；后续校验读取整份价格配置时
// 必须把空行当作未配置，否则之后的每次改价与整块保存都会因 JSON 解析失败而无法进行。
func TestPatchPricingOptionsToleratesEmptyPricingRows(t *testing.T) {
	db := usePricingOptionsDB(t)
	keepModelRatioTable(t)
	require.NoError(t, db.Create(&Option{Key: "ImageRatio", Value: ""}).Error)
	seedPricingOption(t, "ModelRatio", `{"__empty_row_probe__":1}`)

	version, applied, err := PatchPricingOptions(0, []PricingPatch{
		{OptionKey: "ModelRatio", Model: "__empty_row_probe__", Expected: floatPtr(1), Value: floatPtr(2)},
	})
	require.NoError(t, err)
	require.Len(t, applied["ModelRatio"], 1)
	assert.Equal(t, int64(1), version)

	values, existing, _, err := readModelPricingMaps(DB)
	require.NoError(t, err)
	assert.False(t, existing["ImageRatio"], "an empty row is unconfigured")
	assert.NotNil(t, values["ImageRatio"])
}

func pricingRatioFor(t *testing.T, modelName string) float64 {
	t.Helper()
	for _, pricing := range GetPricing() {
		if pricing.ModelName == modelName {
			return pricing.ModelRatio
		}
	}
	require.Failf(t, "model missing from pricing cache", "model %s", modelName)
	return 0
}
