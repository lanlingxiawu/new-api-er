package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 巡检快照之后该模型被切到表达式计费却未配置表达式：行内改价会让模型定价配置无效，
// 必须按模型定价校验整单拒绝、不写库不推进版本，并用可操作的提示指明模型。
func TestApplyPriceMonitorPriceRejectsPricingThatFailsModelValidation(t *testing.T) {
	keepPricingRatios(t)
	useApplyPriceEnv(t, applyPriceSnapshotWithFloor("gpt-4o", nil))
	seedPricingRatios(t, "gpt-4o", "1.5", "5")
	seedApplyPriceOption(t, "billing_setting.billing_mode", `{"gpt-4o":"tiered_expr"}`)

	_, response := performApplyPrice(t, `{"checked_at":100,"pricing_version":0,"items":[{"model":"gpt-4o",`+
		`"fields":{"model_ratio":4},"expected":{"model_ratio":1.5}}]}`)

	require.Equal(t, false, response["success"])
	message, _ := response["message"].(string)
	assert.Contains(t, message, "gpt-4o", "the message must name the model so the admin knows which row to fix")
	assert.NotContains(t, message, "billing expression is required", "validator internals must not reach the client")
	ratio, _ := applyPriceOptionValue(t, "ModelRatio", "gpt-4o")
	assert.InDelta(t, 1.5, ratio, 1e-9, "a rejected patch must not be written")
	assert.Equal(t, int64(0), model.GetPricingConfigVersion())
}
