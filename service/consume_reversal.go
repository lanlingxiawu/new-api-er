package service

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// ReversalResult 冲销结果，返回给管理端。
type ReversalResult struct {
	LedgerId      int `json:"ledger_id"`
	OriginalLogId int `json:"original_log_id"`
	ReversalLogId int `json:"reversal_log_id"`
	UserId        int `json:"user_id"`
	ReversedQuota int `json:"reversed_quota"` // 被冲销的原始额度（带符号）
}

// ReverseConsumptionCostLedger 对某条台账记录（对应一条消费日志）执行定向冲销：
// 插入一条 Quota 取负的镜像消费记录，重新走结算侧效应（余额 + 台账 + 提成），
// 使该笔消费在余额、台账明细、业务概览、提成统计上的净影响归零。
//
// 语义与 RefundTaskQuota 一致，但作用于历史消费日志而非任务：
//  1. 校验：台账记录 -> 原始日志存在、类型为消费、未被冲销过（幂等）；
//  2. 余额修正：DeltaUpdateUserQuota(userId, +originalQuota) 反做原扣费；
//  3. 插入镜像消费日志（Quota = -originalQuota，标记 reversal，引用原始 log_id）；
//  4. 反向记账：RecordCostAndSettleEmployeeCommission(info, -originalQuota, 0, reversalLogId)
//     -> RevenueQuota < 0 自动打 reversal 台账标签并冲减员工提成；
//  5. 原始日志打 reversed 标记，防止重复冲销。
//
// 注意：v1 仅修正钱包余额（user.Quota，也是漏洞刷取所在）；订阅计费来源的冲销暂不处理。
func ReverseConsumptionCostLedger(c *gin.Context, ledgerId int, adminId int, adminName string) (*ReversalResult, error) {
	ledger, err := model.GetConsumptionCostById(ledgerId)
	if err != nil {
		return nil, fmt.Errorf("台账记录不存在: %w", err)
	}
	if ledger.LogId == nil || *ledger.LogId <= 0 {
		return nil, fmt.Errorf("该台账记录没有关联的消费日志，无法冲销")
	}
	logId := *ledger.LogId

	origLog, err := model.GetLogById(logId)
	if err != nil {
		return nil, fmt.Errorf("原始消费日志不存在: %w", err)
	}
	if origLog.Type != model.LogTypeConsume {
		return nil, fmt.Errorf("仅支持冲销消费类型的日志")
	}
	if origLog.Quota == 0 {
		return nil, fmt.Errorf("该消费日志额度为 0，无需冲销")
	}
	if model.IsConsumeLogReversed(origLog) {
		return nil, fmt.Errorf("该消费日志已被冲销，请勿重复操作")
	}

	origQuota := origLog.Quota

	// 1. 余额修正：反做原始扣费（原扣费 balance -= quota，冲销 balance += quota）。
	//    负扣费（bug 凭空增长）场景下 quota < 0，此处会把非法增长扣回。
	if err := model.DeltaUpdateUserQuota(origLog.UserId, origQuota); err != nil {
		return nil, fmt.Errorf("修正用户余额失败: %w", err)
	}

	// 2. 插入镜像消费日志（Quota 取负），保证消费统计净额归零。
	reversalOther := map[string]interface{}{
		"reversal": true,
		"admin_info": map[string]interface{}{
			"reversal":        true,
			"reversed_log_id": logId,
			"admin_id":        adminId,
			"admin_username":  adminName,
		},
	}
	reversalLogId := model.RecordReversalConsumeLog(origLog.UserId, origLog.Username, model.RecordConsumeLogParams{
		ChannelId: origLog.ChannelId,
		ModelName: origLog.ModelName,
		TokenName: origLog.TokenName,
		Quota:     -origQuota,
		Content:   fmt.Sprintf("冲销消费日志 #%d（原额度 %s）", logId, logger.LogQuota(origQuota)),
		TokenId:   origLog.TokenId,
		Group:     origLog.Group,
		Other:     reversalOther,
	})

	// 3. 反向记账（台账 + 提成）：RevenueQuota < 0 自动打 reversal 标签并冲减提成。
	//    与 RefundTaskQuota -> recordTaskCostAndCommission(task, -quota, logId) 同构。
	info := reversalRelayInfoFromLog(origLog)
	RecordCostAndSettleEmployeeCommission(info, -origQuota, 0, reversalLogId)

	// 4. 原始日志打冲销标记，防止重复冲销。
	if err := model.MarkConsumeLogReversed(logId, reversalLogId, adminId, adminName); err != nil {
		common.SysError(fmt.Sprintf("冲销标记写入失败 log_id=%d: %s", logId, err.Error()))
	}

	logger.LogInfo(c, fmt.Sprintf(
		"管理员冲销消费日志 log_id=%d reversal_log_id=%d user_id=%d quota=%s admin=%s",
		logId, reversalLogId, origLog.UserId, logger.LogQuota(origQuota), adminName))

	return &ReversalResult{
		LedgerId:      ledgerId,
		OriginalLogId: logId,
		ReversalLogId: reversalLogId,
		UserId:        origLog.UserId,
		ReversedQuota: origQuota,
	}, nil
}

// reversalRelayInfoFromLog 从原始消费日志重建最小 RelayInfo，供反向记账（成本 + 提成）使用。
// 关键字段：UserId / ChannelId / OriginModelName / UsingGroup / GroupRatio。
// GroupRatio 优先取原始日志 other.group_ratio，缺失时回退 1.0（历史日志兜底）。
func reversalRelayInfoFromLog(origLog *model.Log) *relaycommon.RelayInfo {
	groupRatio := 1.0
	if origLog.Other != "" {
		if otherMap, _ := common.StrToMap(origLog.Other); otherMap != nil {
			if v, ok := otherMap["group_ratio"].(float64); ok && v > 0 {
				groupRatio = v
			}
		}
	}
	info := &relaycommon.RelayInfo{
		UserId:          origLog.UserId,
		OriginModelName: origLog.ModelName,
		UsingGroup:      origLog.Group,
		PriceData: types.PriceData{
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio: groupRatio,
			},
		},
	}
	// ChannelId/ChannelName 在内嵌的 *ChannelMeta 上，必须非 nil 才能被记账逻辑安全读取。
	info.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelId: origLog.ChannelId,
	}
	return info
}

// maybeDisableUserForQuotaAnomaly 余额异常（负扣费 / 额度饱和）实时自动禁用。
//
// 设计要求：不能影响主流程。因此：
//   - 先做极廉价的同步短路（开关关闭 / 无异常时直接返回，不分配 goroutine）；
//   - 真正的禁用 + 记日志 + 通知放到 gopool.Go 异步执行，绝不阻塞或使请求失败。
func maybeDisableUserForQuotaAnomaly(userId int, quota int, saturated bool) {
	if !common.AutomaticDisableUserOnQuotaAnomalyEnabled || userId <= 0 {
		return
	}
	if !saturated && quota >= 0 {
		return
	}
	reason := "quota_saturation"
	if quota < 0 {
		reason = "negative_charge"
	}
	gopool.Go(func() {
		disabled, err := model.DisableUserForQuotaAnomaly(userId)
		if err != nil {
			common.SysError(fmt.Sprintf("余额异常自动禁用失败 user_id=%d: %s", userId, err.Error()))
			return
		}
		if !disabled {
			return // 已禁用 / 用户不存在
		}
		adminInfo := map[string]interface{}{
			"auto_disabled": true,
			"reason":        reason,
			"quota":         quota,
			"saturated":     saturated,
		}
		model.RecordLogWithAdminInfo(userId, model.LogTypeSystem,
			fmt.Sprintf("检测到余额异常（%s），已自动禁用用户", reason), adminInfo)
		common.SysLog(fmt.Sprintf("余额异常自动禁用：user_id=%d reason=%s quota=%d", userId, reason, quota))
	})
}
