package service

import (
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// ReserveRealtimeStreamUsage 以累计费用补充同一资金会话预留，替代旧逐轮实际扣款，防止最终重复收费。
// 参数 info 提供同一资金会话及价格，usage 为各轮选定的累计用量；不支持 Reserve 时返回 nil，否则返回追加预留结果。
func ReserveRealtimeStreamUsage(info *relaycommon.RelayInfo, usage *dto.RealtimeUsage) error {
	reserver, ok := info.Billing.(interface{ Reserve(int) error })
	if !ok {
		return nil
	}
	quota, clamp := calculateAudioQuota(QuotaInfo{InputDetails: TokenDetails{TextTokens: usage.InputTokenDetails.TextTokens, AudioTokens: usage.InputTokenDetails.AudioTokens}, OutputDetails: TokenDetails{TextTokens: usage.OutputTokenDetails.TextTokens, AudioTokens: usage.OutputTokenDetails.AudioTokens}, ModelName: info.UpstreamModelName, UsePrice: info.PriceData.UsePrice, ModelPrice: info.PriceData.ModelPrice, ModelRatio: info.PriceData.ModelRatio, GroupRatio: info.PriceData.GroupRatioInfo.GroupRatio})
	noteQuotaClamp(info, clamp)
	if ok, q, _ := TryTieredSettle(info, billingexpr.TokenParams{P: float64(usage.InputTokens), C: float64(usage.OutputTokens), Len: float64(usage.InputTokens)}); ok {
		quota = q
	}
	if usage.TotalTokens == 0 {
		quota = 0
	}
	return reserver.Reserve(quota)
}
