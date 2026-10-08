package controller

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/stretchr/testify/require"
)

// 价格来源返回 200、结构也能解析，但一个价格都没有时，必须判为失败。
// 否则它会被计成"成功来源"，矩阵里该渠道每个模型都被标成"未提供"并计入价格差异，
// 把接口故障伪装成价格不一致。

func fetchPricingFixture(t *testing.T, body string) dto.TestResult {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	_, testResults, _ := fetchUpstreamPricingSnapshotData(context.Background(), []dto.UpstreamDTO{{Name: "fixture", BaseURL: server.URL, Endpoint: "/pricing"}}, 2)
	require.Len(t, testResults, 1)
	return testResults[0]
}

func TestFetchUpstreamPricingRejectsEmptyPayload(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "pricing list is empty", body: `{"success":true,"data":[]}`},
		{name: "pricing entries have no model name", body: `{"success":true,"data":[{"model_name":"","model_ratio":2}]}`},
		{name: "ratio config maps are empty", body: `{"success":true,"data":{"model_ratio":{},"completion_ratio":{}}}`},
		{name: "only completion ratio without base price", body: `{"success":true,"data":{"completion_ratio":{"m":3}}}`},
		{name: "only cache ratio without base price", body: `{"success":true,"data":{"cache_ratio":{"m":0.5}}}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result := fetchPricingFixture(t, testCase.body)
			require.Equal(t, "error", result.Status)
			require.Equal(t, emptyPricingPayloadError, result.Error)
		})
	}
}

func TestFetchUpstreamPricingAcceptsMinimalPayload(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "model ratio only", body: `{"success":true,"data":{"model_ratio":{"m":2}}}`},
		{name: "model price only", body: `{"success":true,"data":{"model_price":{"m":0.01}}}`},
		{name: "pricing list per request price", body: `{"success":true,"data":[{"model_name":"m","quota_type":1,"model_price":0.02}]}`},
		{name: "pricing list zero ratio", body: `{"success":true,"data":[{"model_name":"m","quota_type":0,"model_ratio":0}]}`},
		// 阶梯表达式本身就是价格，不能因为没有固定倍率就判成空来源。
		{name: "tiered expression only", body: `{"success":true,"data":{"billing_expr":{"m":"p*1"}}}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result := fetchPricingFixture(t, testCase.body)
			require.Equal(t, "success", result.Status, "error: %s", result.Error)
		})
	}
}

func TestConvertOpenRouterRejectsPayloadWithoutPrices(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "no models", body: `{"data":[]}`},
		{name: "unparseable prices", body: `{"data":[{"id":"m","pricing":{"prompt":"x","completion":"y"}}]}`},
		{name: "sentinel negative prices", body: `{"data":[{"id":"m","pricing":{"prompt":"-1","completion":"-1"}}]}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := convertOpenRouterToRatioData(strings.NewReader(testCase.body))
			require.Error(t, err)
			require.Equal(t, emptyPricingPayloadError, err.Error())
		})
	}
}

func TestConvertOpenRouterAcceptsFreeModel(t *testing.T) {
	converted, err := convertOpenRouterToRatioData(strings.NewReader(`{"data":[{"id":"m","pricing":{"prompt":"0","completion":"0"}}]}`))
	require.NoError(t, err)
	require.Equal(t, 0.0, valueMap(converted["model_ratio"])["m"])
}

func TestPricingPayloadHasPrices(t *testing.T) {
	require.False(t, pricingPayloadHasPrices(nil))
	require.False(t, pricingPayloadHasPrices(map[string]any{}))
	require.False(t, pricingPayloadHasPrices(map[string]any{"model_ratio": map[string]any{}}))
	require.False(t, pricingPayloadHasPrices(map[string]any{"completion_ratio": map[string]any{"m": 1.0}}))
	require.True(t, pricingPayloadHasPrices(map[string]any{"model_ratio": map[string]any{"m": 1.0}}))
	require.True(t, pricingPayloadHasPrices(map[string]any{"model_price": map[string]any{"m": 1.0}}))
	require.True(t, pricingPayloadHasPrices(map[string]any{"billing_expr": map[string]any{"m": "p*1"}}))

	// Keys alone are not prices: every value must be checked for a usable number or expression.
	unusable := map[string]map[string]any{
		"null ratio":          {"model_ratio": map[string]any{"m": nil}},
		"string ratio":        {"model_ratio": map[string]any{"m": "1.5"}},
		"negative ratio":      {"model_ratio": map[string]any{"m": -0.5}},
		"NaN price":           {"model_price": map[string]any{"m": math.NaN()}},
		"infinite price":      {"model_price": map[string]any{"m": math.Inf(1)}},
		"blank expression":    {"billing_expr": map[string]any{"m": "  "}},
		"non-string expr":     {"billing_expr": map[string]any{"m": 1.0}},
		"all fields unusable": {"model_ratio": map[string]any{"m": nil}, "model_price": map[string]any{"n": "abc"}, "billing_expr": map[string]any{"o": ""}},
	}
	for name, data := range unusable {
		require.False(t, pricingPayloadHasPrices(data), name)
	}
	usable := map[string]map[string]any{
		"zero ratio is a free model": {"model_ratio": map[string]any{"m": 0.0}},
		"one usable among unusable":  {"model_ratio": map[string]any{"a": nil, "b": 2.0}},
		"json.Number price":          {"model_price": map[string]any{"m": json.Number("0.01")}},
		"int ratio":                  {"model_ratio": map[string]float64{"m": 3}},
	}
	for name, data := range usable {
		require.True(t, pricingPayloadHasPrices(data), name)
	}
}

// L7: a ratio_config source that sends numbers as strings is converted at the source, so the
// usable-price gate, the price cell and the sync differences all see the same numbers. Strings
// that are not finite numbers stay unusable everywhere.
func TestRatioConfigNumericStringsAreComparedLikeNumbers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"success":true,"data":{` +
			`"model_ratio":{"m":" 1.5 ","bad":"abc","nan":"NaN","inf":"+Inf","neg":"-1"},` +
			`"completion_ratio":{"m":"2"},"model_price":{"n":"0.04"},` +
			`"billing_mode":{"t":"tiered_expr"},"billing_expr":{"t":"p*2"}}}`))
	}))
	defer server.Close()
	results, sources := fetchUpstreamPricingSources(context.Background(), []dto.UpstreamDTO{{Name: "fixture", BaseURL: server.URL, Endpoint: "/api/ratio_config"}}, 2)
	require.Len(t, results, 1)
	require.Equal(t, "success", results[0].Status, results[0].Error)
	require.Len(t, sources, 1)
	data := sources[0].data

	require.Equal(t, map[string]struct{}{"m": {}, "n": {}, "t": {}}, pricingPayloadModels(data),
		"converted strings are prices; non-numeric, NaN, Inf and negative ones are not")
	cell, ok := priceMonitorCell(data, "m")
	require.True(t, ok)
	require.NotNil(t, cell.Input)
	require.NotNil(t, cell.Output)
	require.InDelta(t, 3.0, *cell.Input, 1e-9)
	require.InDelta(t, 6.0, *cell.Output, 1e-9)
	cell, ok = priceMonitorCell(data, "n")
	require.True(t, ok)
	require.NotNil(t, cell.Price)
	require.InDelta(t, 0.04, *cell.Price, 1e-9)
	for _, unusable := range []string{"bad", "nan", "inf"} {
		_, ok = priceMonitorCell(data, unusable)
		require.False(t, ok, unusable)
	}
	require.Equal(t, "tiered_expr", valueMap(data[billing_setting.BillingModeField])["t"], "non-numeric fields are untouched")
	require.Equal(t, "p*2", valueMap(data[billing_setting.BillingExprField])["t"])

	differences := buildDifferences(map[string]any{"model_ratio": map[string]any{"m": 1.5}}, sources)
	require.NotContains(t, differences["m"], "model_ratio", "an upstream \"1.5\" equals a local 1.5")

	allStrings := fetchPricingFixture(t, `{"success":true,"data":{"model_ratio":{"m":"abc"}}}`)
	require.Equal(t, emptyPricingPayloadError, allStrings.Error, "a source with no numeric price still fails")
}
