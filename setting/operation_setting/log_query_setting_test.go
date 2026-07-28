package operation_setting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// 边界值分析：每个 getter 在「非法值 → 回退默认」「低于下限 → 夹到下限」
// 「高于上限 → 夹到上限」「区间内 → 原样返回」四类输入下的行为。
// 其中 CurrentHourTTLSec / WarmHours / WarmSleepMs / StatResultCacheSec 的 0
// 是**有意义的取值**（关闭该项），不能被当成非法值回退——这是最容易写错的一处。

func TestLogQuerySetting_StatCacheTTL(t *testing.T) {
	cases := []struct {
		name  string
		hours int
		want  time.Duration
	}{
		{"零值回退默认", 0, DefaultLogQueryStatCacheTTLHours * time.Hour},
		{"负值回退默认", -5, DefaultLogQueryStatCacheTTLHours * time.Hour},
		{"下限", 1, time.Hour},
		{"区间内", 24, 24 * time.Hour},
		{"上限", MaxLogQueryStatCacheTTLHours, MaxLogQueryStatCacheTTLHours * time.Hour},
		{"超出上限夹到上限", MaxLogQueryStatCacheTTLHours + 1, MaxLogQueryStatCacheTTLHours * time.Hour},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &LogQuerySetting{StatCacheTTLHours: c.hours}
			assert.Equal(t, c.want, s.GetStatCacheTTL())
		})
	}
}

// 0 表示「当前整点每次都实时查」，必须原样保留。
func TestLogQuerySetting_CurrentHourTTL(t *testing.T) {
	cases := []struct {
		name string
		sec  int
		want time.Duration
	}{
		{"零值有意义：不缓存当前整点", 0, 0},
		{"负值回退默认", -1, DefaultLogQueryCurrentHourTTLSec * time.Second},
		{"区间内", 30, 30 * time.Second},
		{"上限", MaxLogQueryCurrentHourTTLSec, MaxLogQueryCurrentHourTTLSec * time.Second},
		{"超出上限夹到上限", MaxLogQueryCurrentHourTTLSec + 1, MaxLogQueryCurrentHourTTLSec * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &LogQuerySetting{CurrentHourTTLSec: c.sec}
			assert.Equal(t, c.want, s.GetCurrentHourTTL())
		})
	}
}

func TestLogQuerySetting_StatMaxCachedHours(t *testing.T) {
	assert.Equal(t, DefaultLogQueryStatMaxCachedHours, (&LogQuerySetting{}).GetStatMaxCachedHours())
	assert.Equal(t, DefaultLogQueryStatMaxCachedHours, (&LogQuerySetting{StatMaxCachedHours: -1}).GetStatMaxCachedHours())
	assert.Equal(t, 1, (&LogQuerySetting{StatMaxCachedHours: 1}).GetStatMaxCachedHours())
	assert.Equal(t, 100, (&LogQuerySetting{StatMaxCachedHours: 100}).GetStatMaxCachedHours())
	assert.Equal(t, MaxLogQueryStatMaxCachedHours,
		(&LogQuerySetting{StatMaxCachedHours: MaxLogQueryStatMaxCachedHours + 1}).GetStatMaxCachedHours())
}

func TestLogQuerySetting_StatMemoryEntries(t *testing.T) {
	assert.Equal(t, DefaultLogQueryStatMemoryEntries, (&LogQuerySetting{}).GetStatMemoryEntries())
	// 下限 64：容量太小会让缓存频繁抖动，还不如不缓存。
	assert.Equal(t, 64, (&LogQuerySetting{StatMemoryEntries: 1}).GetStatMemoryEntries())
	assert.Equal(t, 5000, (&LogQuerySetting{StatMemoryEntries: 5000}).GetStatMemoryEntries())
	assert.Equal(t, MaxLogQueryStatMemoryEntries,
		(&LogQuerySetting{StatMemoryEntries: MaxLogQueryStatMemoryEntries + 1}).GetStatMemoryEntries())
}

func TestLogQuerySetting_WarmInterval(t *testing.T) {
	assert.Equal(t, DefaultLogQueryWarmIntervalSec*time.Second, (&LogQuerySetting{}).GetWarmInterval())
	// 下限 60 秒：预热本就每小时才有新活干，比这更密只是空转。
	assert.Equal(t, MinLogQueryWarmIntervalSec*time.Second,
		(&LogQuerySetting{WarmIntervalSec: 1}).GetWarmInterval())
	assert.Equal(t, 600*time.Second, (&LogQuerySetting{WarmIntervalSec: 600}).GetWarmInterval())
	assert.Equal(t, MaxLogQueryWarmIntervalSec*time.Second,
		(&LogQuerySetting{WarmIntervalSec: MaxLogQueryWarmIntervalSec + 1}).GetWarmInterval())
}

// 0 表示启动时不做向前补齐，必须原样保留。
func TestLogQuerySetting_WarmHours(t *testing.T) {
	assert.Equal(t, 0, (&LogQuerySetting{WarmHours: 0}).GetWarmHours())
	assert.Equal(t, DefaultLogQueryWarmHours, (&LogQuerySetting{WarmHours: -1}).GetWarmHours())
	assert.Equal(t, 48, (&LogQuerySetting{WarmHours: 48}).GetWarmHours())
	assert.Equal(t, MaxLogQueryWarmHours, (&LogQuerySetting{WarmHours: MaxLogQueryWarmHours + 1}).GetWarmHours())
}

// 0 表示补齐时不休眠，必须原样保留。
func TestLogQuerySetting_WarmSleep(t *testing.T) {
	assert.Equal(t, time.Duration(0), (&LogQuerySetting{WarmSleepMs: 0}).GetWarmSleep())
	assert.Equal(t, DefaultLogQueryWarmSleepMs*time.Millisecond, (&LogQuerySetting{WarmSleepMs: -1}).GetWarmSleep())
	assert.Equal(t, 500*time.Millisecond, (&LogQuerySetting{WarmSleepMs: 500}).GetWarmSleep())
	assert.Equal(t, MaxLogQueryWarmSleepMs*time.Millisecond,
		(&LogQuerySetting{WarmSleepMs: MaxLogQueryWarmSleepMs + 1}).GetWarmSleep())
}

func TestLogQuerySetting_QueryTimeout(t *testing.T) {
	assert.Equal(t, DefaultLogQueryTimeoutMs*time.Millisecond, (&LogQuerySetting{}).GetQueryTimeout())
	// 下限 1 秒：更短的超时会让正常的残段查询也被误杀。
	assert.Equal(t, MinLogQueryTimeoutMs*time.Millisecond, (&LogQuerySetting{QueryTimeoutMs: 1}).GetQueryTimeout())
	assert.Equal(t, 5*time.Second, (&LogQuerySetting{QueryTimeoutMs: 5000}).GetQueryTimeout())
	assert.Equal(t, MaxLogQueryTimeoutMs*time.Millisecond,
		(&LogQuerySetting{QueryTimeoutMs: MaxLogQueryTimeoutMs + 1}).GetQueryTimeout())
}

// 全局默认值必须是「开箱即用」的：装完不改任何配置就该生效。
func TestLogQuerySetting_GlobalDefaults(t *testing.T) {
	s := GetLogQuerySetting()
	assert.True(t, s.StatCacheEnabled, "缓存默认应开启")
	assert.True(t, s.WarmEnabled, "预热默认应开启")
	assert.Equal(t, DefaultLogQueryCurrentHourTTLSec, s.CurrentHourTTLSec)
	assert.Equal(t, DefaultLogQueryWarmHours, s.WarmHours)
	assert.Equal(t, DefaultLogQueryTimeoutMs, s.QueryTimeoutMs)
}
