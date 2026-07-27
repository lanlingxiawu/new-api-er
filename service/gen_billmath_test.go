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
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hosttypes "github.com/QuantumNous/new-api/types"

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
	got := CalcOpenRouterCacheCreateTokens(dto.Usage{PromptTokens: 500}, hosttypes.PriceData{
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
	pd := hosttypes.PriceData{
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
	pd := hosttypes.PriceData{
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

	// 逐工具字段已由 ToolSurchargeItems 统一承载。
	require.Len(t, s.ToolSurchargeItems, 1)
	assert.Equal(t, 1, s.ToolSurchargeItems[0].Count)
	assert.InDelta(t, 10.0, s.ToolSurchargeItems[0].Price, 1e-9)
	// 10/1000 * 1 * 500000 = 5000
	assert.InDelta(t, 5000.0, got.InexactFloat64(), 1e-6)
}

// 图像生成附加费改由 ResponsesUsageInfo 的工具调用计数驱动、单价走可配置工具定价，
// 不再读 context 里的 image_generation_call。该路径由 text_quota_test.go 的
// TestComposeTieredTextQuotaKeepsToolCallSurcharges 覆盖，这里只保留 Claude 检索。
func TestBillmathCalculateTextToolCallSurcharge_ClaudeWebSearch(t *testing.T) {
	ctx := billmathCtx()
	ctx.Set("claude_web_search_requests", 2)
	ri := billmathRelayInfo(types.RelayFormatOpenAI)
	s := &textQuotaSummary{ModelName: "billmath-plain-model", GroupRatio: 1.0}
	got := calculateTextToolCallSurcharge(ctx, ri, s)

	require.Len(t, s.ToolSurchargeItems, 1)
	assert.Equal(t, 2, s.ToolSurchargeItems[0].Count)
	assert.InDelta(t, 10.0, s.ToolSurchargeItems[0].Price, 1e-9)
	// 10/1000 * 1 * 500000 * 2 = 10000
	assert.InDelta(t, 10000.0, got.InexactFloat64(), 1e-6)
}

// ---------------------------------------------------------------------------
// text_quota.go — calculateTextQuotaSummary
// ---------------------------------------------------------------------------

func billmathTextRelayInfo(pd hosttypes.PriceData, model string, format types.RelayFormat) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: model,
		StartTime:       time.Now(),
		RelayFormat:     format,
		PriceData:       pd,
	}
}

func TestBillmathCalculateTextQuotaSummary_OpenAIRatio(t *testing.T) {
	ctx := billmathCtx()
	pd := hosttypes.PriceData{
		UsePrice:        false,
		ModelRatio:      2,
		CompletionRatio: 3,
		CacheRatio:      0.5,
		GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
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
	pd := hosttypes.PriceData{
		UsePrice:           false,
		ModelRatio:         2,
		CompletionRatio:    3,
		CacheCreationRatio: 1.5,
		GroupRatioInfo:     hosttypes.GroupRatioInfo{GroupRatio: 1},
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
	pd := hosttypes.PriceData{
		UsePrice:        false,
		ModelRatio:      2,
		CompletionRatio: 3,
		CacheRatio:      0.5,
		GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
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
	pd := hosttypes.PriceData{
		UsePrice:       true,
		ModelPrice:     2.0,
		GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1.5},
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
	pd := hosttypes.PriceData{
		UsePrice:       true,
		ModelPrice:     1e6, // 1e6 * 500000 = 5e11 > MaxInt32
		GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
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
	pd := hosttypes.PriceData{
		UsePrice:       false,
		ModelRatio:     2,
		GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
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
	pd := hosttypes.PriceData{
		UsePrice:       false,
		ModelRatio:     2,
		GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
	}
	// "search-preview" suffix drives a web-search surcharge (price 10).
	ri := billmathTextRelayInfo(pd, "billmath-search-preview", types.RelayFormatOpenAI)
	u := &dto.Usage{PromptTokens: 0, CompletionTokens: 0}

	s := calculateTextQuotaSummary(ctx, ri, u)
	// surcharge = 10/1000 * 1 * 500000 = 5000。零 token 但有工具附加费属于可计费
	// 用量，照常扣费；成本账与实扣一致。
	assert.Equal(t, 5000, s.Quota)
	assert.Equal(t, 5000, s.LedgerQuota)
	require.Len(t, s.ToolSurchargeItems, 1)
	assert.Equal(t, 1, s.ToolSurchargeItems[0].Count)
}

// Guard: MaxQuota sanity so pinned saturation asserts stay meaningful.
func TestBillmathMaxQuotaSanity(t *testing.T) {
	assert.Equal(t, math.MaxInt32, common.MaxQuota)
	assert.Equal(t, 500000.0, common.QuotaPerUnit)
}
