package model

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"math"
	"strings"
)

type ModelPricingConversion struct {
	Effective         PricingValues `json:"effective"`
	Expression        string        `json:"expression,omitempty"`
	UnsupportedReason string        `json:"unsupported_reason,omitempty"`
}

// PreviewModelPricingConversion never changes a model's billing mode. Special
// legacy paths whose semantics cannot be represented by this engine are explicit.
func PreviewModelPricingConversion(name string, draft PricingValues) (*ModelPricingConversion, error) {
	effective, err := PreviewModelPricing(name, draft)
	if err != nil {
		return nil, err
	}
	result := &ModelPricingConversion{Effective: effective}
	if effective["billing_setting.billing_mode"] == "tiered_expr" {
		result.UnsupportedReason = "already_expression"
		return result, nil
	}
	if _, fixed := effective["ModelPrice"]; fixed {
		result.UnsupportedReason = "fixed_pricing_engine_unavailable"
		return result, nil
	}
	input, ok := effective["ModelRatio"].(float64)
	if !ok {
		result.UnsupportedReason = "missing_input_price"
		return result, nil
	}
	if strings.Contains(strings.ToLower(name), "claude") || strings.Contains(strings.ToLower(name), "audio") || operation_setting.GetGeminiInputAudioPricePerMillionTokens(name) > 0 || common.IsImageGenerationModel(name) {
		result.UnsupportedReason = "legacy_special_billing"
		return result, nil
	}
	if common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) {
		result.UnsupportedReason = "invalid_quota_unit"
		return result, nil
	}
	for _, key := range []string{"AudioRatio", "AudioCompletionRatio"} {
		if value, ok := effective[key].(float64); ok && value != 1 {
			result.UnsupportedReason = "legacy_audio_billing"
			return result, nil
		}
	}
	price := input * 1_000_000 / common.QuotaPerUnit
	terms := []string{fmt.Sprintf("p * %.17g", price)}
	for _, dimension := range []struct{ variable, key string }{{"c", "CompletionRatio"}, {"cr", "CacheRatio"}, {"cc", "CreateCacheRatio"}, {"img", "ImageRatio"}} {
		ratio, ok := effective[dimension.key].(float64)
		if !ok {
			result.UnsupportedReason = "missing_effective_price"
			return result, nil
		}
		// An image ratio of one keeps image tokens in the input category.
		if dimension.variable == "img" && ratio == 1 {
			continue
		}
		terms = append(terms, fmt.Sprintf("%s * %.17g", dimension.variable, price*ratio))
	}
	result.Expression = `tier("base", ` + strings.Join(terms, " + ") + `)`
	if err := ValidateModelPricing(name, PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": result.Expression}); err != nil {
		return nil, err
	}
	return result, nil
}
