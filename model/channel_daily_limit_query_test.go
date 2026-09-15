package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 限额筛选与今日用量排序的测试。设计见 docs/design/channel-daily-quota-limit.md §9.4 / §9.5。

func TestNormalizeChannelLimitFilter(t *testing.T) {
	// 等价类：合法值原样返回；空串/未知值/大小写混写一律回落到 all（不过滤）。
	assert.Equal(t, ChannelLimitFilterConfigured, NormalizeChannelLimitFilter("configured"))
	assert.Equal(t, ChannelLimitFilterUnlimited, NormalizeChannelLimitFilter("  UNLIMITED "))
	assert.Equal(t, ChannelLimitFilterReached, NormalizeChannelLimitFilter("Reached"))
	assert.Equal(t, ChannelLimitFilterNear, NormalizeChannelLimitFilter("near"))
	assert.Equal(t, ChannelLimitFilterAll, NormalizeChannelLimitFilter(""))
	assert.Equal(t, ChannelLimitFilterAll, NormalizeChannelLimitFilter("'; DROP TABLE channels;--"))
}

func TestChannelSortOptions_AcceptsUsageSortKeys(t *testing.T) {
	// 两个表达式排序键必须被白名单接受。
	opts := NewChannelSortOptions(ChannelSortByDailyUsed, "asc", false)
	assert.Equal(t, ChannelSortByDailyUsed, opts.SortBy)
	assert.Equal(t, "asc", opts.SortOrder)

	opts = NewChannelSortOptions(ChannelSortByDailyUsageRatio, "", false)
	assert.Equal(t, ChannelSortByDailyUsageRatio, opts.SortBy)
	assert.Equal(t, "desc", opts.SortOrder)

	// 非白名单值仍然被清空，防止把用户输入拼进 ORDER BY。
	opts = NewChannelSortOptions("id; DROP TABLE channels", "asc", false)
	assert.Equal(t, "", opts.SortBy)
}

// dailyFilterFixture 建一组覆盖各种状态的渠道，返回它们的 id。
type dailyFilterFixture struct {
	unlimited int // 无上限
	farBelow  int // 有上限，用量远低于阈值
	near      int // 有上限，用量 80%
	reached   int // 今日已达上限（被禁用）
	yesterday int // 昨日达上限且关闭自动恢复，今日仍禁用
	all       []int
}

func mkDailyFilterFixture(t *testing.T, statDate int64) dailyFilterFixture {
	t.Helper()
	off := 0
	f := dailyFilterFixture{}

	unlimited := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 0 })
	farBelow := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 1000 })
	near := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 1000 })
	reached := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 1000 })
	yesterday := mkChannel(t, func(c *Channel) {
		c.DailyQuotaLimit = 1000
		c.DailyLimitAutoRecover = &off
	})

	mkDailyUsageRow(t, statDate, farBelow.Id, 100) // 10%
	mkDailyUsageRow(t, statDate, near.Id, 800)     // 恰好 80%
	mkDailyUsageRow(t, statDate, reached.Id, 1000) // 100%

	ok, err := DisableChannelForDailyLimit(reached.Id, statDate, "limit")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = DisableChannelForDailyLimit(yesterday.Id, statDate-86400, "limit")
	require.NoError(t, err)
	require.True(t, ok)

	f.unlimited, f.farBelow, f.near, f.reached, f.yesterday =
		unlimited.Id, farBelow.Id, near.Id, reached.Id, yesterday.Id
	f.all = []int{f.unlimited, f.farBelow, f.near, f.reached, f.yesterday}
	return f
}

func filterIds(t *testing.T, f dailyFilterFixture, limitFilter string, statDate int64) map[int]bool {
	t.Helper()
	var ids []int
	query := DB.Model(&Channel{}).Where("channels.id IN ?", f.all)
	query = ApplyChannelLimitFilter(query, limitFilter, statDate)
	require.NoError(t, query.Pluck("channels.id", &ids).Error)
	got := make(map[int]bool, len(ids))
	for _, id := range ids {
		got[id] = true
	}
	return got
}

func TestApplyChannelLimitFilter_AllVariants(t *testing.T) {
	requireDB(t)
	statDate := int64(1_701_000_000)
	f := mkDailyFilterFixture(t, statDate)

	t.Run("all", func(t *testing.T) {
		got := filterIds(t, f, ChannelLimitFilterAll, statDate)
		assert.Len(t, got, len(f.all), "all must not filter anything")
	})

	t.Run("configured", func(t *testing.T) {
		got := filterIds(t, f, ChannelLimitFilterConfigured, statDate)
		assert.False(t, got[f.unlimited])
		assert.True(t, got[f.farBelow])
		assert.True(t, got[f.reached])
	})

	t.Run("unlimited", func(t *testing.T) {
		got := filterIds(t, f, ChannelLimitFilterUnlimited, statDate)
		assert.True(t, got[f.unlimited])
		assert.False(t, got[f.farBelow])
	})

	t.Run("reached means currently disabled by the limit, whichever day it tripped", func(t *testing.T) {
		// 「已达上限」不限触发日：昨日触顶且关闭了自动恢复的渠道今天仍被上限挡着，也要列出来。
		got := filterIds(t, f, ChannelLimitFilterReached, statDate)
		assert.True(t, got[f.reached])
		assert.True(t, got[f.yesterday], "a channel still disabled by yesterday's limit has reached its limit")
		assert.False(t, got[f.farBelow])
		assert.False(t, got[f.unlimited])
	})

	t.Run("near", func(t *testing.T) {
		got := filterIds(t, f, ChannelLimitFilterNear, statDate)
		assert.True(t, got[f.near], "exactly at the 80% threshold must be included")
		assert.False(t, got[f.farBelow], "10% is not near the limit")
		assert.False(t, got[f.reached], "already disabled channels belong to 'reached', not 'near'")
		assert.False(t, got[f.unlimited])
	})
}

func TestApplyChannelLimitFilter_NearBoundary(t *testing.T) {
	requireDB(t)
	statDate := int64(1_701_100_000)

	below := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 1000 })
	at := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 1000 })
	mkDailyUsageRow(t, statDate, below.Id, 799) // 79.9%
	mkDailyUsageRow(t, statDate, at.Id, 800)    // 80.0%

	var ids []int
	query := DB.Model(&Channel{}).Where("channels.id IN ?", []int{below.Id, at.Id})
	query = ApplyChannelLimitFilter(query, ChannelLimitFilterNear, statDate)
	require.NoError(t, query.Pluck("channels.id", &ids).Error)

	assert.NotContains(t, ids, below.Id, "79.9% must not be reported as near")
	assert.Contains(t, ids, at.Id, "80.0% is the inclusive boundary")
}

// TestApplyChannelLimitFilter_NearUsesUpstreamAndRoundUsage：用量取上游消耗（cost_quota）；
// 限时恢复模式的渠道只看本轮，不看今日。
func TestApplyChannelLimitFilter_NearUsesUpstreamAndRoundUsage(t *testing.T) {
	requireDB(t)
	statDate := int64(1_701_200_000)

	daily := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 1000 })
	timedLow := mkChannel(t, func(c *Channel) {
		c.DailyQuotaLimit = 1000
		c.DailyLimitRecoverMinutes = 30
		c.DailyLimitPeriodStart = 7_000
	})
	timedHigh := mkChannel(t, func(c *Channel) {
		c.DailyQuotaLimit = 1000
		c.DailyLimitRecoverMinutes = 30
		c.DailyLimitPeriodStart = 7_000
	})
	mkDailyUsageRow(t, statDate, daily.Id, 900)
	mkDailyUsageRow(t, statDate, timedLow.Id, 900)
	mkDailyUsageRow(t, statDate, timedHigh.Id, 900)
	t.Cleanup(func() { _, _ = DeleteChannelLimitPeriodUsageByChannelIds([]int{timedLow.Id, timedHigh.Id}) })
	_, err := UpsertChannelLimitPeriodUsage([]ChannelPeriodDelta{
		{ChannelId: timedLow.Id, PeriodStart: 7_000, CostQuota: 100},
		{ChannelId: timedLow.Id, PeriodStart: 6_000, CostQuota: 990}, // 上一轮，不算
		{ChannelId: timedHigh.Id, PeriodStart: 7_000, CostQuota: 850},
	})
	require.NoError(t, err)

	var ids []int
	query := DB.Model(&Channel{}).Where("channels.id IN ?", []int{daily.Id, timedLow.Id, timedHigh.Id})
	query = ApplyChannelLimitFilter(query, ChannelLimitFilterNear, statDate)
	require.NoError(t, query.Pluck("channels.id", &ids).Error)

	assert.Contains(t, ids, daily.Id)
	assert.NotContains(t, ids, timedLow.Id, "a timed channel is judged by this round, not by today")
	assert.Contains(t, ids, timedHigh.Id)
}

// TestApplyChannelLimitFilter_CountNotInflated 验证筛选不会放大行数——
// 这是分页正确性的前提。
func TestApplyChannelLimitFilter_CountNotInflated(t *testing.T) {
	requireDB(t)
	statDate := int64(1_701_300_000)
	ch := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 1000 })
	mkDailyUsageRow(t, statDate, ch.Id, 900)
	// 造一条其它日期的行，确认不会因为多行用量而重复计数。
	mkDailyUsageRow(t, statDate-86400, ch.Id, 900)

	var count int64
	query := DB.Model(&Channel{}).Where("channels.id = ?", ch.Id)
	query = ApplyChannelLimitFilter(query, ChannelLimitFilterNear, statDate)
	require.NoError(t, query.Count(&count).Error)
	assert.EqualValues(t, 1, count, "a channel must be counted exactly once regardless of usage rows")
}

func TestApplyDailyUsageOrder_UnlimitedAlwaysLast(t *testing.T) {
	requireDB(t)
	statDate := int64(1_701_400_000)

	high := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 1000 })
	low := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 1000 })
	unlimited := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 0 })
	mkDailyUsageRow(t, statDate, high.Id, 900)
	mkDailyUsageRow(t, statDate, low.Id, 100)
	mkDailyUsageRow(t, statDate, unlimited.Id, 5000)

	ids := []int{high.Id, low.Id, unlimited.Id}
	order := func(sortBy string, desc bool) []int {
		var out []int
		query := DB.Model(&Channel{}).Where("channels.id IN ?", ids)
		query, ok := applyDailyUsageOrder(query, sortBy, statDate, desc)
		require.True(t, ok)
		require.NoError(t, query.Pluck("channels.id", &out).Error)
		return out
	}

	// 这是审计指出的缺陷的回归测试：v3 的单级 COALESCE(..., -1) 只在降序把无上限渠道
	// 排到末尾，升序时会跑到最前面。两级排序在升降序下都必须排末尾。
	desc := order(ChannelSortByDailyUsageRatio, true)
	assert.Equal(t, unlimited.Id, desc[len(desc)-1], "unlimited must sort last in DESC")
	asc := order(ChannelSortByDailyUsageRatio, false)
	assert.Equal(t, unlimited.Id, asc[len(asc)-1], "unlimited must sort last in ASC too")

	// 有上限的两个渠道之间按方向排序。
	assert.Equal(t, high.Id, desc[0])
	assert.Equal(t, low.Id, asc[0])
}

// TestApplyDailyUsageOrder_RatioIsFloat 是整数除法的回归测试。
// 漏写 `* 1.0` 时 SQLite/MySQL 会整数除法，两个渠道的比率都退化成 0，排序无法区分。
func TestApplyDailyUsageOrder_RatioIsFloat(t *testing.T) {
	requireDB(t)
	statDate := int64(1_701_500_000)

	half := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 1000 })
	tenth := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 1000 })
	mkDailyUsageRow(t, statDate, half.Id, 500)  // 0.5
	mkDailyUsageRow(t, statDate, tenth.Id, 100) // 0.1

	var out []int
	query := DB.Model(&Channel{}).Where("channels.id IN ?", []int{half.Id, tenth.Id})
	query, ok := applyDailyUsageOrder(query, ChannelSortByDailyUsageRatio, statDate, true)
	require.True(t, ok)
	require.NoError(t, query.Pluck("channels.id", &out).Error)
	require.Len(t, out, 2)
	assert.Equal(t, half.Id, out[0], "0.5 must sort above 0.1; integer division would make both 0")
}

// TestApplyDailyUsageOrder_NoDivisionByZero 是除零的回归测试：
// 缺少 NULLIF 时 PostgreSQL 会直接抛错。
func TestApplyDailyUsageOrder_NoDivisionByZero(t *testing.T) {
	requireDB(t)
	statDate := int64(1_701_600_000)
	zero := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 0 })
	mkDailyUsageRow(t, statDate, zero.Id, 500)

	var out []int
	query := DB.Model(&Channel{}).Where("channels.id = ?", zero.Id)
	query, ok := applyDailyUsageOrder(query, ChannelSortByDailyUsageRatio, statDate, true)
	require.True(t, ok)
	assert.NoError(t, query.Pluck("channels.id", &out).Error, "zero limit must not trigger division by zero")
}

func TestApplyDailyUsageOrder_RejectsUnknownKeysAndZeroDate(t *testing.T) {
	requireDB(t)
	query := DB.Model(&Channel{})
	_, ok := applyDailyUsageOrder(query, "priority", 1, true)
	assert.False(t, ok, "non-usage sort keys must fall through to the column whitelist")
	_, ok = applyDailyUsageOrder(query, ChannelSortByDailyUsed, 0, true)
	assert.False(t, ok, "statDate=0 disables expression sorting (tag mode)")
}

func TestSearchChannels_AppliesLimitFilter(t *testing.T) {
	requireDB(t)
	statDate := int64(1_701_700_000)
	grp := uniq("dlg")
	name := uniq("dlsearch")

	limited := mkChannel(t, func(c *Channel) {
		c.Name = name + "-limited"
		c.Group = grp
		c.DailyQuotaLimit = 1000
	})
	unlimited := mkChannel(t, func(c *Channel) {
		c.Name = name + "-unlimited"
		c.Group = grp
		c.DailyQuotaLimit = 0
	})

	// 不过滤时两个都在。
	found, err := SearchChannels(name, grp, "gpt-4o", true, ChannelLimitFilterAll, statDate)
	require.NoError(t, err)
	assert.Len(t, found, 2)

	// 搜索路径必须与列表路径应用同一套筛选（v3 只改 buildChannelListQuery 时此用例失败）。
	found, err = SearchChannels(name, grp, "gpt-4o", true, ChannelLimitFilterConfigured, statDate)
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, limited.Id, found[0].Id)

	found, err = SearchChannels(name, grp, "gpt-4o", true, ChannelLimitFilterUnlimited, statDate)
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, unlimited.Id, found[0].Id)
}

func TestChannelDailyLimit_BatchAndTagEdit(t *testing.T) {
	requireDB(t)
	tag := uniq("dlbatchtag")
	a := mkChannel(t, func(c *Channel) { c.Tag = &tag })
	b := mkChannel(t, func(c *Channel) { c.Tag = &tag })
	other := mkChannel(t, nil)

	limit := int64(5000)
	off := 0
	dayMode := 0

	// 按 id 批量。
	affected, err := UpdateChannelDailyLimitByIds([]int{a.Id}, DailyLimitEdit{
		QuotaLimit: &limit, AutoRecover: &off, RecoverMinutes: &dayMode,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, affected)

	got := reloadChannel(t, a.Id)
	assert.EqualValues(t, 5000, got.DailyQuotaLimit)
	assert.Zero(t, got.DailyLimitRecoverMinutes)
	assert.False(t, got.GetDailyLimitAutoRecover())
	// 未选中的渠道不受影响。
	assert.EqualValues(t, 0, reloadChannel(t, other.Id).DailyQuotaLimit)

	// 按 Tag 批量（controller 先取 Tag 下的 id 再按 id 更新），覆盖 tag 下所有渠道。
	// 金额要不低于 MinDailyQuotaLimit()，三条写入路径共用同一套校验。
	newLimit := int64(20000)
	tagIds, err := GetChannelIdsByTag(tag)
	require.NoError(t, err)
	affected, err = UpdateChannelDailyLimitByIds(tagIds, DailyLimitEdit{QuotaLimit: &newLimit})
	require.NoError(t, err)
	assert.EqualValues(t, 2, affected)
	assert.EqualValues(t, 20000, reloadChannel(t, a.Id).DailyQuotaLimit)
	assert.EqualValues(t, 20000, reloadChannel(t, b.Id).DailyQuotaLimit)
	// 只传了 limit，其余字段保持不变（nil = 本次不修改）。
	assert.Zero(t, reloadChannel(t, a.Id).DailyLimitRecoverMinutes)
	assert.False(t, reloadChannel(t, a.Id).GetDailyLimitAutoRecover())
}

// TestChannelDailyLimit_BatchClearRequiresExplicitZero 验证「留空 = 不修改，
// 显式 0 = 清除上限」的指针语义。
func TestChannelDailyLimit_BatchClearRequiresExplicitZero(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 800 })

	// 空编辑：什么都不改。
	affected, err := UpdateChannelDailyLimitByIds([]int{ch.Id}, DailyLimitEdit{})
	require.NoError(t, err)
	assert.EqualValues(t, 0, affected)
	assert.EqualValues(t, 800, reloadChannel(t, ch.Id).DailyQuotaLimit)

	// 显式 0：清除上限。结构体 Updates 会忽略零值，因此实现必须用 map。
	zero := int64(0)
	_, err = UpdateChannelDailyLimitByIds([]int{ch.Id}, DailyLimitEdit{QuotaLimit: &zero})
	require.NoError(t, err)
	assert.EqualValues(t, 0, reloadChannel(t, ch.Id).DailyQuotaLimit, "explicit zero must clear the limit")
}

func TestChannelDailyLimit_BatchValidatesInput(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 800 })

	negative := int64(-1)
	_, err := UpdateChannelDailyLimitByIds([]int{ch.Id}, DailyLimitEdit{QuotaLimit: &negative})
	require.Error(t, err, "batch path must reuse the same validation as single-channel save")
	assert.EqualValues(t, 800, reloadChannel(t, ch.Id).DailyQuotaLimit, "rejected edit must not touch the row")

	bad := -1
	limit := int64(1_000_000)
	_, err = UpdateChannelDailyLimitByIds([]int{ch.Id}, DailyLimitEdit{QuotaLimit: &limit, RecoverMinutes: &bad})
	require.Error(t, err)
	assert.EqualValues(t, 800, reloadChannel(t, ch.Id).DailyQuotaLimit, "a rejected edit writes nothing, not even the valid field")
}

// TestChannelDailyLimit_BatchDoesNotTouchStatus 验证批量调高上限不会自动启用渠道。
func TestChannelDailyLimit_BatchDoesNotTouchStatus(t *testing.T) {
	requireDB(t)
	statDate := int64(1_701_800_000)
	ch := mkChannel(t, func(c *Channel) { c.DailyQuotaLimit = 100 })
	ok, err := DisableChannelForDailyLimit(ch.Id, statDate, "limit")
	require.NoError(t, err)
	require.True(t, ok)

	higher := int64(999999)
	_, err = UpdateChannelDailyLimitByIds([]int{ch.Id}, DailyLimitEdit{QuotaLimit: &higher})
	require.NoError(t, err)

	got := reloadChannel(t, ch.Id)
	assert.Equal(t, common.ChannelStatusAutoDisabled, got.Status, "raising the limit must not auto-enable")
	assert.Positive(t, got.DailyLimitDisabledAt, "the disable mark must be preserved")
	assert.EqualValues(t, 999999, got.DailyQuotaLimit)
}

// 按 Tag 改每日上限时，必须在改名前锁定渠道 ID。
//
// 把标签 A 改名成一个已存在的 B 之后再按 B 更新，会把原本就属于 B 的渠道一起改掉。
func TestChannelDailyLimit_TagRenameDoesNotWidenScope(t *testing.T) {
	requireDB(t)
	tagA, tagB := uniq("dl-rename-a"), uniq("dl-rename-b")
	a := mkChannel(t, func(c *Channel) { c.Tag = &tagA })
	b := mkChannel(t, func(c *Channel) { c.Tag = &tagB; c.DailyQuotaLimit = 0 })

	// 改名前锁定 A 的成员。
	ids, err := GetChannelIdsByTag(tagA)
	require.NoError(t, err)
	require.Equal(t, []int{a.Id}, ids, "only channel A carries tag A")

	// A 改名成 B —— 现在两个渠道都叫 B。
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", a.Id).Update("tag", tagB).Error)

	limit := int64(20000)
	affected, err := UpdateChannelDailyLimitByIds(ids, DailyLimitEdit{QuotaLimit: &limit})
	require.NoError(t, err)
	assert.EqualValues(t, 1, affected, "only the originally tagged channel may be updated")
	assert.EqualValues(t, 20000, reloadChannel(t, a.Id).DailyQuotaLimit)
	assert.EqualValues(t, 0, reloadChannel(t, b.Id).DailyQuotaLimit,
		"a channel that already had the destination tag must not be touched")
}
