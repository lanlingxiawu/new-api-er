package settingsaccess

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/performance_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPerformanceScopeAllowsGroupSave(t *testing.T) {
	assert.True(t, AllowsGroup("operations.performance", "performance_setting", map[string]string{
		"body_governance_enabled": "true",
		"body_memory_budget_mb":   "128",
		"disk_cache_enabled":      "true",
	}))

	values := map[string]string{
		"body_governance_enabled":  "true",
		"body_memory_budget_mb":    "128",
		"disk_cache_enabled":       "true",
		"disk_cache_threshold_mb":  "10",
		"disk_cache_max_size_mb":   "1024",
		"disk_cache_path":          "",
		"monitor_enabled":          "true",
		"monitor_cpu_threshold":    "90",
		"monitor_memory_threshold": "90",
		"monitor_disk_threshold":   "95",
	}
	assert.True(t, AllowsGroup("operations.performance", "performance_setting", values))
	draft, err := performance_setting.ParsePerformanceUpdate(values)
	require.NoError(t, err)
	assert.True(t, draft.BodyGovernanceEnabled)
	assert.Equal(t, 128, draft.BodyMemoryBudgetMB)
	assert.True(t, draft.DiskCacheEnabled)
	assert.Equal(t, 1024, draft.DiskCacheMaxSizeMB)

	assert.False(t, AllowsGroup("operations.performance", "performance_setting", map[string]string{
		"body_governance_enabled": "true",
		"unknown_key":             "1",
	}))
	assert.False(t, AllowsGroup("operations.performance", "relay_timeout_setting", map[string]string{
		"enabled": "true",
	}))
}
