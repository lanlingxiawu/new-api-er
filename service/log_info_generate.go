package service

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
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
func attachQuotaSaturationToOther(other *model.LogOther, clamp *common.QuotaClamp) {
	if clamp == nil || other == nil {
		return
	}
	other.SetAdmin("quota_saturation", clamp.AuditMap())
}

// attachQuotaSaturation records the request's quota clamp (if any) onto the
// consume log's other.admin_info and emits a request-correlated backend audit
// line. Called right before RecordConsumeLog on the text/audio/wss paths.
func attachQuotaSaturation(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
	if relayInfo == nil {
		return
	}
	clamp := relayInfo.QuotaClamp
	if clamp == nil {
		return
	}
	attachQuotaSaturationToOther(other, clamp)
	logger.LogWarn(ctx, fmt.Sprintf("quota saturation on consume log: op=%s kind=%s original=%g clamped=%d user=%d model=%s",
		clamp.Op, clamp.Kind, clamp.Original, clamp.Clamped, relayInfo.UserId, relayInfo.GetBillingModelName()))
}

func appendRequestPath(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
	if other == nil {
		return
	}
	if ctx != nil && ctx.Request != nil && ctx.Request.URL != nil {
		if path := ctx.Request.URL.Path; path != "" {
			other.SetPublic("request_path", path)
			return
		}
	}
	if relayInfo != nil && relayInfo.RequestURLPath != "" {
		path := relayInfo.RequestURLPath
		if idx := strings.Index(path, "?"); idx != -1 {
			path = path[:idx]
		}
		other.SetPublic("request_path", path)
	}
}

// AppendRelayLogAdminInfo records relay routing and conversion diagnostics in
// the admin-only scope shared by successful and failed request logs.
func AppendRelayLogAdminInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
	if ctx == nil || other == nil {
		return
	}
	other.SetAdmin("use_channel", ctx.GetStringSlice("use_channel"))
	// 渠道名快照：渠道删除后 channels 表再也回答不了「这条日志跑在哪个渠道」，
	// 而佣金明细等页面必须显示渠道名。写进 admin_info 即自动对普通用户不可见。
	// 只读上下文/relayInfo 里已有的值，不新增任何查询（Rule 0）。
	if channelName := relayLogChannelName(ctx, relayInfo); channelName != "" {
		other.SetAdmin("channel_name", channelName)
	}
	if relayInfo != nil {
		if billingModel := relayInfo.GetBillingModelName(); billingModel != "" && billingModel != relayInfo.OriginModelName {
			other.SetAdmin("billing_model", billingModel)
		}
		if diagnostics := relayInfo.ConversionDiagnostics(); len(diagnostics) > 0 {
			other.SetAdmin("conversion_diagnostics", diagnostics)
		}
		if relayInfo.ConversionDiagnosticsTruncated() {
			other.SetAdmin("conversion_diagnostics_truncated", true)
		}
	}
	if common.GetContextKeyBool(ctx, constant.ContextKeyChannelIsMultiKey) {
		other.SetAdmin("is_multi_key", true)
		other.SetAdmin("multi_key_index", common.GetContextKeyInt(ctx, constant.ContextKeyChannelMultiKeyIndex))
	}
	if common.GetContextKeyBool(ctx, constant.ContextKeyLocalCountTokens) {
		other.SetAdmin("local_count_tokens", true)
	}

	AppendChannelAffinityAdminInfo(ctx, other)
}

// relayLogChannelName 取本次请求实际使用的渠道名；relayInfo 里的快照优先，
// 未构造 relayInfo 的早期错误日志退回 distributor 写入的上下文键。
// 两条来源都是内存读取，不新增数据库查询。
func relayLogChannelName(ctx *gin.Context, relayInfo *relaycommon.RelayInfo) string {
	if name := relayChannelName(relayInfo); name != "" {
		return name
	}
	if ctx == nil {
		return ""
	}
	return common.GetContextKeyString(ctx, constant.ContextKeyChannelName)
}

// GenerateTextOtherInfo 生成文本消费日志的扩展字段，整合倍率、首字时间、订阅和流式诊断摘要。
// 参数 ctx：请求上下文；relayInfo：请求和结算元数据；modelRatio：模型倍率；groupRatio：本次分组倍率；completionRatio：输出倍率。
// 参数 cacheTokens：缓存读取 token 数；cacheRatio：缓存倍率；modelPrice：配置模型单价；userGroupRatio：用户专属分组倍率。
// 返回新建的日志字段；此时订阅可能尚未结算，严格流式结算后会刷新其中的订阅字段。
func GenerateTextOtherInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, modelRatio, groupRatio, completionRatio float64,
	cacheTokens int, cacheRatio float64, modelPrice float64, userGroupRatio float64) *model.LogOther {
	other := model.NewLogOther()
	other.SetPublic("model_ratio", modelRatio)
	other.SetPublic("group_ratio", groupRatio)
	other.SetPublic("completion_ratio", completionRatio)
	other.SetPublic("cache_tokens", cacheTokens)
	other.SetPublic("cache_ratio", cacheRatio)
	other.SetPublic("model_price", modelPrice)
	// 只在确有专属分组倍率时才记。userGroupRatio 传的是 GroupRatioInfo.GroupSpecialRatio，
	// 没配专属倍率时它停在初值 -1；以前无条件写入，于是绝大多数日志里躺着一个
	// user_group_ratio: -1，导出该列会原样打印出来。倍率合法取值不小于 0，据此判断。
	// 任务计费路径（GenerateMjOtherInfo / task_billing）一直是按 HasSpecialRatio 加的守卫，
	// 这里与之对齐。注意有专属倍率时它与 group_ratio 同值——HandleGroupRatio 把专属倍率
	// 同时赋给两个字段，这一项的作用是标出「这条用的是专属价」，不是另一个乘数。
	if userGroupRatio >= 0 {
		other.SetPublic("user_group_ratio", userGroupRatio)
	}
	firstResponseTime := max(relayInfo.FirstResponseTime.UnixMilli()-relayInfo.StartTime.UnixMilli(), 0)
	other.SetPublic("frt", float64(firstResponseTime))
	if relayInfo.ReasoningEffort != "" {
		other.SetPublic("reasoning_effort", relayInfo.ReasoningEffort)
	}
	if relayInfo.IsModelMapped {
		other.SetPublic("is_model_mapped", true)
		other.SetPublic("upstream_model_name", relayInfo.UpstreamModelName)
	}

	isSystemPromptOverwritten := common.GetContextKeyBool(ctx, constant.ContextKeySystemPromptOverride)
	if isSystemPromptOverwritten {
		other.SetPublic("is_system_prompt_overwritten", true)
	}

	AppendRelayLogAdminInfo(ctx, relayInfo, other)
	// 恢复 bb6317462 的上下文原因记录，独立于流式处理、诊断采集和按钮资格。
	if reason := common.GetContextKeyString(ctx, constant.ContextKeyAdminRejectReason); reason != "" {
		other.SetAdmin("reject_reason", reason)
	}
	appendRequestPath(ctx, relayInfo, other)
	appendRequestConversionChain(relayInfo, other)
	appendFinalRequestFormat(relayInfo, other)
	appendBillingInfo(relayInfo, other)
	appendParamOverrideInfo(relayInfo, other)
	appendStreamStatus(relayInfo, other)
	appendStreamLogInfo(relayInfo, other, false)
	return other
}

// Private evidence is stripped at the log read boundary; only the Root detail
// API reads the stored envelope. Never pass this payload to application logging.
// AppendStreamLogInfo 向日志追加公开流式结算摘要和私有原始响应证据，私有部分只经超级管理员详情接口读取。
// 参数 info：请求中转状态，nil 时跳过；other：待原地刷新的日志字段，nil 时跳过。
// 无返回值；新流程只在明确上游异常时保存原始响应，不把私有 envelope 输出到应用日志，不在此处执行结算。
func AppendStreamLogInfo(info *relaycommon.RelayInfo, other *model.LogOther) {
	appendStreamLogInfo(info, other, true)
}

// appendStreamLogInfo 写入流式日志字段；refresh 为 true 时先移除上次写入的诊断字段，新建日志字段无需清理。
func appendStreamLogInfo(info *relaycommon.RelayInfo, other *model.LogOther, refresh bool) {
	if info == nil || other == nil {
		return
	}
	if !info.StreamResponseGate.AllowsStream() {
		if refresh {
			deleteLogOtherPublic(other, "claude_diagnostic_available", "stream_diagnostic_available",
				"stream_diagnostic", "stream_diagnostic_attempt", "stream_result")
		}
		// 只排除新流程证据，策略拒绝原因仍按历史独立字段显示。
		if info.StreamRejectReason != "" {
			other.SetAdmin("reject_reason", info.StreamRejectReason)
		}
		return
	}
	if refresh {
		deleteLogOtherPublic(other, "claude_diagnostic_available", "stream_diagnostic_available")
	}
	if info.StreamResult == nil && info.StreamDiagnostic == nil && info.StreamRejectReason == "" {
		return
	}
	diagnostic := info.StreamDiagnostic.Snapshot()
	if info.StreamStatus != nil && info.StreamStatus.EndError != nil {
		diagnostic.Error = relaycommon.BoundedStreamDiagnosticError(info.StreamStatus.EndError)
	}
	if info.StreamResult != nil {
		other.SetPublic("stream_result", info.StreamResult)
		diagnostic = info.StreamResult.Diagnostic
	}
	if info.StreamRejectReason != "" {
		diagnostic.RejectReason = info.StreamRejectReason
	}
	if diagnostic.RejectReason != "" {
		other.SetAdmin("reject_reason", diagnostic.RejectReason)
	}
	available := info.StreamResult != nil && info.StreamResult.DiagnosticAvailable
	if available {
		other.SetPublic("stream_diagnostic_available", true)
		// 终止 error 可能晚于结果快照写出；日志入队前取得当前正文，避免漏掉补发帧。
		diagnostic.DownstreamBodyBase64 = info.StreamDiagnostic.DownstreamBody()
	}
	if info.StreamResponseGate != nil {
		// 仅收紧新流程的日志副本；保留正常/客户端断开时的用量与错误，不改旧采集路径。
		diagnostic = filterStreamDiagnosticResponse(diagnostic, available && info.StreamDiagnostic != nil)
	}
	other.SetPublic("stream_diagnostic", diagnostic)
	other.SetPublic("stream_diagnostic_attempt", diagnostic.Attempt)
}

// Attach error-path diagnostics without enrolling legacy/non-200 handlers in
// strict streaming or settlement. The capture belongs to this attempt only.
// AppendStreamErrorDiagnostic 保存本次失败尝试的响应诊断和独立策略原因，仅新流式上游异常生成按钮标记，不执行结算。
// 参数 c：含尝试采集器和策略原因的上下文；other：已初始化的日志字段，将原地更新；err：底层错误，nil 时不覆盖已有原因。
func AppendStreamErrorDiagnostic(c *gin.Context, other *model.LogOther, err error) {
	if other == nil {
		return
	}
	value, _ := c.Get(relaycommon.StreamResponseCaptureKey)
	capture, _ := value.(*relaycommon.StreamResponseCapture)
	reject := common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason)
	if reject != "" {
		other.SetAdmin("reject_reason", reject)
	}
	value, _ = c.Get(relaycommon.StreamSessionKey)
	session, _ := value.(*relaycommon.StreamSession)
	if session != nil && !session.ResponseGate.AllowsStream() {
		// 非 200、连接失败和响应头之前超时仅沿用原错误日志，不保存私有响应与用量。
		deleteLogOtherPublic(other, "claude_diagnostic_available", "stream_diagnostic_available",
			"stream_diagnostic", "stream_diagnostic_attempt", "stream_result")
		return
	}
	deleteLogOtherPublic(other, "claude_diagnostic_available", "stream_diagnostic_available")
	available := session.DiagnosticAvailable(IsRelayRequestTimeout(c))
	if capture == nil && reject == "" && !available {
		return
	}
	capture.SetError(err)
	diagnostic := capture.Snapshot()
	if available {
		other.SetPublic("stream_diagnostic_available", true)
		diagnostic.DownstreamBodyBase64 = capture.DownstreamBody()
		// 关闭响应采集仍保留错误与尝试编号，使无 body 的诊断也能查询。
		diagnostic.Attempt = c.GetInt(relaycommon.StreamDiagnosticAttemptKey)
		if err != nil {
			diagnostic.Error = relaycommon.BoundedStreamDiagnosticError(err)
		}
	}
	diagnostic.RejectReason = reject
	if session != nil && session.ResponseGate != nil {
		// 错误日志也以明确上游来源为准，本地错误或提前记录日志不应保存原始响应。
		diagnostic = filterStreamDiagnosticResponse(diagnostic, available && capture != nil)
	}
	other.SetPublic("stream_diagnostic", diagnostic)
	other.SetPublic("stream_diagnostic_attempt", diagnostic.Attempt)
}

// deleteLogOtherPublic 从日志字段的公开层移除 keys，保留管理员、超级管理员与审计层；无匹配时不重建。
func deleteLogOtherPublic(other *model.LogOther, keys ...string) {
	if other == nil || len(keys) == 0 {
		return
	}
	snapshot := other.Snapshot()
	removed := false
	for _, key := range keys {
		if _, exists := snapshot[key]; exists {
			delete(snapshot, key)
			removed = true
		}
	}
	if !removed {
		return
	}
	rebuilt := model.NewLogOther()
	for key, value := range snapshot {
		scoped, _ := value.(map[string]any)
		switch key {
		case "admin_info":
			rebuilt.MergeAdmin(scoped)
		case "root_info":
			rebuilt.MergeRoot(scoped)
		case "audit_info":
			rebuilt.MergeAudit(scoped)
		default:
			rebuilt.SetPublic(key, value)
		}
	}
	*other = *rebuilt
}

// filterStreamDiagnosticResponse 为新流程日志过滤原始响应内容，保留独立错误、读取元数据、用量与结算证据。
// 参数 diagnostic 为按值传入的诊断快照，keepResponse 表示已确认上游异常且开启采集；返回可入队的副本。
// 历史响应只复制元数据切片后清空内容引用，不修改采集器或固定结果，以供后续异常/Realtime 轮次使用。
func filterStreamDiagnosticResponse(diagnostic relaycommon.StreamDiagnostic, keepResponse bool) relaycommon.StreamDiagnostic {
	if keepResponse {
		return diagnostic
	}
	diagnostic.ResponseHeaders = nil
	diagnostic.BodyHead = nil
	diagnostic.BodyTail = nil
	diagnostic.DownstreamBodyBase64 = nil
	if len(diagnostic.PreviousResponses) > 0 {
		previous := make([]relaycommon.StreamResponseSnapshot, len(diagnostic.PreviousResponses))
		copy(previous, diagnostic.PreviousResponses)
		for i := range previous {
			previous[i].ResponseHeaders = nil
			previous[i].BodyHead = nil
			previous[i].BodyTail = nil
		}
		diagnostic.PreviousResponses = previous
	}
	return diagnostic
}

// Keep low-level causes in Root evidence, not ordinary console/usage summaries.
// StreamPublicErrorSummary 生成普通日志使用的错误摘要，响应诊断专用模式仅公开状态码和错误码。
// 参数 c：请求上下文，用于读取响应专用标记；err：非 nil 的中转错误。
// 返回公开摘要字符串；底层错误与原始 body 由独立诊断保存。
func StreamPublicErrorSummary(c *gin.Context, err *types.NewAPIError) string {
	if c.GetBool(relaycommon.StreamResponseOnlyKey) {
		return fmt.Sprintf("upstream response failed (status=%d, code=%s)", err.StatusCode, err.GetErrorCode())
	}
	return err.MaskSensitiveErrorWithStatusCode()
}

func appendParamOverrideInfo(relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
	if relayInfo == nil || other == nil || len(relayInfo.ParamOverrideAudit) == 0 {
		return
	}
	other.SetPublic("po", relayInfo.ParamOverrideAudit)
}

// appendStreamStatus 生成公开流状态；有通用会话、流式结果或响应采集器时省略底层错误文本，旧路径按原逻辑保留。
// 参数 relayInfo：流式状态及协议信息；other：原地写入的日志字段。任一为 nil、非流式或无状态时跳过。
func appendStreamStatus(relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
	if relayInfo == nil || other == nil || !relayInfo.IsStream || relayInfo.StreamStatus == nil {
		return
	}
	ss := relayInfo.StreamStatus
	privateErrors := relayInfo.RelayFormat == types.RelayFormatClaude || relayInfo.StreamDiagnostic != nil || relayInfo.StreamResult != nil
	status := "ok"
	if !ss.IsNormalEnd() || ss.HasErrors() {
		status = "error"
	}
	streamInfo := map[string]any{
		"status":     status,
		"end_reason": string(ss.EndReason),
	}
	if ss.EndError != nil && !privateErrors {
		streamInfo["end_error"] = ss.EndError.Error()
	}
	if ss.ErrorCount > 0 {
		streamInfo["error_count"] = ss.ErrorCount
		if privateErrors {
			other.SetPublic("stream_status", streamInfo)
			return
		}
		messages := make([]string, 0, len(ss.Errors))
		for _, e := range ss.Errors {
			messages = append(messages, e.Message)
		}
		streamInfo["errors"] = messages
	}
	other.SetPublic("stream_status", streamInfo)
}

func appendBillingInfo(relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
	if relayInfo == nil || other == nil {
		return
	}
	// billing_source: "wallet" or "subscription"
	if relayInfo.BillingSource != "" {
		other.SetPublic("billing_source", relayInfo.BillingSource)
	}
	if relayInfo.UserSetting.BillingPreference != "" {
		other.SetPublic("billing_preference", relayInfo.UserSetting.BillingPreference)
	}
	if relayInfo.BillingSource == "subscription" {
		if relayInfo.SubscriptionId != 0 {
			other.SetPublic("subscription_id", relayInfo.SubscriptionId)
		}
		if relayInfo.SubscriptionPreConsumed > 0 {
			other.SetPublic("subscription_pre_consumed", relayInfo.SubscriptionPreConsumed)
		}
		// post_delta: settlement delta applied after actual usage is known (can be negative for refund)
		if relayInfo.SubscriptionPostDelta != 0 {
			other.SetPublic("subscription_post_delta", relayInfo.SubscriptionPostDelta)
		}
		if relayInfo.SubscriptionPlanId != 0 {
			other.SetPublic("subscription_plan_id", relayInfo.SubscriptionPlanId)
		}
		if relayInfo.SubscriptionPlanTitle != "" {
			other.SetPublic("subscription_plan_title", relayInfo.SubscriptionPlanTitle)
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
			remain := max(relayInfo.SubscriptionAmountTotal-usedFinal, 0)
			other.SetPublic("subscription_total", relayInfo.SubscriptionAmountTotal)
			other.SetPublic("subscription_used", usedFinal)
			other.SetPublic("subscription_remain", remain)
		}
		if consumed > 0 {
			other.SetPublic("subscription_consumed", consumed)
		}
		// Wallet quota is not deducted when billed from subscription.
		other.SetPublic("wallet_quota_deducted", 0)
	}
}

func appendRequestConversionChain(relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
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
	other.SetPublic("request_conversion", chain)
}

func appendFinalRequestFormat(relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
	if relayInfo == nil || other == nil {
		return
	}
	if relayInfo.GetFinalRequestRelayFormat() == types.RelayFormatClaude {
		// claude indicates the final upstream request format is Claude Messages.
		// Frontend log rendering uses this to keep the original Claude input display.
		other.SetPublic("claude", true)
	}
}

func GenerateWssOtherInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.RealtimeUsage, modelRatio, groupRatio, completionRatio, audioRatio, audioCompletionRatio, modelPrice, userGroupRatio float64) *model.LogOther {
	info := GenerateTextOtherInfo(ctx, relayInfo, modelRatio, groupRatio, completionRatio, 0, 0.0, modelPrice, userGroupRatio)
	info.SetPublic("ws", true)
	info.SetPublic("audio_input", usage.InputTokenDetails.AudioTokens)
	info.SetPublic("audio_output", usage.OutputTokenDetails.AudioTokens)
	info.SetPublic("text_input", usage.InputTokenDetails.TextTokens)
	info.SetPublic("text_output", usage.OutputTokenDetails.TextTokens)
	info.SetPublic("audio_ratio", audioRatio)
	info.SetPublic("audio_completion_ratio", audioCompletionRatio)
	return info
}

func GenerateAudioOtherInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, modelRatio, groupRatio, completionRatio, audioRatio, audioCompletionRatio, modelPrice, userGroupRatio float64) *model.LogOther {
	info := GenerateTextOtherInfo(ctx, relayInfo, modelRatio, groupRatio, completionRatio, 0, 0.0, modelPrice, userGroupRatio)
	info.SetPublic("audio", true)
	info.SetPublic("audio_input", usage.PromptTokensDetails.AudioTokens)
	info.SetPublic("audio_output", usage.CompletionTokenDetails.AudioTokens)
	info.SetPublic("text_input", usage.PromptTokensDetails.TextTokens)
	info.SetPublic("text_output", usage.CompletionTokenDetails.TextTokens)
	info.SetPublic("audio_ratio", audioRatio)
	info.SetPublic("audio_completion_ratio", audioCompletionRatio)
	return info
}

func GenerateClaudeOtherInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, modelRatio, groupRatio, completionRatio float64,
	cacheTokens int, cacheRatio float64,
	cacheCreationTokens int, cacheCreationRatio float64,
	cacheCreationTokens5m int, cacheCreationRatio5m float64,
	cacheCreationTokens1h int, cacheCreationRatio1h float64,
	modelPrice float64, userGroupRatio float64) *model.LogOther {
	info := GenerateTextOtherInfo(ctx, relayInfo, modelRatio, groupRatio, completionRatio, cacheTokens, cacheRatio, modelPrice, userGroupRatio)
	info.SetPublic("claude", true)
	info.SetPublic("cache_creation_tokens", cacheCreationTokens)
	info.SetPublic("cache_creation_ratio", cacheCreationRatio)
	if cacheCreationTokens5m != 0 {
		info.SetPublic("cache_creation_tokens_5m", cacheCreationTokens5m)
		info.SetPublic("cache_creation_ratio_5m", cacheCreationRatio5m)
	}
	if cacheCreationTokens1h != 0 {
		info.SetPublic("cache_creation_tokens_1h", cacheCreationTokens1h)
		info.SetPublic("cache_creation_ratio_1h", cacheCreationRatio1h)
	}
	return info
}

func GenerateMjOtherInfo(relayInfo *relaycommon.RelayInfo, priceData hosttypes.PriceData) *model.LogOther {
	other := model.NewLogOther()
	other.SetPublic("model_price", priceData.ModelPrice)
	other.SetPublic("group_ratio", priceData.GroupRatioInfo.GroupRatio)
	if priceData.GroupRatioInfo.HasSpecialRatio {
		other.SetPublic("user_group_ratio", priceData.GroupRatioInfo.GroupSpecialRatio)
	}
	appendRequestPath(nil, relayInfo, other)
	return other
}

// InjectTieredBillingInfo overlays tiered billing fields onto an existing
// module-specific other map. Call this after GenerateTextOtherInfo /
// GenerateClaudeOtherInfo / etc. when the request used tiered_expr billing.
func InjectTieredBillingInfo(other *model.LogOther, relayInfo *relaycommon.RelayInfo, result *billingexpr.TieredResult) {
	if relayInfo == nil || other == nil {
		return
	}
	snap := relayInfo.TieredBillingSnapshot
	if snap == nil {
		return
	}
	other.SetPublic("billing_mode", "tiered_expr")
	other.SetPublic("expr_b64", base64.StdEncoding.EncodeToString([]byte(snap.ExprString)))
	if result != nil {
		if tokens := result.BillingTokens; tokens != nil && result.BillingUnit == billingexpr.BillingUnitToken {
			other.SetPublic("image_cache_tokens", tokens.ImgCR)
			other.SetPublic("billing_tokens", map[string]float64{
				"p": tokens.P, "c": tokens.C, "len": tokens.Len,
				"cr": tokens.CR, "cc": tokens.CC, "cc1h": tokens.CC1h,
				"img": tokens.Img, "img_cr": tokens.ImgCR, "img_o": tokens.ImgO,
				"ai": tokens.AI, "ao": tokens.AO,
			})
		}
		if result.ImageCount != nil {
			other.SetPublic("image_count", *result.ImageCount)
		}
		other.SetPublic("matched_tier", result.MatchedTier)
		if result.BillingUnit != "" {
			other.SetPublic("billing_unit", result.BillingUnit)
		}
		if result.FixedPrice != nil {
			other.SetPublic("fixed_price", *result.FixedPrice)
		}
		if len(result.RequestRules) > 0 {
			other.SetPublic("request_rules", result.RequestRules)
		}
	} else if snap.EstimatedBillingUnit != "" {
		if snap.EstimatedImageCount != nil {
			other.SetPublic("image_count", *snap.EstimatedImageCount)
		}
		other.SetPublic("matched_tier", snap.EstimatedTier)
		other.SetPublic("billing_unit", snap.EstimatedBillingUnit)
		if snap.EstimatedFixedPrice != nil {
			other.SetPublic("fixed_price", *snap.EstimatedFixedPrice)
		}
	}
}
