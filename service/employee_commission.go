package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/shopspring/decimal"
)

// TrySettleEmployeeCommission 在 API 消费结算完成后被异步调用。
// 检查消费用户的邀请人是否是员工，若是则计算并写入提成日志。
//
// 参数：
//   relayInfo      — 当次请求的 relay 信息（含 PriceData、ChannelId 等）
//   quota          — 用户实际消耗的 quota（即收入，可为负，退款冲销）
//   surchargeQuota — quota 中属于「固定价加付项」的部分（如 web/file search、
//                    图像生成调用），已包含组倍率。这部分上游为固定单价，不随
//                    渠道 token 折扣变化，故不套用 cost_ratio。无加付项传 0。
//   logId          — 对应的 logs.id（用于关联溯源、幂等去重）
func TrySettleEmployeeCommission(relayInfo *relaycommon.RelayInfo, quota int, surchargeQuota int64, logId int) {
	if quota == 0 {
		return
	}

	// 1. 查询消费用户的邀请人
	inviterId := model.GetUserInviterId(relayInfo.UserId)
	if inviterId == 0 {
		return
	}

	// 2. 员工不对自身消费计提成（前置，省去无谓的员工档案查询）
	if inviterId == relayInfo.UserId {
		return
	}

	// 3. 邀请人必须是启用状态的员工
	emp := model.GetEmployeeByUserId(inviterId)
	if emp == nil {
		return
	}

	// 4. 精度计算
	revenueQuota := int64(quota)
	costRatio := model.GetChannelCostRatio(relayInfo.ChannelId)
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio

	// 拆分收入：固定价加付项按 cost_ratio=1 计成本（仅组倍率部分计入毛利），
	// 其余 token 收入套用渠道成本系数。
	surcharge := clampSurcharge(surchargeQuota, revenueQuota)
	tokenRevenue := revenueQuota - surcharge

	tokenCost := calcCostQuota(tokenRevenue, groupRatio, costRatio)
	surchargeCost := calcCostQuota(surcharge, groupRatio, 1.0)
	costQuota := tokenCost + surchargeCost
	profitQuota := revenueQuota - costQuota

	// 5. 利润 <= 0 不计提成（含退款、成本高于售价等场景），
	//    但仍写入日志用于审计与对账。
	//    提成率优先使用员工当前等级的 rate；无等级时回退到 emp.CommissionRate。
	effectiveRate := model.GetEffectiveCommissionRate(emp.UserId, emp.CommissionRate)
	var commissionQuota int64
	if profitQuota > 0 {
		commissionQuota = calcCommissionQuota(profitQuota, effectiveRate)
	}

	// 6. 写入提成日志（无论是否产生提成都记录）
	commLog := &model.EmployeeCommissionLog{
		EmployeeId:      emp.Id,
		EmployeeUserId:  emp.UserId,
		CustomerUserId:  relayInfo.UserId,
		LogId:           logId,
		ModelName:       relayInfo.OriginModelName,
		ChannelId:       relayInfo.ChannelId,
		RevenueQuota:    revenueQuota,
		CostQuota:       costQuota,
		ProfitQuota:     profitQuota,
		CommissionQuota: commissionQuota,
		CommissionRate:  effectiveRate,
		CostRatio:       costRatio,
		GroupRatio:      groupRatio,
	}
	inserted, err := model.CreateCommissionLog(commLog)
	if err != nil {
		common.SysError("employee_commission: failed to create commission log: " + err.Error())
		return
	}
	// 幂等：该 log_id 已结算过（重试/并发），跳过汇总累加，避免重复计提
	if !inserted {
		return
	}

	// 7. 原子更新 user_extensions 汇总（无提成则不累加提成额度）
	if commissionQuota != 0 {
		if err := model.AddCommissionQuota(emp.UserId, commissionQuota); err != nil {
			common.SysError("employee_commission: failed to update user_extensions: " + err.Error())
		}
	}
	newProfitTotal, err := model.AddProfitStats(emp.UserId, profitQuota, false)
	if err != nil {
		common.SysError("employee_commission: failed to update profit stats: " + err.Error())
		return
	}

	// 8. 检查是否触发等级升级（仅利润为正时才可能升级）
	if profitQuota > 0 {
		model.TryAutoUpgradeTier(emp.UserId, newProfitTotal)
	}
}

// RecordTransactionCost 在每笔消费结算后异步调用，记录逐笔精确成本到 consumption_costs。
// 覆盖全平台所有消费（不仅员工归属流量），用于平台级成本/利润精确统计。
// 成本算法与提成一致：token 部分套用渠道成本系数，固定价加付项按 cost_ratio=1。
func RecordTransactionCost(relayInfo *relaycommon.RelayInfo, quota int, surchargeQuota int64, logId int) {
	if quota == 0 {
		return
	}
	revenueQuota := int64(quota)
	costRatio := model.GetChannelCostRatio(relayInfo.ChannelId)
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio

	surcharge := clampSurcharge(surchargeQuota, revenueQuota)
	tokenRevenue := revenueQuota - surcharge
	costQuota := calcCostQuota(tokenRevenue, groupRatio, costRatio) +
		calcCostQuota(surcharge, groupRatio, 1.0)

	rec := &model.ConsumptionCost{
		LogId:        logId,
		UserId:       relayInfo.UserId,
		ChannelId:    relayInfo.ChannelId,
		GroupName:    relayInfo.UsingGroup,
		ModelName:    relayInfo.OriginModelName,
		RevenueQuota: revenueQuota,
		CostQuota:    costQuota,
		GroupRatio:   groupRatio,
		CostRatio:    costRatio,
	}
	if err := model.CreateConsumptionCost(rec); err != nil {
		common.SysError("consumption_cost: failed to create record: " + err.Error())
	}
}

// clampSurcharge 约束加付项额度，保证 0 <= surcharge <= revenue（revenue>0 时）。
// revenue<=0（退款等）时返回 0，因为此时利润必 <=0、不影响提成结果，
// 同时避免 tokenRevenue 出现非预期负值。
func clampSurcharge(surcharge, revenue int64) int64 {
	if surcharge <= 0 || revenue <= 0 {
		return 0
	}
	if surcharge > revenue {
		return revenue
	}
	return surcharge
}

// calcCostQuota 用 decimal 精度计算成本额度。
//
// 成本逻辑：用户实际支付了 revenue，其中包含了 group_ratio 加价。
// 剔除加价后得到原始成本基准，再乘以渠道折扣系数得到实际成本。
//
//   cost = revenue / group_ratio * cost_ratio
//
// tiered_expr 场景 group_ratio 同样适用，因为 PriceData.GroupRatioInfo.GroupRatio
// 在 tiered 模式下同样被应用（见 modelPriceHelperTiered）。
func calcCostQuota(revenueQuota int64, groupRatio, costRatio float64) int64 {
	dRevenue := decimal.NewFromInt(revenueQuota)

	var dBaseCost decimal.Decimal
	if groupRatio != 0 {
		dBaseCost = dRevenue.Div(decimal.NewFromFloat(groupRatio))
	} else {
		// group_ratio=0 表示免费模型，成本也视为 0
		dBaseCost = decimal.Zero
	}

	dCost := dBaseCost.Mul(decimal.NewFromFloat(costRatio)).Round(0)
	return dCost.IntPart()
}

// calcCommissionQuota 用 decimal 精度计算提成额度。
// 仅在利润为正时计提；利润 <=0 由调用方拦截，这里再做一次防御。
func calcCommissionQuota(profitQuota int64, commissionRate float64) int64 {
	if profitQuota <= 0 || commissionRate <= 0 {
		return 0
	}
	result := decimal.NewFromInt(profitQuota).Mul(decimal.NewFromFloat(commissionRate)).Round(0)
	// 保底 1 quota，避免因精度截断丢失极小提成
	if result.IsZero() {
		return 1
	}
	return result.IntPart()
}

