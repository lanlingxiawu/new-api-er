package service

import (
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// settleClaudeStreamQuota owns the one terminal balance adjustment. On uncertain
// failures it records the intended charge without retrying arithmetic or refund.
// settleClaudeStreamQuota 执行严格 Claude 的唯一终止结算，并把结算后的订阅金额刷新到待保存日志。
// 参数 ctx：请求日志上下文；relayInfo：带非 nil ClaudeStream 的中转信息；params：最终结算参数指针，资金失败时会清零费用与统计项。
// 返回 true 表示本次已尝试结算；false 表示此前已尝试，应跳过后续日志和资金操作。
// 仅采用已记录的资金提交差额；全额退款显式覆盖最终消耗为 0，失败不自动重试非幂等资金运算。
func settleClaudeStreamQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, params *ConsumptionSettlementParams) bool {
	stream := relayInfo.ClaudeStream
	if stream.SettlementAttempted {
		return false
	}
	stream.SettlementAttempted = true // 先置位再调用资金接口，失败也不自动重做非幂等扣款/退款。
	stream.IntendedQuota = params.Quota
	if relayInfo.Billing != nil {
		stream.ReservedQuota = relayInfo.Billing.GetPreConsumedQuota()
	}
	err := SettleBilling(ctx, relayInfo, params.Quota)
	stream.SettlementState = "settled"
	if params.Quota == 0 {
		stream.SettlementState = "released"
	}
	if err != nil {
		stream.SettlementState = "failed"
		stream.Diagnostic.SettlementError = err.Error()
		fundingCommitted := false // 资金与令牌分步提交；只有资金已提交才保留实际收费与 partial 状态。
		if session, ok := relayInfo.Billing.(interface{ FundingCommitted() bool }); ok {
			fundingCommitted = session.FundingCommitted()
		}
		if fundingCommitted {
			stream.SettlementState = "partial"
		} else {
			params.Quota = 0
			params.CountUsage = false
			params.LedgerQuota = 0
			params.SurchargeQuota = 0
		}
		logger.LogError(ctx, "Claude stream settlement needs review")
	}

	if params.Other != nil && relayInfo.BillingSource == BillingSourceSubscription {
		// Other 生成于结算前；先重置序列化器会省略的零值字段，使全额退款覆盖预扣快照而不是留下旧消耗。
		params.Other["subscription_consumed"] = int64(0)
		params.Other["subscription_post_delta"] = int64(0)
		appendBillingInfo(relayInfo, params.Other)
	}

	return true
}
