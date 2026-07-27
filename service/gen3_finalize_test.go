package service

import (
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// quota.go / text_quota.go — relay-finalization consume paths.
// PostTextConsumeQuota / PostAudioConsumeQuota / PostWssConsumeQuota /
// PreWssConsumeQuota. Rule 0: heavy work (cost/commission, perf sample) is
// async fire-and-forget; the synchronous part deducts quota + writes one log.
// DB-backed; deduction verified against the written log's quota.
// ===========================================================================

func finalizeCtx(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("token_name", "tkn")
	return c
}

func finalizeRelayInfo(uid, tid, chid int, key string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId:                  uid,
		TokenId:                 tid,
		TokenKey:                key,
		BillingSource:           BillingSourceWallet,
		UsingGroup:              "default",
		OriginModelName:         "gpt-4o",
		RelayFormat:             types.RelayFormatOpenAI,
		FinalRequestRelayFormat: types.RelayFormatOpenAI,
		StartTime:               time.Now().Add(-2 * time.Second),
		ChannelMeta:             &relaycommon.ChannelMeta{ChannelId: chid},
		PriceData: hosttypes.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			ModelPrice:      0,
			GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
	}
}

// --- PostTextConsumeQuota --------------------------------------------------

func TestFinalize_PostTextConsumeQuota_DeductsWallet(t *testing.T) {
	truncate(t)
	const uid, tid, chid = 3201, 3201, 71
	seedUser(t, uid, 1_000_000)
	seedToken(t, tid, uid, "sk-fin-text", 900_000)
	seedChannel(t, chid)

	info := finalizeRelayInfo(uid, tid, chid, "sk-fin-text")
	usage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500}

	PostTextConsumeQuota(finalizeCtx(t), info, usage, nil)

	log := getLastLog(t)
	require.NotNil(t, log)
	require.Greater(t, log.Quota, 0)
	// Wallet + token both decreased by the settled quota.
	assert.Equal(t, 1_000_000-log.Quota, getUserQuota(t, uid))
	assert.Equal(t, 900_000-log.Quota, getTokenRemainQuota(t, tid))
	assert.Equal(t, "gpt-4o", log.ModelName)
}

func TestFinalize_PostTextConsumeQuota_ZeroTokensNoDeduction(t *testing.T) {
	truncate(t)
	const uid, tid, chid = 3202, 3202, 72
	seedUser(t, uid, 500_000)
	seedToken(t, tid, uid, "sk-fin-text0", 400_000)
	seedChannel(t, chid)

	info := finalizeRelayInfo(uid, tid, chid, "sk-fin-text0")
	// TotalTokens 0 => countUsage false, no deduction, log still written.
	usage := &dto.Usage{PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0}

	PostTextConsumeQuota(finalizeCtx(t), info, usage, []string{"pre"})

	assert.Equal(t, 500_000, getUserQuota(t, uid))
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, 0, log.Quota)
}

func TestFinalize_PostTextConsumeQuota_RichUsage(t *testing.T) {
	truncate(t)
	const uid, tid, chid = 3211, 3211, 77
	seedUser(t, uid, 5_000_000)
	seedToken(t, tid, uid, "sk-fin-rich", 4_000_000)
	seedChannel(t, chid)

	info := finalizeRelayInfo(uid, tid, chid, "sk-fin-rich")
	info.PriceData.CacheCreationRatio = 1.5
	info.PriceData.CacheRatio = 0.5

	c := finalizeCtx(t)
	// Drives the Claude web-search extraContent branch.
	c.Set("claude_web_search_requests", 2)

	usage := &dto.Usage{
		PromptTokens:     2000,
		CompletionTokens: 500,
		TotalTokens:      2500,
		PromptTokensDetails: dto.InputTokenDetails{
			ImageTokens:          100,
			CachedCreationTokens: 200,
		},
	}
	PostTextConsumeQuota(c, info, usage, nil)

	log := getLastLog(t)
	require.NotNil(t, log)
	require.Greater(t, log.Quota, 0)
	assert.Equal(t, 5_000_000-log.Quota, getUserQuota(t, uid))
}

// --- PostAudioConsumeQuota -------------------------------------------------

func TestFinalize_PostAudioConsumeQuota_Deducts(t *testing.T) {
	truncate(t)
	const uid, tid, chid = 3203, 3203, 73
	seedUser(t, uid, 1_000_000)
	seedToken(t, tid, uid, "sk-fin-audio", 900_000)
	seedChannel(t, chid)

	info := finalizeRelayInfo(uid, tid, chid, "sk-fin-audio")
	usage := &dto.Usage{
		PromptTokens:        1000,
		CompletionTokens:    200,
		TotalTokens:         1200,
		PromptTokensDetails: dto.InputTokenDetails{TextTokens: 800, AudioTokens: 200},
	}

	PostAudioConsumeQuota(finalizeCtx(t), info, usage, "audio-extra")

	log := getLastLog(t)
	require.NotNil(t, log)
	require.Greater(t, log.Quota, 0)
	assert.Equal(t, 1_000_000-log.Quota, getUserQuota(t, uid))
}

func TestFinalize_PostAudioConsumeQuota_ZeroTokens(t *testing.T) {
	truncate(t)
	const uid, tid, chid = 3204, 3204, 74
	seedUser(t, uid, 300_000)
	seedToken(t, tid, uid, "sk-fin-audio0", 200_000)
	seedChannel(t, chid)

	info := finalizeRelayInfo(uid, tid, chid, "sk-fin-audio0")
	PostAudioConsumeQuota(finalizeCtx(t), info, &dto.Usage{TotalTokens: 0}, "")

	assert.Equal(t, 300_000, getUserQuota(t, uid))
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, 0, log.Quota)
}

// --- PostWssConsumeQuota ---------------------------------------------------

func TestFinalize_PostWssConsumeQuota_Deducts(t *testing.T) {
	truncate(t)
	const uid, tid, chid = 3205, 3205, 75
	seedUser(t, uid, 1_000_000)
	seedToken(t, tid, uid, "sk-fin-wss", 900_000)
	seedChannel(t, chid)

	info := finalizeRelayInfo(uid, tid, chid, "sk-fin-wss")
	usage := &dto.RealtimeUsage{
		InputTokens:        1000,
		OutputTokens:       500,
		TotalTokens:        1500,
		InputTokenDetails:  dto.InputTokenDetails{TextTokens: 900, AudioTokens: 100},
		OutputTokenDetails: dto.OutputTokenDetails{TextTokens: 500},
	}

	PostWssConsumeQuota(finalizeCtx(t), info, "gpt-4o-realtime", usage, "wss-extra")

	log := getLastLog(t)
	require.NotNil(t, log)
	require.Greater(t, log.Quota, 0)
	assert.Equal(t, 1_000_000-log.Quota, getUserQuota(t, uid))
}

// --- PreWssConsumeQuota ----------------------------------------------------

func TestFinalize_PreWssConsumeQuota_Deducts(t *testing.T) {
	truncate(t)
	const uid, tid, chid = 3206, 3206, 76
	seedUser(t, uid, 5_000_000)
	// PreWssConsumeQuota looks up the token by key with the "sk-" prefix trimmed.
	seedToken(t, tid, uid, "fin-prewss", 5_000_000)
	seedChannel(t, chid)

	info := finalizeRelayInfo(uid, tid, chid, "sk-fin-prewss")
	info.TokenKey = "sk-fin-prewss"
	usage := &dto.RealtimeUsage{
		InputTokens:        1000,
		OutputTokens:       200,
		TotalTokens:        1200,
		InputTokenDetails:  dto.InputTokenDetails{TextTokens: 900, AudioTokens: 100},
		OutputTokenDetails: dto.OutputTokenDetails{TextTokens: 200},
	}

	err := PreWssConsumeQuota(finalizeCtx(t), info, usage)
	require.NoError(t, err)
	// Some quota was deducted from the wallet.
	assert.Less(t, getUserQuota(t, uid), 5_000_000)
}

func TestFinalize_PreWssConsumeQuota_UsePriceNoop(t *testing.T) {
	info := &relaycommon.RelayInfo{UsePrice: true}
	assert.NoError(t, PreWssConsumeQuota(finalizeCtx(t), info, &dto.RealtimeUsage{}))
}

// --- CalcOpenRouterCacheCreateTokens (pure) --------------------------------

func TestFinalize_CalcOpenRouterCacheCreateTokens(t *testing.T) {
	// CacheCreationRatio == 1 short-circuits to 0.
	assert.Equal(t, 0, CalcOpenRouterCacheCreateTokens(dto.Usage{}, hosttypes.PriceData{CacheCreationRatio: 1}))

	pd := hosttypes.PriceData{
		ModelRatio:         2,
		CompletionRatio:    2,
		CacheRatio:         0.5,
		CacheCreationRatio: 1.5,
	}
	usage := dto.Usage{
		PromptTokens:        1000,
		CompletionTokens:    100,
		Cost:                float64(1.0),
		PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 100},
	}
	// Just exercise the arithmetic path (result is deterministic given inputs).
	_ = CalcOpenRouterCacheCreateTokens(usage, pd)
}
