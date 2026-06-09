package service

import (
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/shopspring/decimal"
)

var employeeCommissionNow = func() int64 {
	return time.Now().Unix()
}

func optionalLogId(logId int) *int {
	if logId <= 0 {
		return nil
	}
	return common.GetPointer(logId)
}

func relayChannelName(relayInfo *relaycommon.RelayInfo) string {
	if relayInfo == nil || relayInfo.ChannelMeta == nil {
		return ""
	}
	if name := relayInfo.ChannelName; name != "" {
		return name
	}
	if relayInfo.ChannelId == 0 {
		return ""
	}
	if channel, err := model.CacheGetChannel(relayInfo.ChannelId); err == nil && channel != nil {
		return channel.Name
	}
	return ""
}

func buildConsumptionCostRecord(relayInfo *relaycommon.RelayInfo, quota int, surchargeQuota int64, logId int, createdAt int64) *model.ConsumptionCost {
	revenueQuota := int64(quota)
	costRatio := model.GetChannelCostRatio(relayInfo.ChannelId)
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio

	costQuota := calcCostQuota(revenueQuota, groupRatio, costRatio)

	return &model.ConsumptionCost{
		LogId:        optionalLogId(logId),
		UserId:       relayInfo.UserId,
		ChannelId:    relayInfo.ChannelId,
		ChannelName:  relayChannelName(relayInfo),
		GroupName:    relayInfo.UsingGroup,
		ModelName:    relayInfo.OriginModelName,
		RevenueQuota: revenueQuota,
		CostQuota:    costQuota,
		GroupRatio:   groupRatio,
		CostRatio:    costRatio,
		CreatedAt:    createdAt,
	}
}

func RecordCostAndSettleEmployeeCommission(relayInfo *relaycommon.RelayInfo, quota int, surchargeQuota int64, logId int) {
	if quota == 0 {
		return
	}

	createdAt := employeeCommissionNow()
	costRec := buildConsumptionCostRecord(relayInfo, quota, surchargeQuota, logId, createdAt)

	inviterId := model.GetUserInviterId(relayInfo.UserId)
	if inviterId == 0 || inviterId == relayInfo.UserId {
		if err := model.CreateConsumptionCost(costRec); err != nil {
			common.SysError("consumption_cost: failed to create record: " + err.Error())
		}
		return
	}

	mutual, err := model.IsMutualInvitation(relayInfo.UserId, inviterId)
	if err != nil {
		common.SysError(fmt.Sprintf(
			"employee_commission: failed to check mutual invitation user_id=%d inviter_id=%d log_id=%d: %s",
			relayInfo.UserId,
			inviterId,
			logId,
			err.Error(),
		))
		if err := model.CreateConsumptionCost(costRec); err != nil {
			common.SysError("consumption_cost: failed to create record: " + err.Error())
		}
		return
	}
	if mutual {
		adminInfo := map[string]interface{}{
			"reason":           "mutual_invitation",
			"employee_user_id": inviterId,
			"customer_user_id": relayInfo.UserId,
			"log_id":           logId,
			"model_name":       relayInfo.OriginModelName,
			"channel_id":       relayInfo.ChannelId,
		}
		model.RecordLogWithAdminInfo(
			inviterId,
			model.LogTypeSystem,
			"skipped employee commission because employee and customer are mutual inviters",
			adminInfo,
		)
		common.SysLog(fmt.Sprintf(
			"employee_commission: skipped mutual invitation commission employee_user_id=%d customer_user_id=%d log_id=%d model=%s channel_id=%d",
			inviterId,
			relayInfo.UserId,
			logId,
			relayInfo.OriginModelName,
			relayInfo.ChannelId,
		))
		if err := model.CreateConsumptionCost(costRec); err != nil {
			common.SysError("consumption_cost: failed to create record: " + err.Error())
		}
		return
	}

	emp := model.GetEmployeeByUserId(inviterId)
	if emp == nil {
		if err := model.CreateConsumptionCost(costRec); err != nil {
			common.SysError("consumption_cost: failed to create record: " + err.Error())
		}
		return
	}

	revenueQuota := int64(quota)
	costQuota := costRec.CostQuota
	profitQuota := revenueQuota - costQuota
	effectiveRate := model.GetEffectiveCommissionRate(emp.UserId)
	commissionQuota := calcSettlementCommissionQuota(revenueQuota, profitQuota, effectiveRate)

	commLog := &model.EmployeeCommissionLog{
		EmployeeId:      emp.Id,
		EmployeeUserId:  emp.UserId,
		CustomerUserId:  relayInfo.UserId,
		LogId:           optionalLogId(logId),
		ModelName:       relayInfo.OriginModelName,
		ChannelId:       relayInfo.ChannelId,
		RevenueQuota:    revenueQuota,
		CostQuota:       costQuota,
		ProfitQuota:     profitQuota,
		CommissionQuota: commissionQuota,
		CommissionRate:  effectiveRate,
		CostRatio:       costRec.CostRatio,
		GroupRatio:      costRec.GroupRatio,
		CreatedAt:       createdAt,
	}
	inserted, err := model.CreateConsumptionCostAndCommissionLog(costRec, commLog)
	if err != nil {
		common.SysError("employee_commission: failed to create cost and commission logs: " + err.Error())
		return
	}
	if !inserted {
		return
	}
	model.BufferCommissionAndProfit(emp.UserId, commissionQuota, profitQuota)
}

// TrySettleEmployeeCommission 在 API 消费结算完成后被异步调用。
// 检查消费用户的邀请人是否是员工，若是则计算并写入提成日志。
//
// 参数：
//
//	relayInfo      — 当次请求的 relay 信息（含 PriceData、ChannelId 等）
//	quota          — 用户实际消耗的 quota（即收入，可为负，退款冲销）
//	surchargeQuota — 保留参数，暂未使用
//	logId          — 对应的 logs.id（用于关联溯源、幂等去重）
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

	mutual, err := model.IsMutualInvitation(relayInfo.UserId, inviterId)
	if err != nil {
		common.SysError(fmt.Sprintf(
			"employee_commission: failed to check mutual invitation user_id=%d inviter_id=%d log_id=%d: %s",
			relayInfo.UserId,
			inviterId,
			logId,
			err.Error(),
		))
		return
	}
	if mutual {
		adminInfo := map[string]interface{}{
			"reason":           "mutual_invitation",
			"employee_user_id": inviterId,
			"customer_user_id": relayInfo.UserId,
			"log_id":           logId,
			"model_name":       relayInfo.OriginModelName,
			"channel_id":       relayInfo.ChannelId,
		}
		model.RecordLogWithAdminInfo(
			inviterId,
			model.LogTypeSystem,
			"skipped employee commission because employee and customer are mutual inviters",
			adminInfo,
		)
		common.SysLog(fmt.Sprintf(
			"employee_commission: skipped mutual invitation commission employee_user_id=%d customer_user_id=%d log_id=%d model=%s channel_id=%d",
			inviterId,
			relayInfo.UserId,
			logId,
			relayInfo.OriginModelName,
			relayInfo.ChannelId,
		))
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

	costQuota := calcCostQuota(revenueQuota, groupRatio, costRatio)
	profitQuota := revenueQuota - costQuota

	// 5. 正向消费只在利润为正时计提；退款/负消费按同一成本公式冲销佣金。
	//    正向亏损不产生负佣金，避免将渠道亏损转嫁到员工。
	//    提成率统一由员工当前等级决定。
	effectiveRate := model.GetEffectiveCommissionRate(emp.UserId)
	commissionQuota := calcSettlementCommissionQuota(revenueQuota, profitQuota, effectiveRate)

	// 6. 写入提成日志（无论是否产生提成都记录）
	commLog := &model.EmployeeCommissionLog{
		EmployeeId:      emp.Id,
		EmployeeUserId:  emp.UserId,
		CustomerUserId:  relayInfo.UserId,
		LogId:           optionalLogId(logId),
		ModelName:       relayInfo.OriginModelName,
		ChannelId:       relayInfo.ChannelId,
		RevenueQuota:    revenueQuota,
		CostQuota:       costQuota,
		ProfitQuota:     profitQuota,
		CommissionQuota: commissionQuota,
		CommissionRate:  effectiveRate,
		CostRatio:       costRatio,
		GroupRatio:      groupRatio,
		CreatedAt:       employeeCommissionNow(),
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

	// 7. 提成和利润增量写入缓冲区，由后台定时批量刷入 user_extensions 并检查等级升级
	model.BufferCommissionAndProfit(emp.UserId, commissionQuota, profitQuota)
}

// RecordTransactionCost 在每笔消费结算后异步调用，记录逐笔精确成本到 consumption_costs。
// 覆盖全平台所有消费（不仅员工归属流量），用于平台级成本/利润精确统计。
// 成本算法与提成一致：全部收入统一套用渠道成本系数。
func RecordTransactionCost(relayInfo *relaycommon.RelayInfo, quota int, surchargeQuota int64, logId int) {
	if quota == 0 {
		return
	}
	rec := buildConsumptionCostRecord(relayInfo, quota, surchargeQuota, logId, employeeCommissionNow())
	if err := model.CreateConsumptionCost(rec); err != nil {
		common.SysError("consumption_cost: failed to create record: " + err.Error())
	}
}

// calcCostQuota 用 decimal 精度计算成本额度。
//
// 成本逻辑：用户实际支付了 revenue，其中包含了 group_ratio 加价。
// 剔除加价后得到原始成本基准，再乘以渠道折扣系数得到实际成本。
//
//	cost = revenue / group_ratio * cost_ratio
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

// calcSettlementCommissionQuota 计算单笔结算应产生的佣金增量。
// 正向消费：只有正利润计提。
// 退款冲销：只有负利润产生负佣金，和原正向计提保持符号对称。
func calcSettlementCommissionQuota(revenueQuota, profitQuota int64, commissionRate float64) int64 {
	if profitQuota == 0 {
		return 0
	}
	return calcCommissionQuota(profitQuota, commissionRate)
}

// calcCommissionQuota 用 decimal 精度计算提成额度。
// 正利润有 1 quota 保底；负利润用于退款冲销，极小负值同样至少冲销 1 quota。
func calcCommissionQuota(profitQuota int64, commissionRate float64) int64 {
	if profitQuota == 0 || commissionRate <= 0 {
		return 0
	}
	result := decimal.NewFromInt(profitQuota).Mul(decimal.NewFromFloat(commissionRate)).Round(0)
	// 保底 1 quota，避免因精度截断丢失极小提成/冲销
	if result.IsZero() {
		if profitQuota > 0 {
			return 1
		}
		return -1
	}
	return result.IntPart()
}
