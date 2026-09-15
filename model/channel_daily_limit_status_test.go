package model

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 每日上限状态机的测试。设计见 docs/design/channel-daily-quota-limit.md §6.2 / §6.3。
//
// 核心不变式：daily_limit_disabled_at > 0  <=>  最近一次禁用来自每日上限。
// 下面每个用例都是围绕这个不变式的并发/时序保护。

func reloadChannel(t *testing.T, id int) *Channel {
	t.Helper()
	ch, err := GetChannelById(id, false)
	require.NoError(t, err)
	return ch
}

func TestDisableChannelForDailyLimit_OnlyFromEnabled(t *testing.T) {
	requireDB(t)
	statDate := int64(1_700_000_000)

	// 启用态：应当禁用成功，并在同一次更新里写入状态与两个标记列。
	ch := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 100 })
	ok, err := DisableChannelForDailyLimit(ch.Id, statDate, "limit reached")
	require.NoError(t, err)
	assert.True(t, ok)

	got := reloadChannel(t, ch.Id)
	assert.Equal(t, common.ChannelStatusAutoDisabled, got.Status)
	assert.Positive(t, got.DailyLimitDisabledAt)
	assert.EqualValues(t, statDate, got.DailyLimitDisabledDate)
	info := got.GetOtherInfo()
	assert.Equal(t, "limit reached", info["status_reason"])
	assert.NotNil(t, info["status_time"])

	// 已是禁用态：条件不满足，不得重复禁用。
	ok, err = DisableChannelForDailyLimit(ch.Id, statDate, "again")
	require.NoError(t, err)
	assert.False(t, ok, "must not win the race twice")
}

func TestDisableChannelForDailyLimit_SkipsNonEnabledStatuses(t *testing.T) {
	requireDB(t)
	statDate := int64(1_700_000_000)

	// 管理员手动禁用的渠道不得被本功能改写状态或打标记。
	manual := mkChannel(t, func(c *Channel) {
		c.Status = common.ChannelStatusManuallyDisabled
		c.DailyQuotaLimit = 100
	})
	ok, err := DisableChannelForDailyLimit(manual.Id, statDate, "limit")
	require.NoError(t, err)
	assert.False(t, ok)
	got := reloadChannel(t, manual.Id)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, got.Status, "manual disable must not be overwritten")
	assert.Zero(t, got.DailyLimitDisabledAt)

	// 已因上游报错被禁用的渠道同理：不能被打上「限额禁用」标记，
	// 否则次日会把一个真坏的渠道自动恢复。
	broken := mkChannel(t, func(c *Channel) {
		c.Status = common.ChannelStatusAutoDisabled
		c.DailyQuotaLimit = 100
	})
	ok, err = DisableChannelForDailyLimit(broken.Id, statDate, "limit")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Zero(t, reloadChannel(t, broken.Id).DailyLimitDisabledAt)
}

// TestUpdateChannelStatus_ClearsMarksEvenWhenStatusUnchanged 是审计发现的缺陷的回归测试。
//
// 场景：渠道先因每日上限被禁用（status=3），随后一个在途请求报错，触发 DisableChannel
// 写入同样的 status=3。UpdateChannelStatus 内部有「目标状态与当前相同则直接返回」的
// 早退分支，若标记清除放在早退之后，标记就会残留，次日把坏渠道自动恢复。
func TestUpdateChannelStatus_ClearsMarksEvenWhenStatusUnchanged(t *testing.T) {
	requireDB(t)
	statDate := int64(1_700_000_000)
	ch := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 100 })

	ok, err := DisableChannelForDailyLimit(ch.Id, statDate, "limit reached")
	require.NoError(t, err)
	require.True(t, ok)
	require.Positive(t, reloadChannel(t, ch.Id).DailyLimitDisabledAt)

	// 同样的目标状态再来一次（模拟上游报错禁用）。
	UpdateChannelStatus(ch.Id, "", common.ChannelStatusAutoDisabled, "upstream error")

	got := reloadChannel(t, ch.Id)
	assert.Equal(t, common.ChannelStatusAutoDisabled, got.Status)
	assert.Zero(t, got.DailyLimitDisabledAt, "marks must be cleared even though the status did not change")
	assert.Zero(t, got.DailyLimitDisabledDate)
}

func TestUpdateChannelStatus_ClearsMarksOnManualEnable(t *testing.T) {
	requireDB(t)
	statDate := int64(1_700_000_000)
	ch := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 100 })

	ok, err := DisableChannelForDailyLimit(ch.Id, statDate, "limit reached")
	require.NoError(t, err)
	require.True(t, ok)

	// 管理员手动启用（controller 走的就是这个函数）。
	UpdateChannelStatus(ch.Id, "", common.ChannelStatusEnabled, "manual operation")

	got := reloadChannel(t, ch.Id)
	assert.Equal(t, common.ChannelStatusEnabled, got.Status)
	assert.Zero(t, got.DailyLimitDisabledAt)
}

func TestClearDailyLimitMarks_IsNoopWhenAbsent(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, nil)
	// 无标记时是零行更新，且不得影响其它列。
	clearDailyLimitMarksIfPresent(ch.Id)
	got := reloadChannel(t, ch.Id)
	assert.Equal(t, common.ChannelStatusEnabled, got.Status)
	assert.Zero(t, got.DailyLimitDisabledAt)
	// 非法 id 安全返回。
	assert.NotPanics(t, func() { clearDailyLimitMarksIfPresent(0) })
}

func TestEnableDisableChannelByTag_ClearsMarks(t *testing.T) {
	requireDB(t)
	statDate := int64(1_700_000_000)
	tag := uniq("dltag")
	ch := mkChannel(t, func(c *Channel) {
		c.Tag = &tag
		c.DailyQuotaLimit = 100
	})

	ok, err := DisableChannelForDailyLimit(ch.Id, statDate, "limit reached")
	require.NoError(t, err)
	require.True(t, ok)

	require.NoError(t, EnableChannelByTag(tag))
	got := reloadChannel(t, ch.Id)
	assert.Equal(t, common.ChannelStatusEnabled, got.Status)
	assert.Zero(t, got.DailyLimitDisabledAt, "tag-level enable must clear the marks in the same UPDATE")

	// 再禁用一次，验证 tag 级禁用同样清标记。
	ok, err = DisableChannelForDailyLimit(ch.Id, statDate, "limit reached")
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, DisableChannelByTag(tag))
	got = reloadChannel(t, ch.Id)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, got.Status)
	assert.Zero(t, got.DailyLimitDisabledAt)
}

func TestRecoverChannelFromDailyLimit_Conditions(t *testing.T) {
	requireDB(t)
	yesterday := int64(1_700_000_000)
	today := yesterday + 86400

	// 正常恢复：昨日被限额禁用。
	ch := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 100 })
	ok, err := DisableChannelForDailyLimit(ch.Id, yesterday, "limit")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = RecoverChannelFromDailyLimit(ch.Id, today)
	require.NoError(t, err)
	assert.True(t, ok)
	got := reloadChannel(t, ch.Id)
	assert.Equal(t, common.ChannelStatusEnabled, got.Status)
	assert.Zero(t, got.DailyLimitDisabledAt)
	assert.Zero(t, got.DailyLimitDisabledDate)

	// 幂等：再调一次不会重复恢复。
	ok, err = RecoverChannelFromDailyLimit(ch.Id, today)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestRecoverChannelFromDailyLimit_SameDayNotRecovered(t *testing.T) {
	requireDB(t)
	today := int64(1_700_086_400)
	ch := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 100 })
	ok, err := DisableChannelForDailyLimit(ch.Id, today, "limit")
	require.NoError(t, err)
	require.True(t, ok)

	// 当天禁用的渠道当天不得恢复。
	ok, err = RecoverChannelFromDailyLimit(ch.Id, today)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, common.ChannelStatusAutoDisabled, reloadChannel(t, ch.Id).Status)
}

func TestRecoverChannelFromDailyLimit_SkipsForeignDisables(t *testing.T) {
	requireDB(t)
	today := int64(1_700_172_800)

	// 没有限额标记的自动禁用渠道（上游报错禁用）不得被恢复。
	ch := mkChannel(t, func(c *Channel) {
		c.Status = common.ChannelStatusAutoDisabled
		c.DailyQuotaLimit = 100
	})
	ok, err := RecoverChannelFromDailyLimit(ch.Id, today)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, common.ChannelStatusAutoDisabled, reloadChannel(t, ch.Id).Status)
}

func TestListDailyLimitRecoveryCandidates(t *testing.T) {
	requireDB(t)
	yesterday := int64(1_700_259_200)
	today := yesterday + 86400
	off := 0

	auto := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 100 })
	manual := mkChannel(t, func(c *Channel) {
		c.DailyQuotaLimit = 100
		c.DailyLimitAutoRecover = &off
	})
	sameDay := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 100 })

	for _, ch := range []*Channel{auto, manual} {
		ok, err := DisableChannelForDailyLimit(ch.Id, yesterday, "limit")
		require.NoError(t, err)
		require.True(t, ok)
	}
	ok, err := DisableChannelForDailyLimit(sameDay.Id, today, "limit")
	require.NoError(t, err)
	require.True(t, ok)

	ids, err := ListDailyLimitRecoveryCandidates(today, 0)
	require.NoError(t, err)
	idSet := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}
	assert.Contains(t, idSet, auto.Id)
	assert.NotContains(t, idSet, manual.Id, "auto_recover=0 must be filtered in SQL, not fetched and logged every tick")
	assert.NotContains(t, idSet, sameDay.Id, "same-day disables are not recovery candidates")

	// enabledAfter 过滤：总开关重新打开之后不补做更早的禁用。
	future := reloadChannel(t, auto.Id).DailyLimitDisabledAt + 1
	ids, err = ListDailyLimitRecoveryCandidates(today, future)
	require.NoError(t, err)
	for _, id := range ids {
		assert.NotEqual(t, auto.Id, id, "disables predating the switch flip must not be recovered")
	}
}

// TestDailyLimitStatus_ConcurrentDisableAndEnable 验证并发下状态与标记始终自洽：
// 不允许出现「已启用但仍带限额标记」这种组合。
func TestDailyLimitStatus_ConcurrentDisableAndEnable(t *testing.T) {
	requireDB(t)
	statDate := int64(1_700_345_600)
	ch := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 100 })

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_, _ = DisableChannelForDailyLimit(ch.Id, statDate, "limit")
			} else {
				UpdateChannelStatus(ch.Id, "", common.ChannelStatusEnabled, "manual operation")
			}
		}(i)
	}
	wg.Wait()

	got := reloadChannel(t, ch.Id)
	if got.Status == common.ChannelStatusEnabled {
		assert.Zero(t, got.DailyLimitDisabledAt, "enabled channel must never carry a daily-limit mark")
	} else {
		assert.Equal(t, common.ChannelStatusAutoDisabled, got.Status)
	}
}

// TestUpdateChannelStatus_ClearsMarksInSameSave 验证正常的状态变更把标记清零并进同一次
// 保存，而不是靠一条额外的 UPDATE：中间态（状态已变但标记还在）不允许落库。
func TestUpdateChannelStatus_ClearsMarksInSameSave(t *testing.T) {
	requireDB(t)
	statDate := int64(1_700_432_000)
	ch := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 100 })

	ok, err := DisableChannelForDailyLimit(ch.Id, statDate, "limit reached")
	require.NoError(t, err)
	require.True(t, ok)

	// 手动禁用：状态确实变化，走 SaveWithoutKey 分支。
	UpdateChannelStatus(ch.Id, "", common.ChannelStatusManuallyDisabled, "manual operation")

	got := reloadChannel(t, ch.Id)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, got.Status)
	assert.Zero(t, got.DailyLimitDisabledAt)
	assert.Zero(t, got.DailyLimitDisabledDate)
}

// TestClearDailyLimitMarksIfPresent_LeavesCleanChannelsAlone 验证无标记时不改动其它列。
func TestClearDailyLimitMarksIfPresent_LeavesCleanChannelsAlone(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, nil)
	before := reloadChannel(t, ch.Id)

	clearDailyLimitMarksIfPresent(ch.Id)

	got := reloadChannel(t, ch.Id)
	assert.Zero(t, got.DailyLimitDisabledAt)
	assert.Equal(t, before.Status, got.Status, "a clean channel must be left untouched")

	// 带标记时要清掉。
	statDate := int64(1_700_518_400)
	ok, err := DisableChannelForDailyLimit(ch.Id, statDate, "limit")
	require.NoError(t, err)
	require.True(t, ok)
	clearDailyLimitMarksIfPresent(ch.Id)
	assert.Zero(t, reloadChannel(t, ch.Id).DailyLimitDisabledAt)
}

// TestClearDailyLimitMarks_NotFooledByStaleCache 锁住一个曾经真实存在过的回归。
//
// 曾把 clearDailyLimitMarksIfPresent 优化成「先读内存缓存，缓存里没标记就跳过写入」。
// 那是错的：CacheUpdateChannelStatus 只同步 Status，不同步两个标记列；另一节点写入标记
// 后本节点缓存要等全量同步才可见。于是「缓存说没标记、DB 里其实有」这个危险方向会让清除
// 被跳过，标记残留到次日、把一个真坏的渠道恢复成启用。
//
// 这里构造的正是那个状态：DB 有标记，缓存里的同一对象标记为 0。
func TestClearDailyLimitMarks_NotFooledByStaleCache(t *testing.T) {
	requireDB(t)
	statDate := int64(1_700_604_800)
	ch := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 100 })

	ok, err := DisableChannelForDailyLimit(ch.Id, statDate, "limit")
	require.NoError(t, err)
	require.True(t, ok)
	require.Positive(t, reloadChannel(t, ch.Id).DailyLimitDisabledAt)

	// 让缓存持有一个「没有标记」的陈旧副本。
	origCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = origCache })

	stale := *ch
	stale.Status = common.ChannelStatusAutoDisabled
	stale.DailyLimitDisabledAt = 0
	stale.DailyLimitDisabledDate = 0
	channelSyncLock.Lock()
	// 测试进程没跑过 InitChannelCache，map 可能是 nil。
	mapWasNil := channelsIDM == nil
	if mapWasNil {
		channelsIDM = make(map[int]*Channel)
	}
	prev, had := channelsIDM[ch.Id]
	channelsIDM[ch.Id] = &stale
	channelSyncLock.Unlock()
	t.Cleanup(func() {
		channelSyncLock.Lock()
		if mapWasNil {
			channelsIDM = nil
		} else if had {
			channelsIDM[ch.Id] = prev
		} else {
			delete(channelsIDM, ch.Id)
		}
		channelSyncLock.Unlock()
	})

	clearDailyLimitMarksIfPresent(ch.Id)

	assert.Zero(t, reloadChannel(t, ch.Id).DailyLimitDisabledAt,
		"a stale cache entry must not cause the mark to survive in the database")
}
