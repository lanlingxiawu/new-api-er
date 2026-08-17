package model

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/price_monitor_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// useConfigGroupDB 把 DB 换成内存 SQLite。
//
// 这里刻意不用真实项目库（Rule 15.5 的例外）：SaveConfigGroup 写的是 options 表，
// 而那张表是共享开发库里**正在运行的实例的活配置**——用真库跑一遍就等于把
// 开发环境的限流阈值和连接池改掉。跨方言的 SQL 契约由
// TestPersistOptionsTxOnRealDatabase 单独用一次性键在真库上验证。
//
// 同时补上 common.OptionMap 的初始化：生产由 InitOptionMap 保证，
// 测试里不初始化会在写 map 时 panic，并不是被测代码的问题。
func useConfigGroupDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := DB
	previousType := common.MainDatabaseType()
	previousMap := common.OptionMap
	rateBefore := operation_setting.GetRateLimitSetting()
	poolBefore := operation_setting.GetDBPoolSetting()
	sessionBefore := operation_setting.GetUserSessionSetting()
	relayTimeoutBefore := operation_setting.GetRelayTimeoutSetting()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Option{}))
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.OptionMapRWMutex.Lock()
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousMap
		common.OptionMapRWMutex.Unlock()
		operation_setting.ReplaceRateLimitSetting(rateBefore)
		operation_setting.ReplaceDBPoolSetting(poolBefore)
		operation_setting.ReplaceUserSessionSetting(sessionBefore)
		operation_setting.ReplaceRelayTimeoutSetting(relayTimeoutBefore)
	})
	return db
}

func optionRowValue(t *testing.T, db *gorm.DB, key string) (string, bool) {
	t.Helper()
	var row Option
	if err := db.Where(&Option{Key: key}).First(&row).Error; err != nil {
		return "", false
	}
	return row.Value, true
}

// ---------------------------------------------------------------------------
// validateGroupFields —— 等价类划分 + 判定覆盖
// ---------------------------------------------------------------------------

func TestValidateGroupFieldsRejectsMalformedInput(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]string
		reason string
	}{
		{"空字段名", map[string]string{"": "1"}, "invalid configuration field"},
		{"字段名带点会越权写到别的模块", map[string]string{"other_setting.x": "1"}, "invalid configuration field"},
		{"不在白名单内的字段", map[string]string{"not_a_field": "1"}, "not editable"},
		{"_enabled 字段收到非布尔值", map[string]string{"critical_enabled": "yes-please"}, "boolean"},
		{"数值字段收到非整数", map[string]string{"critical_num": "12.5"}, "integer"},
		{"数值字段收到空串", map[string]string{"critical_num": ""}, "integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateGroupFields(tc.values, rateLimitFields)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.reason)
		})
	}
}

func TestValidateGroupFieldsAcceptsWhitelistedValues(t *testing.T) {
	require.NoError(t, validateGroupFields(map[string]string{
		"critical_enabled": "false",
		"critical_num":     "60",
		"search_num":       "-1", // 语法合法；范围由 Validate* 负责，此处不该拦
	}, rateLimitFields))

	require.NoError(t, validateGroupFields(map[string]string{
		"max_idle_conns":     "100",
		"log_max_open_conns": "0",
	}, dbPoolFields))
}

// 两个模块的白名单必须互斥，否则一个模块的键能写进另一个模块。
func TestConfigGroupFieldWhitelistsAreDisjoint(t *testing.T) {
	for field := range rateLimitFields {
		_, clash := dbPoolFields[field]
		assert.False(t, clash, "字段 %s 同时出现在两个模块白名单里", field)
	}
	assert.NotEmpty(t, rateLimitFields)
	assert.NotEmpty(t, dbPoolFields)
}

// 白名单必须覆盖结构体的全部 json 字段，否则新增配置项在后台改不了。
func TestConfigGroupWhitelistsCoverEveryStructField(t *testing.T) {
	assert.Len(t, rateLimitFields, 23, "rate_limit_setting 字段数与白名单不一致")
	assert.Len(t, dbPoolFields, 5, "db_pool_setting 字段数与白名单不一致")
	assert.Len(t, userSessionFields, 5, "user_session_setting 字段数与白名单不一致")
	assert.Len(t, relayTimeoutFields, 3, "relay_timeout_setting 字段数与白名单不一致")
}

func TestSaveVeridropMonitorConfigGroupPersistsAndPublishes(t *testing.T) {
	db := useConfigGroupDB(t)
	original := operation_setting.GetVeridropMonitorSetting()
	t.Cleanup(func() { operation_setting.ReplaceVeridropMonitorSetting(original) })

	applied, err := SaveConfigGroup("veridrop_monitor_setting", map[string]string{
		"enabled":        "true",
		"base_url":       "https://veridrop.example",
		"max_concurrent": "4",
	})
	require.NoError(t, err)
	require.True(t, applied)
	require.True(t, operation_setting.GetVeridropMonitorSnapshot().Enabled)
	require.Equal(t, 4, operation_setting.GetVeridropMonitorSnapshot().MaxConcurrent)
	value, ok := optionRowValue(t, db, "veridrop_monitor_setting.base_url")
	require.True(t, ok)
	require.Equal(t, "https://veridrop.example", value)
}

func TestSaveVeridropMonitorConfigGroupRejectsInvalidValues(t *testing.T) {
	useConfigGroupDB(t)
	original := operation_setting.GetVeridropMonitorSetting()
	t.Cleanup(func() { operation_setting.ReplaceVeridropMonitorSetting(original) })

	applied, err := SaveConfigGroup("veridrop_monitor_setting", map[string]string{
		"default_mode": "fast",
	})
	require.Error(t, err)
	require.False(t, applied)
}

func TestSavePriceMonitorConfigGroupPersistsAndPublishes(t *testing.T) {
	db := useConfigGroupDB(t)
	original := *price_monitor_setting.GetPriceMonitorSetting()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap("price_monitor_setting", map[string]string{
			"enabled":            strconv.FormatBool(original.Enabled),
			"interval_minutes":   strconv.Itoa(original.IntervalMinutes),
			"timeout_seconds":    strconv.Itoa(original.TimeoutSeconds),
			"include_official":   strconv.FormatBool(original.IncludeOfficial),
			"include_models_dev": strconv.FormatBool(original.IncludeModelsDev),
			"model_whitelist":    original.ModelWhitelist,
		}))
	})

	applied, err := SaveConfigGroup("price_monitor_setting", map[string]string{
		"enabled":            "true",
		"interval_minutes":   "5",
		"timeout_seconds":    "120",
		"include_official":   "true",
		"include_models_dev": "true",
		"model_whitelist":    "free-model",
	})
	require.NoError(t, err)
	require.True(t, applied)
	snapshot := price_monitor_setting.GetPriceMonitorSetting()
	assert.True(t, snapshot.Enabled)
	assert.Equal(t, 5, snapshot.IntervalMinutes)
	assert.Equal(t, 120, snapshot.TimeoutSeconds)
	assert.True(t, snapshot.IncludeOfficial)
	assert.True(t, snapshot.IncludeModelsDev)
	assert.Equal(t, "free-model", snapshot.ModelWhitelist)
	value, ok := optionRowValue(t, db, "price_monitor_setting.interval_minutes")
	require.True(t, ok)
	assert.Equal(t, "5", value)
}

func TestSavePriceMonitorConfigGroupRejectsInvalidValues(t *testing.T) {
	db := useConfigGroupDB(t)

	for _, values := range []map[string]string{
		{"interval_minutes": "4"},
		{"timeout_seconds": "0"},
		{"timeout_seconds": "121"},
		{"include_official": "false"},
		{"unknown": "value"},
	} {
		applied, err := SaveConfigGroup("price_monitor_setting", values)
		require.Error(t, err)
		assert.False(t, applied)
	}
	_, exists := optionRowValue(t, db, "price_monitor_setting.interval_minutes")
	assert.False(t, exists)
}

func TestSaveRelayTimeoutConfigGroupPersistsAndPublishes(t *testing.T) {
	db := useConfigGroupDB(t)

	applied, err := SaveConfigGroup("relay_timeout_setting", map[string]string{
		"enabled":                  "false",
		"response_timeout_seconds": "45",
		"total_timeout_seconds":    "600",
	})
	require.NoError(t, err)
	assert.True(t, applied)

	snapshot := operation_setting.GetRelayTimeoutSnapshot()
	require.NotNil(t, snapshot)
	assert.False(t, snapshot.Enabled)
	assert.Equal(t, 45, snapshot.ResponseTimeoutSeconds)
	assert.Equal(t, 600, snapshot.TotalTimeoutSeconds)
	value, ok := optionRowValue(t, db, "relay_timeout_setting.enabled")
	require.True(t, ok)
	assert.Equal(t, "false", value)
}

func TestSaveRelayTimeoutConfigGroupRejectsInvalidValuesBeforePersistence(t *testing.T) {
	db := useConfigGroupDB(t)

	for _, values := range []map[string]string{
		{"enabled": "not-a-bool"},
		{"response_timeout_seconds": "-1"},
		{"total_timeout_seconds": strconv.Itoa(operation_setting.MaxRelayTimeoutSettingSeconds + 1)},
	} {
		applied, err := SaveConfigGroup("relay_timeout_setting", values)
		require.Error(t, err)
		assert.False(t, applied)
	}
	_, exists := optionRowValue(t, db, "relay_timeout_setting.enabled")
	assert.False(t, exists)
}

func TestSaveUserSessionConfigGroupPersistsAndPublishes(t *testing.T) {
	db := useConfigGroupDB(t)
	values := map[string]string{
		"active_limit": "25", "issuance_limit": "80", "issuance_window_sec": "43200",
		"revoked_retention_days": "2", "hourly_alert_threshold": "4000",
	}
	applied, err := SaveConfigGroup("user_session_setting", values)
	require.NoError(t, err)
	assert.True(t, applied)
	assert.Equal(t, 25, operation_setting.GetUserSessionSnapshot().ActiveLimit)
	value, ok := optionRowValue(t, db, "user_session_setting.active_limit")
	require.True(t, ok)
	assert.Equal(t, "25", value)
}

func TestSaveUserSessionConfigGroupRejectsWindowBeyondRetention(t *testing.T) {
	db := useConfigGroupDB(t)
	applied, err := SaveConfigGroup("user_session_setting", map[string]string{
		"active_limit": "25", "issuance_limit": "80", "issuance_window_sec": "86401",
		"revoked_retention_days": "1", "hourly_alert_threshold": "4000",
	})
	require.Error(t, err)
	assert.False(t, applied)
	_, exists := optionRowValue(t, db, "user_session_setting.active_limit")
	assert.False(t, exists)
}

// ---------------------------------------------------------------------------
// SaveConfigGroup —— 模块路由、校验、落库、热生效
// ---------------------------------------------------------------------------

func TestSaveConfigGroupRejectsUnknownModule(t *testing.T) {
	applied, err := SaveConfigGroup("performance_setting", map[string]string{"x": "1"})
	require.Error(t, err)
	assert.False(t, applied)
	assert.Contains(t, err.Error(), "not editable")
}

func TestSaveConfigGroupRejectsEmptyValues(t *testing.T) {
	applied, err := SaveConfigGroup("rate_limit_setting", map[string]string{})
	require.Error(t, err)
	assert.False(t, applied)
}

// 范围校验失败必须在落库之前返回，否则库里会留下一份进程拒绝接受的配置。
func TestSaveConfigGroupOutOfRangeDoesNotTouchDatabase(t *testing.T) {
	db := useConfigGroupDB(t)
	before := operation_setting.GetRateLimitSetting()

	applied, err := SaveConfigGroup("rate_limit_setting", map[string]string{
		"critical_num": strconv.Itoa(200_001), // 超过 maxRateLimitNum
	})

	require.Error(t, err)
	assert.False(t, applied)
	assert.Equal(t, before, operation_setting.GetRateLimitSetting(), "校验失败不应改动内存配置")
	_, exists := optionRowValue(t, db, "rate_limit_setting.critical_num")
	assert.False(t, exists, "校验失败不应写库")
}

func TestSaveConfigGroupPersistsAndTakesEffectImmediately(t *testing.T) {
	db := useConfigGroupDB(t)

	applied, err := SaveConfigGroup("rate_limit_setting", map[string]string{
		"critical_num":          "77",
		"critical_duration_sec": "300",
		"critical_enabled":      "true",
	})
	require.NoError(t, err)
	assert.True(t, applied)

	// 1. 落库
	value, ok := optionRowValue(t, db, "rate_limit_setting.critical_num")
	require.True(t, ok)
	assert.Equal(t, "77", value)

	// 2. 内存草稿
	assert.Equal(t, 77, operation_setting.GetRateLimitSetting().CriticalNum)

	// 3. 请求路径读的快照——这一步才是"立即生效"的真正含义
	snapshot := operation_setting.GetRateLimitSnapshot()
	assert.Equal(t, 77, snapshot.Critical.Num)
	assert.Equal(t, int64(300), snapshot.Critical.Duration)
	assert.True(t, snapshot.Critical.Enabled)

	// 4. OptionMap（管理接口读的那份）
	common.OptionMapRWMutex.RLock()
	mapped := common.OptionMap["rate_limit_setting.critical_num"]
	common.OptionMapRWMutex.RUnlock()
	assert.Equal(t, "77", mapped)
}

// 整组保存的意义就在于跨字段校验：单字段看都合法，组合起来非法必须被拒。
func TestSaveConfigGroupValidatesAcrossFields(t *testing.T) {
	db := useConfigGroupDB(t)

	applied, err := SaveConfigGroup("db_pool_setting", map[string]string{
		"max_idle_conns": "500",
		"max_open_conns": "100", // 单看都在范围内，但 idle > open 非法
	})

	require.Error(t, err)
	assert.False(t, applied)
	assert.Contains(t, err.Error(), "idle")
	_, exists := optionRowValue(t, db, "db_pool_setting.max_idle_conns")
	assert.False(t, exists)
}

func TestSaveConfigGroupAppliesDBPoolImmediately(t *testing.T) {
	useConfigGroupDB(t)

	applied, err := SaveConfigGroup("db_pool_setting", map[string]string{
		"max_idle_conns":   "37",
		"max_open_conns":   "137",
		"max_lifetime_sec": "120",
	})
	require.NoError(t, err)
	assert.True(t, applied)

	snapshot := operation_setting.GetDBPoolSnapshot()
	assert.Equal(t, 137, snapshot.MaxOpen)
	assert.Equal(t, 120.0, snapshot.Lifetime.Seconds())

	// 真的推到了 sql.DB 上，而不只是改了内存
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	assert.Equal(t, 137, sqlDB.Stats().MaxOpenConnections)
}

// 单键入口（PUT /api/option）对这两个模块也走整组校验，不能绕过范围检查。
func TestSaveConfigGroupSingleFieldStillValidated(t *testing.T) {
	useConfigGroupDB(t)

	_, err := SaveConfigGroup("rate_limit_setting", map[string]string{"search_num": "0"})
	require.Error(t, err, "0 低于下界，单字段保存同样要被拒")

	applied, err := SaveConfigGroup("rate_limit_setting", map[string]string{"search_num": "9"})
	require.NoError(t, err)
	assert.True(t, applied)
	assert.Equal(t, 9, operation_setting.GetRateLimitSnapshot().Search.Num)
}

// 同一个键重复保存要走 UPDATE 而不是插入第二行。
func TestSaveConfigGroupIsIdempotentPerKey(t *testing.T) {
	db := useConfigGroupDB(t)

	for _, num := range []string{"30", "40", "50"} {
		_, err := SaveConfigGroup("rate_limit_setting", map[string]string{"search_num": num})
		require.NoError(t, err)
	}

	var count int64
	require.NoError(t, db.Model(&Option{}).Where(&Option{Key: "rate_limit_setting.search_num"}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	value, _ := optionRowValue(t, db, "rate_limit_setting.search_num")
	assert.Equal(t, "50", value)
}

// ---------------------------------------------------------------------------
// persistOptionsTx —— 唯一需要真库的部分：key 是保留字，各方言引用规则不同
// ---------------------------------------------------------------------------

func TestPersistOptionsTxOnRealDatabase(t *testing.T) {
	requireDB(t)
	key := "zz_test_config_group." + uniq("k")
	t.Cleanup(func() { DB.Unscoped().Where(&Option{Key: key}).Delete(&Option{}) })

	require.NoError(t, persistOptionsTx(map[string]string{key: "first"}))
	value, ok := optionRowValue(t, DB, key)
	require.True(t, ok)
	assert.Equal(t, "first", value)

	// 第二次写同一个键必须是更新，不能因主键冲突失败，也不能插出第二行
	require.NoError(t, persistOptionsTx(map[string]string{key: "second"}))
	value, ok = optionRowValue(t, DB, key)
	require.True(t, ok)
	assert.Equal(t, "second", value)

	var count int64
	require.NoError(t, DB.Model(&Option{}).Where(&Option{Key: key}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}
