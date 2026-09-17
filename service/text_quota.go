package service

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// ToolSurchargeItem is one billable tool-call line for consume logs.
type ToolSurchargeItem struct {
	Name  string  `json:"name"`
	Count int     `json:"count"`
	Price float64 `json:"price"`
}

func appendToolSurchargeLogInfo(other *model.LogOther, items []ToolSurchargeItem) {
	if len(items) == 0 {
		return
	}
	other.SetPublic("tool_surcharges", items)
}

type textQuotaSummary struct {
	PromptTokens          int
	CompletionTokens      int
	TotalTokens           int
	CacheTokens           int
	CacheCreationTokens   int
	CacheCreationTokens5m int
	CacheCreationTokens1h int
	ImageTokens           int
	// TextTokens 是输入里的纯文本部分，仅用于日志展示，不参与计费。
	// 上游（如 Azure images 的 input_tokens_details.text_tokens）报了就用它，
	// 没报时由 PromptTokens - ImageTokens 推导，让日志里
	// 「输入 = 文本输入 + 图像输入」这一关系可以自洽核对。
	TextTokens             int
	AudioTokens            int
	ModelName              string
	TokenName              string
	UseTimeSeconds         int64
	CompletionRatio        float64
	CacheRatio             float64
	ImageRatio             float64
	ModelRatio             float64
	GroupRatio             float64
	ModelPrice             float64
	CacheCreationRatio     float64
	CacheCreationRatio5m   float64
	CacheCreationRatio1h   float64
	Quota                  int
	IsClaudeUsageSemantic  bool
	UsageSemantic          string
	AudioInputPrice        float64
	ToolSurchargeItems     []ToolSurchargeItem
	ToolCallSurchargeQuota decimal.Decimal
	StreamCacheBillable    bool // 仅已有受管流式终态时允许纯缓存用量计费；旧路径不扩大收费资格。
	FixedPriceBilling      bool
	// LedgerQuota 是本仓库的成本记账口径，与实际向用户扣减的 Quota 分开：
	// 上游没有返回 usage 时不向用户计费（Quota 归零），但工具调用附加费仍要
	// 计入成本账。参见下方 buildTextQuotaSummary 末尾的赋值。
	LedgerQuota int
	// UpstreamBaseQuota 是渠道每日上限「上游消耗」口径的基础消耗：同一次用量按分组倍率 1 计算的
	// 额度（含其它倍率与工具附加费），与 Quota 同步清零。免费分组（倍率 0）时 Quota 为 0，只有它
	// 能还原上游实际消耗。见 docs/design/channel-limit-upstream-basis-and-timed-recovery.md §4.2。
	UpstreamBaseQuota int64
}

// hasBillableUsage 判断用量摘要是否含普通 token 或工具附加费；纯缓存资格只扩展到已有受管流式终态的摘要。
// 接收者 s：当前费用计算摘要；无参数；返回 true 表示存在可计费项目，最终是否收费仍受流式结算策略控制。
func (s *textQuotaSummary) hasBillableUsage() bool {
	return s.FixedPriceBilling || s.TotalTokens > 0 || !s.ToolCallSurchargeQuota.IsZero() ||
		s.StreamCacheBillable && (s.CacheTokens > 0 || s.CacheCreationTokens > 0 || s.CacheCreationTokens5m > 0 || s.CacheCreationTokens1h > 0)
}

// textInputTokensForLog returns the text-only portion of the input for logging.
// Not every upstream reports text_tokens alongside image_tokens, so fall back to
// PromptTokens - ImageTokens; a negative result means the upstream counts don't
// nest the way we assume, in which case 0 is the only honest answer.
func (s *textQuotaSummary) textInputTokensForLog() int {
	if s.TextTokens > 0 {
		return s.TextTokens
	}
	if remaining := s.PromptTokens - s.ImageTokens; remaining > 0 {
		return remaining
	}
	return 0
}

func cacheWriteTokensTotal(summary textQuotaSummary) int {
	if summary.CacheCreationTokens5m > 0 || summary.CacheCreationTokens1h > 0 {
		splitCacheWriteTokens := summary.CacheCreationTokens5m + summary.CacheCreationTokens1h
		if summary.CacheCreationTokens > splitCacheWriteTokens {
			return summary.CacheCreationTokens
		}
		return splitCacheWriteTokens
	}
	return summary.CacheCreationTokens
}

func isLegacyClaudeDerivedOpenAIUsage(relayInfo *relaycommon.RelayInfo, usage *dto.Usage) bool {
	if relayInfo == nil || usage == nil {
		return false
	}
	if relayInfo.GetFinalRequestRelayFormat() == types.RelayFormatClaude {
		return false
	}
	if usage.UsageSource != "" || usage.UsageSemantic != "" {
		return false
	}
	return usage.ClaudeCacheCreation5mTokens > 0 || usage.ClaudeCacheCreation1hTokens > 0
}

func collectToolSurchargeItem(items []ToolSurchargeItem, name string, count int, modelName string) []ToolSurchargeItem {
	if count <= 0 {
		return items
	}
	price := operation_setting.GetToolPriceForModel(name, modelName)
	if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return items
	}
	return append(items, ToolSurchargeItem{
		Name:  name,
		Count: count,
		Price: price,
	})
}

func mergeToolSurchargeItems(items []ToolSurchargeItem) []ToolSurchargeItem {
	if len(items) == 0 {
		return nil
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name == items[j].Name {
			return items[i].Price < items[j].Price
		}
		return items[i].Name < items[j].Name
	})

	merged := items[:0]
	for _, item := range items {
		lastIndex := len(merged) - 1
		if lastIndex >= 0 &&
			merged[lastIndex].Name == item.Name &&
			merged[lastIndex].Price == item.Price {
			if item.Count > math.MaxInt-merged[lastIndex].Count {
				common.SysError("tool surcharge call count overflow for " + item.Name)
				merged[lastIndex].Count = math.MaxInt
			} else {
				merged[lastIndex].Count += item.Count
			}
			continue
		}
		merged = append(merged, item)
	}
	return merged
}

func calculateTextToolCallSurcharge(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, summary *textQuotaSummary) decimal.Decimal {
	dGroupRatio := decimal.NewFromFloat(summary.GroupRatio)
	dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)

	var items []ToolSurchargeItem

	if relayInfo.ResponsesUsageInfo != nil {
		for name, tool := range relayInfo.ResponsesUsageInfo.BuiltInTools {
			if tool == nil {
				continue
			}
			items = collectToolSurchargeItem(items, name, tool.CallCount, summary.ModelName)
		}
	}
	if relayInfo.RelayMode != relayconstant.RelayModeResponses &&
		strings.HasSuffix(summary.ModelName, "search-preview") {
		items = collectToolSurchargeItem(items, dto.BuildInToolWebSearchPreview, 1, summary.ModelName)
	}

	items = collectToolSurchargeItem(
		items,
		dto.BuildInToolWebSearch,
		ctx.GetInt("claude_web_search_requests"),
		summary.ModelName,
	)

	if ctx.GetBool("gemini_google_search_call") {
		items = collectToolSurchargeItem(items, dto.BuildInToolGoogleSearch, 1, summary.ModelName)
	}

	summary.ToolSurchargeItems = mergeToolSurchargeItems(items)
	var surcharge decimal.Decimal
	for _, item := range summary.ToolSurchargeItems {
		surcharge = surcharge.Add(decimal.NewFromFloat(item.Price).
			Mul(decimal.NewFromInt(int64(item.Count))).
			Div(decimal.NewFromInt(1000)).
			Mul(dGroupRatio).
			Mul(dQuotaPerUnit))
	}

	return surcharge
}

// noteQuotaClamp records the first quota saturation event onto relayInfo so it
// can later be attached to the consume/task log for admin auditing. First
// non-nil clamp wins (a single request may hit multiple conversions).
func noteQuotaClamp(relayInfo *relaycommon.RelayInfo, clamp *common.QuotaClamp) {
	if clamp == nil || relayInfo == nil {
		return
	}
	if relayInfo.QuotaClamp == nil {
		relayInfo.QuotaClamp = clamp
	}
}

func composeTieredTextQuota(relayInfo *relaycommon.RelayInfo, summary textQuotaSummary, tieredQuota int, tieredResult *billingexpr.TieredResult) int {
	if summary.ToolCallSurchargeQuota.IsZero() {
		return tieredQuota
	}

	if tieredResult != nil {
		if snap := relayInfo.TieredBillingSnapshot; snap != nil {
			quota, clamp := common.QuotaFromDecimalChecked(decimal.NewFromFloat(tieredResult.ActualQuotaBeforeGroup).
				Mul(decimal.NewFromFloat(snap.GroupRatio)).
				Add(summary.ToolCallSurchargeQuota))
			noteQuotaClamp(relayInfo, clamp)
			return quota
		}
	}

	// Saturate the final sum, not just the surcharge: tieredQuota can be near
	// MaxQuota and adding the surcharge could push the total past the
	// single-request quota policy bound.
	total, clamp := common.QuotaFromDecimalChecked(
		decimal.NewFromInt(int64(tieredQuota)).Add(summary.ToolCallSurchargeQuota),
	)
	noteQuotaClamp(relayInfo, clamp)
	return total
}

// calculateTextQuotaSummary expects a usage already remapped by
// effectiveBillingUsage; PostTextConsumeQuota performs that remap once and shares
// the result with tiered billing, affinity observation and logging.
// 参数 ctx 提供请求计费上下文，relayInfo 提供价格及受管终态，usage 为已归一化用量；返回费用摘要，不执行资金扣减。
// 纯缓存收费资格以 StreamResult 为准，兼容原生 Claude 接管后停用通用会话；非受管请求维持原资格。
func calculateTextQuotaSummary(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage) textQuotaSummary {
	summary := textQuotaSummary{
		ModelName:            relayInfo.GetBillingModelName(),
		TokenName:            ctx.GetString("token_name"),
		UseTimeSeconds:       time.Now().Unix() - relayInfo.StartTime.Unix(),
		CompletionRatio:      relayInfo.PriceData.CompletionRatio,
		CacheRatio:           relayInfo.PriceData.CacheRatio,
		ImageRatio:           relayInfo.PriceData.ImageRatio,
		ModelRatio:           relayInfo.PriceData.ModelRatio,
		GroupRatio:           relayInfo.PriceData.GroupRatioInfo.GroupRatio,
		ModelPrice:           relayInfo.PriceData.ModelPrice,
		CacheCreationRatio:   relayInfo.PriceData.CacheCreationRatio,
		CacheCreationRatio5m: relayInfo.PriceData.CacheCreation5mRatio,
		CacheCreationRatio1h: relayInfo.PriceData.CacheCreation1hRatio,
		UsageSemantic:        usageSemanticFromUsage(relayInfo, usage),
		StreamCacheBillable:  relayInfo.StreamResult != nil,
	}
	summary.IsClaudeUsageSemantic = summary.UsageSemantic == "anthropic"

	if usage == nil {
		usage = &dto.Usage{
			PromptTokens:     relayInfo.GetEstimatePromptTokens(),
			CompletionTokens: 0,
			TotalTokens:      relayInfo.GetEstimatePromptTokens(),
		}
	}

	summary.PromptTokens = usage.PromptTokens
	summary.CompletionTokens = usage.CompletionTokens
	summary.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	summary.CacheTokens = usage.PromptTokensDetails.CachedTokens
	summary.CacheCreationTokens = usage.PromptTokensDetails.CacheCreationTokensTotal()
	summary.CacheCreationTokens5m = usage.ClaudeCacheCreation5mTokens
	summary.CacheCreationTokens1h = usage.ClaudeCacheCreation1hTokens
	summary.ImageTokens = usage.PromptTokensDetails.ImageTokens
	summary.TextTokens = usage.PromptTokensDetails.TextTokens
	summary.AudioTokens = usage.PromptTokensDetails.AudioTokens
	legacyClaudeDerived := isLegacyClaudeDerivedOpenAIUsage(relayInfo, usage)
	isOpenRouterClaudeBilling := relayInfo.ChannelMeta != nil &&
		relayInfo.ChannelType == constant.ChannelTypeOpenRouter &&
		summary.IsClaudeUsageSemantic

	if isOpenRouterClaudeBilling {
		summary.PromptTokens -= summary.CacheTokens
		isUsingCustomSettings := relayInfo.PriceData.UsePrice || hasCustomModelRatio(summary.ModelName, relayInfo.PriceData.ModelRatio)
		if summary.CacheCreationTokens == 0 && relayInfo.PriceData.CacheCreationRatio != 1 && usage.Cost != 0 && !isUsingCustomSettings {
			maybeCacheCreationTokens := CalcOpenRouterCacheCreateTokens(*usage, relayInfo.PriceData)
			if maybeCacheCreationTokens >= 0 && summary.PromptTokens >= maybeCacheCreationTokens {
				summary.CacheCreationTokens = maybeCacheCreationTokens
			}
		}
		summary.PromptTokens -= summary.CacheCreationTokens
	}

	dPromptTokens := decimal.NewFromInt(int64(summary.PromptTokens))
	dCacheTokens := decimal.NewFromInt(int64(summary.CacheTokens))
	dImageTokens := decimal.NewFromInt(int64(summary.ImageTokens))
	dAudioTokens := decimal.NewFromInt(int64(summary.AudioTokens))
	dCompletionTokens := decimal.NewFromInt(int64(summary.CompletionTokens))
	dCachedCreationTokens := decimal.NewFromInt(int64(summary.CacheCreationTokens))
	dCompletionRatio := decimal.NewFromFloat(summary.CompletionRatio)
	dCacheRatio := decimal.NewFromFloat(summary.CacheRatio)
	dImageRatio := decimal.NewFromFloat(summary.ImageRatio)
	dModelRatio := decimal.NewFromFloat(summary.ModelRatio)
	dGroupRatio := decimal.NewFromFloat(summary.GroupRatio)
	dModelPrice := decimal.NewFromFloat(summary.ModelPrice)
	dCacheCreationRatio := decimal.NewFromFloat(summary.CacheCreationRatio)
	dCacheCreationRatio5m := decimal.NewFromFloat(summary.CacheCreationRatio5m)
	dCacheCreationRatio1h := decimal.NewFromFloat(summary.CacheCreationRatio1h)
	dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)

	ratio := dModelRatio.Mul(dGroupRatio)
	summary.ToolCallSurchargeQuota = calculateTextToolCallSurcharge(ctx, relayInfo, &summary)

	var audioInputQuota decimal.Decimal
	// audioInputBase / upstreamBase：同一份用量按分组倍率 1 计算，供每日上限的上游消耗口径使用。
	var audioInputBase decimal.Decimal
	var upstreamBase decimal.Decimal
	if !relayInfo.PriceData.UsePrice {
		baseTokens := dPromptTokens

		var cachedTokensWithRatio decimal.Decimal
		if !dCacheTokens.IsZero() {
			if !summary.IsClaudeUsageSemantic && !legacyClaudeDerived {
				baseTokens = baseTokens.Sub(dCacheTokens)
			}
			cachedTokensWithRatio = dCacheTokens.Mul(dCacheRatio)
		}

		var cachedCreationTokensWithRatio decimal.Decimal
		hasSplitCacheCreationTokens := summary.CacheCreationTokens5m > 0 || summary.CacheCreationTokens1h > 0
		if !dCachedCreationTokens.IsZero() || hasSplitCacheCreationTokens {
			if !summary.IsClaudeUsageSemantic && !legacyClaudeDerived {
				baseTokens = baseTokens.Sub(dCachedCreationTokens)
				cachedCreationTokensWithRatio = dCachedCreationTokens.Mul(dCacheCreationRatio)
			} else {
				remaining := max(summary.CacheCreationTokens-summary.CacheCreationTokens5m-summary.CacheCreationTokens1h, 0)
				cachedCreationTokensWithRatio = decimal.NewFromInt(int64(remaining)).Mul(dCacheCreationRatio)
				cachedCreationTokensWithRatio = cachedCreationTokensWithRatio.Add(decimal.NewFromInt(int64(summary.CacheCreationTokens5m)).Mul(dCacheCreationRatio5m))
				cachedCreationTokensWithRatio = cachedCreationTokensWithRatio.Add(decimal.NewFromInt(int64(summary.CacheCreationTokens1h)).Mul(dCacheCreationRatio1h))
			}
		}

		var imageTokensWithRatio decimal.Decimal
		if !dImageTokens.IsZero() {
			baseTokens = baseTokens.Sub(dImageTokens)
			imageTokensWithRatio = dImageTokens.Mul(dImageRatio)
		}

		if !dAudioTokens.IsZero() {
			summary.AudioInputPrice = operation_setting.GetGeminiInputAudioPricePerMillionTokens(summary.ModelName)
			if summary.AudioInputPrice > 0 {
				baseTokens = baseTokens.Sub(dAudioTokens)
				audioInputBase = decimal.NewFromFloat(summary.AudioInputPrice).
					Div(decimal.NewFromInt(1000000)).Mul(dAudioTokens).Mul(dQuotaPerUnit)
				audioInputQuota = audioInputBase.Mul(dGroupRatio)
			}
		}

		// OpenAI cache-write usage reports unadjusted prefix counts, so
		// cached_tokens + cache_write_tokens can exceed prompt_tokens and the
		// remainder can go negative. Clamp at zero so overlap never turns into
		// a negative base charge.
		if baseTokens.IsNegative() {
			baseTokens = decimal.Zero
		}

		promptQuota := baseTokens.Add(cachedTokensWithRatio).Add(imageTokensWithRatio).Add(cachedCreationTokensWithRatio)
		completionQuota := dCompletionTokens.Mul(dCompletionRatio)
		upstreamBase = promptQuota.Add(completionQuota).Mul(dModelRatio).Add(audioInputBase)
		quotaCalculateDecimal := promptQuota.Add(completionQuota).Mul(ratio)
		quotaCalculateDecimal = quotaCalculateDecimal.Add(audioInputQuota)
		quotaCalculateDecimal = relayInfo.PriceData.ApplyOtherRatiosToDecimal(quotaCalculateDecimal)
		quotaCalculateDecimal = quotaCalculateDecimal.Add(summary.ToolCallSurchargeQuota)

		if !ratio.IsZero() && quotaCalculateDecimal.LessThanOrEqual(decimal.Zero) {
			quotaCalculateDecimal = decimal.NewFromInt(1)
		}
		quota, clamp := common.QuotaFromDecimalChecked(quotaCalculateDecimal)
		summary.Quota = quota
		noteQuotaClamp(relayInfo, clamp)
	} else {
		upstreamBase = dModelPrice.Mul(dQuotaPerUnit).Add(audioInputBase)
		quotaCalculateDecimal := dModelPrice.Mul(dQuotaPerUnit).Mul(dGroupRatio)
		quotaCalculateDecimal = quotaCalculateDecimal.Add(audioInputQuota)
		quotaCalculateDecimal = relayInfo.PriceData.ApplyOtherRatiosToDecimal(quotaCalculateDecimal)
		quotaCalculateDecimal = quotaCalculateDecimal.Add(summary.ToolCallSurchargeQuota)
		quota, clamp := common.QuotaFromDecimalChecked(quotaCalculateDecimal)
		summary.Quota = quota
		noteQuotaClamp(relayInfo, clamp)
	}

	// 判定是否计费看的是「有没有产生可计费用量」，而不是单看 token 数：
	// /v1/alpha/search 这类请求不返回 usage，但确实调用了一次 web_search，
	// 附加费必须照常向用户扣。真正不该扣费的是上游超时、什么都没返回的情况，
	// 那时 token 与附加费同时为零。
	if !summary.hasBillableUsage() {
		summary.Quota = 0
	} else if !ratio.IsZero() && summary.Quota == 0 {
		summary.Quota = 1
	}
	// 成本账与实扣口径一致：只要计了费就记账。
	if summary.hasBillableUsage() {
		summary.LedgerQuota = summary.Quota
		upstreamBase = relayInfo.PriceData.ApplyOtherRatiosToDecimal(upstreamBase).
			Add(toolSurchargeBase(summary.ToolSurchargeItems))
		summary.UpstreamBaseQuota = int64(common.QuotaFromDecimal(upstreamBase))
	}

	return summary
}

// toolSurchargeBase 求工具附加费不乘分组倍率的基础额度，供每日上限的上游消耗口径使用。
func toolSurchargeBase(items []ToolSurchargeItem) decimal.Decimal {
	var total decimal.Decimal
	for _, item := range items {
		total = total.Add(decimal.NewFromFloat(item.Price).
			Mul(decimal.NewFromInt(int64(item.Count))).
			Div(decimal.NewFromInt(1000)).
			Mul(decimal.NewFromFloat(common.QuotaPerUnit)))
	}
	return total
}

// tieredUpstreamBaseQuota 阶梯表达式计费时的基础消耗：表达式在乘分组倍率之前的额度加工具附加费。
func tieredUpstreamBaseQuota(summary textQuotaSummary, tieredResult *billingexpr.TieredResult) int64 {
	if tieredResult == nil || !summary.hasBillableUsage() {
		return summary.UpstreamBaseQuota
	}
	return int64(common.QuotaFromDecimal(decimal.NewFromFloat(tieredResult.ActualQuotaBeforeGroup).
		Add(toolSurchargeBase(summary.ToolSurchargeItems))))
}

func usageSemanticFromUsage(relayInfo *relaycommon.RelayInfo, usage *dto.Usage) string {
	if usage != nil && usage.UsageSemantic != "" {
		return usage.UsageSemantic
	}
	if relayInfo != nil && relayInfo.GetFinalRequestRelayFormat() == types.RelayFormatClaude {
		return "anthropic"
	}
	return "openai"
}

// PostTextConsumeQuota 根据用量和既有价格规则计算费用并发起最终结算，严格流式无收费来源时清零全部费用项。
// 参数 ctx：请求及日志上下文；relayInfo：计费配置、预扣和流式结果；usage：解析或估算的用量，nil 时使用既有请求估算。
// 参数 extraContent：消费日志的附加说明，可为 nil。已尝试严格结算的请求直接退出以避免重复收费。
func PostTextConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, extraContent []string) {
	usage = FinalizeStreamUsage(ctx, relayInfo, usage)
	if relayInfo.StreamResult != nil && relayInfo.StreamResult.SettlementAttempted {
		return
	}
	originUsage := usage
	billingUsage := effectiveBillingUsage(usage)
	if usage == nil {
		extraContent = append(extraContent, "上游无计费信息")
	}
	if originUsage != nil {
		ObserveChannelAffinityUsageCacheByRelayFormat(ctx, billingUsage, relayInfo.GetFinalRequestRelayFormat())
	}

	relayInfo.StreamRejectReason = common.GetContextKeyString(ctx, constant.ContextKeyAdminRejectReason)
	summary := calculateTextQuotaSummary(ctx, relayInfo, billingUsage)

	var tieredResult *billingexpr.TieredResult
	var tieredTokens billingexpr.TokenParams
	tieredBillingApplied := false
	snap := relayInfo.TieredBillingSnapshot
	// Providers normally estimate missing usage before settlement. Preserve the
	// same prompt estimate when a fixed-price expression reaches us without it;
	// its conditions must still run and may select a token-priced fallback.
	if billingUsage == nil && snap != nil && billingexpr.UsesFixedPricingByHash(snap.ExprString, snap.ExprHash) {
		billingUsage = &dto.Usage{PromptTokens: summary.PromptTokens, CompletionTokens: summary.CompletionTokens, TotalTokens: summary.TotalTokens}
	}
	if billingUsage != nil {
		var tieredUsedVars map[string]bool
		if snap := relayInfo.TieredBillingSnapshot; snap != nil {
			tieredUsedVars = billingexpr.UsedVarsByHash(snap.ExprString, snap.ExprHash)
		}
		tieredTokens = BuildTieredTokenParams(billingUsage, summary.IsClaudeUsageSemantic, tieredUsedVars)
		tieredOk, tieredQuota, tieredRes := TryTieredSettle(relayInfo, tieredTokens)
		if tieredOk {
			tieredBillingApplied = true
			tieredResult = tieredRes
			summary.Quota = composeTieredTextQuota(relayInfo, summary, tieredQuota, tieredRes)
			summary.FixedPriceBilling = isFixedPriceSettlement(relayInfo, tieredRes)
			if summary.FixedPriceBilling {
				summary.AudioInputPrice = 0
				if summary.LedgerQuota == 0 {
					summary.LedgerQuota = summary.Quota
				}
			}
			summary.UpstreamBaseQuota = tieredUpstreamBaseQuota(summary, tieredRes)
		}
	}

	if stream := relayInfo.StreamResult; stream != nil && stream.UsageSource == "none" {
		// 在费用展示及日志生成前执行免收费策略，按次、阶梯和工具附加费一并清零。
		summary.Quota = 0
		summary.LedgerQuota = 0
		summary.UpstreamBaseQuota = 0
		summary.ToolCallSurchargeQuota = decimal.Zero
		summary.ToolSurchargeItems = nil
	}
	for _, item := range summary.ToolSurchargeItems {
		q := decimal.NewFromFloat(item.Price).
			Mul(decimal.NewFromInt(int64(item.Count))).
			Div(decimal.NewFromInt(1000)).
			Mul(decimal.NewFromFloat(summary.GroupRatio)).
			Mul(decimal.NewFromFloat(common.QuotaPerUnit))
		extraContent = append(extraContent, fmt.Sprintf(
			"%s 调用 %d 次，调用花费 %s",
			item.Name,
			item.Count,
			logger.LogQuota(common.QuotaFromDecimal(q)),
		))
	}
	if summary.AudioInputPrice > 0 && summary.AudioTokens > 0 {
		q := decimal.NewFromFloat(summary.AudioInputPrice).Div(decimal.NewFromInt(1000000)).Mul(decimal.NewFromInt(int64(summary.AudioTokens))).Mul(decimal.NewFromFloat(summary.GroupRatio)).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
		extraContent = append(extraContent, fmt.Sprintf("Audio Input 花费 %s", logger.LogQuota(common.QuotaFromDecimal(q))))
	}

	// 与 buildTextQuotaSummary 的计费口径保持一致：零 token 但有工具附加费时
	// 仍然扣费，不该打「无法扣费」的日志。
	countUsage := summary.hasBillableUsage()
	if relayInfo.StreamResult != nil && relayInfo.StreamResult.UsageSource == "none" {
		countUsage = false
	}
	// 严格流式零收费由 stream_result 解释；即使上游有用量，也可能因没有有效交付而释放预扣。
	if !countUsage && relayInfo.StreamResult == nil {
		extraContent = append(extraContent, "上游没有返回计费信息，无法扣费（可能是上游超时）")
		logger.LogError(ctx, fmt.Sprintf("total tokens is 0, cannot consume quota, userId %d, channelId %d, tokenId %d, model %s， pre-consumed quota %d", relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, summary.ModelName, relayInfo.FinalPreConsumedQuota))
	}

	logModel := summary.ModelName
	if strings.HasPrefix(logModel, "gpt-4-gizmo") {
		logModel = "gpt-4-gizmo-*"
		extraContent = append(extraContent, fmt.Sprintf("模型 %s", summary.ModelName))
	}
	if strings.HasPrefix(logModel, "gpt-4o-gizmo") {
		logModel = "gpt-4o-gizmo-*"
		extraContent = append(extraContent, fmt.Sprintf("模型 %s", summary.ModelName))
	}

	logContent := strings.Join(extraContent, ", ")
	var other *model.LogOther
	if summary.IsClaudeUsageSemantic {
		other = GenerateClaudeOtherInfo(ctx, relayInfo,
			summary.ModelRatio, summary.GroupRatio, summary.CompletionRatio,
			summary.CacheTokens, summary.CacheRatio,
			summary.CacheCreationTokens, summary.CacheCreationRatio,
			summary.CacheCreationTokens5m, summary.CacheCreationRatio5m,
			summary.CacheCreationTokens1h, summary.CacheCreationRatio1h,
			summary.ModelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
		other.SetPublic("usage_semantic", "anthropic")
	} else {
		other = GenerateTextOtherInfo(ctx, relayInfo, summary.ModelRatio, summary.GroupRatio, summary.CompletionRatio, summary.CacheTokens, summary.CacheRatio, summary.ModelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	}
	appendUsageBillingPathForLog(other, common.GetContextKeyBool(ctx, constant.ContextKeyLocalCountTokens), originUsage)
	if summary.ImageTokens != 0 {
		other.SetPublic("image", true)
		other.SetPublic("image_ratio", summary.ImageRatio)
		// 键名 image_output 是历史遗留：存的是输入图像 token（见 summary.ImageTokens 的
		// 赋值来源 usage.PromptTokensDetails.ImageTokens）。键不能改，历史日志和用户
		// 已保存的导出列偏好都引用它；展示文案已在导出列与详情弹窗中更正为「图像输入」。
		other.SetPublic("image_output", summary.ImageTokens)
		// 同时给出文本输入，让「输入 = 文本输入 + 图像输入」在日志里可直接核对。
		other.SetPublic("text_input", summary.textInputTokensForLog())
	}
	appendToolSurchargeLogInfo(other, summary.ToolSurchargeItems)
	if summary.AudioInputPrice > 0 && summary.AudioTokens > 0 {
		other.SetPublic("audio_input_seperate_price", true)
		other.SetPublic("audio_input_token_count", summary.AudioTokens)
		other.SetPublic("audio_input_price", summary.AudioInputPrice)
	}
	if summary.CacheCreationTokens > 0 {
		other.SetPublic("cache_creation_tokens", summary.CacheCreationTokens)
		other.SetPublic("cache_creation_ratio", summary.CacheCreationRatio)
	}
	if summary.CacheCreationTokens5m > 0 {
		other.SetPublic("cache_creation_tokens_5m", summary.CacheCreationTokens5m)
		other.SetPublic("cache_creation_ratio_5m", summary.CacheCreationRatio5m)
	}
	if summary.CacheCreationTokens1h > 0 {
		other.SetPublic("cache_creation_tokens_1h", summary.CacheCreationTokens1h)
		other.SetPublic("cache_creation_ratio_1h", summary.CacheCreationRatio1h)
	}
	cacheWriteTokens := cacheWriteTokensTotal(summary)
	if cacheWriteTokens > 0 {
		// cache_write_tokens: normalized cache creation total for UI display.
		// If split 5m/1h values are present, this is their sum; otherwise it falls back
		// to cache_creation_tokens.
		other.SetPublic("cache_write_tokens", cacheWriteTokens)
	}
	if relayInfo.GetFinalRequestRelayFormat() != types.RelayFormatClaude && billingUsage != nil && billingUsage.UsageSource != "" && billingUsage.InputTokens > 0 {
		// input_tokens_total: explicit normalized total input used by the usage log UI.
		// Only write this field when upstream/current conversion has already provided a
		// reliable total input value and tagged the usage source. Do not infer it from
		// prompt/cache fields here, otherwise old upstream payloads may be double-counted.
		other.SetPublic("input_tokens_total", billingUsage.InputTokens)
	}
	if tieredBillingApplied {
		InjectTieredBillingInfo(other, relayInfo, tieredResult)

	}

	attachQuotaSaturation(ctx, relayInfo, other)

	FinalizeConsumptionSettlement(ctx, relayInfo, ConsumptionSettlementParams{
		ChannelId:         relayInfo.ChannelId,
		PromptTokens:      summary.PromptTokens,
		CompletionTokens:  summary.CompletionTokens,
		ModelName:         logModel,
		TokenName:         summary.TokenName,
		Quota:             summary.Quota,
		Content:           logContent,
		TokenId:           relayInfo.TokenId,
		UseTimeSeconds:    int(summary.UseTimeSeconds),
		IsStream:          relayInfo.IsStream,
		Group:             relayInfo.UsingGroup,
		Other:             other,
		CountUsage:        countUsage,
		SurchargeQuota:    int64(summary.ToolCallSurchargeQuota.Round(0).IntPart()),
		LedgerQuota:       summary.LedgerQuota,
		UpstreamBaseQuota: &summary.UpstreamBaseQuota,
	})
	gopool.Go(func() {
		perfmetrics.RecordRelaySample(relayInfo, relayInfo.StreamResult == nil || !relayInfo.StreamResult.Failed, int64(summary.CompletionTokens))
	})
}
