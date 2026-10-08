package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSub2API mimics a sub2api upstream (0.2.9): /v1/sub2api/billing answers
// per API key, /api/v1/model-plaza is anonymous.
type fakeSub2API struct {
	mu           sync.Mutex
	billing      map[string]string // key (with sk-) -> JSON body; missing key -> 401
	billingCode  int               // non-zero: every billing request answers with it
	billingPlain bool              // with billingCode: a plain body, as another gateway would send
	billingByKey map[string]int    // per-key status override (sub2api's own error body)
	plaza        string            // JSON of data; empty -> plaza disabled (404)
	plazaCode    int               // non-zero: plaza answers with it and plain text
	billingCalls []string
	plazaCalls   int
}

// productionSub2APIKeys is the process-wide limiter the code starts with; a test that has not
// installed its own (isolateSub2APIKeyLimiter) gets a fresh one from server(), so rejected keys
// counted by one test never carry over into another.
var productionSub2APIKeys = priceMonitorSub2APIKeys

func (f *fakeSub2API) server(t *testing.T) *httptest.Server {
	if priceMonitorSub2APIKeys == productionSub2APIKeys {
		isolateSub2APIKeyLimiter(t, time.Now)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/sub2api/billing":
			key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			f.mu.Lock()
			f.billingCalls = append(f.billingCalls, key)
			f.mu.Unlock()
			if code := f.billingByKey[key]; code != 0 {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"permission_error","message":"x"}}`))
				return
			}
			if f.billingCode != 0 {
				w.WriteHeader(f.billingCode)
				if f.billingPlain {
					_, _ = w.Write([]byte("forbidden"))
					return
				}
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"x","message":"x"}}`))
				return
			}
			body, ok := f.billing[key]
			if !ok {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":"INVALID_API_KEY","message":"Invalid API key"}`))
				return
			}
			_, _ = w.Write([]byte(body))
		case "/api/v1/model-plaza":
			f.mu.Lock()
			f.plazaCalls++
			f.mu.Unlock()
			assert.Empty(t, r.Header.Get("Authorization"), "the plaza is fetched anonymously; the channel key is not sent")
			if f.plazaCode != 0 {
				w.WriteHeader(f.plazaCode)
				_, _ = w.Write([]byte("not found"))
				return
			}
			if f.plaza == "" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":404,"message":"Model plaza is not enabled"}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"message":"success","data":` + f.plaza + `}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func sub2apiBilling(group, resolved float64, extra string) string {
	body := `{"object":"sub2api.key_billing","schema_version":1,"billing_scope":"token","group_rate_multiplier":` +
		strconv.FormatFloat(group, 'f', -1, 64) + `,"resolved_rate_multiplier":` + strconv.FormatFloat(resolved, 'f', -1, 64) +
		`,"effective_rate_multiplier":` + strconv.FormatFloat(resolved, 'f', -1, 64) + `,"observed_at":"2026-09-28T00:00:00Z"`
	if extra != "" {
		body += "," + extra
	}
	return body + "}"
}

func sub2apiChannel(id int, baseURL, key string) *model.Channel {
	channel := &model.Channel{Id: id, Name: "sub" + strconv.Itoa(id), Key: key, BaseURL: &baseURL, Type: constant.ChannelTypeSub2API}
	return channel
}

func sub2apiRatio(channel *model.Channel) priceMonitorRatioObservation {
	return fetchPriceMonitorSub2APIRatio(context.Background(), 2*time.Second, channel)
}

// ── ratio from /v1/sub2api/billing ──────────────────────────────────────────

func TestFetchSub2APIRatio(t *testing.T) {
	t.Run("the resolved multiplier, which includes the per-user override", func(t *testing.T) {
		api := &fakeSub2API{billing: map[string]string{"sk-a": sub2apiBilling(1.5, 1.2, `"user_rate_multiplier":1.2`)}}
		got := sub2apiRatio(sub2apiChannel(1, api.server(t).URL, "sk-a"))
		require.True(t, got.ok, got.reason)
		assert.InDelta(t, 1.2, got.ratio, 1e-9)
		assert.Equal(t, priceMonitorRatioSourceSub2API, got.source)
		assert.Equal(t, priceMonitorUpstreamKindSub2API, got.kind)
		assert.Nil(t, got.peakMultiplier)
	})

	t.Run("a peak multiplier is taken as the worst case", func(t *testing.T) {
		api := &fakeSub2API{billing: map[string]string{"sk-a": sub2apiBilling(1, 1, `"peak_rate_enabled":true,"peak_start":"18:00","peak_end":"23:00","peak_rate_multiplier":1.5,"timezone":"Asia/Shanghai"`)}}
		got := sub2apiRatio(sub2apiChannel(1, api.server(t).URL, "sk-a"))
		require.True(t, got.ok, got.reason)
		assert.InDelta(t, 1.5, got.ratio, 1e-9)
		require.NotNil(t, got.peakMultiplier)
		assert.InDelta(t, 1.5, *got.peakMultiplier, 1e-9)
		assert.Equal(t, "18:00-23:00 Asia/Shanghai", got.peakWindow)
	})

	t.Run("a peak discount below 1 does not lower the worst case", func(t *testing.T) {
		api := &fakeSub2API{billing: map[string]string{"sk-a": sub2apiBilling(1, 1, `"peak_rate_enabled":true,"peak_start":"01:00","peak_end":"06:00","peak_rate_multiplier":0.5`)}}
		got := sub2apiRatio(sub2apiChannel(1, api.server(t).URL, "sk-a"))
		require.True(t, got.ok, got.reason)
		assert.InDelta(t, 1, got.ratio, 1e-9)
	})

	t.Run("keys in different groups: the highest", func(t *testing.T) {
		api := &fakeSub2API{billing: map[string]string{"sk-a": sub2apiBilling(0.8, 0.8, ""), "sk-b": sub2apiBilling(1.3, 1.3, "")}}
		got := sub2apiRatio(sub2apiChannel(1, api.server(t).URL, "sk-a\nsk-b"))
		require.True(t, got.ok, got.reason)
		assert.InDelta(t, 1.3, got.ratio, 1e-9)
	})

	t.Run("a disabled key is not queried; a key the upstream rejects is skipped", func(t *testing.T) {
		api := &fakeSub2API{billing: map[string]string{"sk-a": sub2apiBilling(9, 9, ""), "sk-c": sub2apiBilling(1.1, 1.1, "")}}
		channel := sub2apiChannel(1, api.server(t).URL, "sk-a\nsk-revoked\nsk-c")
		channel.ChannelInfo.IsMultiKey = true
		channel.ChannelInfo.MultiKeyStatusList = map[int]int{0: common.ChannelStatusManuallyDisabled}
		got := sub2apiRatio(channel)
		require.True(t, got.ok, got.reason)
		assert.InDelta(t, 1.1, got.ratio, 1e-9)
		assert.NotContains(t, api.billingCalls, "sk-a")
	})

	failures := map[string]struct {
		api    *fakeSub2API
		key    string
		reason string
	}{
		"old sub2api or simple mode": {&fakeSub2API{billingCode: http.StatusNotFound}, "sk-a", priceMonitorRatioReasonSub2APIUnsupported},
		"key without a group":        {&fakeSub2API{billingCode: http.StatusForbidden}, "sk-a", priceMonitorRatioReasonSub2APINoGroup},
		"every key rejected":         {&fakeSub2API{billing: map[string]string{}}, "sk-a", priceMonitorRatioReasonRejected},
		"rate limited":               {&fakeSub2API{billingCode: http.StatusTooManyRequests}, "sk-a", priceMonitorRatioReasonRateLimited},
		"server error":               {&fakeSub2API{billingCode: http.StatusBadGateway}, "sk-a", priceMonitorRatioReasonUnavailable},
		// Some gateways answer every path with 200: the object marker tells sub2api apart.
		"a 200 that is not sub2api": {&fakeSub2API{billing: map[string]string{"sk-a": `{"success":true}`}}, "sk-a", priceMonitorRatioReasonSub2APIUnsupported},
	}
	for name, tc := range failures {
		t.Run(name, func(t *testing.T) {
			got := sub2apiRatio(sub2apiChannel(1, tc.api.server(t).URL, tc.key))
			assert.False(t, got.ok)
			assert.Equal(t, tc.reason, got.reason)
		})
	}

	t.Run("no key", func(t *testing.T) {
		got := sub2apiRatio(sub2apiChannel(1, "http://127.0.0.1:1", ""))
		assert.Equal(t, priceMonitorRatioReasonNoSource, got.reason)
	})
}

func TestFetchSub2APIRatioTimesOut(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); server.Close() })
	started := time.Now()
	got := fetchPriceMonitorSub2APIRatio(context.Background(), 200*time.Millisecond, sub2apiChannel(1, server.URL, "sk-a"))
	assert.Less(t, time.Since(started), 2*time.Second)
	assert.Equal(t, priceMonitorRatioReasonUnavailable, got.reason)
}

// ── which source is tried ───────────────────────────────────────────────────

type orderFetcher struct {
	calls    []string
	official priceMonitorRatioObservation
	sub2api  priceMonitorRatioObservation
	log      priceMonitorRatioObservation
}

func (f *orderFetcher) fetcher() priceMonitorUpstreamRatioFetcher {
	return priceMonitorUpstreamRatioFetcher{
		official: func(context.Context, *model.Channel) priceMonitorRatioObservation {
			f.calls = append(f.calls, "official")
			return f.official
		},
		sub2api: func(context.Context, *model.Channel) priceMonitorRatioObservation {
			f.calls = append(f.calls, "sub2api")
			return f.sub2api
		},
		log: func(context.Context, *model.Channel) priceMonitorRatioObservation {
			f.calls = append(f.calls, "log")
			return f.log
		},
	}
}

func TestResolveChannelRatioSourceOrder(t *testing.T) {
	sub2apiOK := priceMonitorRatioObservation{ok: true, ratio: 1.2, source: priceMonitorRatioSourceSub2API, kind: priceMonitorUpstreamKindSub2API}
	officialOK := priceMonitorRatioObservation{ok: true, ratio: 0.8, source: priceMonitorRatioSourceOfficial, kind: priceMonitorUpstreamKindNewAPI}
	logOK := priceMonitorRatioObservation{ok: true, ratio: 0.9, source: priceMonitorRatioSourceLog, kind: priceMonitorUpstreamKindNewAPI}
	notSupported := priceMonitorRatioObservation{reason: priceMonitorRatioReasonNotSupported}
	sub2apiMissing := priceMonitorRatioObservation{reason: priceMonitorRatioReasonSub2APIUnsupported}

	cases := []struct {
		name      string
		channel   *model.Channel
		kind      string
		fetcher   *orderFetcher
		wantCalls []string
		want      float64
	}{
		{"a Sub2API channel only asks sub2api", sub2apiChannel(1, "https://s.example", "sk-a"), "", &orderFetcher{sub2api: sub2apiOK}, []string{"sub2api"}, 1.2},
		{"a Sub2API channel does not fall back to the new-api log", sub2apiChannel(1, "https://s.example", "sk-a"), "", &orderFetcher{sub2api: sub2apiMissing, log: logOK}, []string{"sub2api"}, 0},
		{"a channel recognised as sub2api last round goes straight there", costTestChannel(1, "https://s.example", true), priceMonitorUpstreamKindSub2API, &orderFetcher{sub2api: sub2apiOK, official: officialOK}, []string{"sub2api"}, 1.2},
		{"the official API answers: sub2api is not tried", costTestChannel(1, "https://n.example", true), "", &orderFetcher{official: officialOK, sub2api: sub2apiOK}, []string{"official"}, 0.8},
		{"no account token: sub2api before the rate-limited log", costTestChannel(1, "https://s.example", false), "", &orderFetcher{sub2api: sub2apiOK, log: logOK}, []string{"sub2api"}, 1.2},
		{"the official API is missing: sub2api next", costTestChannel(1, "https://s.example", true), "", &orderFetcher{official: notSupported, sub2api: sub2apiOK}, []string{"official", "sub2api"}, 1.2},
		{"neither: the log fallback as before", costTestChannel(1, "https://n.example", false), "", &orderFetcher{sub2api: sub2apiMissing, log: logOK}, []string{"sub2api", "log"}, 0.9},
		// A new-api account answered but the key is elsewhere: that upstream is new-api, not sub2api.
		{"an official answer about the key skips sub2api", costTestChannel(1, "https://n.example", true), "", &orderFetcher{official: priceMonitorRatioObservation{reason: priceMonitorRatioReasonKeyNotInAccount}, log: logOK}, []string{"official", "log"}, 0.9},
		{"a channel known as new-api does not probe sub2api", costTestChannel(1, "https://n.example", false), priceMonitorUpstreamKindNewAPI, &orderFetcher{sub2api: sub2apiOK, log: logOK}, []string{"log"}, 0.9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, _ := resolvePriceMonitorChannelRatio(context.Background(), tc.channel, tc.kind, true, true, tc.fetcher.fetcher())
			assert.Equal(t, tc.wantCalls, tc.fetcher.calls)
			if tc.want == 0 {
				assert.False(t, got.ok)
				return
			}
			require.True(t, got.ok)
			assert.InDelta(t, tc.want, got.ratio, 1e-9)
		})
	}
}

// The kind and peak details land on the snapshot row, so the next round goes
// straight to the right interface and the page can show the peak window.
func TestApplyRatioObservationRecordsKindAndPeak(t *testing.T) {
	cost := PriceMonitorChannelCost{ChannelId: 1}
	peak := 1.5
	applyPriceMonitorRatioObservation(&cost, priceMonitorRatioObservation{ok: true, ratio: 1.5, source: priceMonitorRatioSourceSub2API, kind: priceMonitorUpstreamKindSub2API, peakMultiplier: &peak, peakWindow: "18:00-23:00"}, time.Unix(1_800_000_000, 0))
	assert.Equal(t, priceMonitorUpstreamKindSub2API, cost.UpstreamKind)
	require.NotNil(t, cost.PeakMultiplier)
	assert.InDelta(t, 1.5, *cost.PeakMultiplier, 1e-9)
	assert.Equal(t, "18:00-23:00", cost.PeakWindow)

	// A later observation without a peak clears the stale window.
	applyPriceMonitorRatioObservation(&cost, priceMonitorRatioObservation{ok: true, ratio: 1, source: priceMonitorRatioSourceSub2API, kind: priceMonitorUpstreamKindSub2API}, time.Unix(1_800_000_100, 0))
	assert.Nil(t, cost.PeakMultiplier)
	assert.Empty(t, cost.PeakWindow)

	// A failure keeps what was learned about the upstream.
	applyPriceMonitorRatioObservation(&cost, priceMonitorRatioObservation{reason: priceMonitorRatioReasonUnavailable}, time.Unix(1_800_000_200, 0))
	assert.Equal(t, priceMonitorUpstreamKindSub2API, cost.UpstreamKind)

	// The sub2api interface is gone (gateway replaced or downgraded): forget the kind,
	// so the next round identifies the upstream again instead of asking sub2api forever.
	applyPriceMonitorRatioObservation(&cost, priceMonitorRatioObservation{reason: priceMonitorRatioReasonSub2APIUnsupported}, time.Unix(1_800_000_300, 0))
	assert.Empty(t, cost.UpstreamKind)
}

// Upgrading the snapshot makes every channel due once, so channels that were
// "not supported" before sub2api was known are retried now, not in 6 hours.
func TestPriceMonitorCostsDueAfterUpgrade(t *testing.T) {
	ratio := 0.8
	costs := map[string]PriceMonitorChannelCost{"1": {ChannelId: 1, UpstreamRatio: &ratio, AttemptedAt: 100, ObservedAt: 100}}
	due := priceMonitorCostsDueNow(costs)
	assert.Zero(t, due["1"].AttemptedAt)
	require.NotNil(t, due["1"].UpstreamRatio, "the last known ratio is kept until a new one arrives")
	assert.Equal(t, int64(100), costs["1"].AttemptedAt, "the stored snapshot is not modified")
}

// ── prices from /api/v1/model-plaza ─────────────────────────────────────────

const plazaTwoGroups = `{"description":"","groups":[
 {"id":1,"name":"default","platform":"anthropic","rate_multiplier":1,"peak_rate_enabled":false,"long_context_pricing_enabled":false,"models":[
  {"name":"claude-a","platform":"anthropic","pricing":{"billing_mode":"token","input_price":0.000003,"output_price":0.000015,"cache_write_price":0.00000375,"cache_read_price":0.0000003,"intervals":[]}},
  {"name":"claude-long","platform":"anthropic","pricing":{"billing_mode":"token","input_price":0.000003,"output_price":0.000015,"intervals":[{"min_tokens":0,"max_tokens":200000,"input_price":0.000003},{"min_tokens":200000,"input_price":0.000006}]}},
  {"name":"img","platform":"openai","pricing":{"billing_mode":"image","image_output_price":0.04,"intervals":[]}},
  {"name":"per-call","platform":"openai","pricing":{"billing_mode":"per_request","per_request_price":0.05,"intervals":[]}}
 ]},
 {"id":2,"name":"vip","platform":"anthropic","rate_multiplier":0.5,"peak_rate_enabled":false,"long_context_pricing_enabled":true,"models":[
  {"name":"claude-a","platform":"anthropic","pricing":{"billing_mode":"token","input_price":0.000004,"output_price":0.000012,"intervals":[]}},
  {"name":"claude-long","platform":"anthropic","pricing":{"billing_mode":"token","input_price":0.000003,"output_price":0.000015,"intervals":[{"min_tokens":0,"max_tokens":200000,"input_price":0.000003},{"min_tokens":200000,"input_price":0.000006}]}}
 ]}
]}`

func plazaBilling(group float64) sub2apiKeyBilling {
	return sub2apiKeyBilling{Object: sub2apiKeyBillingObject, GroupRateMultiplier: group, ResolvedRateMultiplier: group}
}

func TestConvertSub2APIPlaza(t *testing.T) {
	t.Run("the group matching the key's multiplier, converted to ratios", func(t *testing.T) {
		data, err := convertSub2APIPlazaToRatioData([]byte(plazaTwoGroups), plazaBilling(1))
		require.NoError(t, err)
		modelRatio := valueMap(data["model_ratio"])
		// $3 per 1M input = ratio 1.5
		assert.InDelta(t, 1.5, modelRatio["claude-a"], 1e-9)
		assert.InDelta(t, 5, valueMap(data["completion_ratio"])["claude-a"], 1e-9)
		assert.InDelta(t, 0.1, valueMap(data["cache_ratio"])["claude-a"], 1e-9)
		assert.InDelta(t, 1.25, valueMap(data["create_cache_ratio"])["claude-a"], 1e-9)
		assert.InDelta(t, 0.05, valueMap(data["model_price"])["per-call"], 1e-9)
		// Long-context tiers only bill when the group enables them; here it does not.
		assert.InDelta(t, 1.5, modelRatio["claude-long"], 1e-9)
		assert.NotContains(t, modelRatio, "img", "per-image prices are not compared")
	})

	t.Run("a group billing long-context tiers skips tiered models", func(t *testing.T) {
		data, err := convertSub2APIPlazaToRatioData([]byte(plazaTwoGroups), plazaBilling(0.5))
		require.NoError(t, err)
		modelRatio := valueMap(data["model_ratio"])
		assert.InDelta(t, 2, modelRatio["claude-a"], 1e-9)
		assert.NotContains(t, modelRatio, "claude-long")
	})

	t.Run("several groups share the multiplier: the highest price per field", func(t *testing.T) {
		plaza := strings.Replace(plazaTwoGroups, `"rate_multiplier":0.5`, `"rate_multiplier":1`, 1)
		data, err := convertSub2APIPlazaToRatioData([]byte(plaza), plazaBilling(1))
		require.NoError(t, err)
		// input: max(3, 4) = $4 -> ratio 2; output: max(15, 12) = $15 -> completion 15/4
		assert.InDelta(t, 2, valueMap(data["model_ratio"])["claude-a"], 1e-9)
		assert.InDelta(t, 3.75, valueMap(data["completion_ratio"])["claude-a"], 1e-9)
	})

	t.Run("a peak setting has to match as well", func(t *testing.T) {
		billing := plazaBilling(1)
		billing.PeakRateEnabled = true
		peak := 1.5
		billing.PeakRateMultiplier = &peak
		_, err := convertSub2APIPlazaToRatioData([]byte(plazaTwoGroups), billing)
		require.EqualError(t, err, sub2apiGroupUnknownError)
	})

	t.Run("no visible group has the key's multiplier", func(t *testing.T) {
		_, err := convertSub2APIPlazaToRatioData([]byte(plazaTwoGroups), plazaBilling(0.3))
		require.EqualError(t, err, sub2apiGroupUnknownError)
	})

	t.Run("the group has no priced model", func(t *testing.T) {
		plaza := `{"groups":[{"id":1,"rate_multiplier":1,"models":[{"name":"img","pricing":{"billing_mode":"image","intervals":[]}}]}]}`
		_, err := convertSub2APIPlazaToRatioData([]byte(plaza), plazaBilling(1))
		require.EqualError(t, err, emptyPricingPayloadError)
	})
}

// ── fetching the plaza as a price source ────────────────────────────────────

func withSub2APIChannelLoader(t *testing.T, channel *model.Channel) {
	t.Helper()
	previous := pricingSourceChannel
	pricingSourceChannel = func(int) (*model.Channel, error) { return channel, nil }
	t.Cleanup(func() { pricingSourceChannel = previous })
}

func fetchSub2APISource(t *testing.T, baseURL string) dto.TestResult {
	t.Helper()
	results, _ := fetchUpstreamPricingSources(context.Background(), []dto.UpstreamDTO{{ID: 7, Name: "sub", BaseURL: baseURL, Endpoint: sub2apiPricingEndpoint}}, 5)
	require.Len(t, results, 1)
	return results[0]
}

func TestFetchSub2APIPlazaSource(t *testing.T) {
	t.Run("prices for the key's group", func(t *testing.T) {
		api := &fakeSub2API{plaza: plazaTwoGroups, billing: map[string]string{"sk-a": sub2apiBilling(0.5, 0.5, "")}}
		url := api.server(t).URL
		withSub2APIChannelLoader(t, sub2apiChannel(7, url, "sk-a"))
		results, sources := fetchUpstreamPricingSources(context.Background(), []dto.UpstreamDTO{{ID: 7, Name: "sub", BaseURL: url, Endpoint: sub2apiPricingEndpoint}}, 5)
		require.Equal(t, "success", results[0].Status, results[0].Error)
		require.Len(t, sources, 1)
		assert.InDelta(t, 2, valueMap(sources[0].data["model_ratio"])["claude-a"], 1e-9)
	})

	t.Run("plaza disabled: the key is never sent", func(t *testing.T) {
		api := &fakeSub2API{billing: map[string]string{"sk-a": sub2apiBilling(1, 1, "")}}
		url := api.server(t).URL
		withSub2APIChannelLoader(t, sub2apiChannel(7, url, "sk-a"))
		result := fetchSub2APISource(t, url)
		assert.Equal(t, sub2apiPlazaDisabledError, result.Error)
		assert.Empty(t, api.billingCalls)
		assert.Equal(t, priceMonitorFailureSub2APIPlazaDisabled, priceMonitorFailureKind(result.Error))
	})

	t.Run("not sub2api at all: a plain fetch failure, not 'plaza disabled'", func(t *testing.T) {
		api := &fakeSub2API{plazaCode: http.StatusNotFound}
		url := api.server(t).URL
		withSub2APIChannelLoader(t, sub2apiChannel(7, url, "sk-a"))
		result := fetchSub2APISource(t, url)
		assert.NotEqual(t, "success", result.Status)
		assert.Equal(t, priceMonitorFailureFetch, priceMonitorFailureKind(result.Error))
		assert.Empty(t, api.billingCalls)
	})

	t.Run("group not found", func(t *testing.T) {
		api := &fakeSub2API{plaza: plazaTwoGroups, billing: map[string]string{"sk-a": sub2apiBilling(0.3, 0.3, "")}}
		url := api.server(t).URL
		withSub2APIChannelLoader(t, sub2apiChannel(7, url, "sk-a"))
		result := fetchSub2APISource(t, url)
		assert.Equal(t, sub2apiGroupUnknownError, result.Error)
		assert.Equal(t, priceMonitorFailureSub2APIGroupUnknown, priceMonitorFailureKind(result.Error))
	})

	t.Run("the first enabled key is used", func(t *testing.T) {
		api := &fakeSub2API{plaza: plazaTwoGroups, billing: map[string]string{"sk-b": sub2apiBilling(1, 1, "")}}
		url := api.server(t).URL
		channel := sub2apiChannel(7, url, "sk-a\nsk-b")
		channel.ChannelInfo.IsMultiKey = true
		channel.ChannelInfo.MultiKeyStatusList = map[int]int{0: common.ChannelStatusAutoDisabled}
		withSub2APIChannelLoader(t, channel)
		result := fetchSub2APISource(t, url)
		assert.Equal(t, "success", result.Status, result.Error)
		assert.Equal(t, []string{"sk-b"}, api.billingCalls)
	})
}

func TestPriceMonitorEndpointCandidatesSub2API(t *testing.T) {
	assert.Equal(t, []string{sub2apiPricingEndpoint}, priceMonitorEndpointCandidates(constant.ChannelTypeSub2API, "", ""))
	assert.Equal(t, []string{"/custom"}, priceMonitorEndpointCandidates(constant.ChannelTypeSub2API, "/custom", ""), "a pinned endpoint still wins")
	assert.Equal(t, []string{defaultEndpoint, "/api/ratio_config", sub2apiPricingEndpoint}, priceMonitorEndpointCandidates(constant.ChannelTypeOpenAI, "", ""))
	assert.Equal(t, []string{sub2apiPricingEndpoint, defaultEndpoint, "/api/ratio_config"}, priceMonitorEndpointCandidates(constant.ChannelTypeOpenAI, "", sub2apiPricingEndpoint), "a remembered sub2api source is tried first")
}
