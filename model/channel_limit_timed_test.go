package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 限时恢复模式（达到上限后 N 分钟恢复并开启新一轮）的状态转换、编辑语义与轮次用量表。
// 设计见 docs/design/channel-limit-upstream-basis-and-timed-recovery.md §3、§5。

func mkTimedChannel(t *testing.T, minutes int, periodStart int64, mut func(*Channel)) *Channel {
	t.Helper()
	return mkChannel(t, func(c *Channel) {
		c.DailyQuotaLimit = 1_000_000
		c.DailyLimitRecoverMinutes = minutes
		c.DailyLimitPeriodStart = periodStart
		if mut != nil {
			mut(c)
		}
	})
}

// 旧轮次的禁用请求必须落空：渠道刚被恢复进入新一轮时，快照还停在上一轮的节点会立刻
// 用上一轮已满的用量再提交一次禁用。
func TestDisableChannelForTimedLimit_RequiresCurrentPeriod(t *testing.T) {
	requireDB(t)
	today := int64(1_700_600_000)
	ch := mkTimedChannel(t, 30, 1_000, nil)

	ok, err := DisableChannelForTimedLimit(ch.Id, 999, today, "stale round")
	require.NoError(t, err)
	assert.False(t, ok, "a request from a previous round must not disable the channel")
	assert.Equal(t, common.ChannelStatusEnabled, reloadChannel(t, ch.Id).Status)

	ok, err = DisableChannelForTimedLimit(ch.Id, 1_000, today, "round limit reached")
	require.NoError(t, err)
	assert.True(t, ok)
	got := reloadChannel(t, ch.Id)
	assert.Equal(t, common.ChannelStatusAutoDisabled, got.Status)
	assert.Positive(t, got.DailyLimitDisabledAt)
	assert.EqualValues(t, today, got.DailyLimitDisabledDate, "disabled_date feeds the 'limit reached' filter")
	assert.Equal(t, "round limit reached", got.GetOtherInfo()["status_reason"])
}

func TestDailyAndTimedDisablesDoNotCrossModes(t *testing.T) {
	requireDB(t)
	today := int64(1_700_600_000)

	timed := mkTimedChannel(t, 30, 1_000, nil)
	ok, err := DisableChannelForDailyLimit(timed.Id, today, "day request")
	require.NoError(t, err)
	assert.False(t, ok, "a day-mode request must not disable a timed-mode channel")

	daily := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 1_000_000 })
	ok, err = DisableChannelForTimedLimit(daily.Id, 0, today, "timed request")
	require.NoError(t, err)
	assert.False(t, ok, "a timed-mode request must not disable a day-mode channel")
}

func TestRecoverChannelFromTimedLimit_Conditions(t *testing.T) {
	requireDB(t)
	now := common.GetTimestamp()
	disabledAt := now - 1799 // 30 分钟差 1 秒
	ch := mkTimedChannel(t, 30, 1_000, func(c *Channel) {
		c.Status = common.ChannelStatusAutoDisabled
		c.DailyLimitDisabledAt = disabledAt
		c.DailyLimitDisabledDate = 1_700_600_000
	})

	ids, err := ListTimedLimitRecoveryCandidates(now, 0)
	require.NoError(t, err)
	assert.NotContains(t, ids, ch.Id, "one second short of the interval is not due yet")
	ok, err := RecoverChannelFromTimedLimit(ch.Id, now)
	require.NoError(t, err)
	assert.False(t, ok)

	due := disabledAt + 1800
	ids, err = ListTimedLimitRecoveryCandidates(due, 0)
	require.NoError(t, err)
	assert.Contains(t, ids, ch.Id)
	ids, err = ListTimedLimitRecoveryCandidates(due, disabledAt+1)
	require.NoError(t, err)
	assert.NotContains(t, ids, ch.Id, "disables before the switch was turned back on are not recovered")

	ok, err = RecoverChannelFromTimedLimit(ch.Id, due)
	require.NoError(t, err)
	assert.True(t, ok)
	got := reloadChannel(t, ch.Id)
	assert.Equal(t, common.ChannelStatusEnabled, got.Status)
	assert.Zero(t, got.DailyLimitDisabledAt)
	assert.Zero(t, got.DailyLimitDisabledDate)
	assert.EqualValues(t, due, got.DailyLimitPeriodStart, "recovery starts a new round at the recovery time")

	ok, err = RecoverChannelFromTimedLimit(ch.Id, due)
	require.NoError(t, err)
	assert.False(t, ok, "recovery is idempotent")
}

func TestTimedRecoverySkipsManualAndForeignDisables(t *testing.T) {
	requireDB(t)
	now := common.GetTimestamp()
	off := 0
	manual := mkTimedChannel(t, 30, 1_000, func(c *Channel) {
		c.Status = common.ChannelStatusAutoDisabled
		c.DailyLimitDisabledAt = now - 7200
		c.DailyLimitAutoRecover = &off
	})
	// 上游报错禁用：没有限额标记。
	foreign := mkTimedChannel(t, 30, 1_000, func(c *Channel) { c.Status = common.ChannelStatusAutoDisabled })

	ids, err := ListTimedLimitRecoveryCandidates(now, 0)
	require.NoError(t, err)
	assert.NotContains(t, ids, manual.Id, "auto recovery turned off")
	assert.NotContains(t, ids, foreign.Id, "not disabled by the limit")
}

func TestListDailyLimitRecoveryCandidates_ExcludesTimedChannels(t *testing.T) {
	requireDB(t)
	yesterday := int64(1_700_700_000)
	today := yesterday + 86400
	ch := mkTimedChannel(t, 30, 1_000, func(c *Channel) {
		c.Status = common.ChannelStatusAutoDisabled
		c.DailyLimitDisabledAt = yesterday + 10
		c.DailyLimitDisabledDate = yesterday
	})

	ids, err := ListDailyLimitRecoveryCandidates(today, 0)
	require.NoError(t, err)
	assert.NotContains(t, ids, ch.Id, "timed channels recover by interval, not at midnight")
	ok, err := RecoverChannelFromDailyLimit(ch.Id, today)
	require.NoError(t, err)
	assert.False(t, ok)
}

// 手动启用限时渠道开启新一轮：否则本轮用量仍压在上限之上，下一笔请求就会再次触发禁用。
func TestManualEnableStartsNewPeriodForTimedChannels(t *testing.T) {
	requireDB(t)
	timed := mkTimedChannel(t, 30, 1_000, func(c *Channel) { c.Status = common.ChannelStatusAutoDisabled })
	daily := mkChannel(t, func(c *Channel) {
		c.DailyQuotaLimit = 1_000_000
		c.Status = common.ChannelStatusAutoDisabled
	})

	before := common.GetTimestamp()
	UpdateChannelStatus(timed.Id, "", common.ChannelStatusEnabled, "manual operation")
	UpdateChannelStatus(daily.Id, "", common.ChannelStatusEnabled, "manual operation")

	assert.GreaterOrEqual(t, reloadChannel(t, timed.Id).DailyLimitPeriodStart, before)
	assert.Zero(t, reloadChannel(t, daily.Id).DailyLimitPeriodStart, "day-mode channels have no rounds")

	// 禁用不开新一轮。
	start := reloadChannel(t, timed.Id).DailyLimitPeriodStart
	UpdateChannelStatus(timed.Id, "", common.ChannelStatusManuallyDisabled, "manual operation")
	assert.EqualValues(t, start, reloadChannel(t, timed.Id).DailyLimitPeriodStart)
}

func TestEnableChannelByTag_StartsNewPeriodForTimedChannels(t *testing.T) {
	requireDB(t)
	tag := uniq("timedtag")
	timed := mkTimedChannel(t, 30, 1_000, func(c *Channel) {
		c.Tag = &tag
		c.Status = common.ChannelStatusAutoDisabled
	})
	daily := mkChannel(t, func(c *Channel) {
		c.Tag = &tag
		c.DailyQuotaLimit = 1_000_000
		c.Status = common.ChannelStatusAutoDisabled
	})

	running := mkTimedChannel(t, 30, 1_000, func(c *Channel) { c.Tag = &tag })

	before := common.GetTimestamp()
	require.NoError(t, EnableChannelByTag(tag))
	assert.GreaterOrEqual(t, reloadChannel(t, timed.Id).DailyLimitPeriodStart, before)
	assert.Zero(t, reloadChannel(t, daily.Id).DailyLimitPeriodStart)
	// 已经在跑的渠道保持当前一轮：否则按 Tag 点一次启用，本轮快跑满的渠道就凭空多出一整轮额度。
	assert.EqualValues(t, 1_000, reloadChannel(t, running.Id).DailyLimitPeriodStart,
		"an already-enabled timed channel must keep its current round")
}

// 只关闭自动恢复（不带恢复间隔）即切到「不自动恢复」：限时间隔一并归零，
// 不能留下「限时 + 不恢复」这种永远不会恢复的组合。
func TestDailyLimitEdit_TurningOffAutoRecoverLeavesTimedMode(t *testing.T) {
	requireDB(t)
	ch := mkTimedChannel(t, 30, 1_000, nil)
	off := 0
	_, err := UpdateChannelDailyLimitByIds([]int{ch.Id}, DailyLimitEdit{AutoRecover: &off})
	require.NoError(t, err)

	got := reloadChannel(t, ch.Id)
	assert.False(t, got.GetDailyLimitAutoRecover())
	assert.Zero(t, got.DailyLimitRecoverMinutes, "no auto recovery means no timed recovery either")

	// 同时带了间隔时以间隔为准（限时模式隐含自动恢复）。
	minutes := 15
	_, err = UpdateChannelDailyLimitByIds([]int{ch.Id}, DailyLimitEdit{AutoRecover: &off, RecoverMinutes: &minutes})
	require.NoError(t, err)
	got = reloadChannel(t, ch.Id)
	assert.Equal(t, 15, got.DailyLimitRecoverMinutes)
	assert.True(t, got.GetDailyLimitAutoRecover())
}

// Channel.Update() 不写限时恢复的两列：间隔只经 DailyLimitEdit 写入（要在同一条 UPDATE 里据旧值
// 判断是否开启新一轮），period_start 是服务端维护的只读列。
func TestChannelUpdateDoesNotWriteTimedColumns(t *testing.T) {
	requireDB(t)
	ch := mkTimedChannel(t, 30, 1_000, nil)
	patch := reloadChannel(t, ch.Id)
	patch.DailyLimitRecoverMinutes = 60
	patch.DailyLimitPeriodStart = 9_999
	require.NoError(t, patch.Update())

	got := reloadChannel(t, ch.Id)
	assert.Equal(t, 30, got.DailyLimitRecoverMinutes)
	assert.EqualValues(t, 1_000, got.DailyLimitPeriodStart)
}

// 恢复间隔变化开启新一轮，原样回传不重置。锁住 CASE 读到的是**旧**间隔——
// 在 MySQL 上这依赖 GORM 按列名排序生成 SET（period_start 排在 recover_minutes 之前）。
func TestDailyLimitEdit_RecoverMinutesChangeStartsNewPeriod(t *testing.T) {
	requireDB(t)
	ch := mkTimedChannel(t, 30, 1_000, nil)

	same := 30
	_, err := UpdateChannelDailyLimitByIds([]int{ch.Id}, DailyLimitEdit{RecoverMinutes: &same})
	require.NoError(t, err)
	assert.EqualValues(t, 1_000, reloadChannel(t, ch.Id).DailyLimitPeriodStart,
		"echoing the same interval must keep the current round")

	before := common.GetTimestamp()
	changed := 60
	_, err = UpdateChannelDailyLimitByIds([]int{ch.Id}, DailyLimitEdit{RecoverMinutes: &changed})
	require.NoError(t, err)
	got := reloadChannel(t, ch.Id)
	assert.Equal(t, 60, got.DailyLimitRecoverMinutes)
	assert.GreaterOrEqual(t, got.DailyLimitPeriodStart, before, "a changed interval starts a new round")

	// 从按日切到限时：隐含自动恢复，并开启第一轮。
	off := 0
	daily := mkChannel(t, func(c *Channel) {
		c.DailyQuotaLimit = 1_000_000
		c.DailyLimitAutoRecover = &off
	})
	_, err = UpdateChannelDailyLimitByIds([]int{daily.Id}, DailyLimitEdit{AutoRecover: &off, RecoverMinutes: &changed})
	require.NoError(t, err)
	got = reloadChannel(t, daily.Id)
	assert.True(t, got.GetDailyLimitAutoRecover(), "timed mode implies auto recovery")
	assert.GreaterOrEqual(t, got.DailyLimitPeriodStart, before)

	tooLong := MaxDailyLimitRecoverMinutes + 1
	_, err = UpdateChannelDailyLimitByIds([]int{ch.Id}, DailyLimitEdit{RecoverMinutes: &tooLong})
	require.Error(t, err)
	assert.Equal(t, 60, reloadChannel(t, ch.Id).DailyLimitRecoverMinutes, "an invalid edit writes nothing")
}

func TestChannelLimitPeriodUsage_UpsertGetAndCleanup(t *testing.T) {
	requireDB(t)
	ch := mkTimedChannel(t, 30, 5_000, nil)
	t.Cleanup(func() { _, _ = DeleteChannelLimitPeriodUsageByChannelIds([]int{ch.Id}) })

	processed, err := UpsertChannelLimitPeriodUsage([]ChannelPeriodDelta{
		{ChannelId: ch.Id, PeriodStart: 5_000, CostQuota: 30},
		{ChannelId: ch.Id, PeriodStart: 5_000, CostQuota: 12},
		{ChannelId: ch.Id, PeriodStart: 4_000, CostQuota: 7},
		{ChannelId: 0, PeriodStart: 5_000, CostQuota: 99}, // 非法渠道跳过
	})
	require.NoError(t, err)
	assert.Equal(t, 4, processed)

	current := ChannelPeriodKey{ChannelId: ch.Id, PeriodStart: 5_000}
	previous := ChannelPeriodKey{ChannelId: ch.Id, PeriodStart: 4_000}
	missing := ChannelPeriodKey{ChannelId: ch.Id, PeriodStart: 3_000}
	rows, err := GetChannelLimitPeriodUsages([]ChannelPeriodKey{current, previous, missing})
	require.NoError(t, err)
	assert.EqualValues(t, 42, rows[current], "deltas on the same round accumulate atomically")
	assert.EqualValues(t, 7, rows[previous])
	_, found := rows[missing]
	assert.False(t, found)

	// 保留期清理：早于 cutoff 的旧轮次删除，但渠道当前一轮即便早于 cutoff 也要保留——
	// 否则一个很久没触顶的限时渠道会被清零、放行一整轮额度。
	_, err = CleanupChannelLimitPeriodUsage(6_000)
	require.NoError(t, err)
	rows, err = GetChannelLimitPeriodUsages([]ChannelPeriodKey{current, previous})
	require.NoError(t, err)
	assert.EqualValues(t, 42, rows[current], "the current round must survive retention cleanup")
	_, found = rows[previous]
	assert.False(t, found, "an old round is cleaned up")

	empty, err := GetChannelLimitPeriodUsages(nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}
