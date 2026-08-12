package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFilterPriceMonitorMarketplaceModelsExcludesWhitelist(t *testing.T) {
	pricing := []model.Pricing{{ModelName: "gpt-4.1"}, {ModelName: "claude-3"}, {ModelName: "Gemini-Pro"}, {ModelName: ""}}

	all := filterPriceMonitorMarketplaceModels(pricing, "")
	filtered := filterPriceMonitorMarketplaceModels(pricing, " gpt-4.1, claude-3\nunknown\ngpt-4.1 ")
	caseSensitive := filterPriceMonitorMarketplaceModels(pricing, "gemini-pro")

	require.Equal(t, map[string]struct{}{"gpt-4.1": {}, "claude-3": {}, "Gemini-Pro": {}}, all)
	require.Equal(t, map[string]struct{}{"Gemini-Pro": {}}, filtered)
	require.Contains(t, caseSensitive, "Gemini-Pro")
}

func TestBuildPriceMonitorItemsFiltersMarketplaceAndUntrustedSources(t *testing.T) {
	differences := map[string]map[string]dto.DifferenceItem{
		"visible-model": {
			"model_ratio": {
				Current: 2.0,
				Upstreams: map[string]interface{}{
					"channel-a": "same",
					"channel-b": 3.0,
					"channel-c": 5.0,
					"official":  4.0,
				},
				Confidence: map[string]bool{"channel-a": true, "channel-b": false, "channel-c": true, "official": true},
			},
		},
		"hidden-model": {
			"model_ratio": {Current: 1.0, Upstreams: map[string]interface{}{"official": 2.0}, Confidence: map[string]bool{"official": true}},
		},
	}

	items := buildPriceMonitorItems(
		differences,
		map[string]struct{}{"visible-model": {}},
		map[string]string{"channel-a": priceSourceChannel, "channel-b": priceSourceChannel, "channel-c": priceSourceChannel, "official": priceSourceOfficial},
		map[string]map[string]struct{}{"channel-a": {"visible-model": {}}, "channel-b": {"visible-model": {}}, "channel-c": {"other-model": {}}},
	)

	require.Equal(t, []PriceMonitorItem{{
		Model:    "visible-model",
		Field:    "model_ratio",
		Platform: 2.0,
		Sources:  []PriceMonitorSource{{Name: "official", Value: 4.0, Type: priceSourceOfficial}},
	}}, items)
}

func TestBuildPriceMonitorMatrixConvertsEverySourceIndependently(t *testing.T) {
	localData := map[string]any{
		"model_ratio":      map[string]float64{"model-a": 1},
		"completion_ratio": map[string]float64{"model-a": 4},
	}
	sources := []pricingSource{
		{name: "官方价格", data: map[string]any{"model_ratio": map[string]float64{"model-a": 1}, "completion_ratio": map[string]float64{"model-a": 4}}},
		{name: "渠道乙(2)", data: map[string]any{"model_ratio": map[string]float64{"model-a": .5}, "completion_ratio": map[string]float64{"model-a": 3}}},
	}

	headers, items := buildPriceMonitorMatrix(localData, sources, map[string]struct{}{"model-a": {}}, map[string]string{"官方价格": priceSourceOfficial, "渠道乙(2)": priceSourceChannel})

	require.Equal(t, []PriceMonitorSourceHeader{
		{Key: priceMonitorPlatformKey, Name: "平台配置", Type: priceMonitorPlatformKey},
		{Key: "官方价格", Name: "官方价格", Type: priceSourceOfficial},
		{Key: "渠道乙(2)", Name: "渠道乙", Type: priceSourceChannel},
	}, headers)
	require.Len(t, items, 1)
	require.Equal(t, 2.0, *items[0].Prices[priceMonitorPlatformKey].Input)
	require.Equal(t, 8.0, *items[0].Prices[priceMonitorPlatformKey].Output)
	require.Equal(t, 1.0, *items[0].Prices["渠道乙(2)"].Input)
	require.Equal(t, 3.0, *items[0].Prices["渠道乙(2)"].Output)
	require.False(t, items[0].Prices["官方价格"].Different)
	require.True(t, items[0].Prices["渠道乙(2)"].Different)
}

func TestBuildPriceMonitorMatrixExplainsUnavailableSources(t *testing.T) {
	localData := map[string]any{"model_ratio": map[string]float64{"model-a": 1}, "completion_ratio": map[string]float64{"model-a": 4}}
	sources := []pricingSource{
		{name: "官方倍率预设(-100)", data: map[string]any{"model_ratio": map[string]float64{"model-a": 2}, "completion_ratio": map[string]float64{"model-a": 4}}},
		{name: "缺失渠道(7)", data: map[string]any{}},
		{name: "占位渠道(8)", data: map[string]any{"model_ratio": map[string]float64{"model-a": 37.5}, "completion_ratio": map[string]float64{"model-a": 1}}},
	}
	types := map[string]string{"官方倍率预设(-100)": priceSourceOfficial, "缺失渠道(7)": priceSourceChannel, "占位渠道(8)": priceSourceChannel}

	headers, items := buildPriceMonitorMatrix(localData, sources, map[string]struct{}{"model-a": {}}, types)

	require.Equal(t, "官方价格", headers[1].Name)
	require.Equal(t, "占位渠道", headers[2].Name)
	require.Equal(t, "缺失渠道", headers[3].Name)
	require.Len(t, items, 1)
	require.Equal(t, priceMonitorUnavailableMissing, items[0].Prices["缺失渠道(7)"].UnavailableReason)
	require.Equal(t, priceMonitorUnavailablePlaceholder, items[0].Prices["占位渠道(8)"].UnavailableReason)
}

func TestPriceMonitorSourceLabelsDisambiguateDuplicateChannelNames(t *testing.T) {
	headers := buildPriceMonitorSourceHeaders([]pricingSource{{name: "同名渠道(1)"}, {name: "同名渠道(2)"}}, map[string]string{"同名渠道(1)": priceSourceChannel, "同名渠道(2)": priceSourceChannel})
	require.Equal(t, "同名渠道", headers[1].Name)
	require.Equal(t, "同名渠道（2）", headers[2].Name)
}

func TestBuildPriceMonitorMatrixOnlyComparesEnabledChannelModels(t *testing.T) {
	localData := map[string]any{
		"model_ratio":      map[string]float64{"model-a": 1, "model-b": 1},
		"completion_ratio": map[string]float64{"model-a": 1, "model-b": 1},
	}
	sources := []pricingSource{
		{name: "official", data: map[string]any{"model_ratio": map[string]float64{"model-a": 2}, "completion_ratio": map[string]float64{"model-a": 1}}},
		{name: "channel", data: map[string]any{"model_ratio": map[string]float64{"model-a": 3, "model-b": 4}, "completion_ratio": map[string]float64{"model-a": 1, "model-b": 1}}, applicableModels: map[string]struct{}{"model-b": {}}},
	}
	types := map[string]string{"official": priceSourceOfficial, "channel": priceSourceChannel}

	_, items := buildPriceMonitorMatrix(localData, sources, map[string]struct{}{"model-a": {}, "model-b": {}}, types)

	require.Len(t, items, 2)
	require.NotContains(t, items[0].Prices, "channel")
	require.Equal(t, priceMonitorUnavailableMissing, items[1].Prices["official"].UnavailableReason)
	require.True(t, items[1].Prices["official"].Different)
	require.Contains(t, items[1].Prices, "channel")
}

func TestBuildPriceMonitorMatrixKeepsFailedOfficialSourceVisible(t *testing.T) {
	localData := map[string]any{"model_ratio": map[string]float64{"model-a": 1}, "completion_ratio": map[string]float64{"model-a": 1}}
	sources := []pricingSource{
		{name: "official", failed: true},
		{name: "channel", data: map[string]any{"model_ratio": map[string]float64{"model-a": 2}, "completion_ratio": map[string]float64{"model-a": 1}}},
	}

	_, items := buildPriceMonitorMatrix(localData, sources, map[string]struct{}{"model-a": {}}, map[string]string{"official": priceSourceOfficial, "channel": priceSourceChannel})

	require.Len(t, items, 1)
	require.Equal(t, priceMonitorUnavailableSourceFailed, items[0].Prices["official"].UnavailableReason)
	require.False(t, items[0].Prices["official"].Different)
}

func TestBuildPriceMonitorMatrixHandlesFixedExpressionAndUntrustedSources(t *testing.T) {
	localData := map[string]any{
		"model_price":      map[string]float64{"fixed-model": .04},
		"billing_mode":     map[string]string{"expr-model": "tiered_expr"},
		"billing_expr":     map[string]string{"expr-model": "p * 2"},
		"model_ratio":      map[string]float64{"untrusted-model": 1},
		"completion_ratio": map[string]float64{"untrusted-model": 1},
	}
	sources := []pricingSource{{name: "渠道甲(1)", data: map[string]any{
		"model_price":      map[string]float64{"fixed-model": .05},
		"billing_mode":     map[string]string{"expr-model": "tiered_expr"},
		"billing_expr":     map[string]string{"expr-model": "p * 3"},
		"model_ratio":      map[string]float64{"untrusted-model": 37.5},
		"completion_ratio": map[string]float64{"untrusted-model": 1},
	}}}

	_, items := buildPriceMonitorMatrix(localData, sources, map[string]struct{}{"fixed-model": {}, "expr-model": {}, "untrusted-model": {}}, map[string]string{"渠道甲(1)": priceSourceChannel})

	require.Len(t, items, 2)
	require.Equal(t, priceMonitorModeExpression, items[0].Prices[priceMonitorPlatformKey].Mode)
	require.True(t, items[0].Prices["渠道甲(1)"].Different)
	require.True(t, items[0].Prices["渠道甲(1)"].ModeDifferent)
	require.Equal(t, priceMonitorModeRequest, items[1].Prices[priceMonitorPlatformKey].Mode)
	require.Equal(t, .04, *items[1].Prices[priceMonitorPlatformKey].Price)
	require.Equal(t, .05, *items[1].Prices["渠道甲(1)"].Price)
	require.True(t, items[1].Prices["渠道甲(1)"].PriceDifferent)
}

func TestPriceMonitorCellParsesStandardTieredExpression(t *testing.T) {
	expression := `len <= 200000 ? tier("0_200k", p * 1.25 + c * 10 + cr * 0.125) : tier("200k_plus", p * 2.5 + c * 15 + cr * 0.25)`
	data := map[string]any{
		"billing_mode": map[string]string{"gemini": "tiered_expr"},
		"billing_expr": map[string]string{"gemini": expression},
	}

	cell, ok := priceMonitorCell(data, "gemini")

	require.True(t, ok)
	require.Equal(t, priceMonitorModeExpression, cell.Mode)
	require.Equal(t, []PriceMonitorPriceTier{
		{Range: "输入长度 ≤ 200,000 tokens", ConditionVariable: "len", ConditionOperator: "<=", ConditionValue: floatPointer(200000), Input: 1.25, Output: 10, Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(.125)}}},
		{Range: "输入长度 > 200,000 tokens", ConditionVariable: "len", ConditionOperator: ">", ConditionValue: floatPointer(200000), Input: 2.5, Output: 15, Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(.25)}}},
	}, cell.Tiers)
}

func TestPriceMonitorCellConvertsAllOptionalRatioFields(t *testing.T) {
	data := map[string]any{
		"model_ratio":            map[string]float64{"all-fields": 1},
		"completion_ratio":       map[string]float64{"all-fields": 4},
		"cache_ratio":            map[string]float64{"all-fields": .25},
		"create_cache_ratio":     map[string]float64{"all-fields": 1.25},
		"image_ratio":            map[string]float64{"all-fields": 2},
		"audio_ratio":            map[string]float64{"all-fields": 3},
		"audio_completion_ratio": map[string]float64{"all-fields": 6},
	}

	cell, ok := priceMonitorCell(data, "all-fields")

	require.True(t, ok)
	require.Equal(t, []PriceMonitorPriceLane{
		{Key: priceMonitorLaneCacheRead, Price: floatPointer(.5)},
		{Key: priceMonitorLaneCacheWrite, Price: floatPointer(2.5)},
		{Key: priceMonitorLaneImageInput, Price: floatPointer(4)},
		{Key: priceMonitorLaneAudioInput, Price: floatPointer(6)},
		{Key: priceMonitorLaneAudioOutput, Price: floatPointer(12)},
	}, cell.Lanes)
}

func TestPriceMonitorCellKeepsInputOnlyPrice(t *testing.T) {
	cell, ok := priceMonitorCell(map[string]any{
		"model_ratio": map[string]float64{"text-embedding-3-small": .01},
	}, "text-embedding-3-small")

	require.True(t, ok)
	require.Equal(t, priceMonitorModeToken, cell.Mode)
	require.Equal(t, .02, *cell.Input)
	require.Nil(t, cell.Output)
}

func TestPriceMonitorCellDoesNotGuessDynamicExpressionPrice(t *testing.T) {
	data := map[string]any{
		"billing_mode": map[string]string{"dynamic": "tiered_expr"},
		"billing_expr": map[string]string{"dynamic": `tier("base", p * 2 + c * 8)|||when(header("x-fast") has "1") * 2`},
	}

	cell, ok := priceMonitorCell(data, "dynamic")

	require.True(t, ok)
	require.Empty(t, cell.Tiers)
	require.True(t, cell.Dynamic)

	data["billing_expr"] = map[string]string{"dynamic": `tier("base", max(p * 2, p * 3) + c * 8)`}
	cell, ok = priceMonitorCell(data, "dynamic")
	require.True(t, ok)
	require.Empty(t, cell.Tiers)
	require.True(t, cell.Dynamic)
}

func TestPriceMonitorDifferenceFlagsAreFieldSpecific(t *testing.T) {
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(2), Output: floatPointer(8), Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(.5)}}}
	source := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(2), Output: floatPointer(9), Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(.6)}}}

	markPriceMonitorDifferences(platform, &source)

	require.True(t, source.Different)
	require.False(t, source.InputDifferent)
	require.True(t, source.OutputDifferent)
	require.False(t, source.ModeDifferent)
	require.True(t, source.Lanes[0].Different)
}

func TestPriceMonitorDifferenceFlagsHandleMissingPrices(t *testing.T) {
	t.Run("both outputs unavailable", func(t *testing.T) {
		platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(.02)}
		source := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(.02)}

		markPriceMonitorDifferences(platform, &source)

		require.False(t, source.InputDifferent)
		require.False(t, source.OutputDifferent)
		require.False(t, source.Different)
	})

	t.Run("only source output unavailable", func(t *testing.T) {
		platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(.02), Output: floatPointer(.02)}
		source := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(.02)}

		markPriceMonitorDifferences(platform, &source)

		require.False(t, source.InputDifferent)
		require.True(t, source.OutputDifferent)
		require.True(t, source.Different)
	})
}

func TestQueryPriceMonitorMatrixFiltersModelsSourcesAndPaginates(t *testing.T) {
	snapshot := PriceMonitorSnapshot{
		CheckedAt: 123, Status: "success", PasswordExpireAt: 456,
		SourceHeaders: []PriceMonitorSourceHeader{
			{Key: priceMonitorPlatformKey, Name: "平台配置", Type: priceMonitorPlatformKey},
			{Key: "official", Name: "官方", Type: priceSourceOfficial},
			{Key: "channel", Name: "渠道", Type: priceSourceChannel},
		},
		MatrixItems: []PriceMonitorMatrixItem{
			{Model: "alpha", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: {}, "official": {}, "channel": {}}},
			{Model: "alpha-plus", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: {}, "official": {}, "channel": {}}},
			{Model: "beta", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: {}, "official": {}, "channel": {}}},
		},
	}

	result := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{Model: "alpha", Source: priceSourceOfficial, Page: 2, PageSize: 1})

	require.Equal(t, 2, result.Total)
	require.Equal(t, 2, result.Page)
	require.Equal(t, []PriceMonitorSourceHeader{snapshot.SourceHeaders[0], snapshot.SourceHeaders[1]}, result.SourceHeaders)
	require.Len(t, result.Items, 1)
	require.Equal(t, "alpha-plus", result.Items[0].Model)
	require.NotContains(t, result.Items[0].Prices, "channel")
}

func TestQueryPriceMonitorSnapshotFiltersBeforePagination(t *testing.T) {
	snapshot := PriceMonitorSnapshot{CheckedAt: 123, Status: "partial", PasswordExpireAt: 456, Items: []PriceMonitorItem{
		{Model: "alpha", Field: "model_ratio", Sources: []PriceMonitorSource{{Name: "one", Type: priceSourceChannel, Value: 2.0}}},
		{Model: "alpha-plus", Field: "model_price", Sources: []PriceMonitorSource{{Name: "official", Type: priceSourceOfficial, Value: 3.0}}},
		{Model: "alpha-plus", Field: "completion_ratio", Sources: []PriceMonitorSource{{Name: "official", Type: priceSourceOfficial, Value: 4.0}}},
		{Model: "beta", Field: "model_ratio", Sources: []PriceMonitorSource{{Name: "two", Type: priceSourceChannel, Value: 4.0}}},
	}}

	result := queryPriceMonitorSnapshot(snapshot, priceMonitorQuery{Model: "alpha", Source: priceSourceOfficial, Page: 1, PageSize: 1})
	require.Equal(t, int64(123), result.CheckedAt)
	require.Equal(t, int64(456), result.PasswordExpireAt)
	require.Equal(t, "partial", result.Status)
	require.Equal(t, 2, result.Total)
	require.Equal(t, 1, result.ModelTotal)
	require.Len(t, result.Items, 1)
	require.Equal(t, "alpha-plus", result.Items[0].Model)
	require.Empty(t, result.Items[0].Sources[0].Type)
}

func TestQueryPriceMonitorMatrixKeepsColumnsUsedOutsideCurrentPage(t *testing.T) {
	snapshot := PriceMonitorSnapshot{
		SourceHeaders: []PriceMonitorSourceHeader{
			{Key: priceMonitorPlatformKey, Type: priceMonitorPlatformKey},
			{Key: "official", Type: priceSourceOfficial},
			{Key: "channel-a", Type: priceSourceChannel},
			{Key: "channel-b", Type: priceSourceChannel},
		},
		MatrixItems: []PriceMonitorMatrixItem{
			{Model: "alpha", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: {}, "official": {}, "channel-a": {}}},
			{Model: "beta", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: {}, "official": {}, "channel-b": {}}},
		},
	}

	result := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{Page: 1, PageSize: 1})

	require.Equal(t, snapshot.SourceHeaders, result.SourceHeaders)
}

func TestQueryPriceMonitorMatrixAlwaysKeepsOfficialColumn(t *testing.T) {
	snapshot := PriceMonitorSnapshot{
		SourceHeaders: []PriceMonitorSourceHeader{
			{Key: priceMonitorPlatformKey, Type: priceMonitorPlatformKey},
			{Key: "official", Type: priceSourceOfficial},
			{Key: "channel", Type: priceSourceChannel},
		},
		MatrixItems: []PriceMonitorMatrixItem{{Model: "alpha", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: {}, "official": {}, "channel": {}}}},
	}

	result := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{Source: priceSourceChannel, Page: 1, PageSize: 20})

	require.Equal(t, snapshot.SourceHeaders, result.SourceHeaders)
}

func TestQueryPriceMonitorMatrixSelectsExactChannelColumnsAndReturnsModels(t *testing.T) {
	snapshot := PriceMonitorSnapshot{
		SourceHeaders: []PriceMonitorSourceHeader{
			{Key: priceMonitorPlatformKey, Type: priceMonitorPlatformKey},
			{Key: "official", Type: priceSourceOfficial},
			{Key: "channel-a", Type: priceSourceChannel},
			{Key: "channel-b", Type: priceSourceChannel},
		},
		MatrixItems: []PriceMonitorMatrixItem{
			{Model: "beta", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: {}, "official": {}, "channel-b": {}}},
			{Model: "alpha", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: {}, "official": {}, "channel-a": {}, "channel-b": {}}},
		},
	}

	result := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{
		Model: "alpha", SourceKeys: []string{"channel-b"}, SourceKeysSet: true,
		Comparison: "all", Page: 1, PageSize: 20,
	})

	require.Equal(t, []string{"alpha", "beta"}, result.AvailableModels)
	require.Equal(t, []PriceMonitorSourceHeader{snapshot.SourceHeaders[0], snapshot.SourceHeaders[1], snapshot.SourceHeaders[3]}, result.SourceHeaders)
	require.Equal(t, []string{"channel-b"}, result.AppliedFilters.SourceKeys)
	require.Equal(t, "all", result.AppliedFilters.Comparison)
	require.Len(t, result.Items, 1)
	require.NotContains(t, result.Items[0].Prices, "channel-a")
}

func TestQueryPriceMonitorMatrixSupportsClearingAllChannelColumns(t *testing.T) {
	snapshot := PriceMonitorSnapshot{
		SourceHeaders: []PriceMonitorSourceHeader{
			{Key: priceMonitorPlatformKey, Type: priceMonitorPlatformKey},
			{Key: "official", Type: priceSourceOfficial},
			{Key: "channel", Type: priceSourceChannel},
		},
		MatrixItems: []PriceMonitorMatrixItem{{Model: "alpha", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: {}, "official": {}, "channel": {}}}},
	}

	result := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{SourceKeys: []string{}, SourceKeysSet: true, Page: 1, PageSize: 20})

	require.Equal(t, snapshot.SourceHeaders[:2], result.SourceHeaders)
	require.NotContains(t, result.Items[0].Prices, "channel")
}

func TestPriceMonitorQueryFromContextPreservesExplicitEmptySourceSelection(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/price_monitor/results?source_keys=&comparison=channel_platform&page=0&page_size=201", nil)

	query := priceMonitorQueryFromContext(context)

	require.True(t, query.SourceKeysSet)
	require.Empty(t, query.SourceKeys)
	require.Equal(t, "channel_platform", query.Comparison)
	require.Equal(t, 0, query.Page)
	require.Equal(t, 201, query.PageSize)
}

func TestQueryPriceMonitorMatrixComparisonFilters(t *testing.T) {
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(1), Output: floatPointer(2), Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(.1)}}}
	officialDifferent := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(1), Output: floatPointer(3), Different: true, OutputDifferent: true}
	channelDifferent := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(2), Output: floatPointer(2), Different: true, InputDifferent: true, Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(.2), Different: true}}}
	fixedDifferent := PriceMonitorPriceCell{Mode: priceMonitorModeRequest, Price: floatPointer(4), Different: true, ModeDifferent: true, PriceDifferent: true}
	snapshot := PriceMonitorSnapshot{
		SourceHeaders: []PriceMonitorSourceHeader{
			{Key: priceMonitorPlatformKey, Type: priceMonitorPlatformKey},
			{Key: "official", Type: priceSourceOfficial},
			{Key: "channel", Type: priceSourceChannel},
		},
		MatrixItems: []PriceMonitorMatrixItem{
			{Model: "price-difference", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: platform, "official": officialDifferent, "channel": channelDifferent}},
			{Model: "official-missing", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: platform, "official": {Different: true, UnavailableReason: priceMonitorUnavailableMissing}}},
			{Model: "channel-missing", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: platform, "official": platform, "channel": {Different: true, UnavailableReason: priceMonitorUnavailableMissing}}},
			{Model: "source-failed", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: platform, "official": platform, "channel": {UnavailableReason: priceMonitorUnavailableSourceFailed}}},
			{Model: "billing-difference", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: platform, "official": platform, "channel": fixedDifferent}},
		},
	}

	tests := []struct {
		comparison string
		models     []string
	}{
		{"channel_official", []string{"billing-difference", "channel-missing", "price-difference"}},
		{"channel_platform", []string{"billing-difference", "channel-missing", "price-difference"}},
		{"platform_official", []string{"official-missing", "price-difference"}},
		{"input", []string{"price-difference"}},
		{"output", []string{"price-difference"}},
		{"cache", []string{"price-difference"}},
		{"billing", []string{"billing-difference"}},
		{"official_missing", []string{"official-missing"}},
		{"channel_missing", []string{"channel-missing"}},
		{"source_failed", []string{"source-failed"}},
	}
	for _, test := range tests {
		t.Run(test.comparison, func(t *testing.T) {
			result := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{Comparison: test.comparison, Page: 1, PageSize: 20})
			models := make([]string, 0, len(result.Items))
			for _, item := range result.Items {
				models = append(models, item.Model)
			}
			require.ElementsMatch(t, test.models, models)
		})
	}
}

func TestPriceMonitorPriceCellsEqualUsesNormalizedPrices(t *testing.T) {
	left := PriceMonitorPriceCell{
		Mode: priceMonitorModeExpression,
		Tiers: []PriceMonitorPriceTier{{
			Range: "0-200k", ConditionVariable: "len", ConditionOperator: "<=", ConditionValue: floatPointer(200000),
			Input: 1, Output: 2, Lanes: []PriceMonitorPriceLane{{Key: priceMonitorLaneCacheRead, Price: floatPointer(.1)}},
		}},
	}
	right := left
	right.Different = true
	right.Tiers = append([]PriceMonitorPriceTier(nil), left.Tiers...)
	right.Tiers[0].Lanes = append([]PriceMonitorPriceLane(nil), left.Tiers[0].Lanes...)

	require.True(t, priceMonitorPriceCellsEqual(left, right), "display-only difference flags must not affect source-to-source comparison")

	right.Tiers[0].Lanes[0].Price = floatPointer(.2)
	require.False(t, priceMonitorPriceCellsEqual(left, right))
	require.True(t, priceMonitorPriceCellsEqual(
		PriceMonitorPriceCell{UnavailableReason: priceMonitorUnavailableMissing},
		PriceMonitorPriceCell{UnavailableReason: priceMonitorUnavailableMissing},
	))
	require.False(t, priceMonitorPriceCellsEqual(
		PriceMonitorPriceCell{UnavailableReason: priceMonitorUnavailableMissing},
		PriceMonitorPriceCell{Mode: priceMonitorModeToken},
	))
}

func TestQueryPriceMonitorMatrixTrimsHeadersAndRowPricesForComparison(t *testing.T) {
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(1), Output: floatPointer(2)}
	official := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(1), Output: floatPointer(3), Different: true, OutputDifferent: true}
	channelA := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(2), Output: floatPointer(3), Different: true, InputDifferent: true}
	channelB := PriceMonitorPriceCell{Mode: priceMonitorModeToken, Input: floatPointer(4), Output: floatPointer(3), Different: true, InputDifferent: true}
	snapshot := PriceMonitorSnapshot{
		SourceHeaders: []PriceMonitorSourceHeader{
			{Key: priceMonitorPlatformKey, Type: priceMonitorPlatformKey},
			{Key: "official", Type: priceSourceOfficial},
			{Key: "channel-a", Type: priceSourceChannel},
			{Key: "channel-b", Type: priceSourceChannel},
		},
		MatrixItems: []PriceMonitorMatrixItem{
			{Model: "alpha", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: platform, "official": official, "channel-a": channelA}},
			{Model: "beta", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: platform, "official": official, "channel-b": channelB}},
		},
	}

	result := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{Comparison: "channel_official", Page: 1, PageSize: 1})

	require.Equal(t, []PriceMonitorSourceHeader{snapshot.SourceHeaders[1], snapshot.SourceHeaders[2], snapshot.SourceHeaders[3]}, result.SourceHeaders, "headers must be based on all filtered models, not only the current page")
	require.Len(t, result.Items, 1)
	require.Equal(t, map[string]PriceMonitorPriceCell{"official": official, "channel-a": channelA}, result.Items[0].Prices)
	require.NotContains(t, result.Items[0].Prices, priceMonitorPlatformKey)
	require.NotContains(t, result.Items[0].Prices, "channel-b")
}

func TestQueryPriceMonitorMatrixComparisonHeaderMappings(t *testing.T) {
	platform := PriceMonitorPriceCell{Mode: priceMonitorModeToken}
	snapshot := PriceMonitorSnapshot{
		SourceHeaders: []PriceMonitorSourceHeader{
			{Key: priceMonitorPlatformKey, Type: priceMonitorPlatformKey},
			{Key: "official", Type: priceSourceOfficial},
			{Key: "channel", Type: priceSourceChannel},
		},
		MatrixItems: []PriceMonitorMatrixItem{
			{Model: "official", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: platform, "official": {Different: true, UnavailableReason: priceMonitorUnavailableMissing}}},
			{Model: "channel", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: platform, "official": platform, "channel": {Different: true, UnavailableReason: priceMonitorUnavailableMissing}}},
			{Model: "failed", Prices: map[string]PriceMonitorPriceCell{priceMonitorPlatformKey: platform, "official": platform, "channel": {UnavailableReason: priceMonitorUnavailableSourceFailed}}},
		},
	}

	tests := []struct {
		comparison string
		headers    []PriceMonitorSourceHeader
	}{
		{"platform_official", snapshot.SourceHeaders[:2]},
		{"official_missing", snapshot.SourceHeaders[1:2]},
		{"channel_missing", snapshot.SourceHeaders[2:3]},
		{"source_failed", snapshot.SourceHeaders[2:3]},
	}
	for _, test := range tests {
		t.Run(test.comparison, func(t *testing.T) {
			result := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{Comparison: test.comparison, Page: 1, PageSize: 20})
			require.Equal(t, test.headers, result.SourceHeaders)
			allowed := make(map[string]struct{}, len(test.headers))
			for _, header := range test.headers {
				allowed[header.Key] = struct{}{}
			}
			for _, item := range result.Items {
				for key := range item.Prices {
					require.Contains(t, allowed, key)
				}
			}
		})
	}
}

func TestQueryPriceMonitorSnapshotPaginationBoundaries(t *testing.T) {
	snapshot := PriceMonitorSnapshot{Items: []PriceMonitorItem{
		{Model: "alpha", Field: "model_ratio"},
		{Model: "beta", Field: "model_ratio"},
	}}

	defaulted := queryPriceMonitorSnapshot(snapshot, priceMonitorQuery{Page: 0, PageSize: 0})
	require.Equal(t, 1, defaulted.Page)
	require.Equal(t, 20, defaulted.PageSize)
	require.Equal(t, 2, defaulted.ModelTotal)

	beyondEnd := queryPriceMonitorSnapshot(snapshot, priceMonitorQuery{Page: 3, PageSize: 1})
	require.Empty(t, beyondEnd.Items)
	require.Equal(t, 2, beyondEnd.Total)
	require.Equal(t, 2, beyondEnd.ModelTotal)

	clamped := queryPriceMonitorSnapshot(snapshot, priceMonitorQuery{Page: 1, PageSize: 201})
	require.Equal(t, 200, clamped.PageSize)
}

func legacyPriceMonitorPageRedesignContract(t *testing.T) {
	require.Contains(t, priceMonitorHTML, `id="price-matrix-table"`)
	require.Contains(t, priceMonitorHTML, `class="table-scroll"`)
	require.Contains(t, priceMonitorHTML, `id="page-size-picker"`)
	require.NotContains(t, priceMonitorHTML, `id="reauthenticate"`)
	require.Contains(t, priceMonitorHTML, `prefers-color-scheme:dark`)
	require.Contains(t, priceMonitorHTML, `prefers-reduced-motion:reduce`)
	require.Contains(t, priceMonitorHTML, `平台配置`)
	require.Contains(t, priceMonitorHTML, `输入`)
	require.Contains(t, priceMonitorHTML, `输出`)
	require.Contains(t, priceMonitorHTML, `Token 用量价格统一按百万计`)
	require.Contains(t, priceMonitorHTML, `price-different`)
	require.NotContains(t, priceMonitorHTML, `relativeDifference`)
	require.NotContains(t, priceMonitorHTML, `difference-rate`)
	require.Contains(t, priceMonitorHTML, `maximumFractionDigits:digits`)
	require.Contains(t, priceMonitorHTML, `.000001?10:6`)
	require.Contains(t, priceMonitorHTML, `动态规则计费`)
	require.Contains(t, priceMonitorHTML, `该来源未提供此模型价格`)
	require.Contains(t, priceMonitorHTML, `tier-prices`)
	require.NotContains(t, priceMonitorHTML, `max-height:calc(100vh`)
	require.Contains(t, priceMonitorHTML, `缓存读取`)
	require.Contains(t, priceMonitorHTML, `缓存写入`)
	require.Contains(t, priceMonitorHTML, `图片输入`)
	require.Contains(t, priceMonitorHTML, `音频输入`)
	require.Contains(t, priceMonitorHTML, `音频输出`)
	require.NotContains(t, priceMonitorHTML, `每百万 tokens`)
	require.NotContains(t, priceMonitorHTML, `localStorage`)
	require.NotContains(t, priceMonitorHTML, `sessionStorage`)
	require.NotContains(t, priceMonitorHTML, `language-toggle`)
	require.False(t, strings.Contains(priceMonitorHTML, "Model price"), "the share page is Chinese-only")
}

func TestPriceMonitorPageAvailabilityContract(t *testing.T) {
	require.Contains(t, priceMonitorHTML, `id="price-matrix-table"`)
	require.Contains(t, priceMonitorHTML, `class="table-scroll"`)
	require.Contains(t, priceMonitorHTML, `id="page-size-picker"`)
	require.NotContains(t, priceMonitorHTML, `id="reauthenticate"`)
	require.Contains(t, priceMonitorHTML, `prefers-color-scheme:dark`)
	require.Contains(t, priceMonitorHTML, `prefers-reduced-motion:reduce`)
	require.Contains(t, priceMonitorHTML, `Token 用量价格统一按百万计算`)
	require.Contains(t, priceMonitorHTML, `官方价格预设未收录此模型`)
	require.Contains(t, priceMonitorHTML, `该渠道价格接口未提供此模型`)
	require.Contains(t, priceMonitorHTML, `仅提供输入价格`)
	require.Contains(t, priceMonitorHTML, `价格来源检查失败，请等待下次巡检`)
	require.Contains(t, priceMonitorHTML, `该渠道未启用此模型，不参与对比`)
	require.Contains(t, priceMonitorHTML, `来源返回占位价格，未参与对比`)
	require.NotContains(t, priceMonitorHTML, `relativeDifference`)
	require.NotContains(t, priceMonitorHTML, `difference-rate`)
	require.Contains(t, priceMonitorHTML, `.000001?10:6`)
	require.NotContains(t, priceMonitorHTML, `max-height:calc(100vh`)
	require.NotContains(t, priceMonitorHTML, `localStorage`)
	require.NotContains(t, priceMonitorHTML, `sessionStorage`)
	require.False(t, strings.Contains(priceMonitorHTML, "Model price"), "the share page is Chinese-only")
}

func TestPriceMonitorSourceHeadersIncludeAPIURL(t *testing.T) {
	headers := buildPriceMonitorSourceHeaders([]pricingSource{
		{name: "channel-a(1)", apiURL: "https://api.example.com/v1"},
		{name: "channel-a(2)", apiURL: "https://api.example.com/v1"},
	}, map[string]string{"channel-a(1)": priceSourceChannel, "channel-a(2)": priceSourceChannel})

	require.Equal(t, "https://api.example.com/v1", headers[1].APIURL)
	require.Equal(t, headers[1].APIURL, headers[2].APIURL)
}

func TestPriceMonitorDisplayURLRemovesCredentialsAndQuery(t *testing.T) {
	require.Equal(t, "https://api.example.com:8443/v1", priceMonitorDisplayURL("https://user:secret@api.example.com:8443/v1?api_key=hidden#fragment"))
	require.Equal(t, "http://127.0.0.1:3000/api", priceMonitorDisplayURL(" http://127.0.0.1:3000/api/?token=hidden "))
	require.Empty(t, priceMonitorDisplayURL("javascript:alert(1)"))
	require.Empty(t, priceMonitorDisplayURL("not-a-url"))
}

func TestQueryPriceMonitorMatrixFiltersChannelByAPIURLAndKeepsOfficial(t *testing.T) {
	snapshot := PriceMonitorSnapshot{
		SourceHeaders: []PriceMonitorSourceHeader{
			{Key: priceMonitorPlatformKey, Name: "platform", Type: priceMonitorPlatformKey},
			{Key: "official", Name: "official", Type: priceSourceOfficial},
			{Key: "channel-a", Name: "channel-a", Type: priceSourceChannel, APIURL: "https://a.example/v1"},
			{Key: "channel-b", Name: "channel-b", Type: priceSourceChannel, APIURL: "https://b.example/v1"},
		},
		MatrixItems: []PriceMonitorMatrixItem{{Model: "alpha", Prices: map[string]PriceMonitorPriceCell{
			priceMonitorPlatformKey: {}, "official": {}, "channel-a": {}, "channel-b": {},
		}}},
	}

	result := queryPriceMonitorMatrix(snapshot, priceMonitorQuery{Source: "a.example", Page: 1, PageSize: 20})

	require.Equal(t, []PriceMonitorSourceHeader{snapshot.SourceHeaders[0], snapshot.SourceHeaders[1], snapshot.SourceHeaders[2]}, result.SourceHeaders)
	require.Equal(t, snapshot.SourceHeaders, result.AvailableSourceHeaders)
	require.NotContains(t, result.Items[0].Prices, "channel-b")
}

func TestPriceMonitorPageSourceNavigationContract(t *testing.T) {
	require.Contains(t, priceMonitorHTML, `id="source-picker"`)
	require.Contains(t, priceMonitorHTML, `id="scroll-left"`)
	require.Contains(t, priceMonitorHTML, `id="scroll-right"`)
	require.Contains(t, priceMonitorHTML, `available_source_headers`)
	require.Contains(t, priceMonitorHTML, `status-pill`)
}

func TestPriceMonitorPageShowsChannelAPIURLBelowName(t *testing.T) {
	require.Contains(t, priceMonitorHTML, `const title=header.name,subtitle=header.type==='platform'?'平台基准':header.type==='official'?'必须对比':(header.api_url||'')`)
	require.NotContains(t, priceMonitorHTML, `const title=header.type==='channel'?(header.api_url||header.name):header.name`)
}

func TestPriceMonitorPageFilterAndStickyOfficialContract(t *testing.T) {
	require.Contains(t, priceMonitorHTML, `th.fixed-source-0,td.fixed-source-0`)
	require.Contains(t, priceMonitorHTML, `th.fixed-source-1,td.fixed-source-1`)
	require.Contains(t, priceMonitorHTML, `fixedClasses[header.key]=header.type+' fixed-source-'+fixedIndex`)
	require.Contains(t, priceMonitorHTML, `left:230px`)
	require.Contains(t, priceMonitorHTML, `left:450px`)
	require.Contains(t, priceMonitorHTML, `id="model-suggestions"`)
	require.Contains(t, priceMonitorHTML, `id="source-options"`)
	require.Contains(t, priceMonitorHTML, `data-comparison="channel_official"`)
	require.Contains(t, priceMonitorHTML, `id="comparison-more"`)
	require.Contains(t, priceMonitorHTML, `source_keys:`)
}

func TestPriceMonitorPageUsesCompactPasswordPrompt(t *testing.T) {
	require.Contains(t, priceMonitorHTML, `<section class="auth-card"><form id="auth-form"><label for="password">请输入访问口令</label>`)
	require.Contains(t, priceMonitorHTML, `id="auth-error"`)
	require.NotContains(t, priceMonitorHTML, `<div class="mark">`)
	require.NotContains(t, priceMonitorHTML, `输入管理员提供的周期访问密码`)
	require.NotContains(t, priceMonitorHTML, `密码仅保存在当前页面内存中`)
}

func TestPriceMonitorPageUsesAnchoredModelSuggestions(t *testing.T) {
	require.Contains(t, priceMonitorHTML, `.model-picker{position:relative}`)
	require.Contains(t, priceMonitorHTML, `.model-suggestions{position:absolute`)
	require.Contains(t, priceMonitorHTML, `max-height:260px`)
	require.Contains(t, priceMonitorHTML, `overflow-y:auto`)
	require.Contains(t, priceMonitorHTML, `id="model-suggestions"`)
	require.Contains(t, priceMonitorHTML, `availableModels=data.available_models||[]`)
	require.Contains(t, priceMonitorHTML, `model-suggestion-check`)
	require.Contains(t, priceMonitorHTML, `id="model-empty"`)
	require.NotContains(t, priceMonitorHTML, `<datalist`)
}

func TestPriceMonitorPageUsesDraftedSearchableSourcePicker(t *testing.T) {
	require.Contains(t, priceMonitorHTML, `id="source-search"`)
	require.Contains(t, priceMonitorHTML, `id="source-apply"`)
	require.Contains(t, priceMonitorHTML, `id="source-selected-count"`)
	require.Contains(t, priceMonitorHTML, `draftSourceKeys`)
	require.Contains(t, priceMonitorHTML, `function applySourceSelection()`)
	require.Contains(t, priceMonitorHTML, `sourceSearch`)
}

func TestPriceMonitorPageUsesAnchoredComparisonMenu(t *testing.T) {
	require.Contains(t, priceMonitorHTML, `id="comparison-picker"`)
	require.Contains(t, priceMonitorHTML, `id="comparison-menu"`)
	require.Contains(t, priceMonitorHTML, `comparison-option-check`)
	require.Contains(t, priceMonitorHTML, `function renderComparisonMenu()`)
	require.NotContains(t, priceMonitorHTML, `<select id="comparison-more"`)
}

func TestPriceMonitorPageUsesConsistentDropdownsWithoutReauthentication(t *testing.T) {
	require.Contains(t, priceMonitorHTML, `id="page-size-picker"`)
	require.Contains(t, priceMonitorHTML, `id="page-size-menu"`)
	require.Contains(t, priceMonitorHTML, `data-page-size="20"`)
	require.Contains(t, priceMonitorHTML, `border-right:2px solid currentColor`)
	require.Contains(t, priceMonitorHTML, `details[open]>summary:after`)
	require.NotContains(t, priceMonitorHTML, `<select id="page-size"`)
	require.NotContains(t, priceMonitorHTML, `id="reauthenticate"`)
}

func TestPriceMonitorPageUsesMobileInfiniteLoading(t *testing.T) {
	require.Contains(t, priceMonitorHTML, `id="load-more"`)
	require.Contains(t, priceMonitorHTML, `id="load-more-retry"`)
	require.Contains(t, priceMonitorHTML, `window.matchMedia('(max-width:760px)')`)
	require.Contains(t, priceMonitorHTML, `new IntersectionObserver`)
	require.Contains(t, priceMonitorHTML, `if(loadingMore)return`)
	require.Contains(t, priceMonitorHTML, `requestVersion`)
	require.Contains(t, priceMonitorHTML, `data-result-model`)
	require.Contains(t, priceMonitorHTML, `load(true)`)
	require.Contains(t, priceMonitorHTML, `已加载 '+loaded+' 个，共 '+total+' 个模型`)
	require.Contains(t, priceMonitorHTML, `.pager{display:none}`)
}

func TestPriceMonitorPagePreservesRenderedTableWhileFiltering(t *testing.T) {
	require.Contains(t, priceMonitorHTML, `comparison='all',hasRendered=false,availableModels=[]`)
	require.Contains(t, priceMonitorHTML, `const preserveTable=hasRendered`)
	require.Contains(t, priceMonitorHTML, `$('table-scroll').classList.toggle('is-loading',preserveTable)`)
	require.Contains(t, priceMonitorHTML, `hasRendered=true`)
	require.NotContains(t, priceMonitorHTML, `$('state').classList.remove('hidden');$('price-matrix-table').classList.add('hidden');try`)
}

func TestPriceMonitorPageOmitsEnabledModelSubtitle(t *testing.T) {
	require.NotContains(t, priceMonitorHTML, "仅对比已启用模型")
	require.NotContains(t, priceMonitorHTML, "渠道只对比自身已启用的模型")
}

func TestBuildPriceMonitorShareSummary(t *testing.T) {
	previousAddress := system_setting.ServerAddress
	t.Cleanup(func() { system_setting.ServerAddress = previousAddress })
	snapshot := PriceMonitorSnapshot{
		CheckedAt: 123, Status: "partial", SourceTotal: 8, SourceOK: 7, SourceError: 1,
		ModelCount: 22, ItemCount: 28, AccessPassword: "Abc234XY", PasswordExpireAt: 456,
	}

	system_setting.ServerAddress = "https://example.com/"
	summary := buildPriceMonitorShareSummary(snapshot)
	require.Equal(t, int64(123), summary.CheckedAt)
	require.Equal(t, "partial", summary.Status)
	require.Equal(t, 8, summary.SourceTotal)
	require.Equal(t, 7, summary.SourceOK)
	require.Equal(t, 1, summary.SourceError)
	require.Equal(t, 22, summary.ModelCount)
	require.Equal(t, 28, summary.ItemCount)
	require.Equal(t, "https://example.com/price_monitor/view", summary.ShareURL)
	require.Equal(t, "Abc234XY", summary.AccessPassword)
	require.Equal(t, int64(456), summary.PasswordExpireAt)

	encoded, err := common.Marshal(summary)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `"items"`)
	require.NotContains(t, string(encoded), `"page"`)
	require.NotContains(t, string(encoded), `"platform"`)
}

func TestBuildPriceMonitorShareSummaryUsesRelativeURLWhenServerAddressEmpty(t *testing.T) {
	previousAddress := system_setting.ServerAddress
	t.Cleanup(func() { system_setting.ServerAddress = previousAddress })
	system_setting.ServerAddress = ""

	summary := buildPriceMonitorShareSummary(PriceMonitorSnapshot{})

	require.Equal(t, "/price_monitor/view", summary.ShareURL)
	require.Zero(t, summary.CheckedAt)
	require.Empty(t, summary.AccessPassword)
}

func TestPriceMonitorSnapshotStoreRoundTripAndCorruptFileFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "snapshot.json")
	store := newPriceMonitorSnapshotStore(path)
	want := PriceMonitorSnapshot{CheckedAt: 123, Status: "success", AccessPassword: "secret", Items: []PriceMonitorItem{{Model: "m", Field: "model_ratio"}}, SourceHeaders: []PriceMonitorSourceHeader{}, MatrixItems: []PriceMonitorMatrixItem{}}
	require.NoError(t, store.Save(want))
	require.Equal(t, want, store.Get())

	reloaded := newPriceMonitorSnapshotStore(path)
	require.NoError(t, reloaded.Load())
	require.Equal(t, want, reloaded.Get())

	require.NoError(t, writePriceMonitorFile(path, []byte("not-json")))
	require.Error(t, reloaded.Load())
	require.Equal(t, want, reloaded.Get())
}

func TestPriceMonitorSnapshotStoreConcurrentReaders(t *testing.T) {
	store := newPriceMonitorSnapshotStore(filepath.Join(t.TempDir(), "snapshot.json"))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				require.NoError(t, store.Save(PriceMonitorSnapshot{CheckedAt: int64(j)}))
				_ = store.Get()
			}
		}()
	}
	wg.Wait()
}

func TestPriceMonitorPasswordValidationBoundaries(t *testing.T) {
	now := time.Unix(100, 0)
	snapshot := PriceMonitorSnapshot{AccessPassword: "abc123", PasswordExpireAt: 101}
	require.True(t, validPriceMonitorPassword(snapshot, "abc123", now))
	require.False(t, validPriceMonitorPassword(snapshot, "wrong", now))
	require.False(t, validPriceMonitorPassword(snapshot, "abc123", time.Unix(101, 0)))
}

func TestPriceMonitorCheckDueUsesLastAttemptAndIntervalBoundary(t *testing.T) {
	now := time.Unix(1000, 0)
	require.True(t, priceMonitorCheckDue(true, 5, 0, 0, now))
	require.False(t, priceMonitorCheckDue(false, 5, 0, 0, now))
	require.False(t, priceMonitorCheckDue(true, 5, 800, 900, now))
	require.True(t, priceMonitorCheckDue(true, 5, 700, 0, now))
	require.True(t, priceMonitorCheckDue(true, 1, 700, 0, now), "intervals below five minutes are clamped")
}

func TestPriceMonitorSnapshotNeedsRefreshForOlderMatrixSchema(t *testing.T) {
	require.True(t, priceMonitorSnapshotNeedsRefresh(PriceMonitorSnapshot{MatrixVersion: 1, SourceHeaders: []PriceMonitorSourceHeader{{Key: priceMonitorPlatformKey}}}))
	require.True(t, priceMonitorSnapshotNeedsRefresh(PriceMonitorSnapshot{MatrixVersion: priceMonitorMatrixVersion}))
	require.False(t, priceMonitorSnapshotNeedsRefresh(PriceMonitorSnapshot{MatrixVersion: priceMonitorMatrixVersion, SourceHeaders: []PriceMonitorSourceHeader{{Key: priceMonitorPlatformKey}}}))
}

func TestFetchUpstreamPricingDataParsesPricingFixture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"success":true,"data":[{"model_name":"fixture-model","quota_type":0,"model_ratio":2,"completion_ratio":3}]}`))
	}))
	defer server.Close()

	_, testResults := fetchUpstreamPricingData(context.Background(), []dto.UpstreamDTO{{Name: "fixture", BaseURL: server.URL, Endpoint: "/pricing"}}, 2)
	require.Equal(t, []dto.TestResult{{Name: "fixture", Status: "success"}}, testResults)
}
