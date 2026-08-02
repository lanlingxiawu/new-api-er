package service

import (
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

type ConsumptionSettlementParams struct {
	ChannelId        int
	PromptTokens     int
	CompletionTokens int
	ModelName        string
	TokenName        string
	Quota            int
	SurchargeQuota   int64
	Content          string
	TokenId          int
	UseTimeSeconds   int
	IsStream         bool
	Group            string
	Other            map[string]interface{}
	CreatedAt        int64
	CountUsage       bool
	LedgerQuota      int
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

	if params.CountUsage {
		model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, params.Quota)
		model.UpdateChannelUsedQuota(params.ChannelId, params.Quota)
	}

	if err := SettleBilling(ctx, relayInfo, params.Quota); err != nil {
		logger.LogError(ctx, "error settling billing: "+err.Error())
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
