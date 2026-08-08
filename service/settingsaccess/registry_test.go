package settingsaccess

import (
	"testing"

	"github.com/QuantumNous/new-api/service/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryCoversEverySystemSettingsScope(t *testing.T) {
	for _, scope := range authz.SystemSettingsScopes() {
		_, ok := Resolve(scope)
		assert.True(t, ok, "scope %s must have an access definition", scope)
	}
	assert.Len(t, Scopes(), len(authz.SystemSettingsScopes())+1)
}

func TestOptionKeyAllowlist(t *testing.T) {
	assert.True(t, AllowsOption("site.notice", "Notice"))
	assert.False(t, AllowsOption("site.notice", "SystemName"))
	assert.True(t, AllowsOption("operations.performance", "performance_setting.monitor_enabled"))
	assert.False(t, AllowsOption("operations.performance", "SMTPToken"))
	assert.False(t, AllowsOption("unknown.scope", "Notice"))
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
