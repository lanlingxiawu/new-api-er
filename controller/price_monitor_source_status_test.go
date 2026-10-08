package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCountPricingPayloadModels(t *testing.T) {
	require.Equal(t, 0, countPricingPayloadModels(nil))
	require.Equal(t, 0, countPricingPayloadModels(map[string]any{"completion_ratio": map[string]any{"a": 1.0}}))
	require.Equal(t, 1, countPricingPayloadModels(map[string]any{"model_ratio": map[string]any{"a": 1.0}}))
	require.Equal(t, 2, countPricingPayloadModels(map[string]any{
		"model_ratio": map[string]any{"a": 1.0},
		"model_price": map[string]any{"b": 2.0},
	}))
	require.Equal(t, 1, countPricingPayloadModels(map[string]any{
		"model_ratio": map[string]any{"a": 1.0},
		"model_price": map[string]any{"a": 2.0},
	}), "同一个模型出现在多个字段里只算一次")
	require.Equal(t, 1, countPricingPayloadModels(map[string]any{"billing_expr": map[string]any{"a": "p*1"}}))
}

func TestCountPricingPayloadMatchedModels(t *testing.T) {
	data := map[string]any{"model_ratio": map[string]any{"a": 1.0, "b": 2.0}}
	require.Equal(t, 2, countPricingPayloadMatchedModels(data, map[string]struct{}{"a": {}, "b": {}, "c": {}}))
	require.Equal(t, 1, countPricingPayloadMatchedModels(data, map[string]struct{}{"b": {}}))
	require.Equal(t, 0, countPricingPayloadMatchedModels(data, map[string]struct{}{"z": {}}))
	require.Equal(t, 0, countPricingPayloadMatchedModels(data, nil))
}

func TestPriceMonitorComparableModels(t *testing.T) {
	marketplace := map[string]struct{}{"a": {}, "b": {}}

	t.Run("reference sources compare the whole marketplace", func(t *testing.T) {
		require.Equal(t, marketplace, priceMonitorComparableModels(priceSourceOfficial, marketplace, nil))
	})

	t.Run("channel sources are limited to their enabled models", func(t *testing.T) {
		got := priceMonitorComparableModels(priceSourceChannel, marketplace, map[string]struct{}{"b": {}, "z": {}})
		require.Equal(t, map[string]struct{}{"b": {}}, got)
	})

	t.Run("channel without models has nothing to compare", func(t *testing.T) {
		require.Empty(t, priceMonitorComparableModels(priceSourceChannel, marketplace, map[string]struct{}{}))
	})
}

func TestResolvePriceMonitorSourceStatus(t *testing.T) {
	marketplace := map[string]struct{}{"a": {}, "b": {}}
	priced := map[string]any{"model_ratio": map[string]any{"a": 1.0}}
	foreign := map[string]any{"model_ratio": map[string]any{"x": 1.0}}

	cases := []struct {
		name          string
		source        pricingSource
		sourceType    string
		wantStatus    string
		wantFailure   string
		wantFetched   int
		wantMatched   int
		wantComparing bool
	}{
		{
			name:        "fetch failure",
			source:      pricingSource{name: "s", failed: true, failureReason: priceMonitorFailureFetch},
			sourceType:  priceSourceChannel,
			wantStatus:  priceMonitorSourceStatusFailed,
			wantFailure: priceMonitorFailureFetch,
		},
		{
			name:        "empty payload failure keeps its own reason",
			source:      pricingSource{name: "s", failed: true, failureReason: priceMonitorFailureEmpty},
			sourceType:  priceSourceChannel,
			wantStatus:  priceMonitorSourceStatusFailed,
			wantFailure: priceMonitorFailureEmpty,
		},
		{
			name:       "channel without enabled models",
			source:     pricingSource{name: "s", data: priced, applicableModels: map[string]struct{}{}},
			sourceType: priceSourceChannel,
			wantStatus: priceMonitorSourceStatusNoModels,
		},
		{
			name:        "channel whose model names do not overlap",
			source:      pricingSource{name: "s", data: foreign, applicableModels: map[string]struct{}{"a": {}}},
			sourceType:  priceSourceChannel,
			wantStatus:  priceMonitorSourceStatusNoOverlap,
			wantFetched: 1,
		},
		{
			name:        "reference source whose model names do not overlap",
			source:      pricingSource{name: "s", data: foreign},
			sourceType:  priceSourceOfficial,
			wantStatus:  priceMonitorSourceStatusNoOverlap,
			wantFetched: 1,
		},
		{
			name:          "channel with overlap",
			source:        pricingSource{name: "s", data: priced, applicableModels: map[string]struct{}{"a": {}}},
			sourceType:    priceSourceChannel,
			wantStatus:    priceMonitorSourceStatusOK,
			wantFetched:   1,
			wantMatched:   1,
			wantComparing: true,
		},
		{
			name:          "reference source with overlap",
			source:        pricingSource{name: "s", data: priced},
			sourceType:    priceSourceOfficial,
			wantStatus:    priceMonitorSourceStatusOK,
			wantFetched:   1,
			wantMatched:   1,
			wantComparing: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := resolvePriceMonitorSourceStatus(testCase.source, testCase.sourceType, marketplace)
			require.Equal(t, testCase.wantStatus, got.status)
			require.Equal(t, testCase.wantFailure, got.failureReason)
			require.Equal(t, testCase.wantFetched, got.fetchedModels)
			require.Equal(t, testCase.wantMatched, got.matchedModels)
			require.Equal(t, testCase.wantComparing, got.status == priceMonitorSourceStatusOK)
		})
	}
}

func TestBuildPriceMonitorMatrixSkipsSourcesThatCannotCompare(t *testing.T) {
	localData := map[string]any{"model_ratio": map[string]any{"a": 2.0}, "completion_ratio": map[string]any{"a": 1.0}}
	marketplace := map[string]struct{}{"a": {}}
	sources := []pricingSource{
		{name: "ok", status: priceMonitorSourceStatusOK, data: map[string]any{"model_ratio": map[string]any{"a": 5.0}, "completion_ratio": map[string]any{"a": 1.0}}, applicableModels: map[string]struct{}{"a": {}}},
		{name: "failed", status: priceMonitorSourceStatusFailed, failed: true, applicableModels: map[string]struct{}{"a": {}}},
		{name: "no-overlap", status: priceMonitorSourceStatusNoOverlap, data: map[string]any{"model_ratio": map[string]any{"z": 5.0}}, applicableModels: map[string]struct{}{"a": {}}},
		{name: "no-models", status: priceMonitorSourceStatusNoModels, data: map[string]any{"model_ratio": map[string]any{"a": 5.0}}, applicableModels: map[string]struct{}{}},
	}
	sourceTypes := map[string]string{"ok": priceSourceChannel, "failed": priceSourceChannel, "no-overlap": priceSourceChannel, "no-models": priceSourceChannel}

	headers, items := buildPriceMonitorMatrix(localData, sources, marketplace, sourceTypes)

	require.Len(t, items, 1)
	prices := items[0].Prices
	require.Contains(t, prices, "ok")
	require.Equal(t, priceMonitorUnavailableSourceFailed, prices["failed"].UnavailableReason)
	require.NotContains(t, prices, "no-overlap", "无同名交集的来源不产生差异单元格")
	require.NotContains(t, prices, "no-models", "未配置模型的渠道不产生差异单元格")

	// 表头保留所有来源，前端据此说明它们为什么没参与对比。
	keys := make([]string, 0, len(headers))
	for _, header := range headers {
		keys = append(keys, header.Key)
	}
	require.Equal(t, []string{priceMonitorPlatformKey, "failed", "no-models", "no-overlap", "ok"}, keys)
}

func TestBuildPriceMonitorSourceHeadersCarryStatusAndCoverage(t *testing.T) {
	sources := []pricingSource{{
		name:          "渠道(7)",
		status:        priceMonitorSourceStatusNoOverlap,
		fetchedModels: 128,
		matchedModels: 0,
		endpoint:      "/api/ratio_config",
		apiURL:        "https://example.com",
	}}
	headers := buildPriceMonitorSourceHeaders(sources, map[string]string{"渠道(7)": priceSourceChannel})

	require.Len(t, headers, 2)
	header := headers[1]
	require.Equal(t, priceMonitorSourceStatusNoOverlap, header.Status)
	require.Equal(t, 128, header.FetchedModels)
	require.Equal(t, 0, header.MatchedModels)
	require.Equal(t, "/api/ratio_config", header.Endpoint)
	require.Equal(t, priceMonitorSourceStatusOK, headers[0].Status, "平台列始终是可对比状态")
}

func TestResolvePriceMonitorSourceStatusSeparatesUnconfiguredChannelFromNameMismatch(t *testing.T) {
	marketplace := map[string]struct{}{"a": {}}
	priced := map[string]any{"model_ratio": map[string]any{"b": 1.0}}

	// 渠道配了模型，但都不在模型广场里：这是名字对不上，不是渠道没配模型。
	got := resolvePriceMonitorSourceStatus(
		pricingSource{name: "s", data: priced, applicableModels: map[string]struct{}{"b": {}}},
		priceSourceChannel,
		marketplace,
	)
	require.Equal(t, priceMonitorSourceStatusNoOverlap, got.status)
	require.Equal(t, 1, got.fetchedModels)
	require.Equal(t, 0, got.matchedModels)
}
