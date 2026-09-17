package billing_setting

import "github.com/QuantumNous/new-api/setting/model_setting"

// resolveForkTaskBillingExpr supplies task usage expressions that fork-only
// pricing settings own. It runs after explicit plugin overrides so a saved
// billing_setting.plugin_billing_expr entry still wins.
//
// thirdpartysd2: the admin-maintained resolution × video-input matrix
// (thirdpartysd2_pricing.matrix) is rendered into an expression by the
// model_setting index, so the plugin is billed through the standard task
// expression pipeline (reserve → completion token overlay → settlement).
func resolveForkTaskBillingExpr(pluginKey, model, mappedModel string) (string, bool) {
	if pluginKey != model_setting.ThirdPartySD2PluginKey {
		return "", false
	}
	if expression, ok := model_setting.GetThirdPartySD2BillingExpr(model); ok {
		return expression, true
	}
	if mappedModel != "" && mappedModel != model {
		return model_setting.GetThirdPartySD2BillingExpr(mappedModel)
	}
	return "", false
}
