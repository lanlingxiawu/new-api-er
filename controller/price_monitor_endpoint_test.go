package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestPriceMonitorEndpointCandidates(t *testing.T) {
	cases := []struct {
		name        string
		channelType int
		pinned      string
		remembered  string
		want        []string
	}{
		{
			name: "no configuration probes both defaults",
			want: []string{"/api/pricing", "/api/ratio_config"},
		},
		{
			name:       "remembered endpoint goes first and is not repeated",
			remembered: "/api/ratio_config",
			want:       []string{"/api/ratio_config", "/api/pricing"},
		},
		{
			name:       "remembered default keeps the original order",
			remembered: "/api/pricing",
			want:       []string{"/api/pricing", "/api/ratio_config"},
		},
		{
			name:       "pinned endpoint is used alone",
			pinned:     "https://example.com/ratio.json",
			remembered: "/api/pricing",
			want:       []string{"https://example.com/ratio.json"},
		},
		{
			name:        "openrouter channels never probe",
			channelType: constant.ChannelTypeOpenRouter,
			pinned:      "https://example.com/ratio.json",
			remembered:  "/api/ratio_config",
			want:        []string{openRouterPricingEndpoint},
		},
		{
			name:   "blank pinned endpoint falls back to probing",
			pinned: "   ",
			want:   []string{"/api/pricing", "/api/ratio_config"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.want, priceMonitorEndpointCandidates(testCase.channelType, testCase.pinned, testCase.remembered))
		})
	}
}

func TestPriceMonitorEndpointDisplay(t *testing.T) {
	require.Equal(t, "/api/pricing", priceMonitorEndpointDisplay("/api/pricing"))
	require.Equal(t, "/llm/ratio.json", priceMonitorEndpointDisplay("https://example.com/llm/ratio.json?token=secret"))
	require.Equal(t, "/", priceMonitorEndpointDisplay("https://example.com"))
	require.Equal(t, openRouterPricingEndpoint, priceMonitorEndpointDisplay(openRouterPricingEndpoint))
	require.Equal(t, "", priceMonitorEndpointDisplay(""))
}

func TestResolvePriceMonitorSourcesProbesUntilItFindsPrices(t *testing.T) {
	var pricingHits, ratioHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/pricing":
			pricingHits.Add(1)
			response.WriteHeader(http.StatusNotFound)
		case "/api/ratio_config":
			ratioHits.Add(1)
			_, _ = response.Write([]byte(`{"success":true,"data":{"model_ratio":{"m":2}}}`))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	plans := []priceMonitorSourcePlan{{
		upstream:   dto.UpstreamDTO{ID: 7, Name: "probe", BaseURL: server.URL},
		candidates: []string{"/api/pricing", "/api/ratio_config"},
	}}
	outcomes := resolvePriceMonitorSources(context.Background(), plans, 2)

	outcome := outcomes["probe(7)"]
	require.NotNil(t, outcome)
	require.True(t, outcome.ok)
	require.Equal(t, "/api/ratio_config", outcome.endpoint)
	require.Equal(t, int32(1), pricingHits.Load())
	require.Equal(t, int32(1), ratioHits.Load())
}

func TestResolvePriceMonitorSourcesStopsAtTheFirstSuccess(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"success":true,"data":{"model_ratio":{"m":2}}}`))
	}))
	defer server.Close()

	plans := []priceMonitorSourcePlan{{
		upstream:   dto.UpstreamDTO{ID: 7, Name: "probe", BaseURL: server.URL},
		candidates: []string{"/api/pricing", "/api/ratio_config"},
	}}
	outcomes := resolvePriceMonitorSources(context.Background(), plans, 2)

	require.True(t, outcomes["probe(7)"].ok)
	require.Equal(t, "/api/pricing", outcomes["probe(7)"].endpoint)
	require.Equal(t, int32(1), hits.Load(), "第一个端点成功后不应继续探测")
}

func TestResolvePriceMonitorSourcesDoesNotFallBackFromPinnedEndpoint(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		response.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	plans := []priceMonitorSourcePlan{{
		upstream:   dto.UpstreamDTO{ID: 7, Name: "pinned", BaseURL: server.URL},
		candidates: []string{"/custom/price.json"},
	}}
	outcomes := resolvePriceMonitorSources(context.Background(), plans, 2)

	outcome := outcomes["pinned(7)"]
	require.NotNil(t, outcome)
	require.False(t, outcome.ok)
	require.Equal(t, "/custom/price.json", outcome.endpoint, "失败时也要回显实际用过的端点")
	require.NotEmpty(t, outcome.failure)
	require.Equal(t, int32(1), hits.Load(), "人工指定的端点不做回退")
}

func TestResolvePriceMonitorSourcesKeepsLastFailureAfterAllCandidates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/ratio_config" {
			_, _ = response.Write([]byte(`{"success":true,"data":[]}`))
			return
		}
		response.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	plans := []priceMonitorSourcePlan{{
		upstream:   dto.UpstreamDTO{ID: 7, Name: "broken", BaseURL: server.URL},
		candidates: []string{"/api/pricing", "/api/ratio_config"},
	}}
	outcomes := resolvePriceMonitorSources(context.Background(), plans, 2)

	outcome := outcomes["broken(7)"]
	require.NotNil(t, outcome)
	require.False(t, outcome.ok)
	require.Equal(t, "/api/ratio_config", outcome.endpoint)
	require.Equal(t, emptyPricingPayloadError, outcome.failure)
}
