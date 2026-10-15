package controller

import (
	"math"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestPriceMonitorScenarioUsesActualExpressionMode(t *testing.T) {
	scenario := priceMonitorScenario{UsageSemantics: "openai", At: time.Now(), Usage: dto.Usage{PromptTokens: 1000, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 500}}}
	cell := singleTierCell(t, `p*2+cr*0`)
	markPriceMonitorDifferences(tokenCellWithCache(2, 0, 0), &cell)
	require.Equal(t, priceMonitorModeToken, cell.Mode)
	result := priceMonitorEvaluateCell("source", cell, scenario)
	require.Equal(t, "evaluated", result.Status)
	require.InDelta(t, 0.001, *result.AmountUSD, 1e-12)
}

func TestPriceMonitorScenarioRejectsInvalidTokenQuotes(t *testing.T) {
	scenario := priceMonitorScenario{UsageSemantics: "openai", At: time.Now(), Usage: dto.Usage{PromptTokens: 2, CompletionTokens: 1}}
	for _, price := range []float64{-1, math.Inf(1), math.NaN()} {
		result := priceMonitorEvaluateCell("source", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(price), Output: floatPointer(10)}, scenario)
		require.Equal(t, "unknown", result.Status)
		require.Nil(t, result.AmountUSD)
	}
}

func TestPriceMonitorScenarioUsesRawUsagePerExpression(t *testing.T) {
	scenario := priceMonitorScenario{ID: "cache", UsageSemantics: "openai", At: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Usage: dto.Usage{PromptTokens: 1000, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 500}}}
	old := priceMonitorEvaluateCell("platform", PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Expr: `p * 2`}, scenario)
	next := priceMonitorEvaluateCell("draft", PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Expr: `p * 2 + cr * 0`}, scenario)
	require.Equal(t, "evaluated", old.Status)
	require.Equal(t, "evaluated", next.Status)
	require.InDelta(t, 0.002, *old.AmountUSD, 1e-12)
	require.InDelta(t, 0.001, *next.AmountUSD, 1e-12)
}

func TestPriceMonitorScenarioFixedClock(t *testing.T) {
	cell := PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Expr: `hour("UTC") < 8 ? p * 1 : p * 2`}
	scenario := priceMonitorScenario{ID: "time", UsageSemantics: "openai", At: time.Date(2026, 1, 1, 7, 0, 0, 0, time.UTC), Usage: dto.Usage{PromptTokens: 1000}}
	first := priceMonitorEvaluateCell("draft", cell, scenario)
	require.Equal(t, "evaluated", first.Status)
	require.InDelta(t, 0.001, *first.AmountUSD, 1e-12)
	scenario.At = scenario.At.Add(time.Hour)
	second := priceMonitorEvaluateCell("draft", cell, scenario)
	require.Equal(t, "evaluated", second.Status)
	require.InDelta(t, 0.002, *second.AmountUSD, 1e-12)
}

func TestPriceMonitorScenarioUnsupportedAndZero(t *testing.T) {
	scenario := priceMonitorScenario{UsageSemantics: "openai", At: time.Now()}
	unsupported := priceMonitorEvaluateCell("draft", PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Expr: `fixed(1)`}, scenario)
	require.Equal(t, "unknown", unsupported.Status)
	require.Nil(t, unsupported.AmountUSD)
	zero := priceMonitorEvaluateCell("draft", PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(0)}, scenario)
	require.Equal(t, "evaluated", zero.Status)
	require.Equal(t, 0.0, *zero.AmountUSD)
	scenario.Usage.PromptTokens = -1
	require.Error(t, validatePriceMonitorScenario(scenario))
}
