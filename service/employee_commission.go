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
//   relayInfo  — 当次请求的 relay 信息（含 PriceData、ChannelId 等）
//   quota      — 用户实际消耗的 quota（可为负，退款冲销）
//   logId      — 对应的 logs.id（用于关联溯源）
func TrySettleEmployeeCommission(relayInfo *relaycommon.RelayInfo, quota int, logId int) {
	if quota == 0 {
		return
	}

	// 1. 查询消费用户的邀请人
	inviterId := model.GetUserInviterId(relayInfo.UserId)
	if inviterId == 0 {
		return
	}

	// 2. 邀请人必须是启用状态的员工
	emp := model.GetEmployeeByUserId(inviterId)
	if emp == nil {
		return
	}

	// 3. 员工不对自身消费计提成
	if inviterId == relayInfo.UserId {
		return
	}

	// 4. 精度计算
	revenueQuota := int64(quota)
	costRatio := model.GetChannelCostRatio(relayInfo.ChannelId)
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio

	costQuota := calcCostQuota(revenueQuota, groupRatio, costRatio)
	profitQuota := revenueQuota - costQuota
	commissionQuota := calcCommissionQuota(profitQuota, emp.CommissionRate)

	// 5. 利润为零（或负）且不是退款冲销，直接跳过
	//    退款（quota<0）时 commissionQuota 为负，仍需写入以冲销之前的提成
	if commissionQuota == 0 {
		return
	}

	// 6. 写入提成日志
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
		CommissionRate:  emp.CommissionRate,
		CostRatio:       costRatio,
		GroupRatio:      groupRatio,
	}
	if err := model.CreateCommissionLog(commLog); err != nil {
		common.SysError("employee_commission: failed to create commission log: " + err.Error())
		return
	}

	// 7. 原子更新 user_extensions 汇总
	if err := model.AddCommissionQuota(emp.UserId, commissionQuota); err != nil {
		common.SysError("employee_commission: failed to update user_extensions: " + err.Error())
	}
	if err := model.AddRevenueStats(emp.UserId, revenueQuota, false); err != nil {
		common.SysError("employee_commission: failed to update revenue stats: " + err.Error())
	}
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
// profit 可为负（退款冲销），此时返回负值提成。
func calcCommissionQuota(profitQuota int64, commissionRate float64) int64 {
	if commissionRate <= 0 {
		return 0
	}
	dProfit := decimal.NewFromInt(profitQuota)
	dRate := decimal.NewFromFloat(commissionRate)
	result := dProfit.Mul(dRate).Round(0)

	// 正利润时保底 1 quota，避免因精度截断丢失极小提成
	if profitQuota > 0 && result.IsZero() {
		return 1
	}
	// 负利润（退款）时保上限 -1，同理
	if profitQuota < 0 && result.IsZero() {
		return -1
	}
	return result.IntPart()
}

