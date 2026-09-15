package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// flusher 与禁用 worker。设计见 docs/design/channel-daily-quota-limit.md §5.5 / §6.4 与
// docs/design/channel-limit-upstream-basis-and-timed-recovery.md §5。

// StartChannelDailyLimitWorkers 启动 flusher 与固定数量的禁用 worker。
// 所有节点都要启动（累计与落库是每节点各自进行的）；只有恢复与清理任务限 master。
func StartChannelDailyLimitWorkers() {
	dailyLimit.startOnce.Do(func() {
		// 先同步预热一次配置快照：否则首个 flush 周期（默认 5s）内快照是空的，
		// 这段时间的消费不会被任何渠道计入用量。
		if err := refreshDailyLimitConfigs(); err != nil {
			common.SysError("channel daily limit: initial config load failed: " + err.Error())
		}
		go runChannelDailyLimitFlusher()
		for i := 0; i < dailyLimitDisableWorkers; i++ {
			go runChannelDailyLimitDisableWorker()
		}
		common.SysLog(fmt.Sprintf("channel daily limit workers started: flusher=1, disable_workers=%d", dailyLimitDisableWorkers))
	})
}

func runChannelDailyLimitFlusher() {
	defer func() {
		if err := recover(); err != nil {
			common.SysError(fmt.Sprintf("channel daily limit flusher panic: %v", err))
			// 自愈：panic 后重新拉起，避免累计永久停摆。
			time.Sleep(time.Second)
			go runChannelDailyLimitFlusher()
		}
	}()
	backoff := time.Duration(0)
	for {
		sleep := dailyLimitFlushInterval
		if backoff > 0 {
			sleep = backoff
		}
		time.Sleep(sleep)

		setting := observeDailyLimitSwitch()
		if !setting.Enabled {
			backoff = 0
			continue
		}
		if err := refreshDailyLimitConfigs(); err != nil {
			common.SysError("channel daily limit: refresh configs failed: " + err.Error())
		}
		if err := flushChannelDailyUsage(setting); err != nil {
			// 增量保留在内存里退避重试，绝不丢弃：pending 是每个计数键一个定长计数器，
			// 容量上界是渠道数 × 少量键，不随请求量增长；丢弃会造成金额永久少计。
			backoff = nextFlushBackoff(backoff, dailyLimitFlushInterval)
			common.SysError(fmt.Sprintf("channel daily limit: flush failed, retrying in %s: %s", backoff, err.Error()))
			continue
		}
		backoff = 0
	}
}

func nextFlushBackoff(current, base time.Duration) time.Duration {
	if current <= 0 {
		current = base
	}
	next := current * 2
	if next > dailyLimitFlushMaxBackoff {
		next = dailyLimitFlushMaxBackoff
	}
	return next
}

// observeDailyLimitSwitch 读取当前配置快照并登记总开关的状态变化，返回本轮要用的快照。
//
// flusher 必须在 sleep **之后**调用它。若在 sleep 之前取快照，本轮用的就是一个周期前的
// 开关状态：总开关的关闭要等下一轮才生效，而关闭窗口短于一个周期时，这次「关→开」根本
// 观察不到，enabledAt 保持 0，恢复任务（channel_daily_limit_task.go:67/101）随后以
// enabledAfter=0 运行，把关闭期间到期的渠道一次性全部放行。
func observeDailyLimitSwitch() *operation_setting.ChannelDailyLimitSetting {
	setting := operation_setting.GetChannelDailyLimitSnapshot()
	trackEnabledTransition(setting.Enabled)
	return setting
}

// trackEnabledTransition 记录总开关从「关」切到「开」的时刻。
//
// 必须区分「首次观察」与「真正的 off→on 切换」：用零值 bool 做 prev 会把进程启动当成一次
// 切换，于是 enabledAt 被设成进程启动时间，恢复扫描随即跳过所有更早被禁用的渠道——
// 结果是任何一次重启都会让之前被限额禁用的渠道**永远不再自动恢复**。
func trackEnabledTransition(enabled bool) {
	next := dailyLimitSwitchOff
	if enabled {
		next = dailyLimitSwitchOn
	}
	prev := dailyLimit.lastEnabled.Swap(next)
	// prev == dailyLimitSwitchUnknown 表示这是进程内的第一次观察，不算切换。
	if prev == dailyLimitSwitchOff && next == dailyLimitSwitchOn {
		dailyLimit.enabledAt.Store(common.GetTimestamp())
	}
}

// RefreshChannelDailyLimitConfigs 立即刷新本节点的配置快照。
//
// 供渠道保存 / 批量设置 / 按 Tag 设置 / 限时恢复在写库成功后调用：不刷新的话，本节点要等到
// 下一个 flush 周期（默认 5s）才会用上新的上限或新的一轮。其它节点仍然靠周期刷新收敛，这也是
// 跨节点变更的传播上限。失败只记日志——周期刷新会兜底。
func RefreshChannelDailyLimitConfigs() {
	defer func() {
		if err := recover(); err != nil {
			common.SysError(fmt.Sprintf("channel daily limit: config refresh panic: %v", err))
		}
	}()
	if err := refreshDailyLimitConfigs(); err != nil {
		common.SysError("channel daily limit: config refresh failed: " + err.Error())
	}
}

// refreshDailyLimitConfigs 刷新配置快照，同时完成 tripped 的重新武装。
//
// 重新武装：管理员手动启用了某个已被限额禁用的渠道后，若不清除 tripped，该渠道将永远不会
// 再次触发禁用。这里在快照刷新时发现「有 tripped 但渠道已是启用态」就清除，收敛时间 ≤ 一个
// flush 周期。
func refreshDailyLimitConfigs() error {
	configs, err := model.LoadDailyLimitConfigs()
	if err != nil {
		return err
	}
	next := make(map[int]channelLimitConfig, len(configs))
	for _, cfg := range configs {
		next[cfg.ChannelId] = channelLimitConfig{
			limit:          cfg.LimitQuota,
			recoverMinutes: cfg.RecoverMinutes,
			periodStart:    cfg.PeriodStart,
		}
		if cfg.Status == common.ChannelStatusEnabled && cfg.DisabledAt == 0 {
			rearmTripped(cfg.ChannelId)
		}
	}
	dailyLimit.configs.Store(&next)
	return nil
}

// rearmTripped 清除某渠道所有计数键上的 tripped 标记，并为每个被清除的标记记下当时的
// 用量水位（见 dailyShard.rearmed）。
//
// 记水位这一步不能省：走到这里说明渠道刚从「限额禁用」态被放出来，而累计仍压在上限之上。
// 若不记水位，同一轮 flush 里紧跟着的 recheckAfterFlush 就会以「合计 >= 上限」为由立刻
// 再次禁用。限时模式下放行会开启新一轮，旧键上的水位随之作废，由 pruneOldEntries 清理。
func rearmTripped(channelId int) {
	shard := shardFor(channelId)
	shard.mu.Lock()
	for key := range shard.tripped {
		if key.channelId != channelId {
			continue
		}
		delete(shard.tripped, key)
		shard.rearmed[key] = currentTotalsLocked(shard, key)
	}
	shard.mu.Unlock()
}

// flushChannelDailyUsage 取出所有分片的增量落库，回读汇总刷新本地 total，并复判越限。
//
// 每条增量按它自己携带的计数键落库：按日键写 channel_daily_usages，轮次键写
// channel_limit_period_usages。跨零点无需任何「当前日期」变量，旧键的残留增量仍按旧键入库。
func flushChannelDailyUsage(setting *operation_setting.ChannelDailyLimitSetting) error {
	dayDeltas, periodDeltas, keys := drainPending()
	if len(dayDeltas) > 0 {
		processed, err := model.UpsertChannelDailyUsage(dayDeltas)
		if err != nil {
			// 只回退尚未落库的部分：整批回退会让已经写入的增量在下一轮被重复累加。
			restoreDayPending(dayDeltas[processed:])
			restorePeriodPending(periodDeltas)
			return err
		}
	}
	if len(periodDeltas) > 0 {
		processed, err := model.UpsertChannelLimitPeriodUsage(periodDeltas)
		if err != nil {
			restorePeriodPending(periodDeltas[processed:])
			return err
		}
	}
	if len(keys) == 0 {
		return nil
	}
	if err := refreshTotals(keys); err != nil {
		return err
	}
	today := currentStatDate(setting)
	pruneOldEntries(today)
	recheckAfterFlush(keys, today)
	return nil
}

func drainPending() ([]model.ChannelDailyDelta, []model.ChannelPeriodDelta, []dailyKey) {
	var dayDeltas []model.ChannelDailyDelta
	var periodDeltas []model.ChannelPeriodDelta
	var keys []dailyKey
	for _, shard := range dailyLimit.shards {
		shard.mu.Lock()
		for key, delta := range shard.pending {
			if delta.cost != 0 {
				if key.period {
					periodDeltas = append(periodDeltas, model.ChannelPeriodDelta{
						ChannelId:   key.channelId,
						PeriodStart: key.statDate,
						CostQuota:   delta.cost,
					})
				} else {
					dayDeltas = append(dayDeltas, model.ChannelDailyDelta{
						StatDate:  key.statDate,
						ChannelId: key.channelId,
						CostQuota: delta.cost,
					})
				}
			}
			keys = append(keys, key)
			delete(shard.pending, key)
		}
		// total 里已有的 key 也要复判（可能被其它节点推高）。
		for key := range shard.total {
			keys = append(keys, key)
		}
		shard.mu.Unlock()
	}
	return dayDeltas, periodDeltas, dedupKeys(keys)
}

func dedupKeys(keys []dailyKey) []dailyKey {
	if len(keys) <= 1 {
		return keys
	}
	seen := make(map[dailyKey]struct{}, len(keys))
	out := keys[:0]
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

// restoreDayPending / restorePeriodPending 落库失败时把增量放回内存，等待下一轮重试。
func restoreDayPending(deltas []model.ChannelDailyDelta) {
	for _, delta := range deltas {
		restorePending(dailyKey{statDate: delta.StatDate, channelId: delta.ChannelId}, delta.CostQuota)
	}
}

func restorePeriodPending(deltas []model.ChannelPeriodDelta) {
	for _, delta := range deltas {
		restorePending(dailyKey{statDate: delta.PeriodStart, channelId: delta.ChannelId, period: true}, delta.CostQuota)
	}
}

func restorePending(key dailyKey, cost int64) {
	shard := shardFor(key.channelId)
	shard.mu.Lock()
	addPendingLocked(shard, key, cost)
	shard.mu.Unlock()
}

// refreshTotals 回读汇总，得到包含其它节点增量的全局值。
func refreshTotals(keys []dailyKey) error {
	byDate := make(map[int64][]int)
	var periodKeys []model.ChannelPeriodKey
	for _, key := range keys {
		if key.period {
			periodKeys = append(periodKeys, model.ChannelPeriodKey{ChannelId: key.channelId, PeriodStart: key.statDate})
			continue
		}
		byDate[key.statDate] = append(byDate[key.statDate], key.channelId)
	}
	for statDate, channelIds := range byDate {
		rows, err := model.GetChannelDailyUsages(statDate, channelIds)
		if err != nil {
			return err
		}
		for _, channelId := range channelIds {
			total := dailyTotal{}
			if row := rows[channelId]; row != nil {
				total.cost = row.CostQuota
			}
			setTotal(dailyKey{statDate: statDate, channelId: channelId}, total)
		}
	}
	if len(periodKeys) > 0 {
		rows, err := model.GetChannelLimitPeriodUsages(periodKeys)
		if err != nil {
			return err
		}
		for _, periodKey := range periodKeys {
			setTotal(dailyKey{statDate: periodKey.PeriodStart, channelId: periodKey.ChannelId, period: true},
				dailyTotal{cost: rows[periodKey]})
		}
	}
	return nil
}

func setTotal(key dailyKey, total dailyTotal) {
	shard := shardFor(key.channelId)
	shard.mu.Lock()
	shard.total[key] = total
	shard.mu.Unlock()
}

// pruneOldEntries 清理已经不再参与判定的 total / tripped / rearmed 条目，内存自然回收：
//   - 按日键：早于今日的；
//   - 轮次键：渠道已不在限时模式，或该键不是渠道当前一轮的（渠道被恢复、手动启用、改了恢复
//     间隔之后，旧一轮就作废了）。
//
// 必须在 recheckAfterFlush 之前调用：先清掉作废键的 total，复判时读到的合计就是 0，
// 不会用昨天或上一轮的累计去禁用渠道。pending 不在此清理——它会按自带的键落库。
func pruneOldEntries(today int64) {
	for _, shard := range dailyLimit.shards {
		shard.mu.Lock()
		for key := range shard.total {
			if keyExpired(key, today) {
				delete(shard.total, key)
			}
		}
		for key := range shard.tripped {
			if keyExpired(key, today) {
				delete(shard.tripped, key)
			}
		}
		for key := range shard.rearmed {
			if keyExpired(key, today) {
				delete(shard.rearmed, key)
			}
		}
		shard.mu.Unlock()
	}
}

func keyExpired(key dailyKey, today int64) bool {
	if !key.period {
		return key.statDate < today
	}
	cfg, ok := lookupLimitConfig(key.channelId)
	return !ok || !cfg.timed() || cfg.periodStart != key.statDate
}

// recheckAfterFlush 用刷新后的全局汇总复判越限，捕捉「单节点增量未越限但全网已越限」。
// 只复判渠道当前参与判定的那个键：限时模式的今日键只供展示，永远不触发禁用。
func recheckAfterFlush(keys []dailyKey, today int64) {
	for _, key := range keys {
		cfg, ok := lookupLimitConfig(key.channelId)
		if !ok || cfg.limit <= 0 {
			continue
		}
		if key != limitKey(cfg, key.channelId, today) {
			continue
		}
		shard := shardFor(key.channelId)
		shard.mu.Lock()
		if _, tripped := shard.tripped[key]; tripped {
			shard.mu.Unlock()
			continue
		}
		total, reached := limitReachedLocked(shard, key, cfg)
		if reached {
			markTrippedLocked(shard, key)
		}
		shard.mu.Unlock()
		if reached {
			submitDisable(newDisableRequest(key, today, cfg, total))
		}
	}
}

func runChannelDailyLimitDisableWorker() {
	defer func() {
		if err := recover(); err != nil {
			common.SysError(fmt.Sprintf("channel daily limit disable worker panic: %v", err))
			time.Sleep(time.Second)
			go runChannelDailyLimitDisableWorker()
		}
	}()
	for req := range dailyLimit.disableQueue {
		handleDisableRequest(req)
	}
}

func handleDisableRequest(req disableRequest) {
	defer func() {
		if err := recover(); err != nil {
			clearTripped(req.key())
			common.SysError(fmt.Sprintf("channel daily limit disable panic: channel_id=%d, error=%v", req.channelId, err))
		}
	}()

	// 队列里的请求是**提交时刻**判定的，worker 执行时世界可能已经变了：总开关被关掉、
	// 上限被取消或调高、已经跨日、或者限时渠道已经进入新的一轮。任何一条成立，这个请求就
	// 不该再落地。
	if !disableRequestStillValid(req) {
		// 用 clearTripped 而不是 releaseTripped：这里不是「已经禁用过了」，
		// 而是「这次判定作废」，要让下一轮 recheck 按**当前**配置重新判。
		clearTripped(req.key())
		return
	}

	reason := formatDailyLimitReason(req)
	var disabled bool
	var err error
	if req.period {
		disabled, err = model.DisableChannelForTimedLimit(req.channelId, req.statDate, req.disabledDate(), reason)
	} else {
		disabled, err = model.DisableChannelForDailyLimit(req.channelId, req.statDate, reason)
	}
	if err != nil {
		clearTripped(req.key())
		common.SysError(fmt.Sprintf("channel daily limit: failed to disable channel_id=%d: %s", req.channelId, err.Error()))
		return
	}
	if !disabled {
		// 渠道已不是启用态（管理员刚禁用、上游报错禁用、另一节点抢先），或限时渠道已不在
		// 请求所属的那一轮。本次无事可做，但不能只清标记就走——见 releaseTripped 的注释。
		releaseTripped(req.key())
		return
	}

	common.SysLog(fmt.Sprintf("channel daily limit reached: channel_id=%d, timed=%t, used=%d, limit=%d",
		req.channelId, req.period, req.used, req.limit))
	subject := fmt.Sprintf("通道 #%d 已达每日金额上限并被禁用", req.channelId)
	if req.period {
		subject = fmt.Sprintf("通道 #%d 本轮已达金额上限并被禁用", req.channelId)
	}
	NotifyRootUser(formatNotifyType(req.channelId, common.ChannelStatusAutoDisabled), subject, reason)
}

func formatDailyLimitReason(req disableRequest) string {
	if req.period {
		return fmt.Sprintf("本轮上游消耗 %s，已达上限 %s，自动禁用，%d 分钟后自动恢复并开始新一轮",
			formatDailyLimitAmount(req.used), formatDailyLimitAmount(req.limit), req.recoverMinutes)
	}
	return fmt.Sprintf("每日金额上限触发自动禁用：今日上游消耗 %s，上限 %s",
		formatDailyLimitAmount(req.used), formatDailyLimitAmount(req.limit))
}

// formatDailyLimitAmount 按系统的额度展示口径输出金额。不用 LogQuota：它会在金额后缀上「额度」
// 并固定 6 位小数（＄1.000000 额度）。「点数」口径没有货币符号，保留「点额度」单位。
func formatDailyLimitAmount(quota int64) string {
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		return logger.LogQuota(int(quota))
	}
	return trimAmountZeros(logger.FormatQuota(int(quota)))
}

// trimAmountZeros 去掉金额小数部分多余的 0，至少保留两位小数：＄1.000000 → ＄1.00，＄1.004800 → ＄1.0048。
func trimAmountZeros(amount string) string {
	dot := strings.LastIndexByte(amount, '.')
	if dot < 0 {
		return amount
	}
	end := len(amount)
	for end > dot+3 && amount[end-1] == '0' {
		end--
	}
	return amount[:end]
}

// disableRequestStillValid 重新确认一个排队中的禁用请求现在是否仍然成立：
//  1. 总开关仍开着（Rule 0：关掉就必须立即停止一切副作用）；
//  2. 渠道仍配置了正数上限（管理员可能刚把上限清成 0）；
//  3. 计数键仍是渠道当前参与判定的键——按日模式要求仍是今天、且没切到限时模式；
//     限时模式要求仍在同一轮、且没切回按日模式；
//  4. 按**当前**上限重新算，累计仍然越线（上限可能被调高）。
func disableRequestStillValid(req disableRequest) bool {
	setting := operation_setting.GetChannelDailyLimitSnapshot()
	if setting == nil || !setting.Enabled {
		return false
	}
	cfg, ok := lookupLimitConfig(req.channelId)
	if !ok || cfg.limit <= 0 {
		return false
	}
	if req.period {
		if !cfg.timed() || cfg.periodStart != req.statDate {
			return false
		}
	} else if cfg.timed() || currentStatDate(setting) != req.statDate {
		return false
	}
	shard := shardFor(req.channelId)
	shard.mu.Lock()
	total := totalLocked(shard, req.key())
	shard.mu.Unlock()
	return total >= cfg.limit
}
