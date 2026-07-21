package service

// gen_billmath_test.go — pure (non-DB) billing-math coverage for
// tool_billing.go, tiered_settle.go, quota.go and text_quota.go.
//
// All identifiers here are prefixed Billmath/billmath to avoid collisions with
// the existing service test harness (TestMain, makeRelayInfo, tieredQuota, ...).
// No DB / Redis / network is touched: every getter used reads in-memory
// defaults registered in package init().

import (
	"math"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// billmathCtx returns a bare gin test context (no Keys set).
func billmathCtx() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	return c
}

// ---------------------------------------------------------------------------
// tool_billing.go — ComputeToolCallQuota
// ---------------------------------------------------------------------------

func TestBillmathComputeToolCallQuota_WebSearch(t *testing.T) {
	// web_search_preview default price = 10.0 $/1K calls (model does not match
	// any gpt-4o* override prefix). QuotaPerUnit default = 500000.
	res := ComputeToolCallQuota(ToolCallUsage{
		ModelName:         "billmath-model",
		WebSearchCalls:    3,
		WebSearchToolName: "web_search_preview",
	}, 1.0)

	require.Len(t, res.Items, 1)
	it := res.Items[0]
	assert.Equal(t, "web_search_preview", it.Name)
	assert.Equal(t, 3, it.CallCount)
	assert.InDelta(t, 10.0, it.PricePer1K, 1e-9)
	assert.InDelta(t, 0.03, it.TotalPrice, 1e-9) // 10 * 3 / 1000
	// round(0.03 * 500000 * 1) = 15000
	assert.Equal(t, 15000, it.Quota)
	assert.Equal(t, 15000, res.TotalQuota)
}

func TestBillmathComputeToolCallQuota_FileSearch(t *testing.T) {
	// file_search default price = 2.5 $/1K.
	res := ComputeToolCallQuota(ToolCallUsage{
		ModelName:       "billmath-model",
		FileSearchCalls: 4,
	}, 1.0)

	require.Len(t, res.Items, 1)
	assert.Equal(t, "file_search", res.Items[0].Name)
	assert.InDelta(t, 2.5, res.Items[0].PricePer1K, 1e-9)
	assert.InDelta(t, 0.01, res.Items[0].TotalPrice, 1e-9) // 2.5 * 4 / 1000
	// round(0.01 * 500000) = 5000
	assert.Equal(t, 5000, res.Items[0].Quota)
	assert.Equal(t, 5000, res.TotalQuota)
}

func TestBillmathComputeToolCallQuota_ImageGeneration(t *testing.T) {
	// low / 1024x1024 => 0.011 per call.
	res := ComputeToolCallQuota(ToolCallUsage{
		ModelName:              "billmath-model",
		ImageGenerationCall:    true,
		ImageGenerationQuality: "low",
		ImageGenerationSize:    "1024x1024",
	}, 1.0)

	require.Len(t, res.Items, 1)
	it := res.Items[0]
	assert.Equal(t, "image_generation", it.Name)
	assert.Equal(t, 1, it.CallCount)
	assert.InDelta(t, 0.011, it.PricePer1K, 1e-9)
	assert.InDelta(t, 0.011, it.TotalPrice, 1e-9)
	// round(0.011 * 500000) = 5500
	assert.Equal(t, 5500, it.Quota)
	assert.Equal(t, 5500, res.TotalQuota)
}

func TestBillmathComputeToolCallQuota_ImageGeneration_UnknownFallsBackToHigh(t *testing.T) {
	// Unknown quality/size => GPTImage1High1024x1024 = 0.167.
	res := ComputeToolCallQuota(ToolCallUsage{
		ModelName:              "billmath-model",
		ImageGenerationCall:    true,
		ImageGenerationQuality: "unknown",
		ImageGenerationSize:    "unknown",
	}, 1.0)

	require.Len(t, res.Items, 1)
	assert.InDelta(t, 0.167, res.Items[0].PricePer1K, 1e-9)
	// round(0.167 * 500000) = 83500
	assert.Equal(t, 83500, res.Items[0].Quota)
}

func TestBillmathComputeToolCallQuota_ZeroCounts(t *testing.T) {
	res := ComputeToolCallQuota(ToolCallUsage{
		ModelName:         "billmath-model",
		WebSearchCalls:    0,
		WebSearchToolName: "web_search_preview",
		FileSearchCalls:   0,
	}, 1.0)
	assert.Equal(t, 0, res.TotalQuota)
	assert.Empty(t, res.Items)
}

func TestBillmathComputeToolCallQuota_WebSearchEmptyToolName(t *testing.T) {
	// Count > 0 but empty tool name => web search branch guarded out.
	res := ComputeToolCallQuota(ToolCallUsage{
		ModelName:         "billmath-model",
		WebSearchCalls:    5,
		WebSearchToolName: "",
	}, 1.0)
	assert.Equal(t, 0, res.TotalQuota)
	assert.Empty(t, res.Items)
}

func TestBillmathComputeToolCallQuota_ZeroPriceSkipped(t *testing.T) {
	// Unknown tool name resolves to price 0 => addItem returns early.
	res := ComputeToolCallQuota(ToolCallUsage{
		ModelName:         "billmath-model",
		WebSearchCalls:    5,
		WebSearchToolName: "billmath-unknown-tool",
	}, 1.0)
	assert.Equal(t, 0, res.TotalQuota)
	assert.Empty(t, res.Items)
}

func TestBillmathComputeToolCallQuota_GroupRatioApplied(t *testing.T) {
	res2 := ComputeToolCallQuota(ToolCallUsage{
		ModelName:         "billmath-model",
		WebSearchCalls:    3,
		WebSearchToolName: "web_search_preview",
	}, 2.0)
	assert.Equal(t, 30000, res2.TotalQuota) // round(0.03 * 500000 * 2)

	resHalf := ComputeToolCallQuota(ToolCallUsage{
		ModelName:         "billmath-model",
		WebSearchCalls:    3,
		WebSearchToolName: "web_search_preview",
	}, 0.5)
	assert.Equal(t, 7500, resHalf.TotalQuota) // round(0.03 * 500000 * 0.5)
}

func TestBillmathComputeToolCallQuota_Combined(t *testing.T) {
	res := ComputeToolCallQuota(ToolCallUsage{
		ModelName:              "billmath-model",
		WebSearchCalls:         3,
		WebSearchToolName:      "web_search_preview",
		FileSearchCalls:        4,
		ImageGenerationCall:    true,
		ImageGenerationQuality: "low",
		ImageGenerationSize:    "1024x1024",
	}, 1.0)

	require.Len(t, res.Items, 3)
	// 15000 (web) + 5000 (file) + 5500 (image)
	assert.Equal(t, 15000+5000+5500, res.TotalQuota)
}

// ---------------------------------------------------------------------------
// tiered_settle.go — BuildTieredTokenParams
// ---------------------------------------------------------------------------

func billmathUsage() *dto.Usage {
	u := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
	}
	u.PromptTokensDetails = dto.InputTokenDetails{
		CachedTokens:     100, // cr
		CacheWriteTokens: 50,  // cc (via CacheCreationTokensTotal)
		ImageTokens:      30,  // img
		AudioTokens:      20,  // ai
	}
	u.CompletionTokenDetails = dto.OutputTokenDetails{
		ImageTokens: 10, // img_o
		AudioTokens: 5,  // ao
	}
	return u
}

func TestBillmathBuildTieredTokenParams_OpenAINoUsedVars(t *testing.T) {
	u := billmathUsage()
	p := BuildTieredTokenParams(u, false, nil)

	assert.InDelta(t, 1000, p.P, 1e-9)
	assert.InDelta(t, 500, p.C, 1e-9)
	assert.InDelta(t, 1000, p.Len, 1e-9) // non-claude len == prompt tokens
	assert.InDelta(t, 100, p.CR, 1e-9)
	assert.InDelta(t, 50, p.CC, 1e-9)
	assert.InDelta(t, 0, p.CC1h, 1e-9)
	assert.InDelta(t, 30, p.Img, 1e-9)
	assert.InDelta(t, 10, p.ImgO, 1e-9)
	assert.InDelta(t, 20, p.AI, 1e-9)
	assert.InDelta(t, 5, p.AO, 1e-9)
}

func TestBillmathBuildTieredTokenParams_OpenAIAllUsedVars(t *testing.T) {
	u := billmathUsage()
	used := map[string]bool{
		"cr": true, "cc": true, "cc1h": true,
		"img": true, "ai": true, "img_o": true, "ao": true,
	}
	p := BuildTieredTokenParams(u, false, used)

	// P = 1000 - cr(100) - cc(50) - cc1h(0) - img(30) - ai(20) = 800
	assert.InDelta(t, 800, p.P, 1e-9)
	// C = 500 - imgO(10) - ao(5) = 485
	assert.InDelta(t, 485, p.C, 1e-9)
	// Len is captured from prompt BEFORE subtraction.
	assert.InDelta(t, 1000, p.Len, 1e-9)
	// The returned sub-category fields are the raw values, not subtracted.
	assert.InDelta(t, 100, p.CR, 1e-9)
	assert.InDelta(t, 50, p.CC, 1e-9)
	assert.InDelta(t, 30, p.Img, 1e-9)
}

func TestBillmathBuildTieredTokenParams_OpenAIPartialUsedVars(t *testing.T) {
	u := billmathUsage()
	p := BuildTieredTokenParams(u, false, map[string]bool{"cr": true})
	// only cr subtracted from prompt
	assert.InDelta(t, 900, p.P, 1e-9)
	assert.InDelta(t, 500, p.C, 1e-9)
}

func TestBillmathBuildTieredTokenParams_ClaudeSemantic(t *testing.T) {
	u := billmathUsage()
	u.UsageSemantic = "anthropic"
	u.ClaudeCacheCreation5mTokens = 40
	u.ClaudeCacheCreation1hTokens = 15

	p := BuildTieredTokenParams(u, true, map[string]bool{"cr": true, "cc": true})

	// Claude: no subtraction; cc/cc1h overridden from claude fields.
	assert.InDelta(t, 1000, p.P, 1e-9)
	assert.InDelta(t, 500, p.C, 1e-9)
	assert.InDelta(t, 40, p.CC, 1e-9)
	assert.InDelta(t, 15, p.CC1h, 1e-9)
	// Len = p + cr + cc5m + cc1h = 1000 + 100 + 40 + 15 = 1155
	assert.InDelta(t, 1155, p.Len, 1e-9)
}

// Decision-coverage edge: usage.UsageSemantic=="anthropic" drives the cc
// override, but the isClaudeUsageSemantic flag is passed false — so the
// non-Claude subtraction path still runs with claude-derived cc values.
func TestBillmathBuildTieredTokenParams_AnthropicUsageButNonClaudeFlag(t *testing.T) {
	u := billmathUsage()
	u.UsageSemantic = "anthropic"
	u.ClaudeCacheCreation5mTokens = 40
	u.ClaudeCacheCreation1hTokens = 15

	p := BuildTieredTokenParams(u, false, map[string]bool{"cr": true, "cc": true, "cc1h": true})

	// cc=40, cc1h=15 (from claude fields); cr=100.
	// P = 1000 - 100 - 40 - 15 = 845
	assert.InDelta(t, 845, p.P, 1e-9)
	assert.InDelta(t, 1000, p.Len, 1e-9) // non-claude flag => len stays prompt
	assert.InDelta(t, 40, p.CC, 1e-9)
	assert.InDelta(t, 15, p.CC1h, 1e-9)
}

func TestBillmathBuildTieredTokenParams_NegativeClampToZero(t *testing.T) {
	u := &dto.Usage{PromptTokens: 100, CompletionTokens: 10}
	u.PromptTokensDetails = dto.InputTokenDetails{CachedTokens: 80, CacheWriteTokens: 60}
	u.CompletionTokenDetails = dto.OutputTokenDetails{ImageTokens: 20}

	p := BuildTieredTokenParams(u, false, map[string]bool{"cr": true, "cc": true, "img_o": true})
	// P = 100 - 80 - 60 = -40 -> clamp 0
	assert.InDelta(t, 0, p.P, 1e-9)
	// C = 10 - 20 = -10 -> clamp 0
	assert.InDelta(t, 0, p.C, 1e-9)
}

func TestBillmathBuildTieredTokenParams_AllZero(t *testing.T) {
	p := BuildTieredTokenParams(&dto.Usage{}, false, nil)
	assert.InDelta(t, 0, p.P, 1e-9)
	assert.InDelta(t, 0, p.C, 1e-9)
	assert.InDelta(t, 0, p.Len, 1e-9)
	assert.InDelta(t, 0, p.CR, 1e-9)
}

// ---------------------------------------------------------------------------
// quota.go — hasCustomModelRatio
// ---------------------------------------------------------------------------

func TestBillmathHasCustomModelRatio(t *testing.T) {
	// Unknown model => always custom.
	assert.True(t, hasCustomModelRatio("billmath-definitely-not-a-real-model", 1.0))

	// Pick a real default model + ratio from the in-memory map.
	m := ratio_setting.GetDefaultModelRatioMap()
	require.NotEmpty(t, m)
	var name string
	var ratio float64
	for k, v := range m {
		name, ratio = k, v
		break
	}
	assert.False(t, hasCustomModelRatio(name, ratio), "matching default ratio is not custom")
	assert.True(t, hasCustomModelRatio(name, ratio+1), "differing ratio is custom")
}

// ---------------------------------------------------------------------------
// quota.go — calculateAudioQuota
// ---------------------------------------------------------------------------

func TestBillmathCalculateAudioQuota_UsePrice(t *testing.T) {
	q, clamp := calculateAudioQuota(QuotaInfo{
		UsePrice:   true,
		ModelPrice: 2.0,
		GroupRatio: 1.5,
	})
	// 2 * 500000 * 1.5 = 1500000
	assert.Equal(t, 1500000, q)
	assert.Nil(t, clamp)
}

func TestBillmathCalculateAudioQuota_UsePrice_Saturation(t *testing.T) {
	q, clamp := calculateAudioQuota(QuotaInfo{
		UsePrice:   true,
		ModelPrice: 1e6, // 1e6 * 500000 = 5e11 > MaxInt32
		GroupRatio: 1.0,
	})
	assert.Equal(t, common.MaxQuota, q)
	require.NotNil(t, clamp)
	assert.Equal(t, common.QuotaClampOverflow, clamp.Kind)
}

func TestBillmathCalculateAudioQuota_Ratio_TextOnly(t *testing.T) {
	// Only input text tokens, so completion/audio ratios don't matter.
	q, clamp := calculateAudioQuota(QuotaInfo{
		ModelName:    "billmath-audio-model",
		InputDetails: TokenDetails{TextTokens: 100},
		ModelRatio:   2,
		GroupRatio:   1.5,
	})
	// 100 * (1.5 * 2) = 300
	assert.Equal(t, 300, q)
	assert.Nil(t, clamp)
}

// Mirror of the ratio-branch formula to pin a mixed audio+text case exactly.
func TestBillmathCalculateAudioQuota_Ratio_MixedMirror(t *testing.T) {
	model := "billmath-audio-model"
	info := QuotaInfo{
		ModelName:     model,
		InputDetails:  TokenDetails{TextTokens: 100, AudioTokens: 200},
		OutputDetails: TokenDetails{TextTokens: 50, AudioTokens: 20},
		ModelRatio:    2,
		GroupRatio:    1.5,
	}
	q, clamp := calculateAudioQuota(info)
	assert.Nil(t, clamp)

	completionRatio := decimal.NewFromFloat(ratio_setting.GetCompletionRatio(model))
	audioRatio := decimal.NewFromFloat(ratio_setting.GetAudioRatio(model))
	audioCompletionRatio := decimal.NewFromFloat(ratio_setting.GetAudioCompletionRatio(model))
	ratio := decimal.NewFromFloat(info.GroupRatio).Mul(decimal.NewFromFloat(info.ModelRatio))
	expect := decimal.Zero.
		Add(decimal.NewFromInt(100)).
		Add(decimal.NewFromInt(50).Mul(completionRatio)).
		Add(decimal.NewFromInt(200).Mul(audioRatio)).
		Add(decimal.NewFromInt(20).Mul(audioRatio).Mul(audioCompletionRatio)).
		Mul(ratio)
	assert.Equal(t, common.QuotaFromDecimal(expect), q)
}

func TestBillmathCalculateAudioQuota_Ratio_QuotaLEZeroBecomesOne(t *testing.T) {
	// Zero tokens, non-zero ratio => quota <= 0 clamped up to 1.
	q, _ := calculateAudioQuota(QuotaInfo{
		ModelName:  "billmath-audio-model",
		ModelRatio: 2,
		GroupRatio: 1,
	})
	assert.Equal(t, 1, q)
}

func TestBillmathCalculateAudioQuota_Ratio_ZeroRatioStaysZero(t *testing.T) {
	// ratio is zero => the <=0 -> 1 rule is skipped, quota stays 0.
	q, _ := calculateAudioQuota(QuotaInfo{
		ModelName:    "billmath-audio-model",
		InputDetails: TokenDetails{TextTokens: 100},
		ModelRatio:   0,
		GroupRatio:   1,
	})
	assert.Equal(t, 0, q)
}

// ---------------------------------------------------------------------------
// quota.go — hasAudioTokenDetails / shouldRecordAudioLedgerQuota
// ---------------------------------------------------------------------------

func TestBillmathHasAudioTokenDetails(t *testing.T) {
	assert.False(t, hasAudioTokenDetails(TokenDetails{}, TokenDetails{}))
	assert.True(t, hasAudioTokenDetails(TokenDetails{TextTokens: 1}, TokenDetails{}))
	assert.True(t, hasAudioTokenDetails(TokenDetails{AudioTokens: 1}, TokenDetails{}))
	assert.True(t, hasAudioTokenDetails(TokenDetails{}, TokenDetails{TextTokens: 1}))
	assert.True(t, hasAudioTokenDetails(TokenDetails{}, TokenDetails{AudioTokens: 1}))
}

func TestBillmathShouldRecordAudioLedgerQuota(t *testing.T) {
	det := TokenDetails{}
	withDetails := TokenDetails{TextTokens: 1}

	// totalTokens > 0 => quota != 0
	assert.False(t, shouldRecordAudioLedgerQuota(10, 0, false, det, det))
	assert.True(t, shouldRecordAudioLedgerQuota(10, 5, false, det, det))
	assert.True(t, shouldRecordAudioLedgerQuota(10, -2, false, det, det))

	// totalTokens == 0 => quota <= 0 never recorded
	assert.False(t, shouldRecordAudioLedgerQuota(0, 0, true, withDetails, det))
	assert.False(t, shouldRecordAudioLedgerQuota(0, -5, true, withDetails, det))

	// totalTokens == 0, quota > 0 => usePrice OR hasAudioTokenDetails
	assert.True(t, shouldRecordAudioLedgerQuota(0, 5, true, det, det))
	assert.False(t, shouldRecordAudioLedgerQuota(0, 5, false, det, det))
	assert.True(t, shouldRecordAudioLedgerQuota(0, 5, false, withDetails, det))
}

// ---------------------------------------------------------------------------
// quota.go — CalcOpenRouterCacheCreateTokens
// ---------------------------------------------------------------------------

func TestBillmathCalcOpenRouterCacheCreateTokens_RatioOneShortCircuit(t *testing.T) {
	got := CalcOpenRouterCacheCreateTokens(dto.Usage{PromptTokens: 500}, types.PriceData{
		CacheCreationRatio: 1,
	})
	assert.Equal(t, 0, got)
}

func TestBillmathCalcOpenRouterCacheCreateTokens_Computed(t *testing.T) {
	// quotaPrice = ModelRatio/QuotaPerUnit = 500000/500000 = 1.
	// promptCacheCreatePrice = 1*2 = 2; promptCacheReadPrice = 1*0.5 = 0.5;
	// completionPrice = 1*4 = 4; denom = 2 - 1 = 1.
	// num = cost(1000) - prompt(500)*1 + cacheRead(100)*(1-0.5) - completion(50)*4
	//     = 1000 - 500 + 50 - 200 = 350
	pd := types.PriceData{
		ModelRatio:         common.QuotaPerUnit, // => quotaPrice 1
		CacheCreationRatio: 2,
		CacheRatio:         0.5,
		CompletionRatio:    4,
	}
	u := dto.Usage{PromptTokens: 500, CompletionTokens: 50, Cost: float64(1000)}
	u.PromptTokensDetails = dto.InputTokenDetails{CachedTokens: 100}

	assert.Equal(t, 350, CalcOpenRouterCacheCreateTokens(u, pd))
}

func TestBillmathCalcOpenRouterCacheCreateTokens_NonFloatCostIsZero(t *testing.T) {
	// Cost not a float64 => treated as 0.
	// num = 0 - 500 + 0 - 0 = -500; denom = 1 => -500.
	pd := types.PriceData{
		ModelRatio:         common.QuotaPerUnit,
		CacheCreationRatio: 2,
		CacheRatio:         0.5,
		CompletionRatio:    4,
	}
	u := dto.Usage{PromptTokens: 500, Cost: "not-a-float"}
	assert.Equal(t, -500, CalcOpenRouterCacheCreateTokens(u, pd))
}

// ---------------------------------------------------------------------------
// text_quota.go — cacheWriteTokensTotal
// ---------------------------------------------------------------------------

func TestBillmathCacheWriteTokensTotal(t *testing.T) {
	// no split values => returns CacheCreationTokens
	assert.Equal(t, 30, cacheWriteTokensTotal(textQuotaSummary{CacheCreationTokens: 30}))

	// split present, creation <= split => returns split sum
	assert.Equal(t, 30, cacheWriteTokensTotal(textQuotaSummary{
		CacheCreationTokens: 25, CacheCreationTokens5m: 10, CacheCreationTokens1h: 20,
	}))

	// split present, creation > split => returns creation
	assert.Equal(t, 40, cacheWriteTokensTotal(textQuotaSummary{
		CacheCreationTokens: 40, CacheCreationTokens5m: 10, CacheCreationTokens1h: 5,
	}))

	// only 5m present, creation < split => returns split
	assert.Equal(t, 10, cacheWriteTokensTotal(textQuotaSummary{
		CacheCreationTokens: 5, CacheCreationTokens5m: 10,
	}))
}

// ---------------------------------------------------------------------------
// text_quota.go — isLegacyClaudeDerivedOpenAIUsage
// ---------------------------------------------------------------------------

func billmathRelayInfo(format types.RelayFormat) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{RelayFormat: format}
}

func TestBillmathIsLegacyClaudeDerivedOpenAIUsage(t *testing.T) {
	ri := billmathRelayInfo(types.RelayFormatOpenAI)

	// nil guards
	assert.False(t, isLegacyClaudeDerivedOpenAIUsage(nil, &dto.Usage{}))
	assert.False(t, isLegacyClaudeDerivedOpenAIUsage(ri, nil))

	// claude request format => false
	assert.False(t, isLegacyClaudeDerivedOpenAIUsage(
		billmathRelayInfo(types.RelayFormatClaude),
		&dto.Usage{ClaudeCacheCreation5mTokens: 5}))

	// tagged usage (source or semantic set) => false
	assert.False(t, isLegacyClaudeDerivedOpenAIUsage(ri, &dto.Usage{UsageSource: "oai_chat", ClaudeCacheCreation5mTokens: 5}))
	assert.False(t, isLegacyClaudeDerivedOpenAIUsage(ri, &dto.Usage{UsageSemantic: "openai", ClaudeCacheCreation5mTokens: 5}))

	// clean openai usage carrying claude cache creation => legacy true
	assert.True(t, isLegacyClaudeDerivedOpenAIUsage(ri, &dto.Usage{ClaudeCacheCreation5mTokens: 5}))
	assert.True(t, isLegacyClaudeDerivedOpenAIUsage(ri, &dto.Usage{ClaudeCacheCreation1hTokens: 3}))

	// clean openai usage, no claude tokens => false
	assert.False(t, isLegacyClaudeDerivedOpenAIUsage(ri, &dto.Usage{}))
}

// ---------------------------------------------------------------------------
// text_quota.go — usageSemanticFromUsage
// ---------------------------------------------------------------------------

func TestBillmathUsageSemanticFromUsage(t *testing.T) {
	// explicit usage semantic wins
	assert.Equal(t, "gemini", usageSemanticFromUsage(
		billmathRelayInfo(types.RelayFormatClaude), &dto.Usage{UsageSemantic: "gemini"}))

	// usage nil + claude format => anthropic
	assert.Equal(t, "anthropic", usageSemanticFromUsage(billmathRelayInfo(types.RelayFormatClaude), nil))

	// usage nil + openai format => openai
	assert.Equal(t, "openai", usageSemanticFromUsage(billmathRelayInfo(types.RelayFormatOpenAI), nil))

	// usage present but empty semantic + claude format => anthropic
	assert.Equal(t, "anthropic", usageSemanticFromUsage(billmathRelayInfo(types.RelayFormatClaude), &dto.Usage{}))
}

// ---------------------------------------------------------------------------
// text_quota.go — noteQuotaClamp
// ---------------------------------------------------------------------------

func TestBillmathNoteQuotaClamp(t *testing.T) {
	// nil clamp / nil relayInfo => no-op, no panic
	noteQuotaClamp(nil, &common.QuotaClamp{})
	ri := &relaycommon.RelayInfo{}
	noteQuotaClamp(ri, nil)
	assert.Nil(t, ri.QuotaClamp)

	// first non-nil clamp wins
	first := &common.QuotaClamp{Op: "first"}
	second := &common.QuotaClamp{Op: "second"}
	noteQuotaClamp(ri, first)
	assert.Same(t, first, ri.QuotaClamp)
	noteQuotaClamp(ri, second)
	assert.Same(t, first, ri.QuotaClamp, "already-set clamp must not be overwritten")
}

// ---------------------------------------------------------------------------
// text_quota.go — composeTieredTextQuota
// ---------------------------------------------------------------------------

func TestBillmathComposeTieredTextQuota_NoSurcharge(t *testing.T) {
	ri := &relaycommon.RelayInfo{}
	got := composeTieredTextQuota(ri, textQuotaSummary{}, 500, nil)
	assert.Equal(t, 500, got)
}

func TestBillmathComposeTieredTextQuota_SurchargeNoResult(t *testing.T) {
	ri := &relaycommon.RelayInfo{}
	summary := textQuotaSummary{ToolCallSurchargeQuota: decimal.NewFromInt(123)}
	// round(500 + 123) = 623
	assert.Equal(t, 623, composeTieredTextQuota(ri, summary, 500, nil))
}

func TestBillmathComposeTieredTextQuota_SurchargeWithResultAndSnapshot(t *testing.T) {
	ri := &relaycommon.RelayInfo{
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{GroupRatio: 1.5},
	}
	summary := textQuotaSummary{ToolCallSurchargeQuota: decimal.NewFromInt(200)}
	tr := &billingexpr.TieredResult{ActualQuotaBeforeGroup: 1000.0}
	// 1000 * 1.5 + 200 = 1700 ; tieredQuota arg is ignored on this path.
	assert.Equal(t, 1700, composeTieredTextQuota(ri, summary, 9999, tr))
}

// ---------------------------------------------------------------------------
// text_quota.go — calculateTextToolCallSurcharge
// ---------------------------------------------------------------------------

func TestBillmathCalculateTextToolCallSurcharge_None(t *testing.T) {
	ctx := billmathCtx()
	ri := billmathRelayInfo(types.RelayFormatOpenAI)
	s := &textQuotaSummary{ModelName: "billmath-plain-model", GroupRatio: 1.0}
	got := calculateTextToolCallSurcharge(ctx, ri, s)
	assert.True(t, got.IsZero())
}

func TestBillmathCalculateTextToolCallSurcharge_SearchPreviewSuffix(t *testing.T) {
	ctx := billmathCtx()
	ri := billmathRelayInfo(types.RelayFormatOpenAI) // ResponsesUsageInfo nil
	s := &textQuotaSummary{ModelName: "billmath-search-preview", GroupRatio: 1.0}
	got := calculateTextToolCallSurcharge(ctx, ri, s)

	assert.Equal(t, 1, s.WebSearchCallCount)
	assert.InDelta(t, 10.0, s.WebSearchPrice, 1e-9)
	// 10/1000 * 1 * 500000 = 5000
	assert.InDelta(t, 5000.0, got.InexactFloat64(), 1e-6)
}

func TestBillmathCalculateTextToolCallSurcharge_ClaudeWebSearchAndImage(t *testing.T) {
	ctx := billmathCtx()
	ctx.Set("claude_web_search_requests", 2)
	ctx.Set("image_generation_call", true)
	// quality/size empty => high fallback 0.167
	ri := billmathRelayInfo(types.RelayFormatOpenAI)
	s := &textQuotaSummary{ModelName: "billmath-plain-model", GroupRatio: 1.0}
	got := calculateTextToolCallSurcharge(ctx, ri, s)

	assert.Equal(t, 2, s.ClaudeWebSearchCallCount)
	assert.InDelta(t, 10.0, s.ClaudeWebSearchPrice, 1e-9)
	// claude web search: 10/1000 * 1 * 500000 * 2 = 10000
	// image gen: 0.167 * 1 * 500000 = 83500
	assert.InDelta(t, 10000.0+83500.0, got.InexactFloat64(), 1e-6)
}

// ---------------------------------------------------------------------------
// text_quota.go — calculateTextQuotaSummary
// ---------------------------------------------------------------------------

func billmathTextRelayInfo(pd types.PriceData, model string, format types.RelayFormat) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: model,
		StartTime:       time.Now(),
		RelayFormat:     format,
		PriceData:       pd,
	}
}

func TestBillmathCalculateTextQuotaSummary_OpenAIRatio(t *testing.T) {
	ctx := billmathCtx()
	pd := types.PriceData{
		UsePrice:        false,
		ModelRatio:      2,
		CompletionRatio: 3,
		CacheRatio:      0.5,
		GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
	}
	ri := billmathTextRelayInfo(pd, "billmath-text-model", types.RelayFormatOpenAI)
	u := &dto.Usage{PromptTokens: 1000, CompletionTokens: 200}
	u.PromptTokensDetails = dto.InputTokenDetails{CachedTokens: 100}

	s := calculateTextQuotaSummary(ctx, ri, u)

	// baseTokens = 1000 - 100(cache) = 900; +cache*0.5=50 => promptQuota 950
	// completion = 200*3 = 600 ; (950+600)*ratio(2) = 3100
	assert.Equal(t, 3100, s.Quota)
	assert.Equal(t, 3100, s.LedgerQuota)
	assert.Equal(t, 1200, s.TotalTokens)
	assert.Nil(t, ri.QuotaClamp)
}

func TestBillmathCalculateTextQuotaSummary_OpenAICacheCreation(t *testing.T) {
	ctx := billmathCtx()
	pd := types.PriceData{
		UsePrice:           false,
		ModelRatio:         2,
		CompletionRatio:    3,
		CacheCreationRatio: 1.5,
		GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: 1},
	}
	ri := billmathTextRelayInfo(pd, "billmath-text-model", types.RelayFormatOpenAI)
	u := &dto.Usage{PromptTokens: 1000, CompletionTokens: 0}
	u.PromptTokensDetails = dto.InputTokenDetails{CacheWriteTokens: 100} // cache creation

	s := calculateTextQuotaSummary(ctx, ri, u)
	// baseTokens = 1000 - 100 = 900; +creation*1.5 = 150 => promptQuota 1050
	// (1050 + 0) * ratio(2) = 2100
	assert.Equal(t, 2100, s.Quota)
	assert.Equal(t, 100, s.CacheCreationTokens)
}

func TestBillmathCalculateTextQuotaSummary_ClaudeRatio(t *testing.T) {
	ctx := billmathCtx()
	pd := types.PriceData{
		UsePrice:        false,
		ModelRatio:      2,
		CompletionRatio: 3,
		CacheRatio:      0.5,
		GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
	}
	ri := billmathTextRelayInfo(pd, "billmath-claude-model", types.RelayFormatClaude)
	u := &dto.Usage{PromptTokens: 1000, CompletionTokens: 100}
	u.PromptTokensDetails = dto.InputTokenDetails{CachedTokens: 200}

	s := calculateTextQuotaSummary(ctx, ri, u)
	assert.True(t, s.IsClaudeUsageSemantic)
	// Claude: base NOT reduced by cache. base 1000 + cache 200*0.5=100 => 1100
	// completion 100*3 = 300 ; (1100+300)*2 = 2800
	assert.Equal(t, 2800, s.Quota)
}

func TestBillmathCalculateTextQuotaSummary_UsePrice(t *testing.T) {
	ctx := billmathCtx()
	pd := types.PriceData{
		UsePrice:       true,
		ModelPrice:     2.0,
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.5},
	}
	ri := billmathTextRelayInfo(pd, "billmath-text-model", types.RelayFormatOpenAI)
	u := &dto.Usage{PromptTokens: 1000, CompletionTokens: 200}

	s := calculateTextQuotaSummary(ctx, ri, u)
	// 2 * 500000 * 1.5 = 1500000
	assert.Equal(t, 1500000, s.Quota)
	assert.Equal(t, 1500000, s.LedgerQuota)
}

func TestBillmathCalculateTextQuotaSummary_UsePriceSaturation(t *testing.T) {
	ctx := billmathCtx()
	pd := types.PriceData{
		UsePrice:       true,
		ModelPrice:     1e6, // 1e6 * 500000 = 5e11 > MaxInt32
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	}
	ri := billmathTextRelayInfo(pd, "billmath-text-model", types.RelayFormatOpenAI)
	u := &dto.Usage{PromptTokens: 1, CompletionTokens: 0}

	s := calculateTextQuotaSummary(ctx, ri, u)
	assert.Equal(t, common.MaxQuota, s.Quota)
	require.NotNil(t, ri.QuotaClamp)
	assert.Equal(t, common.QuotaClampOverflow, ri.QuotaClamp.Kind)
}

func TestBillmathCalculateTextQuotaSummary_TotalTokensZeroNoSurcharge(t *testing.T) {
	ctx := billmathCtx()
	pd := types.PriceData{
		UsePrice:       false,
		ModelRatio:     2,
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	}
	ri := billmathTextRelayInfo(pd, "billmath-text-model", types.RelayFormatOpenAI)
	u := &dto.Usage{PromptTokens: 0, CompletionTokens: 0}

	s := calculateTextQuotaSummary(ctx, ri, u)
	assert.Equal(t, 0, s.TotalTokens)
	assert.Equal(t, 0, s.Quota)
	assert.Equal(t, 0, s.LedgerQuota)
}

func TestBillmathCalculateTextQuotaSummary_TotalTokensZeroWithSurcharge(t *testing.T) {
	ctx := billmathCtx()
	pd := types.PriceData{
		UsePrice:       false,
		ModelRatio:     2,
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	}
	// "search-preview" suffix drives a web-search surcharge (price 10).
	ri := billmathTextRelayInfo(pd, "billmath-search-preview", types.RelayFormatOpenAI)
	u := &dto.Usage{PromptTokens: 0, CompletionTokens: 0}

	s := calculateTextQuotaSummary(ctx, ri, u)
	// surcharge = 10/1000 * 1 * 500000 = 5000; computed quota = 5000.
	// TotalTokens==0 with non-zero surcharge => LedgerQuota keeps quota, Quota->0.
	assert.Equal(t, 0, s.Quota)
	assert.Equal(t, 5000, s.LedgerQuota)
	assert.Equal(t, 1, s.WebSearchCallCount)
}

// Guard: MaxQuota sanity so pinned saturation asserts stay meaningful.
func TestBillmathMaxQuotaSanity(t *testing.T) {
	assert.Equal(t, math.MaxInt32, common.MaxQuota)
	assert.Equal(t, 500000.0, common.QuotaPerUnit)
}
