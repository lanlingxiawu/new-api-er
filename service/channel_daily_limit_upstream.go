package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/shopspring/decimal"
)

// 每日上限「上游消耗」口径的计算与累计入口。
// 设计见 docs/design/channel-limit-upstream-basis-and-timed-recovery.md §4。
//
//	上游消耗 = 基础消耗（按模型价格计算、不乘用户分组倍率） × 渠道成本系数

// upstreamBaseQuota 求本次消费不乘用户分组倍率的「基础消耗」（quota 单位）。在 relay goroutine 上
// 构造记账快照时调用，纯算术。
//
//   - explicit 非 nil：结算路径已按分组倍率 1 精确算过（文本计费，含阶梯与按次），直接采用；
//   - 分组倍率 > 0：各计费公式对分组倍率都是线性的，结算额 ÷ 分组倍率即基础消耗；
//   - 分组倍率 = 0（免费分组）：结算额为 0 无法倒推。按次计费用「模型价格 × 单位额度 × 其它倍率」
//     兜底；按量计费且没有 explicit 的路径（音频 / Wss / 按 token 重算的任务）无法还原，计 0。
//
// 退款等负数额度一律返回 0：累计器只增不减。
func upstreamBaseQuota(priceData *hosttypes.PriceData, quota int, explicit *int64) int64 {
	if explicit != nil {
		if *explicit < 0 {
			return 0
		}
		return *explicit
	}
	if priceData == nil || quota < 0 {
		return 0
	}
	groupRatio := priceData.GroupRatioInfo.GroupRatio
	if groupRatio > 0 {
		if quota == 0 {
			return 0
		}
		return eventCostQuota(decimal.NewFromInt(int64(quota)).Div(decimal.NewFromFloat(groupRatio)))
	}
	if priceData.UsePrice && priceData.ModelPrice > 0 {
		return eventCostQuota(decimal.NewFromFloat(priceData.ModelPrice).
			Mul(decimal.NewFromFloat(common.QuotaPerUnit)).
			Mul(decimal.NewFromFloat(priceData.OtherRatioMultiplier())))
	}
	return 0
}

// maxEventCostQuota 是单笔成本/佣金换算结果的上限（约 1.1e15 quota，远超任何真实单笔消费）。
// 极端倍率（如分组倍率 1e-18）下换算会超出 int64：直接 IntPart 会回绕成负数，被累计器丢弃或记成贷记；
// 饱和到 MaxInt64 又会在下一次 `cost += x` 或数据库 `cost_quota + ?` 时溢出，卡住每日上限刷盘。
// 封顶在这里，累计 8192 笔封顶值才会触及 int64 上界。
const maxEventCostQuota int64 = 1 << 50

func eventCostQuota(d decimal.Decimal) int64 {
	return common.Int64FromDecimal(d, maxEventCostQuota)
}

// upstreamQuota = 基础消耗 × 成本系数，四舍五入到 quota。
func upstreamQuota(baseQuota int64, costRatio float64) int64 {
	if baseQuota <= 0 || costRatio <= 0 {
		return 0
	}
	return eventCostQuota(decimal.NewFromInt(baseQuota).Mul(decimal.NewFromFloat(costRatio)))
}

// recordChannelDailyUpstream 累计一笔上游消耗。
//
// 运行在异步记账管线（或任务结算 goroutine）上，不在 relay goroutine 上，因此可以读取成本系数
// （内存缓存 → Redis → DB）。未配置上限的渠道在读取成本系数之前就返回，绝大多数渠道零开销。
// 未配置成本系数时 GetChannelCostRatio 返回 1.0。
func recordChannelDailyUpstream(channelId int, baseQuota int64) {
	if channelId <= 0 || baseQuota <= 0 {
		return
	}
	setting := operation_setting.GetChannelDailyLimitSnapshot()
	if setting == nil || !setting.Enabled {
		return
	}
	if cfg, ok := lookupLimitConfig(channelId); !ok || cfg.limit <= 0 {
		return
	}
	accumulate(channelId, upstreamQuota(baseQuota, model.GetChannelCostRatio(channelId)))
}
