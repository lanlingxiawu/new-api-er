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
		})
	}
}
