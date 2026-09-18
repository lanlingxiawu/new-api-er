package settingsaccess

import (
	"testing"

	"github.com/QuantumNous/new-api/service/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 不属于系统设置页分区的作用域：权限映射在 middleware/system_settings_auth.go 里单独指定。
var nonSystemSettingsScopes = []string{
	ScopeChannelProfitPreview,
	ScopeVeridropDetection,
	ScopeCommissionTierReset,
}

func TestRegistryCoversEverySystemSettingsScope(t *testing.T) {
	for _, scope := range authz.SystemSettingsScopes() {
		_, ok := Resolve(scope)
		assert.True(t, ok, "scope %s must have an access definition", scope)
	}
	for _, scope := range nonSystemSettingsScopes {
		_, ok := Resolve(scope)
		assert.True(t, ok, "scope %s must have an access definition", scope)
	}
	assert.Len(t, Scopes(), len(authz.SystemSettingsScopes())+len(nonSystemSettingsScopes),
		"每个作用域要么是系统设置分区，要么登记在 nonSystemSettingsScopes 里")
}

func TestOptionKeyAllowlist(t *testing.T) {
	assert.True(t, AllowsOption("site.notice", "Notice"))
	assert.False(t, AllowsOption("site.notice", "SystemName"))
	assert.True(t, AllowsOption("operations.performance", "performance_setting.monitor_enabled"))
	assert.False(t, AllowsOption("operations.performance", "SMTPToken"))
	assert.False(t, AllowsOption("unknown.scope", "Notice"))
	assert.False(t, AllowsOption("billing.model-pricing", "price_monitor_setting.enabled"))
	assert.False(t, AllowsOption("billing.model-pricing", "price_monitor_setting.model_whitelist"))
}

// 审计日志保留期由 ConfigManager 支撑，必须用 configScope 登记：
// 普通 scope() 的 GroupKeys 是空 map，AllowsGroup 会一律拒绝，
// 设置页既读不出也存不进。
func TestAuditLogRetentionScope(t *testing.T) {
	assert.True(t, AllowsOption("operations.logs", "audit_log_setting.retention_days"))
	// 该分区原有的键不能因为换成 configScope 丢掉。
	assert.True(t, AllowsOption("operations.logs", "LogConsumeEnabled"))

	assert.True(t, AllowsGroup("operations.logs", "audit_log_setting",
		map[string]string{"retention_days": "180"}))
	assert.False(t, AllowsGroup("operations.logs", "audit_log_setting",
		map[string]string{"retention_days": "180", "unknown_key": "1"}))
	assert.False(t, AllowsGroup("operations.logs", "log_query_setting",
		map[string]string{"query_timeout_ms": "1000"}))
}

func TestChannelProfitPreviewOnlyExposesGroupRatio(t *testing.T) {
	keys, ok := OptionKeys(ScopeChannelProfitPreview)
	require.True(t, ok)
	assert.Equal(t, map[string]struct{}{"GroupRatio": {}}, keys)
	assert.True(t, AllowsOption(ScopeChannelProfitPreview, "GroupRatio"))
	assert.False(t, AllowsOption(ScopeChannelProfitPreview, "TopupGroupRatio"))
}

func TestOptionKeysReturnsCopy(t *testing.T) {
	keys, ok := OptionKeys("site.system-info")
	require.True(t, ok)
	require.NotEmpty(t, keys)
	keys["tampered"] = struct{}{}
	assert.False(t, AllowsOption("site.system-info", "tampered"))
}

func TestConfigGroupAllowlistRejectsPartialSmuggling(t *testing.T) {
	valid := map[string]string{
		"max_idle_conns": "10",
		"max_open_conns": "100",
	}
	assert.True(t, AllowsGroup("system-tuning.database-pool", "db_pool_setting", valid))

	invalid := map[string]string{
		"max_idle_conns": "10",
		"active_limit":   "1",
	}
	assert.False(t, AllowsGroup("system-tuning.database-pool", "db_pool_setting", invalid))
	assert.False(t, AllowsGroup("system-tuning.database-pool", "user_session_setting", valid))
	assert.False(t, AllowsGroup("unknown.scope", "db_pool_setting", valid))
	assert.False(t, AllowsGroup("system-tuning.database-pool", "db_pool_setting", nil))
	assert.True(t, AllowsGroup("system-tuning.relay-timeout", "relay_timeout_setting", map[string]string{
		"enabled": "true", "response_timeout_seconds": "300", "total_timeout_seconds": "0",
	}))
	assert.False(t, AllowsGroup("system-tuning.relay-timeout", "relay_timeout_setting", map[string]string{
		"enabled": "true", "critical_num": "1",
	}))
}

func TestVeridropScopeOnlyAllowsVeridropConfiguration(t *testing.T) {
	assert.True(t, AllowsOption(ScopeVeridropDetection, "veridrop_monitor_setting.enabled"))
	assert.True(t, AllowsOption(ScopeVeridropDetection, "veridrop_monitor_setting.detection_interval_minutes"))
	assert.False(t, AllowsOption(ScopeVeridropDetection, "rate_limit_setting.global_api_enabled"))
	assert.True(t, AllowsGroup(ScopeVeridropDetection, "veridrop_monitor_setting", map[string]string{
		"enabled":  "true",
		"base_url": "https://veridrop.example",
	}))
	assert.False(t, AllowsGroup(ScopeVeridropDetection, "veridrop_monitor_setting", map[string]string{
		"enabled":        "true",
		"max_open_conns": "100",
	}))
	assert.False(t, AllowsGroup(ScopeVeridropDetection, "db_pool_setting", map[string]string{"max_open_conns": "100"}))
}
