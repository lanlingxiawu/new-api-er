package model

import (
	"github.com/QuantumNous/new-api/setting/performance_setting"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestBodyGovernanceConfigPersistence(t *testing.T) {
	db := useConfigGroupDB(t)
	old := *performance_setting.GetPerformanceSetting()
	defer performance_setting.ReplacePerformanceSetting(old)
	applied, err := SaveConfigGroup("performance_setting", map[string]string{"body_governance_enabled": "true", "body_memory_budget_mb": "8"})
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, 8, performance_setting.GetPerformanceSetting().BodyMemoryBudgetMB)
	require.Error(t, UpdateOption("performance_setting.body_memory_budget_mb", "0"))
	require.Error(t, UpdateOptionsBulk(map[string]string{"performance_setting.body_memory_budget_mb": "0"}))
	value, found := optionRowValue(t, db, "performance_setting.body_memory_budget_mb")
	require.True(t, found)
	require.Equal(t, "8", value)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	_, err = SaveConfigGroup("performance_setting", map[string]string{"body_memory_budget_mb": "16"})
	require.Error(t, err)
	require.Equal(t, 8, performance_setting.GetPerformanceSetting().BodyMemoryBudgetMB)
}
