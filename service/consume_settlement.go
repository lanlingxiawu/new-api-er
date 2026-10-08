package service

import (
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// ConsumptionSettlementParams 汇总终止结算及日志入队所需数据，额度单位为系统内部 quota，不是货币元。
type ConsumptionSettlementParams struct {
	ChannelId        int             // 日志所属渠道；0 时从 relayInfo 补入。
	PromptTokens     int             // 最终计费输入 token 数。
	CompletionTokens int             // 最终计费输出 token 数。
	ModelName        string          // 日志模型名；空时使用请求原模型名。
	TokenName        string          // 调用令牌的展示名称，不含令牌密钥。
	Quota            int             // 最终用户收费额度；严格流式为 0 时释放预扣。
	Content          string          // 日志公开说明，不放原始响应或底层私有错误。
	TokenId          int             // 调用令牌 ID；0 时从请求信息补入。
	UseTimeSeconds   int             // 请求耗时，单位秒。
	IsStream         bool            // 本次是否为流式请求。
	Group            string          // 实际计费分组；空时使用请求当前分组。
	Other            *model.LogOther // 待序列化日志扩展字段，结算会刷新其中的订阅和流式信息。
	CreatedAt        int64           // 日志创建时间，Unix 秒，传递给既有日志管线。
	CountUsage       bool            // 是否更新用户/渠道消费统计；严格免收费或资金失败时关闭。
	LedgerQuota      int             // 成本/佣金使用的额度口径；0 时日志快照逻辑取 Quota。
	// UpstreamBaseQuota 渠道每日上限「上游消耗」口径的基础消耗（分组倍率取 1 的额度）。
	// nil 时按结算额与分组倍率推导，见 upstreamBaseQuota。
	UpstreamBaseQuota *int64
}

// EnqueueConsumeLogWithCost is the relay-safe entry point for legacy relay
// paths that already performed billing. It retains only a primitive snapshot
// and schedules cost/commission work after the async log insert.
// upstreamBase 为渠道每日上限的「上游消耗」基数（分组倍率取 1 的额度）；nil 时按
// 「结算额 ÷ 分组倍率」推导，免费分组必须显式传入，否则基数恒为 0、止损失效。
func EnqueueConsumeLogWithCost(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, params model.RecordConsumeLogParams, ledgerQuota int, upstreamBase *int64) {
	if relayInfo == nil {
		return
	}
	markRelaySettlementDone(ctx)
	snapshot := snapshotCostAndCommission(relayInfo, ledgerQuota, upstreamBase)
	if params.ChannelId != 0 {
		snapshot.ChannelID = params.ChannelId
	}
	payload := snapshot.accountingPayload()
	if !model.EnqueueConsumeLog(ctx, relayInfo.UserId, params, &payload) {
		model.DispatchRelayLogAccounting(payload, 0)
	}
}

// FinalizeConsumptionSettlement runs the post-consume side effects shared by
// relay handlers: usage counters, billing settlement, consume log, cost ledger,
// and employee commission ledger.
// The consume log is enqueued, so no log id exists at return time; cost and
// commission are scheduled by the pipeline once the INSERT produced one.
// FinalizeConsumptionSettlement 完成结算、用量统计及日志/成本流水入队；具有 StreamResult 的受管流每个请求仅尝试终止结算一次。
// 参数 ctx：请求日志上下文；relayInfo：本请求渠道、资金来源和流式结果；params：最终额度和日志数据，Other 引用会刷新。
// 无返回值；零收费异常写错误日志，其余走既有异步消费日志管线，入队时尚无日志 ID。
func FinalizeConsumptionSettlement(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, params ConsumptionSettlementParams) {
	if relayInfo == nil {
		logger.LogError(ctx, "consume settlement skipped: relayInfo is nil")
		return
	}
	markRelaySettlementDone(ctx)

	if params.ChannelId == 0 && relayInfo.ChannelMeta != nil {
		params.ChannelId = relayInfo.ChannelMeta.ChannelId
	}
	if params.Group == "" {
		params.Group = relayInfo.UsingGroup
	}
	if params.ModelName == "" {
		params.ModelName = relayInfo.OriginModelName
	}
	if params.TokenId == 0 {
		params.TokenId = relayInfo.TokenId
	}
	// 我方超时结束的请求：平台承担记录（仅管理员可见）写进本请求唯一的结算日志。
	timeoutCost := takeRelayTimeoutCost(ctx)
	if timeoutCost != nil && timeoutCost.absorbed != nil {
		if params.Other == nil {
			params.Other = model.NewLogOther()
		}
		params.Other.SetAdmin(relayTimeoutAbsorbedKey, timeoutCost.absorbed)
	}

	if stream := relayInfo.StreamResult; stream != nil {
		// 先完成唯一资金结算与订阅字段刷新，再构造流式日志摘要，避免记录预扣旧值。
		if !settleStreamQuota(ctx, relayInfo, &params) {
			return
		}
		if params.Other == nil {
			params.Other = model.NewLogOther()
		}
		AppendStreamLogInfo(relayInfo, params.Other)
		failureText := ""
		if stream.Failed && !stream.ClientGone {
			failureText = stream.ErrorMessage
		}
		// 零收费异常保留错误日志和诊断，不再进入消费计数/消费日志分支。
		if params.Quota == 0 && (stream.Failed || stream.SettlementState == "failed" || stream.SettlementState == "partial") {
			// 上游失败：错误日志 content 记录原文（上游的 error.message），供管理员排障；
			// 用户视图读取时按 error_code（流式失败标记 upstream_stream_error）判定为上游
			// 流式错误，并用 admin_info 里记下的终止帧匹配输入改写，与实时终止帧逐项同一
			// 输入（model.maskErrorLogForUser）。
			// 我方超时（relay_timeout）：用户视图按本地错误判定，未命中规则时原样显示
			// content，所以 content 只写我方的超时提示，结束原因的原文（上游帧在截止前
			// 已读到时就是上游的 error.message）只进仅管理员可见的 admin_info.stream_error。
			content := appendStreamFailureContent(ctx, params.Content, failureText)
			if stream.Failed && !stream.ClientGone {
				timedOut := streamFailureTimedOut(ctx)
				if in, ok := streamTerminalInput(ctx); ok {
					params.Other.SetAdmin(operation_setting.RelayStreamMatchCodeKey, in.ErrorCode)
					params.Other.SetAdmin(operation_setting.RelayStreamMatchMessageKey, in.Message)
					if in.Truncated {
						params.Other.SetAdmin(operation_setting.RelayStreamMatchTruncatedKey, true)
					}
				}
				params.Other.SetPublic("error_code", operation_setting.RelayStreamErrorCode)
				if timedOut {
					params.Other.SetPublic("error_code", operation_setting.RelayTimeoutErrorCode)
					content = appendStreamFailureContent(ctx, params.Content, relayTimeoutNote(ctx))
					if failureText != "" {
						params.Other.SetAdmin("stream_error", failureText)
					}
				}
			}
			model.RecordErrorLog(ctx, relayInfo.UserId, params.ChannelId, params.ModelName, params.TokenName, content, params.TokenId, params.UseTimeSeconds, params.IsStream, params.Group, params.Other)
			return
		}
		inputOnly := timeoutCost != nil && timeoutCost.inputOnly
		if failureText != "" || inputOnly {
			// 消费日志 content 是我方文本、用户视图不改写（设计 relay-error-message-masking §4.1），
			// 只写我方的提示（被我方时限切断时写超时提示，只收输入时写明）；上游原文只放进仅管理员可见的 admin_info。
			note := streamFailureConsumeNote(ctx)
			if inputOnly {
				note = relayTimeoutInputOnlyNote(ctx)
			}
			params.Content = appendStreamFailureContent(ctx, params.Content, note)
			if failureText != "" {
				params.Other.SetAdmin("stream_error", failureText)
			}
		}
	}

	// 非流式只收输入的超时结算先于用量统计完成：资金未提交就失败时回退为退款，不计用量、不写消费日志。
	timeoutSettled := false
	if relayInfo.StreamResult == nil {
		var ok bool
		if timeoutSettled, ok = settleRelayTimeoutInputQuota(ctx, relayInfo, timeoutCost, params.Quota); !ok {
			return
		}
	}

	if params.CountUsage {
		model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, params.Quota)
		model.UpdateChannelUsedQuota(params.ChannelId, params.Quota)
	}

	if relayInfo.StreamResult == nil && !timeoutSettled {
		if err := SettleBilling(ctx, relayInfo, params.Quota); err != nil {
			logger.LogError(ctx, "error settling billing: "+err.Error())
		}
	}

	logParams := model.RecordConsumeLogParams{
		ChannelId:        params.ChannelId,
		PromptTokens:     params.PromptTokens,
		CompletionTokens: params.CompletionTokens,
		ModelName:        params.ModelName,
		TokenName:        params.TokenName,
		Quota:            params.Quota,
		Content:          params.Content,
		TokenId:          params.TokenId,
		UseTimeSeconds:   params.UseTimeSeconds,
		IsStream:         params.IsStream,
		Group:            params.Group,
		Other:            params.Other,
		CreatedAt:        params.CreatedAt,
	}

	quotaCopy := params.Quota
	if params.LedgerQuota != 0 {
		quotaCopy = params.LedgerQuota
	}
	snapshot := snapshotCostAndCommission(relayInfo, quotaCopy, params.UpstreamBaseQuota)
	if params.ChannelId != 0 {
		snapshot.ChannelID = params.ChannelId
	}
	payload := snapshot.accountingPayload()
	if model.EnqueueConsumeLog(ctx, relayInfo.UserId, logParams, &payload) {
		return
	}
	// Intake may only be false during shutdown. Never re-enter either database
	// from the relay goroutine; the accounting payload is independently queued.
	model.DispatchRelayLogAccounting(payload, 0)
}

// streamFailureConsumeNote is our own note on a charged stream that failed: the
// relay-timeout text when our time limit cut it (classified as its terminal
// frame was), otherwise the generic upstream stream failure text. English, like
// the rest of the stored content.
func streamFailureConsumeNote(ctx *gin.Context) string {
	if streamFailureTimedOut(ctx) {
		return relayTimeoutNote(ctx)
	}
	return i18n.Translate(i18n.LangEn, i18n.MsgClaudeStreamFailed)
}

// streamFailureTimedOut reports whether our own time limit ended a failed
// stream. The terminal frame's classification wins when one was written, so
// the log and the frame never disagree (the deadline may fire after the frame).
func streamFailureTimedOut(ctx *gin.Context) bool {
	if in, ok := streamTerminalInput(ctx); ok {
		return !in.Upstream
	}
	return IsRelayRequestTimeout(ctx)
}

// relayTimeoutNote is our relay-timeout text, in English like the rest of the
// stored content, with the configured number of seconds.
func relayTimeoutNote(ctx *gin.Context) string {
	return i18n.Translate(i18n.LangEn, i18n.MsgRelayTimeout, map[string]any{"Seconds": RelayRequestTimeoutSeconds(ctx)})
}

// appendStreamFailureContent appends a stream's failure text, with the current
// request id, to a settlement log's content. An empty text leaves it unchanged.
func appendStreamFailureContent(ctx *gin.Context, content, failureText string) string {
	if failureText == "" {
		return content
	}
	if content != "" {
		content += "; "
	}
	return content + MessageWithCurrentRequestId(ctx, failureText)
}
