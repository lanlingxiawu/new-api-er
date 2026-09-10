package service

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// attachQuotaSaturationToOther nests a quota saturation marker under
// other.admin_info.quota_saturation. Nesting under admin_info makes it
// admin-only for free, since model.formatUserLogs strips the whole admin_info
// object for non-admin viewers. Creates admin_info if absent. No-op when the
// clamp is nil (the common case: no saturation happened).
func attachQuotaSaturationToOther(other map[string]interface{}, clamp *common.QuotaClamp) {
	if clamp == nil || other == nil {
		return
	}
	adminInfo, ok := other["admin_info"].(map[string]interface{})
	if !ok || adminInfo == nil {
		adminInfo = map[string]interface{}{}
		other["admin_info"] = adminInfo
	}
	adminInfo["quota_saturation"] = clamp.AuditMap()
}

// attachQuotaSaturation records the request's quota clamp (if any) onto the
// consume log's other.admin_info and emits a request-correlated backend audit
// line. Called right before RecordConsumeLog on the text/audio/wss paths.
func attachQuotaSaturation(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, other map[string]interface{}) {
	if relayInfo == nil {
		return
	}
	clamp := relayInfo.QuotaClamp
	if clamp == nil {
		return
	}
	attachQuotaSaturationToOther(other, clamp)
	logger.LogWarn(ctx, fmt.Sprintf("quota saturation on consume log: op=%s kind=%s original=%g clamped=%d user=%d model=%s",
		clamp.Op, clamp.Kind, clamp.Original, clamp.Clamped, relayInfo.UserId, relayInfo.OriginModelName))
}

func appendRequestPath(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, other map[string]interface{}) {
	if other == nil {
		return
	}
	if ctx != nil && ctx.Request != nil && ctx.Request.URL != nil {
		if path := ctx.Request.URL.Path; path != "" {
			other["request_path"] = path
			return
		}
	}
	if relayInfo != nil && relayInfo.RequestURLPath != "" {
		path := relayInfo.RequestURLPath
		if idx := strings.Index(path, "?"); idx != -1 {
			path = path[:idx]
		}
		other["request_path"] = path
	}
}

// GenerateTextOtherInfo 生成文本消费日志的扩展字段，整合倍率、首字时间、订阅和流式诊断摘要。
// 参数 ctx：请求上下文；relayInfo：请求和结算元数据；modelRatio：模型倍率；groupRatio：本次分组倍率；completionRatio：输出倍率。
// 参数 cacheTokens：缓存读取 token 数；cacheRatio：缓存倍率；modelPrice：配置模型单价；userGroupRatio：用户专属分组倍率。
// 返回新建的日志字段映射；此时订阅可能尚未结算，严格流式结算后会刷新其中的订阅字段。
func GenerateTextOtherInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, modelRatio, groupRatio, completionRatio float64,
	cacheTokens int, cacheRatio float64, modelPrice float64, userGroupRatio float64) map[string]interface{} {
	other := make(map[string]interface{})
	other["model_ratio"] = modelRatio
	other["group_ratio"] = groupRatio
	other["completion_ratio"] = completionRatio
	other["cache_tokens"] = cacheTokens
	other["cache_ratio"] = cacheRatio
	other["model_price"] = modelPrice
	other["user_group_ratio"] = userGroupRatio
	firstResponseTime := relayInfo.FirstResponseTime.UnixMilli() - relayInfo.StartTime.UnixMilli()
	if firstResponseTime < 0 {
		firstResponseTime = 0
	}
	other["frt"] = float64(firstResponseTime)
	if relayInfo.ReasoningEffort != "" {
		other["reasoning_effort"] = relayInfo.ReasoningEffort
	}
	if relayInfo.IsModelMapped {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = relayInfo.UpstreamModelName
	}

	isSystemPromptOverwritten := common.GetContextKeyBool(ctx, constant.ContextKeySystemPromptOverride)
	if isSystemPromptOverwritten {
		other["is_system_prompt_overwritten"] = true
	}

	adminInfo := make(map[string]interface{})
	adminInfo["use_channel"] = ctx.GetStringSlice("use_channel")
	isMultiKey := common.GetContextKeyBool(ctx, constant.ContextKeyChannelIsMultiKey)
	if isMultiKey {
		adminInfo["is_multi_key"] = true
		adminInfo["multi_key_index"] = common.GetContextKeyInt(ctx, constant.ContextKeyChannelMultiKeyIndex)
	}

	isLocalCountTokens := common.GetContextKeyBool(ctx, constant.ContextKeyLocalCountTokens)
	if isLocalCountTokens {
		adminInfo["local_count_tokens"] = isLocalCountTokens
	}

	AppendChannelAffinityAdminInfo(ctx, adminInfo)

	other["admin_info"] = adminInfo
	appendRequestPath(ctx, relayInfo, other)
	appendRequestConversionChain(relayInfo, other)
	appendFinalRequestFormat(relayInfo, other)
	appendBillingInfo(relayInfo, other)
	appendParamOverrideInfo(relayInfo, other)
	appendStreamStatus(relayInfo, other)
	AppendClaudeStreamLogInfo(relayInfo, other)
	return other
}

// Private evidence is stripped at the log read boundary; only the Root detail
// API reads the stored envelope. Never pass this payload to application logging.
// AppendClaudeStreamLogInfo 向日志追加公开流式结算摘要和私有原始响应证据，私有部分只经超级管理员详情接口读取。
// 参数 info：请求中转状态，nil 时跳过；other：待原地扩充的日志映射，nil 时跳过。
// 无返回值；不把私有 envelope 输出到应用日志，不在此处执行结算。
func AppendClaudeStreamLogInfo(info *relaycommon.RelayInfo, other map[string]interface{}) {
	if info == nil || other == nil {
		return
	}
	if info.ClaudeStream == nil && info.ClaudeDiagnostic == nil && info.ClaudeRejectReason == "" {
		return
	}
	diagnostic := info.ClaudeDiagnostic.Snapshot()
	if info.StreamStatus != nil && info.StreamStatus.EndError != nil {
		diagnostic.Error = relaycommon.BoundedClaudeDiagnosticError(info.StreamStatus.EndError)
	}
	if info.ClaudeStream != nil {
		other["claude_stream"] = info.ClaudeStream
		diagnostic = info.ClaudeStream.Diagnostic
	}
	if info.ClaudeRejectReason != "" {
		diagnostic.RejectReason = info.ClaudeRejectReason
	}
	other["claude_diagnostic"] = diagnostic
	other["claude_diagnostic_attempt"] = diagnostic.Attempt
}

// Attach error-path diagnostics without enrolling legacy/non-200 handlers in
// strict streaming or settlement. The capture belongs to this attempt only.
// AppendClaudeErrorDiagnostic 为错误路径追加本次尝试的响应诊断，不把非流式、非 200 或兼容渠道纳入严格结算。
// 参数 c：含尝试采集器和策略原因的上下文；other：已初始化的日志映射，将原地更新；err：底层错误，nil 时不覆盖已有原因。
func AppendClaudeErrorDiagnostic(c *gin.Context, other map[string]interface{}, err error) {
	value, _ := c.Get(relaycommon.ClaudeResponseCaptureKey)
	capture, _ := value.(*relaycommon.ClaudeResponseCapture)
	reject := common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason)
	if capture == nil && reject == "" {
		return
	}
	capture.SetError(err)
	diagnostic := capture.Snapshot()
	diagnostic.RejectReason = reject
	other["claude_diagnostic"] = diagnostic
	other["claude_diagnostic_attempt"] = diagnostic.Attempt
}

// Keep low-level causes in Root evidence, not ordinary console/usage summaries.
// ClaudePublicErrorSummary 生成普通日志使用的错误摘要，Claude 响应专用模式仅公开状态码和错误码。
// 参数 c：请求上下文，用于读取响应专用标记；err：非 nil 的中转错误。
// 返回公开摘要字符串；底层错误与原始 body 由独立诊断保存。
func ClaudePublicErrorSummary(c *gin.Context, err *types.NewAPIError) string {
	if c.GetBool(relaycommon.ClaudeResponseOnlyKey) {
		return fmt.Sprintf("upstream response failed (status=%d, code=%s)", err.StatusCode, err.GetErrorCode())
	}
	return err.MaskSensitiveErrorWithStatusCode()
}

func appendParamOverrideInfo(relayInfo *relaycommon.RelayInfo, other map[string]interface{}) {
	if relayInfo == nil || other == nil || len(relayInfo.ParamOverrideAudit) == 0 {
		return
	}
	other["po"] = relayInfo.ParamOverrideAudit
}

// appendStreamStatus 生成公开的流状态字段，Claude 的底层错误细节仅保存在私有诊断中。
// 参数 relayInfo：流式状态及协议信息；other：原地写入的日志映射。任一为 nil、非流式或无状态时跳过。
func appendStreamStatus(relayInfo *relaycommon.RelayInfo, other map[string]interface{}) {
	if relayInfo == nil || other == nil || !relayInfo.IsStream || relayInfo.StreamStatus == nil {
		return
	}
	ss := relayInfo.StreamStatus
	privateErrors := relayInfo.RelayFormat == types.RelayFormatClaude || relayInfo.ClaudeDiagnostic != nil || relayInfo.ClaudeStream != nil
	status := "ok"
	if !ss.IsNormalEnd() || ss.HasErrors() {
		status = "error"
	}
	streamInfo := map[string]interface{}{
		"status":     status,
		"end_reason": string(ss.EndReason),
	}
	if ss.EndError != nil && !privateErrors {
		streamInfo["end_error"] = ss.EndError.Error()
	}
	if ss.ErrorCount > 0 {
		streamInfo["error_count"] = ss.ErrorCount
		if privateErrors {
			other["stream_status"] = streamInfo
			return
		}
		messages := make([]string, 0, len(ss.Errors))
		for _, e := range ss.Errors {
			messages = append(messages, e.Message)
		}
		streamInfo["errors"] = messages
	}
	other["stream_status"] = streamInfo
}

func appendBillingInfo(relayInfo *relaycommon.RelayInfo, other map[string]interface{}) {
	if relayInfo == nil || other == nil {
		return
	}
	// billing_source: "wallet" or "subscription"
	if relayInfo.BillingSource != "" {
		other["billing_source"] = relayInfo.BillingSource
	}
	if relayInfo.UserSetting.BillingPreference != "" {
		other["billing_preference"] = relayInfo.UserSetting.BillingPreference
	}
	if relayInfo.BillingSource == "subscription" {
		if relayInfo.SubscriptionId != 0 {
			other["subscription_id"] = relayInfo.SubscriptionId
		}
		if relayInfo.SubscriptionPreConsumed > 0 {
			other["subscription_pre_consumed"] = relayInfo.SubscriptionPreConsumed
		}
		// post_delta: settlement delta applied after actual usage is known (can be negative for refund)
		if relayInfo.SubscriptionPostDelta != 0 {
			other["subscription_post_delta"] = relayInfo.SubscriptionPostDelta
		}
		if relayInfo.SubscriptionPlanId != 0 {
			other["subscription_plan_id"] = relayInfo.SubscriptionPlanId
		}
		if relayInfo.SubscriptionPlanTitle != "" {
			other["subscription_plan_title"] = relayInfo.SubscriptionPlanTitle
		}
		// Compute "this request" subscription consumed + remaining
		consumed := relayInfo.SubscriptionPreConsumed + relayInfo.SubscriptionPostDelta
		usedFinal := relayInfo.SubscriptionAmountUsedAfterPreConsume + relayInfo.SubscriptionPostDelta
		if consumed < 0 {
			consumed = 0
		}
		if usedFinal < 0 {
			usedFinal = 0
		}
		if relayInfo.SubscriptionAmountTotal > 0 {
			remain := relayInfo.SubscriptionAmountTotal - usedFinal
			if remain < 0 {
				remain = 0
			}
			other["subscription_total"] = relayInfo.SubscriptionAmountTotal
			other["subscription_used"] = usedFinal
			other["subscription_remain"] = remain
		}
		if consumed > 0 {
			other["subscription_consumed"] = consumed
		}
		// Wallet quota is not deducted when billed from subscription.
		other["wallet_quota_deducted"] = 0
	}
}

func appendRequestConversionChain(relayInfo *relaycommon.RelayInfo, other map[string]interface{}) {
	if relayInfo == nil || other == nil {
		return
	}
	if len(relayInfo.RequestConversionChain) == 0 {
		return
	}
	chain := make([]string, 0, len(relayInfo.RequestConversionChain))
	for _, f := range relayInfo.RequestConversionChain {
		switch f {
		case types.RelayFormatOpenAI:
			chain = append(chain, "OpenAI Compatible")
		case types.RelayFormatClaude:
			chain = append(chain, "Claude Messages")
		case types.RelayFormatGemini:
			chain = append(chain, "Google Gemini")
		case types.RelayFormatOpenAIResponses:
			chain = append(chain, "OpenAI Responses")
		default:
			chain = append(chain, string(f))
		}
	}
	if len(chain) == 0 {
		return
	}
	other["request_conversion"] = chain
}

func appendFinalRequestFormat(relayInfo *relaycommon.RelayInfo, other map[string]interface{}) {
	if relayInfo == nil || other == nil {
		return
	}
	if relayInfo.GetFinalRequestRelayFormat() == types.RelayFormatClaude {
		// claude indicates the final upstream request format is Claude Messages.
		// Frontend log rendering uses this to keep the original Claude input display.
		other["claude"] = true
	}
}

func GenerateWssOtherInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.RealtimeUsage, modelRatio, groupRatio, completionRatio, audioRatio, audioCompletionRatio, modelPrice, userGroupRatio float64) map[string]interface{} {
	info := GenerateTextOtherInfo(ctx, relayInfo, modelRatio, groupRatio, completionRatio, 0, 0.0, modelPrice, userGroupRatio)
	info["ws"] = true
	info["audio_input"] = usage.InputTokenDetails.AudioTokens
	info["audio_output"] = usage.OutputTokenDetails.AudioTokens
	info["text_input"] = usage.InputTokenDetails.TextTokens
	info["text_output"] = usage.OutputTokenDetails.TextTokens
	info["audio_ratio"] = audioRatio
	info["audio_completion_ratio"] = audioCompletionRatio
	return info
}

func GenerateAudioOtherInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, modelRatio, groupRatio, completionRatio, audioRatio, audioCompletionRatio, modelPrice, userGroupRatio float64) map[string]interface{} {
	info := GenerateTextOtherInfo(ctx, relayInfo, modelRatio, groupRatio, completionRatio, 0, 0.0, modelPrice, userGroupRatio)
	info["audio"] = true
	info["audio_input"] = usage.PromptTokensDetails.AudioTokens
	info["audio_output"] = usage.CompletionTokenDetails.AudioTokens
	info["text_input"] = usage.PromptTokensDetails.TextTokens
	info["text_output"] = usage.CompletionTokenDetails.TextTokens
	info["audio_ratio"] = audioRatio
	info["audio_completion_ratio"] = audioCompletionRatio
	return info
}

func GenerateClaudeOtherInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, modelRatio, groupRatio, completionRatio float64,
	cacheTokens int, cacheRatio float64,
	cacheCreationTokens int, cacheCreationRatio float64,
	cacheCreationTokens5m int, cacheCreationRatio5m float64,
	cacheCreationTokens1h int, cacheCreationRatio1h float64,
	modelPrice float64, userGroupRatio float64) map[string]interface{} {
	info := GenerateTextOtherInfo(ctx, relayInfo, modelRatio, groupRatio, completionRatio, cacheTokens, cacheRatio, modelPrice, userGroupRatio)
	info["claude"] = true
	info["cache_creation_tokens"] = cacheCreationTokens
	info["cache_creation_ratio"] = cacheCreationRatio
	if cacheCreationTokens5m != 0 {
		info["cache_creation_tokens_5m"] = cacheCreationTokens5m
		info["cache_creation_ratio_5m"] = cacheCreationRatio5m
	}
	if cacheCreationTokens1h != 0 {
		info["cache_creation_tokens_1h"] = cacheCreationTokens1h
		info["cache_creation_ratio_1h"] = cacheCreationRatio1h
	}
	return info
}

func GenerateMjOtherInfo(relayInfo *relaycommon.RelayInfo, priceData hosttypes.PriceData) map[string]interface{} {
	other := make(map[string]interface{})
	other["model_price"] = priceData.ModelPrice
	other["group_ratio"] = priceData.GroupRatioInfo.GroupRatio
	if priceData.GroupRatioInfo.HasSpecialRatio {
		other["user_group_ratio"] = priceData.GroupRatioInfo.GroupSpecialRatio
	}
	appendRequestPath(nil, relayInfo, other)
	return other
}

// InjectTieredBillingInfo overlays tiered billing fields onto an existing
// module-specific other map. Call this after GenerateTextOtherInfo /
// GenerateClaudeOtherInfo / etc. when the request used tiered_expr billing.
func InjectTieredBillingInfo(other map[string]interface{}, relayInfo *relaycommon.RelayInfo, result *billingexpr.TieredResult) {
	if relayInfo == nil || other == nil {
		return
	}
	snap := relayInfo.TieredBillingSnapshot
	if snap == nil {
		return
	}
	other["billing_mode"] = "tiered_expr"
	other["expr_b64"] = base64.StdEncoding.EncodeToString([]byte(snap.ExprString))
	if result != nil {
		other["matched_tier"] = result.MatchedTier
	}
}
