package billing_setting

import (
	"testing"

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
