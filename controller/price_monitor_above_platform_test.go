package controller

import (
	"sort"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func priceMonitorAbovePlatformSnapshot(items []PriceMonitorMatrixItem, headers []PriceMonitorSourceHeader) PriceMonitorSnapshot {
	return PriceMonitorSnapshot{SourceHeaders: headers, MatrixItems: items}
}

func priceMonitorAbovePlatformHeaders() []PriceMonitorSourceHeader {
	return []PriceMonitorSourceHeader{
		{Key: priceMonitorPlatformKey, Type: priceMonitorPlatformKey},
		{Key: "official", Type: priceSourceOfficial},
		{Key: "models-dev", Type: priceSourceModelsDev},
		{Key: "channel-a", Type: priceSourceChannel},
		{Key: "channel-b", Type: priceSourceChannel},
	}
}

func priceMonitorHeaderKeys(headers []PriceMonitorSourceHeader) []string {
	keys := make([]string, 0, len(headers))
	for _, header := range headers {
		keys = append(keys, header.Key)
	}
	return keys
}

func priceMonitorSortedPriceKeys(item PriceMonitorMatrixItem) []string {
	keys := make([]string, 0, len(item.Prices))
	for key := range item.Prices {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if (keys[i] == priceMonitorPlatformKey) != (keys[j] == priceMonitorPlatformKey) {
			return keys[i] == priceMonitorPlatformKey
		}
		return keys[i] < keys[j]
	})
	return keys
}

func priceMonitorHighestKey(item PriceMonitorMatrixItem) string {
	highest := ""
	for key, price := range item.Prices {
		if !price.Highest {
			continue
		}
		if highest != "" {
			return "<multiple>"
		}
		highest = key
	}
	return highest
}

func TestPriceMonitorSourceAbovePlatformPerToken(t *testing.T) {
	platform := PriceMonitorPriceCell{
		Mode:   priceMonitorModeToken,
		Input:  floatPointer(10),
		Output: floatPointer(20),
		Lanes: []PriceMonitorPriceLane{
			{Key: priceMonitorLaneCacheRead, Price: floatPointer(5)},
			{Key: priceMonitorLaneCacheWrite, Price: floatPointer(12)},
		},
	}
	tests := []struct {
		name   string
		source PriceMonitorPriceCell
		above  bool
	}{
		{"input higher", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(11), Output: floatPointer(20)}, true},
		{"output higher", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(21)}, true},
		{"cache read lane higher", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(20), Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(6)}}}, true},
		{"cache write lane higher", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(20), Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheWrite, Price: floatPointer(13)}}}, true},
		{"every dimension higher", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(11), Output: floatPointer(21), Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(6)}}}, true},
		{"input higher output lower", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(11), Output: floatPointer(1)}, true},
		{"equal", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(20)}, false},
		{"every dimension lower", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(9), Output: floatPointer(19), Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(4)}}}, false},
		{"lane lower", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(20), Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(1)}}}, false},
		{"source output missing", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10)}, false},
		{"source lane price missing", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(20), Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead}}}, false},
		{"lane absent on platform", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(20), Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneAudioInput, Price: floatPointer(999)}}}, false},
		{"within tolerance", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10 + 1e-12), Output: floatPointer(20)}, false},
		{"outside tolerance", PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10 + 1e-6), Output: floatPointer(20)}, true},
		{"different billing mode", PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(999)}, false},
		{"source unavailable", PriceMonitorPriceCell{UnavailableReason: priceMonitorUnavailableMissing}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.above, priceMonitorSourceAbovePlatform(platform, test.source))
		})
	}

	require.False(t, priceMonitorSourceAbovePlatform(
		PriceMonitorPriceCell{UnavailableReason: priceMonitorUnavailableSourceFailed},
		PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(99)},
	), "an unavailable platform cell cannot be compared")

	platformInputOnly := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10)}
	require.False(t, priceMonitorSourceAbovePlatform(
		platformInputOnly,
		PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(50)},
	), "an output price the platform never configured is not a higher price")
}

func TestPriceMonitorSourceAbovePlatformPerRequest(t *testing.T) {
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(0.5)}

	require.True(t, priceMonitorSourceAbovePlatform(platform, PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(0.6)}))
	require.False(t, priceMonitorSourceAbovePlatform(platform, PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(0.5)}))
	require.False(t, priceMonitorSourceAbovePlatform(platform, PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(0.4)}))
	require.False(t, priceMonitorSourceAbovePlatform(platform, PriceMonitorPriceCell{Mode: priceMonitorModeRequest}))
	require.False(t, priceMonitorSourceAbovePlatform(PriceMonitorPriceCell{Mode: priceMonitorModeRequest}, PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(0.6)}))
}

func TestPriceMonitorSourceAbovePlatformTieredExpression(t *testing.T) {
	platform := PriceMonitorPriceCell{
		Mode: priceMonitorModeExpression,
		Tiers: []PriceMonitorPriceTier{
			{Range: "low", ConditionVariable: "len", ConditionOperator: "<=", ConditionValue: floatPointer(200000), Input: 1, Output: 5, Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(0.5)}}},
			{Range: "high", ConditionVariable: "len", ConditionOperator: ">", ConditionValue: floatPointer(200000), Input: 2, Output: 10},
		},
	}
	higherSecondTier := PriceMonitorPriceCell{
		Mode: priceMonitorModeExpression,
		Tiers: []PriceMonitorPriceTier{
			{Range: "low", ConditionVariable: "len", ConditionOperator: "<=", ConditionValue: floatPointer(200000), Input: 1, Output: 5, Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(0.5)}}},
			{Range: "high", ConditionVariable: "len", ConditionOperator: ">", ConditionValue: floatPointer(200000), Input: 2, Output: 11},
		},
	}
	higherLane := PriceMonitorPriceCell{
		Mode: priceMonitorModeExpression,
		Tiers: []PriceMonitorPriceTier{
			{Range: "low", ConditionVariable: "len", ConditionOperator: "<=", ConditionValue: floatPointer(200000), Input: 1, Output: 5, Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(0.9)}}},
			{Range: "high", ConditionVariable: "len", ConditionOperator: ">", ConditionValue: floatPointer(200000), Input: 2, Output: 10},
		},
	}
	fewerTiers := PriceMonitorPriceCell{
		Mode:  priceMonitorModeExpression,
		Tiers: []PriceMonitorPriceTier{{Range: "all", Input: 99, Output: 99}},
	}
	otherCondition := PriceMonitorPriceCell{
		Mode: priceMonitorModeExpression,
		Tiers: []PriceMonitorPriceTier{
			{Range: "low", ConditionVariable: "len", ConditionOperator: "<=", ConditionValue: floatPointer(128000), Input: 9, Output: 9},
			{Range: "high", ConditionVariable: "len", ConditionOperator: ">", ConditionValue: floatPointer(128000), Input: 9, Output: 9},
		},
	}

	require.True(t, priceMonitorSourceAbovePlatform(platform, higherSecondTier))
	require.True(t, priceMonitorSourceAbovePlatform(platform, higherLane))
	require.False(t, priceMonitorSourceAbovePlatform(platform, platform))
	require.False(t, priceMonitorSourceAbovePlatform(platform, fewerTiers), "a different tier count is not comparable")
	require.False(t, priceMonitorSourceAbovePlatform(platform, otherCondition), "different tier conditions are not comparable")
	require.False(t, priceMonitorSourceAbovePlatform(platform, PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Dynamic: true, Tiers: platform.Tiers}), "dynamic source expressions are not comparable")
	require.False(t, priceMonitorSourceAbovePlatform(PriceMonitorPriceCell{Mode: priceMonitorModeExpression, Dynamic: true, Tiers: platform.Tiers}, higherSecondTier), "dynamic platform expressions are not comparable")
}

func TestQueryPriceMonitorMatrixAbovePlatform(t *testing.T) {
	headers := priceMonitorAbovePlatformHeaders()
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(20)}
	snapshot := priceMonitorAbovePlatformSnapshot([]PriceMonitorMatrixItem{
		{Model: "cheaper-and-pricier", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"official":              {Mode: priceMonitorModeToken, Input: floatPointer(9), Output: floatPointer(19), Different: true, InputDifferent: true, OutputDifferent: true},
			"channel-a":             {Mode: priceMonitorModeToken, Input: floatPointer(12), Output: floatPointer(20), Different: true, InputDifferent: true},
			"channel-b":             {Mode: priceMonitorModeToken, Input: floatPointer(1), Output: floatPointer(2), Different: true, InputDifferent: true, OutputDifferent: true},
		}},
		{Model: "official-only-pricier", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"official":              {Mode: priceMonitorModeToken, Input: floatPointer(30), Output: floatPointer(20), Different: true, InputDifferent: true},
			"channel-a":             platform,
		}},
		{Model: "models-dev-only-pricier", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"models-dev":            {Mode: priceMonitorModeToken, Input: floatPointer(99), Output: floatPointer(20), Different: true, InputDifferent: true},
			"channel-a":             platform,
		}},
		{Model: "nothing-pricier", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"channel-a":             {Mode: priceMonitorModeToken, Input: floatPointer(2), Output: floatPointer(3), Different: true, InputDifferent: true, OutputDifferent: true},
		}},
		{Model: "unavailable-sources", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"official":              {Different: true, UnavailableReason: priceMonitorUnavailableMissing},
			"channel-a":             {UnavailableReason: priceMonitorUnavailablePlaceholder},
			"channel-b":             {UnavailableReason: priceMonitorUnavailableSourceFailed},
		}},
		{Model: "other-billing-mode", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"channel-a":             {Mode: priceMonitorModeRequest, Price: floatPointer(500), Different: true, ModeDifferent: true},
		}},
	}, headers)

	result := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{Comparison: "above_platform", Page: 1, PageSize: 20})

	models := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		models = append(models, item.Model)
	}
	require.Equal(t, []string{"cheaper-and-pricier", "official-only-pricier"}, models)
	require.Equal(t, 2, result.Total)
	require.Equal(t, "above_platform", result.AppliedFilters.Comparison)

	require.Equal(t, []string{priceMonitorPlatformKey, "official", "channel-a"}, priceMonitorHeaderKeys(result.SourceHeaders))
	require.Equal(t, []string{priceMonitorPlatformKey, "channel-a"}, priceMonitorSortedPriceKeys(result.Items[0]))
	require.Equal(t, []string{priceMonitorPlatformKey, "official"}, priceMonitorSortedPriceKeys(result.Items[1]))
}

func TestQueryPriceMonitorMatrixAbovePlatformMarksHighestSource(t *testing.T) {
	headers := priceMonitorAbovePlatformHeaders()
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(20)}
	snapshot := priceMonitorAbovePlatformSnapshot([]PriceMonitorMatrixItem{
		{Model: "highest-channel", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"official":              {Mode: priceMonitorModeToken, Input: floatPointer(11), Output: floatPointer(20), Different: true, InputDifferent: true},
			"channel-a":             {Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(40), Different: true, OutputDifferent: true},
			"channel-b":             {Mode: priceMonitorModeToken, Input: floatPointer(12), Output: floatPointer(21), Different: true, InputDifferent: true, OutputDifferent: true},
		}},
		{Model: "highest-official", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"official":              {Mode: priceMonitorModeToken, Input: floatPointer(50), Output: floatPointer(60), Different: true, InputDifferent: true, OutputDifferent: true},
			"channel-a":             {Mode: priceMonitorModeToken, Input: floatPointer(11), Output: floatPointer(20), Different: true, InputDifferent: true},
		}},
		{Model: "tied-sources", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"channel-a":             {Mode: priceMonitorModeToken, Input: floatPointer(11), Output: floatPointer(20), Different: true, InputDifferent: true},
			"channel-b":             {Mode: priceMonitorModeToken, Input: floatPointer(11), Output: floatPointer(20), Different: true, InputDifferent: true},
		}},
	}, headers)

	result := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{Comparison: "above_platform", Page: 1, PageSize: 20})
	require.Len(t, result.Items, 3)

	require.Equal(t, "channel-a", priceMonitorHighestKey(result.Items[0]), "the largest total token price wins even when another source has a higher input price")
	require.Equal(t, "official", priceMonitorHighestKey(result.Items[1]))
	require.Equal(t, "channel-a", priceMonitorHighestKey(result.Items[2]), "ties resolve to the first source header")
	require.False(t, result.Items[0].Prices[priceMonitorPlatformKey].Highest, "the platform column is never the highest source")

	reordered := priceMonitorAbovePlatformSnapshot(snapshot.MatrixItems, []PriceMonitorSourceHeader{headers[0], headers[4], headers[3], headers[1], headers[2]})
	reorderedResult := queryPriceMonitorMatrix(reordered, priceMonitorQuery{Comparison: "above_platform", Page: 1, PageSize: 20})
	require.Equal(t, "channel-a", priceMonitorHighestKey(reorderedResult.Items[0]), "a header order change must not move a strictly higher score")
	require.Equal(t, "channel-b", priceMonitorHighestKey(reorderedResult.Items[2]), "ties follow the header order")

	for _, comparison := range []string{"all", "channel_platform", "channel_official"} {
		other := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{Comparison: comparison, Page: 1, PageSize: 20})
		for _, item := range other.Items {
			for key, price := range item.Prices {
				require.False(t, price.Highest, "%s must not mark a highest source on %s/%s", comparison, item.Model, key)
			}
		}
	}
}

func TestQueryPriceMonitorMatrixAbovePlatformHighestUsesTieredAndRequestScores(t *testing.T) {
	headers := priceMonitorAbovePlatformHeaders()
	requestPlatform := PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(1)}
	tieredPlatform := PriceMonitorPriceCell{
		Mode:  priceMonitorModeExpression,
		Tiers: []PriceMonitorPriceTier{{Range: "low", Input: 1, Output: 2}, {Range: "high", Input: 3, Output: 4}},
	}
	snapshot := priceMonitorAbovePlatformSnapshot([]PriceMonitorMatrixItem{
		{Model: "per-request", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: requestPlatform,
			"channel-a":             {Mode: priceMonitorModeRequest, Price: floatPointer(2), Different: true, PriceDifferent: true},
			"channel-b":             {Mode: priceMonitorModeRequest, Price: floatPointer(3), Different: true, PriceDifferent: true},
		}},
		{Model: "tiered", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: tieredPlatform,
			"channel-a": {Mode: priceMonitorModeExpression, Different: true, ModeDifferent: true, Tiers: []PriceMonitorPriceTier{
				{Range: "low", Input: 9, Output: 9},
				{Range: "high", Input: 3, Output: 4},
			}},
			"channel-b": {Mode: priceMonitorModeExpression, Different: true, ModeDifferent: true, Tiers: []PriceMonitorPriceTier{
				{Range: "low", Input: 1, Output: 2},
				{Range: "high", Input: 3, Output: 5},
			}},
		}},
	}, headers)

	result := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{Comparison: "above_platform", Page: 1, PageSize: 20})
	require.Len(t, result.Items, 2)
	require.Equal(t, "channel-b", priceMonitorHighestKey(result.Items[0]))
	require.Equal(t, "channel-a", priceMonitorHighestKey(result.Items[1]), "the tiered score uses the most expensive tier")
}

func TestQueryPriceMonitorMatrixAbovePlatformRespectsSourceKeyFilter(t *testing.T) {
	headers := priceMonitorAbovePlatformHeaders()
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(20)}
	items := []PriceMonitorMatrixItem{
		{Model: "channel-b-only", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"channel-a":             {Mode: priceMonitorModeToken, Input: floatPointer(1), Output: floatPointer(2), Different: true, InputDifferent: true, OutputDifferent: true},
			"channel-b":             {Mode: priceMonitorModeToken, Input: floatPointer(40), Output: floatPointer(20), Different: true, InputDifferent: true},
		}},
	}
	snapshot := priceMonitorAbovePlatformSnapshot(items, headers)

	hidden := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{Comparison: "above_platform", SourceKeys: []string{"channel-a"}, SourceKeysSet: true, Page: 1, PageSize: 20})
	require.Empty(t, hidden.Items, "hiding the expensive channel removes the model from the table")

	shown := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{Comparison: "above_platform", SourceKeys: []string{"channel-b"}, SourceKeysSet: true, Page: 1, PageSize: 20})
	require.Len(t, shown.Items, 1)
	require.Equal(t, "channel-b", priceMonitorHighestKey(shown.Items[0]))

	require.Equal(t, 1, countPriceMonitorComparisonModels(headers, items).AbovePlatform, "the summary keeps the global scope regardless of the table filter")
}

func TestCountPriceMonitorComparisonModelsAbovePlatform(t *testing.T) {
	headers := priceMonitorAbovePlatformHeaders()
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(10), Output: floatPointer(20)}
	pricier := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(11), Output: floatPointer(20), Different: true, InputDifferent: true}
	cheaper := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(1), Output: floatPointer(2), Different: true, InputDifferent: true, OutputDifferent: true}
	items := []PriceMonitorMatrixItem{
		{Model: "two-pricier-channels", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"channel-a":             pricier,
			"channel-b":             pricier,
		}},
		{Model: "pricier-official", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"official":              pricier,
			"channel-a":             cheaper,
		}},
		{Model: "pricier-models-dev", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"models-dev":            pricier,
			"channel-a":             cheaper,
		}},
		{Model: "all-cheaper", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: platform,
			"official":              cheaper,
			"channel-a":             cheaper,
		}},
	}

	counts := countPriceMonitorComparisonModels(headers, items)
	require.Equal(t, 2, counts.AbovePlatform, "each model counts once and models.dev never contributes")
	require.Zero(t, countPriceMonitorComparisonModels(headers, nil).AbovePlatform)
	require.Zero(t, countPriceMonitorComparisonModels([]PriceMonitorSourceHeader{headers[0]}, items).AbovePlatform)
}

func TestPriceMonitorStatusSnapshotIncludesAbovePlatformCount(t *testing.T) {
	counts := PriceMonitorComparisonModelCounts{PlatformOfficial: 1, PlatformModelsDev: 2, PlatformChannel: 3, ChannelOfficial: 4, ChannelModelsDev: 5, AbovePlatform: 6}
	status := priceMonitorStatusSnapshot(PriceMonitorSnapshot{ComparisonModelCounts: counts})

	require.Equal(t, counts, status["comparison_model_counts"])
}

func TestPriceMonitorAbovePlatformSnapshotRoundTrip(t *testing.T) {
	snapshot := PriceMonitorSnapshot{
		ComparisonModelCounts: PriceMonitorComparisonModelCounts{AbovePlatform: 7},
		MatrixItems: []PriceMonitorMatrixItem{{Model: "model", Prices: map[string]PriceMonitorPriceCell{
			"channel": {Mode: priceMonitorModeToken, Input: floatPointer(3), Highest: true},
		}}},
	}
	encoded, err := common.Marshal(snapshot)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"above_platform":7`)
	require.Contains(t, string(encoded), `"highest":true`)

	var decoded PriceMonitorSnapshot
	require.NoError(t, common.Unmarshal(encoded, &decoded))
	require.Equal(t, 7, decoded.ComparisonModelCounts.AbovePlatform)
	require.True(t, decoded.MatrixItems[0].Prices["channel"].Highest)

	var legacy PriceMonitorSnapshot
	require.NoError(t, common.Unmarshal([]byte(`{"comparison_model_counts":{"platform_official":1}}`), &legacy))
	require.Zero(t, legacy.ComparisonModelCounts.AbovePlatform)
}
