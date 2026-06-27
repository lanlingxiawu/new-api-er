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

func businessStatsCostFallbackPayload(cost *model.ConsumptionCost) map[string]any {
	return map[string]any{
		"cost": cost,
	}
}

func businessStatsCostCommissionFallbackPayload(cost *model.ConsumptionCost, commLog *model.EmployeeCommissionLog, commissionDelta, profitDelta int64) map[string]any {
	payload := map[string]any{
		"cost": cost,
	}
	if commLog != nil {
		payload["commission"] = commLog
		payload["employee_delta"] = map[string]any{
			"user_id":          commLog.EmployeeUserId,
			"commission_delta": commissionDelta,
			"profit_delta":     profitDelta,
		}
	}
	return payload
}

func RecordCostAndSettleEmployeeCommission(relayInfo *relaycommon.RelayInfo, quota int, surchargeQuota int64, logId int) {
	if quota == 0 {
		return
	}
	createdAt := employeeCommissionNow()
	costRec := buildConsumptionCostRecord(relayInfo, quota, surchargeQuota, logId, createdAt)
	guard, ok := model.BeginBusinessStatsSideEffect("business_stats_skipped", businessStatsCostFallbackPayload(costRec))
	if !ok {
		return
	}
	defer guard.Done()
	ctx := guard.Context()

	inviterId, err := model.GetUserInviterIdWithContext(ctx, relayInfo.UserId)
	if err != nil {
		common.SysError(fmt.Sprintf("business_stats_side_effect: settlement_inviter_lookup failed: %s", err.Error()))
		guard.Fail("settlement_inviter_lookup", err, businessStatsCostFallbackPayload(costRec))
		return
	}
	if inviterId == 0 || inviterId == relayInfo.UserId {
		if err := model.CreateConsumptionCost(costRec); err != nil {
			common.SysError("consumption_cost: failed to create record: " + err.Error())
			guard.Fail("consumption_cost_create", err, costRec)
			return
		}
		guard.Success()
		return
	}

	mutual, err := model.IsMutualInvitationWithContext(ctx, relayInfo.UserId, inviterId)
	if err != nil {
		common.SysError(fmt.Sprintf(
			"employee_commission: failed to check mutual invitation user_id=%d inviter_id=%d log_id=%d: %s",
			relayInfo.UserId,
			inviterId,
			logId,
			err.Error(),
		))
		guard.Fail("settlement_mutual_invitation_lookup", err, businessStatsCostFallbackPayload(costRec))
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
			guard.Fail("consumption_cost_create", err, costRec)
			return
		}
		guard.Success()
		return
	}

	emp, err := model.GetEmployeeByUserIdWithContext(ctx, inviterId)
	if err != nil {
		common.SysError(fmt.Sprintf("business_stats_side_effect: settlement_employee_lookup failed: %s", err.Error()))
		guard.Fail("settlement_employee_lookup", err, businessStatsCostFallbackPayload(costRec))
		return
	}
	if emp == nil {
		if err := model.CreateConsumptionCost(costRec); err != nil {
			common.SysError("consumption_cost: failed to create record: " + err.Error())
			guard.Fail("consumption_cost_create", err, costRec)
			return
		}
		guard.Success()
		return
	}

	revenueQuota := int64(quota)
	costQuota := costRec.CostQuota
	profitQuota := revenueQuota - costQuota
	effectiveRate, err := model.GetEffectiveCommissionRateWithContext(ctx, emp.UserId)
	if err != nil {
		common.SysError(fmt.Sprintf("business_stats_side_effect: settlement_effective_commission_rate failed: %s", err.Error()))
		guard.Fail("settlement_effective_commission_rate", err, businessStatsCostFallbackPayload(costRec))
		return
	}
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
	// BufferCommissionAndProfit 不在此处调用：改为在 flushCostAndCommissionLedger 刷盘时
	// 根据 DB RowsAffected 判断是否真正写入，确保多实例场景下不重复累计员工汇总。
	_, err = model.CreateConsumptionCostAndCommissionLog(costRec, commLog)
	if err != nil {
		common.SysError("employee_commission: failed to create cost and commission logs: " + err.Error())
		guard.Fail("cost_commission_create", err, businessStatsCostCommissionFallbackPayload(costRec, commLog, commissionQuota, profitQuota))
		return
	}
	guard.Success()
}

// RecordTransactionCost 在每笔消费结算后异步调用，记录逐笔精确成本到 consumption_costs。
// 覆盖全平台所有消费（不仅员工归属流量），用于平台级成本/利润精确统计。
// 成本算法与提成一致：全部收入统一套用渠道成本系数。
func RecordTransactionCost(relayInfo *relaycommon.RelayInfo, quota int, surchargeQuota int64, logId int) {
	if quota == 0 {
		return
	}
	rec := buildConsumptionCostRecord(relayInfo, quota, surchargeQuota, logId, employeeCommissionNow())
	guard, ok := model.BeginBusinessStatsSideEffect("business_stats_skipped", businessStatsCostFallbackPayload(rec))
	if !ok {
		return
	}
	defer guard.Done()
	if err := model.CreateConsumptionCost(rec); err != nil {
		common.SysError("consumption_cost: failed to create record: " + err.Error())
		guard.Fail("consumption_cost_create", err, rec)
		return
	}
	guard.Success()
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
// 利润为正时计提正佣金；利润为负时产生负佣金（含正向亏损与退款冲销两种场景）；利润为零时不计提。
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
