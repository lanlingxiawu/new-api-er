package model

import (
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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
func GetOrCreateTierLevel(userId int) (*EmployeeTierLevel, error) {
	var level EmployeeTierLevel
	err := DB.Where("user_id = ?", userId).First(&level).Error
	if err == gorm.ErrRecordNotFound {
		level = EmployeeTierLevel{UserId: userId}
		result := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&level)
		if result.Error != nil {
			return nil, result.Error
		}
		// 并发插入时 OnConflict DO NOTHING 可能不填充 id，重新查一次
		if level.Id == 0 {
			if err2 := DB.Where("user_id = ?", userId).First(&level).Error; err2 != nil {
				return nil, err2
			}
		}
		return &level, nil
	}
	return &level, err
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

	level, err := GetOrCreateTierLevel(userId)
	if err != nil {
		return err
	}
	fromTierId := level.TierId

	now := time.Now().Unix()
	if err := DB.Model(&EmployeeTierLevel{}).Where("user_id = ?", userId).Updates(map[string]interface{}{
		"tier_id":      newTierId,
		"source":       source,
		"effective_at": now,
		"remark":       remark,
		"updated_by":   operatedBy,
	}).Error; err != nil {
		return err
	}

	log := &EmployeeTierLog{
		UserId:            userId,
		FromTierId:        fromTierId,
		ToTierId:          newTierId,
		Source:            source,
		ProfitSnapshotUsd: profitSnapshotUsd,
		OperatedBy:        operatedBy,
	}
	return DB.Create(log).Error
}

// TryAutoUpgradeTier 检查员工是否应升级等级，若是则自动升级（只升不降）。
// currentProfit 为刚更新后的 profit_total_quota（quota 单位），内部转换为 USD 与门槛比较。
// 升级时保持员工当前分组（Group）不变；若当前无分组则默认使用"通用"分组。
func TryAutoUpgradeTier(userId int, currentProfit int64) {
	tiers := GetAllTiersCached()
	if len(tiers) == 0 {
		return
	}

	currentProfitUsd := common.QuotaToUSD(currentProfit)

	level, err := GetOrCreateTierLevel(userId)
	if err != nil {
		return
	}

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
	level, err := GetOrCreateTierLevel(userId)
	if err != nil || level.TierId == 0 {
		return 0
	}
	tiers := GetAllTiersCached()
	for _, t := range tiers {
		if t.Id == level.TierId {
			return t.Rate
		}
	}
	return 0
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
	level, err := GetOrCreateTierLevel(userId)
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
