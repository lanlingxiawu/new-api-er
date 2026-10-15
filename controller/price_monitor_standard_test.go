package controller

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPriceMonitorStandardCompleteAST(t *testing.T) {
	tiers, dynamic := parsePriceMonitorExpression(`len<=10?tier("a",p*2+c*3):len<=20?tier("b",p*4+c*6):tier("c",p*8+c*12)`)
	require.False(t, dynamic)
	require.Len(t, tiers, 3)
	_, dynamic = parsePriceMonitorExpression(`tier("a",p*2+c*3)+7`)
	require.True(t, dynamic)
}

func TestPriceMonitorUnknownComparisonStatus(t *testing.T) {
	source := PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Expr: `p*hour("UTC")`, Dynamic: true}
	markPriceMonitorDifferences(PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Expr: `p*day("UTC")`, Dynamic: true}, &source)
	require.Equal(t, "scenario_only", source.ComparisonStatus)
	require.False(t, priceMonitorCellHasComparableDifference(source))
}

func TestPriceMonitorCrossBoundaryFilters(t *testing.T) {
	left := singleTierCell(t, `len<=10?tier("a",p*2):tier("b",p*4)`)
	right := singleTierCell(t, `len<=20?tier("a",p*2):tier("b",p*4)`)
	require.True(t, priceMonitorSourceAbovePlatform(right, left))
	require.False(t, priceMonitorSourceAbovePlatform(left, right))
	require.False(t, priceMonitorPriceCellsEqual(left, right))
	same := singleTierCell(t, `len<=5?tier("a",p*2):len<=10?tier("b",p*2):tier("c",p*4)`)
	require.True(t, priceMonitorPriceCellsEqual(left, same))
}

func TestPriceMonitorStandardSnapshotWithoutExpression(t *testing.T) {
	cell, ok := priceMonitorCell(map[string]any{"billing_mode": map[string]any{"test": "tiered_expr"}, "billing_expr": map[string]any{"test": `len<=10?tier("a",p*2):tier("b",p*4+cr*0)`}}, "test")
	require.True(t, ok)
	require.NotNil(t, cell.Standard)
	cell.Expr = ""
	analysis, err := priceMonitorStandardCellAnalysis(cell)
	require.NoError(t, err)
	require.True(t, analysis.UsedVars["cr"])
	analysis.Segments[0].Prices["p"] = 99
	require.Equal(t, float64(2), cell.Standard.Segments[0].Prices["p"])
}

func TestPriceMonitorMissingDynamicExpressionDoesNotUseLegacyPrice(t *testing.T) {
	cell, ok := priceMonitorCell(map[string]any{"billing_mode": map[string]any{"test": "tiered_expr"}, "model_ratio": map[string]any{"test": float64(5)}, "model_price": map[string]any{"test": float64(2)}}, "test")
	require.True(t, ok)
	require.Equal(t, priceMonitorModeExpression, cell.Mode)
	require.True(t, cell.Dynamic)
	require.Equal(t, priceMonitorUnavailableMissing, cell.UnavailableReason)
	require.Nil(t, cell.Price)
}

func TestPriceMonitorNormalizationDifferenceCannotProveEquality(t *testing.T) {
	left := singleTierCell(t, `p*2`)
	right := singleTierCell(t, `p*2+cr*2`)
	markPriceMonitorDifferences(left, &right)
	require.Equal(t, "partial", right.ComparisonStatus)
	require.False(t, priceMonitorPriceCellsEqual(left, right))
}

func TestPriceMonitorFlattenedExpressionRetainsStandardSemantics(t *testing.T) {
	source := singleTierCell(t, `p*2+cr*0`)
	markPriceMonitorDifferences(tokenCellWithCache(2, 0, 0), &source)
	require.Equal(t, priceMonitorModeToken, source.Mode)
	parsed, err := priceMonitorStandardPrice(source)
	require.NoError(t, err)
	require.True(t, parsed.UsedVars["cr"])
	require.True(t, priceMonitorQuotedVariable(source, "img_o"))
}

func TestPriceMonitorUnknownRemainsVisibleForReview(t *testing.T) {
	cell := PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Dynamic: true, ComparisonStatus: "scenario_only"}
	item := PriceMonitorMatrixItem{Model: "custom", Prices: map[string]PriceMonitorPriceCell{"platform": cell, "source": cell}}
	headers := []PriceMonitorSourceHeader{{Key: "platform", Type: priceMonitorPlatformKey}, {Key: "source", Type: priceSourceChannel}}
	for _, filter := range []string{"all", "needs_review"} {
		visible, keys := priceMonitorMatrixItemDisplayKeys(item, headers, filter)
		require.True(t, visible)
		require.Contains(t, keys, "source")
	}
	visible, _ := priceMonitorMatrixItemDisplayKeys(item, headers, "channel_platform")
	require.False(t, visible)
}
