package operation_setting

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

const (
	rateLimitConfigName   = "rate_limit_setting"
	defaultGlobalAPINum   = 2000
	maxRateLimitNum       = 100_000
	maxRateLimitWindowSec = 1200

	// 限流查 Redis 的超时预算。限流是可降级的旁路，Redis 不可用时降级到本地内存
	// 计数即可，请求不该跟着 go-redis 的默认超时（DialTimeout 5s / ReadTimeout 3s /
	// 重试 3 次）一起等下去。
	//
	// 默认 100ms：健康时这条调用是亚毫秒级的，100ms 有约两个数量级的余量，不会因为
	// 正常抖动误降级；同时把最坏情况从 12s 压到 100ms。
	// 下界 5ms 防止配得过小导致 Redis 稍有延迟就全量降级；
	// 上界 5000ms 对齐 go-redis 的 DialTimeout，再大就没有意义了。
	defaultRateLimitRedisTimeoutMs = 100
	minRateLimitRedisTimeoutMs     = 5
	maxRateLimitRedisTimeoutMs     = 5_000
)

type RateLimitSetting struct {
	GlobalAPIEnabled         bool `json:"global_api_enabled"`
	GlobalAPINum             int  `json:"global_api_num"`
	GlobalAPIDurationSec     int  `json:"global_api_duration_sec"`
	GlobalAPIUserEnabled     bool `json:"global_api_user_enabled"`
	GlobalAPIUserNum         int  `json:"global_api_user_num"`
	GlobalAPIUserDurationSec int  `json:"global_api_user_duration_sec"`
	GlobalWebEnabled         bool `json:"global_web_enabled"`
	GlobalWebNum             int  `json:"global_web_num"`
	GlobalWebDurationSec     int  `json:"global_web_duration_sec"`
	CriticalEnabled          bool `json:"critical_enabled"`
	CriticalNum              int  `json:"critical_num"`
	CriticalDurationSec      int  `json:"critical_duration_sec"`
	AuthRefreshEnabled       bool `json:"auth_refresh_enabled"`
	AuthRefreshNum           int  `json:"auth_refresh_num"`
	AuthRefreshIPNum         int  `json:"auth_refresh_ip_num"`
	AuthRefreshDurationSec   int  `json:"auth_refresh_duration_sec"`
	SearchEnabled            bool `json:"search_enabled"`
	SearchNum                int  `json:"search_num"`
	SearchDurationSec        int  `json:"search_duration_sec"`
	LogExportEnabled         bool `json:"log_export_enabled"`
	LogExportNum             int  `json:"log_export_num"`
	LogExportDurationSec     int  `json:"log_export_duration_sec"`
	RedisTimeoutMs           int  `json:"redis_timeout_ms"`
}

type RateLimitBucket struct {
	Enabled  bool
	Num      int
	Duration int64
}
type AuthRefreshBucket struct {
	RateLimitBucket
	IPNum int
}
type RateLimitSnapshot struct {
	GlobalAPI, GlobalAPIUser, GlobalWeb, Critical, Search, LogExport RateLimitBucket
	AuthRefresh                                                      AuthRefreshBucket
	// RedisTimeout 是单次限流查询允许消耗的最长时间，超时即降级到本地内存计数。
	RedisTimeout time.Duration
}

var rateLimitSetting = RateLimitSetting{
	GlobalAPIEnabled: true, GlobalAPINum: defaultGlobalAPINum, GlobalAPIDurationSec: 180,
	GlobalAPIUserEnabled: true, GlobalAPIUserNum: 360, GlobalAPIUserDurationSec: 180,
	GlobalWebEnabled: true, GlobalWebNum: 2000, GlobalWebDurationSec: 180,
	CriticalEnabled: true, CriticalNum: 60, CriticalDurationSec: 1200,
	AuthRefreshEnabled: true, AuthRefreshNum: 60, AuthRefreshIPNum: 600, AuthRefreshDurationSec: 1200,
	SearchEnabled: true, SearchNum: 10, SearchDurationSec: 60,
	LogExportEnabled: true, LogExportNum: 1, LogExportDurationSec: 600,
	RedisTimeoutMs: defaultRateLimitRedisTimeoutMs,
}
var rateLimitSnapshot atomic.Pointer[RateLimitSnapshot]

func init() {
	config.GlobalConfig.Register(rateLimitConfigName, &rateLimitSetting)
	PublishRateLimitSetting()
}

func clamp(v, fallback, max int) int {
	if v <= 0 {
		return fallback
	}
	if v > max {
		return max
	}
	return v
}
func bucket(enabled bool, num, duration, fallbackNum, fallbackDuration int) RateLimitBucket {
	return RateLimitBucket{enabled, clamp(num, fallbackNum, maxRateLimitNum), int64(clamp(duration, fallbackDuration, maxRateLimitWindowSec))}
}

// clampRedisTimeoutMs 与 clamp 的差别在于有下界：配成 1ms 应当被抬到下界，
// 而不是回落到默认值——否则运维想调紧却得到一个更松的值。
func clampRedisTimeoutMs(value int) int {
	if value <= 0 {
		return defaultRateLimitRedisTimeoutMs
	}
	if value < minRateLimitRedisTimeoutMs {
		return minRateLimitRedisTimeoutMs
	}
	if value > maxRateLimitRedisTimeoutMs {
		return maxRateLimitRedisTimeoutMs
	}
	return value
}

func PublishRateLimitSetting() {
	s := GetRateLimitSetting()
	snap := RateLimitSnapshot{
		GlobalAPI: bucket(s.GlobalAPIEnabled, s.GlobalAPINum, s.GlobalAPIDurationSec, 2000, 180), GlobalAPIUser: bucket(s.GlobalAPIUserEnabled, s.GlobalAPIUserNum, s.GlobalAPIUserDurationSec, 360, 180),
		GlobalWeb: bucket(s.GlobalWebEnabled, s.GlobalWebNum, s.GlobalWebDurationSec, 2000, 180), Critical: bucket(s.CriticalEnabled, s.CriticalNum, s.CriticalDurationSec, 60, 1200),
		Search: bucket(s.SearchEnabled, s.SearchNum, s.SearchDurationSec, 10, 60), LogExport: bucket(s.LogExportEnabled, s.LogExportNum, s.LogExportDurationSec, 1, 600),
	}
	snap.AuthRefresh = AuthRefreshBucket{RateLimitBucket: bucket(s.AuthRefreshEnabled, s.AuthRefreshNum, s.AuthRefreshDurationSec, 60, 1200), IPNum: clamp(s.AuthRefreshIPNum, 600, maxRateLimitNum)}
	snap.RedisTimeout = time.Duration(clampRedisTimeoutMs(s.RedisTimeoutMs)) * time.Millisecond
	rateLimitSnapshot.Store(&snap)
}
func GetRateLimitSnapshot() *RateLimitSnapshot { return rateLimitSnapshot.Load() }

// GetRateLimitSetting / ReplaceRateLimitSetting take the config draft mutex:
// LoadFromDB / SaveToDB / ExportAllConfigs read and write these same fields by
// reflection, so wholesale replacement has to serialize against them. Request
// paths never come here — they read the atomic snapshot.
func GetRateLimitSetting() RateLimitSetting {
	var out RateLimitSetting
	config.WithConfigDraft(func() { out = rateLimitSetting })
	return out
}
func ReplaceRateLimitSetting(s RateLimitSetting) {
	config.WithConfigDraft(func() { rateLimitSetting = s })
	PublishRateLimitSetting()
}
func ValidateRateLimitSetting(s RateLimitSetting) error {
	checks := []struct{ num, duration int }{{s.GlobalAPINum, s.GlobalAPIDurationSec}, {s.GlobalAPIUserNum, s.GlobalAPIUserDurationSec}, {s.GlobalWebNum, s.GlobalWebDurationSec}, {s.CriticalNum, s.CriticalDurationSec}, {s.AuthRefreshNum, s.AuthRefreshDurationSec}, {s.SearchNum, s.SearchDurationSec}, {s.LogExportNum, s.LogExportDurationSec}}
	for _, check := range checks {
		if check.num < 1 || check.num > maxRateLimitNum || check.duration < 1 || check.duration > maxRateLimitWindowSec {
			return fmt.Errorf("rate limit value out of range")
		}
	}
	if s.AuthRefreshIPNum < 1 || s.AuthRefreshIPNum > maxRateLimitNum {
		return fmt.Errorf("rate limit value out of range")
	}
	if s.RedisTimeoutMs < minRateLimitRedisTimeoutMs || s.RedisTimeoutMs > maxRateLimitRedisTimeoutMs {
		return fmt.Errorf("redis timeout must be between %dms and %dms",
			minRateLimitRedisTimeoutMs, maxRateLimitRedisTimeoutMs)
	}
	return nil
}
func ApplyRateLimitEnvDefaults() {
	s := &rateLimitSetting
	s.GlobalAPIEnabled = common.GetEnvOrDefaultBool("GLOBAL_API_RATE_LIMIT_ENABLE", s.GlobalAPIEnabled)
	s.GlobalAPINum = common.GetEnvOrDefault("GLOBAL_API_RATE_LIMIT", s.GlobalAPINum)
	s.GlobalAPIDurationSec = common.GetEnvOrDefault("GLOBAL_API_RATE_LIMIT_DURATION", s.GlobalAPIDurationSec)
	s.GlobalAPIUserEnabled = common.GetEnvOrDefaultBool("GLOBAL_API_USER_RATE_LIMIT_ENABLE", s.GlobalAPIUserEnabled)
	s.GlobalAPIUserNum = common.GetEnvOrDefault("GLOBAL_API_USER_RATE_LIMIT", s.GlobalAPIUserNum)
	s.GlobalAPIUserDurationSec = common.GetEnvOrDefault("GLOBAL_API_USER_RATE_LIMIT_DURATION", s.GlobalAPIUserDurationSec)
	s.GlobalWebEnabled = common.GetEnvOrDefaultBool("GLOBAL_WEB_RATE_LIMIT_ENABLE", s.GlobalWebEnabled)
	s.GlobalWebNum = common.GetEnvOrDefault("GLOBAL_WEB_RATE_LIMIT", s.GlobalWebNum)
	s.GlobalWebDurationSec = common.GetEnvOrDefault("GLOBAL_WEB_RATE_LIMIT_DURATION", s.GlobalWebDurationSec)
	s.CriticalEnabled = common.GetEnvOrDefaultBool("CRITICAL_RATE_LIMIT_ENABLE", s.CriticalEnabled)
	s.CriticalNum = common.GetEnvOrDefault("CRITICAL_RATE_LIMIT", s.CriticalNum)
	s.CriticalDurationSec = common.GetEnvOrDefault("CRITICAL_RATE_LIMIT_DURATION", s.CriticalDurationSec)
	s.AuthRefreshEnabled = common.GetEnvOrDefaultBool("AUTH_REFRESH_RATE_LIMIT_ENABLE", s.AuthRefreshEnabled)
	s.AuthRefreshNum = common.GetEnvOrDefault("AUTH_REFRESH_RATE_LIMIT", s.AuthRefreshNum)
	s.AuthRefreshIPNum = common.GetEnvOrDefault("AUTH_REFRESH_RATE_LIMIT_IP", s.AuthRefreshIPNum)
	s.AuthRefreshDurationSec = common.GetEnvOrDefault("AUTH_REFRESH_RATE_LIMIT_DURATION", s.AuthRefreshDurationSec)
	s.SearchEnabled = common.GetEnvOrDefaultBool("SEARCH_RATE_LIMIT_ENABLE", s.SearchEnabled)
	s.SearchNum = common.GetEnvOrDefault("SEARCH_RATE_LIMIT", s.SearchNum)
	s.SearchDurationSec = common.GetEnvOrDefault("SEARCH_RATE_LIMIT_DURATION", s.SearchDurationSec)
	s.LogExportEnabled = common.GetEnvOrDefaultBool("LOG_EXPORT_RATE_LIMIT_ENABLE", s.LogExportEnabled)
	s.LogExportNum = common.GetEnvOrDefault("LOG_EXPORT_RATE_LIMIT", s.LogExportNum)
	s.LogExportDurationSec = common.GetEnvOrDefault("LOG_EXPORT_RATE_LIMIT_DURATION", s.LogExportDurationSec)
	s.RedisTimeoutMs = common.GetEnvOrDefault("RATE_LIMIT_REDIS_TIMEOUT_MS", s.RedisTimeoutMs)
	PublishRateLimitSetting()
}
