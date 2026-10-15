package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestPriceMonitorDraftRiskIncludesZeroAndNewDimensions(t *testing.T) {
	source := PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Expr: `tier("base", p * 2 + c * 8 + ao * 12)`}
	draft := PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Expr: `tier("base", p * 0 + c * 8 + ao * 0)`}
	risks, comparable := priceMonitorDraftViolations("test", draft, source, 1, 1, "channel")
	require.True(t, comparable)
	require.NotEmpty(t, risks)
	found := map[string]bool{}
	for _, risk := range risks {
		found[risk.Field] = true
	}
	require.True(t, found["0:p"])
	require.True(t, found["0:ao"])
}

func TestPriceMonitorDraftRiskAlignsDifferentBoundaries(t *testing.T) {
	source := PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Expr: `len <= 100 ? tier("a", p * 2) : tier("b", p * 4)`}
	draft := PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Expr: `len <= 200 ? tier("a", p * 2) : tier("b", p * 4)`}
	risks, comparable := priceMonitorDraftViolations("test", draft, source, 1, 1, "channel")
	require.True(t, comparable)
	require.NotEmpty(t, risks)
	fields := make([]string, 0, len(risks))
	for _, risk := range risks {
		fields = append(fields, risk.Field)
	}
	require.Contains(t, fields, "101:p")
}

func TestPriceMonitorDynamicRiskIsUnknown(t *testing.T) {
	dynamic := PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Expr: `hour("UTC") < 8 ? p * 2 : p * 4`, Dynamic: true}
	_, comparable := priceMonitorDraftViolations("test", dynamic, dynamic, 1, 1, "channel")
	require.False(t, comparable)
}

func TestPriceMonitorDraftContextBindsPriceAndSource(t *testing.T) {
	snap := PriceMonitorSnapshot{CheckedAt: 1}
	changes := []model.ModelPricingChange{{ModelName: "test", ExpectedVersion: "a", Pricing: model.PricingValues{"ModelPrice": 0.1}}}
	contexts := map[string]priceMonitorLossContext{"source": {SellFactor: 1, Valid: true}}
	original := priceMonitorDraftContextVersion(snap, changes, contexts)
	require.Equal(t, original, priceMonitorDraftContextVersion(snap, changes, contexts))
	changes[0].Pricing["ModelPrice"] = 0.2
	require.NotEqual(t, original, priceMonitorDraftContextVersion(snap, changes, contexts))
	changes[0].Pricing["ModelPrice"] = 0.1
	contexts["source"] = priceMonitorLossContext{SellFactor: 0.5, Valid: true}
	require.NotEqual(t, original, priceMonitorDraftContextVersion(snap, changes, contexts))
}
