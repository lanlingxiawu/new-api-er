package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The price source may be requested with a client-supplied address (the "sync
// upstream ratio" dialog). The channel key only ever goes to the channel's own
// address; a different address is refused before any request.
func TestFetchSub2APIPlazaRefusesForeignAddress(t *testing.T) {
	attacker := &fakeSub2API{plaza: plazaTwoGroups, billing: map[string]string{"sk-secret": sub2apiBilling(1, 1, "")}}
	attackerURL := attacker.server(t).URL
	withSub2APIChannelLoader(t, sub2apiChannel(7, "https://real.example", "sk-secret"))
	result := fetchSub2APISource(t, attackerURL)
	assert.NotEqual(t, "success", result.Status)
	assert.Empty(t, attacker.billingCalls, "the key is not sent")
	assert.Zero(t, attacker.plazaCalls, "the foreign address is not contacted at all")
	assert.NotContains(t, result.Error, "sk-secret")
}

// A sub2api probe that finds no sub2api must not replace the reason and endpoint
// of the new-api candidates tried before it.
func TestPriceMonitorSourcesKeepNewAPIFailureWhenNotSub2API(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	withSub2APIChannelLoader(t, sub2apiChannel(7, server.URL, "sk-a"))
	upstream := dto.UpstreamDTO{ID: 7, Name: "ch", BaseURL: server.URL}
	outcomes := resolvePriceMonitorSources(context.Background(), []priceMonitorSourcePlan{{
		upstream:   upstream,
		candidates: priceMonitorEndpointCandidates(constant.ChannelTypeOpenAI, "", ""),
	}}, 5)
	outcome := outcomes[pricingSourceDisplayName(upstream)]
	require.NotNil(t, outcome)
	assert.False(t, outcome.ok)
	assert.Equal(t, "/api/ratio_config", outcome.endpoint)
	assert.False(t, strings.HasPrefix(outcome.failure, sub2apiNotDetectedPrefix))
}

// A sub2api-shaped refusal on a channel of another type is still an answer from
// sub2api: it is reported (not replaced by the new-api log's "not supported")
// and remembered, so the next round asks sub2api directly.
func TestSub2APIRefusalIsReportedAndRemembered(t *testing.T) {
	api := &fakeSub2API{billingCode: http.StatusForbidden}
	channel := costTestChannel(1, api.server(t).URL, false)
	channel.Key = "sk-a"
	got := fetchPriceMonitorSub2APIRatio(context.Background(), 2*time.Second, channel)
	assert.Equal(t, priceMonitorRatioReasonSub2APINoGroup, got.reason)
	assert.Equal(t, priceMonitorUpstreamKindSub2API, got.kind)

	fake := &orderFetcher{sub2api: got, log: priceMonitorRatioObservation{reason: priceMonitorRatioReasonNotSupported}}
	resolved, _, _ := resolvePriceMonitorChannelRatio(context.Background(), channel, "", true, true, fake.fetcher())
	assert.Equal(t, []string{"sub2api"}, fake.calls, "the new-api log is not asked")
	assert.Equal(t, priceMonitorRatioReasonSub2APINoGroup, resolved.reason)

	cost := PriceMonitorChannelCost{ChannelId: 1}
	applyPriceMonitorRatioObservation(&cost, resolved, time.Unix(1_800_000_000, 0))
	assert.Equal(t, priceMonitorUpstreamKindSub2API, cost.UpstreamKind)

	// Another gateway's plain 403 on the unknown path is not taken for sub2api.
	plain := &fakeSub2API{billingCode: http.StatusForbidden, billingPlain: true}
	plainURL := plain.server(t).URL
	channel.BaseURL = &plainURL
	other := fetchPriceMonitorSub2APIRatio(context.Background(), 2*time.Second, channel)
	assert.Empty(t, other.kind)
}

// A channel remembered as new-api whose upstream no longer speaks new-api
// (address changed to a sub2api host) is identified again next round.
func TestNewAPIKindIsForgottenWhenNotSupported(t *testing.T) {
	cost := PriceMonitorChannelCost{ChannelId: 1, UpstreamKind: priceMonitorUpstreamKindNewAPI}
	applyPriceMonitorRatioObservation(&cost, priceMonitorRatioObservation{reason: priceMonitorRatioReasonNotSupported}, time.Unix(1_800_000_000, 0))
	assert.Empty(t, cost.UpstreamKind)
}

// One key without a group does not fail a multi-key channel.
func TestFetchSub2APIRatioSkipsKeyWithoutGroup(t *testing.T) {
	api := &fakeSub2API{billing: map[string]string{"sk-b": sub2apiBilling(1.2, 1.2, "")}, billingByKey: map[string]int{"sk-a": http.StatusForbidden}}
	got := sub2apiRatio(sub2apiChannel(1, api.server(t).URL, "sk-a\nsk-b"))
	require.True(t, got.ok, got.reason)
	assert.InDelta(t, 1.2, got.ratio, 1e-9)
}

// sub2api bans an IP after 120 invalid keys in 60 seconds, relay traffic
// included. Within a round, rejected keys per host are capped well below that,
// and a 429 stops further billing queries to the host.
func TestSub2APIProberProtectsTheHost(t *testing.T) {
	api := &fakeSub2API{billing: map[string]string{}}
	url := api.server(t).URL
	prober := newPriceMonitorSub2APIProber(2*time.Second, nil)
	for i := 0; i < 10; i++ {
		keys := make([]string, 0, priceMonitorOfficialMaxKeys)
		for k := 0; k < priceMonitorOfficialMaxKeys; k++ {
			keys = append(keys, "sk-gone-"+strconv.Itoa(i)+"-"+strconv.Itoa(k))
		}
		prober.fetch(context.Background(), sub2apiChannel(i+1, url, strings.Join(keys, "\n")))
	}
	assert.Len(t, api.billingCalls, priceMonitorSub2APIMaxRejectedPerHost)
	blocked := prober.fetch(context.Background(), sub2apiChannel(99, url, "sk-x"))
	assert.Equal(t, priceMonitorRatioReasonWaiting, blocked.reason, "not counted as attempted, retried next round")

	limited := &fakeSub2API{billingCode: http.StatusTooManyRequests}
	limitedURL := limited.server(t).URL
	prober = newPriceMonitorSub2APIProber(2*time.Second, nil)
	first := prober.fetch(context.Background(), sub2apiChannel(1, limitedURL, "sk-a"))
	assert.Equal(t, priceMonitorRatioReasonRateLimited, first.reason)
	prober.fetch(context.Background(), sub2apiChannel(2, limitedURL, "sk-b"))
	assert.Len(t, limited.billingCalls, 1)
}

func TestSub2APIPeakWindowText(t *testing.T) {
	peak := 1.5
	_, _, window := sub2apiWorstCaseRatio(sub2apiKeyBilling{PeakRateEnabled: true, PeakRateMultiplier: &peak, Timezone: "UTC"})
	assert.Equal(t, "UTC", window, "no dangling dash without a start and end")
	ratio, got, _ := sub2apiWorstCaseRatio(sub2apiKeyBilling{ResolvedRateMultiplier: 2, PeakRateEnabled: true, PeakRateMultiplier: floatPointer(0.5)})
	assert.InDelta(t, 2, ratio, 1e-9, "a peak discount does not lower the worst case")
	require.NotNil(t, got)
	assert.InDelta(t, 0.5, *got, 1e-9)
}
