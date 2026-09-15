package service

import (
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
)

// 恢复与历史清理，仅 master 节点执行。设计见 docs/design/channel-daily-quota-limit.md §7 与
// docs/design/channel-limit-upstream-basis-and-timed-recovery.md §5.3。
//
// 本地状态切换（pending 按自带计数键落库、清理作废的 total/tripped）由 flusher 在每个节点各自
// 完成，与本任务无关。

// channelDailyLimitTaskInterval 同时决定限时恢复的精度（约 30 秒）；按日模式不受影响。
const channelDailyLimitTaskInterval = 30 * time.Second

var channelDailyLimitTaskOnce sync.Once

// StartChannelDailyLimitRecoveryTask 启动恢复与历史清理任务（仅 master）。
func StartChannelDailyLimitRecoveryTask() {
	channelDailyLimitTaskOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			common.SysLog(fmt.Sprintf("channel daily limit recovery task started: tick=%s", channelDailyLimitTaskInterval))
			ticker := time.NewTicker(channelDailyLimitTaskInterval)
			defer ticker.Stop()
			runChannelDailyLimitRecoveryTick()
			for range ticker.C {
				runChannelDailyLimitRecoveryTick()
			}
		})
	})
}

func runChannelDailyLimitRecoveryTick() {
	defer func() {
		if err := recover(); err != nil {
			common.SysError(fmt.Sprintf("channel daily limit recovery tick panic: %v", err))
		}
	}()
	setting := operation_setting.GetChannelDailyLimitSnapshot()
	if setting == nil || !setting.Enabled {
		return
	}
	now := time.Now().Unix()
	today := operation_setting.ChannelDailyLimitStatDate(now, setting.Timezone)
	RecoverExpiredDailyLimitChannels(today)
	RecoverTimedLimitChannels(now)
	cleanupChannelDailyUsageHistory(setting, today)
}

// RecoverExpiredDailyLimitChannels 恢复所有「早于今日被限额禁用且开启了次日自动恢复」的按日模式渠道。
//
// 做成幂等扫描而不是依赖进程内的「上次日切日期」：master 停机跨过零点后重启，下一个 tick
// 就能补做，不会漏。
func RecoverExpiredDailyLimitChannels(today int64) {
	// 总开关从关到开之后，不补做关闭期间遗留的自动恢复，避免「一开开关就批量放行」。
	enabledAfter := dailyLimit.enabledAt.Load()
	ids, err := model.ListDailyLimitRecoveryCandidates(today, enabledAfter)
	if err != nil {
		common.SysError("channel daily limit: failed to list recovery candidates: " + err.Error())
		return
	}
	recovered := 0
	for _, channelId := range ids {
		ok, err := model.RecoverChannelFromDailyLimit(channelId, today)
		if err != nil {
			// 单个失败不中断其余渠道。
			common.SysError(fmt.Sprintf("channel daily limit: failed to recover channel_id=%d: %s", channelId, err.Error()))
			continue
		}
		if !ok {
			// 状态已被他人改动，静默跳过。
			continue
		}
		recovered++
		rearmTripped(channelId)
		common.SysLog(fmt.Sprintf("channel daily limit reset: channel_id=%d re-enabled", channelId))
		NotifyRootUser(formatNotifyType(channelId, common.ChannelStatusEnabled),
			fmt.Sprintf("通道 #%d 每日金额上限已重置并恢复启用", channelId),
			"新的一天开始，该通道的每日金额上限已重置，渠道已自动恢复启用。")
	}
	if recovered > 0 {
		common.SysLog(fmt.Sprintf("channel daily limit: recovered %d channel(s)", recovered))
	}
}

// RecoverTimedLimitChannels 恢复所有「限时模式、禁用已满恢复间隔」的渠道，并为它们开启新一轮。
//
// 同样是幂等扫描：master 停机期间到期的渠道，重启后的第一个 tick 就会补做。
func RecoverTimedLimitChannels(now int64) {
	enabledAfter := dailyLimit.enabledAt.Load()
	ids, err := model.ListTimedLimitRecoveryCandidates(now, enabledAfter)
	if err != nil {
		common.SysError("channel daily limit: failed to list timed recovery candidates: " + err.Error())
		return
	}
	recovered := 0
	for _, channelId := range ids {
		ok, err := model.RecoverChannelFromTimedLimit(channelId, now)
		if err != nil {
			common.SysError(fmt.Sprintf("channel daily limit: failed to recover timed channel_id=%d: %s", channelId, err.Error()))
			continue
		}
		if !ok {
			continue
		}
		recovered++
		rearmTripped(channelId)
		common.SysLog(fmt.Sprintf("channel timed limit reset: channel_id=%d re-enabled, new period starts at %d", channelId, now))
		NotifyRootUser(formatNotifyType(channelId, common.ChannelStatusEnabled),
			fmt.Sprintf("通道 #%d 已到恢复时间并恢复启用", channelId),
			"该通道本轮金额上限的恢复间隔已到，渠道已自动恢复启用，并从 0 开始新一轮计数。")
	}
	if recovered > 0 {
		// 立即把新的一轮刷进本节点快照；其它节点靠 flush 周期收敛，期间的旧轮次禁用请求
		// 会被 DisableChannelForTimedLimit 的轮次条件拒绝。
		RefreshChannelDailyLimitConfigs()
		common.SysLog(fmt.Sprintf("channel timed limit: recovered %d channel(s)", recovered))
	}
}

func cleanupChannelDailyUsageHistory(setting *operation_setting.ChannelDailyLimitSetting, today int64) {
	retention := setting.RetentionDays
	if retention < operation_setting.MinChannelDailyLimitRetentionDays {
		retention = operation_setting.MinChannelDailyLimitRetentionDays
	}
	cutoff := today - int64(retention)*86400
	logCleanup("historical usage", model.CleanupChannelDailyUsage, cutoff)
	logCleanup("historical limit period usage", model.CleanupChannelLimitPeriodUsage, cutoff)
	// 兜底清理孤儿行：渠道删除后，累计器里残留的增量可能再写回一行指向已删渠道的记录。
	logOrphanCleanup("usage", model.DeleteOrphanChannelDailyUsage)
	logOrphanCleanup("limit period usage", model.DeleteOrphanChannelLimitPeriodUsage)
}

func logCleanup(label string, cleanup func(int64) (int64, error), cutoff int64) {
	deleted, err := cleanup(cutoff)
	if err != nil {
		common.SysError(fmt.Sprintf("channel daily limit: failed to cleanup %s: %s", label, err.Error()))
		return
	}
	if deleted > 0 {
		common.SysLog(fmt.Sprintf("channel daily limit: cleaned up %d %s row(s)", deleted, label))
	}
}

func logOrphanCleanup(label string, cleanup func() (int64, error)) {
	orphans, err := cleanup()
	if err != nil {
		common.SysError(fmt.Sprintf("channel daily limit: failed to cleanup orphan %s rows: %s", label, err.Error()))
		return
	}
	if orphans > 0 {
		common.SysLog(fmt.Sprintf("channel daily limit: cleaned up %d orphan %s row(s)", orphans, label))
	}
}
