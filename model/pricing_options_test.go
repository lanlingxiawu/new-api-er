package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// usePricingOptionsDB 与 useConfigGroupDB 同理，刻意不用真实项目库（Rule 15.5 的例外）：
// PatchPricingOptions 写的是 options 表里的 ModelRatio / CompletionRatio 等键，而那些是
// 共享开发库里**正在运行的实例的活价格配置**——用真库跑一遍就等于把开发环境的模型价格改掉。
// 跨方言的 SQL 契约（commonKeyCol 引用、FOR UPDATE 子句）由
// TestPricingOptionsLockingOnRealDatabase 用本功能自己新增的键在真库上单独验证。
//
// 同时补上 common.OptionMap 的初始化：生产由 InitOptionMap 保证，测试里不初始化会在写 map 时 panic。
func usePricingOptionsDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := DB
	previousType := common.MainDatabaseType()
	previousMap := common.OptionMap
	previousPricing := map[string]string{}
	for _, key := range modelPricingOptionKeys {
		var value any = pricingMemoryValues(key)
		if key == "billing_setting.billing_mode" {
			value = billing_setting.GetBillingModeCopy()
		}
		if key == "billing_setting.billing_expr" {
			value = billing_setting.GetBillingExprCopy()
		}
		encoded, encodeErr := common.Marshal(value)
		require.NoError(t, encodeErr)
		previousPricing[key] = string(encoded)
	}
	previousKeyCol := commonKeyCol
	commonKeyCol = "`key`"

	// 每个用例一个独立命名的内存库：cache=shared 让 GORM 连接池里的多条连接看到同一个库
	// （并发用例需要），命名则保证用例之间互不串状态。
	dsn := fmt.Sprintf("file:pricing_%s_%d?mode=memory&cache=shared&_pragma=busy_timeout(5000)", t.Name(), time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Option{}))
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.OptionMapRWMutex.Lock()
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		for key, value := range previousPricing {
			require.NoError(t, updateOptionMap(key, value))
		}
		commonKeyCol = previousKeyCol
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousMap
		common.OptionMapRWMutex.Unlock()
	})
	return db
}

func seedPricingOption(t *testing.T, key, value string) {
	t.Helper()
	option := Option{Key: key}
	require.NoError(t, DB.Where(Option{Key: key}).FirstOrCreate(&option).Error)
	option.Value = value
	require.NoError(t, DB.Save(&option).Error)
	common.OptionMapRWMutex.Lock()
	common.OptionMap[key] = value
	common.OptionMapRWMutex.Unlock()
}

func readPricingOptionValues(t *testing.T, key string) map[string]float64 {
	t.Helper()
	var option Option
	require.NoError(t, DB.Where(commonKeyCol+" = ?", key).Take(&option).Error)
	values := make(map[string]float64)
	if option.Value != "" {
		require.NoError(t, common.UnmarshalJsonStr(option.Value, &values))
	}
	return values
}

func floatPtr(value float64) *float64 { return &value }

func TestIsPricingOptionKey(t *testing.T) {
	for _, key := range []string{"ModelRatio", "CompletionRatio", "CacheRatio", "CreateCacheRatio", "ImageRatio", "AudioRatio", "AudioCompletionRatio", "ModelPrice"} {
		require.True(t, IsPricingOptionKey(key), key)
	}
	for _, key := range []string{"GroupRatio", "TopUpLink", "", "modelratio"} {
		require.False(t, IsPricingOptionKey(key), key)
	}
}

// TestUpdateOptionBumpsPricingVersion 锁定跨页保护：倍率设置页的整块覆写走 UpdateOption，
// 它必须推进同一个版本号，否则巡检页的乐观校验对它完全失效。
func TestUpdateOptionBumpsPricingVersion(t *testing.T) {
	usePricingOptionsDB(t)
	seedPricingOption(t, "ModelRatio", `{"gpt-4o":1}`)
	require.Equal(t, int64(0), GetPricingConfigVersion())

	before, err := GetModelPricingSnapshot([]string{"gpt-4o"})
	require.NoError(t, err)
	require.NoError(t, UpdateOption("ModelRatio", `{"gpt-4o":5}`))
	require.Equal(t, int64(1), GetPricingConfigVersion())

	err = UpdateModelPricing([]ModelPricingChange{{ModelName: "gpt-4o", ExpectedVersion: before.Entries[0].Version, Pricing: PricingValues{"ModelRatio": 2}}})
	require.ErrorIs(t, err, ErrModelPricingConflict)

}

// TestPricingOptionsLockingOnRealDatabase 在真实项目库上验证跨方言 SQL 契约：
// commonKeyCol 的列名引用与 lockedPricingTx 的 FOR UPDATE 子句能否被当前方言接受。
// 只使用本功能自己新增的 PricingConfigVersion 键，不触碰开发库里的活价格配置。
func TestPricingOptionsLockingOnRealDatabase(t *testing.T) {
	requireDB(t)

	var existing Option
	hadRow := DB.Where(commonKeyCol+" = ?", PricingConfigVersionKey).Take(&existing).Error == nil
	t.Cleanup(func() {
		if hadRow {
			DB.Save(&existing)
			return
		}
		DB.Unscoped().Where(commonKeyCol+" = ?", PricingConfigVersionKey).Delete(&Option{})
	})

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := readPricingVersionForUpdate(lockedPricingTx(tx))
		return err
	}), "the locking read must be accepted by the configured database dialect")
}

// preservePricingOption 备份一个 option 行并在用例结束后**原样还原**。
//
// 真库用例绝不能直接覆盖再删除价格 option：开发库里那是活配置，删掉等于把整套模型
// 定价清空。本文件里 TestPricingOptionsLockingOnRealDatabase 早就用了这个模式，
// 新增用例必须沿用。
func preservePricingOption(t *testing.T, key string) {
	t.Helper()
	var existing Option
	hadRow := DB.Where(commonKeyCol+" = ?", key).Take(&existing).Error == nil
	t.Cleanup(func() {
		if hadRow {
			DB.Save(&existing)
			return
		}
		DB.Unscoped().Where(commonKeyCol+" = ?", key).Delete(&Option{})
	})
}

// pricingMemoryValues 返回某个价格 option 在内存里的整表副本（含内置默认值）。
func pricingMemoryValues(optionKey string) map[string]float64 {
	switch optionKey {
	case "ModelRatio":
		return ratio_setting.GetModelRatioCopy()
	case "ModelPrice":
		return ratio_setting.GetModelPriceCopy()
	case "CompletionRatio":
		return ratio_setting.GetCompletionRatioCopy()
	case "CacheRatio":
		return ratio_setting.GetCacheRatioCopy()
	case "CreateCacheRatio":
		return ratio_setting.GetCreateCacheRatioCopy()
	case "ImageRatio":
		return ratio_setting.GetImageRatioCopy()
	case "AudioRatio":
		return ratio_setting.GetAudioRatioCopy()
	case "AudioCompletionRatio":
		return ratio_setting.GetAudioCompletionRatioCopy()
	}
	return map[string]float64{}
}

// pricingOptionKeys 是承载「模型 -> 数值」映射的价格类 option。
var pricingOptionKeys = map[string]struct{}{
	"ModelRatio":           {},
	"CompletionRatio":      {},
	"CacheRatio":           {},
	"CreateCacheRatio":     {},
	"ImageRatio":           {},
	"AudioRatio":           {},
	"AudioCompletionRatio": {},
	"ModelPrice":           {},
}

// IsPricingOptionKey 判断某个 option 是否属于价格配置，用于决定是否递增版本号。
func IsPricingOptionKey(key string) bool {
	_, ok := pricingOptionKeys[key]
	return ok
}
