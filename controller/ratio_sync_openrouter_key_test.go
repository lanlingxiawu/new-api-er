package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The OpenRouter price source authenticates with the channel's key. The upstream
// address can come from the browser (the "sync upstream ratio" dialog accepts any
// base_url), so the key only ever goes to the channel's own address.

type openRouterModelsServer struct {
	hits int32
	auth atomic.Value
}

func (s *openRouterModelsServer) start(t *testing.T) string {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&s.hits, 1)
		s.auth.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"data":[{"id":"m","pricing":{"prompt":"0.000003","completion":"0.000015"}}]}`))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func withPricingSourceChannel(t *testing.T, channel *model.Channel) {
	t.Helper()
	previous := pricingSourceChannel
	pricingSourceChannel = func(int) (*model.Channel, error) { return channel, nil }
	t.Cleanup(func() { pricingSourceChannel = previous })
}

func openRouterChannel(baseURL, key string) *model.Channel {
	return &model.Channel{Id: 5, Name: "or", Type: constant.ChannelTypeOpenRouter, Key: key, BaseURL: &baseURL}
}

func fetchOpenRouterSource(t *testing.T, baseURL string) dto.TestResult {
	t.Helper()
	results, _ := fetchUpstreamPricingSources(context.Background(), []dto.UpstreamDTO{{ID: 5, Name: "or", BaseURL: baseURL, Endpoint: openRouterPricingEndpoint}}, 5)
	require.Len(t, results, 1)
	return results[0]
}

func TestOpenRouterPricingSendsKeyOnlyToTheChannelAddress(t *testing.T) {
	attacker := &openRouterModelsServer{}
	attackerURL := attacker.start(t)
	withPricingSourceChannel(t, openRouterChannel("https://openrouter.ai/api", "sk-or-secret"))

	result := fetchOpenRouterSource(t, attackerURL)
	assert.NotEqual(t, "success", result.Status)
	assert.Zero(t, atomic.LoadInt32(&attacker.hits), "the foreign address is not contacted")
	assert.NotContains(t, result.Error, "sk-or-secret")
}

func TestOpenRouterPricingUsesTheChannelAddress(t *testing.T) {
	upstream := &openRouterModelsServer{}
	url := upstream.start(t)
	withPricingSourceChannel(t, openRouterChannel(url+"/", "sk-or"))

	// The trailing slash on either side does not matter.
	result := fetchOpenRouterSource(t, url)
	require.Equal(t, "success", result.Status, result.Error)
	assert.Equal(t, "Bearer sk-or", upstream.auth.Load())
}

// Fetching prices must not advance the relay's multi-key rotation: it takes the
// first enabled key instead of the next key in the polling order.
func TestOpenRouterPricingDoesNotAdvanceKeyPolling(t *testing.T) {
	upstream := &openRouterModelsServer{}
	url := upstream.start(t)
	channel := openRouterChannel(url, "sk-disabled\nsk-first\nsk-second")
	channel.ChannelInfo.IsMultiKey = true
	channel.ChannelInfo.MultiKeyMode = constant.MultiKeyModePolling
	channel.ChannelInfo.MultiKeyStatusList = map[int]int{0: common.ChannelStatusManuallyDisabled}
	channel.ChannelInfo.MultiKeyPollingIndex = 2
	withPricingSourceChannel(t, channel)

	result := fetchOpenRouterSource(t, url)
	require.Equal(t, "success", result.Status, result.Error)
	assert.Equal(t, "Bearer sk-first", upstream.auth.Load())
	assert.Equal(t, 2, channel.ChannelInfo.MultiKeyPollingIndex)
}

func TestOpenRouterPricingNeedsAChannel(t *testing.T) {
	upstream := &openRouterModelsServer{}
	url := upstream.start(t)
	results, _ := fetchUpstreamPricingSources(context.Background(), []dto.UpstreamDTO{{Name: "or", BaseURL: url, Endpoint: openRouterPricingEndpoint}}, 5)
	require.Len(t, results, 1)
	assert.NotEqual(t, "success", results[0].Status)
	assert.Zero(t, atomic.LoadInt32(&upstream.hits))
}

func TestOpenRouterPricingWithoutEnabledKey(t *testing.T) {
	upstream := &openRouterModelsServer{}
	url := upstream.start(t)
	channel := openRouterChannel(url, "sk-a")
	channel.ChannelInfo.IsMultiKey = true
	channel.ChannelInfo.MultiKeyStatusList = map[int]int{0: common.ChannelStatusAutoDisabled}
	withPricingSourceChannel(t, channel)

	result := fetchOpenRouterSource(t, url)
	assert.NotEqual(t, "success", result.Status)
	assert.Zero(t, atomic.LoadInt32(&upstream.hits))
}
