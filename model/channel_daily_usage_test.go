package model

import (
	"fmt"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 渠道每日用量表的读写测试。设计见 docs/design/channel-daily-quota-limit.md §4。
//
// 按 Rule 15.5 使用真实项目库（无则回退 harness 的 SQLite），并且只清理自己插入的行。

// requireUpsert 是 UpsertChannelDailyUsage 的测试包装：该函数返回 (已处理条数, error)，
// 用例只关心成功与否。
func requireUpsert(t *testing.T, deltas []ChannelDailyDelta) {
	t.Helper()
	_, err := UpsertChannelDailyUsage(deltas)
	require.NoError(t, err)
}

// mkDailyUsageRow 插入一行用量并注册行级清理。
func mkDailyUsageRow(t *testing.T, statDate int64, channelId int, cost int64) {
	t.Helper()
	requireUpsert(t, ([]ChannelDailyDelta{{
		StatDate: statDate, ChannelId: channelId, CostQuota: cost,
	}}))
	cleanupDailyUsage(t, statDate, channelId)
}

func cleanupDailyUsage(t *testing.T, statDate int64, channelId int) {
	t.Helper()
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Where("stat_date = ? AND channel_id = ?", statDate, channelId).Delete(&ChannelDailyUsage{})
		}
	})
}

func readDailyUsage(t *testing.T, statDate int64, channelId int) *ChannelDailyUsage {
	t.Helper()
	rows, err := GetChannelDailyUsages(statDate, []int{channelId})
	require.NoError(t, err)
	return rows[channelId]
}

func TestChannelDailyUsage_UpsertAccumulates(t *testing.T) {
	requireDB(t)
	statDate := int64(1_700_000_000)
	channelId := nextTestID()
	cleanupDailyUsage(t, statDate, channelId)

	// 首次插入。
	requireUpsert(t, ([]ChannelDailyDelta{{
		StatDate: statDate, ChannelId: channelId, CostQuota: 40,
	}}))
	row := readDailyUsage(t, statDate, channelId)
	require.NotNil(t, row)
	assert.EqualValues(t, 40, row.CostQuota)

	// 重复 upsert 必须累加而不是覆盖——跨节点汇总完全依赖这个语义。
	requireUpsert(t, ([]ChannelDailyDelta{{
		StatDate: statDate, ChannelId: channelId, CostQuota: 10,
	}}))
	row = readDailyUsage(t, statDate, channelId)
	require.NotNil(t, row)
	assert.EqualValues(t, 50, row.CostQuota)
}

func TestChannelDailyUsage_UpsertIgnoresNoop(t *testing.T) {
	requireDB(t)
	statDate := int64(1_700_086_400)
	channelId := nextTestID()
	cleanupDailyUsage(t, statDate, channelId)

	// 空批次、零增量、非法 id 都不应写入任何行。
	requireUpsert(t, (nil))
	requireUpsert(t, ([]ChannelDailyDelta{
		{StatDate: statDate, ChannelId: channelId, CostQuota: 0},
		{StatDate: statDate, ChannelId: 0, CostQuota: 5},
		{StatDate: 0, ChannelId: channelId, CostQuota: 5},
	}))
	assert.Nil(t, readDailyUsage(t, statDate, channelId))
}

func TestChannelDailyUsage_ConcurrentUpsert(t *testing.T) {
	requireDB(t)
	statDate := int64(1_700_172_800)
	channelId := nextTestID()
	cleanupDailyUsage(t, statDate, channelId)

	const goroutines = 20
	const perGoroutine = 5
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				if _, err := UpsertChannelDailyUsage([]ChannelDailyDelta{{
					StatDate: statDate, ChannelId: channelId, CostQuota: 2,
				}}); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	row := readDailyUsage(t, statDate, channelId)
	require.NotNil(t, row)
	assert.EqualValues(t, goroutines*perGoroutine*2, row.CostQuota, "concurrent upserts must not lose increments")
}

func TestChannelDailyUsage_BatchQueryScopedByDateAndChannel(t *testing.T) {
	requireDB(t)
	today := int64(1_700_259_200)
	yesterday := today - 86400
	chA, chB, chC := nextTestID(), nextTestID(), nextTestID()

	mkDailyUsageRow(t, today, chA, 10)
	mkDailyUsageRow(t, today, chB, 20)
	mkDailyUsageRow(t, yesterday, chA, 999)
	mkDailyUsageRow(t, today, chC, 30)

	rows, err := GetChannelDailyUsages(today, []int{chA, chB})
	require.NoError(t, err)
	assert.Len(t, rows, 2, "must not leak other dates or other channels")
	require.NotNil(t, rows[chA])
	assert.EqualValues(t, 10, rows[chA].CostQuota, "yesterday's row must not bleed into today")
	require.NotNil(t, rows[chB])
	assert.Nil(t, rows[chC])

	// 边界：空 id 列表 / 非法日期返回空 map 而不是查全表。
	empty, err := GetChannelDailyUsages(today, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
	empty, err = GetChannelDailyUsages(0, []int{chA})
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestChannelDailyUsage_CleanupRetentionBoundary(t *testing.T) {
	requireDB(t)
	cutoff := int64(1_700_432_000)
	channelId := nextTestID()

	// 边界值：cutoff 当天保留，cutoff 前一天删除。
	mkDailyUsageRow(t, cutoff, channelId, 1)
	mkDailyUsageRow(t, cutoff-86400, channelId, 2)
	mkDailyUsageRow(t, cutoff+86400, channelId, 3)

	deleted, err := CleanupChannelDailyUsage(cutoff)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, deleted, int64(1))

	assert.Nil(t, readDailyUsage(t, cutoff-86400, channelId), "row before cutoff must be deleted")
	assert.NotNil(t, readDailyUsage(t, cutoff, channelId), "row exactly at cutoff must be kept")
	assert.NotNil(t, readDailyUsage(t, cutoff+86400, channelId), "row after cutoff must be kept")

	// cutoff <= 0 是安全的空操作，不能误删全表。
	deleted, err = CleanupChannelDailyUsage(0)
	require.NoError(t, err)
	assert.EqualValues(t, 0, deleted)
	assert.NotNil(t, readDailyUsage(t, cutoff, channelId))
}

func TestChannelDailyUsage_CleanupPaginatesBeyondBatchSize(t *testing.T) {
	requireDB(t)
	cutoff := int64(1_700_600_000)
	base := cutoff - 86400
	channelId := nextTestID()

	// keyset 翻页：造出超过单批上限的行数，确认全部删除而不是只删第一批。
	rows := channelDailyUsageCleanupBatch + 7
	deltas := make([]ChannelDailyDelta, 0, rows)
	for i := 0; i < rows; i++ {
		deltas = append(deltas, ChannelDailyDelta{
			StatDate: base - int64(i), ChannelId: channelId, CostQuota: 1,
		})
	}
	requireUpsert(t, (deltas))
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Where("channel_id = ?", channelId).Delete(&ChannelDailyUsage{})
		}
	})

	deleted, err := CleanupChannelDailyUsage(cutoff)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, deleted, int64(rows), fmt.Sprintf("expected at least %d rows deleted across batches", rows))

	var remaining int64
	require.NoError(t, DB.Model(&ChannelDailyUsage{}).Where("channel_id = ?", channelId).Count(&remaining).Error)
	assert.EqualValues(t, 0, remaining)
}

func TestChannelDailyLimit_RecoveryHelpers(t *testing.T) {
	ch := &Channel{}
	assert.True(t, ch.GetDailyLimitAutoRecover(), "nil means auto recover (default)")
	assert.False(t, ch.IsDailyLimitTimed())
	off := 0
	ch.DailyLimitAutoRecover = &off
	assert.False(t, ch.GetDailyLimitAutoRecover())
	on := 1
	ch.DailyLimitAutoRecover = &on
	assert.True(t, ch.GetDailyLimitAutoRecover())

	// 新建渠道：限时模式隐含自动恢复并从当前时刻开启第一轮；客户端传入的轮次不采信。
	timed := &Channel{DailyLimitRecoverMinutes: 30, DailyLimitAutoRecover: &off, DailyLimitPeriodStart: 42}
	timed.NormalizeDailyLimitRecovery(1_700_000_000)
	assert.True(t, timed.IsDailyLimitTimed())
	assert.True(t, timed.GetDailyLimitAutoRecover(), "timed mode implies auto recovery")
	assert.EqualValues(t, 1_700_000_000, timed.DailyLimitPeriodStart)

	daily := &Channel{DailyLimitPeriodStart: 42}
	daily.NormalizeDailyLimitRecovery(1_700_000_000)
	assert.Zero(t, daily.DailyLimitPeriodStart, "day mode has no rounds")
}

func TestChannelDailyLimit_ValidateConfig(t *testing.T) {
	minimum := MinDailyQuotaLimit()
	require.Greater(t, minimum, int64(1), "the minimum must be a meaningful amount, not 1 quota unit")

	// 边界值分析：负数拒绝，0 表示无上限（允许），最小值上下各一格。
	assert.Error(t, ValidateDailyLimitConfig(-1, 0))
	assert.NoError(t, ValidateDailyLimitConfig(0, 0))
	assert.Error(t, ValidateDailyLimitConfig(1, 0),
		"1 quota unit is far below one cent and would disable the channel on its first request")
	assert.Error(t, ValidateDailyLimitConfig(minimum-1, 0))
	assert.NoError(t, ValidateDailyLimitConfig(minimum, 0))
	assert.NoError(t, ValidateDailyLimitConfig(minimum+1, 0))

	// 恢复间隔的边界（金额取合法值，确保命中的是间隔分支）：0 = 按日模式，1–10080 分钟合法。
	for _, minutes := range []int{0, 1, MaxDailyLimitRecoverMinutes} {
		assert.NoError(t, ValidateDailyLimitConfig(1_000_000, minutes), "minutes=%d", minutes)
	}
	for _, minutes := range []int{-1, MaxDailyLimitRecoverMinutes + 1} {
		err := ValidateDailyLimitConfig(1_000_000, minutes)
		require.Error(t, err, "minutes=%d", minutes)
		assert.Contains(t, err.Error(), "invalid_recover_minutes")
	}
}

// TestChannelDailyLimit_MinimumFollowsQuotaPerUnit 锁住最小值跟随金额换算：
// 写死一个常量在 Tokens 模式 / 自定义货币下等于没有下限。
func TestChannelDailyLimit_MinimumFollowsQuotaPerUnit(t *testing.T) {
	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })

	common.QuotaPerUnit = 500000
	assert.EqualValues(t, 5000, MinDailyQuotaLimit(), "one cent at the default conversion")

	common.QuotaPerUnit = 1000
	assert.EqualValues(t, 10, MinDailyQuotaLimit())

	// 换算极小时兜底到 1，不能返回 0（0 会让 limit > 0 && limit < min 永远为假）。
	common.QuotaPerUnit = 1
	assert.EqualValues(t, 1, MinDailyQuotaLimit())
}

func TestChannel_ValidateSettingsRejectsBadDailyLimit(t *testing.T) {
	ch := &Channel{Type: 1, DailyQuotaLimit: -5}
	require.Error(t, ch.ValidateSettings())

	ch = &Channel{Type: 1, DailyQuotaLimit: 1_000_000, DailyLimitRecoverMinutes: MaxDailyLimitRecoverMinutes + 1}
	require.Error(t, ch.ValidateSettings())

	// 低于最小上限（一分钱）也要拒绝：这种渠道第一个请求结算完就会被禁用。
	ch = &Channel{Type: 1, DailyQuotaLimit: 100}
	require.Error(t, ch.ValidateSettings())

	ch = &Channel{Type: 1, DailyQuotaLimit: 1_000_000, DailyLimitRecoverMinutes: 30}
	require.NoError(t, ch.ValidateSettings())


	// 存量渠道（全部默认值）必须继续通过校验。
	require.NoError(t, (&Channel{Type: 1}).ValidateSettings())
}

func TestChannelDailyLimit_LoadConfigsOnlyLimited(t *testing.T) {
	requireDB(t)
	limited := mkChannel(t, func(ch *Channel) {
		ch.DailyQuotaLimit = 5000
		ch.DailyLimitRecoverMinutes = 30
		ch.DailyLimitPeriodStart = 1_700_000_000
	})
	unlimited := mkChannel(t, func(ch *Channel) { ch.DailyQuotaLimit = 0 })

	configs, err := LoadDailyLimitConfigs()
	require.NoError(t, err)

	byId := make(map[int]DailyLimitConfig, len(configs))
	for _, cfg := range configs {
		byId[cfg.ChannelId] = cfg
	}
	got, ok := byId[limited.Id]
	require.True(t, ok, "channel with a limit must appear in the snapshot")
	assert.EqualValues(t, 5000, got.LimitQuota)
	assert.Equal(t, 30, got.RecoverMinutes)
	assert.EqualValues(t, 1_700_000_000, got.PeriodStart)
	assert.Equal(t, common.ChannelStatusEnabled, got.Status)

	_, ok = byId[unlimited.Id]
	assert.False(t, ok, "channels without a limit must not be loaded")
}

// 渠道删除后不得留下指向已删渠道的用量行。
//
// 回归背景：累计器的 pending 增量活在内存里，删除渠道并不会清掉它；下一个 flush 周期
// 会把它 upsert 回 channel_daily_usages，留下一条孤儿记录。修法是「先丢内存、再删行」，
// 外加一个周期性的孤儿兜底清理。

func TestChannelDailyUsage_DeleteByChannelIds(t *testing.T) {
	requireDB(t)
	a := mkChannel(t, nil)
	b := mkChannel(t, nil)
	statDate := int64(1_700_000_000)

	_, err := UpsertChannelDailyUsage([]ChannelDailyDelta{
		{StatDate: statDate, ChannelId: a.Id, CostQuota: 10},
		{StatDate: statDate - 86400, ChannelId: a.Id, CostQuota: 20},
		{StatDate: statDate, ChannelId: b.Id, CostQuota: 30},
	})
	require.NoError(t, err)

	removed, err := DeleteChannelDailyUsageByChannelIds([]int{a.Id})
	require.NoError(t, err)
	assert.EqualValues(t, 2, removed, "both days of channel a must go")

	rows, err := GetChannelDailyUsages(statDate, []int{a.Id, b.Id})
	require.NoError(t, err)
	assert.NotContains(t, rows, a.Id, "deleted channel must have no usage rows left")
	assert.Contains(t, rows, b.Id, "an unrelated channel must be untouched")

	// 空入参是安全的 no-op，不能退化成删全表。
	removed, err = DeleteChannelDailyUsageByChannelIds(nil)
	require.NoError(t, err)
	assert.EqualValues(t, 0, removed)
	rows, err = GetChannelDailyUsages(statDate, []int{b.Id})
	require.NoError(t, err)
	assert.Contains(t, rows, b.Id, "nil input must not delete anything")

	_, _ = DeleteChannelDailyUsageByChannelIds([]int{b.Id})
}

// TestChannelDailyUsage_DeleteOrphans 覆盖兜底清理，同时验证 NOT IN (子查询) 这条
// 跨库写法在真实数据库上可用。
func TestChannelDailyUsage_DeleteOrphans(t *testing.T) {
	requireDB(t)
	live := mkChannel(t, nil)
	statDate := int64(1_700_000_100)
	// 一个必然不存在的渠道 id。
	var maxId int
	require.NoError(t, DB.Model(&Channel{}).Select("COALESCE(MAX(id),0)").Scan(&maxId).Error)
	ghost := maxId + 100_000

	_, err := UpsertChannelDailyUsage([]ChannelDailyDelta{
		{StatDate: statDate, ChannelId: live.Id, CostQuota: 5},
		{StatDate: statDate, ChannelId: ghost, CostQuota: 7},
	})
	require.NoError(t, err)

	removed, err := DeleteOrphanChannelDailyUsage()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, removed, int64(1), "the ghost row must be removed")

	rows, err := GetChannelDailyUsages(statDate, []int{live.Id, ghost})
	require.NoError(t, err)
	assert.Contains(t, rows, live.Id, "a row for a live channel must survive the orphan sweep")
	assert.NotContains(t, rows, ghost, "the orphan row must be gone")

	_, _ = DeleteChannelDailyUsageByChannelIds([]int{live.Id})
}
