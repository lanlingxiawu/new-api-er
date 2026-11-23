package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTargetedFixesSQLite(t *testing.T) {
	originalDB, originalLogDB := DB, LOG_DB
	originalType, originalLogType := common.MainDatabaseType(), common.LogDatabaseType()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	connection, err := database.DB()
	require.NoError(t, err)
	connection.SetMaxOpenConns(1)
	DB, LOG_DB = database, database
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	initCol()
	t.Cleanup(func() {
		DB, LOG_DB = originalDB, originalLogDB
		common.SetDatabaseTypes(originalType, originalLogType)
		initCol()
		require.NoError(t, connection.Close())
	})
	require.NoError(t, database.AutoMigrate(&User{}, &Channel{}, &EmployeeProfile{}, &EmployeeCommissionTier{}, &EmployeeTierLevel{}, &EmployeeTierLog{}, &EmployeeCommissionResetPeriodDailyStat{}, &PlatformChannelDailyStat{}, &EmployeeCommissionDailyStat{}, &EmployeeCustomerCommissionDailyStat{}, &EmployeeCommissionLog{}, &ConsumptionCost{}))
	t.Run("rounding", TestCommissionExportRoundsAggregate)
	t.Run("pagination", TestChannelStatsPaginationStableTies)
	t.Run("newer-baseline", TestSwitchPeriodResetIncludesNewerBaseline)
	t.Run("baseline-and-orphans", TestSwitchPeriodResetAdvancesBaselineAndPreservesOrphan)
	t.Run("batch-concurrency-retry", TestSwitchPeriodResetBatchAndConcurrentUpdate)
}
