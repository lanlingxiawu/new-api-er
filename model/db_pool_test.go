package model

import (
	"database/sql"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestApplyPoolIgnoresNilDatabase(t *testing.T) {
	require.NoError(t, applyPool(nil, 10, 100, time.Minute))
}

func TestApplyPoolPushesValuesToSQLDB(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:applypool?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)

	require.NoError(t, applyPool(db, 7, 21, 90*time.Second))

	sqlDB, err := db.DB()
	require.NoError(t, err)
	stats := sqlDB.Stats()
	assert.Equal(t, 21, stats.MaxOpenConnections)
}

// applyPool 里的 recover 不是装饰：db.DB() 在连接已关闭时的行为随驱动而异，
// 而这条路径会在启动和每次热更新时跑，panic 逃出去会直接打断启动。
func TestApplyPoolRecoversFromBrokenHandle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:applypoolbroken?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	require.NotPanics(t, func() { _ = applyPool(db, 1, 2, time.Second) })
}

func TestApplyDBPoolSettingUsesSnapshotValues(t *testing.T) {
	requireDB(t)
	before := operation_setting.GetDBPoolSetting()
	t.Cleanup(func() {
		operation_setting.ReplaceDBPoolSetting(before)
		_ = ApplyDBPoolSetting()
	})

	draft := before
	draft.MaxIdleConns = 11
	draft.MaxOpenConns = 111
	draft.MaxLifetimeSec = 300
	operation_setting.ReplaceDBPoolSetting(draft)

	require.NoError(t, ApplyDBPoolSetting())

	sqlDB, err := DB.DB()
	require.NoError(t, err)
	assert.Equal(t, 111, sqlDB.Stats().MaxOpenConnections)
}

// 日志库为 0 时继承主库设置；配了独立日志库时两者必须分别生效，
// 否则日志库的连接数会被主库的值覆盖。
func TestApplyDBPoolSettingSeparatesLogDatabase(t *testing.T) {
	requireDB(t)
	requireLogDB(t)
	if LOG_DB == DB {
		t.Skip("log DB shares the main handle; nothing to separate")
	}

	before := operation_setting.GetDBPoolSetting()
	t.Cleanup(func() {
		operation_setting.ReplaceDBPoolSetting(before)
		_ = ApplyDBPoolSetting()
	})

	draft := before
	draft.MaxIdleConns = 10
	draft.MaxOpenConns = 100
	draft.LogMaxIdleConns = 20
	draft.LogMaxOpenConns = 200
	operation_setting.ReplaceDBPoolSetting(draft)

	require.NoError(t, ApplyDBPoolSetting())

	mainDB, err := DB.DB()
	require.NoError(t, err)
	logDB, err := LOG_DB.DB()
	require.NoError(t, err)
	assert.Equal(t, 100, mainDB.Stats().MaxOpenConnections)
	assert.Equal(t, 200, logDB.Stats().MaxOpenConnections)
}

func TestGetDBPoolRuntimeStatsReportsSharedLogPoolOnce(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:poolstatsshared?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(37)

	previousDB, previousLogDB := DB, LOG_DB
	DB, LOG_DB = db, db
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		require.NoError(t, sqlDB.Close())
	})

	stats, err := GetDBPoolRuntimeStats()
	require.NoError(t, err)
	assert.Equal(t, 37, stats.Main.MaxOpenConnections)
	assert.Nil(t, stats.Log)
	assert.True(t, stats.LogReusesMain)
	assert.Positive(t, stats.SampledAt)
}

func TestGetDBPoolRuntimeStatsReportsDistinctPools(t *testing.T) {
	mainDB, err := gorm.Open(sqlite.Open("file:poolstatsmain?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	logDB, err := gorm.Open(sqlite.Open("file:poolstatslog?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	mainSQLDB, err := mainDB.DB()
	require.NoError(t, err)
	logSQLDB, err := logDB.DB()
	require.NoError(t, err)
	mainSQLDB.SetMaxOpenConns(41)
	logSQLDB.SetMaxOpenConns(17)

	previousDB, previousLogDB := DB, LOG_DB
	DB, LOG_DB = mainDB, logDB
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		require.NoError(t, mainSQLDB.Close())
		require.NoError(t, logSQLDB.Close())
	})

	stats, err := GetDBPoolRuntimeStats()
	require.NoError(t, err)
	assert.Equal(t, 41, stats.Main.MaxOpenConnections)
	require.NotNil(t, stats.Log)
	assert.Equal(t, 17, stats.Log.MaxOpenConnections)
	assert.False(t, stats.LogReusesMain)
}

func TestGetDBPoolRuntimeStatsRejectsMissingMainPool(t *testing.T) {
	previousDB, previousLogDB := DB, LOG_DB
	DB, LOG_DB = nil, nil
	t.Cleanup(func() { DB, LOG_DB = previousDB, previousLogDB })

	_, err := GetDBPoolRuntimeStats()
	require.Error(t, err)
}

func TestMapDBPoolStatsConvertsWaitDurationToMilliseconds(t *testing.T) {
	stats := mapDBPoolStats(sql.DBStats{
		MaxOpenConnections: 8,
		WaitDuration:       1500 * time.Microsecond,
	})

	assert.Equal(t, 8, stats.MaxOpenConnections)
	assert.EqualValues(t, 1, stats.WaitDurationMs)
}
