package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
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
}
