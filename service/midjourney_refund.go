package service

import (
	"context"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// ChargeMidjourneySubmission charges a Midjourney submission (the wallet, then
// the token unless the request came from the playground) and records on the
// task what was actually charged, so a later failure refunds exactly that and
// never money that was not taken: a failed wallet charge records nothing, a
// failed token charge records the wallet amount without the token. Returns
// whether the wallet was charged; the caller counts usage and writes the
// consume log only then. task may be nil or unsaved (insert failed): the charge
// still happens, there is just nothing to record it on.
//
// Midjourney submissions are always wallet-billed (the Midjourney relay never
// selects a subscription), matching the wallet-only refund.
func ChargeMidjourneySubmission(relayInfo *relaycommon.RelayInfo, task *model.Midjourney, quota int) bool {
	charged, tokenId := 0, 0
	if quota > 0 {
		if err := model.DecreaseUserQuota(relayInfo.UserId, quota, false); err != nil {
			common.SysLog(fmt.Sprintf("Midjourney 扣费失败 user %d: %s", relayInfo.UserId, err.Error()))
			midjourneyRecordCharge(task, 0, 0)
			return false
		}
		charged = quota
		if !relayInfo.IsPlayground {
			if err := model.DecreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, quota); err != nil {
				common.SysLog(fmt.Sprintf("Midjourney 扣减令牌额度失败 token %d: %s", relayInfo.TokenId, err.Error()))
			} else {
				tokenId = relayInfo.TokenId
			}
		}
		checkAndSendQuotaNotify(relayInfo, quota, 0)
	}
	midjourneyRecordCharge(task, charged, tokenId)
	return true
}

func midjourneyRecordCharge(task *model.Midjourney, quota, tokenId int) {
	if task == nil || task.Id == 0 {
		return
	}
	if err := task.SetChargedBilling(quota, tokenId); err != nil {
		common.SysLog(fmt.Sprintf("Midjourney 记录扣费状态失败 task %s: %s", task.MjId, err.Error()))
	}
}

// RefundMidjourneyQuota reverses what a failed Midjourney task's submission
// recorded: the user's balance, the token quota (when the submission charged a
// token), and the usage the submission added to users.used_quota and
// channels.used_quota (the request count stays). It writes one refund log with
// the token id.
//
// The refund amount is claimed first with a compare-and-set on the task's quota
// (quota -> 0), so a task is refunded at most once even if two callers reach
// here. Returns whether the balance was refunded; when that fails the amount is
// put back on the task for manual reconciliation.
func RefundMidjourneyQuota(ctx context.Context, task *model.Midjourney, reason string) bool {
	quota := task.Quota
	if quota <= 0 {
		return true
	}
	claimed, err := task.ClaimRefundQuota(quota)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("Midjourney 退款认领失败 task %s: %s", task.MjId, err.Error()))
		return false
	}
	if !claimed {
		logger.LogInfo(ctx, fmt.Sprintf("Midjourney 任务 %s 已退款，跳过", task.MjId))
		return true
	}
	task.Quota = 0

	if err := model.IncreaseUserQuota(task.UserId, quota, false); err != nil {
		logger.LogError(ctx, fmt.Sprintf("退还 Midjourney 用户额度失败 task %s: %s", task.MjId, err.Error()))
		if restoreErr := task.RestoreRefundQuota(quota); restoreErr != nil {
			logger.LogError(ctx, fmt.Sprintf("Midjourney 退款失败且恢复 quota 失败 task %s: %s", task.MjId, restoreErr.Error()))
		} else {
			task.Quota = quota
		}
		return false
	}

	if task.TokenId > 0 {
		if tokenKey := resolveTokenKey(ctx, task.TokenId, task.MjId); tokenKey != "" {
			if err := model.IncreaseTokenQuota(task.TokenId, tokenKey, quota); err != nil {
				logger.LogWarn(ctx, fmt.Sprintf("退还 Midjourney 令牌额度失败 task %s: %s", task.MjId, err.Error()))
			}
		}
	}

	model.UpdateUserUsedQuota(task.UserId, -quota)
	model.UpdateChannelUsedQuota(task.ChannelId, -quota)

	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   model.LogTypeRefund,
		Content:   "",
		ChannelId: task.ChannelId,
		ModelName: CovertMjpActionToModelName(task.Action),
		Quota:     quota,
		TokenId:   task.TokenId,
		Other: map[string]interface{}{
			"task_id": task.MjId,
			"reason":  reason,
		},
	})
	return true
}
