package operation_setting

import (
	"time"

	"github.com/QuantumNous/new-api/setting/config"
)

// LogQuerySetting 控制使用日志列表与统计接口的查询加速。
//
// 行数与额度都是按时间可加的量，因此按整点小时缓存，两端不足整点的残段实时补齐，
// 结果与直接扫 logs 逐位相等。命中后每次查询只剩两端各 ≤1 小时的扫描。
// 已完成的整点其值不再变化，所以缓存不需要任何失效逻辑。
type LogQuerySetting struct {
	// —— 缓存 ——
	StatCacheEnabled   bool `json:"stat_cache_enabled"`
	StatCacheTTLHours  int  `json:"stat_cache_ttl_hours"`  // 已完成整点的 TTL
	CurrentHourTTLSec  int  `json:"current_hour_ttl_sec"`  // 当前整点的短 TTL；0 = 每次实时查
	StatMaxCachedHours int  `json:"stat_max_cached_hours"` // 单次查询最多拼多少个整点
	StatMemoryEntries  int  `json:"stat_memory_entries"`   // 无 Redis 时的进程内 LRU 容量

	// —— 预热 ——
	WarmEnabled     bool `json:"warm_enabled"`
	WarmIntervalSec int  `json:"warm_interval_sec"`
	WarmHours       int  `json:"warm_hours"`    // 启动时向前补齐的整点数；0 = 不补齐
	WarmSleepMs     int  `json:"warm_sleep_ms"` // 补齐时批间 sleep

	// —— 通用 ——
	QueryTimeoutMs int `json:"query_timeout_ms"`
}

const (
	DefaultLogQueryStatCacheTTLHours  = 48 // 2 天，应短于日志保留期
	DefaultLogQueryCurrentHourTTLSec  = 15
	DefaultLogQueryStatMaxCachedHours = 720 // 30 天
	DefaultLogQueryStatMemoryEntries  = 4096
	DefaultLogQueryWarmIntervalSec    = 300
	DefaultLogQueryWarmHours          = 48
	DefaultLogQueryWarmSleepMs        = 200
	// DefaultLogQueryTimeoutMs 是兜底闸门，不是性能目标：只掐真正失控的查询，
	// 避免把带筛选但仍然可用的慢查询误杀。
	DefaultLogQueryTimeoutMs = 30000

	MaxLogQueryStatCacheTTLHours  = 8760 // 1 年
	MaxLogQueryCurrentHourTTLSec  = 300
	MaxLogQueryStatMaxCachedHours = 8760
	MaxLogQueryStatMemoryEntries  = 1000000
	MaxLogQueryWarmIntervalSec    = 3600
	MinLogQueryWarmIntervalSec    = 60
	MaxLogQueryWarmHours          = 720
	MaxLogQueryWarmSleepMs        = 10000
	MaxLogQueryTimeoutMs          = 60000
	MinLogQueryTimeoutMs          = 1000
)

var logQuerySetting = LogQuerySetting{
	StatCacheEnabled:   true,
	StatCacheTTLHours:  DefaultLogQueryStatCacheTTLHours,
	CurrentHourTTLSec:  DefaultLogQueryCurrentHourTTLSec,
	StatMaxCachedHours: DefaultLogQueryStatMaxCachedHours,
	StatMemoryEntries:  DefaultLogQueryStatMemoryEntries,

	WarmEnabled:     true,
	WarmIntervalSec: DefaultLogQueryWarmIntervalSec,
	WarmHours:       DefaultLogQueryWarmHours,
	WarmSleepMs:     DefaultLogQueryWarmSleepMs,

	QueryTimeoutMs: DefaultLogQueryTimeoutMs,
}

func init() {
	config.GlobalConfig.Register("log_query_setting", &logQuerySetting)
}

// GetLogQuerySetting 返回全局配置。调用方不得跨请求缓存该指针。
func GetLogQuerySetting() *LogQuerySetting {
	return &logQuerySetting
}

func (s *LogQuerySetting) GetStatCacheTTL() time.Duration {
	hours := s.StatCacheTTLHours
	if hours <= 0 {
		hours = DefaultLogQueryStatCacheTTLHours
	}
	return time.Duration(clampInt(hours, 1, MaxLogQueryStatCacheTTLHours)) * time.Hour
}

// GetCurrentHourTTL 允许为 0：那表示当前整点每次都实时查，不走缓存。
func (s *LogQuerySetting) GetCurrentHourTTL() time.Duration {
	if s.CurrentHourTTLSec < 0 {
		return time.Duration(DefaultLogQueryCurrentHourTTLSec) * time.Second
	}
	return time.Duration(clampInt(s.CurrentHourTTLSec, 0, MaxLogQueryCurrentHourTTLSec)) * time.Second
}

func (s *LogQuerySetting) GetStatMaxCachedHours() int {
	if s.StatMaxCachedHours <= 0 {
		return DefaultLogQueryStatMaxCachedHours
	}
	return clampInt(s.StatMaxCachedHours, 1, MaxLogQueryStatMaxCachedHours)
}

func (s *LogQuerySetting) GetStatMemoryEntries() int {
	if s.StatMemoryEntries <= 0 {
		return DefaultLogQueryStatMemoryEntries
	}
	return clampInt(s.StatMemoryEntries, 64, MaxLogQueryStatMemoryEntries)
}

func (s *LogQuerySetting) GetWarmInterval() time.Duration {
	sec := s.WarmIntervalSec
	if sec <= 0 {
		sec = DefaultLogQueryWarmIntervalSec
	}
	return time.Duration(clampInt(sec, MinLogQueryWarmIntervalSec, MaxLogQueryWarmIntervalSec)) * time.Second
}

// GetWarmHours 允许为 0：那表示启动时不做向前补齐，只预热此后新完成的整点。
func (s *LogQuerySetting) GetWarmHours() int {
	if s.WarmHours < 0 {
		return DefaultLogQueryWarmHours
	}
	return clampInt(s.WarmHours, 0, MaxLogQueryWarmHours)
}

// GetWarmSleep 允许为 0：补齐时不休眠。
func (s *LogQuerySetting) GetWarmSleep() time.Duration {
	if s.WarmSleepMs < 0 {
		return time.Duration(DefaultLogQueryWarmSleepMs) * time.Millisecond
	}
	return time.Duration(clampInt(s.WarmSleepMs, 0, MaxLogQueryWarmSleepMs)) * time.Millisecond
}

func (s *LogQuerySetting) GetQueryTimeout() time.Duration {
	ms := s.QueryTimeoutMs
	if ms <= 0 {
		ms = DefaultLogQueryTimeoutMs
	}
	return time.Duration(clampInt(ms, MinLogQueryTimeoutMs, MaxLogQueryTimeoutMs)) * time.Millisecond
}
