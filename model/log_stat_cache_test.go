package model

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 小时缓存分解的正确性。
//
// 核心断言：分解路径的结果 == 直接对 logs 做整段聚合的结果（逐位相等）。
// 这条等式对「兄弟行」免疫——两边聚合的是同一批数据，无论时间窗里还有没有
// 其它测试/生产的行，等式都成立。因此这些用例不需要独占 logs 表。
//
// 时间窗选在很久以前且每个用例互不重叠，理由有二：
//   1. 生产写入只落在「当前整点」，不会并发改动历史窗口，缓存不会中途失效；
//   2. 历史整点相对注入的 now 已经「完成」，才能走到缓存分支。
// 缓存隔离靠 isolateLogStatCache 换掉版本号，Redis 与内存两条路都不会串味。
// ---------------------------------------------------------------------------

// logStatTestAnchor = 2000-01-01T00:00:00Z，整点对齐。
const logStatTestAnchor int64 = 946684800

// 本文件用独立的 id 段与计数器，**不碰** zz_harness_test.go 的共享 nextTestID/uniq。
//
// 原因：共享计数器一旦被这里推进，同一次运行中排在后面的所有用例拿到的 id 都会平移。
// 而共享开发库里存有历史遗留的测试行，平移后某些用例会突然撞上主键冲突
// （实测：本文件早期版本用 nextTestID 时，TestTask_* 会因 tasks 表的残留行报 1062）。
// 测试之间不该有这种隐式耦合，所以这里自带一段互不重叠的 id 空间。
const logStatTestIDBase = 900_000_000

var logStatTestIDCounter int64

func logStatNextID() int {
	return logStatTestIDBase + int(atomic.AddInt64(&logStatTestIDCounter, 1))
}

func logStatUniq(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, atomic.AddInt64(&logStatTestIDCounter, 1))
}

// logStatTestWindow 给每个用例分配一段互不重叠的历史时间窗（起点整点对齐）。
// 每段留 24 小时，足够覆盖跨小时/跨天的用例。
func logStatTestWindow(t *testing.T) int64 {
	t.Helper()
	return logStatTestAnchor + int64(logStatNextID()-logStatTestIDBase)*24*3600
}

// isolateLogStatCache 给当前用例换一个独立的缓存版本号，避免跨用例、
// 跨进程（Redis 有 TTL，键会活过一次 go test）读到陈旧值。
func isolateLogStatCache(t *testing.T) {
	t.Helper()
	prev := logStatCacheVersion
	logStatCacheVersion = logStatUniq("tv")
	t.Cleanup(func() { logStatCacheVersion = prev })
	resetLogStatCache()
	t.Cleanup(resetLogStatCache)
}

// enableLogStatCache 打开缓存开关并把当前整点 TTL 设成给定值。
func enableLogStatCache(t *testing.T, currentHourTTLSec int) {
	t.Helper()
	s := operation_setting.GetLogQuerySetting()
	prevEnabled, prevTTL := s.StatCacheEnabled, s.CurrentHourTTLSec
	s.StatCacheEnabled = true
	s.CurrentHourTTLSec = currentHourTTLSec
	t.Cleanup(func() {
		s.StatCacheEnabled = prevEnabled
		s.CurrentHourTTLSec = prevTTL
	})
}

// forceLogStatCacheFailure 把缓存指向一个连不上的 Redis，使 Get/Set 全部报错。
// 用来验证「缓存故障必须降级回源」这条约定。返回值用于恢复现场。
func forceLogStatCacheFailure(t *testing.T) func() {
	t.Helper()
	prevRDB := common.RDB
	prevEnabled := common.RedisEnabled
	// 端口 1 上不会有服务，拨号立刻被拒（不会卡满 cachex 的 2 秒超时）。
	common.RDB = redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	common.RedisEnabled = true
	resetLogStatCache()
	return func() {
		common.RDB = prevRDB
		common.RedisEnabled = prevEnabled
		resetLogStatCache()
	}
}

// mkLogStatRow 在 LOG_DB 插入一条日志行并注册按 id 清理。
// 刻意不复用 mkLogRow：那个工厂会消耗共享的 nextTestID/uniq（见上方说明）。
func mkLogStatRow(t *testing.T, mut func(l *Log)) *Log {
	t.Helper()
	requireLogDB(t)
	l := &Log{
		UserId:    logStatNextID(),
		Username:  logStatUniq("lsu"),
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeConsume,
	}
	if mut != nil {
		mut(l)
	}
	require.NoError(t, createLog(l))
	id := l.Id
	t.Cleanup(func() {
		if LOG_DB != nil {
			LOG_DB.Where("id = ?", id).Delete(&Log{})
		}
	})
	return l
}

// mkLogAt 在指定时刻插入一条日志行。
func mkLogAt(t *testing.T, at int64, logType int, quota, prompt, completion int) *Log {
	t.Helper()
	return mkLogStatRow(t, func(l *Log) {
		l.CreatedAt = at
		l.Type = logType
		l.Quota = quota
		l.PromptTokens = prompt
		l.CompletionTokens = completion
	})
}

// ---------------------------------------------------------------------------
// 纯函数：整点对齐
// ---------------------------------------------------------------------------

func TestLogStatHourAlignment(t *testing.T) {
	const h = 946684800 // 整点

	assert.Equal(t, int64(h), floorHour(h), "整点向下取整应为自身")
	assert.Equal(t, int64(h), floorHour(h+1))
	assert.Equal(t, int64(h), floorHour(h+3599))
	assert.Equal(t, int64(h+3600), floorHour(h+3600))

	assert.Equal(t, int64(h), ceilHour(h), "整点向上取整应为自身")
	assert.Equal(t, int64(h+3600), ceilHour(h+1))
	assert.Equal(t, int64(h+3600), ceilHour(h+3599))
	assert.Equal(t, int64(h+3600), ceilHour(h+3600))

	// 负数时间戳不该把对齐算崩（Go 的 % 对负数返回负余数）。
	assert.Equal(t, int64(-3600), floorHour(-1))
	assert.Equal(t, int64(0), ceilHour(-1))
}

// ---------------------------------------------------------------------------
// 纯函数：可加性与类型取数
// ---------------------------------------------------------------------------

func TestLogHourStatAddAndCountFor(t *testing.T) {
	a := logHourStat{
		CountAll:    5,
		CountByType: map[int]int64{LogTypeConsume: 3, LogTypeTopup: 2},
		Quota:       100,
		Tokens:      50,
	}
	b := logHourStat{
		CountAll:    4,
		CountByType: map[int]int64{LogTypeConsume: 1, LogTypeError: 3},
		Quota:       7,
		Tokens:      9,
	}
	a.addFrom(b)

	assert.Equal(t, int64(9), a.CountAll)
	assert.Equal(t, int64(4), a.CountByType[LogTypeConsume])
	assert.Equal(t, int64(2), a.CountByType[LogTypeTopup])
	assert.Equal(t, int64(3), a.CountByType[LogTypeError])
	assert.Equal(t, int64(107), a.Quota)
	assert.Equal(t, int64(59), a.Tokens)

	// LogTypeUnknown（0）语义是「不加类型过滤」，取全量而非 map[0]。
	assert.Equal(t, int64(9), a.countFor(LogTypeUnknown))
	assert.Equal(t, int64(4), a.countFor(LogTypeConsume))
	assert.Equal(t, int64(0), a.countFor(LogTypeLogin), "没有的类型应为 0 而不是 panic")

	// 空值加法不应 panic（CountByType 为 nil）。
	var zero logHourStat
	zero.addFrom(b)
	assert.Equal(t, int64(4), zero.CountAll)
	assert.Equal(t, int64(1), zero.CountByType[LogTypeConsume])
}

// ---------------------------------------------------------------------------
// 缓存资格判定（决策覆盖）
// ---------------------------------------------------------------------------

func TestLogStatCacheUsable(t *testing.T) {
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	s := operation_setting.GetLogQuerySetting()

	const start = logStatTestAnchor
	end := start + 3600

	assert.True(t, logStatCacheUsable(start, end, false), "无筛选 + 有时间范围应可用")
	assert.False(t, logStatCacheUsable(start, end, true), "带非类型筛选应回落")
	assert.False(t, logStatCacheUsable(0, 0, false), "无时间范围应回落（Rule 8.3 禁止无界扫描）")
	assert.False(t, logStatCacheUsable(0, end, false), "缺起点应回落")
	assert.False(t, logStatCacheUsable(start, 0, false), "缺终点应回落")
	assert.False(t, logStatCacheUsable(end, start, false), "起点晚于终点应回落")

	// 跨度上限：等于上限可用，超出即回落。
	maxHours := int64(s.GetStatMaxCachedHours())
	assert.True(t, logStatCacheUsable(start, start+maxHours*3600-1, false))
	assert.False(t, logStatCacheUsable(start, start+(maxHours+1)*3600, false))

	// 开关关闭。
	s.StatCacheEnabled = false
	assert.False(t, logStatCacheUsable(start, end, false), "开关关闭应回落")
	s.StatCacheEnabled = true

	// ClickHouse 日志库走原路径（列存的 count/sum 本来就快，加缓存是负优化）。
	prev := common.LogDatabaseType()
	common.SetLogDatabaseType(common.DatabaseTypeClickHouse)
	assert.False(t, logStatCacheUsable(start, end, false), "ClickHouse 应回落")
	common.SetLogDatabaseType(prev)
}

// ---------------------------------------------------------------------------
// 核心：分解结果 == 整段直查
// ---------------------------------------------------------------------------

// assertDecompositionMatchesDirect 是本文件的主断言。
func assertDecompositionMatchesDirect(t *testing.T, ctx context.Context, start, end, now int64) logHourStat {
	t.Helper()
	direct, err := queryLogHourStatRange(ctx, start, end)
	require.NoError(t, err)

	got, err := sumLogStatRangeAt(ctx, start, end, now)
	require.NoError(t, err)

	assert.Equal(t, direct.CountAll, got.CountAll, "全类型行数应与整段直查一致")
	assert.Equal(t, direct.Quota, got.Quota, "消费额度应与整段直查一致")
	assert.Equal(t, direct.Tokens, got.Tokens, "token 数应与整段直查一致")
	for _, lt := range []int{
		LogTypeTopup, LogTypeConsume, LogTypeManage,
		LogTypeSystem, LogTypeError, LogTypeRefund, LogTypeLogin,
	} {
		assert.Equal(t, direct.countFor(lt), got.countFor(lt), "type=%d 的行数应一致", lt)
	}
	return got
}

func TestSumLogStatRange_PureHistoryAllHoursComplete(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	ctx := context.Background()

	base := logStatTestWindow(t)
	// 三个完整整点，各放一条消费日志。
	mkLogAt(t, base+10, LogTypeConsume, 11, 1, 2)
	mkLogAt(t, base+3600+10, LogTypeConsume, 22, 3, 4)
	mkLogAt(t, base+2*3600+10, LogTypeConsume, 33, 5, 6)

	start, end := base, base+3*3600-1
	now := base + 10*3600 // 远晚于窗口，三个整点都已完成

	got := assertDecompositionMatchesDirect(t, ctx, start, end, now)
	assert.Equal(t, int64(66), got.Quota, "11+22+33")
	assert.Equal(t, int64(21), got.Tokens, "(1+2)+(3+4)+(5+6)")
	assert.Equal(t, int64(3), got.countFor(LogTypeConsume))
}

func TestSumLogStatRange_CacheHitAvoidsDatabase(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	ctx := context.Background()

	base := logStatTestWindow(t)
	mkLogAt(t, base+10, LogTypeConsume, 40, 1, 1)
	start, end := base, base+3600-1
	now := base + 5*3600

	first, err := sumLogStatRangeAt(ctx, start, end, now)
	require.NoError(t, err)

	// 第二次必须全部命中缓存：把 LOG_DB 摘掉，若还去查库就会 panic/报错。
	saved := LOG_DB
	LOG_DB = nil
	t.Cleanup(func() { LOG_DB = saved })

	second, err := sumLogStatRangeAt(ctx, start, end, now)
	require.NoError(t, err, "纯历史区间第二次查询不应触碰数据库")
	assert.Equal(t, first, second)
}

func TestSumLogStatRange_HeadResidual(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	ctx := context.Background()

	base := logStatTestWindow(t)
	// 起点落在整点中间：base+1800。头残段 [base+1800, base+3600) 必须补齐。
	mkLogAt(t, base+600, LogTypeConsume, 5, 0, 0)  // 在起点之前，不应计入
	mkLogAt(t, base+2000, LogTypeConsume, 7, 0, 0) // 头残段内
	mkLogAt(t, base+3600+10, LogTypeConsume, 9, 0, 0)

	start, end := base+1800, base+2*3600-1
	now := base + 10*3600

	got := assertDecompositionMatchesDirect(t, ctx, start, end, now)
	assert.Equal(t, int64(16), got.Quota, "7+9，不含起点之前的 5")
}

func TestSumLogStatRange_TailResidual(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	ctx := context.Background()

	base := logStatTestWindow(t)
	mkLogAt(t, base+10, LogTypeConsume, 3, 0, 0)
	mkLogAt(t, base+3600+100, LogTypeConsume, 4, 0, 0)  // 尾残段内
	mkLogAt(t, base+3600+3000, LogTypeConsume, 5, 0, 0) // 终点之后，不应计入

	start, end := base, base+3600+1800
	now := base + 10*3600

	got := assertDecompositionMatchesDirect(t, ctx, start, end, now)
	assert.Equal(t, int64(7), got.Quota, "3+4，不含终点之后的 5")
}

func TestSumLogStatRange_BothResidualsWithinSingleHour(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	ctx := context.Background()

	base := logStatTestWindow(t)
	mkLogAt(t, base+100, LogTypeConsume, 1, 0, 0)  // 区间之前
	mkLogAt(t, base+1200, LogTypeConsume, 2, 0, 0) // 区间内
	mkLogAt(t, base+3000, LogTypeConsume, 4, 0, 0) // 区间之后

	// 起止都在同一个整点内，没有任何完整整点可用。
	start, end := base+1000, base+2000
	now := base + 10*3600

	got := assertDecompositionMatchesDirect(t, ctx, start, end, now)
	assert.Equal(t, int64(2), got.Quota)
}

func TestSumLogStatRange_SpanningCurrentHour(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	ctx := context.Background()

	base := logStatTestWindow(t)
	mkLogAt(t, base+10, LogTypeConsume, 10, 0, 0)        // 已完成整点
	mkLogAt(t, base+3600+10, LogTypeConsume, 20, 0, 0)   // 已完成整点
	mkLogAt(t, base+2*3600+10, LogTypeConsume, 30, 0, 0) // 当前整点（未完成）

	// now 落在第三个整点内 → 前两个整点走缓存，第三个实时查。
	now := base + 2*3600 + 1800
	start, end := base, now

	got := assertDecompositionMatchesDirect(t, ctx, start, end, now)
	assert.Equal(t, int64(60), got.Quota)
}

// 当前整点绝不能进长 TTL 缓存，否则新写入的日志在整点结束前都看不见。
func TestSumLogStatRange_CurrentHourNotCachedWhenTTLZero(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 0) // 当前整点不缓存
	ctx := context.Background()

	base := logStatTestWindow(t)
	mkLogAt(t, base+10, LogTypeConsume, 10, 0, 0)
	now := base + 1800
	start, end := base, now

	before, err := sumLogStatRangeAt(ctx, start, end, now)
	require.NoError(t, err)
	assert.Equal(t, int64(10), before.Quota)

	// 往同一个（未完成的）整点再插一条，必须立刻可见。
	mkLogAt(t, base+20, LogTypeConsume, 5, 0, 0)
	after, err := sumLogStatRangeAt(ctx, start, end, now)
	require.NoError(t, err)
	assert.Equal(t, int64(15), after.Quota, "当前整点必须实时，不得被缓存冻结")
}

// 已完成的整点是不可变量，缓存后不再回源——这是整个方案成立的根基。
func TestSumLogStatRange_CompletedHourIsFrozen(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 0)
	ctx := context.Background()

	base := logStatTestWindow(t)
	mkLogAt(t, base+10, LogTypeConsume, 10, 0, 0)
	start, end := base, base+3600-1
	now := base + 5*3600 // 该整点已完成

	first, err := sumLogStatRangeAt(ctx, start, end, now)
	require.NoError(t, err)
	assert.Equal(t, int64(10), first.Quota)

	// 迟到写入不会被看见——这是设计上的取舍，不是 bug：
	// 已完成整点被视为不可变，换来的是免失效逻辑。
	mkLogAt(t, base+20, LogTypeConsume, 999, 0, 0)
	second, err := sumLogStatRangeAt(ctx, start, end, now)
	require.NoError(t, err)
	assert.Equal(t, int64(10), second.Quota, "已完成整点的缓存值不应被迟到写入改变")
}

func TestSumLogStatRange_TypeBreakdown(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	ctx := context.Background()

	base := logStatTestWindow(t)
	// 每种类型各一条，额度都给 100——只有消费类应计入 Quota。
	types := []int{
		LogTypeTopup, LogTypeConsume, LogTypeManage,
		LogTypeSystem, LogTypeError, LogTypeRefund, LogTypeLogin,
	}
	for i, lt := range types {
		mkLogAt(t, base+int64(i)*10+10, lt, 100, 1, 1)
	}

	start, end := base, base+3600-1
	now := base + 5*3600

	got := assertDecompositionMatchesDirect(t, ctx, start, end, now)
	assert.Equal(t, int64(100), got.Quota, "只有消费类日志计入额度")
	assert.Equal(t, int64(2), got.Tokens, "只有消费类日志计入 token")
	for _, lt := range types {
		assert.Equal(t, int64(1), got.countFor(lt), "type=%d 应为 1 条", lt)
	}
}

func TestSumLogStatRange_MultiDayRange(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	ctx := context.Background()

	base := logStatTestWindow(t)
	// 跨 20 个整点，稀疏放置——验证空整点也被正确处理（不是漏算成 0 就跳过）。
	var want int64
	for i := 0; i < 20; i += 3 {
		mkLogAt(t, base+int64(i)*3600+30, LogTypeConsume, int(i)+1, 0, 0)
		want += int64(i) + 1
	}
	start, end := base, base+20*3600-1
	now := base + 30*3600

	got := assertDecompositionMatchesDirect(t, ctx, start, end, now)
	assert.Equal(t, want, got.Quota)
}

// ---------------------------------------------------------------------------
// 降级路径：任何缓存故障都不能让查询失败
// ---------------------------------------------------------------------------

func TestSumLogStatRange_SurvivesCacheReadFailure(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	ctx := context.Background()

	base := logStatTestWindow(t)
	mkLogAt(t, base+10, LogTypeConsume, 42, 0, 0)
	start, end := base, base+3600-1
	now := base + 5*3600

	// 指向一个连不上的 Redis：Get/Set 都会超时报错，但结果必须依然正确。
	restore := forceLogStatCacheFailure(t)
	defer restore()

	got, err := sumLogStatRangeAt(ctx, start, end, now)
	require.NoError(t, err, "缓存故障必须降级回源，而不是把错误抛给调用方")
	assert.Equal(t, int64(42), got.Quota)
}

// ---------------------------------------------------------------------------
// Redis 后端（跨节点共享）
// ---------------------------------------------------------------------------

func TestSumLogStatRange_RedisBackedSharedAcrossInstances(t *testing.T) {
	requireLogDB(t)
	enableRedis(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	ctx := context.Background()

	base := logStatTestWindow(t)
	mkLogAt(t, base+10, LogTypeConsume, 77, 0, 0)
	start, end := base, base+3600-1
	now := base + 5*3600

	first, err := sumLogStatRangeAt(ctx, start, end, now)
	require.NoError(t, err)
	assert.Equal(t, int64(77), first.Quota)

	// 模拟另一个节点：换一个全新的 cache 实例（进程内 LRU 是空的），
	// 值若真写进了 Redis，新实例就该直接读到。
	resetLogStatCache()
	saved := LOG_DB
	LOG_DB = nil
	t.Cleanup(func() { LOG_DB = saved })

	second, err := sumLogStatRangeAt(ctx, start, end, now)
	require.NoError(t, err, "另一个节点应能直接从 Redis 命中，无需查库")
	assert.Equal(t, first, second)
}

// ---------------------------------------------------------------------------
// 超时保护
// ---------------------------------------------------------------------------

func TestQueryLogHourStatRange_RespectsContextCancellation(t *testing.T) {
	requireLogDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)

	_, err := queryLogHourStatRange(ctx, logStatTestAnchor, logStatTestAnchor+3600)
	assert.Error(t, err, "已取消的 context 必须让查询立刻失败，而不是继续占住连接")
}
