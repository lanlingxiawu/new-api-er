package billing_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	forkSD2Model     = "dreamina-seedance-2-0-260128"
	forkSD2FastModel = "dreamina-seedance-2-0-fast-260128"
)

func TestResolveForkPublicTaskBillingExpr(t *testing.T) {
	saveBillingSetting(t)
	matrixExpr, ok := model_setting.GetThirdPartySD2BillingExpr(forkSD2Model)
	require.True(t, ok)

	expression, ok := ResolveForkPublicTaskBillingExpr(model_setting.ThirdPartySD2PluginKey, forkSD2Model)
	require.True(t, ok)
	assert.Equal(t, matrixExpr, expression)

	_, ok = ResolveForkPublicTaskBillingExpr("doubao", forkSD2Model)
	assert.False(t, ok, "only fork-priced plugins publish a task expression")
	_, ok = ResolveForkPublicTaskBillingExpr(model_setting.ThirdPartySD2PluginKey, "unknown-model")
	assert.False(t, ok)

	override := `tier("base", u("tokens") * 5 / 1000000)`
	billingSetting.PluginBillingExpr[PluginBillingExprKey(model_setting.ThirdPartySD2PluginKey, forkSD2Model)] = override
	expression, ok = ResolveForkPublicTaskBillingExpr(model_setting.ThirdPartySD2PluginKey, forkSD2Model)
	require.True(t, ok)
	assert.Equal(t, override, expression, "a saved plugin override is what the model bills with")
}

func TestValidateForkTaskUsageFacts(t *testing.T) {
	saveBillingSetting(t)
	tests := []struct {
		name, pluginKey, model, mapped, resolution, wantErr string
	}{
		{name: "priced resolution", pluginKey: "thirdpartysd2", model: forkSD2FastModel, resolution: "720p"},
		{name: "unpriced resolution", pluginKey: "thirdpartysd2", model: forkSD2FastModel, resolution: "1080p",
			wantErr: forkSD2FastModel + " does not support 1080p resolution (supported: 480p, 720p)"},
		{name: "alias checks the mapped model matrix", pluginKey: "thirdpartysd2", model: "sd2-alias", mapped: forkSD2FastModel, resolution: "4k",
			wantErr: "sd2-alias does not support 4k resolution (supported: 480p, 720p)"},
		{name: "standard model 4k", pluginKey: "thirdpartysd2", model: forkSD2Model, resolution: "4k"},
		{name: "other plugin", pluginKey: "doubao", model: forkSD2FastModel, resolution: "4k"},
		{name: "model without matrix", pluginKey: "thirdpartysd2", model: "unknown-model", resolution: "4k"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateForkTaskUsageFacts(testCase.pluginKey, testCase.model, testCase.mapped, map[string]any{"output_resolution": testCase.resolution})
			if testCase.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.EqualError(t, err, testCase.wantErr)
		})
	}

	t.Run("plugin override skips the matrix check", func(t *testing.T) {
		billingSetting.PluginBillingExpr[PluginBillingExprKey("thirdpartysd2", forkSD2FastModel)] = `tier("base", u("tokens") * 5 / 1000000)`
		assert.NoError(t, ValidateForkTaskUsageFacts("thirdpartysd2", forkSD2FastModel, "", map[string]any{"output_resolution": "4k"}))
		assert.NoError(t, ValidateForkTaskUsageFacts("thirdpartysd2", "sd2-alias", forkSD2FastModel, map[string]any{"output_resolution": "4k"}))
	})
}

// withForkSD2Matrix saves a pricing matrix through the settings path the admin
// API uses, and restores the previous one.
func withForkSD2Matrix(t *testing.T, raw string) {
	t.Helper()
	saved, err := config.ConfigToMap(config.GlobalConfig.Get("thirdpartysd2_pricing"))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("thirdpartysd2_pricing", saved))
		model_setting.RebuildThirdPartySD2PricingIndex()
	})
	require.NoError(t, model_setting.ValidateThirdPartySD2PricingMatrixJSON(raw))
	require.NoError(t, config.GlobalConfig.UpdateFromMap("thirdpartysd2_pricing", map[string]string{"matrix": raw}))
	model_setting.RebuildThirdPartySD2PricingIndex()
}

// The matrix is the only switch that makes a resolution available, so removing
// a built-in resolution has to make requests for it fail.
func TestValidateForkTaskUsageFacts_RemovedResolutionIsRejected(t *testing.T) {
	saveBillingSetting(t)
	withForkSD2Matrix(t, `{"`+forkSD2Model+`":{"4k":null}}`)

	err := ValidateForkTaskUsageFacts("thirdpartysd2", forkSD2Model, "", map[string]any{"output_resolution": "4k"})
	assert.EqualError(t, err, forkSD2Model+" does not support 4k resolution (supported: 480p, 720p, 1080p)")
	assert.NoError(t, ValidateForkTaskUsageFacts("thirdpartysd2", forkSD2Model, "", map[string]any{"output_resolution": "1080p"}))
}

func TestValidateForkTaskModelPricing(t *testing.T) {
	saveBillingSetting(t)
	// a priced model, another plugin and a model the matrix does not configure
	assert.NoError(t, ValidateForkTaskModelPricing("thirdpartysd2", forkSD2Model, ""))
	assert.NoError(t, ValidateForkTaskModelPricing("doubao", forkSD2Model, ""))
	assert.NoError(t, ValidateForkTaskModelPricing("thirdpartysd2", "unknown-model", ""))

	withForkSD2Matrix(t, `{"`+forkSD2FastModel+`":{"480p":null,"720p":null}}`)
	unpriced := forkSD2FastModel + " has no priced resolution; an administrator must price it before it can be used"
	assert.EqualError(t, ValidateForkTaskModelPricing("thirdpartysd2", forkSD2FastModel, ""), unpriced)
	assert.EqualError(t, ValidateForkTaskModelPricing("thirdpartysd2", "sd2-alias", forkSD2FastModel),
		"sd2-alias has no priced resolution; an administrator must price it before it can be used")
	assert.EqualError(t, ValidateForkTaskUsageFacts("thirdpartysd2", forkSD2FastModel, "", map[string]any{"output_resolution": "480p"}), unpriced)

	_, ok := ResolveTaskBillingExpr("thirdpartysd2", forkSD2FastModel, "")
	assert.False(t, ok, "no expression prices the model")
	// the other model keeps its prices
	assert.NoError(t, ValidateForkTaskModelPricing("thirdpartysd2", forkSD2Model, ""))
}
