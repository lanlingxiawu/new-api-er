package model

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ============================================================================
// EmployeeTierLevel 进程内缓存（Redis 未启用时的降级）
// ============================================================================

type tierLevelMemEntry struct {
	level     EmployeeTierLevel
	expiresAt time.Time
}

const tierLevelMemCacheTTL = 5 * time.Minute

var tierLevelMemCache sync.Map // map[int]tierLevelMemEntry

func getTierLevelFromMem(userId int) *EmployeeTierLevel {
	v, ok := tierLevelMemCache.Load(userId)
	if !ok {
		return nil
	}
	entry := v.(tierLevelMemEntry)
	if time.Now().After(entry.expiresAt) {
		tierLevelMemCache.Delete(userId)
		return nil
	}
	level := entry.level
	return &level
}

func setTierLevelToMem(level *EmployeeTierLevel) {
	if level == nil {
		return
	}
	tierLevelMemCache.Store(level.UserId, tierLevelMemEntry{
		level:     *level,
		expiresAt: time.Now().Add(operation_setting.GetLedgerPipelineSetting().GetJitteredCacheTTL(operation_setting.CacheIdxTierLevel)),
	})
}

func deleteTierLevelFromMem(userId int) {
	tierLevelMemCache.Delete(userId)
}

// ============================================================================
// EmployeeTierLevel Redis 缓存
// ============================================================================

const tierLevelRedisKeyPrefix = "employee_tier_level:"

func tierLevelRedisKey(userId int) string {
	return fmt.Sprintf("%s%d", tierLevelRedisKeyPrefix, userId)
}

func getTierLevelFromRedis(userId int) *EmployeeTierLevel {
	if !common.RedisEnabled {
		return nil
	}
	val, err := common.RedisGet(tierLevelRedisKey(userId))
	if err != nil {
		return nil
	}
	var level EmployeeTierLevel
	if err := common.Unmarshal([]byte(val), &level); err != nil {
		return nil
	}
	return &level
}

func setTierLevelToRedis(level *EmployeeTierLevel) {
	if !common.RedisEnabled || level == nil {
		return
	}
	data, err := common.Marshal(level)
	if err != nil {
		return
	}
	if err := common.RedisSet(tierLevelRedisKey(level.UserId), string(data), 0); err != nil {
		common.SysError("setTierLevelToRedis: " + err.Error())
	}
}

// setTierLevelToRedisNX 仅在 key 不存在时写入（缓存回填专用）。
// 用 Lua 脚本保证 EXISTS + SET 原子执行，防止并发回填覆盖更新侧写入的新值。
func setTierLevelToRedisNX(level *EmployeeTierLevel) {
	if !common.RedisEnabled || level == nil {
		return
	}
	data, err := common.Marshal(level)
	if err != nil {
		return
	}
	lua := `
		if redis.call("EXISTS", KEYS[1]) == 0 then
			redis.call("SET", KEYS[1], ARGV[1])
			return 1
		end
		return 0
	`
	ctx := context.Background()
	if err := common.RDB.Eval(ctx, lua, []string{tierLevelRedisKey(level.UserId)}, string(data)).Err(); err != nil {
		common.SysError(fmt.Sprintf("setTierLevelToRedisNX: userId=%d err=%s", level.UserId, err.Error()))
	}
}

func deleteTierLevelFromRedis(userId int) {
	if !common.RedisEnabled {
		return
	}
	if err := common.RedisDelKey(tierLevelRedisKey(userId)); err != nil {
		common.SysError(fmt.Sprintf("deleteTierLevelFromRedis: userId=%d err=%s", userId, err.Error()))
	}
}

func refreshTierLevelCacheFromDB(userId int) {
	refreshTierLevelCacheFromDBByUserIds([]int{userId})
}

func refreshTierLevelCacheFromDBByUserIds(userIds []int) {
	if !common.RedisEnabled || len(userIds) == 0 {
		return
	}
	unique := make(map[int]struct{}, len(userIds))
	ids := make([]int, 0, len(userIds))
	for _, userId := range userIds {
		if userId <= 0 {
			continue
		}
		if _, ok := unique[userId]; ok {
			continue
		}
		unique[userId] = struct{}{}
		ids = append(ids, userId)
	}
	if len(ids) == 0 {
		return
	}
	var levels []*EmployeeTierLevel
	if err := DB.Where("user_id IN ?", ids).Find(&levels).Error; err != nil {
		common.SysError("refreshTierLevelCacheFromDBByUserIds: " + err.Error())
		return
	}
	found := make(map[int]struct{}, len(levels))
	for _, level := range levels {
		found[level.UserId] = struct{}{}
		setTierLevelToRedis(level)
		setTierLevelToMem(level)
	}
	for _, userId := range ids {
		if _, ok := found[userId]; !ok {
			deleteTierLevelFromRedis(userId)
			deleteTierLevelFromMem(userId)
		}
	}
}

// ============================================================================
// 数据模型
// ============================================================================

// EmployeeCommissionTier 阶梯提成等级配置（全局共享）。
// Level 为正整数等级编号（1、2、3…），级别越大越高。
// Group 为同一等级内的分组名称（默认"通用"），同 Level+Group 唯一。
// ThresholdUsd 为员工累计利润（USD）达到该等级的最低门槛。
type EmployeeCommissionTier struct {
	Id           int64   `json:"id"`
	Level        int     `json:"level" gorm:"not null;default:1;uniqueIndex:uniq_tier_level_group"`
	Group        string  `json:"group" gorm:"column:tier_group;not null;default:'通用';uniqueIndex:uniq_tier_level_group"`
	ThresholdUsd float64 `json:"threshold_usd" gorm:"column:threshold_usd;not null;default:0"`
	Rate         float64 `json:"rate" gorm:"not null;default:0"`
	CreatedAt    int64   `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt    int64   `json:"updated_at" gorm:"autoUpdateTime"`
}

// EmployeeTierLevel 员工当前等级（每人一条，按需懒创建）。
// tier_id=0 表示尚未达到任何等级。
// source: auto=系统自动升级 / manual=管理员手动调整 / custom=员工自定义。
type EmployeeTierLevel struct {
	Id          int64  `json:"id"`
	UserId      int    `json:"user_id" gorm:"uniqueIndex;not null"`
	TierId      int64  `json:"tier_id" gorm:"not null;default:0"`
	Source      string `json:"source" gorm:"type:varchar(16);default:'auto'"`
	EffectiveAt int64  `json:"effective_at" gorm:"default:0"`
	Remark      string `json:"remark,omitempty" gorm:"type:varchar(256);default:''"`
	UpdatedBy   int    `json:"updated_by,omitempty" gorm:"default:0"`

	// BaselineProfitQuota/BaselineCommissionQuota 为上次月度重置时的快照值（quota 单位）。
	// "本期"值 = 对应累计值 - 基准值（clamp >=0）。默认 0，未发生过重置时等价于"本期=累计"。
	BaselineProfitQuota      int64 `json:"baseline_profit_quota" gorm:"column:baseline_profit_quota;not null;default:0"`
	BaselineConsumptionQuota int64 `json:"baseline_consumption_quota" gorm:"column:baseline_consumption_quota;not null;default:0"`
	BaselineCostQuota        int64 `json:"baseline_cost_quota" gorm:"column:baseline_cost_quota;not null;default:0"`
	BaselineCommissionQuota  int64 `json:"baseline_commission_quota" gorm:"column:baseline_commission_quota;not null;default:0"`
	BaselineResetAt          int64 `json:"baseline_reset_at" gorm:"column:baseline_reset_at;not null;default:0"`
}

// EmployeeTierLog 等级变更日志（只追加，用于审计溯源）。
// ProfitSnapshotUsd 记录触发时员工的累计利润（USD），仅 auto 时有值。
type EmployeeTierLog struct {
	Id                int64   `json:"id"`
	UserId            int     `json:"user_id" gorm:"index;not null"`
	FromTierId        int64   `json:"from_tier_id" gorm:"default:0"`
	ToTierId          int64   `json:"to_tier_id" gorm:"not null"`
	Source            string  `json:"source" gorm:"type:varchar(16);default:'auto'"`
	ProfitSnapshotUsd float64 `json:"profit_snapshot_usd" gorm:"column:profit_snapshot_usd;default:0"`
	OperatedAt        int64   `json:"operated_at" gorm:"autoCreateTime"`
	OperatedBy        int     `json:"operated_by,omitempty" gorm:"default:0"`
}

// ============================================================================
// 等级配置缓存（同 GetChannelCostRatio 模式）
// ============================================================================

var (
	tierCache          []*EmployeeCommissionTier
	tierCacheLock      sync.RWMutex
	tierCacheExpiresAt time.Time
)

const tierCacheRedisKey = "employee_commission_tiers"

// InvalidateTierCache 清空等级缓存（配置变更时调用）。
func InvalidateTierCache() {
	if common.RedisEnabled {
		_ = common.RedisDelKey(tierCacheRedisKey)
	}
	tierCacheLock.Lock()
	tierCache = nil
	tierCacheExpiresAt = time.Time{}
	tierCacheLock.Unlock()
}

// GetAllTiersCached 返回按 tier_group ASC, level ASC 排序的全量等级列表（带缓存）。
func GetAllTiersCached() []*EmployeeCommissionTier {
	tierCacheLock.RLock()
	if tierCache != nil && time.Now().Before(tierCacheExpiresAt) {
		cached := tierCache
		tierCacheLock.RUnlock()
		return cached
	}
	tierCacheLock.RUnlock()

	tiers := loadTiersFromDB()

	tierCacheLock.Lock()
	tierCache = tiers
	tierCacheExpiresAt = time.Now().Add(operation_setting.GetLedgerPipelineSetting().GetJitteredCacheTTL(operation_setting.CacheIdxTierDefinitions))
	tierCacheLock.Unlock()

	return tiers
}

func loadTiersFromDB() []*EmployeeCommissionTier {
	var tiers []*EmployeeCommissionTier
	_ = DB.Order("tier_group ASC, level ASC").Find(&tiers).Error
	return tiers
}

// ============================================================================
// EmployeeCommissionTier CRUD
// ============================================================================

func GetAllTiers() ([]*EmployeeCommissionTier, error) {
	var tiers []*EmployeeCommissionTier
	err := DB.Order("tier_group ASC, level ASC").Find(&tiers).Error
	return tiers, err
}

func GetTiers(page, pageSize int) ([]*EmployeeCommissionTier, int64, error) {
	var tiers []*EmployeeCommissionTier
	var total int64
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	tx := DB.Model(&EmployeeCommissionTier{})
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	if err := tx.Order("tier_group ASC, level ASC").Offset(offset).Limit(pageSize).Find(&tiers).Error; err != nil {
		return nil, 0, err
	}
	return tiers, total, nil
}

func CreateTier(tier *EmployeeCommissionTier) error {
	err := DB.Create(tier).Error
	if err == nil {
		InvalidateTierCache()
	}
	return err
}

func UpdateTier(tier *EmployeeCommissionTier) error {
	var count int64
	if err := DB.Model(&EmployeeCommissionTier{}).Where("id = ?", tier.Id).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return gorm.ErrRecordNotFound
	}
	err := DB.Model(tier).Updates(map[string]interface{}{
		"level":         tier.Level,
		"tier_group":    tier.Group,
		"threshold_usd": tier.ThresholdUsd,
		"rate":          tier.Rate,
	}).Error
	if err == nil {
		InvalidateTierCache()
	}
	return err
}

func DeleteTier(id int64) error {
	var refCount int64
	if err := DB.Model(&EmployeeTierLevel{}).Where("tier_id = ?", id).Count(&refCount).Error; err != nil {
		return err
	}
	if refCount > 0 {
		return fmt.Errorf("该提成等级已有 %d 名员工使用，请先迁移员工后再删除", refCount)
	}
	err := DB.Delete(&EmployeeCommissionTier{}, "id = ?", id).Error
	if err == nil {
		InvalidateTierCache()
	}
	return err
}

func TierExists(id int64) (bool, error) {
	var count int64
	if err := DB.Model(&EmployeeCommissionTier{}).Where("id = ?", id).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// ============================================================================
// EmployeeTierLevel — 当前等级读写
// ============================================================================

// GetOrCreateTierLevel 获取员工当前等级记录，不存在则懒创建（tier_id=0）。
// readCache 可选：传 true 时读取顺序为 Redis → DB（命中 DB 后回写 Redis）。
func GetOrCreateTierLevel(userId int, readCacheOpt ...bool) (*EmployeeTierLevel, error) {
	return GetOrCreateTierLevelWithContext(context.Background(), userId, readCacheOpt...)
}

func GetOrCreateTierLevelWithContext(ctx context.Context, userId int, readCacheOpt ...bool) (*EmployeeTierLevel, error) {
	readCache := len(readCacheOpt) > 0 && readCacheOpt[0]
	if readCache {
		// L1: 进程内内存缓存（纳秒级），始终优先，避免 Redis 网络开销。
		if level := getTierLevelFromMem(userId); level != nil {
			return level, nil
		}
		// L2: Redis（跨实例共享），命中后回填内存缓存。
		if level := getTierLevelFromRedis(userId); level != nil {
			setTierLevelToMem(level)
			return level, nil
		}
	}
	var level EmployeeTierLevel
	err := DB.WithContext(ctx).Where("user_id = ?", userId).First(&level).Error
	if err == gorm.ErrRecordNotFound {
		level = EmployeeTierLevel{UserId: userId}
		result := DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&level)
		if result.Error != nil {
			return nil, result.Error
		}
		// 并发插入时 OnConflict DO NOTHING 可能不填充 id，重新查一次
		if level.Id == 0 {
			if err2 := DB.WithContext(ctx).Where("user_id = ?", userId).First(&level).Error; err2 != nil {
				return nil, err2
			}
		}
		setTierLevelToRedisNX(&level)
		setTierLevelToMem(&level)
		return &level, nil
	}
	if err != nil {
		return nil, err
	}
	setTierLevelToRedisNX(&level)
	setTierLevelToMem(&level)
	return &level, nil
}

func ensureTierLevelsForUserIds(userIds []int) error {
	unique := make(map[int]struct{}, len(userIds))
	rows := make([]EmployeeTierLevel, 0, len(userIds))
	for _, userId := range userIds {
		if userId <= 0 {
			continue
		}
		if _, ok := unique[userId]; ok {
			continue
		}
		unique[userId] = struct{}{}
		rows = append(rows, EmployeeTierLevel{UserId: userId})
	}
	if len(rows) == 0 {
		return nil
	}
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, 500).Error; err != nil {
		return err
	}
	return nil
}

// SetTierLevel 更新员工等级（admin/system 调用）并写入变更日志。
func SetTierLevel(userId int, newTierId int64, source string, operatedBy int, remark string, profitSnapshotUsd float64) error {
	if newTierId > 0 {
		exists, err := TierExists(newTierId)
		if err != nil {
			return err
		}
		if !exists {
			return gorm.ErrRecordNotFound
		}
	}

	level, err := GetOrCreateTierLevel(userId, false)
	if err != nil {
		return err
	}
	fromTierId := level.TierId

	now := time.Now().Unix()

	if err := DB.Transaction(func(tx *gorm.DB) error {
		// 写 DB；若记录不存在则创建（防止 GetOrCreateTierLevel 与本次写入之间记录被删除的极端情况）
		result := tx.Model(&EmployeeTierLevel{}).Where("user_id = ?", userId).Updates(map[string]interface{}{
			"tier_id":      newTierId,
			"source":       source,
			"effective_at": now,
			"remark":       remark,
			"updated_by":   operatedBy,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			newRecord := &EmployeeTierLevel{
				UserId:      userId,
				TierId:      newTierId,
				Source:      source,
				EffectiveAt: now,
				Remark:      remark,
				UpdatedBy:   operatedBy,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "user_id"}},
				DoUpdates: clause.AssignmentColumns([]string{"tier_id", "source", "effective_at", "remark", "updated_by"}),
			}).Create(newRecord).Error; err != nil {
				return err
			}
		}

		log := &EmployeeTierLog{
			UserId:            userId,
			FromTierId:        fromTierId,
			ToTierId:          newTierId,
			Source:            source,
			ProfitSnapshotUsd: profitSnapshotUsd,
			OperatedBy:        operatedBy,
		}
		return tx.Create(log).Error
	}); err != nil {
		return err
	}

	level.TierId = newTierId
	level.Source = source
	level.EffectiveAt = now
	level.Remark = remark
	level.UpdatedBy = operatedBy
	if common.RedisEnabled {
		// refreshTierLevelCacheFromDB 从 DB 重新读取并同时写入 Redis 和内存缓存
		refreshTierLevelCacheFromDB(userId)
	} else {
		// 无 Redis 时 refreshTierLevelCacheFromDB 是 no-op，直接用已更新的 level 写内存
		setTierLevelToMem(level)
	}

	return nil
}

// TryAutoUpgradeTier 检查员工是否应升级等级，若是则自动升级（只升不降）。
// 本期利润从 employee_commission_reset_period_daily_stats 聚合，无需外部传入累计值。
// 升级时保持员工当前分组（Group）不变；若当前无分组则默认使用"通用"分组。
func TryAutoUpgradeTier(userId int) {
	tiers := GetAllTiersCached()
	if len(tiers) == 0 {
		return
	}

	level, err := GetOrCreateTierLevel(userId, true)
	if err != nil {
		return
	}

	// 确定当期 reset_started_at：有明确重置记录则用 BaselineResetAt，
	// 否则退回到当前自然月的周期起始时间。
	resetStartedAt := level.BaselineResetAt
	if resetStartedAt == 0 {
		resetStartedAt = ResolveCommissionMonthlyPeriod(time.Now().Unix()).PeriodStartAt
	}

	// 本期利润 = SUM(profit_quota) FROM employee_commission_reset_period_daily_stats
	var periodProfit int64
	if err := DB.Model(&EmployeeCommissionResetPeriodDailyStat{}).
		Select("COALESCE(SUM(profit_quota), 0)").
		Where("employee_user_id = ? AND reset_started_at = ?", userId, resetStartedAt).
		Scan(&periodProfit).Error; err != nil {
		common.SysError(fmt.Sprintf("TryAutoUpgradeTier: query period profit failed userId=%d err=%s", userId, err.Error()))
		return
	}
	currentProfitUsd := common.QuotaToUSD(periodProfit)

	// 获取当前分组与等级
	currentGroup := "通用"
	currentLevel := 0
	if level.TierId != 0 {
		found := false
		for _, t := range tiers {
			if t.Id == level.TierId {
				currentGroup = t.Group
				currentLevel = t.Level
				found = true
				break
			}
		}
		if !found {
			// 员工绑定的等级已被删除，跳过自动升级，避免静默切换到其他分组
			common.SysError(fmt.Sprintf("TryAutoUpgradeTier: userId=%d has orphaned tierId=%d, skipping auto upgrade", userId, level.TierId))
			return
		}
	}

	// 在当前分组内找到可达的最高等级（同分组内 tiers 已按 level ASC 排序）
	var bestTier *EmployeeCommissionTier
	for _, t := range tiers {
		if t.Group != currentGroup {
			continue
		}
		if currentProfitUsd >= t.ThresholdUsd {
			bestTier = t
		}
	}

	if bestTier == nil || bestTier.Id == level.TierId {
		return
	}
	// 只升不降：按等级比较
	if bestTier.Level <= currentLevel {
		return
	}

	_ = SetTierLevel(userId, bestTier.Id, "auto", 0, "", currentProfitUsd)
}

// tryAutoUpgradeTierBatch 批量检查并升级员工等级（只升不降）。
// 相比对每个员工串行调用 TryAutoUpgradeTier，本函数将 N 次 SUM 查询合并为 1 次，
// 适用于 flush 场景（一次冲洗多个员工时调用）。
//
// 逻辑与 TryAutoUpgradeTier 完全等价：
//   - 用 baseline_reset_at 确定当期起始，若为 0 则退回自然月起点
//   - 单次批量 SUM(profit_quota) GROUP BY (employee_user_id, reset_started_at)
//   - 仅对本期 reset_started_at 的利润行做升级判定
func tryAutoUpgradeTierBatch(userIds []int) {
	if len(userIds) == 0 {
		return
	}
	tiers := GetAllTiersCached()
	if len(tiers) == 0 {
		return
	}
	tierById := make(map[int64]*EmployeeCommissionTier, len(tiers))
	for _, t := range tiers {
		tierById[t.Id] = t
	}

	// 1. 确保所有员工都有 employee_tier_levels 记录（对应旧路径 GetOrCreateTierLevel 的懒创建）。
	// 新员工在第一次月度重置前不会有记录，必须在此处补建，否则升档判断会跳过他们。
	if err := ensureTierLevelsForUserIds(userIds); err != nil {
		common.SysError("tryAutoUpgradeTierBatch: ensure tier levels failed: " + err.Error())
		// 不 return：ensureTierLevelsForUserIds 失败通常是部分失败，已有记录的员工仍可继续。
	}

	// 2. 批量加载等级记录（1 次 DB 查询替代 N 次 GetOrCreateTierLevel）
	levelMap, err := GetTierLevelsByUserIds(userIds)
	if err != nil {
		common.SysError("tryAutoUpgradeTierBatch: load tier levels failed: " + err.Error())
		return
	}

	// 3. 确定每位员工的当期 reset_started_at，并收集唯一值用于下推过滤
	currentPeriod := ResolveCommissionMonthlyPeriod(time.Now().Unix())
	resetStartedAtByUser := make(map[int]int64, len(userIds))
	uniqueResets := make([]int64, 0, 2)
	seenResets := make(map[int64]bool, 2)
	for _, uid := range userIds {
		var rst int64
		if lv := levelMap[uid]; lv != nil && lv.BaselineResetAt > 0 {
			rst = lv.BaselineResetAt
		} else {
			rst = currentPeriod.PeriodStartAt
		}
		resetStartedAtByUser[uid] = rst
		if !seenResets[rst] {
			seenResets[rst] = true
			uniqueResets = append(uniqueResets, rst)
		}
	}

	// 4. 单次批量 SUM 查询（原来 N 次串行查询 → 1 次）
	type profitRow struct {
		EmployeeUserId int
		ResetStartedAt int64
		PeriodProfit   int64
	}
	var profitRows []profitRow
	if err := DB.Model(&EmployeeCommissionResetPeriodDailyStat{}).
		Select("employee_user_id, reset_started_at, COALESCE(SUM(profit_quota), 0) as period_profit").
		Where("employee_user_id IN ? AND reset_started_at IN ?", userIds, uniqueResets).
		Group("employee_user_id, reset_started_at").
		Scan(&profitRows).Error; err != nil {
		common.SysError("tryAutoUpgradeTierBatch: query period profits failed: " + err.Error())
		return
	}
	profitByUser := make(map[int]int64, len(profitRows))
	for _, row := range profitRows {
		if resetStartedAtByUser[row.EmployeeUserId] == row.ResetStartedAt {
			profitByUser[row.EmployeeUserId] = row.PeriodProfit
		}
	}

	// 5. 逐员工判断是否需要升级
	for _, uid := range userIds {
		level := levelMap[uid]
		if level == nil {
			continue
		}
		currentProfitUsd := common.QuotaToUSD(profitByUser[uid])

		currentGroup := "通用"
		currentLevel := 0
		if level.TierId != 0 {
			if ct, ok := tierById[level.TierId]; ok {
				currentGroup = ct.Group
				currentLevel = ct.Level
			} else {
				common.SysError(fmt.Sprintf("tryAutoUpgradeTierBatch: userId=%d has orphaned tierId=%d, skipping", uid, level.TierId))
				continue
			}
		}

		var bestTier *EmployeeCommissionTier
		for _, t := range tiers {
			if t.Group != currentGroup {
				continue
			}
			if currentProfitUsd >= t.ThresholdUsd {
				bestTier = t
			}
		}
		if bestTier == nil || bestTier.Id == level.TierId {
			continue
		}
		if bestTier.Level <= currentLevel {
			continue
		}
		_ = SetTierLevel(uid, bestTier.Id, "auto", 0, "", currentProfitUsd)
	}
}

// getTierThresholdUsd 在已排序列表中查找指定 tier_id 的 USD 门槛，未找到返回 -1。
func getTierThresholdUsd(tiers []*EmployeeCommissionTier, tierId int64) float64 {
	for _, t := range tiers {
		if t.Id == tierId {
			return t.ThresholdUsd
		}
	}
	return -1
}

// GetEffectiveCommissionRate 返回员工的有效提成率。
// 提成率统一由员工当前等级决定，不再支持自定义比例。
// 未绑定等级时返回 0（不计提成）。
func GetEffectiveCommissionRate(userId int) float64 {
	rate, _ := GetEffectiveCommissionRateWithContext(context.Background(), userId)
	return rate
}

func GetEffectiveCommissionRateWithContext(ctx context.Context, userId int) (float64, error) {
	level, err := GetOrCreateTierLevelWithContext(ctx, userId, true)
	if err != nil || level.TierId == 0 {
		return 0, err
	}
	tiers := GetAllTiersCached()
	for _, t := range tiers {
		if t.Id == level.TierId {
			return t.Rate, nil
		}
	}
	return 0, nil
}

// ============================================================================
// EmployeeTierLog 查询
// ============================================================================

type TierLogFilter struct {
	UserId   int
	Page     int
	PageSize int
}

func GetTierLogs(filter TierLogFilter) ([]*EmployeeTierLog, int64, error) {
	var logs []*EmployeeTierLog
	var total int64

	tx := DB.Model(&EmployeeTierLog{})
	if filter.UserId != 0 {
		tx = tx.Where("user_id = ?", filter.UserId)
	}
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (filter.Page - 1) * filter.PageSize
	if err := tx.Order("operated_at DESC, id DESC").Offset(offset).Limit(filter.PageSize).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

// GetTierLevelByUserId 获取指定员工的当前等级（附带等级信息），用于列表展示。
func GetTierLevelByUserId(userId int) (*EmployeeTierLevel, *EmployeeCommissionTier) {
	level, err := GetOrCreateTierLevel(userId, true)
	if err != nil || level.TierId == 0 {
		return level, nil
	}
	tiers := GetAllTiersCached()
	for _, t := range tiers {
		if t.Id == level.TierId {
			return level, t
		}
	}
	return level, nil
}

// GetTierLevelsByUserIds 批量获取员工当前等级，返回 userId -> EmployeeTierLevel map。
func GetTierLevelsByUserIds(userIds []int) (map[int]*EmployeeTierLevel, error) {
	if len(userIds) == 0 {
		return map[int]*EmployeeTierLevel{}, nil
	}
	var levels []*EmployeeTierLevel
	if err := DB.Where("user_id IN ?", userIds).Find(&levels).Error; err != nil {
		return nil, err
	}
	result := make(map[int]*EmployeeTierLevel, len(levels))
	for _, l := range levels {
		result[l.UserId] = l
	}
	return result, nil
}

// GetTierLevelsByUserIdsCached batch-loads tier levels for the given users: L1 memory
// hits are served first, the remaining users are loaded in ONE `WHERE user_id IN (...)`
// query, and the L1 cache is warmed. Unlike GetOrCreateTierLevel it does NOT lazily
// create missing rows — a missing level yields nil, which effectiveResetStartedAt
// treats the same as a level with BaselineResetAt=0 (the row is created later by
// tryAutoUpgradeTierBatch). Used by the settlement flush prefetch to collapse N serial
// mem/Redis/DB round-trips into a single batched DB query.
func GetTierLevelsByUserIdsCached(userIds []int) map[int]*EmployeeTierLevel {
	result := make(map[int]*EmployeeTierLevel, len(userIds))
	seen := make(map[int]struct{}, len(userIds))
	miss := make([]int, 0, len(userIds))
	for _, uid := range userIds {
		if uid <= 0 {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		if lvl := getTierLevelFromMem(uid); lvl != nil {
			result[uid] = lvl
			continue
		}
		miss = append(miss, uid)
	}
	if len(miss) == 0 {
		return result
	}
	levelMap, err := GetTierLevelsByUserIds(miss)
	if err != nil {
		common.SysError("GetTierLevelsByUserIdsCached: batch load failed: " + err.Error())
		return result
	}
	for _, uid := range miss {
		lvl := levelMap[uid]
		result[uid] = lvl // may be nil when the user has no tier-level row yet
		if lvl != nil {
			setTierLevelToMem(lvl)
		}
	}
	return result
}

// ============================================================================
// 月度周期重置
// ============================================================================

// ResetEmployeeTierLevelsForPeriod 批量重置员工等级并更新 baseline_reset_at。
//
// 对每条 baseline_reset_at < resetAt 的 EmployeeTierLevel：
//   - tier_id != 0：重置到该等级所在分组（Group）内 Level 最小的等级；
//   - tier_id == 0：保持不变（无分组上下文，无需调级）；
//   - 无论是否调级，均将 baseline_reset_at 更新为 resetAt，
//     并写入一条 source="reset" 的 EmployeeTierLog。
//
// baseline_reset_at < resetAt 承担"本周期是否已处理"的幂等守卫：处理后
// baseline_reset_at 被置为 resetAt，重复调用（如崩溃重启后）会自动跳过已处理的行。
// effective_at 仅记录等级重置实际执行时间。
//
// 本期利润从 employee_commission_reset_period_daily_stats 动态聚合，不再快照到
// baseline_profit_quota 等字段，重置逻辑无需提前聚合 user_extensions 数据。
//
// 调用方应循环调用直到 processed == 0；本函数本身不做日期/调度判断，
// resetAt 由调用方（定时任务或"立即重置"接口）计算后传入。
//
// operatedBy 写入 EmployeeTierLevel.UpdatedBy 与 EmployeeTierLog.OperatedBy：
// 定时任务传 0（系统），管理员"立即重置"传当前管理员的 user id。
func ResetEmployeeTierLevelsForPeriod(resetAt int64, batchSize int, operatedBy int) (processed int, selected int, err error) {
	if batchSize <= 0 {
		batchSize = 300
	}

	var employeeUserIds []int
	if err = DB.Model(&EmployeeProfile{}).Select("user_id").Where("status = ?", 1).Scan(&employeeUserIds).Error; err != nil {
		return 0, 0, err
	}
	if err = ensureTierLevelsForUserIds(employeeUserIds); err != nil {
		return 0, 0, err
	}

	// Load valid tiers before selecting rows so orphaned tier_id records do not
	// occupy an entire batch and block valid employees behind them.
	tiers := GetAllTiersCached()
	tierById := make(map[int64]*EmployeeCommissionTier, len(tiers))
	groupMinTier := make(map[string]*EmployeeCommissionTier, len(tiers))
	validTierIds := make([]int64, 0, len(tiers))
	for _, t := range tiers {
		tierById[t.Id] = t
		validTierIds = append(validTierIds, t.Id)
		if existing, ok := groupMinTier[t.Group]; !ok || t.Level < existing.Level {
			groupMinTier[t.Group] = t
		}
	}

	levelsToRefresh := make([]int, 0)

	if len(validTierIds) > 0 && len(employeeUserIds) > 0 {
		var orphanLevels []*EmployeeTierLevel
		if err = DB.Where("user_id IN ? AND baseline_reset_at < ? AND tier_id <> ? AND tier_id NOT IN ?", employeeUserIds, resetAt, 0, validTierIds).
			Order("id ASC").
			Limit(batchSize).
			Find(&orphanLevels).Error; err != nil {
			return 0, 0, err
		}
		if len(orphanLevels) > 0 {
			orphanProcessed := 0
			err = DB.Transaction(func(tx *gorm.DB) error {
				for _, level := range orphanLevels {
					operatedAt := time.Now().Unix()
					common.SysError(fmt.Sprintf("ResetEmployeeTierLevelsForPeriod: userId=%d has orphaned tierId=%d, refreshing baseline only", level.UserId, level.TierId))
					updates := map[string]interface{}{
						"source":            "reset",
						"effective_at":      operatedAt,
						"remark":            "月度自动重置",
						"updated_by":        operatedBy,
						"baseline_reset_at": resetAt,
					}
					res := tx.Model(&EmployeeTierLevel{}).Where("id = ? AND baseline_reset_at = ?", level.Id, level.BaselineResetAt).Updates(updates)
					if res.Error != nil {
						return res.Error
					}
					if res.RowsAffected == 0 {
						common.SysLog(fmt.Sprintf("ResetEmployeeTierLevelsForPeriod: orphan userId=%d skipped due to concurrent tier update", level.UserId))
						continue
					}
					levelsToRefresh = append(levelsToRefresh, level.UserId)
					orphanProcessed++
				}
				return nil
			})
			if err != nil {
				return 0, 0, err
			}
			// orphanProcessed 仅用于日志，不计入 processed（孤儿修复是维护操作，非完整月度重置）
			if orphanProcessed > 0 {
				common.SysLog(fmt.Sprintf("ResetEmployeeTierLevelsForPeriod: refreshed %d orphan baselines", orphanProcessed))
			}
			refreshTierLevelCacheFromDBByUserIds(levelsToRefresh)
			levelsToRefresh = levelsToRefresh[:0]
		}
	}

	if len(employeeUserIds) == 0 {
		return 0, 0, nil
	}
	var levels []*EmployeeTierLevel
	query := DB.Where("user_id IN ? AND baseline_reset_at < ?", employeeUserIds, resetAt)
	if len(validTierIds) > 0 {
		query = query.Where("tier_id = ? OR tier_id IN ?", 0, validTierIds)
	}
	if err = query.Order("id ASC").Limit(batchSize).Find(&levels).Error; err != nil {
		return 0, 0, err
	}
	if len(levels) == 0 {
		return 0, 0, nil
	}
	selected = len(levels)

	// 将 levels 拆成若干子批次，每批独立事务，控制单次持锁行数和时间。
	// 子批次失败不影响已提交批次（幂等守卫 baseline_reset_at 确保重试安全）。
	const resetSubBatchSize = 30
	for i := 0; i < len(levels); i += resetSubBatchSize {
		end := i + resetSubBatchSize
		if end > len(levels) {
			end = len(levels)
		}
		subBatch := levels[i:end]
		subRefresh := make([]int, 0, len(subBatch))

		subErr := DB.Transaction(func(tx *gorm.DB) error {
			for _, level := range subBatch {
				operatedAt := time.Now().Unix()

				targetTierId := level.TierId
				if level.TierId != 0 {
					if currentTier, ok := tierById[level.TierId]; ok {
						if minTier, ok2 := groupMinTier[currentTier.Group]; ok2 {
							targetTierId = minTier.Id
						}
					} else {
						common.SysError(fmt.Sprintf("ResetEmployeeTierLevelsForPeriod: userId=%d has orphaned tierId=%d, refreshing baseline only", level.UserId, level.TierId))
					}
				}

				updates := map[string]interface{}{
					"tier_id":           targetTierId,
					"source":            "reset",
					"effective_at":      operatedAt,
					"remark":            "月度自动重置",
					"updated_by":        operatedBy,
					"baseline_reset_at": resetAt,
				}
				res := tx.Model(&EmployeeTierLevel{}).Where("id = ? AND baseline_reset_at = ?", level.Id, level.BaselineResetAt).Updates(updates)
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected == 0 {
					common.SysLog(fmt.Sprintf("ResetEmployeeTierLevelsForPeriod: userId=%d skipped due to concurrent tier update", level.UserId))
					continue
				}
				subRefresh = append(subRefresh, level.UserId)

				log := &EmployeeTierLog{
					UserId:     level.UserId,
					FromTierId: level.TierId,
					ToTierId:   targetTierId,
					Source:     "reset",
					OperatedAt: operatedAt,
					OperatedBy: operatedBy,
				}
				if err := tx.Create(log).Error; err != nil {
					return err
				}
				processed++
			}
			return nil
		})
		if subErr != nil {
			// 本子批次失败：已提交的子批次保持（幂等），返回错误让调用方决定是否重试。
			err = subErr
			refreshTierLevelCacheFromDBByUserIds(levelsToRefresh)
			return processed, selected, err
		}
		levelsToRefresh = append(levelsToRefresh, subRefresh...)
		// 子批次提交后立即刷新缓存，缩短其他 goroutine 看到旧等级的窗口。
		refreshTierLevelCacheFromDBByUserIds(subRefresh)
	}

	return processed, selected, nil
}
