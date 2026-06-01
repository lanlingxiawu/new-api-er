package model

import (
	"sync"
	"time"

	"gorm.io/gorm"
)

// ============================================================================
// EmployeeProfile — 员工档案
// ============================================================================

// EmployeeProfile 记录员工的提成配置。员工本身是普通用户（users 表），
// 通过 user_id 关联；管理员创建此记录后该用户即具备员工身份。
type EmployeeProfile struct {
	Id             int    `json:"id"`
	UserId         int    `json:"user_id" gorm:"uniqueIndex;not null"`
	CommissionRate float64 `json:"commission_rate" gorm:"not null;default:0.1"` // 提成比例 0.0～1.0
	TargetQuota    int64  `json:"target_quota" gorm:"default:0"`               // 业绩目标（quota 单位，0=不设限）
	CommissionRules string `json:"commission_rules,omitempty" gorm:"type:text;default:''"` // JSON 预留多档规则
	Status         int    `json:"status" gorm:"default:1"`                     // 1=启用 2=禁用
	Remark         string `json:"remark,omitempty" gorm:"type:varchar(255);default:''"`
	CreatedAt      int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

// ============================================================================
// ChannelCostConfig — 渠道成本配置
// ============================================================================

// ChannelCostConfig 记录每个渠道的实际采购成本系数。
// cost_ratio=1.0 表示无折扣（成本 = 标准价格），0.6 表示渠道成本为标准价的 60%。
type ChannelCostConfig struct {
	Id        int     `json:"id"`
	ChannelId int     `json:"channel_id" gorm:"uniqueIndex;not null"`
	CostRatio float64 `json:"cost_ratio" gorm:"not null;default:1.0"`
	Remark    string  `json:"remark,omitempty" gorm:"type:varchar(255);default:''"`
	CreatedAt int64   `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt int64   `json:"updated_at" gorm:"autoUpdateTime"`
}

// ============================================================================
// EmployeeCommissionLog — 提成日志
// ============================================================================

// EmployeeCommissionLog 记录每次 API 消费触发的提成计算结果。
// commission_quota 可为负数（退款冲销场景）。
type EmployeeCommissionLog struct {
	Id             int     `json:"id"`
	EmployeeId     int     `json:"employee_id" gorm:"index;not null"`
	EmployeeUserId int     `json:"employee_user_id" gorm:"index;not null"`
	CustomerUserId int     `json:"customer_user_id" gorm:"index;not null"`
	LogId          int     `json:"log_id" gorm:"index;default:0"`
	ModelName      string  `json:"model_name" gorm:"type:varchar(255);default:''"`
	ChannelId      int     `json:"channel_id" gorm:"default:0"`
	RevenueQuota   int64   `json:"revenue_quota" gorm:"default:0"`
	CostQuota      int64   `json:"cost_quota" gorm:"default:0"`
	ProfitQuota    int64   `json:"profit_quota" gorm:"default:0"`
	CommissionQuota int64  `json:"commission_quota" gorm:"default:0"`
	CommissionRate  float64 `json:"commission_rate" gorm:"default:0"`
	CostRatio       float64 `json:"cost_ratio" gorm:"default:1"`
	GroupRatio      float64 `json:"group_ratio" gorm:"default:1"`
	// settle_status: 0=待结算 1=已结算 2=已撤销（v2 实现结算功能）
	SettleStatus int   `json:"settle_status" gorm:"default:0"`
	SettledAt    int64 `json:"settled_at" gorm:"default:0"`
	CreatedAt    int64 `json:"created_at" gorm:"autoCreateTime;index"`
}

// ============================================================================
// 缓存：渠道成本系数（避免每次结算都查数据库）
// ============================================================================

var (
	channelCostCache     = make(map[int]float64)
	channelCostCacheLock sync.RWMutex
	channelCostCacheTime time.Time
	channelCostCacheTTL  = 5 * time.Minute
)

func invalidateChannelCostCache() {
	channelCostCacheLock.Lock()
	defer channelCostCacheLock.Unlock()
	channelCostCache = make(map[int]float64)
	channelCostCacheTime = time.Time{}
}

// GetChannelCostRatio 返回渠道成本系数，未配置时返回 1.0（标准成本）。
// 带 5 分钟内存缓存，降低数据库压力。
func GetChannelCostRatio(channelId int) float64 {
	channelCostCacheLock.RLock()
	if !channelCostCacheTime.IsZero() && time.Since(channelCostCacheTime) < channelCostCacheTTL {
		if ratio, ok := channelCostCache[channelId]; ok {
			channelCostCacheLock.RUnlock()
			return ratio
		}
	}
	channelCostCacheLock.RUnlock()

	var cfg ChannelCostConfig
	err := DB.Where("channel_id = ?", channelId).First(&cfg).Error
	if err != nil {
		// 未配置或查询失败，返回默认值
		return 1.0
	}

	channelCostCacheLock.Lock()
	channelCostCache[channelId] = cfg.CostRatio
	channelCostCacheTime = time.Now()
	channelCostCacheLock.Unlock()

	return cfg.CostRatio
}

// ============================================================================
// EmployeeProfile CRUD
// ============================================================================

// GetEmployeeByUserId 根据 user_id 查询启用的员工档案，未找到返回 nil。
func GetEmployeeByUserId(userId int) *EmployeeProfile {
	var emp EmployeeProfile
	err := DB.Where("user_id = ? AND status = 1", userId).First(&emp).Error
	if err != nil {
		return nil
	}
	return &emp
}

// IsEmployee 判断指定用户是否是启用状态的员工。
func IsEmployee(userId int) bool {
	return GetEmployeeByUserId(userId) != nil
}

// GetAllEmployees 获取员工列表（分页）。
func GetAllEmployees(page, pageSize int) ([]*EmployeeProfile, int64, error) {
	var employees []*EmployeeProfile
	var total int64

	offset := (page - 1) * pageSize
	tx := DB.Model(&EmployeeProfile{})
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := tx.Order("id DESC").Offset(offset).Limit(pageSize).Find(&employees).Error; err != nil {
		return nil, 0, err
	}
	return employees, total, nil
}

// CreateEmployee 创建员工档案，并确保对应的 user_extensions 记录存在。
func CreateEmployee(emp *EmployeeProfile) error {
	if err := DB.Create(emp).Error; err != nil {
		return err
	}
	return EnsureUserExtension(emp.UserId)
}

// UpdateEmployee 更新员工档案。
func UpdateEmployee(emp *EmployeeProfile) error {
	return DB.Model(emp).Updates(map[string]interface{}{
		"commission_rate":   emp.CommissionRate,
		"target_quota":      emp.TargetQuota,
		"commission_rules":  emp.CommissionRules,
		"status":            emp.Status,
		"remark":            emp.Remark,
	}).Error
}

// DisableEmployee 禁用员工（软删除）。
func DisableEmployee(id int) error {
	return DB.Model(&EmployeeProfile{}).Where("id = ?", id).
		Update("status", 2).Error
}

// ============================================================================
// ChannelCostConfig CRUD
// ============================================================================

// UpsertChannelCostConfig 创建或更新渠道成本配置。
func UpsertChannelCostConfig(channelId int, costRatio float64, remark string) error {
	var cfg ChannelCostConfig
	err := DB.Where("channel_id = ?", channelId).First(&cfg).Error
	if err == gorm.ErrRecordNotFound {
		cfg = ChannelCostConfig{
			ChannelId: channelId,
			CostRatio: costRatio,
			Remark:    remark,
		}
		if err := DB.Create(&cfg).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		if err := DB.Model(&cfg).Updates(map[string]interface{}{
			"cost_ratio": costRatio,
			"remark":     remark,
		}).Error; err != nil {
			return err
		}
	}
	invalidateChannelCostCache()
	return nil
}

// DeleteChannelCostConfig 删除渠道成本配置（恢复默认 1.0）。
func DeleteChannelCostConfig(channelId int) error {
	err := DB.Where("channel_id = ?", channelId).Delete(&ChannelCostConfig{}).Error
	if err != nil {
		return err
	}
	invalidateChannelCostCache()
	return nil
}

// GetAllChannelCostConfigs 获取所有渠道成本配置。
func GetAllChannelCostConfigs() ([]*ChannelCostConfig, error) {
	var configs []*ChannelCostConfig
	err := DB.Order("channel_id ASC").Find(&configs).Error
	return configs, err
}

// ============================================================================
// EmployeeCommissionLog 查询
// ============================================================================

type CommissionLogFilter struct {
	EmployeeUserId int
	CustomerUserId int
	StartTime      int64
	EndTime        int64
	Page           int
	PageSize       int
}

// GetCommissionLogs 查询提成日志（管理员用，支持多条件筛选）。
func GetCommissionLogs(filter CommissionLogFilter) ([]*EmployeeCommissionLog, int64, error) {
	var logs []*EmployeeCommissionLog
	var total int64

	tx := DB.Model(&EmployeeCommissionLog{})
	if filter.EmployeeUserId != 0 {
		tx = tx.Where("employee_user_id = ?", filter.EmployeeUserId)
	}
	if filter.CustomerUserId != 0 {
		tx = tx.Where("customer_user_id = ?", filter.CustomerUserId)
	}
	if filter.StartTime != 0 {
		tx = tx.Where("created_at >= ?", filter.StartTime)
	}
	if filter.EndTime != 0 {
		tx = tx.Where("created_at <= ?", filter.EndTime)
	}

	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (filter.Page - 1) * filter.PageSize
	if err := tx.Order("created_at DESC").Offset(offset).Limit(filter.PageSize).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

type CommissionSummaryItem struct {
	EmployeeUserId  int     `json:"employee_user_id"`
	TotalRevenue    int64   `json:"total_revenue_quota"`
	TotalCost       int64   `json:"total_cost_quota"`
	TotalProfit     int64   `json:"total_profit_quota"`
	TotalCommission int64   `json:"total_commission_quota"`
	RecordCount     int64   `json:"record_count"`
}

// GetCommissionSummary 按员工汇总提成统计。
func GetCommissionSummary(startTime, endTime int64) ([]*CommissionSummaryItem, error) {
	var items []*CommissionSummaryItem
	tx := DB.Model(&EmployeeCommissionLog{}).
		Select("employee_user_id, SUM(revenue_quota) as total_revenue_quota, SUM(cost_quota) as total_cost_quota, SUM(profit_quota) as total_profit_quota, SUM(commission_quota) as total_commission_quota, COUNT(*) as record_count").
		Group("employee_user_id")
	if startTime != 0 {
		tx = tx.Where("created_at >= ?", startTime)
	}
	if endTime != 0 {
		tx = tx.Where("created_at <= ?", endTime)
	}
	err := tx.Scan(&items).Error
	return items, err
}

// CreateCommissionLog 写入提成日志。
func CreateCommissionLog(log *EmployeeCommissionLog) error {
	return DB.Create(log).Error
}
