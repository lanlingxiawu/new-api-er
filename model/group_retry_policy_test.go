package model

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestGroupRetryPolicyDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
			case "mysql":
				if os.Getenv("TEST_MYSQL_DSN") == "" {
					t.Skip("TEST_MYSQL_DSN not configured")
				}
				driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
			case "postgres":
				if os.Getenv("TEST_POSTGRES_DSN") == "" {
					t.Skip("TEST_POSTGRES_DSN not configured")
				}
				driver = postgres.Open(os.Getenv("TEST_POSTGRES_DSN"))
			}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: fmt.Sprintf("retry_%d_", time.Now().UnixNano())}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			previousDB, previousType := DB, common.MainDatabaseType()
			common.OptionMapRWMutex.Lock()
			previousOptions := common.OptionMap
			common.OptionMap = make(map[string]string)
			common.OptionMapRWMutex.Unlock()
			oldCounts, oldStatus := operation_setting.GroupRetryTimes2JSONString(), operation_setting.GetGroupRetryStatusSetting()
			oldGuard := operation_setting.GetGroupRetryGuardSetting()
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			initCol()
			t.Cleanup(func() {
				require.NoError(t, db.Migrator().DropTable(&Option{}))
				require.NoError(t, sqlDB.Close())
				DB = previousDB
				common.SetMainDatabaseType(previousType)
				initCol()
				common.OptionMapRWMutex.Lock()
				common.OptionMap = previousOptions
				common.OptionMapRWMutex.Unlock()
				require.NoError(t, operation_setting.UpdateGroupRetryTimesByJSONString(oldCounts))
				operation_setting.ReplaceGroupRetryStatusSetting(oldStatus)
				operation_setting.ReplaceGroupRetryGuardSetting(oldGuard)
			})
			require.NoError(t, db.AutoMigrate(&Option{}))
			applied, err := SaveGroupRetryPolicy(`{"default":2}`, map[string]string{"enabled": "true", "rules": `{"default":"429"}`})
			require.NoError(t, err)
			require.True(t, applied)
			for _, key := range []string{"GroupRetryTimes", "group_retry_status_setting.enabled", "group_retry_status_setting.rules"} {
				value, exists := optionRowValue(t, db, key)
				require.True(t, exists)
				require.Equal(t, value, common.OptionMap[key])
			}
			for _, invalid := range []struct{ counts, rules string }{
				{`{"default":21}`, `{"default":"500"}`},
				{`{"default":-2}`, `{"default":"500"}`},
				{`{"default":3}`, `{"default":"600"}`},
			} {
				applied, err = SaveGroupRetryPolicy(invalid.counts, map[string]string{"enabled": "false", "rules": invalid.rules})
				require.Error(t, err)
				require.False(t, applied)
				value, _ := optionRowValue(t, db, "GroupRetryTimes")
				require.Equal(t, `{"default":2}`, value)
				require.True(t, operation_setting.GetGroupRetryStatusSetting().Enabled)
			}
			failure := errors.New("simulated option update failure")
			updates := 0
			require.NoError(t, db.Callback().Update().After("gorm:update").Register("test:retry_rollback", func(tx *gorm.DB) {
				updates++
				if updates == 2 {
					tx.AddError(failure)
				}
			}))
			applied, err = SaveGroupRetryPolicy(`{"default":3}`, map[string]string{"enabled": "false", "rules": `{"default":"500"}`})
			require.ErrorIs(t, err, failure)
			require.False(t, applied)
			require.NoError(t, db.Callback().Update().Remove("test:retry_rollback"))
			value, _ := optionRowValue(t, db, "GroupRetryTimes")
			require.Equal(t, `{"default":2}`, value)
			value, _ = optionRowValue(t, db, "group_retry_status_setting.rules")
			require.Equal(t, `{"default":"429"}`, value)
			require.Equal(t, `{"default":2}`, operation_setting.GroupRetryTimes2JSONString())
			require.True(t, operation_setting.GetGroupRetryStatusSetting().Enabled)
			applied, err = SaveGroupRetryPolicy(`{"default":-1}`, map[string]string{"enabled": "false", "rules": `{"default":"429"}`})
			require.NoError(t, err)
			require.True(t, applied)
			_, overridden := operation_setting.GroupRetryStatusAllowed("default", 429)
			require.False(t, overridden)
			count, exists := operation_setting.GetGroupRetryTimes("default")
			require.True(t, exists)
			require.Equal(t, -1, count)
			require.Equal(t, `{"default":"429"}`, operation_setting.GetGroupRetryStatusSetting().Rules)
			payload := map[string]string{"times": `{"default":10}`, "status_enabled": "true", "status_rules": `{"default":"429"}`, "guard_enabled": "true", "guard_rules": "{}"}
			applied, err = SaveConfigGroup("group_retry_policy", payload)
			require.NoError(t, err)
			require.True(t, applied)
			var beforeRows []Option
			require.NoError(t, db.Order(commonKeyCol).Find(&beforeRows).Error)
			require.Len(t, beforeRows, 6)
			beforeGuard := operation_setting.GetGroupRetryGuardSetting()
			updates = 0
			require.NoError(t, db.Callback().Update().After("gorm:update").Register("test:retry_guard_rollback", func(tx *gorm.DB) {
				updates++
				if updates == 2 {
					tx.AddError(failure)
				}
			}))
			payload["times"] = `{"default":3}`
			payload["guard_enabled"] = "false"
			applied, err = SaveConfigGroup("group_retry_policy", payload)
			require.ErrorIs(t, err, failure)
			require.False(t, applied)
			require.NoError(t, db.Callback().Update().Remove("test:retry_guard_rollback"))
			var afterRows []Option
			require.NoError(t, db.Order(commonKeyCol).Find(&afterRows).Error)
			require.Equal(t, beforeRows, afterRows)
			require.Equal(t, beforeGuard, operation_setting.GetGroupRetryGuardSetting())
			require.Equal(t, `{"default":10}`, operation_setting.GroupRetryTimes2JSONString())
		})
	}
}

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
	require.NoError(t, db.Order(commonKeyCol).Find(&beforeRows).Error)
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
	require.NoError(t, db.Order(commonKeyCol).Find(&afterRows).Error)
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
