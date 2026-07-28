package model

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 后台热更新：管理员在设置页保存后，运行中的进程必须立刻按新配置工作，不需重启。
//
// 传播链（由 updateOptionMap 承担，本文件锁住的是「落到本模块之后的行为」）：
//   保存 → UpdateOption 写 options 表 → updateOptionMap 按 "." 拆键
//        → config.GlobalConfig.Get("log_query_setting") → UpdateConfigFromMap 原地改结构体
//   其它节点由 SyncOptions 周期性 loadOptionsFromDatabase 跟上。
//
// 因此只要 getter 每次读取全局结构体，就是热的；唯一的例外是被 sync.Once
// 冻结在首次构造时的进程内 LRU（容量 / 默认 TTL），靠 resetLogStatCache 重建。
// ---------------------------------------------------------------------------

// applyLogQueryOption 模拟管理员在设置页保存一项配置。
func applyLogQueryOption(t *testing.T, field string, value string) {
	t.Helper()
	handled := handleConfigUpdate("log_query_setting."+field, value)
	require.True(t, handled, "log_query_setting.%s 必须被配置分发认领，否则设置页保存了也没用", field)
}

func TestLogQuerySetting_HotReload_BoolSwitches(t *testing.T) {
	s := operation_setting.GetLogQuerySetting()
	prevCache, prevWarm := s.StatCacheEnabled, s.WarmEnabled
	t.Cleanup(func() {
		s.StatCacheEnabled = prevCache
		s.WarmEnabled = prevWarm
	})

	applyLogQueryOption(t, "stat_cache_enabled", "false")
	assert.False(t, s.StatCacheEnabled)
	// 两个逃逸阀必须立刻改变判定结果，否则「一键回退」是假的。
	assert.False(t, logStatCacheUsable(logStatTestAnchor, logStatTestAnchor+3600, false),
		"关掉总开关后应立刻回落原查询")

	applyLogQueryOption(t, "stat_cache_enabled", "true")
	assert.True(t, s.StatCacheEnabled)
	assert.True(t, logStatCacheUsable(logStatTestAnchor, logStatTestAnchor+3600, false))

	applyLogQueryOption(t, "warm_enabled", "false")
	assert.False(t, logStatWarmEnabled(), "关掉预热开关后下一轮循环应跳过")
	applyLogQueryOption(t, "warm_enabled", "true")
	assert.True(t, logStatWarmEnabled())
}

func TestLogQuerySetting_HotReload_Durations(t *testing.T) {
	s := operation_setting.GetLogQuerySetting()
	prevTTL, prevCur, prevTimeout, prevSpan := s.StatCacheTTLHours, s.CurrentHourTTLSec,
		s.QueryTimeoutMs, s.StatMaxCachedHours
	t.Cleanup(func() {
		s.StatCacheTTLHours = prevTTL
		s.CurrentHourTTLSec = prevCur
		s.QueryTimeoutMs = prevTimeout
		s.StatMaxCachedHours = prevSpan
	})

	applyLogQueryOption(t, "stat_cache_ttl_hours", "24")
	assert.Equal(t, 24*time.Hour, s.GetStatCacheTTL())

	applyLogQueryOption(t, "current_hour_ttl_sec", "0")
	assert.Equal(t, time.Duration(0), s.GetCurrentHourTTL(), "0 是合法取值：当前整点改为每次实时查")

	applyLogQueryOption(t, "query_timeout_ms", "5000")
	assert.Equal(t, 5*time.Second, s.GetQueryTimeout())

	// 跨度上限收窄后，超出的区间应立刻开始回落。
	applyLogQueryOption(t, "stat_max_cached_hours", "2")
	assert.True(t, logStatCacheUsable(logStatTestAnchor, logStatTestAnchor+2*3600-1, false))
	assert.False(t, logStatCacheUsable(logStatTestAnchor, logStatTestAnchor+10*3600, false))
}

// 进程内 LRU 的容量被 sync.Once 冻结在首次构造时，改配置必须触发重建，
// 否则这一项在后台改了等于没改。
func TestLogQuerySetting_HotReload_RebuildsMemoryCache(t *testing.T) {
	isolateLogStatCache(t)
	s := operation_setting.GetLogQuerySetting()
	prev := s.StatMemoryEntries
	t.Cleanup(func() { s.StatMemoryEntries = prev })

	before := getLogStatCache()
	require.NotNil(t, before)

	applyLogQueryOption(t, "stat_memory_entries", "256")
	assert.Equal(t, 256, s.GetStatMemoryEntries())

	after := getLogStatCache()
	assert.NotSame(t, before, after, "改容量后必须换成新的缓存实例，否则新容量永远不生效")
}

// 重建缓存实例不能让正在服务的查询出错或读到脏数据。
func TestLogQuerySetting_HotReload_RebuildKeepsQueriesCorrect(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	s := operation_setting.GetLogQuerySetting()
	prev := s.StatMemoryEntries
	t.Cleanup(func() { s.StatMemoryEntries = prev })
	ctx := context.Background()

	base := logStatTestWindow(t)
	start, end := base, base+3600-1
	now := base + 5*3600
	mkLogAt(t, base+10, LogTypeConsume, 17, 0, 0)

	first, err := sumLogStatRangeAt(ctx, start, end, now)
	require.NoError(t, err)
	assert.Equal(t, int64(17), first.Quota)

	// 热更新触发重建后，结果必须依然正确（重建只是丢弃实例，不改变数据）。
	applyLogQueryOption(t, "stat_memory_entries", strconv.Itoa(512))
	second, err := sumLogStatRangeAt(ctx, start, end, now)
	require.NoError(t, err)
	assert.Equal(t, first, second, "重建缓存不得改变查询结果")
}

// 未注册的模块名不应被误认领——否则拼错的 key 会被静默吞掉。
func TestLogQuerySetting_HotReload_UnknownModuleNotHandled(t *testing.T) {
	assert.False(t, handleConfigUpdate("log_query_setting_typo.warm_enabled", "false"))
	assert.False(t, handleConfigUpdate("log_query_setting", "false"), "缺少 . 分隔的键不是分层配置")
}
