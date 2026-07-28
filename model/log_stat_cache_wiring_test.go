package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 缓存分解接入 GetAllLogs / SumUsedQuota 之后的行为。
//
// 这些用例断言的是「对外可见的结果不变」：无论走缓存还是回落，
// 列表总数与统计额度都必须与直接扫 logs 的结果相同。
//
// 基线用 queryLogHourStatRange 在插入前量一次——它是直查，不写缓存，
// 所以不会把空值冻结进去。这样即便时间窗里有其它行，断言依然成立。
// ---------------------------------------------------------------------------

func logStatBaseline(t *testing.T, start, end int64) logHourStat {
	t.Helper()
	base, err := queryLogHourStatRange(context.Background(), start, end)
	require.NoError(t, err)
	return base
}

func TestGetAllLogs_TotalViaHourCacheMatchesDirectCount(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)

	base := logStatTestWindow(t)
	start, end := base, base+2*3600-1
	before := logStatBaseline(t, start, end)

	mkLogAt(t, base+10, LogTypeConsume, 5, 0, 0)
	mkLogAt(t, base+20, LogTypeConsume, 6, 0, 0)
	mkLogAt(t, base+3600+10, LogTypeTopup, 7, 0, 0)

	logs, total, err := GetAllLogs(LogTypeUnknown, start, end, "", "", "", 0, 100, 0, 0, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, before.CountAll+3, total, "全类型总数应精确，不被截断")
	assert.Len(t, logs, int(before.CountAll)+3, "列表数据本身不受缓存影响")

	// 与直查逐位一致——这是缓存分解正确性的对外体现。
	direct := logStatBaseline(t, start, end)
	assert.Equal(t, direct.CountAll, total)
}

func TestGetAllLogs_TotalRespectsTypeFilterThroughCache(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)

	base := logStatTestWindow(t)
	start, end := base, base+3600-1
	before := logStatBaseline(t, start, end)

	mkLogAt(t, base+10, LogTypeConsume, 1, 0, 0)
	mkLogAt(t, base+20, LogTypeConsume, 1, 0, 0)
	mkLogAt(t, base+30, LogTypeError, 0, 0, 0)

	// 类型不进缓存指纹，而是作为字段存在值里——所以切换类型 tab 仍命中同一份缓存。
	_, consume, err := GetAllLogs(LogTypeConsume, start, end, "", "", "", 0, 10, 0, 0, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, before.countFor(LogTypeConsume)+2, consume)

	_, errType, err := GetAllLogs(LogTypeError, start, end, "", "", "", 0, 10, 0, 0, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, before.countFor(LogTypeError)+1, errType)

	_, topup, err := GetAllLogs(LogTypeTopup, start, end, "", "", "", 0, 10, 0, 0, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, before.countFor(LogTypeTopup), topup, "没有该类型的行时应为基线值")
}

func TestGetAllLogs_FallsBackWhenFiltered(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)

	base := logStatTestWindow(t)
	start, end := base, base+3600-1
	user := logStatUniq("wireu")

	mkLogStatRow(t, func(l *Log) {
		l.CreatedAt = base + 10
		l.Type = LogTypeConsume
		l.Username = user
		l.Quota = 3
	})
	mkLogAt(t, base+20, LogTypeConsume, 99, 0, 0) // 别的用户，不该被算进来

	// 带 username 筛选 → 回落原查询，结果只能是该用户的行。
	logs, total, err := GetAllLogs(LogTypeUnknown, start, end, "", user, "", 0, 10, 0, 0, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), total, "带筛选时不得错用无筛选的缓存值")
	require.Len(t, logs, 1)
	assert.Equal(t, user, logs[0].Username)
}

func TestGetAllLogs_FallsBackWhenCacheDisabled(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)
	s := operation_setting.GetLogQuerySetting()
	s.StatCacheEnabled = false

	base := logStatTestWindow(t)
	start, end := base, base+3600-1
	before := logStatBaseline(t, start, end)
	mkLogAt(t, base+10, LogTypeConsume, 5, 0, 0)

	_, total, err := GetAllLogs(LogTypeUnknown, start, end, "", "", "", 0, 10, 0, 0, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, before.CountAll+1, total, "关掉缓存后结果必须与今天一致")
}

// 无时间范围时必须回落——Rule 8.3 禁止无界扫描，也不能拿缓存瞎凑。
func TestGetAllLogs_FallsBackWithoutTimeRange(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)

	base := logStatTestWindow(t)
	user := logStatUniq("wirent")
	mkLogStatRow(t, func(l *Log) {
		l.CreatedAt = base + 10
		l.Type = LogTypeConsume
		l.Username = user
	})

	// 用 username 收窄，避免真的去数全表。
	_, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "", user, "", 0, 10, 0, 0, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
}

func TestSumUsedQuota_QuotaViaHourCacheMatchesDirectSum(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)

	base := logStatTestWindow(t)
	start, end := base, base+2*3600-1
	before := logStatBaseline(t, start, end)

	mkLogAt(t, base+10, LogTypeConsume, 11, 0, 0)
	mkLogAt(t, base+3600+10, LogTypeConsume, 22, 0, 0)
	mkLogAt(t, base+20, LogTypeTopup, 1000, 0, 0) // 非消费类，不计入额度

	stat, err := SumUsedQuota(LogTypeUnknown, start, end, "", "", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, int(before.Quota)+33, stat.Quota, "额度只累加消费类日志")
}

func TestSumUsedQuota_FallsBackWhenFiltered(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)

	base := logStatTestWindow(t)
	start, end := base, base+3600-1
	user := logStatUniq("wireq")

	mkLogStatRow(t, func(l *Log) {
		l.CreatedAt = base + 10
		l.Type = LogTypeConsume
		l.Username = user
		l.Quota = 8
	})
	mkLogAt(t, base+20, LogTypeConsume, 500, 0, 0) // 别的用户

	stat, err := SumUsedQuota(LogTypeUnknown, start, end, "", user, "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, 8, stat.Quota, "带筛选时不得错用无筛选的缓存值")
}

// SumUsedQuota 历来忽略 logType（恒按消费类统计），缓存路径必须保持同样口径，
// 否则「类型」筛选会突然开始影响统计徽章——那是行为变更，不是性能优化。
func TestSumUsedQuota_IgnoresLogTypeLikeBefore(t *testing.T) {
	requireLogDB(t)
	isolateLogStatCache(t)
	enableLogStatCache(t, 15)

	base := logStatTestWindow(t)
	start, end := base, base+3600-1
	before := logStatBaseline(t, start, end)

	mkLogAt(t, base+10, LogTypeConsume, 12, 0, 0)
	mkLogAt(t, base+20, LogTypeTopup, 999, 0, 0)

	want := int(before.Quota) + 12
	for _, lt := range []int{LogTypeUnknown, LogTypeTopup, LogTypeConsume, LogTypeError} {
		stat, err := SumUsedQuota(lt, start, end, "", "", "", 0, "")
		require.NoError(t, err)
		assert.Equal(t, want, stat.Quota, "logType=%d 不应改变额度统计口径", lt)
	}
}
