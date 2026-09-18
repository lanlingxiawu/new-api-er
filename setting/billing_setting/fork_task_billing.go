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

// thirdPartySD2MatrixModel returns the model the pricing matrix configures for
// a thirdpartysd2 request: the client model first, then the mapped model. A
// model whose resolutions were all removed is still configured — it simply
// prices nothing — so selection follows matrix membership rather than the
// presence of an expression.
func thirdPartySD2MatrixModel(pluginKey, model, mappedModel string) (string, bool) {
	if pluginKey != model_setting.ThirdPartySD2PluginKey {
		return "", false
	}
	if _, ok := model_setting.GetThirdPartySD2Resolutions(model); ok {
		return model, true
	}
	if mappedModel != "" && mappedModel != model {
		if _, ok := model_setting.GetThirdPartySD2Resolutions(mappedModel); ok {
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
	if len(resolutions) == 0 {
		return forkTaskModelUnpricedError(model)
	}
	return fmt.Errorf("%s does not support %s resolution (supported: %s)", model, resolution, strings.Join(resolutions, ", "))
}

// ValidateForkTaskModelPricing rejects a submission whose fork-priced model has
// no price at all, before it can fall back to a generic per-call price.
//
// thirdpartysd2: removing every resolution of a model makes the model
// unavailable. Its matrix expression disappears with the last resolution, so
// without this check the request would leave the task expression path and be
// priced by the generic model price / ratio fallback.
func ValidateForkTaskModelPricing(pluginKey, model, mappedModel string) error {
	matrixModel, ok := thirdPartySD2MatrixModel(pluginKey, model, mappedModel)
	if !ok {
		return nil
	}
	resolutions, ok := model_setting.GetThirdPartySD2Resolutions(matrixModel)
	if !ok || len(resolutions) > 0 {
		return nil
	}
	return forkTaskModelUnpricedError(model)
}

func forkTaskModelUnpricedError(model string) error {
	return fmt.Errorf("%s has no priced resolution; an administrator must price it before it can be used", model)
}
