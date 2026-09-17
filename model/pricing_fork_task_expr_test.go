package model

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The thirdpartysd2 pricing matrix has no model-level expression; the public
// pricing API publishes the task expression the model bills with.
func TestPricingPublishesThirdPartySD2MatrixExpression(t *testing.T) {
	resetPricingEndpointTestTables(t)
	const sd2Model = "dreamina-seedance-2-0-fast-260128"
	const source = `
const RESOLUTION = {enum: ["480p", "720p", "1080p", "4k"]};
const VIDEO_INPUT = {enum: ["none", "video"]};
export const meta = {
  apiVersion: 1, key: "thirdpartysd2", name: "Pricing SD2 Probe", version: "1.0.0", author: {name: "Test"},
  models: ["dreamina-seedance-2-0-fast-260128"], fetchMode: "per_task",
  usageSchema: {tokens: {type: "number", unit: "token"}, output_resolution: RESOLUTION, video_input: VIDEO_INPUT},
  usageExamples: [{label: "720p", facts: {tokens: 1000000, output_resolution: "720p", video_input: "none"}}]
};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {}; }
`
	// The model package does not load the built-in plugins, so the probe owns the key.
	_, hadBuiltin := jsplugin.DefaultRegistry.Get("thirdpartysd2")
	require.False(t, hadBuiltin)
	_, err := jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister("thirdpartysd2") })

	insertPricingEndpointChannel(t, pricingTestChannelID(951), constant.ChannelTypeTaskPlugin, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, pricingTestChannelID(951), sd2Model)
	insertPricingEndpointAbility(t, pricingTestChannelID(951), "ordinary-model")

	pricing := pricingByModel(GetPricing())
	require.Contains(t, pricing, sd2Model)
	expression, ok := model_setting.GetThirdPartySD2BillingExpr(sd2Model)
	require.True(t, ok)
	assert.Equal(t, "tiered_expr", pricing[sd2Model].BillingMode)
	assert.Equal(t, expression, pricing[sd2Model].BillingExpr)
	assert.Empty(t, pricing["ordinary-model"].BillingMode)
	assert.Empty(t, pricing["ordinary-model"].BillingExpr)
}
