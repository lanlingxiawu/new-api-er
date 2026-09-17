package billing_setting

import (
	"fmt"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/setting/model_setting"
)

// resolveForkTaskBillingExpr supplies task usage expressions that fork-only
// pricing settings own. It runs after explicit plugin overrides so a saved
// billing_setting.plugin_billing_expr entry still wins.
//
// thirdpartysd2: the admin-maintained resolution × video-input matrix
// (thirdpartysd2_pricing.matrix) is rendered into an expression by the
// model_setting index, so the plugin is billed through the standard task
// expression pipeline (reserve → completion token overlay → settlement).
func resolveForkTaskBillingExpr(pluginKey, model, mappedModel string) (string, bool) {
	matrixModel, ok := thirdPartySD2MatrixModel(pluginKey, model, mappedModel)
	if !ok {
		return "", false
	}
	return model_setting.GetThirdPartySD2BillingExpr(matrixModel)
}

// thirdPartySD2MatrixModel returns the model whose matrix expression prices a
// thirdpartysd2 request: the client model first, then the mapped model.
func thirdPartySD2MatrixModel(pluginKey, model, mappedModel string) (string, bool) {
	if pluginKey != model_setting.ThirdPartySD2PluginKey {
		return "", false
	}
	if _, ok := model_setting.GetThirdPartySD2BillingExpr(model); ok {
		return model, true
	}
	if mappedModel != "" && mappedModel != model {
		if _, ok := model_setting.GetThirdPartySD2BillingExpr(mappedModel); ok {
			return mappedModel, true
		}
	}
	return "", false
}

// ResolveForkPublicTaskBillingExpr returns the effective task expression of a
// fork-priced plugin model for the public pricing page, which otherwise only
// shows model-level expressions. Other plugins return false.
func ResolveForkPublicTaskBillingExpr(pluginKey, model string) (string, bool) {
	if pluginKey != model_setting.ThirdPartySD2PluginKey {
		return "", false
	}
	expression, ok := ResolveTaskBillingExpr(pluginKey, model, "")
	return expression, ok && strings.TrimSpace(expression) != ""
}

// ValidateForkTaskUsageFacts rejects submissions whose usage facts the
// fork-owned pricing cannot price, before any reservation or upstream call.
//
// thirdpartysd2: the accepted output resolutions of a model are exactly the
// resolutions configured in its pricing matrix. The generated expression ends
// with an unconditional tier, so an unpriced resolution must be rejected here
// instead of being billed at that tier. A saved plugin override owns pricing
// on its own and skips the matrix check.
func ValidateForkTaskUsageFacts(pluginKey, model, mappedModel string, facts map[string]any) error {
	if pluginKey != model_setting.ThirdPartySD2PluginKey {
		return nil
	}
	if _, ok := GetPluginBillingExpr(pluginKey, model); ok {
		return nil
	}
	if mappedModel != "" && mappedModel != model {
		if _, ok := GetPluginBillingExpr(pluginKey, mappedModel); ok {
			return nil
		}
	}
	matrixModel, ok := thirdPartySD2MatrixModel(pluginKey, model, mappedModel)
	if !ok {
		return nil
	}
	resolutions, ok := model_setting.GetThirdPartySD2Resolutions(matrixModel)
	if !ok {
		return nil
	}
	resolution, _ := facts["output_resolution"].(string)
	if slices.Contains(resolutions, resolution) {
		return nil
	}
	return fmt.Errorf("%s does not support %s resolution (supported: %s)", model, resolution, strings.Join(resolutions, ", "))
}
