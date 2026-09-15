package model

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
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

func TestPatchPricingOptionsAppliesAndBumpsVersion(t *testing.T) {
	usePricingOptionsDB(t)
	seedPricingOption(t, "ModelRatio", `{"gpt-4o":1,"other":9}`)
	seedPricingOption(t, "CompletionRatio", `{"gpt-4o":3}`)

	version, applied, err := PatchPricingOptions(0, []PricingPatch{
		{OptionKey: "ModelRatio", Model: "gpt-4o", Expected: floatPtr(1), Value: floatPtr(2.5)},
		{OptionKey: "CompletionRatio", Model: "gpt-4o", Expected: floatPtr(3), Value: floatPtr(3)},
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), version)
	require.Len(t, applied["ModelRatio"], 1)
	require.Empty(t, applied["CompletionRatio"], "a field equal to the current value is not written")

	ratios := readPricingOptionValues(t, "ModelRatio")
	require.InDelta(t, 2.5, ratios["gpt-4o"], 1e-9)
	require.InDelta(t, 9, ratios["other"], 1e-9, "other models in the same option must not be touched")
	require.Equal(t, int64(1), GetPricingConfigVersion())
}

func TestPatchPricingOptionsRejectsStaleVersion(t *testing.T) {
	usePricingOptionsDB(t)
	seedPricingOption(t, "ModelRatio", `{"gpt-4o":1}`)

	version, _, err := PatchPricingOptions(0, []PricingPatch{{OptionKey: "ModelRatio", Model: "gpt-4o", Expected: floatPtr(1), Value: floatPtr(2)}})
	require.NoError(t, err)
	require.Equal(t, int64(1), version)

	_, _, err = PatchPricingOptions(0, []PricingPatch{{OptionKey: "ModelRatio", Model: "gpt-4o", Expected: floatPtr(2), Value: floatPtr(3)}})
	require.ErrorIs(t, err, ErrPricingVersionConflict)
	require.InDelta(t, 2, readPricingOptionValues(t, "ModelRatio")["gpt-4o"], 1e-9, "a rejected request must not write anything")
}

func TestPatchPricingOptionsRejectsValueConflictWithoutPartialWrite(t *testing.T) {
	usePricingOptionsDB(t)
	seedPricingOption(t, "ModelRatio", `{"gpt-4o":1}`)
	seedPricingOption(t, "CompletionRatio", `{"gpt-4o":3}`)

	// ModelRatio 的 expected 正确、CompletionRatio 的 expected 过期：整个请求必须回滚，
	// 不能留下「改了一半」的价格。
	_, _, err := PatchPricingOptions(0, []PricingPatch{
		{OptionKey: "ModelRatio", Model: "gpt-4o", Expected: floatPtr(1), Value: floatPtr(7)},
		{OptionKey: "CompletionRatio", Model: "gpt-4o", Expected: floatPtr(99), Value: floatPtr(8)},
	})
	require.ErrorIs(t, err, ErrPricingValueConflict)
	require.InDelta(t, 1, readPricingOptionValues(t, "ModelRatio")["gpt-4o"], 1e-9)
	require.InDelta(t, 3, readPricingOptionValues(t, "CompletionRatio")["gpt-4o"], 1e-9)
	require.Equal(t, int64(0), GetPricingConfigVersion())
}

func TestPatchPricingOptionsExpectedNilMeansUnconfigured(t *testing.T) {
	usePricingOptionsDB(t)
	seedPricingOption(t, "CacheRatio", `{"other":1}`)

	_, _, err := PatchPricingOptions(0, []PricingPatch{{OptionKey: "CacheRatio", Model: "gpt-4o", Expected: nil, Value: floatPtr(0.5)}})
	require.NoError(t, err)
	require.InDelta(t, 0.5, readPricingOptionValues(t, "CacheRatio")["gpt-4o"], 1e-9)

	_, _, err = PatchPricingOptions(1, []PricingPatch{{OptionKey: "CacheRatio", Model: "gpt-4o", Expected: nil, Value: floatPtr(0.6)}})
	require.ErrorIs(t, err, ErrPricingValueConflict, "the model is configured now, so a nil expectation is stale")
}

func TestPatchPricingOptionsDeletesModelKey(t *testing.T) {
	usePricingOptionsDB(t)
	seedPricingOption(t, "ImageRatio", `{"gpt-4o":2,"other":3}`)

	_, applied, err := PatchPricingOptions(0, []PricingPatch{{OptionKey: "ImageRatio", Model: "gpt-4o", Expected: floatPtr(2), Value: nil}})
	require.NoError(t, err)
	require.Len(t, applied["ImageRatio"], 1)

	values := readPricingOptionValues(t, "ImageRatio")
	require.NotContains(t, values, "gpt-4o")
	require.InDelta(t, 3, values["other"], 1e-9)
}

func TestPatchPricingOptionsNoOpKeepsVersion(t *testing.T) {
	usePricingOptionsDB(t)
	seedPricingOption(t, "ModelRatio", `{"gpt-4o":1}`)

	version, applied, err := PatchPricingOptions(0, []PricingPatch{{OptionKey: "ModelRatio", Model: "gpt-4o", Expected: floatPtr(1), Value: floatPtr(1)}})
	require.NoError(t, err)
	require.Empty(t, applied)
	require.Equal(t, int64(0), version, "a request that changes nothing must not invalidate other clients' optimistic reads")
	require.Equal(t, int64(0), GetPricingConfigVersion())
}

func TestPatchPricingOptionsRejectsUnknownOptionKey(t *testing.T) {
	usePricingOptionsDB(t)

	_, _, err := PatchPricingOptions(0, []PricingPatch{{OptionKey: "GroupRatio", Model: "gpt-4o", Value: floatPtr(1)}})
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrPricingVersionConflict)
}

// TestUpdateOptionBumpsPricingVersion 锁定跨页保护：倍率设置页的整块覆写走 UpdateOption，
// 它必须推进同一个版本号，否则巡检页的乐观校验对它完全失效。
func TestUpdateOptionBumpsPricingVersion(t *testing.T) {
	usePricingOptionsDB(t)
	seedPricingOption(t, "ModelRatio", `{"gpt-4o":1}`)
	require.Equal(t, int64(0), GetPricingConfigVersion())

	require.NoError(t, UpdateOption("ModelRatio", `{"gpt-4o":5}`))
	require.Equal(t, int64(1), GetPricingConfigVersion())

	_, _, err := PatchPricingOptions(0, []PricingPatch{{OptionKey: "ModelRatio", Model: "gpt-4o", Expected: floatPtr(1), Value: floatPtr(2)}})
	require.ErrorIs(t, err, ErrPricingVersionConflict, "an inline edit must not silently overwrite a whole-block save")
}

// TestPatchPricingOptionsConcurrentWritersOnlyOneWins 是 TOCTOU 的直接回归用例：
// 「读副本 -> 比 expected -> UpdateOptionsBulk」的老写法会让两个请求都通过校验、后写覆盖先写。
//
// SQLite 是单写者模型，冲突可能表现为 ErrPricingVersionConflict，也可能表现为事务层的
// SQLITE_BUSY 回滚——两者都满足「只有一个写入者能落库」，因此这里只断言成功者恰好一个。
func TestPatchPricingOptionsConcurrentWritersOnlyOneWins(t *testing.T) {
	usePricingOptionsDB(t)
	seedPricingOption(t, "ModelRatio", `{"gpt-4o":1}`)

	var waitGroup sync.WaitGroup
	results := make([]error, 2)
	targets := []float64{2, 3}
	start := make(chan struct{})
	for index := range results {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			<-start
			_, _, err := PatchPricingOptions(0, []PricingPatch{{OptionKey: "ModelRatio", Model: "gpt-4o", Expected: floatPtr(1), Value: floatPtr(targets[index])}})
			results[index] = err
		}(index)
	}
	close(start)
	waitGroup.Wait()

	succeeded := make([]int, 0, 2)
	for index, err := range results {
		if err == nil {
			succeeded = append(succeeded, index)
		}
	}
	require.Len(t, succeeded, 1, "exactly one concurrent writer may win; results: %v", results)
	require.Equal(t, int64(1), GetPricingConfigVersion())
	require.InDelta(t, targets[succeeded[0]], readPricingOptionValues(t, "ModelRatio")["gpt-4o"], 1e-9, "the stored value must belong to the writer that won")
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

// TestPatchPricingOptions_OnRealDatabase 是线上 apply_price 整条挂掉的回归测试。
//
// 上面那批用例跑在内存 SQLite 上，而 lockedPricingTx 在 SQLite 分支是原样返回 tx——
// 也就是说它们**从未执行过附加 FOR UPDATE 的那条代码路径**。真库（MySQL/PG）上
// tx.Clauses(...) 会把链式状态固化成一个具体 Statement，同一个实例上再调 .Where()
// 变成累加，于是「先读版本行、再读价格行」退化为
//
//	WHERE key = 'PricingConfigVersion' AND key = 'ModelRatio'
//
// 命中 0 行 → FirstOrCreate 报 record not found → 整个接口 500。
//
// 这个用例必须跑在真库上才有意义，因此用 requireDB 而不是 usePricingOptionsDB。
func TestPatchPricingOptions_OnRealDatabase(t *testing.T) {
	requireDB(t)
	const key = "ModelRatio"
	const probe = "__pricing_regression_probe__"

	// PatchPricingOptions 成功后会 updateOptionMap 刷新内存快照；单独 -run 本用例时
	// 没有别的用例建过 common.OptionMap，会 panic「assignment to entry in nil map」。
	// 用例不能依赖别的用例先跑过。（helper 在 option_test.go）
	ensureOptionMap()
	preservePricingOption(t, key)
	preservePricingOption(t, PricingConfigVersionKey)
	require.NoError(t, DB.Save(&Option{Key: key, Value: `{"gpt-4":1.5}`}).Error)
	require.NoError(t, DB.Save(&Option{Key: PricingConfigVersionKey, Value: "7"}).Error)

	value := 0.123
	version, applied, err := PatchPricingOptions(7, []PricingPatch{
		{OptionKey: key, Model: probe, Expected: nil, Value: &value},
	})
	require.NoError(t, err, "the full patch flow must work on the real database dialect")
	assert.EqualValues(t, 8, version, "version must advance by one")
	require.Len(t, applied[key], 1)

	var stored Option
	require.NoError(t, DB.Where(commonKeyCol+" = ?", key).Take(&stored).Error)
	values := map[string]float64{}
	require.NoError(t, common.UnmarshalJsonStr(stored.Value, &values))
	assert.InDelta(t, value, values[probe], 1e-9, "the patched model price must be persisted")
	assert.InDelta(t, 1.5, values["gpt-4"], 1e-9, "untouched models must survive the partial update")
}

// TestPatchPricingOptions_LockedTxIsNotReusable 直接盯住上面那个 bug 的机理：
// 同一个 locked *gorm.DB 连续用于两条查询时不得污染条件。
func TestPatchPricingOptions_LockedTxIsNotReusable(t *testing.T) {
	requireDB(t)
	const key = "ModelRatio"
	preservePricingOption(t, key)
	require.NoError(t, DB.Save(&Option{Key: key, Value: `{"gpt-4":1.5}`}).Error)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		locked := lockedPricingTx(tx)
		if _, err := readPricingVersionForUpdate(locked); err != nil {
			return err
		}
		_, _, err := readPricingOptionForUpdate(locked, key)
		return err
	}), "reusing one locked tx for two reads must not accumulate WHERE conditions")
}

// 内置默认价没有持久化到 options 表时，改价不得被误判成取值冲突。
//
// 巡检页显示的是**生效价**（含内置默认），客户端照它回传 expected；而
// readPricingOptionForUpdate 只看 options 表，查不到就当「未配置」，
// 于是 expected 永远对不上，一个用默认价的模型刷新多少次都改不动。
func TestPatchPricingOptions_UnpersistedDefaultIsNotAConflict(t *testing.T) {
	usePricingOptionsDB(t)

	// 只动这一个键并在用例结束后还原。不能调 InitRatioSettings()——它会把整份内置
	// 默认表灌进**包级**全局映射，污染同一次运行里的其它用例
	// （TestPatchPricingOptionsExpectedNilMeansUnconfigured 就是这么被弄挂的）。
	const modelName = "__unpersisted_default_probe__"
	const effective = 3.5
	origin := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelRatioByJSONString(origin) })

	live := map[string]float64{}
	require.NoError(t, common.UnmarshalJsonStr(origin, &live))
	live[modelName] = effective
	encoded, err := common.Marshal(live)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(encoded)))

	// options 表里**没有**这个模型 —— 模拟「生效价来自内存默认，未持久化」。
	require.NoError(t, DB.Save(&Option{Key: "ModelRatio", Value: `{"__other__":1}`}).Error)

	expected := effective
	value := effective * 2
	_, applied, err := PatchPricingOptions(0, []PricingPatch{
		{OptionKey: "ModelRatio", Model: modelName, Expected: &expected, Value: &value},
	})
	require.NoError(t, err,
		"expected matching the in-memory effective value must not be reported as a value conflict")
	require.Len(t, applied["ModelRatio"], 1)

	var stored Option
	require.NoError(t, DB.Where(commonKeyCol+" = ?", "ModelRatio").Take(&stored).Error)
	values := map[string]float64{}
	require.NoError(t, common.UnmarshalJsonStr(stored.Value, &values))
	assert.InDelta(t, value, values[modelName], 1e-9)
	assert.InDelta(t, 1, values["__other__"], 1e-9, "untouched keys must survive")
	assert.Len(t, values, 2,
		"the fallback is for comparison only; it must not persist the whole effective table")
}

// 从没保存过的价格项：以内存里的内置默认表为底打补丁。写回后内存按整表替换，只写被改的键
// 会把其它模型的默认价全部冲掉（它们的缓存读取随即回落到 1、按全价计费）。
func TestPatchPricingOptions_UnsavedOptionKeepsBuiltinDefaults(t *testing.T) {
	usePricingOptionsDB(t)
	origin, err := common.Marshal(ratio_setting.GetCacheRatioCopy())
	require.NoError(t, err)
	t.Cleanup(func() { _ = ratio_setting.UpdateCacheRatioByJSONString(string(origin)) })
	require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(`{"__probe_a__":0.1,"__probe_b__":0.2}`))

	expected := 0.1
	value := 0.05
	_, applied, err := PatchPricingOptions(0, []PricingPatch{
		{OptionKey: "CacheRatio", Model: "__probe_a__", Expected: &expected, Value: &value},
	})
	require.NoError(t, err)
	require.Len(t, applied["CacheRatio"], 1)

	stored := readPricingOptionValues(t, "CacheRatio")
	assert.InDelta(t, 0.05, stored["__probe_a__"], 1e-9)
	assert.InDelta(t, 0.2, stored["__probe_b__"], 1e-9, "untouched built-in defaults must be persisted with the patch")
	ratio, ok := ratio_setting.GetCacheRatio("__probe_b__")
	require.True(t, ok, "the in-memory table must still hold the other defaults")
	assert.InDelta(t, 0.2, ratio, 1e-9)
}
