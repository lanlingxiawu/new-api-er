package service

import (
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// ConsumptionSettlementParams 汇总终止结算及日志入队所需数据，额度单位为系统内部 quota，不是货币元。
type ConsumptionSettlementParams struct {
	ChannelId        int                    // 日志所属渠道；0 时从 relayInfo 补入。
	PromptTokens     int                    // 最终计费输入 token 数。
	CompletionTokens int                    // 最终计费输出 token 数。
	ModelName        string                 // 日志模型名；空时使用请求原模型名。
	TokenName        string                 // 调用令牌的展示名称，不含令牌密钥。
	Quota            int                    // 最终用户收费额度；严格流式为 0 时释放预扣。
	SurchargeQuota   int64                  // 工具等附加费的内部额度，供成本/分账快照使用。
	Content          string                 // 日志公开说明，不放原始响应或底层私有错误。
	TokenId          int                    // 调用令牌 ID；0 时从请求信息补入。
	UseTimeSeconds   int                    // 请求耗时，单位秒。
	IsStream         bool                   // 本次是否为流式请求。
	Group            string                 // 实际计费分组；空时使用请求当前分组。
	Other            map[string]interface{} // 待序列化日志扩展字段，结算会刷新其中的订阅和流式信息。
	CreatedAt        int64                  // 日志创建时间，Unix 秒，传递给既有日志管线。
	CountUsage       bool                   // 是否更新用户/渠道消费统计；严格免收费或资金失败时关闭。
	LedgerQuota      int                    // 成本/佣金使用的额度口径；0 时日志快照逻辑取 Quota。
}

// EnqueueConsumeLogWithCost is the relay-safe entry point for legacy relay
// paths that already performed billing. It retains only a primitive snapshot
// and schedules cost/commission work after the async log insert.
func EnqueueConsumeLogWithCost(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, params model.RecordConsumeLogParams, ledgerQuota int, surchargeQuota int64) {
	if relayInfo == nil {
		return
	}
	snapshot := snapshotCostAndCommission(relayInfo, ledgerQuota, surchargeQuota)
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
// FinalizeConsumptionSettlement 完成结算、用量统计及日志/成本流水入队；严格 Claude 流每个请求仅尝试结算一次。
// 参数 ctx：请求日志上下文；relayInfo：本请求渠道、资金来源和流式结果；params：最终额度和日志数据，Other 引用会刷新。
// 无返回值；零收费异常写错误日志，其余走既有异步消费日志管线，入队时尚无日志 ID。
func FinalizeConsumptionSettlement(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, params ConsumptionSettlementParams) {
	if relayInfo == nil {
		logger.LogError(ctx, "consume settlement skipped: relayInfo is nil")
		return
	}

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

	if stream := relayInfo.ClaudeStream; stream != nil {
		// 先完成唯一资金结算与订阅字段刷新，再构造流式日志摘要，避免记录预扣旧值。
		if !settleClaudeStreamQuota(ctx, relayInfo, &params) {
			return
		}
		AppendClaudeStreamLogInfo(relayInfo, params.Other)
		// 零收费异常保留错误日志和诊断，不再进入消费计数/消费日志分支。
		if params.Quota == 0 && (stream.Failed || stream.SettlementState == "failed" || stream.SettlementState == "partial") {
			model.RecordErrorLog(ctx, relayInfo.UserId, params.ChannelId, params.ModelName, params.TokenName, params.Content, params.TokenId, params.UseTimeSeconds, params.IsStream, params.Group, params.Other)
			return
		}
	}

	if params.CountUsage {
		model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, params.Quota)
		model.UpdateChannelUsedQuota(params.ChannelId, params.Quota)
	}

	if relayInfo.ClaudeStream == nil {
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
	snapshot := snapshotCostAndCommission(relayInfo, quotaCopy, params.SurchargeQuota)
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
