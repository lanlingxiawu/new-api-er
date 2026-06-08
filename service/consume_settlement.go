package service

import (
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

type ConsumptionSettlementParams struct {
	ChannelId              int
	PromptTokens           int
	CompletionTokens       int
	ModelName              string
	TokenName              string
	Quota                  int
	SurchargeQuota         int64
	Content                string
	TokenId                int
	UseTimeSeconds         int
	IsStream               bool
	Group                  string
	Other                  map[string]interface{}
	CreatedAt              int64
	CountUsage             bool
	AsyncCostAndCommission bool
}

// FinalizeConsumptionSettlement runs the post-consume side effects shared by
// relay handlers: usage counters, billing settlement, consume log, cost ledger,
// and employee commission ledger.
func FinalizeConsumptionSettlement(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, params ConsumptionSettlementParams) int {
	if relayInfo == nil {
		logger.LogError(ctx, "consume settlement skipped: relayInfo is nil")
		return 0
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

	logId := model.RecordConsumeLog(ctx, relayInfo.UserId, model.RecordConsumeLogParams{
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
	})

	relayInfoCopy := *relayInfo
	if relayInfoCopy.ChannelMeta != nil {
		channelMetaCopy := *relayInfoCopy.ChannelMeta
		relayInfoCopy.ChannelMeta = &channelMetaCopy
	} else {
		relayInfoCopy.ChannelMeta = &relaycommon.ChannelMeta{}
	}
	if params.ChannelId != 0 {
		relayInfoCopy.ChannelMeta.ChannelId = params.ChannelId
	}
	quotaCopy := params.Quota
	surchargeCopy := params.SurchargeQuota
	recordCostAndCommission := func() {
		RecordCostAndSettleEmployeeCommission(&relayInfoCopy, quotaCopy, surchargeCopy, logId)
	}

	if params.AsyncCostAndCommission {
		gopool.Go(recordCostAndCommission)
	} else {
		recordCostAndCommission()
	}

	return logId
}
