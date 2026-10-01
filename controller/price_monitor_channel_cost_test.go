package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/price_monitor_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── status ──────────────────────────────────────────────────────────────────

// Any difference is reported; only floating-point noise counts as equal.
func TestPriceMonitorChannelCostStatus(t *testing.T) {
	ratio := func(v float64) *float64 { return &v }
	tenth, fifth := 0.1, 0.2
	cases := []struct {
		name     string
		upstream *float64
		cost     float64
		want     string
	}{
		{"no upstream ratio", nil, 1, priceMonitorCostStatusUnknown},
		{"free upstream group", ratio(0), 1, priceMonitorCostStatusFree},
		{"equal", ratio(0.8), 0.8, priceMonitorCostStatusMatch},
		{"floating-point noise is equal", ratio(0.3), tenth + fifth, priceMonitorCostStatusMatch},
		{"1% low is reported", ratio(0.8), 0.792, priceMonitorCostStatusLow},
		{"1% high is reported", ratio(0.8), 0.808, priceMonitorCostStatusHigh},
		{"a hundredth apart is reported", ratio(1.2), 1.2001, priceMonitorCostStatusHigh},
		{"cost ratio zero is low", ratio(0.5), 0, priceMonitorCostStatusLow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, deviation := priceMonitorChannelCostStatus(tc.upstream, tc.cost)
			assert.Equal(t, tc.want, status)
			if tc.upstream == nil || *tc.upstream == 0 {
				assert.Nil(t, deviation)
			} else {
				require.NotNil(t, deviation)
				assert.InDelta(t, tc.cost / *tc.upstream - 1, *deviation, 1e-12)
			}
		})
	}
}

func TestApplyPriceMonitorChannelCostStatusUsesCurrentCostRatio(t *testing.T) {
	ratio := 0.8
	costs := map[string]PriceMonitorChannelCost{"7": {ChannelId: 7, UpstreamRatio: &ratio}, "8": {ChannelId: 8}}
	applyPriceMonitorChannelCostStatus(costs, func(id int) float64 {
		if id == 7 {
			return 1
		}
		return 1
	}, map[int]bool{7: true})
	assert.Equal(t, priceMonitorCostStatusHigh, costs["7"].Status)
	assert.True(t, costs["7"].CostRatioConfigured)
	assert.Equal(t, priceMonitorCostStatusUnknown, costs["8"].Status)
	assert.False(t, costs["8"].CostRatioConfigured, "no config row: checked as 1.0 and marked")
	assert.Equal(t, 1, countPriceMonitorCostRatioMismatch(costs))
}

// ── scheduling and fallback ─────────────────────────────────────────────────

func costTestChannel(id int, baseURL string, withAccount bool) *model.Channel {
	channel := &model.Channel{Id: id, Name: "ch" + strconv.Itoa(id), Key: "sk-key" + strconv.Itoa(id), BaseURL: &baseURL, Type: constant.ChannelTypeOpenAI}
	if withAccount {
		channel.SetSetting(dto.ChannelSettings{AccountBalanceToken: "access-token", AccountBalanceUserID: "42"})
	}
	return channel
}

type fakeRatioFetcher struct {
	mu            sync.Mutex
	officialCalls []int
	sub2apiCalls  []int
	logCalls      []int
	official      map[int]priceMonitorRatioObservation
	// sub2api 缺省答"不是 sub2api"：这些用例的上游都是 new-api。
	sub2api map[int]priceMonitorRatioObservation
	log     map[int]priceMonitorRatioObservation
}

func (f *fakeRatioFetcher) fetcher() priceMonitorUpstreamRatioFetcher {
	return priceMonitorUpstreamRatioFetcher{
		official: func(_ context.Context, channel *model.Channel) priceMonitorRatioObservation {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.officialCalls = append(f.officialCalls, channel.Id)
			return f.official[channel.Id]
		},
		sub2api: func(_ context.Context, channel *model.Channel) priceMonitorRatioObservation {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.sub2apiCalls = append(f.sub2apiCalls, channel.Id)
			if observation, ok := f.sub2api[channel.Id]; ok {
				return observation
			}
			return priceMonitorRatioObservation{reason: priceMonitorRatioReasonSub2APIUnsupported}
		},
		log: func(_ context.Context, channel *model.Channel) priceMonitorRatioObservation {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.logCalls = append(f.logCalls, channel.Id)
			if observation, ok := f.log[channel.Id]; ok {
				return observation
			}
			return priceMonitorRatioObservation{reason: priceMonitorRatioReasonNoConsumeLog}
		},
	}
}

func costTestSetting(logsPerHost int) price_monitor_setting.PriceMonitorSetting {
	return price_monitor_setting.PriceMonitorSetting{
		UpstreamLogQueriesPerHost: logsPerHost,
		UpstreamRatioRefreshHours: 6,
		UpstreamRatioMaxAgeDays:   7,
	}
}

func sourceNamesFor(channels ...*model.Channel) map[int]string {
	names := map[int]string{}
	for _, channel := range channels {
		names[channel.Id] = channel.Name + "(" + strconv.Itoa(channel.Id) + ")"
	}
	return names
}

func TestResolveUpstreamRatiosOfficialFirst(t *testing.T) {
	channel := costTestChannel(1, "https://up.example", true)
	fake := &fakeRatioFetcher{official: map[int]priceMonitorRatioObservation{
		1: {ok: true, source: priceMonitorRatioSourceOfficial, group: "vip", ratio: 0.8},
	}}
	now := time.Unix(1_800_000_000, 0)
	got := resolvePriceMonitorUpstreamRatios(context.Background(), []*model.Channel{channel}, sourceNamesFor(channel), nil, costTestSetting(8), now, fake.fetcher())

	cost := got["1"]
	require.NotNil(t, cost.UpstreamRatio)
	assert.Equal(t, 0.8, *cost.UpstreamRatio)
	assert.Equal(t, "vip", cost.UpstreamGroup)
	assert.Equal(t, priceMonitorRatioSourceOfficial, cost.RatioSource)
	assert.Equal(t, now.Unix(), cost.ObservedAt)
	assert.Equal(t, "ch1(1)", cost.SourceKey)
	assert.Empty(t, fake.logCalls, "the log is a fallback, not used when the official API answers")
}

func TestResolveUpstreamRatiosFallsBackToLog(t *testing.T) {
	noAccount := costTestChannel(1, "https://up.example", false)
	officialFails := costTestChannel(2, "https://up.example", true)
	autoGroup := costTestChannel(3, "https://up.example", true)
	fake := &fakeRatioFetcher{
		official: map[int]priceMonitorRatioObservation{
			2: {reason: priceMonitorRatioReasonKeyNotInAccount},
			3: {reason: priceMonitorRatioReasonAutoGroup},
		},
		log: map[int]priceMonitorRatioObservation{
			1: {ok: true, source: priceMonitorRatioSourceLog, group: "default", ratio: 1},
			2: {ok: true, source: priceMonitorRatioSourceLog, group: "vip", ratio: 0.7},
			3: {ok: true, source: priceMonitorRatioSourceLog, group: "vip", ratio: 0.9},
		},
	}
	channels := []*model.Channel{noAccount, officialFails, autoGroup}
	got := resolvePriceMonitorUpstreamRatios(context.Background(), channels, sourceNamesFor(channels...), nil, costTestSetting(8), time.Unix(1_800_000_000, 0), fake.fetcher())

	assert.ElementsMatch(t, []int{2, 3}, fake.officialCalls, "no account token: the official API is not tried")
	for id, want := range map[string]float64{"1": 1, "2": 0.7, "3": 0.9} {
		require.NotNil(t, got[id].UpstreamRatio, id)
		assert.Equal(t, want, *got[id].UpstreamRatio, id)
		assert.Equal(t, priceMonitorRatioSourceLog, got[id].RatioSource, id)
	}
}

func TestResolveUpstreamRatiosReportsOfficialReasonWhenBothFail(t *testing.T) {
	channel := costTestChannel(1, "https://up.example", true)
	fake := &fakeRatioFetcher{official: map[int]priceMonitorRatioObservation{1: {reason: priceMonitorRatioReasonKeyNotInAccount}}}
	got := resolvePriceMonitorUpstreamRatios(context.Background(), []*model.Channel{channel}, sourceNamesFor(channel), nil, costTestSetting(8), time.Unix(1_800_000_000, 0), fake.fetcher())
	assert.Nil(t, got["1"].UpstreamRatio)
	assert.Equal(t, priceMonitorRatioReasonKeyNotInAccount, got["1"].Reason, "the official reason is more specific than 'no consume log'")
}

// The log endpoint is rate limited per IP, so each upstream host gets a budget
// per round; channels over the budget wait and go first next round.
func TestResolveUpstreamRatiosLogBudgetRotates(t *testing.T) {
	a := costTestChannel(1, "https://up.example", false)
	b := costTestChannel(2, "https://up.example", false)
	c := costTestChannel(3, "https://up.example", false)
	other := costTestChannel(4, "https://other.example", false)
	logOK := priceMonitorRatioObservation{ok: true, source: priceMonitorRatioSourceLog, group: "g", ratio: 1}
	fake := &fakeRatioFetcher{log: map[int]priceMonitorRatioObservation{1: logOK, 2: logOK, 3: logOK, 4: logOK}}
	channels := []*model.Channel{a, b, c, other}
	names := sourceNamesFor(channels...)
	now := time.Unix(1_800_000_000, 0)

	first := resolvePriceMonitorUpstreamRatios(context.Background(), channels, names, nil, costTestSetting(2), now, fake.fetcher())
	assert.Len(t, fake.logCalls, 3, "two on the shared host, one on the other host")
	waiting := 0
	for _, id := range []string{"1", "2", "3"} {
		if first[id].Reason == priceMonitorRatioReasonWaiting {
			waiting++
			assert.Zero(t, first[id].AttemptedAt, "a waiting channel is not counted as attempted")
		}
	}
	require.Equal(t, 1, waiting)

	// Next round, past the refresh interval for nobody but the waiting one: it goes first.
	fake.logCalls = nil
	resolvePriceMonitorUpstreamRatios(context.Background(), channels, names, first, costTestSetting(2), now.Add(time.Minute), fake.fetcher())
	require.Len(t, fake.logCalls, 1, "only the channel that waited is due")
}

func TestResolveUpstreamRatiosRateLimitStopsTheHost(t *testing.T) {
	a := costTestChannel(1, "https://up.example", false)
	b := costTestChannel(2, "https://up.example", false)
	fake := &fakeRatioFetcher{log: map[int]priceMonitorRatioObservation{
		1: {reason: priceMonitorRatioReasonRateLimited},
		2: {reason: priceMonitorRatioReasonRateLimited},
	}}
	channels := []*model.Channel{a, b}
	got := resolvePriceMonitorUpstreamRatios(context.Background(), channels, sourceNamesFor(channels...), nil, costTestSetting(8), time.Unix(1_800_000_000, 0), fake.fetcher())
	assert.Len(t, fake.logCalls, 1, "after a 429 the host is not queried again this round")
	reasons := []string{got["1"].Reason, got["2"].Reason}
	assert.ElementsMatch(t, []string{priceMonitorRatioReasonRateLimited, priceMonitorRatioReasonWaiting}, reasons)
}

func TestResolveUpstreamRatiosRefreshIntervalAndMaxAge(t *testing.T) {
	channel := costTestChannel(1, "https://up.example", true)
	names := sourceNamesFor(channel)
	now := time.Unix(1_800_000_000, 0)
	ratio := 0.8
	fake := &fakeRatioFetcher{official: map[int]priceMonitorRatioObservation{1: {reason: priceMonitorRatioReasonUnavailable}}}

	recent := map[string]PriceMonitorChannelCost{"1": {ChannelId: 1, UpstreamRatio: &ratio, UpstreamGroup: "vip", RatioSource: priceMonitorRatioSourceOfficial, ObservedAt: now.Add(-time.Hour).Unix(), AttemptedAt: now.Add(-time.Hour).Unix()}}
	got := resolvePriceMonitorUpstreamRatios(context.Background(), []*model.Channel{channel}, names, recent, costTestSetting(8), now, fake.fetcher())
	assert.Empty(t, fake.officialCalls, "within the refresh interval nothing is fetched")
	require.NotNil(t, got["1"].UpstreamRatio)

	// Due for refresh, the fetch fails: the unexpired observation is kept, with the reason.
	stale := map[string]PriceMonitorChannelCost{"1": {ChannelId: 1, UpstreamRatio: &ratio, ObservedAt: now.Add(-48 * time.Hour).Unix(), AttemptedAt: now.Add(-7 * time.Hour).Unix()}}
	got = resolvePriceMonitorUpstreamRatios(context.Background(), []*model.Channel{channel}, names, stale, costTestSetting(8), now, fake.fetcher())
	require.NotNil(t, got["1"].UpstreamRatio)
	assert.Equal(t, priceMonitorRatioReasonUnavailable, got["1"].Reason)
	assert.Equal(t, now.Unix(), got["1"].AttemptedAt)

	// Past the max age, the old observation is dropped.
	expired := map[string]PriceMonitorChannelCost{"1": {ChannelId: 1, UpstreamRatio: &ratio, ObservedAt: now.Add(-8 * 24 * time.Hour).Unix(), AttemptedAt: now.Add(-8 * 24 * time.Hour).Unix()}}
	got = resolvePriceMonitorUpstreamRatios(context.Background(), []*model.Channel{channel}, names, expired, costTestSetting(8), now, fake.fetcher())
	assert.Nil(t, got["1"].UpstreamRatio)
}

// A channel whose ID stays but whose upstream or key changed must not keep the
// old upstream's ratio: it is dropped and refetched at once, inside the refresh
// interval. Each input that decides which upstream ratio is read counts.
func TestResolveUpstreamRatiosRefetchesWhenChannelSourceChanges(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	oldRatio := 0.1
	changes := map[string]func(*model.Channel){
		"base url": func(ch *model.Channel) {
			url := "https://changed.example"
			ch.BaseURL = &url
		},
		"key":  func(ch *model.Channel) { ch.Key = "sk-replaced" },
		"type": func(ch *model.Channel) { ch.Type = constant.ChannelTypeSub2API },
		"account token": func(ch *model.Channel) {
			ch.SetSetting(dto.ChannelSettings{AccountBalanceToken: "other-token", AccountBalanceUserID: "42"})
		},
		"account user": func(ch *model.Channel) {
			ch.SetSetting(dto.ChannelSettings{AccountBalanceToken: "access-token", AccountBalanceUserID: "43"})
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			before := costTestChannel(1, "https://up.example", true)
			previous := map[string]PriceMonitorChannelCost{"1": {
				ChannelId: 1, UpstreamRatio: &oldRatio, UpstreamGroup: "old", RatioSource: priceMonitorRatioSourceOfficial,
				UpstreamKind: priceMonitorUpstreamKindNewAPI, ObservedAt: now.Add(-time.Hour).Unix(), AttemptedAt: now.Add(-time.Hour).Unix(),
				SourceFingerprint: priceMonitorRatioSourceFingerprint(before),
			}}
			after := costTestChannel(1, "https://up.example", true)
			change(after)
			fresh := priceMonitorRatioObservation{ok: true, source: priceMonitorRatioSourceOfficial, group: "new", ratio: 3}
			fake := &fakeRatioFetcher{
				official: map[int]priceMonitorRatioObservation{1: fresh},
				sub2api:  map[int]priceMonitorRatioObservation{1: fresh},
			}

			got := resolvePriceMonitorUpstreamRatios(context.Background(), []*model.Channel{after}, sourceNamesFor(after), previous, costTestSetting(8), now, fake.fetcher())

			assert.NotEmpty(t, append(fake.officialCalls, fake.sub2apiCalls...), "a changed channel is refetched inside the refresh interval")
			require.NotNil(t, got["1"].UpstreamRatio)
			assert.Equal(t, 3.0, *got["1"].UpstreamRatio)
			assert.Equal(t, "new", got["1"].UpstreamGroup)
			assert.Equal(t, priceMonitorRatioSourceFingerprint(after), got["1"].SourceFingerprint)
		})
	}
}

// When the refetch after a change yields nothing, the old upstream's ratio is
// not kept as a fallback: it belongs to a different upstream.
func TestResolveUpstreamRatiosChangedChannelDropsOldRatioOnFailure(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	oldRatio := 0.1
	before := costTestChannel(1, "https://up.example", true)
	previous := map[string]PriceMonitorChannelCost{"1": {
		ChannelId: 1, UpstreamRatio: &oldRatio, ObservedAt: now.Add(-time.Hour).Unix(), AttemptedAt: now.Add(-time.Hour).Unix(),
		UpstreamKind: priceMonitorUpstreamKindNewAPI, SourceFingerprint: priceMonitorRatioSourceFingerprint(before),
	}}
	after := costTestChannel(1, "https://changed.example", true)
	fake := &fakeRatioFetcher{official: map[int]priceMonitorRatioObservation{1: {reason: priceMonitorRatioReasonUnavailable}}}

	got := resolvePriceMonitorUpstreamRatios(context.Background(), []*model.Channel{after}, sourceNamesFor(after), previous, costTestSetting(8), now, fake.fetcher())

	assert.Nil(t, got["1"].UpstreamRatio)
	assert.Equal(t, priceMonitorRatioReasonUnavailable, got["1"].Reason)
	assert.NotEqual(t, priceMonitorUpstreamKindNewAPI, got["1"].UpstreamKind, "the old upstream's gateway kind is forgotten too")
}

// An unchanged channel keeps its ratio through the refresh interval; so does
// a snapshot written before fingerprints existed, which adopts the current one.
func TestResolveUpstreamRatiosUnchangedOrLegacyChannelKeepsRatio(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	ratio := 0.8
	channel := costTestChannel(1, "https://up.example", true)
	for name, fingerprint := range map[string]string{"unchanged": priceMonitorRatioSourceFingerprint(channel), "legacy": ""} {
		t.Run(name, func(t *testing.T) {
			previous := map[string]PriceMonitorChannelCost{"1": {
				ChannelId: 1, UpstreamRatio: &ratio, ObservedAt: now.Add(-time.Hour).Unix(), AttemptedAt: now.Add(-time.Hour).Unix(),
				SourceFingerprint: fingerprint,
			}}
			fake := &fakeRatioFetcher{}
			got := resolvePriceMonitorUpstreamRatios(context.Background(), []*model.Channel{channel}, sourceNamesFor(channel), previous, costTestSetting(8), now, fake.fetcher())
			assert.Empty(t, fake.officialCalls)
			require.NotNil(t, got["1"].UpstreamRatio)
			assert.Equal(t, 0.8, *got["1"].UpstreamRatio)
			assert.Equal(t, priceMonitorRatioSourceFingerprint(channel), got["1"].SourceFingerprint)
		})
	}
}

// A multi-key channel auto-disabling one key is routine; it must not look like
// a changed source and throw the ratio away. The fingerprint never carries the
// key itself.
func TestPriceMonitorRatioSourceFingerprint(t *testing.T) {
	channel := costTestChannel(1, "https://up.example", true)
	channel.Key = "sk-a\nsk-b"
	channel.ChannelInfo.IsMultiKey = true
	base := priceMonitorRatioSourceFingerprint(channel)
	channel.ChannelInfo.MultiKeyStatusList = map[int]int{1: common.ChannelStatusAutoDisabled}
	assert.Equal(t, base, priceMonitorRatioSourceFingerprint(channel))

	padded := costTestChannel(1, "  https://up.example ", true)
	padded.Key = channel.Key
	assert.Equal(t, base, priceMonitorRatioSourceFingerprint(padded), "surrounding whitespace in the URL is not a change")

	assert.NotContains(t, base, "sk-")
	assert.Len(t, base, 16)
}

// When the step budget runs out, channels not yet reached make no request and
// keep the previous observation; they are not marked attempted, so they go
// first next round.
func TestResolveUpstreamRatiosStopsWhenBudgetExhausted(t *testing.T) {
	channel := costTestChannel(1, "https://up.example", true)
	now := time.Unix(1_800_000_000, 0)
	ratio := 0.8
	attempted := now.Add(-7 * time.Hour).Unix()
	previous := map[string]PriceMonitorChannelCost{"1": {ChannelId: 1, UpstreamRatio: &ratio, UpstreamGroup: "vip", ObservedAt: attempted, AttemptedAt: attempted}}
	fake := &fakeRatioFetcher{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := resolvePriceMonitorUpstreamRatios(ctx, []*model.Channel{channel}, sourceNamesFor(channel), previous, costTestSetting(8), now, fake.fetcher())
	assert.Empty(t, fake.officialCalls)
	assert.Empty(t, fake.logCalls)
	require.NotNil(t, got["1"].UpstreamRatio)
	assert.Equal(t, 0.8, *got["1"].UpstreamRatio)
	assert.Equal(t, attempted, got["1"].AttemptedAt)
}

// A request cut off by the budget is not the upstream's fault: its failure is
// discarded, the previous observation kept, and the channel stays due.
func TestResolveUpstreamRatiosDiscardsRequestCutByBudget(t *testing.T) {
	channel := costTestChannel(1, "https://up.example", true)
	now := time.Unix(1_800_000_000, 0)
	ratio := 0.8
	attempted := now.Add(-7 * time.Hour).Unix()
	previous := map[string]PriceMonitorChannelCost{"1": {ChannelId: 1, UpstreamRatio: &ratio, UpstreamGroup: "vip", ObservedAt: attempted, AttemptedAt: attempted}}
	ctx, cancel := context.WithCancel(context.Background())
	fetcher := priceMonitorUpstreamRatioFetcher{
		official: func(context.Context, *model.Channel) priceMonitorRatioObservation {
			cancel() // the budget runs out while this request is in flight
			return priceMonitorRatioObservation{reason: priceMonitorRatioReasonUnavailable}
		},
		sub2api: func(context.Context, *model.Channel) priceMonitorRatioObservation {
			return priceMonitorRatioObservation{reason: priceMonitorRatioReasonSub2APIUnsupported}
		},
		log: func(context.Context, *model.Channel) priceMonitorRatioObservation {
			return priceMonitorRatioObservation{reason: priceMonitorRatioReasonUnavailable}
		},
	}
	got := resolvePriceMonitorUpstreamRatios(ctx, []*model.Channel{channel}, sourceNamesFor(channel), previous, costTestSetting(8), now, fetcher)
	assert.Empty(t, got["1"].Reason)
	assert.Equal(t, attempted, got["1"].AttemptedAt)
	require.NotNil(t, got["1"].UpstreamRatio)
}

func TestResolveUpstreamRatiosOpenRouterUnsupported(t *testing.T) {
	channel := costTestChannel(1, "https://openrouter.ai/api", true)
	channel.Type = constant.ChannelTypeOpenRouter
	fake := &fakeRatioFetcher{}
	got := resolvePriceMonitorUpstreamRatios(context.Background(), []*model.Channel{channel}, sourceNamesFor(channel), nil, costTestSetting(8), time.Unix(1_800_000_000, 0), fake.fetcher())
	assert.Equal(t, priceMonitorRatioReasonUnsupportedSource, got["1"].Reason)
	assert.Empty(t, fake.officialCalls)
	assert.Empty(t, fake.logCalls)
}

// ── official API ────────────────────────────────────────────────────────────

// fakeNewAPI mimics an official new-api upstream: the token list returns masked
// keys (first 4 + ten stars + last 4), /api/token/search matches a full key.
// fullKeys mimics versions before 2026-03, whose list returned the whole key;
// zeroTotal mimics an upstream whose count query failed (total 0 on full pages).
type fakeNewAPI struct {
	mu          sync.Mutex
	tokens      map[string]string // raw key (no sk-) -> group
	userGroup   string
	groups      string // raw JSON of /api/user/self/groups data
	status      int    // non-zero: every request answers with it
	hang        bool   // never answer
	fullKeys    bool
	zeroTotal   bool
	pageCap     int // non-zero: page_size is capped at this, like a fork with a lower limit
	listCalls   int
	searchCalls int
	seenAuth    []string
	seenUser    []string
}

func (f *fakeNewAPI) server(t *testing.T) *httptest.Server {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.seenAuth = append(f.seenAuth, r.Header.Get("Authorization"))
		f.seenUser = append(f.seenUser, r.Header.Get("New-Api-User"))
		f.mu.Unlock()
		if f.hang {
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		switch r.URL.Path {
		case "/api/token/":
			f.mu.Lock()
			f.listCalls++
			f.mu.Unlock()
			keys := make([]string, 0, len(f.tokens))
			for key := range f.tokens {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			page, _ := strconv.Atoi(r.URL.Query().Get("p"))
			size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
			if f.pageCap > 0 && size > f.pageCap {
				size = f.pageCap
			}
			start, end := min((page-1)*size, len(keys)), min(page*size, len(keys))
			items := make([]string, 0, end-start)
			for _, key := range keys[start:end] {
				shown := priceMonitorMaskTokenKey(key)
				if f.fullKeys {
					shown = key
				}
				items = append(items, `{"key":"`+shown+`","group":"`+f.tokens[key]+`"}`)
			}
			total := len(keys)
			if f.zeroTotal {
				total = 0
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"page":` + strconv.Itoa(page) + `,"page_size":` + strconv.Itoa(size) + `,"total":` + strconv.Itoa(total) + `,"items":[` + strings.Join(items, ",") + `]}}`))
		case "/api/token/search":
			f.mu.Lock()
			f.searchCalls++
			f.mu.Unlock()
			group, ok := f.tokens[r.URL.Query().Get("token")]
			if !ok {
				_, _ = w.Write([]byte(`{"success":true,"data":{"items":[],"total":0}}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"key":"x","group":"` + group + `"}],"total":1}}`))
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"group":"` + f.userGroup + `"}}`))
		case "/api/user/self/groups":
			_, _ = w.Write([]byte(`{"success":true,"data":` + f.groups + `}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(func() { close(release); server.Close() })
	return server
}

func officialTestChannel(id int, baseURL, key string) *model.Channel {
	channel := costTestChannel(id, baseURL, true)
	channel.Key = key
	return channel
}

const testUserGroups = `{"default":{"ratio":1,"desc":"d"},"vip":{"ratio":0.8,"desc":"v"},"svip":{"ratio":1.5,"desc":"s"},"auto":{"ratio":"自动","desc":"a"}}`

func officialFetch(channel *model.Channel) priceMonitorRatioObservation {
	return newPriceMonitorUpstreamRatioFetcher(2*time.Second, nil).official(context.Background(), channel)
}

func TestFetchOfficialGroupRatio(t *testing.T) {
	const keyA, keyB = "abcd1111111111111111wxyz", "efgh2222222222222222stuv"

	t.Run("token group and its ratio, with the account headers", func(t *testing.T) {
		api := &fakeNewAPI{tokens: map[string]string{keyA: "vip"}, groups: testUserGroups}
		got := officialFetch(officialTestChannel(9, api.server(t).URL, "sk-"+keyA))
		require.True(t, got.ok, got.reason)
		assert.Equal(t, "vip", got.group)
		assert.Equal(t, 0.8, got.ratio)
		assert.Equal(t, priceMonitorRatioSourceOfficial, got.source)
		assert.Zero(t, api.searchCalls, "the token list is enough; the rate-limited search is not used")
		for i := range api.seenAuth {
			assert.Equal(t, "Bearer access-token", api.seenAuth[i])
			assert.Equal(t, "42", api.seenUser[i])
		}
	})

	t.Run("empty token group follows the account group", func(t *testing.T) {
		api := &fakeNewAPI{tokens: map[string]string{keyA: ""}, userGroup: "default", groups: testUserGroups}
		got := officialFetch(officialTestChannel(9, api.server(t).URL, keyA))
		require.True(t, got.ok, got.reason)
		assert.Equal(t, "default", got.group)
		assert.Equal(t, 1.0, got.ratio)
	})

	t.Run("keys in different groups: the highest ratio, all groups listed", func(t *testing.T) {
		api := &fakeNewAPI{tokens: map[string]string{keyA: "vip", keyB: "svip"}, groups: testUserGroups}
		got := officialFetch(officialTestChannel(9, api.server(t).URL, "sk-"+keyA+"\nsk-"+keyB))
		require.True(t, got.ok, got.reason)
		assert.Equal(t, "svip", got.group)
		assert.Equal(t, 1.5, got.ratio)
		assert.Equal(t, []string{"svip", "vip"}, got.groups)
	})

	t.Run("two tokens share a masked form: the exact search decides", func(t *testing.T) {
		twin := "abcd9999999999999999wxyz" // same first 4 and last 4 as keyA
		api := &fakeNewAPI{tokens: map[string]string{keyA: "vip", twin: "svip"}, groups: testUserGroups}
		got := officialFetch(officialTestChannel(9, api.server(t).URL, "sk-"+twin))
		require.True(t, got.ok, got.reason)
		assert.Equal(t, "svip", got.group)
		assert.Equal(t, 1, api.searchCalls)
	})

	t.Run("a key deleted upstream is skipped; the others decide", func(t *testing.T) {
		api := &fakeNewAPI{tokens: map[string]string{keyB: "svip"}, groups: testUserGroups}
		got := officialFetch(officialTestChannel(9, api.server(t).URL, "sk-"+keyA+"\nsk-"+keyB))
		require.True(t, got.ok, got.reason)
		assert.Equal(t, "svip", got.group)
	})

	t.Run("a disabled key of a multi-key channel is not looked up", func(t *testing.T) {
		api := &fakeNewAPI{tokens: map[string]string{keyA: "svip", keyB: "vip"}, groups: testUserGroups}
		channel := officialTestChannel(9, api.server(t).URL, "sk-"+keyA+"\nsk-"+keyB)
		channel.ChannelInfo.IsMultiKey = true
		channel.ChannelInfo.MultiKeyStatusList = map[int]int{0: common.ChannelStatusAutoDisabled}
		got := officialFetch(channel)
		require.True(t, got.ok, got.reason)
		assert.Equal(t, "vip", got.group, "the disabled svip key does not raise the ratio")
		assert.Nil(t, got.groups)
	})

	t.Run("an older upstream lists full keys", func(t *testing.T) {
		api := &fakeNewAPI{tokens: map[string]string{keyA: "vip", keyB: "svip"}, groups: testUserGroups, fullKeys: true}
		got := officialFetch(officialTestChannel(9, api.server(t).URL, "sk-"+keyB))
		require.True(t, got.ok, got.reason)
		assert.Equal(t, "svip", got.group)
		assert.Zero(t, api.searchCalls)
	})

	failures := map[string]struct {
		api    *fakeNewAPI
		key    string
		reason string
	}{
		"key not in the account": {&fakeNewAPI{tokens: map[string]string{keyA: "vip"}, groups: testUserGroups}, "sk-" + keyB, priceMonitorRatioReasonKeyNotInAccount},
		"auto group":             {&fakeNewAPI{tokens: map[string]string{keyA: "auto"}, groups: testUserGroups}, "sk-" + keyA, priceMonitorRatioReasonAutoGroup},
		"group not usable":       {&fakeNewAPI{tokens: map[string]string{keyA: "hidden"}, groups: testUserGroups}, "sk-" + keyA, priceMonitorRatioReasonGroupMissing},
		"rejected token":         {&fakeNewAPI{status: http.StatusUnauthorized}, "sk-" + keyA, priceMonitorRatioReasonRejected},
		"rate limited":           {&fakeNewAPI{status: http.StatusTooManyRequests}, "sk-" + keyA, priceMonitorRatioReasonRateLimited},
		"not a new-api upstream": {&fakeNewAPI{status: http.StatusNotFound}, "sk-" + keyA, priceMonitorRatioReasonNotSupported},
		"server error":           {&fakeNewAPI{status: http.StatusBadGateway}, "sk-" + keyA, priceMonitorRatioReasonUnavailable},
	}
	for name, tc := range failures {
		t.Run(name, func(t *testing.T) {
			got := officialFetch(officialTestChannel(9, tc.api.server(t).URL, tc.key))
			assert.False(t, got.ok)
			assert.Equal(t, tc.reason, got.reason)
		})
	}
}

// Channels on one upstream account share one token list and one group table per
// round, and a failure is not retried for every channel.
func TestFetchOfficialLoadsEachAccountOnce(t *testing.T) {
	keys := []string{"aaaa1111111111111111aaaa", "bbbb2222222222222222bbbb", "cccc3333333333333333cccc"}
	api := &fakeNewAPI{tokens: map[string]string{keys[0]: "vip", keys[1]: "default", keys[2]: "svip"}, groups: testUserGroups}
	baseURL := api.server(t).URL
	fetcher := newPriceMonitorUpstreamRatioFetcher(2*time.Second, nil)
	for i, key := range keys {
		got := fetcher.official(context.Background(), officialTestChannel(i+1, baseURL, "sk-"+key))
		require.True(t, got.ok, got.reason)
	}
	assert.Equal(t, 1, api.listCalls)

	limited := &fakeNewAPI{status: http.StatusTooManyRequests}
	limitedURL := limited.server(t).URL
	fetcher = newPriceMonitorUpstreamRatioFetcher(2*time.Second, nil)
	for i, key := range keys {
		got := fetcher.official(context.Background(), officialTestChannel(i+1, limitedURL, "sk-"+key))
		assert.Equal(t, priceMonitorRatioReasonRateLimited, got.reason)
	}
	assert.Len(t, limited.seenAuth, 1, "one rejected request, not one per channel")
}

// A total of 0 is not proof the list is complete: a full page means read on,
// and a key on the second page is still found without the rate-limited search.
func TestFetchOfficialReadsOnWhenTotalIsZero(t *testing.T) {
	tokens := map[string]string{}
	for i := 0; i < priceMonitorTokenPageSize+20; i++ {
		tokens[fmt.Sprintf("k%03d1111111111111111%04d", i, i)] = "default"
	}
	last := fmt.Sprintf("k%03d1111111111111111%04d", priceMonitorTokenPageSize+19, priceMonitorTokenPageSize+19)
	tokens[last] = "vip"
	api := &fakeNewAPI{tokens: tokens, groups: testUserGroups, zeroTotal: true}
	got := officialFetch(officialTestChannel(9, api.server(t).URL, "sk-"+last))
	require.True(t, got.ok, got.reason)
	assert.Equal(t, "vip", got.group)
	assert.Equal(t, 2, api.listCalls)
	assert.Zero(t, api.searchCalls)

	missing := officialFetch(officialTestChannel(9, api.server(t).URL, "sk-zzzz9999999999999999zzzz"))
	assert.Equal(t, priceMonitorRatioReasonKeyNotInAccount, missing.reason, "after a short page the list is complete")
}

// A positive total is trusted over the page length: an upstream whose page size
// is capped below 100 returns short pages that are not the last one.
func TestFetchOfficialReadsOnWhenPagesAreCapped(t *testing.T) {
	tokens := map[string]string{}
	for i := 0; i < 30; i++ {
		tokens[fmt.Sprintf("k%03d1111111111111111%04d", i, i)] = "default"
	}
	last := fmt.Sprintf("k%03d1111111111111111%04d", 29, 29)
	tokens[last] = "vip"
	api := &fakeNewAPI{tokens: tokens, groups: testUserGroups, pageCap: 10}
	got := officialFetch(officialTestChannel(9, api.server(t).URL, "sk-"+last))
	require.True(t, got.ok, got.reason)
	assert.Equal(t, "vip", got.group)
	assert.Equal(t, 3, api.listCalls)
	assert.Zero(t, api.searchCalls)
}

// An upstream that never answers must not stall the check: every request is
// bounded by the source timeout.
func TestFetchOfficialTimesOut(t *testing.T) {
	api := &fakeNewAPI{hang: true}
	fetcher := newPriceMonitorUpstreamRatioFetcher(200*time.Millisecond, nil)
	started := time.Now()
	got := fetcher.official(context.Background(), officialTestChannel(9, api.server(t).URL, "sk-abcd1111111111111111wxyz"))
	assert.Less(t, time.Since(started), 2*time.Second)
	assert.Equal(t, priceMonitorRatioReasonUnavailable, got.reason)
}

// The balance query requires both the token and the user ID; so does this.
func TestAccountCredentialNeedsTokenAndUser(t *testing.T) {
	channel := costTestChannel(1, "https://up.example", false)
	channel.SetSetting(dto.ChannelSettings{AccountBalanceToken: "access-token"})
	assert.False(t, priceMonitorHasAccountCredential(channel))
	channel.SetSetting(dto.ChannelSettings{AccountBalanceToken: "access-token", AccountBalanceUserID: "1"})
	assert.True(t, priceMonitorHasAccountCredential(channel))
}

// ── log fallback ────────────────────────────────────────────────────────────

func logServer(t *testing.T, status int, body string) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/log/token" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		assert.Equal(t, "Bearer sk-key9", r.Header.Get("Authorization"))
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func logEntry(id, createdAt, logType int, group, other string) string {
	return `{"id":` + strconv.Itoa(id) + `,"type":` + strconv.Itoa(logType) + `,"created_at":` + strconv.Itoa(createdAt) +
		`,"group":"` + group + `","other":"` + strings.ReplaceAll(other, `"`, `\"`) + `"}`
}

func TestFetchLogGroupRatio(t *testing.T) {
	t.Run("newest consume log wins; user_group_ratio overrides group_ratio", func(t *testing.T) {
		body := `{"success":true,"data":[` +
			logEntry(1, 100, 2, "old", `{"group_ratio":1}`) + `,` +
			logEntry(2, 300, 2, "vip", `{"group_ratio":1,"user_group_ratio":0.6}`) + `,` +
			logEntry(3, 400, 5, "err", `{"group_ratio":9}`) + `]}`
		got := fetchPriceMonitorLogGroupRatio(context.Background(), costTestChannel(9, logServer(t, 0, body).URL, false))
		require.True(t, got.ok)
		assert.Equal(t, "vip", got.group)
		assert.Equal(t, 0.6, got.ratio)
		assert.Equal(t, priceMonitorRatioSourceLog, got.source)
	})

	t.Run("user_group_ratio of -1 means none", func(t *testing.T) {
		body := `{"success":true,"data":[` + logEntry(1, 100, 2, "vip", `{"group_ratio":0.8,"user_group_ratio":-1}`) + `]}`
		got := fetchPriceMonitorLogGroupRatio(context.Background(), costTestChannel(9, logServer(t, 0, body).URL, false))
		require.True(t, got.ok)
		assert.Equal(t, 0.8, got.ratio)
	})

	t.Run("no consume log with a ratio", func(t *testing.T) {
		body := `{"success":true,"data":[` + logEntry(1, 100, 2, "vip", `{"model_ratio":1}`) + `]}`
		got := fetchPriceMonitorLogGroupRatio(context.Background(), costTestChannel(9, logServer(t, 0, body).URL, false))
		assert.False(t, got.ok)
		assert.Equal(t, priceMonitorRatioReasonNoConsumeLog, got.reason)
	})

	t.Run("a disabled first key is not used", func(t *testing.T) {
		body := `{"success":true,"data":[` + logEntry(1, 100, 2, "vip", `{"group_ratio":0.8}`) + `]}`
		channel := costTestChannel(9, logServer(t, 0, body).URL, false)
		channel.Key = "sk-dead\nsk-key9"
		channel.ChannelInfo.IsMultiKey = true
		channel.ChannelInfo.MultiKeyStatusList = map[int]int{0: common.ChannelStatusManuallyDisabled}
		got := fetchPriceMonitorLogGroupRatio(context.Background(), channel)
		require.True(t, got.ok, got.reason)
	})

	t.Run("rate limited", func(t *testing.T) {
		got := fetchPriceMonitorLogGroupRatio(context.Background(), costTestChannel(9, logServer(t, http.StatusTooManyRequests, "").URL, false))
		assert.False(t, got.ok)
		assert.Equal(t, priceMonitorRatioReasonRateLimited, got.reason)
	})
}

// With the log fallback turned off, a channel without account credentials has no
// source at all; it is not "waiting" for a turn that never comes.
func TestResolveUpstreamRatiosLogsDisabledIsNoSource(t *testing.T) {
	channel := costTestChannel(1, "https://up.example", false)
	fake := &fakeRatioFetcher{}
	got := resolvePriceMonitorUpstreamRatios(context.Background(), []*model.Channel{channel}, sourceNamesFor(channel), nil, costTestSetting(0), time.Unix(1_800_000_000, 0), fake.fetcher())
	assert.Equal(t, priceMonitorRatioReasonNoSource, got["1"].Reason)
	assert.NotZero(t, got["1"].AttemptedAt)
	assert.Empty(t, fake.logCalls)
}

// Workers for different hosts run concurrently and write results under a lock;
// nothing may read the shared result map outside it (the runtime aborts on a
// concurrent map read and write, even without -race).
func TestResolveUpstreamRatiosConcurrentHosts(t *testing.T) {
	channels := make([]*model.Channel, 0, 120)
	for host := 0; host < 40; host++ {
		for i := 0; i < 3; i++ {
			channels = append(channels, costTestChannel(host*10+i+1, "https://h"+strconv.Itoa(host)+".example", i%2 == 0))
		}
	}
	names := sourceNamesFor(channels...)
	slow := priceMonitorUpstreamRatioFetcher{
		official: func(context.Context, *model.Channel) priceMonitorRatioObservation {
			time.Sleep(time.Millisecond)
			return priceMonitorRatioObservation{ok: true, source: priceMonitorRatioSourceOfficial, group: "g", ratio: 1}
		},
		sub2api: func(context.Context, *model.Channel) priceMonitorRatioObservation {
			time.Sleep(time.Millisecond)
			return priceMonitorRatioObservation{reason: priceMonitorRatioReasonSub2APIUnsupported}
		},
		log: func(context.Context, *model.Channel) priceMonitorRatioObservation {
			time.Sleep(time.Millisecond)
			return priceMonitorRatioObservation{ok: true, source: priceMonitorRatioSourceLog, group: "g", ratio: 1}
		},
	}
	var previous map[string]PriceMonitorChannelCost
	now := time.Unix(1_800_000_000, 0)
	for round := 0; round < 5; round++ {
		now = now.Add(7 * time.Hour) // every round is past the refresh interval
		previous = resolvePriceMonitorUpstreamRatios(context.Background(), channels, names, previous, costTestSetting(8), now, slow)
		require.Len(t, previous, len(channels))
	}
	for _, cost := range previous {
		require.NotNil(t, cost.UpstreamRatio)
	}
}
