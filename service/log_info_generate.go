package service

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/aws/smithy-go"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
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
	// 恢复 bb6317462 的上下文原因记录，独立于流式处理、诊断采集和按钮资格。
	if reason := common.GetContextKeyString(ctx, constant.ContextKeyAdminRejectReason); reason != "" {
		other["reject_reason"] = reason
	}
	appendRequestPath(ctx, relayInfo, other)
	appendRequestConversionChain(relayInfo, other)
	appendFinalRequestFormat(relayInfo, other)
	appendBillingInfo(relayInfo, other)
	appendParamOverrideInfo(relayInfo, other)
	appendStreamStatus(relayInfo, other)
	AppendStreamLogInfo(relayInfo, other)
	return other
}

// Private evidence is stripped at the log read boundary; only the Root detail
// API reads the stored envelope. Never pass this payload to application logging.
// AppendStreamLogInfo 向日志追加公开流式结算摘要和私有原始响应证据，私有部分只经超级管理员详情接口读取。
// 参数 info：请求中转状态，nil 时跳过；other：待原地扩充的日志映射，nil 时跳过。
// 无返回值；新流程只在明确上游异常时保存原始响应，不把私有 envelope 输出到应用日志，不在此处执行结算。
func AppendStreamLogInfo(info *relaycommon.RelayInfo, other map[string]interface{}) {
	if info == nil || other == nil {
		return
	}
	delete(other, "claude_diagnostic_available")
	delete(other, "stream_diagnostic_available")
	if !info.StreamResponseGate.AllowsStream() {
		// 只排除新流程证据，策略拒绝原因仍按历史独立字段显示。
		if info.StreamRejectReason != "" {
			other["reject_reason"] = info.StreamRejectReason
		}
		delete(other, "stream_diagnostic")
		delete(other, "stream_diagnostic_attempt")
		delete(other, "stream_result")
		return
	}
	if info.StreamResult == nil && info.StreamDiagnostic == nil && info.StreamRejectReason == "" {
		return
	}
	diagnostic := info.StreamDiagnostic.Snapshot()
	if info.StreamStatus != nil && info.StreamStatus.EndError != nil {
		diagnostic.Error = relaycommon.BoundedStreamDiagnosticError(info.StreamStatus.EndError)
	}
	if info.StreamResult != nil {
		other["stream_result"] = info.StreamResult
		diagnostic = info.StreamResult.Diagnostic
	}
	if info.StreamRejectReason != "" {
		diagnostic.RejectReason = info.StreamRejectReason
	}
	if diagnostic.RejectReason != "" {
		other["reject_reason"] = diagnostic.RejectReason
	}
	available := info.StreamResult != nil && info.StreamResult.DiagnosticAvailable
	if available {
		other["stream_diagnostic_available"] = true
		// 终止 error 可能晚于结果快照写出；日志入队前取得当前正文，避免漏掉补发帧。
		diagnostic.DownstreamBodyBase64 = info.StreamDiagnostic.DownstreamBody()
	}
	if info.StreamResponseGate != nil {
		// 仅收紧新流程的日志副本；保留正常/客户端断开时的用量与错误，不改旧采集路径。
		diagnostic = filterStreamDiagnosticResponse(diagnostic, available && info.StreamDiagnostic != nil)
	}
	other["stream_diagnostic"] = diagnostic
	other["stream_diagnostic_attempt"] = diagnostic.Attempt
}

// Attach error-path diagnostics without enrolling legacy/non-200 handlers in
// strict streaming or settlement. The capture belongs to this attempt only.
// AppendStreamErrorDiagnostic 保存本次失败尝试的响应诊断和独立策略原因，仅新流式上游异常生成按钮标记，不执行结算。
// 参数 c：含尝试采集器和策略原因的上下文；other：已初始化的日志映射，将原地更新；err：底层错误，nil 时不覆盖已有原因。
func AppendStreamErrorDiagnostic(c *gin.Context, other map[string]interface{}, err error) {
	value, _ := c.Get(relaycommon.StreamResponseCaptureKey)
	capture, _ := value.(*relaycommon.StreamResponseCapture)
	reject := common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason)
	if reject != "" {
		other["reject_reason"] = reject
	}
	delete(other, "claude_diagnostic_available")
	delete(other, "stream_diagnostic_available")
	value, _ = c.Get(relaycommon.StreamSessionKey)
	session, _ := value.(*relaycommon.StreamSession)
	if session != nil && !session.ResponseGate.AllowsStream() {
		// 非 200、连接失败和响应头之前超时仅沿用原错误日志，不保存私有响应与用量。
		delete(other, "stream_diagnostic")
		delete(other, "stream_diagnostic_attempt")
		delete(other, "stream_result")
		return
	}
	available := session.DiagnosticAvailable(IsRelayRequestTimeout(c))
	if capture == nil && reject == "" && !available {
		return
	}
	capture.SetError(err)
	diagnostic := capture.Snapshot()
	if available {
		other["stream_diagnostic_available"] = true
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
	other["stream_diagnostic"] = diagnostic
	other["stream_diagnostic_attempt"] = diagnostic.Attempt
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

// Keep low-level causes in Root evidence; explicit upstream messages remain useful in ordinary logs.
// StreamPublicErrorSummary 优先保留明确上游消息，仅在响应专用模式缺少消息时使用状态码摘要。
// 参数 c：请求上下文，用于读取响应专用标记；err：非 nil 的中转错误。
// 返回脱敏后的公开摘要；底层原因、附加 metadata 与原始 body 由独立诊断保存。
func StreamPublicErrorSummary(c *gin.Context, err *types.NewAPIError) string {
	if c.GetBool(relaycommon.StreamResponseOnlyKey) {
		if message := err.UpstreamErrorMessage(); message != "" {
			message = common.MaskSensitiveInfo(message)
			if err.StatusCode == 0 {
				return message
			}
			return fmt.Sprintf("status_code=%d, %s", err.StatusCode, message)
		}
		return fmt.Sprintf("upstream response failed (status=%d, code=%s)", err.StatusCode, err.GetErrorCode())
	}
	return err.MaskSensitiveErrorWithStatusCode()
}

// StreamFailureLogMessage selects a public message once at stream termination.
// Only original error payload fields or a typed upstream API error qualify;
// transport errors and private diagnostic previews use the existing local hint.
// The caller excludes normal completion and client disconnects. Request IDs are
// added at the common log sink, not here, and no response/settlement state changes.
func StreamFailureLogMessage(snapshot relaycommon.StreamSnapshot) string {
	data := snapshot.ErrorPayload
	if len(snapshot.ErrorFrame) > 0 {
		_, data = relaycommon.StreamFramePayload(snapshot.ErrorFrame)
	}
	message := ""
	if len(data) > 0 {
		var response dto.GeneralErrorResponse
		if common.Unmarshal(data, &response) == nil {
			message = upstreamErrorMessage(response)
		}
		// A type mismatch in an unrelated DTO field must not hide a valid message.
		// Preserve field priority, only accept strings, and never salvage malformed JSON.
		if message == "" && gjson.ValidBytes(data) {
			value := gjson.ParseBytes(data)
			for _, path := range []string{
				"error.message", "error", "message", "msg", "err", "error_msg", "detail",
				"header.message", "response.error.message", "Response.Error.Message",
				"base_resp.status_msg", "output.message", "response.status_details.error.message",
				"last_error.msg",
			} {
				candidate := value.Get(path)
				if candidate.Type == gjson.String {
					message = common.StripRequestIds(candidate.Str)
					if message != "" {
						break
					}
				}
			}
		}
	}
	if message == "" {
		var apiErr *types.NewAPIError
		var sdkErr smithy.APIError
		switch {
		case errors.As(snapshot.Err, &apiErr):
			// An explicit wrapper (including hidden/empty provenance) takes precedence.
			message = apiErr.UpstreamErrorMessage()
		case errors.As(snapshot.Err, &sdkErr):
			// EventStream exceptions have no JSON error frame; keep the SDK cause untouched.
			message = common.StripRequestIds(sdkErr.ErrorMessage())
		}
	}
	if message == "" {
		return i18n.Translate(i18n.LangEn, i18n.MsgClaudeStreamFailed)
	}
	return common.MaskSensitiveInfo(message)
}

func appendParamOverrideInfo(relayInfo *relaycommon.RelayInfo, other map[string]interface{}) {
	if relayInfo == nil || other == nil || len(relayInfo.ParamOverrideAudit) == 0 {
		return
	}
	other["po"] = relayInfo.ParamOverrideAudit
}

// appendStreamStatus 生成公开流状态；有通用会话、流式结果或响应采集器时省略底层错误文本，旧路径按原逻辑保留。
// 参数 relayInfo：流式状态及协议信息；other：原地写入的日志映射。任一为 nil、非流式或无状态时跳过。
func appendStreamStatus(relayInfo *relaycommon.RelayInfo, other map[string]interface{}) {
	if relayInfo == nil || other == nil || !relayInfo.IsStream || relayInfo.StreamStatus == nil {
		return
	}
	ss := relayInfo.StreamStatus
	privateErrors := relayInfo.RelayFormat == types.RelayFormatClaude || relayInfo.StreamDiagnostic != nil || relayInfo.StreamResult != nil
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
