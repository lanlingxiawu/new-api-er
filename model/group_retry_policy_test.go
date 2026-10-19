package model

import (
	"errors"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestGroupRetryPolicyAtomicSave(t *testing.T) {
	// Live option keys use the established isolated config fixture; generic
	// persistOptionsTx dialect coverage uses row-scoped keys on the project DB.
	db := useConfigGroupDB(t)
	counts := operation_setting.GroupRetryTimes2JSONString()
	statuses := operation_setting.GetGroupRetryStatusSetting()
	guard := operation_setting.GetGroupRetryGuardSetting()
	t.Cleanup(func() {
		operation_setting.UpdateGroupRetryTimesByJSONString(counts)
		operation_setting.ReplaceGroupRetryStatusSetting(statuses)
		operation_setting.ReplaceGroupRetryGuardSetting(guard)
	})
	payload := map[string]string{"times": `{"g":1}`, "status_enabled": "true", "status_rules": `{"g":"429"}`, "guard_enabled": "false", "guard_rules": "{}"}
	applied, err := SaveConfigGroup("group_retry_policy", payload)
	require.NoError(t, err)
	require.True(t, applied)
	for _, module := range []string{"group_retry_policy", "group_retry_times"} {
		invalid := map[string]string{"times": `{"g":11}`}
		if module == "group_retry_policy" {
			for key, value := range payload {
				if key != "times" {
					invalid[key] = value
				}
			}
		}
		applied, err = SaveConfigGroup(module, invalid)
		require.Error(t, err)
		require.False(t, applied)
		stored, ok := optionRowValue(t, db, "GroupRetryTimes")
		require.True(t, ok)
		require.Equal(t, `{"g":1}`, stored)
	}
	for _, field := range []string{"times", "status_rules", "guard_rules", "guard_enabled"} {
		previous := payload[field]
		payload[field] = "null"
		applied, err = SaveConfigGroup("group_retry_policy", payload)
		require.Error(t, err)
		require.False(t, applied)
		stored, ok := optionRowValue(t, db, "GroupRetryTimes")
		require.True(t, ok)
		require.Equal(t, `{"g":1}`, stored)
		payload[field] = previous
	}
	payload["times"] = `{"g":2}`
	payload["status_enabled"] = "false"
	payload["status_rules"] = `{"g":"500"}`
	payload["guard_enabled"] = "true"
	var beforeRows []Option
	require.NoError(t, db.Order("key").Find(&beforeRows).Error)
	beforeGuard := operation_setting.GetGroupRetryGuardSetting()
	writes := 0
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("retry_policy_failure", func(tx *gorm.DB) {
		writes++
		if writes == 2 {
			tx.AddError(errors.New("injected write failure"))
		}
	}))
	applied, err = SaveConfigGroup("group_retry_policy", payload)
	require.Error(t, err)
	require.False(t, applied)
	require.NoError(t, db.Callback().Update().Remove("retry_policy_failure"))
	var afterRows []Option
	require.NoError(t, db.Order("key").Find(&afterRows).Error)
	require.Equal(t, beforeRows, afterRows)
	require.Equal(t, beforeGuard, operation_setting.GetGroupRetryGuardSetting())
	require.True(t, operation_setting.GetGroupRetryStatusSetting().Enabled)
	stored, ok := optionRowValue(t, db, "GroupRetryTimes")
	require.True(t, ok)
	require.Equal(t, `{"g":1}`, stored)
	live, ok := operation_setting.GetGroupRetryTimes("g")
	require.True(t, ok)
	require.Equal(t, 1, live)
	require.NoError(t, UpdateOption("GroupRetryTimes", `{"g":3}`))
	live, _ = operation_setting.GetGroupRetryTimes("g")
	require.Equal(t, 3, live)
	delete(payload, "times")
	_, err = SaveConfigGroup("group_retry_policy", payload)
	require.Error(t, err)
}

func TestGroupRetryGuardHotReloadKeepsValidSnapshot(t *testing.T) {
	useConfigGroupDB(t)
	old := operation_setting.GetGroupRetryGuardSetting()
	t.Cleanup(func() { operation_setting.ReplaceGroupRetryGuardSetting(old) })
	require.NoError(t, publishModelPricingOptions(map[string]string{
		"group_retry_guard_setting.enabled": "true", "group_retry_guard_setting.rules": "{}", "group_retry_guard_setting.revision": "revision-one",
	}))
	require.True(t, operation_setting.GroupRetryGuardSnapshot().Enabled)
	require.Equal(t, "revision-one", operation_setting.GetGroupRetryGuardSetting().Revision)
	require.Error(t, publishModelPricingOptions(map[string]string{
		"group_retry_guard_setting.enabled": "false", "group_retry_guard_setting.rules": "null", "group_retry_guard_setting.revision": "invalid",
	}))
	require.True(t, operation_setting.GroupRetryGuardSnapshot().Enabled)
	require.Equal(t, "revision-one", operation_setting.GetGroupRetryGuardSetting().Revision)
}
