package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// BusinessStatsCircuitBreakerSetting controls the post-settlement business
// stats side path: consumption costs, employee commission ledgers, and their
// aggregate buffers.
type BusinessStatsCircuitBreakerSetting struct {
	Enabled                bool  `json:"enabled"`
	ManualDisabled         bool  `json:"manual_disabled"`
	FailureThreshold       int   `json:"failure_threshold"`
	InitialCooldownSeconds int64 `json:"initial_cooldown_seconds"`
	MaxCooldownSeconds     int64 `json:"max_cooldown_seconds"`
	SideEffectDBTimeoutMs  int   `json:"side_effect_db_timeout_ms"`
}

var businessStatsCircuitBreakerSetting = BusinessStatsCircuitBreakerSetting{
	Enabled:                true,
	ManualDisabled:         false,
	FailureThreshold:       3,
	InitialCooldownSeconds: 60,
	MaxCooldownSeconds:     3600,
	SideEffectDBTimeoutMs:  800,
}

func init() {
	config.GlobalConfig.Register("business_stats_circuit_breaker_setting", &businessStatsCircuitBreakerSetting)
}

func GetBusinessStatsCircuitBreakerSetting() *BusinessStatsCircuitBreakerSetting {
	return &businessStatsCircuitBreakerSetting
}
