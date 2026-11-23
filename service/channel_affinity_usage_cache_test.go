package service

import (
	"fmt"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var channelAffinityTestSeq atomic.Int64

func affinityTestKeys(t *testing.T) (string, string) {
	t.Helper()
	key := fmt.Sprintf("%s:%d", t.Name(), channelAffinityTestSeq.Add(1))
	return key, key
}

func buildChannelAffinityStatsContextForTest(ruleName, usingGroup, keyFP string) *gin.Context {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	setChannelAffinityContext(ctx, channelAffinityMeta{
		CacheKey:       fmt.Sprintf("test:%s:%s:%s", ruleName, usingGroup, keyFP),
		TTLSeconds:     600,
		RuleName:       ruleName,
		UsingGroup:     usingGroup,
		KeyFingerprint: keyFP,
	})
	return ctx
}

func TestObserveChannelAffinityUsageCacheByRelayFormat_ClaudeMode(t *testing.T) {
	ruleName, keyFP := affinityTestKeys(t)
	usingGroup := "default"
	ctx := buildChannelAffinityStatsContextForTest(ruleName, usingGroup, keyFP)

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 40,
		TotalTokens:      140,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 30,
		},
	}

	ObserveChannelAffinityUsageCacheByRelayFormat(ctx, usage, types.RelayFormatClaude)
	flushChannelAffinityUsageStats()
	stats := GetChannelAffinityUsageCacheStats(ruleName, usingGroup, keyFP)

	require.EqualValues(t, 1, stats.Total)
	require.EqualValues(t, 1, stats.Hit)
	require.EqualValues(t, 100, stats.PromptTokens)
	require.EqualValues(t, 40, stats.CompletionTokens)
	require.EqualValues(t, 140, stats.TotalTokens)
	require.EqualValues(t, 30, stats.CachedTokens)
	require.Equal(t, cacheTokenRateModeCachedOverPromptPlusCached, stats.CachedTokenRateMode)
}

func TestObserveChannelAffinityUsageCacheByRelayFormat_MixedMode(t *testing.T) {
	ruleName, keyFP := affinityTestKeys(t)
	usingGroup := "default"
	ctx := buildChannelAffinityStatsContextForTest(ruleName, usingGroup, keyFP)

	openAIUsage := &dto.Usage{
		PromptTokens: 100,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 10,
		},
	}
	claudeUsage := &dto.Usage{
		PromptTokens: 80,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 20,
		},
	}

	ObserveChannelAffinityUsageCacheByRelayFormat(ctx, openAIUsage, types.RelayFormatOpenAI)
	ObserveChannelAffinityUsageCacheByRelayFormat(ctx, claudeUsage, types.RelayFormatClaude)
	flushChannelAffinityUsageStats()
	stats := GetChannelAffinityUsageCacheStats(ruleName, usingGroup, keyFP)

	require.EqualValues(t, 2, stats.Total)
	require.EqualValues(t, 2, stats.Hit)
	require.EqualValues(t, 180, stats.PromptTokens)
	require.EqualValues(t, 30, stats.CachedTokens)
	require.Equal(t, cacheTokenRateModeMixed, stats.CachedTokenRateMode)
}

func TestObserveChannelAffinityUsageCacheByRelayFormat_UnsupportedModeKeepsEmpty(t *testing.T) {
	ruleName, keyFP := affinityTestKeys(t)
	usingGroup := "default"
	ctx := buildChannelAffinityStatsContextForTest(ruleName, usingGroup, keyFP)

	usage := &dto.Usage{
		PromptTokens: 100,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 25,
		},
	}

	ObserveChannelAffinityUsageCacheByRelayFormat(ctx, usage, types.RelayFormatGemini)
	flushChannelAffinityUsageStats()
	stats := GetChannelAffinityUsageCacheStats(ruleName, usingGroup, keyFP)

	require.EqualValues(t, 1, stats.Total)
	require.EqualValues(t, 1, stats.Hit)
	require.EqualValues(t, 25, stats.CachedTokens)
	require.Equal(t, "", stats.CachedTokenRateMode)
}

func TestObserveChannelAffinityUsageCache_SyncEnvAppliesBeforeReturn(t *testing.T) {
	t.Setenv("CHANNEL_AFFINITY_USAGE_STATS_SYNC", "true")
	ruleName, keyFP := affinityTestKeys(t)
	usingGroup := "default"
	ctx := buildChannelAffinityStatsContextForTest(ruleName, usingGroup, keyFP)
	ObserveChannelAffinityUsageCacheByRelayFormat(ctx, &dto.Usage{PromptTokens: 7}, types.RelayFormatOpenAI)
	stats := GetChannelAffinityUsageCacheStats(ruleName, usingGroup, keyFP)
	require.EqualValues(t, 1, stats.Total)
	require.EqualValues(t, 7, stats.PromptTokens)
}

func TestAffinityUsageStatsDropsWhenQueueIsFull(t *testing.T) {
	t.Setenv("CHANNEL_AFFINITY_USAGE_STATS_SYNC", "")
	flushChannelAffinityUsageStats()

	release := make(chan struct{})
	entered := make(chan struct{}, channelAffinityUsageStatsWorkers)
	hook := func(affinityUsageObservation) {
		entered <- struct{}{}
		<-release
	}
	affinityUsageObserveHook.Store(&hook)
	t.Cleanup(func() {
		close(release)
		affinityUsageObserveHook.Store(nil)
		flushChannelAffinityUsageStats()
	})

	for range channelAffinityUsageStatsWorkers {
		require.True(t, enqueueAffinityUsageObservation(affinityUsageObservation{}))
	}
	deadline := time.After(2 * time.Second)
	for range channelAffinityUsageStatsWorkers {
		select {
		case <-entered:
		case <-deadline:
			t.Fatal("usage stats workers did not reach the blocked hook")
		}
	}
	for range channelAffinityUsageStatsQueueSize {
		require.True(t, enqueueAffinityUsageObservation(affinityUsageObservation{}))
	}
	before := affinityUsageDrops.Load()
	require.False(t, enqueueAffinityUsageObservation(affinityUsageObservation{}))
	require.Equal(t, before+1, affinityUsageDrops.Load())
}
