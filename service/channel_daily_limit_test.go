package service

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 渠道每日金额上限累计器的测试。设计见 docs/design/channel-daily-quota-limit.md §5、§6.4。

// resetDailyLimitState 清空累计器的全部进程内状态，让每个用例互不干扰。
func resetDailyLimitState(t *testing.T) {
	t.Helper()
	for _, shard := range dailyLimit.shards {
		shard.mu.Lock()
		shard.pending = make(map[dailyKey]*dailyDelta)
		shard.total = make(map[dailyKey]dailyTotal)
		shard.tripped = make(map[dailyKey]struct{})
		shard.rearmed = make(map[dailyKey]dailyTotal)
		shard.mu.Unlock()
	}
	empty := map[int]channelLimitConfig{}
	dailyLimit.configs.Store(&empty)
	dailyLimit.dayRange.Store(nil)
	dailyLimit.enabledAt.Store(0)
	dailyLimit.lastEnabled.Store(dailyLimitSwitchUnknown)
	drainDisableQueue()
	t.Cleanup(func() { drainDisableQueue() })
}

func drainDisableQueue() {
	for {
		select {
		case <-dailyLimit.disableQueue:
		default:
			return
		}
	}
}

// setDailyLimitConfigs 直接注入配置快照，绕开 DB 刷新。
func setDailyLimitConfigs(configs map[int]channelLimitConfig) {
	next := make(map[int]channelLimitConfig, len(configs))
	for k, v := range configs {
		next[k] = v
	}
	dailyLimit.configs.Store(&next)
}

// enableDailyLimitSetting 打开总开关并在用例结束后还原。
func enableDailyLimitSetting(t *testing.T, enabled bool) {
	t.Helper()
	orig := operation_setting.GetChannelDailyLimitSetting()
	next := orig
	next.Enabled = enabled
	if next.Timezone == "" {
		next.Timezone = operation_setting.DefaultChannelDailyLimitTimezone
	}
	operation_setting.ReplaceChannelDailyLimitSetting(next)
	t.Cleanup(func() { operation_setting.ReplaceChannelDailyLimitSetting(orig) })
}

func pendingUsed(channelId int, statDate int64) int64 {
	shard := shardFor(channelId)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	delta := shard.pending[dailyKey{statDate: statDate, channelId: channelId}]
	if delta == nil {
		return 0
	}
	return delta.cost
}

func isTripped(channelId int, statDate int64) bool {
	shard := shardFor(channelId)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	_, ok := shard.tripped[dailyKey{statDate: statDate, channelId: channelId}]
	return ok
}

// hasReleaseMark 报告某 (日期, 渠道) 是否记录了人工放行水位。
func hasReleaseMark(channelId int, statDate int64) bool {
	shard := shardFor(channelId)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	_, ok := shard.rearmed[dailyKey{statDate: statDate, channelId: channelId}]
	return ok
}

func todayStatDate() int64 {
	setting := operation_setting.GetChannelDailyLimitSnapshot()
	return operation_setting.ChannelDailyLimitStatDate(time.Now().Unix(), setting.Timezone)
}

// TestDailyLimit_ThresholdBoundaries 覆盖上限判定的边界值：limit-1 / limit / limit+1。
func TestDailyLimit_ThresholdBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		amount  int64
		tripped bool
	}{
		{"just below the limit", 99, false},
		{"exactly at the limit", 100, true},
		{"above the limit", 101, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetDailyLimitState(t)
			enableDailyLimitSetting(t, true)
			channelId := 900001
			setDailyLimitConfigs(map[int]channelLimitConfig{
				channelId: {limit: 100},
			})

			accumulate(channelId, tc.amount)

			statDate := todayStatDate()
			assert.Equal(t, tc.tripped, isTripped(channelId, statDate))
			assert.EqualValues(t, tc.amount, pendingUsed(channelId, statDate), "amount must be accumulated regardless")
		})
	}
}

// TestDailyLimit_UnlimitedChannelsAreFree 验证未配置上限的渠道零开销：不占内存、不判定。
func TestDailyLimit_UnlimitedChannelsAreFree(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	statDate := todayStatDate()

	// 完全不在快照里的渠道。
	accumulate(900002, 10_000)
	assert.Zero(t, pendingUsed(900002, statDate))

	// 在快照里但 limit <= 0。
	setDailyLimitConfigs(map[int]channelLimitConfig{
		900003: {limit: 0},
		900004: {limit: -5},
	})
	accumulate(900003, 10_000)
	accumulate(900004, 10_000)
	assert.Zero(t, pendingUsed(900003, statDate))
	assert.Zero(t, pendingUsed(900004, statDate))
}

// TestDailyLimit_DisabledSwitchStopsEverything 验证总开关是有效的关停开关（Rule 0）。
func TestDailyLimit_DisabledSwitchStopsEverything(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, false)
	channelId := 900005
	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 1},
	})

	accumulate(channelId, 10_000)
	assert.Zero(t, pendingUsed(channelId, todayStatDate()))
	assert.False(t, isTripped(channelId, todayStatDate()))
}

// TestDailyLimit_IgnoresNonPositiveAmounts 覆盖 quota<=0 与非法 channelId。
func TestDailyLimit_IgnoresNonPositiveAmounts(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	channelId := 900006
	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 100},
	})

	accumulate(channelId, 0)
	accumulate(channelId, -50)
	accumulate(channelId, 0)
	accumulate(0, 50)
	assert.Zero(t, pendingUsed(channelId, todayStatDate()))
}

func periodKeyOf(channelId int, periodStart int64) dailyKey {
	return dailyKey{statDate: periodStart, channelId: channelId, period: true}
}

func pendingOf(key dailyKey) int64 {
	shard := shardFor(key.channelId)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if delta := shard.pending[key]; delta != nil {
		return delta.cost
	}
	return 0
}

func trippedOf(key dailyKey) bool {
	shard := shardFor(key.channelId)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	_, ok := shard.tripped[key]
	return ok
}

// TestDailyLimit_TimedModeJudgesByRound 限时模式：今日用量照记（展示），本轮用量另记一份，
// 判定只看本轮；上一轮的累计不参与。
func TestDailyLimit_TimedModeJudgesByRound(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	statDate := todayStatDate()
	channelId := 900007
	round := periodKeyOf(channelId, 5_000)

	// 今日已经跑了很多（上一轮），本轮刚开始。
	setDailyTotal(channelId, statDate, dailyTotal{cost: 10_000})
	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 100, recoverMinutes: 30, periodStart: 5_000},
	})

	accumulate(channelId, 60)
	assert.EqualValues(t, 60, pendingUsed(channelId, statDate), "today's usage is still recorded for display")
	assert.EqualValues(t, 60, pendingOf(round), "the round gets its own counter")
	assert.False(t, isTripped(channelId, statDate), "the day counter never trips in timed mode")
	assert.False(t, trippedOf(round), "60 < 100 within this round")
	assert.Empty(t, dailyLimit.disableQueue)

	accumulate(channelId, 40)
	require.True(t, trippedOf(round))
	require.Len(t, dailyLimit.disableQueue, 1)
	req := <-dailyLimit.disableQueue
	assert.True(t, req.period)
	assert.EqualValues(t, 5_000, req.statDate)
	assert.EqualValues(t, statDate, req.disabledDate(), "the disable is stamped with today for the 'limit reached' filter")
	assert.EqualValues(t, 100, req.used)
	assert.Equal(t, 30, req.recoverMinutes)

	// 按日模式的同一份今日累计则直接越限。
	daily := 900008
	setDailyTotal(daily, statDate, dailyTotal{cost: 10_000})
	setDailyLimitConfigs(map[int]channelLimitConfig{daily: {limit: 100}})
	accumulate(daily, 1)
	assert.True(t, isTripped(daily, statDate))
	assert.False(t, trippedOf(periodKeyOf(daily, 0)))
}

// TestDailyLimit_TimedRecheckAndPruneFollowTheCurrentRound flush 后的复判只看当前一轮；
// 进入新一轮后旧轮次的 total / tripped / rearmed 被清理，今日键在限时模式下永远不触发禁用。
func TestDailyLimit_TimedRecheckAndPruneFollowTheCurrentRound(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	statDate := todayStatDate()
	channelId := 900009
	oldRound := periodKeyOf(channelId, 5_000)
	newRound := periodKeyOf(channelId, 6_000)
	dayKey := dailyKey{statDate: statDate, channelId: channelId}

	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 100, recoverMinutes: 30, periodStart: 5_000},
	})
	setTotal(oldRound, dailyTotal{cost: 150})
	setTotal(dayKey, dailyTotal{cost: 999})

	recheckAfterFlush([]dailyKey{dayKey}, statDate)
	assert.Empty(t, dailyLimit.disableQueue, "the day key is display-only in timed mode")
	recheckAfterFlush([]dailyKey{oldRound}, statDate)
	require.Len(t, dailyLimit.disableQueue, 1, "other nodes pushed the round over the limit")
	drainDisableQueue()

	// 渠道恢复进入新一轮：旧轮次作废。
	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 100, recoverMinutes: 30, periodStart: 6_000},
	})
	rearmTripped(channelId)
	pruneOldEntries(statDate)
	shard := shardFor(channelId)
	shard.mu.Lock()
	_, oldTotal := shard.total[oldRound]
	_, oldMark := shard.rearmed[oldRound]
	_, dayTotal := shard.total[dayKey]
	shard.mu.Unlock()
	assert.False(t, oldTotal, "a finished round's total is pruned")
	assert.False(t, oldMark, "a finished round's release mark is pruned")
	assert.True(t, dayTotal, "today's total stays for display")

	recheckAfterFlush([]dailyKey{oldRound, newRound, dayKey}, statDate)
	assert.Empty(t, dailyLimit.disableQueue, "a new round starts from zero")

	// 切回按日模式：轮次键全部作废，今日键重新参与判定。
	setTotal(newRound, dailyTotal{cost: 50})
	setDailyLimitConfigs(map[int]channelLimitConfig{channelId: {limit: 100}})
	assert.True(t, keyExpired(newRound, statDate))
	assert.False(t, keyExpired(dayKey, statDate))
	recheckAfterFlush([]dailyKey{dayKey}, statDate)
	assert.Len(t, dailyLimit.disableQueue, 1, "back in day mode, today's usage counts again")
}

// TestDailyLimit_TimedDisableRequestValidity 排队中的限时禁用请求：轮次变了、切回按日模式都作废；
// 按日请求在渠道切到限时模式后同样作废。
func TestDailyLimit_TimedDisableRequestValidity(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	statDate := todayStatDate()
	channelId := 900010
	cfg := channelLimitConfig{limit: 100, recoverMinutes: 30, periodStart: 5_000}
	setDailyLimitConfigs(map[int]channelLimitConfig{channelId: cfg})
	round := periodKeyOf(channelId, 5_000)
	setTotal(round, dailyTotal{cost: 100})
	req := newDisableRequest(round, statDate, cfg, 100)
	require.True(t, disableRequestStillValid(req))

	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 100, recoverMinutes: 30, periodStart: 6_000},
	})
	assert.False(t, disableRequestStillValid(req), "the round moved on")

	setDailyLimitConfigs(map[int]channelLimitConfig{channelId: {limit: 100}})
	assert.False(t, disableRequestStillValid(req), "the channel switched back to day mode")

	dayReq := seedTrippedRequest(t, channelId, statDate, 100)
	require.True(t, disableRequestStillValid(dayReq))
	setDailyLimitConfigs(map[int]channelLimitConfig{channelId: cfg})
	assert.False(t, disableRequestStillValid(dayReq), "a day request is void once the channel is timed")
}

// TestDailyLimit_TimedFlushPersistsRound 按轮次落库并回读：两张表各自累加，互不串号。
func TestDailyLimit_TimedFlushPersistsRound(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	statDate := todayStatDate()
	ch := &model.Channel{
		Id:                       9_900_110,
		Type:                     1,
		Key:                      fmt.Sprintf("dlk-%d", time.Now().UnixNano()),
		Name:                     "daily-limit-timed-flush",
		Status:                   common.ChannelStatusEnabled,
		Models:                   "gpt-4o",
		Group:                    "default",
		DailyQuotaLimit:          1_000,
		DailyLimitRecoverMinutes: 30,
		DailyLimitPeriodStart:    common.GetTimestamp(),
	}
	require.NoError(t, model.DB.Create(ch).Error)
	t.Cleanup(func() {
		model.DB.Unscoped().Delete(&model.Channel{}, ch.Id)
		_, _ = model.DeleteChannelDailyUsageByChannelIds([]int{ch.Id})
		_, _ = model.DeleteChannelLimitPeriodUsageByChannelIds([]int{ch.Id})
	})
	setDailyLimitConfigs(map[int]channelLimitConfig{
		ch.Id: {limit: 1_000, recoverMinutes: 30, periodStart: ch.DailyLimitPeriodStart},
	})

	accumulate(ch.Id, 70)
	accumulate(ch.Id, 30)
	setting := operation_setting.GetChannelDailyLimitSnapshot()
	require.NoError(t, flushChannelDailyUsage(setting))

	round := periodKeyOf(ch.Id, ch.DailyLimitPeriodStart)
	rows, err := model.GetChannelLimitPeriodUsages([]model.ChannelPeriodKey{{ChannelId: ch.Id, PeriodStart: ch.DailyLimitPeriodStart}})
	require.NoError(t, err)
	assert.EqualValues(t, 100, rows[model.ChannelPeriodKey{ChannelId: ch.Id, PeriodStart: ch.DailyLimitPeriodStart}])
	day, err := model.GetChannelDailyUsages(statDate, []int{ch.Id})
	require.NoError(t, err)
	require.NotNil(t, day[ch.Id])
	assert.EqualValues(t, 100, day[ch.Id].CostQuota)

	shard := shardFor(ch.Id)
	shard.mu.Lock()
	assert.EqualValues(t, 100, shard.total[round].cost, "the round total is read back after flush")
	assert.Empty(t, shard.pending)
	shard.mu.Unlock()
}

// TestDailyLimit_TrippedPreventsDuplicateSubmission 验证同一天不重复提交禁用。
func TestDailyLimit_TrippedPreventsDuplicateSubmission(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	channelId := 900009
	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 10},
	})

	accumulate(channelId, 10)
	accumulate(channelId, 10)
	accumulate(channelId, 10)

	assert.Len(t, dailyLimit.disableQueue, 1, "only one disable request per channel per day")
	// 但金额仍然继续累计，数字要准。
	assert.EqualValues(t, 30, pendingUsed(channelId, todayStatDate()))
}

// TestDailyLimit_RearmAfterManualEnable 是审计阻断问题 #2 的回归测试。
//
// v3 只在跨日时清 tripped，导致「禁用 → 管理员手动启用 → 继续消费」的场景下当天再也不会
// 触发禁用，与设计承诺矛盾。
func TestDailyLimit_RearmAfterManualEnable(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	channelId := 900010
	statDate := todayStatDate()
	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 10},
	})

	// 第一次达标 → 提交禁用。
	accumulate(channelId, 10)
	require.True(t, isTripped(channelId, statDate))
	drainDisableQueue()

	// 管理员手动启用后，配置刷新时应重新武装。
	rearmTripped(channelId)
	assert.False(t, isTripped(channelId, statDate), "manual enable must rearm the trip")

	// 再消费 1 quota → 必须再次触发禁用。
	accumulate(channelId, 1)
	assert.True(t, isTripped(channelId, statDate), "channel must be disabled again after further consumption")
	assert.Len(t, dailyLimit.disableQueue, 1)
}

// setDailyTotal 直接注入「已落库汇总」，模拟其它节点推高后的全局值。
func setDailyTotal(channelId int, statDate int64, total dailyTotal) {
	shard := shardFor(channelId)
	shard.mu.Lock()
	shard.total[dailyKey{statDate: statDate, channelId: channelId}] = total
	shard.mu.Unlock()
}

// TestDailyLimit_ManualEnableSurvivesRecheck 是「手动启用被下一个 flush 周期撤销」的回归测试。
//
// 场景：渠道当日累计已经压在上限之上并被禁用，管理员手动启用。下一轮 flusher 先
// rearmTripped 清标记，紧接着 recheckAfterFlush 复判——如果复判只看「合计 >= 上限」，
// 就会在没有任何新消费的情况下立刻再次禁用，管理员当天拿不回这个渠道。
func TestDailyLimit_ManualEnableSurvivesRecheck(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	channelId := 900030
	statDate := todayStatDate()
	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 100},
	})

	// 达到上限 → 禁用；随后增量落库，累计留在 total 里。
	accumulate(channelId, 100)
	require.True(t, isTripped(channelId, statDate))
	drainDisableQueue()
	drainPending()
	setDailyTotal(channelId, statDate, dailyTotal{cost: 100})

	// 管理员手动启用 → 下一轮配置刷新重新武装。
	rearmTripped(channelId)
	require.False(t, isTripped(channelId, statDate))

	// 同一轮 flush 的复判：没有任何新消费，必须放过。
	recheckAfterFlush([]dailyKey{{statDate: statDate, channelId: channelId}}, statDate)
	assert.False(t, isTripped(channelId, statDate), "manual enable must not be undone without new consumption")
	assert.Empty(t, dailyLimit.disableQueue, "no disable request may be submitted without new consumption")

	// 反复复判同样不得触发——否则每个 flush 周期都会重试一次。
	recheckAfterFlush([]dailyKey{{statDate: statDate, channelId: channelId}}, statDate)
	assert.False(t, isTripped(channelId, statDate))
	assert.Empty(t, dailyLimit.disableQueue)

	// 但只要再产生一分钱消费，就必须立刻重新禁用。
	accumulate(channelId, 1)
	assert.True(t, isTripped(channelId, statDate), "further consumption must disable the channel again")
	assert.Len(t, dailyLimit.disableQueue, 1)
}

// TestDailyLimit_ManualEnableThenFlushRecheck 覆盖同一场景下由 flush 侧发现的新增量：
// 放行水位之上的消费即使来自**其它节点**（体现为回读后的 total 变高），也要重新触发。
func TestDailyLimit_ManualEnableThenFlushRecheck(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	channelId := 900031
	statDate := todayStatDate()
	key := dailyKey{statDate: statDate, channelId: channelId}
	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 100},
	})

	accumulate(channelId, 100)
	drainDisableQueue()
	drainPending()
	setDailyTotal(channelId, statDate, dailyTotal{cost: 100})
	rearmTripped(channelId)

	// 其它节点又推高了 5 → 回读后 total 高于放行水位。
	setDailyTotal(channelId, statDate, dailyTotal{cost: 105})
	recheckAfterFlush([]dailyKey{key}, key.statDate)

	assert.True(t, isTripped(channelId, statDate), "usage from another node above the release mark must re-trip")
	assert.Len(t, dailyLimit.disableQueue, 1)
}

// TestDailyLimit_LoweredLimitStillDisablesImmediately 锁住上面那个修复的边界：
// 放行水位只在**确实清掉了 tripped 标记**时记录。管理员把上限调到今日已用之下时
// 渠道从未 tripped 过，因此必须照旧在下一个 flush 周期立即禁用，不能被误豁免。
func TestDailyLimit_LoweredLimitStillDisablesImmediately(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	channelId := 900032
	statDate := todayStatDate()
	key := dailyKey{statDate: statDate, channelId: channelId}

	// 今日已用 100，上限原本很高，从未触发。
	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 10_000},
	})
	setDailyTotal(channelId, statDate, dailyTotal{cost: 100})
	rearmTripped(channelId) // 配置刷新每轮都会对启用态渠道调用，这里必须是无副作用的
	recheckAfterFlush([]dailyKey{key}, key.statDate)
	require.False(t, isTripped(channelId, statDate))

	// 管理员把上限调到 50（低于今日已用）→ 下一轮复判必须立即禁用。
	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 50},
	})
	recheckAfterFlush([]dailyKey{key}, key.statDate)

	assert.True(t, isTripped(channelId, statDate), "lowering the limit below today's usage must disable at once")
	assert.Len(t, dailyLimit.disableQueue, 1)
}

// TestDailyLimit_PruneClearsRearmMarks 保证放行水位不会跨日残留：
// 昨天的水位若留到今天，今天的判定会被错误豁免。
func TestDailyLimit_PruneClearsRearmMarks(t *testing.T) {
	resetDailyLimitState(t)
	channelId := 900033
	today := todayStatDate()
	yesterday := today - 86400

	shard := shardFor(channelId)
	shard.mu.Lock()
	shard.rearmed[dailyKey{statDate: yesterday, channelId: channelId}] = dailyTotal{cost: 100}
	shard.rearmed[dailyKey{statDate: today, channelId: channelId}] = dailyTotal{cost: 5}
	shard.mu.Unlock()

	pruneOldEntries(today)

	shard.mu.Lock()
	defer shard.mu.Unlock()
	_, old := shard.rearmed[dailyKey{statDate: yesterday, channelId: channelId}]
	_, now := shard.rearmed[dailyKey{statDate: today, channelId: channelId}]
	assert.False(t, old, "yesterday's release mark must be pruned")
	assert.True(t, now, "today's release mark must be kept")
}

// TestDailyLimit_QueueFullDropsAndRearms 是审计阻断问题 #4 的回归测试：
// 队列满时必须立即返回（不阻塞提交方，它可能是 relay 结算 goroutine），
// 并清除 tripped 交由下一轮重试。
func TestDailyLimit_QueueFullDropsAndRearms(t *testing.T) {
	resetDailyLimitState(t)
	channelId := 900011
	statDate := todayStatDate()

	// 填满队列。
	for i := 0; i < dailyLimitDisableQueueSize; i++ {
		dailyLimit.disableQueue <- disableRequest{channelId: channelId + i + 1, statDate: statDate}
	}

	shard := shardFor(channelId)
	shard.mu.Lock()
	shard.tripped[dailyKey{statDate: statDate, channelId: channelId}] = struct{}{}
	shard.mu.Unlock()

	done := make(chan struct{})
	go func() {
		submitDisable(disableRequest{channelId: channelId, statDate: statDate})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("submitDisable blocked on a full queue; it must never block the caller")
	}
	assert.False(t, isTripped(channelId, statDate), "dropped request must clear tripped so the next flush retries")
}

// TestDailyLimit_ConcurrentAccumulationIsExact 验证并发累计不丢增量。
func TestDailyLimit_ConcurrentAccumulationIsExact(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	channelId := 900012
	setDailyLimitConfigs(map[int]channelLimitConfig{
		// 上限设得足够高，避免触发禁用干扰计数。
		channelId: {limit: 1 << 40},
	})

	const goroutines = 100
	const perGoroutine = 20
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				accumulate(channelId, 1)
			}
		}()
	}
	wg.Wait()

	assert.EqualValues(t, goroutines*perGoroutine, pendingUsed(channelId, todayStatDate()))
}

// TestDailyLimit_PendingSurvivesFlushFailure 验证落库失败时增量被放回内存而不是丢弃。
// 丢弃会造成金额永久少计，是本功能最不该出现的错误。
func TestDailyLimit_PendingSurvivesFlushFailure(t *testing.T) {
	resetDailyLimitState(t)
	statDate := todayStatDate()
	channelId := 900013

	restoreDayPending([]model.ChannelDailyDelta{
		{StatDate: statDate, ChannelId: channelId, CostQuota: 42},
	})
	assert.EqualValues(t, 42, pendingUsed(channelId, statDate))

	// 二次失败继续累加，不覆盖。
	restoreDayPending([]model.ChannelDailyDelta{
		{StatDate: statDate, ChannelId: channelId, CostQuota: 8},
	})
	assert.EqualValues(t, 50, pendingUsed(channelId, statDate))

	// 轮次增量放回轮次键，不能混进今日键。
	restorePeriodPending([]model.ChannelPeriodDelta{
		{ChannelId: channelId, PeriodStart: 5_000, CostQuota: 9},
	})
	assert.EqualValues(t, 9, pendingOf(periodKeyOf(channelId, 5_000)))
	assert.EqualValues(t, 50, pendingUsed(channelId, statDate))
}

// TestDailyLimit_RestorePendingOnlyUnpersisted 是「部分落库失败导致重复计数」的回归测试。
//
// UpsertChannelDailyUsage 逐条写入且没有事务包裹，中途失败时前面的条目已经落库。
// 若整批回退，已落库的部分会在下一轮被再加一次，金额越算越多。
func TestDailyLimit_RestorePendingOnlyUnpersisted(t *testing.T) {
	resetDailyLimitState(t)
	statDate := todayStatDate()
	chA, chB := 900021, 900022

	deltas := []model.ChannelDailyDelta{
		{StatDate: statDate, ChannelId: chA, CostQuota: 10},
		{StatDate: statDate, ChannelId: chB, CostQuota: 20},
	}
	// 模拟「第 1 条已落库、第 2 条失败」：只回退 deltas[1:]。
	processed := 1
	restoreDayPending(deltas[processed:])

	assert.Zero(t, pendingUsed(chA, statDate),
		"an already-persisted delta must not be restored, otherwise it is counted twice")
	assert.EqualValues(t, 20, pendingUsed(chB, statDate),
		"the unpersisted delta must be kept for the next retry")
}

// TestDailyLimit_FlushBackoff 验证退避序列有上界。
func TestDailyLimit_FlushBackoff(t *testing.T) {
	base := 5 * time.Second
	got := nextFlushBackoff(0, base)
	assert.Equal(t, 10*time.Second, got)
	for i := 0; i < 20; i++ {
		got = nextFlushBackoff(got, base)
	}
	assert.Equal(t, dailyLimitFlushMaxBackoff, got, "backoff must be capped")
}

// TestDailyLimit_PruneOldEntries 验证跨日后旧条目被回收，内存有界。
func TestDailyLimit_PruneOldEntries(t *testing.T) {
	resetDailyLimitState(t)
	channelId := 900014
	today := todayStatDate()
	yesterday := today - 86400

	shard := shardFor(channelId)
	shard.mu.Lock()
	shard.total[dailyKey{statDate: yesterday, channelId: channelId}] = dailyTotal{cost: 1}
	shard.total[dailyKey{statDate: today, channelId: channelId}] = dailyTotal{cost: 2}
	shard.tripped[dailyKey{statDate: yesterday, channelId: channelId}] = struct{}{}
	shard.tripped[dailyKey{statDate: today, channelId: channelId}] = struct{}{}
	shard.mu.Unlock()

	pruneOldEntries(today)

	shard.mu.Lock()
	defer shard.mu.Unlock()
	_, oldTotal := shard.total[dailyKey{statDate: yesterday, channelId: channelId}]
	_, newTotal := shard.total[dailyKey{statDate: today, channelId: channelId}]
	assert.False(t, oldTotal, "yesterday's total must be pruned")
	assert.True(t, newTotal, "today's total must be kept")
	_, oldTrip := shard.tripped[dailyKey{statDate: yesterday, channelId: channelId}]
	assert.False(t, oldTrip)
}

// TestDailyLimit_CrossMidnightKeepsDatesSeparate 是审计阻断问题 #3（多节点日切）的回归测试：
// 累计键自带日期，跨零点的增量必须各自落到自己的日期，不串日。
func TestDailyLimit_CrossMidnightKeepsDatesSeparate(t *testing.T) {
	resetDailyLimitState(t)
	channelId := 900015
	today := todayStatDate()
	yesterday := today - 86400

	shard := shardFor(channelId)
	shard.mu.Lock()
	addPendingLocked(shard, dailyKey{statDate: yesterday, channelId: channelId}, 30)
	addPendingLocked(shard, dailyKey{statDate: today, channelId: channelId}, 5)
	shard.mu.Unlock()

	deltas, _, keys := drainPending()

	byDate := make(map[int64]int64)
	for _, d := range deltas {
		if d.ChannelId == channelId {
			byDate[d.StatDate] += d.CostQuota
		}
	}
	assert.EqualValues(t, 30, byDate[yesterday], "yesterday's leftover must be persisted under yesterday")
	assert.EqualValues(t, 5, byDate[today])
	assert.NotEmpty(t, keys)
}

func TestDailyLimit_DedupKeys(t *testing.T) {
	k1 := dailyKey{statDate: 1, channelId: 1}
	k2 := dailyKey{statDate: 1, channelId: 2}
	assert.Len(t, dedupKeys([]dailyKey{k1, k1, k2, k2, k1}), 2)
	assert.Len(t, dedupKeys([]dailyKey{k1}), 1)
	assert.Empty(t, dedupKeys(nil))
}

// TestDailyLimit_StatDateCacheFollowsTimezone 验证日期边界缓存在时区变更时失效。
func TestDailyLimit_StatDateCacheFollowsTimezone(t *testing.T) {
	resetDailyLimitState(t)
	shanghai := &operation_setting.ChannelDailyLimitSetting{Timezone: "Asia/Shanghai"}
	utc := &operation_setting.ChannelDailyLimitSetting{Timezone: "UTC"}

	sh := currentStatDate(shanghai)
	ut := currentStatDate(utc)
	// 两个时区的当日 00:00 不可能相同（UTC+8 与 UTC 相差 8 小时）。
	assert.NotEqual(t, sh, ut, "timezone change must invalidate the cached day boundary")
	// 再切回来必须得到原值，证明缓存按时区键正确失效而不是粘住。
	assert.Equal(t, sh, currentStatDate(shanghai))
}

// TestDailyLimit_TrackEnabledTransition 验证只有真正的「关→开」切换才记录生效时刻。
func TestDailyLimit_TrackEnabledTransition(t *testing.T) {
	resetDailyLimitState(t)

	// 先关后开：这是真正的切换，必须记录时刻。
	trackEnabledTransition(false)
	assert.Zero(t, dailyLimit.enabledAt.Load())
	trackEnabledTransition(true)
	first := dailyLimit.enabledAt.Load()
	assert.Positive(t, first, "an off->on switch must record the moment")

	// 持续开启不刷新时刻。
	trackEnabledTransition(true)
	assert.Equal(t, first, dailyLimit.enabledAt.Load())
}

// TestDailyLimit_FreshStartIsNotASwitchOn 是「重启后旧禁用永不恢复」的回归测试。
//
// lastEnabled 若用零值 bool 表示「上一次是关闭」，进程启动时的第一次观察就会被当成一次
// 关→开切换，enabledAt 被设成进程启动时间；恢复扫描随即跳过所有更早被禁用的渠道，
// 于是任何一次重启都会让当天之前被限额禁用的渠道永远不再自动恢复。
func TestDailyLimit_FreshStartIsNotASwitchOn(t *testing.T) {
	resetDailyLimitState(t)
	require.Equal(t, dailyLimitSwitchUnknown, dailyLimit.lastEnabled.Load(),
		"a fresh process must start in the unknown state")

	// 进程启动后第一次观察到「开启」——这不是切换。
	trackEnabledTransition(true)
	assert.Zero(t, dailyLimit.enabledAt.Load(),
		"process start must not be treated as an off->on switch; otherwise recovery skips everything disabled before the restart")
}

// flusher 在 sleep **之前**取配置快照，醒来后用它判断开关状态（见 runChannelDailyLimitFlusher）。
// 于是它对总开关的观察整整滞后一个周期：管理员在 sleep 期间关掉开关，这一轮醒来仍按「开」处理，
// 下一轮才看到「关」。对「关→开」而言，关闭窗口短于一个周期时这次切换根本不会被观察到，
// enabledAt 保持 0；恢复任务（channel_daily_limit_task.go:67/101）随后以 enabledAfter=0 运行，
// 把关闭期间到期的渠道一次性全部放行——而 enabledAt 正是为了避免这件事才存在的。
//
// 本用例复现 flusher 的真实取值顺序：快照在前、使用在后，期间改动实时配置。
func TestDailyLimit_FlusherObservesSwitchAfterSleep(t *testing.T) {
	resetDailyLimitState(t)
	original := operation_setting.GetChannelDailyLimitSetting()
	t.Cleanup(func() { operation_setting.ReplaceChannelDailyLimitSetting(original) })

	setEnabled := func(enabled bool) {
		next := original
		next.Enabled = enabled
		operation_setting.ReplaceChannelDailyLimitSetting(next)
	}

	// 第一轮：开关开着，flusher 醒来后观察到「开」。
	setEnabled(true)
	first := observeDailyLimitSwitch()
	require.True(t, first.Enabled)
	require.Equal(t, dailyLimitSwitchOn, dailyLimit.lastEnabled.Load())

	// sleep 期间管理员关掉了开关：flusher 这一轮必须看到「关」并跳过刷写，
	// 而不是拿 sleep 之前的快照继续按「开」跑一轮。
	setEnabled(false)
	second := observeDailyLimitSwitch()
	assert.False(t, second.Enabled,
		"the flusher must read the switch after sleeping; a snapshot taken a full interval earlier makes it act on stale state")
	assert.Equal(t, dailyLimitSwitchOff, dailyLimit.lastEnabled.Load())

	// 关闭窗口短于一个刷写周期时同样要被观察到：重新打开必须登记成一次 off→on 切换，
	// 否则 enabledAt 保持 0，恢复任务会以 enabledAfter=0 运行，把关闭期间到期的渠道一次性全放行。
	dailyLimit.enabledAt.Store(0)
	setEnabled(true)
	third := observeDailyLimitSwitch()
	assert.True(t, third.Enabled)
	assert.Equal(t, dailyLimitSwitchOn, dailyLimit.lastEnabled.Load())
	assert.NotZero(t, dailyLimit.enabledAt.Load(),
		"off→on must stamp enabledAt so the recovery scan only releases channels disabled before the switch came back")
}

func TestDailyLimit_ReasonDescribesMode(t *testing.T) {
	daily := formatDailyLimitReason(disableRequest{used: 10, limit: 5})
	timed := formatDailyLimitReason(disableRequest{period: true, used: 10, limit: 5, recoverMinutes: 45})
	assert.Contains(t, daily, "今日上游消耗")
	assert.Contains(t, timed, "本轮上游消耗")
	assert.Contains(t, timed, "45 分钟后自动恢复")
	// 金额后面不再缀「额度」二字，也不再固定 6 位小数。
	assert.NotContains(t, daily, "额度")
	assert.NotContains(t, timed, "额度")
	assert.NotContains(t, timed, "000 ")
}

func TestTrimAmountZeros(t *testing.T) {
	cases := map[string]string{
		"＄1.000000":  "＄1.00",
		"＄1.004800":  "＄1.0048",
		"＄12.345678": "＄12.345678",
		"＄0.000020":  "＄0.00002",
		"¥7.250000":  "¥7.25",
		"¤3.100000":  "¤3.10",
		"1000":       "1000",
	}
	for input, want := range cases {
		assert.Equal(t, want, trimAmountZeros(input), input)
	}
}

// TestDailyLimit_DisableWorkerSkipsNonEnabledChannel 验证条件更新未命中时清除 tripped，
// 交由下一轮重判，而不是永久卡住。
func TestDailyLimit_DisableWorkerSkipsNonEnabledChannel(t *testing.T) {
	resetDailyLimitState(t)
	statDate := todayStatDate()

	ch := &model.Channel{
		Id:              9_900_101,
		Type:            1,
		Key:             fmt.Sprintf("dlk-%d", time.Now().UnixNano()),
		Name:            "daily-limit-disable-worker",
		Status:          common.ChannelStatusManuallyDisabled,
		Models:          "gpt-4o",
		Group:           "default",
		DailyQuotaLimit: 100,
	}
	require.NoError(t, model.DB.Create(ch).Error)
	t.Cleanup(func() { model.DB.Unscoped().Delete(&model.Channel{}, ch.Id) })

	// worker 现在会先复核请求是否仍然成立（开关 / 日期 / 当前上限 / 当前用量），
	// 所以这些前置条件必须齐备，否则请求会在到达「渠道非启用态」这条分支之前就被丢弃，
	// 本用例也就测不到它要测的东西。
	enableDailyLimitSetting(t, true)
	setDailyLimitConfigs(map[int]channelLimitConfig{
		ch.Id: {limit: 100},
	})
	shard := shardFor(ch.Id)
	shard.mu.Lock()
	shard.total[dailyKey{statDate: statDate, channelId: ch.Id}] = dailyTotal{cost: 100}
	shard.tripped[dailyKey{statDate: statDate, channelId: ch.Id}] = struct{}{}
	shard.mu.Unlock()

	handleDisableRequest(disableRequest{channelId: ch.Id, statDate: statDate, limit: 100, used: 100})

	var got model.Channel
	require.NoError(t, model.DB.First(&got, ch.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, got.Status, "manual disable must not be overwritten")
	assert.Zero(t, got.DailyLimitDisabledAt)
	assert.False(t, isTripped(ch.Id, statDate), "a missed conditional update must clear tripped")
	// 清标记的同时必须记下放行水位，否则每个 flush 周期都会空转重试一次，
	// 而且之后管理员手动启用该渠道时会被立刻撤销。见 releaseTripped。
	assert.True(t, hasReleaseMark(ch.Id, statDate),
		"skipping a non-enabled channel must record a release mark, not just clear the flag")
}

// TestDailyLimit_AlreadyDisabledChannelDoesNotSpin 覆盖「禁用请求发现渠道已不是启用态」
// 这条分支：不得每个 flush 周期空转重试一次，之后的手动启用也不得被立刻撤销。
func TestDailyLimit_AlreadyDisabledChannelDoesNotSpin(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	statDate := todayStatDate()

	// 真实的 DB 行：handleDisableRequest 会走条件更新，需要一条已被管理员禁用的渠道。
	ch := &model.Channel{
		Id:              9_900_102,
		Type:            1,
		Key:             fmt.Sprintf("dlk-%d", time.Now().UnixNano()),
		Name:            "daily-limit-already-disabled",
		Status:          common.ChannelStatusManuallyDisabled,
		Models:          "gpt-4o",
		Group:           "default",
		DailyQuotaLimit: 100,
	}
	require.NoError(t, model.DB.Create(ch).Error)
	t.Cleanup(func() { model.DB.Unscoped().Delete(&model.Channel{}, ch.Id) })

	channelId := ch.Id
	key := dailyKey{statDate: statDate, channelId: channelId}
	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 100},
	})

	accumulate(channelId, 100)
	require.True(t, isTripped(channelId, statDate))
	drainDisableQueue()
	drainPending()
	setDailyTotal(channelId, statDate, dailyTotal{cost: 100})

	// 走真实的 worker 分支：渠道已被管理员手动禁用，条件更新命中 0 行。
	handleDisableRequest(disableRequest{
		channelId: channelId, statDate: statDate,
		limit: 100, used: 100,
	})
	require.False(t, isTripped(channelId, statDate))

	// 后续复判不得再提交禁用请求——渠道本来就是禁用的，重试没有意义。
	for i := 0; i < 3; i++ {
		recheckAfterFlush([]dailyKey{key}, key.statDate)
	}
	assert.Empty(t, dailyLimit.disableQueue, "must not re-submit a disable for an already-disabled channel")

	// 管理员随后手动启用：同样要求有新消费才重新禁用。
	rearmTripped(channelId)
	recheckAfterFlush([]dailyKey{key}, key.statDate)
	assert.False(t, isTripped(channelId, statDate), "manual enable must survive the next flush cycle")

	accumulate(channelId, 1)
	assert.True(t, isTripped(channelId, statDate), "further consumption must disable it again")
}

// 排队中的禁用请求必须在执行前重新确认，否则「关掉开关 / 取消上限 / 跨日」之后
// 队列里的旧请求仍会把渠道禁掉。

func seedTrippedRequest(t *testing.T, channelId int, statDate int64, limit int64) disableRequest {
	t.Helper()
	shard := shardFor(channelId)
	shard.mu.Lock()
	shard.total[dailyKey{statDate: statDate, channelId: channelId}] = dailyTotal{cost: limit}
	shard.tripped[dailyKey{statDate: statDate, channelId: channelId}] = struct{}{}
	shard.mu.Unlock()
	return disableRequest{channelId: channelId, statDate: statDate, limit: limit,
		used: limit}
}

func TestDailyLimit_QueuedDisableDroppedWhenSwitchOff(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	channelId, statDate := 900060, todayStatDate()
	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 100},
	})
	req := seedTrippedRequest(t, channelId, statDate, 100)
	require.True(t, disableRequestStillValid(req), "precondition: valid while enabled")

	enableDailyLimitSetting(t, false)
	assert.False(t, disableRequestStillValid(req),
		"a queued request must not land after the master switch is turned off")
}

func TestDailyLimit_QueuedDisableDroppedWhenLimitCleared(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	channelId, statDate := 900061, todayStatDate()
	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 100},
	})
	req := seedTrippedRequest(t, channelId, statDate, 100)
	require.True(t, disableRequestStillValid(req))

	// 管理员清掉了上限。
	setDailyLimitConfigs(map[int]channelLimitConfig{})
	assert.False(t, disableRequestStillValid(req),
		"a queued request must not land after the channel's limit is cleared")
}

func TestDailyLimit_QueuedDisableDroppedWhenLimitRaised(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	channelId, statDate := 900062, todayStatDate()
	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 100},
	})
	req := seedTrippedRequest(t, channelId, statDate, 100)

	// 上限被调高到当日用量之上。
	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 100_000},
	})
	assert.False(t, disableRequestStillValid(req),
		"a queued request must be re-judged against the CURRENT limit, not the one it was queued with")
}

func TestDailyLimit_QueuedDisableDroppedAfterDayRollover(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	channelId := 900063
	yesterday := todayStatDate() - 86400
	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 100},
	})
	req := seedTrippedRequest(t, channelId, yesterday, 100)
	assert.False(t, disableRequestStillValid(req),
		"a request queued for a previous day must not disable the channel today")
}

func TestDailyLimit_QueuedDisableStillValidWhenNothingChanged(t *testing.T) {
	resetDailyLimitState(t)
	enableDailyLimitSetting(t, true)
	channelId, statDate := 900064, todayStatDate()
	setDailyLimitConfigs(map[int]channelLimitConfig{
		channelId: {limit: 100},
	})
	req := seedTrippedRequest(t, channelId, statDate, 100)
	assert.True(t, disableRequestStillValid(req),
		"the normal path must still go through")
}
