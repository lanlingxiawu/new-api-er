package model

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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
	}
	for _, userId := range ids {
		if _, ok := found[userId]; !ok {
			deleteTierLevelFromRedis(userId)
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

	// BaselineProfitQuota/BaselineCommissionQuota 为上次月度重置时
	// UserExtension.ProfitTotalQuota/CommissionTotalQuota 的快照值（quota 单位）。
	// "本期"值 = 对应累计值 - 基准值（结果 clamp 到 >=0）。默认 0，未发生过重置时
	// 等价于"本期=累计"，向后兼容。
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
	tierCache     []*EmployeeCommissionTier
	tierCacheLock sync.RWMutex
	tierCacheTime time.Time
	tierCacheTTL  = 5 * time.Minute
)

const tierCacheRedisKey = "employee_commission_tiers"

// InvalidateTierCache 清空等级缓存（配置变更时调用）。
func InvalidateTierCache() {
	if common.RedisEnabled {
		_ = common.RedisDelKey(tierCacheRedisKey)
	}
	tierCacheLock.Lock()
	tierCache = nil
	tierCacheTime = time.Time{}
	tierCacheLock.Unlock()
}

// GetAllTiersCached 返回按 tier_group ASC, level ASC 排序的全量等级列表（带缓存）。
func GetAllTiersCached() []*EmployeeCommissionTier {
	tierCacheLock.RLock()
	if !tierCacheTime.IsZero() && time.Since(tierCacheTime) < tierCacheTTL && tierCache != nil {
		cached := tierCache
		tierCacheLock.RUnlock()
		return cached
	}
	tierCacheLock.RUnlock()

	tiers := loadTiersFromDB()

	tierCacheLock.Lock()
	tierCache = tiers
	tierCacheTime = time.Now()
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
		if level := getTierLevelFromRedis(userId); level != nil {
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
		return &level, nil
	}
	if err != nil {
		return nil, err
	}
	setTierLevelToRedisNX(&level)
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
	refreshTierLevelCacheFromDB(userId)

	return nil
}

// TryAutoUpgradeTier 检查员工是否应升级等级，若是则自动升级（只升不降）。
// currentProfit 为刚更新后的 profit_total_quota（quota 单位），内部转换为 USD 与门槛比较。
// 升级时保持员工当前分组（Group）不变；若当前无分组则默认使用"通用"分组。
func TryAutoUpgradeTier(userId int, currentProfit int64) {
	tiers := GetAllTiersCached()
	if len(tiers) == 0 {
		return
	}

	// 找到当前利润（USD）能达到的最高等级（按 level ASC 已排序）
	level, err := GetOrCreateTierLevel(userId, true)
	if err != nil {
		return
	}

	// 周期利润 = 累计利润 - 上次重置基准（默认 0，向后兼容）。
	periodProfit := currentProfit - level.BaselineProfitQuota
	if periodProfit < 0 {
		periodProfit = 0
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

// ============================================================================
// 月度周期重置
// ============================================================================

// ResetEmployeeTierLevelsForPeriod 批量重置员工等级与"本期业绩/本期提成"基准。
//
// 对每条 baseline_reset_at < resetAt 的 EmployeeTierLevel：
//   - tier_id != 0：重置到该等级所在分组（Group）内 Level 最小的等级；
//   - tier_id == 0：保持不变（无分组上下文，无需调级）；
//   - 无论是否调级，均将 baseline_profit_quota/baseline_commission_quota
//     刷新为该员工当前 UserExtension.profit_total_quota/commission_total_quota
//     的快照值，并写入一条 source="reset" 的 EmployeeTierLog。
//
// baseline_reset_at < resetAt 承担"本周期是否已处理"的幂等守卫：处理后
// baseline_reset_at 被置为 resetAt，重复调用（如崩溃重启后）会自动跳过已处理的行。
// effective_at 仅记录等级重置实际执行时间。
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
			userIds := make([]int, 0, len(orphanLevels))
			for _, l := range orphanLevels {
				userIds = append(userIds, l.UserId)
			}
			extByUserId, err := GetUserExtensionsByUserIds(userIds)
			if err != nil {
				return 0, 0, err
			}
			customerConsumptionByUserId, err := GetCustomerUsedQuotaTotalsByEmployees(userIds)
			if err != nil {
				return 0, 0, err
			}
			stats, err := GetCommissionStatsByEmployeeIds(userIds)
			if err != nil {
				return 0, 0, err
			}
			costByUserId := make(map[int]int64, len(stats))
			for _, stat := range stats {
				costByUserId[stat.EmployeeUserId] = stat.TotalCost
			}
			orphanProcessed := 0
			err = DB.Transaction(func(tx *gorm.DB) error {
				for _, level := range orphanLevels {
					operatedAt := time.Now().Unix()
					var consumptionTotal, costTotal, profitTotal, commissionTotal int64
					consumptionTotal = customerConsumptionByUserId[level.UserId]
					costTotal = costByUserId[level.UserId]
					if ext, ok := extByUserId[level.UserId]; ok {
						profitTotal = ext.ProfitTotalQuota
						commissionTotal = ext.CommissionTotalQuota
					}
					common.SysError(fmt.Sprintf("ResetEmployeeTierLevelsForPeriod: userId=%d has orphaned tierId=%d, refreshing baseline only", level.UserId, level.TierId))

					updated := *level
					updated.Source = "reset"
					updated.EffectiveAt = operatedAt
					updated.Remark = "月度自动重置"
					updated.UpdatedBy = operatedBy
					updated.BaselineConsumptionQuota = consumptionTotal
					updated.BaselineCostQuota = costTotal
					updated.BaselineProfitQuota = profitTotal
					updated.BaselineCommissionQuota = commissionTotal
					updated.BaselineResetAt = resetAt

					updates := map[string]interface{}{
						"source":                     "reset",
						"effective_at":               operatedAt,
						"remark":                     "月度自动重置",
						"updated_by":                 operatedBy,
						"baseline_consumption_quota": consumptionTotal,
						"baseline_cost_quota":        costTotal,
						"baseline_profit_quota":      profitTotal,
						"baseline_commission_quota":  commissionTotal,
						"baseline_reset_at":          resetAt,
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

	userIds := make([]int, 0, len(levels))
	for _, l := range levels {
		userIds = append(userIds, l.UserId)
	}
	extByUserId, err := GetUserExtensionsByUserIds(userIds)
	if err != nil {
		return 0, selected, err
	}
	customerConsumptionByUserId, err := GetCustomerUsedQuotaTotalsByEmployees(userIds)
	if err != nil {
		return 0, selected, err
	}
	stats, err := GetCommissionStatsByEmployeeIds(userIds)
	if err != nil {
		return 0, selected, err
	}
	costByUserId := make(map[int]int64, len(stats))
	for _, stat := range stats {
		costByUserId[stat.EmployeeUserId] = stat.TotalCost
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		for _, level := range levels {
			operatedAt := time.Now().Unix()
			var consumptionTotal, costTotal, profitTotal, commissionTotal int64
			consumptionTotal = customerConsumptionByUserId[level.UserId]
			costTotal = costByUserId[level.UserId]
			if ext, ok := extByUserId[level.UserId]; ok {
				profitTotal = ext.ProfitTotalQuota
				commissionTotal = ext.CommissionTotalQuota
			}

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

			updated := *level
			updated.TierId = targetTierId
			updated.Source = "reset"
			updated.EffectiveAt = operatedAt
			updated.Remark = "月度自动重置"
			updated.UpdatedBy = operatedBy
			updated.BaselineConsumptionQuota = consumptionTotal
			updated.BaselineCostQuota = costTotal
			updated.BaselineProfitQuota = profitTotal
			updated.BaselineCommissionQuota = commissionTotal
			updated.BaselineResetAt = resetAt

			updates := map[string]interface{}{
				"tier_id":                    targetTierId,
				"source":                     "reset",
				"effective_at":               operatedAt,
				"remark":                     "月度自动重置",
				"updated_by":                 operatedBy,
				"baseline_consumption_quota": consumptionTotal,
				"baseline_cost_quota":        costTotal,
				"baseline_profit_quota":      profitTotal,
				"baseline_commission_quota":  commissionTotal,
				"baseline_reset_at":          resetAt,
			}
			res := tx.Model(&EmployeeTierLevel{}).Where("id = ? AND baseline_reset_at = ?", level.Id, level.BaselineResetAt).Updates(updates)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				common.SysLog(fmt.Sprintf("ResetEmployeeTierLevelsForPeriod: userId=%d skipped due to concurrent tier update", level.UserId))
				continue
			}
			levelsToRefresh = append(levelsToRefresh, level.UserId)

			log := &EmployeeTierLog{
				UserId:            level.UserId,
				FromTierId:        level.TierId,
				ToTierId:          targetTierId,
				Source:            "reset",
				ProfitSnapshotUsd: common.QuotaToUSD(profitTotal),
				OperatedAt:        operatedAt,
				OperatedBy:        operatedBy,
			}
			if err := tx.Create(log).Error; err != nil {
				return err
			}
			processed++
		}
		return nil
	})
	if err != nil {
		return 0, selected, err
	}
	refreshTierLevelCacheFromDBByUserIds(levelsToRefresh)

	return processed, selected, nil
}
