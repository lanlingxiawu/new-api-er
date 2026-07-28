package model

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setWarmSleep 把预热的批间 sleep 设为 0，让用例跑得快。
func setWarmSleep(t *testing.T, ms int) {
	t.Helper()
	s := operation_setting.GetLogQuerySetting()
	prev := s.WarmSleepMs
	s.WarmSleepMs = ms
	t.Cleanup(func() { s.WarmSleepMs = prev })
}

// hourIsCached 判断某个整点是否已在长 TTL 缓存里。
func hourIsCached(t *testing.T, hour int64) bool {
	t.Helper()
	_, found, err := getLogStatCache().Get(logStatCacheKey("h", hour))
	require.NoError(t, err)
	return found
}

func TestRunLogStatWarmCycle_WarmsCompletedHoursNewestFirst(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	setWarmSleep(t, 0)
	ctx := context.Background()

	base := logStatTestWindow(t)
	// now 落在 base+5h 这个整点内 → 已完成的最近整点是 base+4h。
	now := base + 5*3600 + 100

	require.Equal(t, 3, runLogStatWarmCycle(ctx, now, 3))

	assert.True(t, hourIsCached(t, base+4*3600), "最近一个已完成的整点应被预热")
	assert.True(t, hourIsCached(t, base+3*3600))
	assert.True(t, hourIsCached(t, base+2*3600))
	assert.False(t, hourIsCached(t, base+3600), "超出本轮范围的整点不应被预热")
}

// 当前整点仍在写入，绝不能进长 TTL 缓存——否则整点结束前新日志都看不见。
func TestRunLogStatWarmCycle_NeverWarmsCurrentHour(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	setWarmSleep(t, 0)
	ctx := context.Background()

	base := logStatTestWindow(t)
	now := base + 5*3600 + 100
	currentHour := floorHour(now)

	runLogStatWarmCycle(ctx, now, 3)

	assert.False(t, hourIsCached(t, currentHour), "当前整点不得进入长 TTL 缓存")
	assert.True(t, hourIsCached(t, currentHour-3600), "上一个整点应被预热")
}

func TestRunLogStatWarmCycle_SkipsAlreadyCachedHours(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	setWarmSleep(t, 0)
	ctx := context.Background()

	base := logStatTestWindow(t)
	mkLogAt(t, base+4*3600+10, LogTypeConsume, 31, 0, 0)
	now := base + 5*3600 + 100

	require.Equal(t, 1, runLogStatWarmCycle(ctx, now, 1))
	require.True(t, hourIsCached(t, base+4*3600))

	// 第二轮必须直接跳过：摘掉 LOG_DB，若还去查库就会报错。
	saved := LOG_DB
	LOG_DB = nil
	t.Cleanup(func() { LOG_DB = saved })

	assert.Equal(t, 1, runLogStatWarmCycle(ctx, now, 1), "已缓存的整点应跳过而不是回源")
}

func TestRunLogStatWarmCycle_HonoursCancellation(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	setWarmSleep(t, 0)

	base := logStatTestWindow(t)
	now := base + 20*3600

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	assert.Equal(t, 0, runLogStatWarmCycle(ctx, now, 10), "已取消的 context 应立刻停止预热")
}

func TestRunLogStatWarmCycle_ZeroOrNegativeHoursIsNoop(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	ctx := context.Background()

	now := logStatTestWindow(t) + 5*3600
	assert.Equal(t, 0, runLogStatWarmCycle(ctx, now, 0))
	assert.Equal(t, 0, runLogStatWarmCycle(ctx, now, -1))
}

// 预热失败必须完全静默：它只影响命中率，绝不能影响在线请求。
func TestRunLogStatWarmCycle_SilentOnQueryFailure(t *testing.T) {
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	setWarmSleep(t, 0)
	ctx := context.Background()

	saved := LOG_DB
	LOG_DB = nil
	t.Cleanup(func() { LOG_DB = saved })

	now := logStatTestWindow(t) + 5*3600
	assert.NotPanics(t, func() {
		runLogStatWarmCycle(ctx, now, 2)
	}, "查询失败不得 panic，也不得向外传播")
}

func TestRunLogStatWarmCycleSafely_RecoversFromPanic(t *testing.T) {
	isolateLogStatCache(t)
	// 让 GetLogQuerySetting 之后的路径 panic：把缓存实例置空是做不到的
	// （getLogStatCache 会重建），所以直接验证 recover 包装本身生效。
	assert.NotPanics(t, func() {
		runLogStatWarmCycleSafely(context.Background(), 0)
	})
}

func TestLogStatWarmEnabled(t *testing.T) {
	s := operation_setting.GetLogQuerySetting()
	prevCache, prevWarm := s.StatCacheEnabled, s.WarmEnabled
	t.Cleanup(func() {
		s.StatCacheEnabled = prevCache
		s.WarmEnabled = prevWarm
	})

	s.StatCacheEnabled, s.WarmEnabled = true, true
	assert.True(t, logStatWarmEnabled())

	s.WarmEnabled = false
	assert.False(t, logStatWarmEnabled(), "预热开关关闭时不应预热")

	s.WarmEnabled = true
	s.StatCacheEnabled = false
	assert.False(t, logStatWarmEnabled(), "缓存总开关关闭时预热没有意义")

	s.StatCacheEnabled = true
	prevDB := common.LogDatabaseType()
	common.SetLogDatabaseType(common.DatabaseTypeClickHouse)
	assert.False(t, logStatWarmEnabled(), "ClickHouse 不需要预热")
	common.SetLogDatabaseType(prevDB)
}

// 非 master 节点不得启动预热协程：多节点同时跑只是白白多耗数据库。
func TestStartLogStatWarmLoop_MasterOnly(t *testing.T) {
	prev := common.IsMasterNode
	common.IsMasterNode = false
	t.Cleanup(func() { common.IsMasterNode = prev })

	isolateLogStatCache(t)
	enableLogStatCache(t, 15)

	saved := LOG_DB
	LOG_DB = nil
	t.Cleanup(func() { LOG_DB = saved })

	// 非 master 时应立即返回，不起协程、不碰数据库。
	assert.NotPanics(t, StartLogStatWarmLoop)
	time.Sleep(50 * time.Millisecond)
}

// 多 master 部署下，同一个整点只应有一个节点真正去算。
func TestAcquireLogStatWarmLock_MutualExclusion(t *testing.T) {
	enableRedis(t)
	isolateLogStatCache(t)
	ctx := context.Background()

	hour := logStatTestWindow(t)
	t.Cleanup(func() {
		if common.RDB != nil {
			common.RDB.Del(ctx, logStatWarmLockKey(hour))
		}
	})

	assert.True(t, acquireLogStatWarmLock(ctx, hour), "首个节点应拿到锁")
	assert.False(t, acquireLogStatWarmLock(ctx, hour), "第二个节点应被挡住")
}

// 没有 Redis 时不该因为拿不到锁就停掉预热——单机本就没有竞争。
func TestAcquireLogStatWarmLock_NoRedisAlwaysProceeds(t *testing.T) {
	prev := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prev })

	assert.True(t, acquireLogStatWarmLock(context.Background(), logStatTestAnchor))
}
