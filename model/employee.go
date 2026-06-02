package model

import (
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	// log_id 关联 logs.id，唯一约束用于提成幂等：同一笔消费日志只会产生一条提成记录，
	// 避免上层结算重试导致重复计提。
	LogId          int     `json:"log_id" gorm:"uniqueIndex;default:0"`
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

// 渠道成本系数缓存策略：
//   - 单机部署：使用进程内 map 缓存（5 分钟 TTL）。
//   - 集群部署（启用 Redis）：以 Redis 为共享缓存，写操作通过 RedisDel 全局失效，
//     保证各实例读到一致的成本系数；本地 map 缓存被跳过以避免实例间不一致。
const channelCostDefaultRatio = 1.0

var (
	channelCostCache     = make(map[int]float64)
	channelCostCacheLock sync.RWMutex
	channelCostCacheTime time.Time
	channelCostCacheTTL  = 5 * time.Minute
)

func channelCostRedisKey(channelId int) string {
	return "channel_cost_ratio:" + strconv.Itoa(channelId)
}

// invalidateChannelCostCache 清空成本系数缓存。
// 集群下删除对应 Redis 键以触发全局失效；同时清空本地 map 兜底。
func invalidateChannelCostCache(channelId int) {
	if common.RedisEnabled {
		_ = common.RedisDelKey(channelCostRedisKey(channelId))
	}
	channelCostCacheLock.Lock()
	channelCostCache = make(map[int]float64)
	channelCostCacheTime = time.Time{}
	channelCostCacheLock.Unlock()
}

// loadChannelCostRatioFromDB 从数据库读取成本系数，未配置返回默认值 1.0。
func loadChannelCostRatioFromDB(channelId int) float64 {
	var cfg ChannelCostConfig
	if err := DB.Where("channel_id = ?", channelId).First(&cfg).Error; err != nil {
		return channelCostDefaultRatio
	}
	return cfg.CostRatio
}

// GetChannelCostRatio 返回渠道成本系数，未配置时返回 1.0（标准成本）。
func GetChannelCostRatio(channelId int) float64 {
	if common.RedisEnabled {
		key := channelCostRedisKey(channelId)
		if val, err := common.RedisGet(key); err == nil {
			if ratio, perr := strconv.ParseFloat(val, 64); perr == nil {
				return ratio
			}
		}
		ratio := loadChannelCostRatioFromDB(channelId)
		_ = common.RedisSet(key, strconv.FormatFloat(ratio, 'f', -1, 64), channelCostCacheTTL)
		return ratio
	}

	channelCostCacheLock.RLock()
	if !channelCostCacheTime.IsZero() && time.Since(channelCostCacheTime) < channelCostCacheTTL {
		if ratio, ok := channelCostCache[channelId]; ok {
			channelCostCacheLock.RUnlock()
			return ratio
		}
	}
	channelCostCacheLock.RUnlock()

	ratio := loadChannelCostRatioFromDB(channelId)

	channelCostCacheLock.Lock()
	channelCostCache[channelId] = ratio
	channelCostCacheTime = time.Now()
	channelCostCacheLock.Unlock()

	return ratio
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

// UpdateEmployee 更新员工档案。记录不存在时返回 gorm.ErrRecordNotFound。
// 先做存在性校验（避免 MySQL 在值未变化时 RowsAffected=0 被误判为不存在）。
func UpdateEmployee(emp *EmployeeProfile) error {
	var count int64
	if err := DB.Model(&EmployeeProfile{}).Where("id = ?", emp.Id).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return gorm.ErrRecordNotFound
	}
	return DB.Model(emp).Updates(map[string]interface{}{
		"commission_rate":  emp.CommissionRate,
		"target_quota":     emp.TargetQuota,
		"commission_rules": emp.CommissionRules,
		"status":           emp.Status,
		"remark":           emp.Remark,
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
	invalidateChannelCostCache(channelId)
	return nil
}

// DeleteChannelCostConfig 删除渠道成本配置（恢复默认 1.0）。
func DeleteChannelCostConfig(channelId int) error {
	err := DB.Where("channel_id = ?", channelId).Delete(&ChannelCostConfig{}).Error
	if err != nil {
		return err
	}
	invalidateChannelCostCache(channelId)
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

// ============================================================================
// 经营概览（财务统计）聚合
// ============================================================================

// CommissionTotals 提成相关流量的总计（仅员工归属流量）。
type CommissionTotals struct {
	TotalRevenue    int64 `json:"total_revenue"`
	TotalCost       int64 `json:"total_cost"`
	TotalProfit     int64 `json:"total_profit"`
	TotalCommission int64 `json:"total_commission"`
	RecordCount     int64 `json:"record_count"`
}

// applyCommissionTimeRange 给查询附加时间范围条件。
func applyCommissionTimeRange(tx *gorm.DB, startTime, endTime int64) *gorm.DB {
	if startTime != 0 {
		tx = tx.Where("created_at >= ?", startTime)
	}
	if endTime != 0 {
		tx = tx.Where("created_at <= ?", endTime)
	}
	return tx
}

// GetCommissionTotals 汇总指定时间范围内的提成流量总计。
// 别名与结构体字段 snake_case 一一对应，保证跨库 Scan 正确。
func GetCommissionTotals(startTime, endTime int64) (CommissionTotals, error) {
	var t CommissionTotals
	tx := DB.Model(&EmployeeCommissionLog{}).
		Select("COALESCE(SUM(revenue_quota),0) as total_revenue, " +
			"COALESCE(SUM(cost_quota),0) as total_cost, " +
			"COALESCE(SUM(profit_quota),0) as total_profit, " +
			"COALESCE(SUM(commission_quota),0) as total_commission, " +
			"COUNT(*) as record_count")
	tx = applyCommissionTimeRange(tx, startTime, endTime)
	err := tx.Scan(&t).Error
	return t, err
}

// CommissionEmployeeStat 按员工汇总。
type CommissionEmployeeStat struct {
	EmployeeUserId  int   `json:"employee_user_id"`
	TotalRevenue    int64 `json:"total_revenue"`
	TotalCost       int64 `json:"total_cost"`
	TotalProfit     int64 `json:"total_profit"`
	TotalCommission int64 `json:"total_commission"`
	RecordCount     int64 `json:"record_count"`
}

// GetCommissionStatsByEmployee 按员工分组汇总。
func GetCommissionStatsByEmployee(startTime, endTime int64) ([]*CommissionEmployeeStat, error) {
	var items []*CommissionEmployeeStat
	tx := DB.Model(&EmployeeCommissionLog{}).
		Select("employee_user_id, " +
			"COALESCE(SUM(revenue_quota),0) as total_revenue, " +
			"COALESCE(SUM(cost_quota),0) as total_cost, " +
			"COALESCE(SUM(profit_quota),0) as total_profit, " +
			"COALESCE(SUM(commission_quota),0) as total_commission, " +
			"COUNT(*) as record_count").
		Group("employee_user_id").
		Order("total_commission DESC")
	tx = applyCommissionTimeRange(tx, startTime, endTime)
	err := tx.Scan(&items).Error
	return items, err
}

// CommissionChannelStat 按渠道汇总。
type CommissionChannelStat struct {
	ChannelId    int    `json:"channel_id"`
	TotalRevenue int64  `json:"total_revenue"`
	TotalCost    int64  `json:"total_cost"`
	TotalProfit  int64  `json:"total_profit"`
	RecordCount  int64  `json:"record_count"`
	ChannelName  string `json:"channel_name" gorm:"-"`
}

// GetCommissionStatsByChannel 按渠道分组汇总。
func GetCommissionStatsByChannel(startTime, endTime int64) ([]*CommissionChannelStat, error) {
	var items []*CommissionChannelStat
	tx := DB.Model(&EmployeeCommissionLog{}).
		Select("channel_id, " +
			"COALESCE(SUM(revenue_quota),0) as total_revenue, " +
			"COALESCE(SUM(cost_quota),0) as total_cost, " +
			"COALESCE(SUM(profit_quota),0) as total_profit, " +
			"COUNT(*) as record_count").
		Group("channel_id").
		Order("total_profit DESC")
	tx = applyCommissionTimeRange(tx, startTime, endTime)
	err := tx.Scan(&items).Error
	return items, err
}

// CommissionDailyStat 按天汇总（date 为 UTC 日期 YYYY-MM-DD）。
type CommissionDailyStat struct {
	Date            string `json:"date"`
	TotalRevenue    int64  `json:"total_revenue"`
	TotalCost       int64  `json:"total_cost"`
	TotalProfit     int64  `json:"total_profit"`
	TotalCommission int64  `json:"total_commission"`
}

// GetCommissionStatsByDay 按天汇总。
//
// 为保证 SQLite/MySQL/PostgreSQL 三库一致，不使用各库差异化的日期函数，
// 改为只取必要列后在 Go 侧按 UTC 自然日聚合。
func GetCommissionStatsByDay(startTime, endTime int64) ([]*CommissionDailyStat, error) {
	type row struct {
		CreatedAt       int64
		RevenueQuota    int64
		CostQuota       int64
		ProfitQuota     int64
		CommissionQuota int64
	}
	var rows []row
	tx := DB.Model(&EmployeeCommissionLog{}).
		Select("created_at, revenue_quota, cost_quota, profit_quota, commission_quota").
		Order("created_at ASC")
	tx = applyCommissionTimeRange(tx, startTime, endTime)
	if err := tx.Scan(&rows).Error; err != nil {
		return nil, err
	}

	bucket := make(map[string]*CommissionDailyStat)
	order := make([]string, 0)
	for _, r := range rows {
		day := time.Unix(r.CreatedAt, 0).UTC().Format("2006-01-02")
		stat, ok := bucket[day]
		if !ok {
			stat = &CommissionDailyStat{Date: day}
			bucket[day] = stat
			order = append(order, day)
		}
		stat.TotalRevenue += r.RevenueQuota
		stat.TotalCost += r.CostQuota
		stat.TotalProfit += r.ProfitQuota
		stat.TotalCommission += r.CommissionQuota
	}

	result := make([]*CommissionDailyStat, 0, len(order))
	for _, day := range order {
		result = append(result, bucket[day])
	}
	return result, nil
}

// CreateCommissionLog 写入提成日志（幂等）。
//
// 通过 log_id 唯一索引 + ON CONFLICT DO NOTHING 实现去重：
// 同一笔消费日志（log_id）重复结算时只会落库一次。
// 返回 inserted 表示本次是否真正插入了新记录；调用方据此决定是否累加汇总，
// 避免并发/重试场景下重复计提。
//
// 注意：log_id<=0 时无法依赖唯一索引去重（多条 0 会互相冲突），
// 这类记录直接插入并视为已插入，正常消费链路 log_id 恒为正值。
func CreateCommissionLog(log *EmployeeCommissionLog) (inserted bool, err error) {
	if log.LogId <= 0 {
		err = DB.Create(log).Error
		return err == nil, err
	}
	result := DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "log_id"}},
		DoNothing: true,
	}).Create(log)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}
