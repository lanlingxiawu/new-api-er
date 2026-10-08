// Regression tests for price monitor defects found by the branch audit
// (docs/design/branch-audit-vs-main.md H3, M9, M10, L14, L15).

package controller

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/price_monitor_setting"

	"github.com/stretchr/testify/require"
)

// H3: the settings dialog always sends the three channel-cost-check fields
// (web/src/features/system-settings/models/price-monitor-panel.tsx saveMutation) and
// validatePriceMonitorSettingsRequest forwards them, so model/config_group.go priceMonitorFields
// must list them; otherwise SaveConfigGroup rejects every save from the UI as "not editable".
func TestBranchAuditPriceMonitorSettingsSaveAcceptsChannelCostFields(t *testing.T) {
	useApplyPriceEnv(t, PriceMonitorSnapshot{})
	restorePriceMonitorSettingForAudit(t)

	logs, refresh, maxAge := 8, 6, 7
	values, ok := validatePriceMonitorSettingsRequest(priceMonitorSettingsRequest{
		Enabled:                   true,
		IntervalMinutes:           360,
		TimeoutSeconds:            10,
		UpstreamLogQueriesPerHost: &logs,
		UpstreamRatioRefreshHours: &refresh,
		UpstreamRatioMaxAgeDays:   &maxAge,
	})
	require.True(t, ok, "the request the UI sends is valid")

	_, err := model.SaveConfigGroup("price_monitor_setting", values)
	require.NoError(t, err, "the settings the UI sends must be saved")
	saved := price_monitor_setting.GetPriceMonitorSetting()
	require.Equal(t, 8, saved.UpstreamLogQueriesPerHost)
	require.Equal(t, 6, saved.UpstreamRatioRefreshHours)
	require.Equal(t, 7, saved.UpstreamRatioMaxAgeDays)
	var option model.Option
	require.NoError(t, model.DB.Where(model.Option{Key: "price_monitor_setting.upstream_ratio_max_age_days"}).Take(&option).Error)
	require.Equal(t, "7", option.Value, "persisted, so the value survives a restart")
}

// SaveConfigGroup is also reachable without the controller's validation, so it must refuse
// non-integer and out-of-range cost-check values instead of silently keeping the old ones.
func TestBranchAuditPriceMonitorSaveConfigGroupRejectsInvalidCostFields(t *testing.T) {
	useApplyPriceEnv(t, PriceMonitorSnapshot{})
	restorePriceMonitorSettingForAudit(t)
	before := *price_monitor_setting.GetPriceMonitorSetting()
	for name, values := range map[string]map[string]string{
		"non-integer log queries":   {"upstream_log_queries_per_host": "abc"},
		"log queries above max":     {"upstream_log_queries_per_host": "21"},
		"negative log queries":      {"upstream_log_queries_per_host": "-1"},
		"refresh of zero hours":     {"upstream_ratio_refresh_hours": "0"},
		"max age above 30 days":     {"upstream_ratio_max_age_days": "31"},
		"interval above 30 days":    {"interval_minutes": "43201"},
		"interval that overflows":   {"interval_minutes": "9223372036854775807"},
		"unknown field still fails": {"not_a_field": "1"},
	} {
		_, err := model.SaveConfigGroup("price_monitor_setting", values)
		require.Error(t, err, name)
	}
	require.Equal(t, before.UpstreamLogQueriesPerHost, price_monitor_setting.GetPriceMonitorSetting().UpstreamLogQueriesPerHost)
	require.Equal(t, before.IntervalMinutes, price_monitor_setting.GetPriceMonitorSetting().IntervalMinutes)
}

// interval_minutes is also the share password lifetime (checkedAt + interval); an unbounded
// value overflowed that computation into the past. The request validator caps it at 30 days.
func TestBranchAuditPriceMonitorIntervalHasUpperBound(t *testing.T) {
	_, ok := validatePriceMonitorSettingsRequest(priceMonitorSettingsRequest{IntervalMinutes: price_monitor_setting.MaxIntervalMinutes, TimeoutSeconds: 10})
	require.True(t, ok, "30 days is accepted")
	for _, minutes := range []int{price_monitor_setting.MaxIntervalMinutes + 1, 153_722_868, int(^uint(0) >> 1)} {
		values, ok := validatePriceMonitorSettingsRequest(priceMonitorSettingsRequest{IntervalMinutes: minutes, TimeoutSeconds: 10})
		require.False(t, ok, minutes)
		require.Nil(t, values)
	}
}

// M9: a check that fails before saving (all sources failed, whitelist excludes everything,
// channel load failed) leaves the snapshot "needing a refresh". The refresh shortcut must not
// bypass the last attempt time, or the failing check runs again on every one-minute tick.
func TestBranchAuditPriceMonitorFailedRunIsNotRetriedEveryTick(t *testing.T) {
	isolatePriceMonitorSchedulerForAudit(t, PriceMonitorSnapshot{})

	now := time.Now()
	failedAMinuteAgo := now.Add(-time.Minute).Unix()
	priceMonitorLastAttempt.Store(failedAMinuteAgo)
	require.False(t, priceMonitorRunning.Load(), "precondition: no check running")

	runPriceMonitorCheckIfDue(now)
	require.Eventually(t, func() bool { return !priceMonitorRunning.Load() }, 5*time.Second, 10*time.Millisecond)

	require.Equal(t, failedAMinuteAgo, priceMonitorLastAttempt.Load(),
		"an attempt that failed a minute ago must not be retried before the 360-minute interval")

	// Once the interval has passed, the failed check is retried.
	later := now.Add(361 * time.Minute)
	runPriceMonitorCheckIfDue(later)
	require.Eventually(t, func() bool { return !priceMonitorRunning.Load() }, 5*time.Second, 10*time.Millisecond)
	require.Greater(t, priceMonitorLastAttempt.Load(), failedAMinuteAgo, "retried after the interval")
}

// M9: after an install or upgrade (snapshot without source headers, or an older matrix version)
// the first tick of the process checks immediately, even if the last successful check is recent.
func TestBranchAuditPriceMonitorRefreshRunsPromptlyOnceAfterUpgrade(t *testing.T) {
	now := time.Now()
	recent := now.Add(-time.Minute).Unix()
	isolatePriceMonitorSchedulerForAudit(t, PriceMonitorSnapshot{CheckedAt: recent, MatrixVersion: priceMonitorMatrixVersion - 1, SourceHeaders: []PriceMonitorSourceHeader{{Key: priceMonitorPlatformKey}}})
	priceMonitorLastAttempt.Store(0)

	runPriceMonitorCheckIfDue(now)
	require.Eventually(t, func() bool { return !priceMonitorRunning.Load() }, 5*time.Second, 10*time.Millisecond)
	require.NotZero(t, priceMonitorLastAttempt.Load(), "an outdated snapshot is refreshed on the first tick, not after the interval")
}

// isolatePriceMonitorSchedulerForAudit gives the scheduler a temporary snapshot store and a
// whitelist that excludes every marketplace model, so a triggered check stops before any network
// request (and records only its attempt time).
func isolatePriceMonitorSchedulerForAudit(t *testing.T, snapshot PriceMonitorSnapshot) {
	t.Helper()
	requireDB(t)
	getPriceMonitorStore()
	previousStore := priceMonitorStore
	priceMonitorStore = newPriceMonitorSnapshotStore(filepath.Join(t.TempDir(), "snapshot.json"))
	if snapshot.CheckedAt != 0 {
		require.NoError(t, priceMonitorStore.Save(snapshot))
	}
	previousAttempt := priceMonitorLastAttempt.Load()
	previousError := getPriceMonitorRuntimeError()
	t.Cleanup(func() {
		priceMonitorStore = previousStore
		priceMonitorLastAttempt.Store(previousAttempt)
		setPriceMonitorRuntimeError(previousError)
	})
	restorePriceMonitorSettingForAudit(t)

	// Exclude every marketplace model so a triggered run stops before any network request.
	names := make([]string, 0)
	for _, pricing := range model.GetPricing() {
		names = append(names, pricing.ModelName)
	}
	require.NoError(t, config.GlobalConfig.UpdateFromMap("price_monitor_setting", map[string]string{
		"enabled":          "true",
		"interval_minutes": "360",
		"model_whitelist":  strings.Join(names, "\n"),
	}))
}

// M10: the endpoint memory is only a probe-order cache (price-monitor-source-coverage-guard.md
// §3.2). A remembered absolute URL (a pin the admin has since removed, kept by an older snapshot)
// must not be tried, or the channel keeps being compared against that other host.s prices.
func TestBranchAuditPriceMonitorForgetsRemovedCustomEndpoint(t *testing.T) {
	pricing := func(ratio float64) *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/pricing" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = fmt.Fprintf(w, `{"success":true,"data":[{"model_name":"m","quota_type":0,"model_ratio":%g,"completion_ratio":1}]}`, ratio)
		}))
		t.Cleanup(server.Close)
		return server
	}
	formerlyPinned := pricing(9)
	channelUpstream := pricing(1)

	upstream := dto.UpstreamDTO{ID: 7, Name: "ch", BaseURL: channelUpstream.URL}
	// The pin was removed; the previous round remembered the pinned absolute URL.
	candidates := priceMonitorEndpointCandidates(constant.ChannelTypeOpenAI, "", formerlyPinned.URL+"/api/pricing")
	outcomes := resolvePriceMonitorSources(context.Background(), []priceMonitorSourcePlan{{upstream: upstream, candidates: candidates}}, 5)

	outcome := outcomes[pricingSourceDisplayName(upstream)]
	require.NotNil(t, outcome)
	require.True(t, outcome.ok)
	require.InDelta(t, 1, valueMap(outcome.source.data["model_ratio"])["m"], 1e-9,
		"without a pin, prices must come from the channel's own upstream, not a remembered foreign URL")
}

// L14: sub2api bans an IP for 60 s after 120 invalid keys in 60 s, which also blocks our relay
// traffic to that host. The price-source step (fetchSub2APIPlazaPricing) fetches channels
// concurrently and sends up to 5 keys each; it must stay within its share of the per-host cap.
func TestBranchAuditPriceMonitorSub2APIPriceSourceCapsRejectedKeysPerHost(t *testing.T) {
	isolateSub2APIKeyLimiter(t, time.Now)
	var rejected atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case sub2apiModelPlazaPath:
			_, _ = w.Write([]byte(`{"code":0,"message":"success","data":` + plazaTwoGroups + `}`))
		case sub2apiKeyBillingPath:
			rejected.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"INVALID_API_KEY","message":"Invalid API key"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	channels, upstreams := revokedSub2APIChannels(server.URL, 1, 5)
	usePricingSourceChannels(t, channels)

	_, _ = fetchUpstreamPricingSources(context.Background(), upstreams, 5)
	require.Equal(t, int64(priceMonitorSub2APIPriceStepLimit), rejected.Load(),
		"one price fetch sends rejected keys up to the price step's share of the per-host cap, no more")
}

// L14: the price step and the ratio step run in the same round and both send channel keys to
// /v1/sub2api/billing. The ratio step reuses the price step's per-key answers instead of sending the
// same keys again, and part of the per-host cap is reserved for it: a host with 20+ locally enabled
// but revoked keys can no longer starve every ratio-step channel into "waiting" forever.
func TestBranchAuditPriceMonitorSub2APIRatioStepReusesPriceStepAnswers(t *testing.T) {
	isolateSub2APIKeyLimiter(t, time.Now)
	api := &fakeSub2API{billing: map[string]string{}, plaza: plazaTwoGroups}
	url := api.server(t).URL
	channels, upstreams := revokedSub2APIChannels(url, 1, 5)
	usePricingSourceChannels(t, channels)

	run := newPriceMonitorSub2APIRun()
	results, _ := fetchUpstreamPricingSources(withPriceMonitorSub2APIRun(context.Background(), run), upstreams[:3], 5)
	require.Len(t, results, 3)
	require.Equal(t, 3*priceMonitorOfficialMaxKeys, len(api.billingCalls), "the price step asks each of the 15 keys once")
	require.Equal(t, priceMonitorSub2APIPriceStepLimit, len(api.billingCalls), "and that is its whole share of the cap")

	fetcher := newPriceMonitorUpstreamRatioFetcher(2*time.Second, run)
	for id := 1; id <= 3; id++ {
		got := fetcher.sub2api(context.Background(), channels[id])
		require.Equal(t, priceMonitorRatioReasonRejected, got.reason, "channel %d: the price step's answer is reused", id)
	}
	require.Equal(t, priceMonitorSub2APIPriceStepLimit, len(api.billingCalls), "keys already answered in this round are not sent again")

	fourth := fetcher.sub2api(context.Background(), channels[4])
	require.Equal(t, priceMonitorRatioReasonRejected, fourth.reason, "the reserved share lets the ratio step check a channel the price step never reached")
	require.Equal(t, priceMonitorSub2APIMaxRejectedPerHost, len(api.billingCalls))

	fifth := fetcher.sub2api(context.Background(), channels[5])
	require.Equal(t, priceMonitorRatioReasonWaiting, fifth.reason, "the whole cap is used; the channel is retried next round")
	require.Equal(t, priceMonitorSub2APIMaxRejectedPerHost, len(api.billingCalls))
}

// A successful answer is reused as well (one billing request per key per round), while a transient
// failure (5xx, timeout) is asked again by the ratio step.
func TestPriceMonitorSub2APIRunReusesOnlyStableAnswers(t *testing.T) {
	isolateSub2APIKeyLimiter(t, time.Now)
	api := &fakeSub2API{
		billing:      map[string]string{"sk-good": sub2apiBilling(1, 1, "")},
		billingByKey: map[string]int{"sk-flaky": http.StatusBadGateway},
		plaza:        plazaTwoGroups,
	}
	url := api.server(t).URL
	good := sub2apiChannel(1, url, "sk-good")
	flaky := sub2apiChannel(2, url, "sk-flaky")
	usePricingSourceChannels(t, map[int]*model.Channel{1: good, 2: flaky})

	run := newPriceMonitorSub2APIRun()
	upstreams := []dto.UpstreamDTO{
		{ID: 1, Name: "sub", BaseURL: url, Endpoint: sub2apiPricingEndpoint},
		{ID: 2, Name: "sub", BaseURL: url, Endpoint: sub2apiPricingEndpoint},
	}
	_, _ = fetchUpstreamPricingSources(withPriceMonitorSub2APIRun(context.Background(), run), upstreams, 5)
	require.ElementsMatch(t, []string{"sk-good", "sk-flaky"}, api.billingCalls)

	fetcher := newPriceMonitorUpstreamRatioFetcher(2*time.Second, run)
	require.True(t, fetcher.sub2api(context.Background(), good).ok)
	require.Equal(t, priceMonitorRatioReasonUnavailable, fetcher.sub2api(context.Background(), flaky).reason)
	require.ElementsMatch(t, []string{"sk-good", "sk-flaky", "sk-flaky"}, api.billingCalls,
		"the successful key is not asked twice; the 502 is asked again")
}

// L14: the per-host cap spans calls within a 60 s window. Each manual "sync upstream ratios" is a
// separate call; repeated clicks must not add up to sub2api's ban threshold.
func TestBranchAuditPriceMonitorSub2APIRejectedKeyCapSpansCalls(t *testing.T) {
	clock := &fakeSub2APIClock{now: time.Unix(1_700_000_000, 0)}
	isolateSub2APIKeyLimiter(t, clock.Now)
	api := &fakeSub2API{billing: map[string]string{}, plaza: plazaTwoGroups}
	url := api.server(t).URL
	channels, upstreams := revokedSub2APIChannels(url, 1, 5)
	usePricingSourceChannels(t, channels)

	_, _ = fetchUpstreamPricingSources(context.Background(), upstreams, 5)
	require.Equal(t, priceMonitorSub2APIPriceStepLimit, len(api.billingCalls))
	for click := 0; click < 6; click++ {
		_, _ = fetchUpstreamPricingSources(context.Background(), upstreams, 5)
	}
	require.Equal(t, priceMonitorSub2APIPriceStepLimit, len(api.billingCalls), "further calls within the window send no keys")
	require.Equal(t, priceMonitorRatioReasonRejected, newPriceMonitorUpstreamRatioFetcher(2*time.Second, nil).sub2api(context.Background(), channels[1]).reason,
		"the ratio step still has its reserved share")
	require.Equal(t, priceMonitorSub2APIMaxRejectedPerHost, len(api.billingCalls))

	clock.Advance(priceMonitorSub2APIRejectWindow)
	_, _ = fetchUpstreamPricingSources(context.Background(), upstreams, 5)
	require.Equal(t, priceMonitorSub2APIMaxRejectedPerHost+priceMonitorSub2APIPriceStepLimit, len(api.billingCalls), "once the window has passed, keys are sent again")
}

// Concurrent senders never push the rejected-key count past the cap: a slot is reserved before
// a key is sent, and a sender waits while the in-flight requests could still reach it.
func TestPriceMonitorSub2APIKeyLimiter(t *testing.T) {
	clock := &fakeSub2APIClock{now: time.Unix(1_700_000_000, 0)}
	limiter := newPriceMonitorSub2APIKeyLimiter(clock.Now)
	limit := priceMonitorSub2APIMaxRejectedPerHost
	for i := 0; i < limit; i++ {
		require.True(t, limiter.acquireForTest("h", limit))
	}
	acquired := make(chan bool, 1)
	go func() { acquired <- limiter.acquireForTest("h", limit) }()
	select {
	case <-acquired:
		require.Fail(t, "a 21st request must wait while 20 are in flight")
	case <-time.After(50 * time.Millisecond):
	}
	limiter.release("h", "")
	require.True(t, <-acquired, "a successful request frees its slot")
	require.True(t, limiter.acquireForTest("other", limit), "hosts are counted separately")
	limiter.release("other", "")
	for i := 0; i < limit; i++ {
		limiter.release("h", priceMonitorRatioReasonRejected)
	}
	require.False(t, limiter.acquireForTest("h", limit), "the cap is reached: no more keys in this window")
	require.False(t, limiter.acquireForTest("h", priceMonitorSub2APIPriceStepLimit), "nor for the smaller price-step share")

	clock.Advance(priceMonitorSub2APIRejectWindow - time.Second)
	require.False(t, limiter.acquireForTest("h", limit), "still inside the window")
	clock.Advance(time.Second)
	require.True(t, limiter.acquireForTest("h", limit), "rejections older than the window no longer count")
	limiter.release("h", "")

	require.True(t, limiter.acquireForTest("h", limit))
	limiter.release("h", priceMonitorRatioReasonRateLimited)
	require.False(t, limiter.acquireForTest("h", limit), "a 429 stops the host for a window")
	clock.Advance(priceMonitorSub2APIRejectWindow)
	require.True(t, limiter.acquireForTest("h", limit), "and only for a window")
	limiter.release("h", "")

	share := priceMonitorSub2APIPriceStepLimit
	for i := 0; i < share; i++ {
		require.True(t, limiter.acquireForTest("p", share))
		limiter.release("p", priceMonitorRatioReasonRejected)
	}
	require.False(t, limiter.acquireForTest("p", share), "the price step's share is used up")
	require.True(t, limiter.acquireForTest("p", limit), "the ratio step's reserve is still available")
	limiter.release("p", "")

	clock.Advance(priceMonitorSub2APIRejectWindow)
	require.True(t, limiter.acquireForTest("h", limit))
	limiter.release("h", "")
	require.True(t, limiter.acquireForTest("p", limit))
	limiter.release("p", "")
	require.Empty(t, limiter.hosts, "hosts with nothing left to track are forgotten")
}

type fakeSub2APIClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeSub2APIClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeSub2APIClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// isolateSub2APIKeyLimiter gives the test its own process-wide sub2api key limiter, so rejected keys
// counted by one test never affect another.
func isolateSub2APIKeyLimiter(t *testing.T, now func() time.Time) {
	t.Helper()
	previous := priceMonitorSub2APIKeys
	priceMonitorSub2APIKeys = newPriceMonitorSub2APIKeyLimiter(now)
	t.Cleanup(func() { priceMonitorSub2APIKeys = previous })
}

// revokedSub2APIChannels builds multi-key Sub2API channels first..last whose five keys the fake
// upstream rejects, plus the matching price-source upstreams.
func revokedSub2APIChannels(url string, first, last int) (map[int]*model.Channel, []dto.UpstreamDTO) {
	channels := map[int]*model.Channel{}
	upstreams := make([]dto.UpstreamDTO, 0, last-first+1)
	for id := first; id <= last; id++ {
		keys := make([]string, 0, priceMonitorOfficialMaxKeys)
		for k := 0; k < priceMonitorOfficialMaxKeys; k++ {
			keys = append(keys, fmt.Sprintf("sk-revoked-%d-%d", id, k))
		}
		channel := sub2apiChannel(id, url, strings.Join(keys, "\n"))
		channel.ChannelInfo.IsMultiKey = true
		channels[id] = channel
		upstreams = append(upstreams, dto.UpstreamDTO{ID: id, Name: "sub", BaseURL: url, Endpoint: sub2apiPricingEndpoint})
	}
	return channels, upstreams
}

func usePricingSourceChannels(t *testing.T, channels map[int]*model.Channel) {
	t.Helper()
	var mu sync.Mutex
	previous := pricingSourceChannel
	pricingSourceChannel = func(id int) (*model.Channel, error) {
		mu.Lock()
		defer mu.Unlock()
		channel, ok := channels[id]
		if !ok {
			return nil, fmt.Errorf("channel %d not found", id)
		}
		return channel, nil
	}
	t.Cleanup(func() { pricingSourceChannel = previous })
}

// L15: the empty-payload guard counts usable prices, not keys. A ratio_config answer whose
// model_ratio values are all null / non-numeric is a failed source; counting it as successful
// would show every one of its models as "missing" and count it as a price difference.
func TestBranchAuditPriceMonitorRejectsPayloadWithoutUsablePrices(t *testing.T) {
	for name, body := range map[string]string{
		"null ratios":        `{"success":true,"data":{"model_ratio":{"m":null}}}`,
		"non-numeric ratios": `{"success":true,"data":{"model_ratio":{"m":"abc"},"model_price":{"n":null}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			results, _ := fetchUpstreamPricingSources(context.Background(), []dto.UpstreamDTO{{Name: "fixture", BaseURL: server.URL, Endpoint: "/api/ratio_config"}}, 2)
			require.Len(t, results, 1)
			require.Equal(t, "error", results[0].Status, "a payload without a single usable price is not a successful source")
		})
	}
}

// L15: per-model coverage counts a model only when its value is a usable price (the predicate shared
// with pricingPayloadHasPrices). A source whose only matching model has a null price is not "OK";
// otherwise that model shows as missing and counts as a price difference.
func TestBranchAuditPriceMonitorCountsOnlyModelsWithUsablePrices(t *testing.T) {
	data := map[string]any{
		"model_ratio":                    map[string]any{"x": 1.0, "zero": 0.0, "null": nil, "text": "abc", "negative": -1.0, "inf": math.Inf(1), "nan": math.NaN()},
		"model_price":                    map[string]any{"p": 0.02, "q": nil},
		billing_setting.BillingExprField: map[string]any{"e": "p*1", "blank": "  ", "number": 1.0},
		"completion_ratio":               map[string]any{"c": 2.0},
	}
	require.Equal(t, map[string]struct{}{"x": {}, "zero": {}, "p": {}, "e": {}}, pricingPayloadModels(data))
	require.Equal(t, 4, countPricingPayloadModels(data))

	marketplace := map[string]struct{}{"m": {}}
	status := resolvePriceMonitorSourceStatus(pricingSource{name: "s", data: map[string]any{"model_ratio": map[string]any{"x": 1.0, "m": nil}}}, priceSourceOfficial, marketplace)
	require.Equal(t, priceMonitorSourceStatusNoOverlap, status.status, "m has no usable price, so the source does not cover the platform")
	require.Equal(t, 1, status.fetchedModels)
	require.Zero(t, status.matchedModels)

	status = resolvePriceMonitorSourceStatus(pricingSource{name: "s", data: map[string]any{"model_ratio": map[string]any{"m": nil}, "model_price": map[string]any{"m": 0.02}}}, priceSourceOfficial, marketplace)
	require.Equal(t, priceMonitorSourceStatusOK, status.status, "a usable price in any price field counts the model")
	require.Equal(t, 1, status.fetchedModels)
	require.Equal(t, 1, status.matchedModels)
}

// M9: the schedule compares wall-clock times. After the clock is set back (NTP), the last success and
// last attempt lie in the future; they must not postpone the next check by "jump + interval".
func TestBranchAuditPriceMonitorCheckDueIgnoresTimesAfterNow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	future := now.Add(3 * time.Hour).Unix()
	require.True(t, priceMonitorCheckDue(true, 360, future, 0, now), "a last success after now does not postpone the check")
	require.True(t, priceMonitorCheckDue(true, 360, 0, future, now), "nor does a last attempt after now")
	require.True(t, priceMonitorCheckDue(true, 360, future, future, now))
	require.False(t, priceMonitorCheckDue(true, 360, future, now.Unix()-60, now), "a valid recent attempt still postpones it")
	require.False(t, priceMonitorCheckDue(true, 360, now.Unix()-60, future, now), "and so does a valid recent success")

	attempt := now.Unix()
	require.False(t, priceMonitorCheckDue(true, 360, future, attempt, now.Add(time.Minute)), "after the re-run its attempt time governs")
	succeeded := now.Add(30 * time.Second).Unix()
	require.True(t, priceMonitorCheckDue(true, 360, succeeded, attempt, now.Add(360*time.Minute+30*time.Second)), "a successful re-run replaces the pre-jump success time")
	// If the re-run fails, the pre-jump success time becomes valid again once the clock passes it:
	// the next check waits for it once more, which bounds the extra delay by the jump itself.
	require.False(t, priceMonitorCheckDue(true, 360, future, attempt, now.Add(360*time.Minute)))
	require.True(t, priceMonitorCheckDue(true, 360, future, attempt, now.Add(3*time.Hour+360*time.Minute)))

	require.False(t, priceMonitorCheckDue(true, 5, now.Unix(), 0, now), "a time equal to now is valid")
	require.False(t, priceMonitorCheckDue(true, 5, now.Unix()-299, 0, now))
	require.True(t, priceMonitorCheckDue(true, 5, now.Unix()-300, 0, now))
}

// M9: end to end, a scheduler whose last attempt lies in the future (clock set back) runs a check on
// the next tick and records the new attempt time, after which the interval applies again.
func TestBranchAuditPriceMonitorRunsAfterBackwardClockJump(t *testing.T) {
	now := time.Now()
	isolatePriceMonitorSchedulerForAudit(t, PriceMonitorSnapshot{CheckedAt: now.Add(2 * time.Hour).Unix(), MatrixVersion: priceMonitorMatrixVersion, SourceHeaders: []PriceMonitorSourceHeader{{Key: priceMonitorPlatformKey}}})
	beforeJump := now.Add(2 * time.Hour).Unix()
	priceMonitorLastAttempt.Store(beforeJump)

	runPriceMonitorCheckIfDue(now)
	require.Eventually(t, func() bool { return !priceMonitorRunning.Load() }, 5*time.Second, 10*time.Millisecond)
	attempted := priceMonitorLastAttempt.Load()
	require.Less(t, attempted, beforeJump, "the check ran and recorded an attempt time on the adjusted clock")

	runPriceMonitorCheckIfDue(now.Add(time.Minute))
	require.Eventually(t, func() bool { return !priceMonitorRunning.Load() }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, attempted, priceMonitorLastAttempt.Load(), "the next tick waits for the interval again")
}
