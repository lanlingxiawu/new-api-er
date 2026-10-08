package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The loss lines replace the abstract factors on the page: for each dimension
// where the upstream's actual cost (list price × upstream group ratio) is above
// our lowest sell price (platform price × lowest sell ratio), show both prices.

func lossLineVerdict(t *testing.T, platform, source PriceMonitorPriceCell, context priceMonitorLossContext) PriceMonitorPriceCell {
	t.Helper()
	headers := []PriceMonitorSourceHeader{
		{Key: priceMonitorPlatformKey, Type: priceMonitorPlatformKey},
		{Key: "channel-a", Type: priceSourceChannel},
	}
	context.Valid = true
	items := []PriceMonitorMatrixItem{{Model: "m", Prices: map[string]PriceMonitorPriceCell{
		priceMonitorPlatformKey: platform,
		"channel-a":             source,
	}}}
	applyPriceMonitorLossVerdicts(headers, items, map[string]priceMonitorLossContext{"channel-a": context})
	return items[0].Prices["channel-a"]
}

func TestPriceMonitorLossLinesPerToken(t *testing.T) {
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(20), Lanes: []PriceMonitorPriceLane{
		{Key: priceMonitorLaneCacheRead, Price: floatPointer(1)},
	}}
	source := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(12), Output: floatPointer(18), Lanes: []PriceMonitorPriceLane{
		{Key: priceMonitorLaneCacheRead, Price: floatPointer(2)},
	}}
	cell := lossLineVerdict(t, platform, source, priceMonitorLossContext{SellFactor: 1, Configured: 0.5})

	require.Equal(t, []string{priceMonitorLossKindMeasured}, cell.LossKinds)
	assert.Equal(t, []PriceMonitorLossLine{
		{Key: priceMonitorLossLineInput, Cost: 12, Sell: 10},
		{Key: priceMonitorLaneCacheRead, Cost: 2, Sell: 1},
	}, cell.LossLines, "output is cheaper upstream, so it is not listed")
}

func TestPriceMonitorLossLinesApplyBothRatios(t *testing.T) {
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(10)}
	source := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(9), Output: floatPointer(4)}
	cell := lossLineVerdict(t, platform, source, priceMonitorLossContext{SellFactor: 0.8, Configured: 0.5, UpstreamRatio: floatPointer(1.5)})

	require.Equal(t, []string{priceMonitorLossKindMeasured}, cell.LossKinds)
	require.Len(t, cell.LossLines, 1, "output: 4 × 1.5 = 6 is below 10 × 0.8 = 8")
	assert.Equal(t, priceMonitorLossLineInput, cell.LossLines[0].Key)
	assert.InDelta(t, 13.5, cell.LossLines[0].Cost, 1e-9, "9 × 1.5")
	assert.InDelta(t, 8, cell.LossLines[0].Sell, 1e-9, "10 × 0.8")
}

func TestPriceMonitorLossLinesPerRequest(t *testing.T) {
	cell := lossLineVerdict(t,
		PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(0.5)},
		PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(0.75)},
		priceMonitorLossContext{SellFactor: 1, Configured: 0.5})
	assert.Equal(t, []PriceMonitorLossLine{{Key: priceMonitorLossLinePrice, Cost: 0.75, Sell: 0.5}}, cell.LossLines)
}

func TestPriceMonitorLossLinesTiered(t *testing.T) {
	tiers := func(highOutput float64) []PriceMonitorPriceTier {
		return []PriceMonitorPriceTier{
			{Range: "low", ConditionVariable: "len", ConditionOperator: "<=", ConditionValue: floatPointer(200000), Input: 1, Output: 5},
			{Range: "high", ConditionVariable: "len", ConditionOperator: ">", ConditionValue: floatPointer(200000), Input: 2, Output: highOutput},
		}
	}
	cell := lossLineVerdict(t,
		PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Tiers: tiers(10)},
		PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Tiers: tiers(14)},
		priceMonitorLossContext{SellFactor: 1, Configured: 0.5})

	require.Len(t, cell.LossLines, 1)
	line := cell.LossLines[0]
	require.NotNil(t, line.Tier)
	assert.Equal(t, 1, *line.Tier, "the second tier")
	assert.Equal(t, priceMonitorLossLineOutput, line.Key)
	assert.InDelta(t, 14, line.Cost, 1e-9)
	assert.InDelta(t, 10, line.Sell, 1e-9)
}

// A configured-cost loss is about the cost ratio, not any price: no lines.
func TestPriceMonitorLossLinesAbsentForConfiguredOnly(t *testing.T) {
	cell := lossLineVerdict(t,
		PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(1)},
		PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(0.5)},
		priceMonitorLossContext{SellFactor: 0.7, Configured: 1.2})
	require.Equal(t, []string{priceMonitorLossKindConfigured}, cell.LossKinds)
	assert.Empty(t, cell.LossLines)
}

// The measured badge and the lines come from the same comparison: a badge
// always has at least one line, and a dimension exactly at the sell price is
// neither a loss nor a line.
func TestPriceMonitorLossLinesMatchTheMeasuredBadge(t *testing.T) {
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(20)}
	cases := map[string]struct {
		input, output float64
		upstream      *float64
		sell          float64
	}{
		"equal to the sell price":          {8, 16, nil, 0.8},
		"just above through the ratio":     {10, 20, floatPointer(1.0000001), 1},
		"equal after the upstream ratio":   {16, 32, floatPointer(0.5), 0.8},
		"output only":                      {5, 30, nil, 1},
		"below everywhere":                 {1, 1, nil, 1},
		"above after a marked-up upstream": {9, 9, floatPointer(1.5), 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			source := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(tc.input), Output: floatPointer(tc.output)}
			cell := lossLineVerdict(t, platform, source, priceMonitorLossContext{SellFactor: tc.sell, Configured: 0, UpstreamRatio: tc.upstream})
			measured := false
			for _, kind := range cell.LossKinds {
				measured = measured || kind == priceMonitorLossKindMeasured
			}
			assert.Equal(t, measured, len(cell.LossLines) > 0)
		})
	}
}

// Recompute resets the verdict before re-deciding; stale lines must not survive.
func TestResetPriceMonitorCellVerdictClearsLossLines(t *testing.T) {
	cell := resetPriceMonitorCellVerdict(PriceMonitorPriceCell{LossLines: []PriceMonitorLossLine{{Key: priceMonitorLossLineInput, Cost: 2, Sell: 1}}})
	assert.Nil(t, cell.LossLines)
}

// The share page must not see the lines: cost ÷ list price gives away the
// upstream group ratio.
func TestPublicPriceMonitorResultHidesLossLines(t *testing.T) {
	result := priceMonitorMatrixQueryResult{Items: []PriceMonitorMatrixItem{{
		Model: "m",
		Prices: map[string]PriceMonitorPriceCell{
			"c(1)": {LossKinds: []string{priceMonitorLossKindMeasured}, LossLines: []PriceMonitorLossLine{{Key: priceMonitorLossLineInput, Cost: 2, Sell: 1}}},
		},
	}}}
	stripPriceMonitorPublicResult(&result)
	cell := result.Items[0].Prices["c(1)"]
	assert.Equal(t, []string{priceMonitorLossKindMeasured}, cell.LossKinds)
	assert.Nil(t, cell.LossLines)
}
