package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// LogTaskConsumption records task consumption logs and stats. Actual charging
// is handled by BillingSession (PreConsumeBilling + SettleBilling).
func LogTaskConsumption(c *gin.Context, info *relaycommon.RelayInfo) {
	tokenName := c.GetString("token_name")
	logContent := fmt.Sprintf("operation %s", info.Action)
	if common.StringsContains(constant.TaskPricePatches, info.OriginModelName) {
		logContent = fmt.Sprintf("%s, charged per call", logContent)
	} else if len(info.PriceData.OtherRatios) > 0 {
		var contents []string
		for key, ratio := range info.PriceData.OtherRatios {
			if ratio != 1.0 {
				contents = append(contents, fmt.Sprintf("%s: %.2f", key, ratio))
			}
		}
		if len(contents) > 0 {
			logContent = fmt.Sprintf("%s, pricing params: %s", logContent, strings.Join(contents, ", "))
		}
	}

	other := make(map[string]interface{})
	other["is_task"] = true
	other["request_path"] = c.Request.URL.Path
	other["model_price"] = info.PriceData.ModelPrice
	if info.PriceData.ModelRatio > 0 {
		other["model_ratio"] = info.PriceData.ModelRatio
	}
	other["group_ratio"] = info.PriceData.GroupRatioInfo.GroupRatio
	if info.PriceData.GroupRatioInfo.HasSpecialRatio {
		other["user_group_ratio"] = info.PriceData.GroupRatioInfo.GroupSpecialRatio
	}
	if info.IsModelMapped {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = info.UpstreamModelName
	}
	attachQuotaSaturation(c, info, other)

	logID := model.RecordConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
		ChannelId: info.ChannelId,
		ModelName: info.OriginModelName,
		TokenName: tokenName,
		Quota:     info.PriceData.Quota,
		Content:   logContent,
		TokenId:   info.TokenId,
		Group:     info.UsingGroup,
		Other:     other,
	})
	model.UpdateUserUsedQuotaAndRequestCount(info.UserId, info.PriceData.Quota)
	model.UpdateChannelUsedQuota(info.ChannelId, info.PriceData.Quota)

	infoCopy := *info
	quotaCopy := info.PriceData.Quota
	go func() {
		RecordCostAndSettleEmployeeCommission(&infoCopy, quotaCopy, 0, logID)
	}()
}

func resolveTokenKey(ctx context.Context, tokenID int, taskID string) string {
	token, err := model.GetTokenById(tokenID)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("failed to load token key (tokenId=%d, task=%s): %s", tokenID, taskID, err.Error()))
		return ""
	}
	return token.Key
}

func taskIsSubscription(task *model.Task) bool {
	return task.PrivateData.BillingSource == BillingSourceSubscription && task.PrivateData.SubscriptionId > 0
}

// taskAdjustFunding adjusts the task funding source. delta > 0 charges more,
// delta < 0 refunds.
func taskAdjustFunding(task *model.Task, delta int) error {
	if taskIsSubscription(task) {
		return model.PostConsumeUserSubscriptionDelta(task.PrivateData.SubscriptionId, int64(delta))
	}
	if delta > 0 {
		return model.DecreaseUserQuota(task.UserId, delta, false)
	}
	return model.IncreaseUserQuota(task.UserId, -delta, false)
}

// taskAdjustTokenQuota adjusts token quota. delta > 0 charges more, delta < 0 refunds.
func taskAdjustTokenQuota(ctx context.Context, task *model.Task, delta int) {
	if task.PrivateData.TokenId <= 0 || delta == 0 {
		return
	}
	tokenKey := resolveTokenKey(ctx, task.PrivateData.TokenId, task.TaskID)
	if tokenKey == "" {
		return
	}

	var err error
	if delta > 0 {
		err = model.DecreaseTokenQuota(task.PrivateData.TokenId, tokenKey, delta)
	} else {
		err = model.IncreaseTokenQuota(task.PrivateData.TokenId, tokenKey, -delta)
	}
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("failed to adjust token quota (delta=%d, task=%s): %s", delta, task.TaskID, err.Error()))
	}
}

func taskBillingOther(task *model.Task) map[string]interface{} {
	other := make(map[string]interface{})
	if bc := task.PrivateData.BillingContext; bc != nil {
		other["model_price"] = bc.ModelPrice
		if bc.ModelRatio > 0 {
			other["model_ratio"] = bc.ModelRatio
		}
		other["group_ratio"] = bc.GroupRatio
		if len(bc.OtherRatios) > 0 {
			for key, value := range bc.OtherRatios {
				other[key] = value
			}
		}
	}
	props := task.Properties
	if props.UpstreamModelName != "" && props.UpstreamModelName != props.OriginModelName {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = props.UpstreamModelName
	}
	return other
}

func taskModelName(task *model.Task) string {
	if bc := task.PrivateData.BillingContext; bc != nil && bc.OriginModelName != "" {
		return bc.OriginModelName
	}
	return task.Properties.OriginModelName
}

func buildTaskLedgerRelayInfo(task *model.Task) *relaycommon.RelayInfo {
	modelName := taskModelName(task)
	groupRatio := ratio_setting.GetGroupRatio(task.Group)
	modelRatio := float64(0)
	modelPrice := float64(0)
	usePrice := false
	var otherRatios map[string]float64

	if bc := task.PrivateData.BillingContext; bc != nil {
		groupRatio = bc.GroupRatio
		modelRatio = bc.ModelRatio
		modelPrice = bc.ModelPrice
		usePrice = bc.PerCallBilling
		if bc.OriginModelName != "" {
			modelName = bc.OriginModelName
		}
		if len(bc.OtherRatios) > 0 {
			otherRatios = make(map[string]float64, len(bc.OtherRatios))
			for key, value := range bc.OtherRatios {
				otherRatios[key] = value
			}
		}
	} else {
		modelRatio, _, _ = ratio_setting.GetModelRatio(modelName)
	}

	return &relaycommon.RelayInfo{
		UserId:          task.UserId,
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: task.ChannelId},
		TokenId:         task.PrivateData.TokenId,
		UsingGroup:      task.Group,
		OriginModelName: modelName,
		PriceData: types.PriceData{
			ModelPrice:     modelPrice,
			ModelRatio:     modelRatio,
			UsePrice:       usePrice,
			OtherRatios:    otherRatios,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: groupRatio},
		},
	}
}

func recordTaskCostAndCommission(task *model.Task, quota int, logID int) {
	if task == nil || quota == 0 {
		return
	}
	ledgerInfo := buildTaskLedgerRelayInfo(task)
	go func() {
		RecordCostAndSettleEmployeeCommission(ledgerInfo, quota, 0, logID)
	}()
}

// RefundTaskQuota refunds pre-consumed task quota when an async task fails.
func RefundTaskQuota(ctx context.Context, task *model.Task, reason string) {
	quota := task.Quota
	if quota == 0 {
		return
	}

	if err := taskAdjustFunding(task, -quota); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("failed to refund task funding for %s: %s", task.TaskID, err.Error()))
		return
	}

	taskAdjustTokenQuota(ctx, task, -quota)

	other := taskBillingOther(task)
	other["task_id"] = task.TaskID
	other["reason"] = reason
	logID := model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   model.LogTypeRefund,
		Content:   "",
		ChannelId: task.ChannelId,
		ModelName: taskModelName(task),
		Quota:     quota,
		TokenId:   task.PrivateData.TokenId,
		Group:     task.Group,
		Other:     other,
	})
	recordTaskCostAndCommission(task, -quota, logID)
}

// RecalculateTaskQuota reconciles actual task cost against the pre-consumed quota.
func RecalculateTaskQuota(ctx context.Context, task *model.Task, actualQuota int, reason string, clamps ...*common.QuotaClamp) {
	if actualQuota <= 0 {
		return
	}
	preConsumedQuota := task.Quota
	quotaDelta := actualQuota - preConsumedQuota

	if quotaDelta == 0 {
		logger.LogInfo(ctx, fmt.Sprintf("task %s quota already accurate (%s, %s)",
			task.TaskID, logger.LogQuota(actualQuota), reason))
		return
	}

	logger.LogInfo(ctx, fmt.Sprintf("task %s quota reconciled: delta=%s (actual=%s, pre-consumed=%s, %s)",
		task.TaskID,
		logger.LogQuota(quotaDelta),
		logger.LogQuota(actualQuota),
		logger.LogQuota(preConsumedQuota),
		reason,
	))

	if err := taskAdjustFunding(task, quotaDelta); err != nil {
		logger.LogError(ctx, fmt.Sprintf("task quota funding adjustment failed for %s: %s", task.TaskID, err.Error()))
		return
	}

	taskAdjustTokenQuota(ctx, task, quotaDelta)
	task.Quota = actualQuota

	var logType int
	var logQuota int
	if quotaDelta > 0 {
		logType = model.LogTypeConsume
		logQuota = quotaDelta
		model.UpdateUserUsedQuotaAndRequestCount(task.UserId, quotaDelta)
		model.UpdateChannelUsedQuota(task.ChannelId, quotaDelta)
	} else {
		logType = model.LogTypeRefund
		logQuota = -quotaDelta
	}

	other := taskBillingOther(task)
	other["task_id"] = task.TaskID
	other["pre_consumed_quota"] = preConsumedQuota
	other["actual_quota"] = actualQuota
	for _, clamp := range clamps {
		attachQuotaSaturationToOther(other, clamp)
	}

	logID := model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   logType,
		Content:   reason,
		ChannelId: task.ChannelId,
		ModelName: taskModelName(task),
		Quota:     logQuota,
		TokenId:   task.PrivateData.TokenId,
		Group:     task.Group,
		Other:     other,
	})
	recordTaskCostAndCommission(task, quotaDelta, logID)
}

// RecalculateTaskQuotaByTokens recomputes billing from actual token usage.
func RecalculateTaskQuotaByTokens(ctx context.Context, task *model.Task, totalTokens int) {
	if totalTokens <= 0 {
		return
	}

	modelName := taskModelName(task)
	modelRatio, hasRatioSetting, _ := ratio_setting.GetModelRatio(modelName)
	if !hasRatioSetting || modelRatio <= 0 {
		return
	}

	group := task.Group
	if group == "" {
		user, err := model.GetUserById(task.UserId, false)
		if err == nil {
			group = user.Group
		}
	}
	if group == "" {
		return
	}

	groupRatio := ratio_setting.GetGroupRatio(group)
	userGroupRatio, hasUserGroupRatio := ratio_setting.GetGroupGroupRatio(group, group)

	finalGroupRatio := groupRatio
	if hasUserGroupRatio {
		finalGroupRatio = userGroupRatio
	}

	otherMultiplier := 1.0
	if bc := task.PrivateData.BillingContext; bc != nil {
		for _, ratio := range bc.OtherRatios {
			if ratio != 1.0 && ratio > 0 {
				otherMultiplier *= ratio
			}
		}
	}

	actualQuota, clamp := common.QuotaFromFloatChecked(float64(totalTokens) * modelRatio * finalGroupRatio * otherMultiplier)
	reason := fmt.Sprintf(
		"token recalculation: tokens=%d, modelRatio=%.2f, groupRatio=%.2f, otherMultiplier=%.4f",
		totalTokens, modelRatio, finalGroupRatio, otherMultiplier,
	)
	RecalculateTaskQuota(ctx, task, actualQuota, reason, clamp)
}
