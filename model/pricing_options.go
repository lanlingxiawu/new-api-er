package model

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PricingConfigVersionKey 是价格配置的持久化版本号。任何写价格类 option 的路径都必须
// 在同一事务内递增它，否则并发写入只能被最后一个提交者静默覆盖。
const PricingConfigVersionKey = "PricingConfigVersion"

const pricingFloatEpsilon = 1e-9

var (
	// ErrPricingVersionConflict 表示自客户端读取后已有其他人改过价格配置。
	ErrPricingVersionConflict = errors.New("pricing config version conflict")
	// ErrPricingValueConflict 表示目标模型的某个字段当前值与客户端所见不一致。
	ErrPricingValueConflict = errors.New("pricing config value conflict")
	// ErrPricingPatchInvalid 表示改写后的模型价格未通过 validateModelPricing 校验，整单未写入。
	ErrPricingPatchInvalid = errors.New("pricing patch is invalid")
)

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

// PricingPatch 描述对单个 option 里单个模型键的一次修改。
// Expected 为 nil 表示客户端所见是「未配置」；Value 为 nil 表示删除该模型键。
type PricingPatch struct {
	OptionKey string
	Model     string
	Expected  *float64
	Value     *float64
}

// GetPricingConfigVersion 读取当前价格配置版本号。缺失或非法时按 0 处理。
func GetPricingConfigVersion() int64 {
	common.OptionMapRWMutex.RLock()
	raw, ok := common.OptionMap[PricingConfigVersionKey]
	common.OptionMapRWMutex.RUnlock()
	if !ok {
		return 0
	}
	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return version
}

// PatchPricingOptions 在单个事务内完成 行锁 → 校验版本 → 校验目标字段 → 局部改写 → 校验改写后的模型价格 → 递增版本。
//
// 这是「按模型局部更新价格」的写入路径。之所以不能沿用 UpdateOptionsBulk，是因为它内部只有
// FirstOrCreate + Save，没有版本列、没有条件更新、也没有行锁：在事务之外比对 expected 再调用它，
// 两个并发请求会双双通过比对，后写覆盖先写。
//
// 改写后的每个被改模型都经过与模型定价整块保存（mutateModelPricingOptions）相同的
// validateModelPricing 校验，失败时整单回滚并返回 *PricingPatchInvalidError（errors.Is 匹配 ErrPricingPatchInvalid）。
// 与整块保存共用 modelPricingMutationMu：进程内的写库、内存刷新与定价缓存重建按提交顺序串行，
// 避免后提交的内存状态被先提交者覆盖。成功写入后刷新 OptionMap、定价缓存（RefreshPricing）与对外暴露数据缓存。
//
// 返回值 applied 是「实际写入的 option key -> 模型字段列表」；与当前值相同的字段不写，
// 由调用方归入 unchanged。没有任何实际写入时版本号不变。
func PatchPricingOptions(expectedVersion int64, patches []PricingPatch) (int64, map[string][]PricingPatch, error) {
	if len(patches) == 0 {
		return GetPricingConfigVersion(), map[string][]PricingPatch{}, nil
	}
	byOptionKey := make(map[string][]PricingPatch)
	for _, patch := range patches {
		if !IsPricingOptionKey(patch.OptionKey) {
			return 0, nil, errors.New("unsupported pricing option key: " + patch.OptionKey)
		}
		byOptionKey[patch.OptionKey] = append(byOptionKey[patch.OptionKey], patch)
	}
	// 固定加锁顺序，避免两个并发请求以相反顺序锁定同一批行而死锁。
	optionKeys := make([]string, 0, len(byOptionKey))
	for optionKey := range byOptionKey {
		optionKeys = append(optionKeys, optionKey)
	}
	sort.Strings(optionKeys)

	modelPricingMutationMu.Lock()
	defer modelPricingMutationMu.Unlock()

	newVersion := expectedVersion + 1
	applied := make(map[string][]PricingPatch)
	written := make(map[string]string)

	err := DB.Transaction(func(tx *gorm.DB) error {
		// 先版本行、后价格行，与 mutateModelPricingOptions / updatePricingOption 的加锁顺序一致。
		currentVersion, err := readPricingVersionForUpdate(lockedPricingTx(tx))
		if err != nil {
			return err
		}
		if currentVersion != expectedVersion {
			return ErrPricingVersionConflict
		}
		// 改写前的整份价格配置（含计费模式、表达式），供校验改写后的模型价格。
		before, _, _, err := readModelPricingMaps(lockForUpdate(tx))
		if err != nil {
			return err
		}

		patchedValues := make(map[string]map[string]float64)
		patchedOptions := make(map[string]Option)
		for _, optionKey := range optionKeys {
			values, option, err := readPricingOptionForUpdate(lockedPricingTx(tx), optionKey)
			if err != nil {
				return err
			}
			changed := false
			for _, patch := range byOptionKey[optionKey] {
				current, exists := values[patch.Model]
				if !exists && patch.Expected != nil {
					// 内置默认价没有持久化到 options 表，DB 里查不到不等于「未配置」。
					// 不补这一步的话，巡检页显示的是默认价、客户端照它回传 expected，
					// 而这里判定 exists=false → 永远 ErrPricingValueConflict，
					// 刷新多少次都改不动一个用默认价的模型。
					//
					// 只在 Expected != nil 时兜底。Expected == nil 表示客户端认为这个模型
					// **完全没有配置**，那本来就该按 options 表的事实判断；对 nil 也套用
					// 兜底只会凭空制造冲突，并让判定结果依赖内存映射被谁先填过
					// （会把 TestPatchPricingOptionsExpectedNilMeansUnconfigured 变成
					// 依赖测试执行顺序的用例）。
					if fallback, ok := pricingEffectiveValue(optionKey, patch.Model); ok {
						current, exists = fallback, true
					}
				}
				if !pricingValueMatches(patch.Expected, current, exists) {
					return ErrPricingValueConflict
				}
				if patch.Value == nil {
					if !exists {
						continue
					}
					delete(values, patch.Model)
				} else {
					if exists && nearlyEqualPricingValue(current, *patch.Value) {
						continue
					}
					values[patch.Model] = *patch.Value
				}
				applied[optionKey] = append(applied[optionKey], patch)
				changed = true
			}
			if changed {
				patchedValues[optionKey] = values
				patchedOptions[optionKey] = option
			}
		}

		if len(patchedValues) == 0 {
			// 没有任何实际写入，版本号也不该前进，否则会平白让其他客户端的乐观校验失败。
			newVersion = currentVersion
			return nil
		}
		if err := validatePricingPatches(before, patchedValues, applied); err != nil {
			return err
		}
		for _, optionKey := range optionKeys {
			values, changed := patchedValues[optionKey]
			if !changed {
				continue
			}
			encoded, err := common.Marshal(values)
			if err != nil {
				return err
			}
			option := patchedOptions[optionKey]
			option.Value = string(encoded)
			if err := tx.Save(&option).Error; err != nil {
				return err
			}
			written[optionKey] = option.Value
		}

		versionOption := Option{Key: PricingConfigVersionKey}
		if err := tx.Where(Option{Key: PricingConfigVersionKey}).FirstOrCreate(&versionOption).Error; err != nil {
			return err
		}
		newVersion = currentVersion + 1
		versionOption.Value = strconv.FormatInt(newVersion, 10)
		if err := tx.Save(&versionOption).Error; err != nil {
			return err
		}
		written[PricingConfigVersionKey] = versionOption.Value
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	if len(written) == 0 {
		return newVersion, applied, nil
	}
	for key, value := range written {
		if updateErr := updateOptionMap(key, value); updateErr != nil {
			common.SysError("failed to refresh pricing option in memory: " + updateErr.Error())
		}
	}
	RefreshPricing()
	ratio_setting.InvalidateExposedDataCache()
	return newVersion, applied, nil
}

// validatePricingPatches 用 validateModelPricing 校验本次实际改动的每个模型改写后的完整价格。
// 参数 before：改写前的整份价格配置（readModelPricingMaps 结果，不修改）；patched：被改写的 option key -> 改写后的整表；
// applied：实际写入的补丁，决定需要校验的模型。返回按模型名排序的首个失败模型的 *PricingPatchInvalidError。
func validatePricingPatches(before map[string]map[string]any, patched map[string]map[string]float64, applied map[string][]PricingPatch) error {
	after := maps.Clone(before)
	for optionKey, values := range patched {
		entries := make(map[string]any, len(values))
		for name, value := range values {
			entries[name] = value
		}
		after[optionKey] = entries
	}
	names := make(map[string]struct{})
	for _, patches := range applied {
		for _, patch := range patches {
			names[patch.Model] = struct{}{}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		if err := validateModelPricing(name, modelPricingValues(after, name), modelPricingValues(before, name)); err != nil {
			return &PricingPatchInvalidError{Model: name, Err: err}
		}
	}
	return nil
}

// lockedPricingTx 只在支持行锁的数据库上附加 FOR UPDATE。
//
// SQLite 不支持 FOR UPDATE。它是单写者模型：若另一事务在本事务读取之后提交了写入，
// 本事务升级为写事务时会拿到 SQLITE_BUSY / snapshot 冲突并整体回滚，而不是静默覆盖。
// 语义仍然安全，只是冲突表现为「事务失败需重试」而不是 ErrPricingVersionConflict。
func lockedPricingTx(tx *gorm.DB) *gorm.DB {
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		return tx
	}
	// Session(&gorm.Session{}) 不能省。tx.Clauses(...) 会把链式状态**固化**成一个具体的
	// Statement，此后在同一个实例上再调 .Where() 是**累加**而不是新起一条链。于是
	// 「先读版本行、再读价格行」会退化成
	//     WHERE key = 'PricingConfigVersion' AND key = 'ModelRatio'
	// 命中 0 行，FirstOrCreate 报 record not found —— 线上 apply_price 就是这么整条挂掉的。
	//
	// 这个坑只在非 SQLite 上出现：SQLite 分支原样返回 tx，没有固化 Statement，每条链
	// 都是干净的。所以跑在内存 SQLite 上的那批单测完全看不到它（见
	// TestPatchPricingOptions_ReusedLockedTxOnRealDB）。
	//
	// 加 Session 之后，后续每条链都在克隆的 Statement 上构造，Locking 子句照常保留。
	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).Session(&gorm.Session{})
}

func readPricingVersionForUpdate(tx *gorm.DB) (int64, error) {
	var option Option
	// key 是三种数据库里的保留字，列名引用方式不同，走 model/main.go 的 commonKeyCol。
	err := tx.Where(commonKeyCol+" = ?", PricingConfigVersionKey).Take(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if option.Value == "" {
		return 0, nil
	}
	version, parseErr := strconv.ParseInt(option.Value, 10, 64)
	if parseErr != nil {
		return 0, nil
	}
	return version, nil
}

func readPricingOptionForUpdate(tx *gorm.DB, optionKey string) (map[string]float64, Option, error) {
	option := Option{Key: optionKey}
	if err := tx.Where(Option{Key: optionKey}).FirstOrCreate(&option).Error; err != nil {
		return nil, option, err
	}
	if option.Value == "" {
		// 这一项从没保存过：计费用的是内存里的内置默认表。必须以它为底打补丁——写回后
		// updateOptionMap 会整表替换内存，只写被改的那个键会把其它模型的默认价全部冲掉。
		return pricingMemoryValues(optionKey), option, nil
	}
	values := make(map[string]float64)
	if err := common.UnmarshalJsonStr(option.Value, &values); err != nil {
		return nil, option, err
	}
	return values, option, nil
}

// pricingEffectiveValue 返回某个价格 option 里模型**当前实际生效**的值。
//
// ratio_setting 的内存映射在 init 时就 AddAll 了内置默认值（见 model_ratio.go 的
// modelRatioMap.AddAll(defaultModelRatio)），所以它代表的是「计费真正用的值」，
// 正是 expected 应该比对的对象；options 表里只有被显式保存过的键。
//
// 已保存过的 option 只用它**比对**、不并入写回（从没保存过的 option 见 readPricingOptionForUpdate）。
func pricingEffectiveValue(optionKey, modelName string) (float64, bool) {
	value, ok := pricingMemoryValues(optionKey)[modelName]
	return value, ok
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

func pricingValueMatches(expected *float64, current float64, exists bool) bool {
	if expected == nil {
		return !exists
	}
	return exists && nearlyEqualPricingValue(current, *expected)
}

func nearlyEqualPricingValue(left, right float64) bool {
	return math.Abs(left-right) < pricingFloatEpsilon
}

// PricingPatchInvalidError 表示模型 Model 改价后的完整价格未通过校验；Err 为校验器给出的原因。
// errors.Is(err, ErrPricingPatchInvalid) 为真，errors.Unwrap 返回 Err。
type PricingPatchInvalidError struct {
	Model string
	Err   error
}

func (e *PricingPatchInvalidError) Error() string {
	return fmt.Sprintf("%s: model %s: %v", ErrPricingPatchInvalid.Error(), e.Model, e.Err)
}

func (e *PricingPatchInvalidError) Is(target error) bool {
	return target == ErrPricingPatchInvalid
}

func (e *PricingPatchInvalidError) Unwrap() error {
	return e.Err
}
