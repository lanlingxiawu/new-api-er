package model

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// ============================================================================
// EmployeeProfile — 员工档案
// ============================================================================

// EmployeeProfile 记录员工的提成配置。员工本身是普通用户（users 表），
// 通过 user_id 关联；管理员创建此记录后该用户即具备员工身份。
type EmployeeProfile struct {
	Id           int     `json:"id"`
	UserId       int     `json:"user_id" gorm:"uniqueIndex;not null"`
	TargetAmount float64 `json:"target_amount" gorm:"column:target_amount;default:0"` // 业绩目标（USD 金额，0=不设限；业绩=客户消耗额）
	Status       int     `json:"status" gorm:"default:1"`                             // 1=启用 2=禁用
	Remark       string  `json:"remark,omitempty" gorm:"type:varchar(255);default:''"`
	CreatedAt    int64   `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt    int64   `json:"updated_at" gorm:"autoUpdateTime"`
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
	Id             int `json:"id"`
	EmployeeId     int `json:"employee_id" gorm:"index;not null"`
	EmployeeUserId int `json:"employee_user_id" gorm:"index;index:idx_employee_commission_created_employee,priority:2;not null"`
	CustomerUserId int `json:"customer_user_id" gorm:"index;not null"`
	// log_id 关联 logs.id，唯一约束用于提成幂等：同一笔消费日志只会产生一条提成记录，
	// 避免上层结算重试导致重复计提。
	LogId           *int    `json:"log_id" gorm:"uniqueIndex"`
	ModelName       string  `json:"model_name" gorm:"type:varchar(255);default:''"`
	ChannelId       int     `json:"channel_id" gorm:"index:idx_employee_commission_created_channel,priority:2;default:0"`
	RevenueQuota    int64   `json:"revenue_quota" gorm:"default:0"`
	CostQuota       int64   `json:"cost_quota" gorm:"default:0"`
	ProfitQuota     int64   `json:"profit_quota" gorm:"default:0"`
	CommissionQuota int64   `json:"commission_quota" gorm:"default:0"`
	CommissionRate  float64 `json:"commission_rate" gorm:"default:0"`
	CostRatio       float64 `json:"cost_ratio"`
	GroupRatio      float64 `json:"group_ratio"`
	// settle_status: 0=待结算 1=已结算 2=已撤销（v2 实现结算功能）
	SettleStatus int   `json:"settle_status" gorm:"default:0"`
	SettledAt    int64 `json:"settled_at" gorm:"default:0"`
	CreatedAt    int64 `json:"created_at" gorm:"autoCreateTime;index;index:idx_employee_commission_created_employee,priority:1;index:idx_employee_commission_created_channel,priority:1"`
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

// ResetChannelCostCache 清空渠道成本系数内存缓存（测试专用）。
func ResetChannelCostCache() {
	channelCostCacheLock.Lock()
	channelCostCache = make(map[int]float64)
	channelCostCacheTime = time.Time{}
	channelCostCacheLock.Unlock()
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

// GetEmployeeById 根据 employee.id 查询员工档案（不限状态）。
func GetEmployeeById(id int) (*EmployeeProfile, error) {
	var emp EmployeeProfile
	err := DB.Where("id = ?", id).First(&emp).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &emp, err
}

// employeeByUserIdCache 缓存 userId → *EmployeeProfile（启用状态）。
// 非员工缓存 nil 值，避免对非员工用户重复查库。
// 员工创建/更新/禁用时调用 InvalidateEmployeeCache 失效。
var (
	employeeByUserIdCache     = make(map[int]*employeeCacheEntry)
	employeeByUserIdCacheLock sync.RWMutex
	employeeCacheTTL          = 5 * time.Minute
)

type employeeCacheEntry struct {
	profile  *EmployeeProfile // nil 表示非员工
	cachedAt time.Time
}

// GetEmployeeByUserId 根据 user_id 查询启用的员工档案，未找到返回 nil。
func GetEmployeeByUserId(userId int) *EmployeeProfile {
	employeeByUserIdCacheLock.RLock()
	if entry, ok := employeeByUserIdCache[userId]; ok && time.Since(entry.cachedAt) < employeeCacheTTL {
		employeeByUserIdCacheLock.RUnlock()
		return entry.profile
	}
	employeeByUserIdCacheLock.RUnlock()

	var emp EmployeeProfile
	err := DB.Where("user_id = ? AND status = 1", userId).First(&emp).Error
	var profile *EmployeeProfile
	if err == nil {
		profile = &emp
	}

	employeeByUserIdCacheLock.Lock()
	employeeByUserIdCache[userId] = &employeeCacheEntry{profile: profile, cachedAt: time.Now()}
	employeeByUserIdCacheLock.Unlock()

	return profile
}

// InvalidateEmployeeCache 清除指定用户的员工缓存。在创建/更新/禁用员工后调用。
func InvalidateEmployeeCache(userId int) {
	employeeByUserIdCacheLock.Lock()
	delete(employeeByUserIdCache, userId)
	employeeByUserIdCacheLock.Unlock()
}

// IsEmployee 判断指定用户是否是启用状态的员工。
func IsEmployee(userId int) bool {
	return GetEmployeeByUserId(userId) != nil
}

// HasEmployeeProfile reports whether a user already has any employee profile.
func HasEmployeeProfile(userId int) bool {
	var count int64
	err := DB.Model(&EmployeeProfile{}).Where("user_id = ?", userId).Count(&count).Error
	if err != nil {
		common.SysLog("failed to check employee profile: user_id=" + strconv.Itoa(userId) + ", error=" + err.Error())
		return false
	}
	return count > 0
}

func GetEmployeeProfileStatusesByUserIds(userIds []int) (map[int]int, error) {
	statuses := make(map[int]int, len(userIds))
	if len(userIds) == 0 {
		return statuses, nil
	}
	type statusRow struct {
		UserId int
		Status int
	}
	var rows []statusRow
	if err := DB.Model(&EmployeeProfile{}).
		Select("user_id, status").
		Where("user_id IN ?", userIds).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		statuses[row.UserId] = row.Status
	}
	return statuses, nil
}

type EmployeeFilter struct {
	UserId    int
	Keyword   string
	Status    int
	SortBy    string
	SortOrder string
}

// GetAllEmployees 获取员工列表（分页）。
func GetAllEmployees(page, pageSize int, filter EmployeeFilter) ([]*EmployeeProfile, int64, error) {
	var employees []*EmployeeProfile
	var total int64

	offset := (page - 1) * pageSize
	tx := DB.Model(&EmployeeProfile{})
	if filter.UserId != 0 {
		tx = tx.Where("employee_profiles.user_id = ?", filter.UserId)
	}
	if filter.Status != 0 {
		tx = tx.Where("employee_profiles.status = ?", filter.Status)
	}
	if filter.Keyword != "" {
		keyword := "%" + filter.Keyword + "%"
		tx = tx.Joins("LEFT JOIN users ON users.id = employee_profiles.user_id").
			Where("users.username LIKE ? OR users.display_name LIKE ? OR users.email LIKE ? OR users.remark LIKE ?", keyword, keyword, keyword, keyword)
	}
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	orderColumn := "employee_profiles.id"
	switch filter.SortBy {
	case "id":
		orderColumn = "employee_profiles.id"
	case "user_id":
		orderColumn = "employee_profiles.user_id"
	case "username":
		tx = tx.Joins("LEFT JOIN users AS sort_users ON sort_users.id = employee_profiles.user_id")
		orderColumn = "sort_users.username"
	case "target_amount":
		orderColumn = "employee_profiles.target_amount"
	case "customer_count":
		tx = tx.Joins("LEFT JOIN (SELECT inviter_id AS employee_user_id, COUNT(*) AS customer_count FROM users WHERE role = ? GROUP BY inviter_id) customer_rollup ON customer_rollup.employee_user_id = employee_profiles.user_id", common.RoleCommonUser)
		orderColumn = "COALESCE(customer_rollup.customer_count, 0)"
	case "total_consumption_quota":
		tx = tx.Joins("LEFT JOIN (SELECT inviter_id AS employee_user_id, COALESCE(SUM(used_quota),0) AS total_consumption_quota FROM users WHERE role = ? GROUP BY inviter_id) consumption_rollup ON consumption_rollup.employee_user_id = employee_profiles.user_id", common.RoleCommonUser)
		orderColumn = "COALESCE(consumption_rollup.total_consumption_quota, 0)"
	case "total_cost_quota":
		tx = tx.Joins("LEFT JOIN (SELECT employee_user_id, COALESCE(SUM(cost_quota),0) AS total_cost_quota FROM employee_commission_daily_stats GROUP BY employee_user_id) commission_rollup ON commission_rollup.employee_user_id = employee_profiles.user_id")
		orderColumn = "COALESCE(commission_rollup.total_cost_quota, 0)"
	case "total_profit_quota":
		tx = tx.Joins("LEFT JOIN (SELECT employee_user_id, COALESCE(SUM(profit_quota),0) AS total_profit_quota FROM employee_commission_daily_stats GROUP BY employee_user_id) commission_rollup ON commission_rollup.employee_user_id = employee_profiles.user_id")
		orderColumn = "COALESCE(commission_rollup.total_profit_quota, 0)"
	case "total_commission_quota":
		tx = tx.Joins("LEFT JOIN (SELECT employee_user_id, COALESCE(SUM(commission_quota),0) AS total_commission_quota FROM employee_commission_daily_stats GROUP BY employee_user_id) commission_rollup ON commission_rollup.employee_user_id = employee_profiles.user_id")
		orderColumn = "COALESCE(commission_rollup.total_commission_quota, 0)"
	case "current_performance_quota":
		tx = tx.Joins("LEFT JOIN (SELECT employee_user_id, COALESCE(SUM(profit_quota),0) AS total_profit_quota FROM employee_commission_daily_stats GROUP BY employee_user_id) current_profit_rollup ON current_profit_rollup.employee_user_id = employee_profiles.user_id").
			Joins("LEFT JOIN employee_tier_levels AS current_perf_levels ON current_perf_levels.user_id = employee_profiles.user_id")
		orderColumn = "CASE WHEN COALESCE(current_profit_rollup.total_profit_quota, 0) - COALESCE(current_perf_levels.baseline_profit_quota, 0) < 0 THEN 0 ELSE COALESCE(current_profit_rollup.total_profit_quota, 0) - COALESCE(current_perf_levels.baseline_profit_quota, 0) END"
	case "current_commission_quota":
		tx = tx.Joins("LEFT JOIN (SELECT employee_user_id, COALESCE(SUM(commission_quota),0) AS total_commission_quota FROM employee_commission_daily_stats GROUP BY employee_user_id) current_commission_rollup ON current_commission_rollup.employee_user_id = employee_profiles.user_id").
			Joins("LEFT JOIN employee_tier_levels AS current_commission_levels ON current_commission_levels.user_id = employee_profiles.user_id")
		orderColumn = "CASE WHEN COALESCE(current_commission_rollup.total_commission_quota, 0) - COALESCE(current_commission_levels.baseline_commission_quota, 0) < 0 THEN 0 ELSE COALESCE(current_commission_rollup.total_commission_quota, 0) - COALESCE(current_commission_levels.baseline_commission_quota, 0) END"
	case "current_tier_rate":
		tx = tx.Joins("LEFT JOIN employee_tier_levels AS tier_levels_sort ON tier_levels_sort.user_id = employee_profiles.user_id").
			Joins("LEFT JOIN employee_commission_tiers AS tiers_sort ON tiers_sort.id = tier_levels_sort.tier_id")
		orderColumn = "COALESCE(tiers_sort.rate, 0)"
	case "current_tier_level":
		tx = tx.Joins("LEFT JOIN employee_tier_levels AS tier_levels_sort ON tier_levels_sort.user_id = employee_profiles.user_id").
			Joins("LEFT JOIN employee_commission_tiers AS tiers_sort ON tiers_sort.id = tier_levels_sort.tier_id")
		orderColumn = "COALESCE(tiers_sort.level, 0)"
	case "status":
		orderColumn = "employee_profiles.status"
	case "remark":
		tx = tx.Joins("LEFT JOIN users AS remark_users ON remark_users.id = employee_profiles.user_id")
		orderColumn = "remark_users.remark"
	case "created_at":
		orderColumn = "employee_profiles.created_at"
	}
	orderDirection := "DESC"
	if strings.EqualFold(filter.SortOrder, "asc") {
		orderDirection = "ASC"
	}
	if err := tx.Order(orderColumn + " " + orderDirection).Offset(offset).Limit(pageSize).Find(&employees).Error; err != nil {
		return nil, 0, err
	}
	return employees, total, nil
}

// CreateEmployee 创建员工档案，并确保对应的 user_extensions 记录存在。
func CreateEmployee(emp *EmployeeProfile) error {
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(emp).Error; err != nil {
			return err
		}
		if err := tx.Model(&User{}).
			Where("id = ?", emp.UserId).
			Updates(map[string]interface{}{
				"inviter_id": 0,
				"remark":     emp.Remark,
			}).Error; err != nil {
			return err
		}
		return tx.Delete(&CustomerProfile{}, "customer_user_id = ?", emp.UserId).Error
	}); err != nil {
		return err
	}
	InvalidateEmployeeCache(emp.UserId)
	InvalidateInviterIdCache(emp.UserId)
	return EnsureUserExtension(emp.UserId)
}

// UpdateEmployee 更新员工档案。记录不存在时返回 gorm.ErrRecordNotFound。
// 先做存在性校验（避免 MySQL 在值未变化时 RowsAffected=0 被误判为不存在）。
func UpdateEmployee(emp *EmployeeProfile) error {
	var existing EmployeeProfile
	if err := DB.Select("id", "user_id").Where("id = ?", emp.Id).First(&existing).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return gorm.ErrRecordNotFound
		}
		return err
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&EmployeeProfile{}).Where("id = ?", emp.Id).Updates(map[string]interface{}{
			"status": emp.Status,
			"remark": emp.Remark,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&User{}).Where("id = ?", existing.UserId).Update("remark", emp.Remark).Error
	})
	if err == nil {
		InvalidateEmployeeCache(existing.UserId)
		_ = invalidateUserCache(existing.UserId)
	}
	return err
}

// DisableEmployee 禁用员工（软删除）。
func DisableEmployee(id int) error {
	// 先查 userId 用于失效缓存
	emp, _ := GetEmployeeById(id)
	err := DB.Model(&EmployeeProfile{}).Where("id = ?", id).
		Update("status", 2).Error
	if err == nil && emp != nil {
		InvalidateEmployeeCache(emp.UserId)
	}
	return err
}

// ============================================================================
// ChannelCostConfig CRUD
// ============================================================================

// UpsertChannelCostConfig 创建或更新渠道成本配置。
func UpsertChannelCostConfig(channelId int, costRatio float64, remark string) error {
	var cfg ChannelCostConfig
	err := DB.Where("channel_id = ?", channelId).First(&cfg).Error
	if err == gorm.ErrRecordNotFound {
		now := time.Now().Unix()
		if err := DB.Model(&ChannelCostConfig{}).Create(map[string]interface{}{
			"channel_id": channelId,
			"cost_ratio": costRatio,
			"remark":     remark,
			"created_at": now,
			"updated_at": now,
		}).Error; err != nil {
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
	ModelName      string
	ChannelId      int
	LossStatus     string
	StartTime      int64
	EndTime        int64
	Page           int
	PageSize       int
}

func applyCommissionLogFilter(tx *gorm.DB, filter CommissionLogFilter) *gorm.DB {
	if filter.EmployeeUserId != 0 {
		tx = tx.Where("employee_user_id = ?", filter.EmployeeUserId)
	}
	if filter.CustomerUserId != 0 {
		tx = tx.Where("customer_user_id = ?", filter.CustomerUserId)
	}
	if filter.ModelName != "" {
		tx = tx.Where("model_name LIKE ?", "%"+filter.ModelName+"%")
	}
	if filter.ChannelId != 0 {
		tx = tx.Where("channel_id = ?", filter.ChannelId)
	}
	switch strings.TrimSpace(filter.LossStatus) {
	case "loss":
		tx = tx.Where("profit_quota < ?", 0)
	case "normal":
		tx = tx.Where("profit_quota >= ?", 0)
	}
	return applyCreatedAtTimeRange(tx, filter.StartTime, filter.EndTime)
}

func canCountCommissionLogsFromDaily(filter CommissionLogFilter) bool {
	return filter.CustomerUserId == 0 &&
		filter.ChannelId == 0 &&
		strings.TrimSpace(filter.LossStatus) == "" &&
		strings.TrimSpace(filter.ModelName) == ""
}

func countCommissionLogsFromDailyRange(startDate, endDate int64, employeeUserId int) (int64, error) {
	var total int64
	tx := DB.Model(&EmployeeCommissionDailyStat{}).
		Select("COALESCE(SUM(record_count), 0)").
		Where("stat_date >= ? AND stat_date <= ?", startDate, endDate)
	if employeeUserId != 0 {
		tx = tx.Where("employee_user_id = ?", employeeUserId)
	}
	err := tx.Scan(&total).Error
	return total, err
}

func countCommissionLogsFromLedgerRange(filter CommissionLogFilter, startTime, endTime int64) (int64, error) {
	var total int64
	filter.StartTime = startTime
	filter.EndTime = endTime
	tx := applyCommissionLogFilter(DB.Model(&EmployeeCommissionLog{}), filter)
	err := tx.Count(&total).Error
	return total, err
}

func countCommissionLogsFromDailyStats(filter CommissionLogFilter) (int64, error) {
	if filter.StartTime == 0 && filter.EndTime == 0 {
		var total int64
		tx := DB.Model(&EmployeeCommissionDailyStat{}).
			Select("COALESCE(SUM(record_count), 0)")
		if filter.EmployeeUserId != 0 {
			tx = tx.Where("employee_user_id = ?", filter.EmployeeUserId)
		}
		err := tx.Scan(&total).Error
		return total, err
	}

	plan, err := ResolveBusinessStatsQueryPlan(filter.StartTime, filter.EndTime)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, r := range plan.CoveredRanges {
		count, err := countCommissionLogsFromDailyRange(r.StartDate, r.EndDate, filter.EmployeeUserId)
		if err != nil {
			return 0, err
		}
		total += count
	}
	for _, r := range plan.UncoveredRanges {
		count, err := countCommissionLogsFromLedgerRange(filter, r.StartDate, unixDayEnd(r.EndDate))
		if err != nil {
			return 0, err
		}
		total += count
	}
	for _, r := range plan.DetailRanges {
		count, err := countCommissionLogsFromLedgerRange(filter, r.StartTime, r.EndTime)
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func countCommissionLogs(filter CommissionLogFilter) (int64, error) {
	if canCountCommissionLogsFromDaily(filter) {
		return countCommissionLogsFromDailyStats(filter)
	}
	var total int64
	tx := applyCommissionLogFilter(DB.Model(&EmployeeCommissionLog{}), filter)
	err := tx.Count(&total).Error
	return total, err
}

// GetCommissionLogs 查询提成日志（管理员用，支持多条件筛选）。
func GetCommissionLogs(filter CommissionLogFilter) ([]*EmployeeCommissionLog, int64, error) {
	var logs []*EmployeeCommissionLog

	total, err := countCommissionLogs(filter)
	if err != nil {
		return nil, 0, err
	}

	offset := (filter.Page - 1) * filter.PageSize
	tx := applyCommissionLogFilter(DB.Model(&EmployeeCommissionLog{}), filter)
	if err := tx.Order("created_at DESC").Offset(offset).Limit(filter.PageSize).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

type CommissionSummaryItem struct {
	EmployeeUserId  int   `json:"employee_user_id"`
	TotalRevenue    int64 `json:"total_revenue_quota"`
	TotalCost       int64 `json:"total_cost_quota"`
	TotalProfit     int64 `json:"total_profit_quota"`
	TotalCommission int64 `json:"total_commission_quota"`
	RecordCount     int64 `json:"record_count"`
}

// GetCommissionSummary 按员工汇总提成统计，优先使用日聚合统计。
type CommissionMonthlyStatFilter struct {
	EmployeeUserId int
	PeriodStartAt  int64
	StartTime      int64
	EndTime        int64
	Page           int
	PageSize       int
}

type EmployeeCommissionMonthlyStatItem struct {
	EmployeeCommissionMonthlyStat
	TotalRevenueUsd    float64 `json:"total_revenue_usd"`
	TotalCostUsd       float64 `json:"total_cost_usd"`
	TotalProfitUsd     float64 `json:"total_profit_usd"`
	TotalCommissionUsd float64 `json:"total_commission_usd"`
}

type CommissionCalendarDayStat struct {
	StatDate        int64   `json:"stat_date"`
	Date            string  `json:"date"`
	RevenueQuota    int64   `json:"revenue_quota"`
	CostQuota       int64   `json:"cost_quota"`
	ProfitQuota     int64   `json:"profit_quota"`
	CommissionQuota int64   `json:"commission_quota"`
	RecordCount     int64   `json:"record_count"`
	RevenueUsd      float64 `json:"revenue_usd"`
	CostUsd         float64 `json:"cost_usd"`
	ProfitUsd       float64 `json:"profit_usd"`
	CommissionUsd   float64 `json:"commission_usd"`
}

type CommissionCalendarSummary struct {
	RevenueQuota    int64   `json:"revenue_quota"`
	CostQuota       int64   `json:"cost_quota"`
	ProfitQuota     int64   `json:"profit_quota"`
	CommissionQuota int64   `json:"commission_quota"`
	RecordCount     int64   `json:"record_count"`
	RevenueUsd      float64 `json:"revenue_usd"`
	CostUsd         float64 `json:"cost_usd"`
	ProfitUsd       float64 `json:"profit_usd"`
	CommissionUsd   float64 `json:"commission_usd"`
}

type CommissionCalendarStats struct {
	Days             []*CommissionCalendarDayStat `json:"days"`
	Summary          CommissionCalendarSummary    `json:"summary"`
	PeriodStartAt    int64                        `json:"period_start_at"`
	PeriodEndAt      int64                        `json:"period_end_at"`
	PeriodBoundaryAt int64                        `json:"period_boundary_at"`
	PeriodKey        string                       `json:"period_key"`
	Timezone         string                       `json:"timezone"`
}

func normalizeCommissionMonthlyStatFilter(filter CommissionMonthlyStatFilter) CommissionMonthlyStatFilter {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 100 {
		filter.PageSize = 20
	}
	return filter
}

func applyCommissionMonthlyStatFilter(tx *gorm.DB, filter CommissionMonthlyStatFilter) *gorm.DB {
	if filter.EmployeeUserId != 0 {
		tx = tx.Where("employee_user_id = ?", filter.EmployeeUserId)
	}
	if filter.PeriodStartAt != 0 {
		tx = tx.Where("period_start_at = ?", filter.PeriodStartAt)
	}
	if filter.StartTime != 0 && filter.EndTime != 0 {
		tx = tx.Where("period_start_at <= ? AND period_end_at >= ?", filter.EndTime, filter.StartTime)
	}
	return tx
}

func GetCommissionMonthlyStats(filter CommissionMonthlyStatFilter) ([]*EmployeeCommissionMonthlyStatItem, int64, error) {
	filter = normalizeCommissionMonthlyStatFilter(filter)
	base := applyCommissionMonthlyStatFilter(DB.Model(&EmployeeCommissionMonthlyStat{}), filter)
	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rows []*EmployeeCommissionMonthlyStat
	offset := (filter.Page - 1) * filter.PageSize
	if err := base.Session(&gorm.Session{}).Order("period_start_at DESC, commission_quota DESC").
		Offset(offset).
		Limit(filter.PageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]*EmployeeCommissionMonthlyStatItem, 0, len(rows))
	for _, row := range rows {
		item := &EmployeeCommissionMonthlyStatItem{
			EmployeeCommissionMonthlyStat: *row,
			TotalRevenueUsd:               common.QuotaToUSD(row.RevenueQuota),
			TotalCostUsd:                  common.QuotaToUSD(row.CostQuota),
			TotalProfitUsd:                common.QuotaToUSD(row.ProfitQuota),
			TotalCommissionUsd:            common.QuotaToUSD(row.CommissionQuota),
		}
		items = append(items, item)
	}
	return items, total, nil
}

func GetCommissionCalendarStats(startTime, endTime int64, employeeUserId int) (*CommissionCalendarStats, error) {
	if startTime <= 0 || endTime <= 0 || startTime > endTime {
		return &CommissionCalendarStats{Days: []*CommissionCalendarDayStat{}}, nil
	}
	period := ResolveCommissionMonthlyPeriod(startTime)
	loc, _ := commissionMonthlyStatLocation(period.Timezone)
	boundaryAt := period.PeriodEndAt + 1
	stats := &CommissionCalendarStats{
		Days:             make([]*CommissionCalendarDayStat, 0),
		PeriodStartAt:    period.PeriodStartAt,
		PeriodEndAt:      period.PeriodEndAt,
		PeriodBoundaryAt: boundaryAt,
		PeriodKey:        period.PeriodKey,
		Timezone:         period.Timezone,
	}

	dayStats := make(map[string]*CommissionCalendarDayStat)
	tx := DB.Model(&EmployeeCommissionLog{}).
		Select("id, created_at, revenue_quota, cost_quota, profit_quota, commission_quota").
		Where("created_at >= ? AND created_at < ?", period.PeriodStartAt, boundaryAt).
		Order("id ASC")
	if employeeUserId > 0 {
		tx = tx.Where("employee_user_id = ?", employeeUserId)
	}

	type calendarLogRow struct {
		Id              int
		CreatedAt       int64
		RevenueQuota    int64
		CostQuota       int64
		ProfitQuota     int64
		CommissionQuota int64
	}
	var rows []calendarLogRow
	if err := tx.FindInBatches(&rows, 1000, func(batchTx *gorm.DB, batch int) error {
		for _, r := range rows {
			local := time.Unix(r.CreatedAt, 0).In(loc)
			date := local.Format("2006-01-02")
			day := dayStats[date]
			if day == nil {
				localDayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
				day = &CommissionCalendarDayStat{
					StatDate: localDayStart.Unix(),
					Date:     date,
				}
				dayStats[date] = day
			}
			day.RevenueQuota += r.RevenueQuota
			day.CostQuota += r.CostQuota
			day.ProfitQuota += r.ProfitQuota
			day.CommissionQuota += r.CommissionQuota
			day.RecordCount++
			stats.Summary.RevenueQuota += r.RevenueQuota
			stats.Summary.CostQuota += r.CostQuota
			stats.Summary.ProfitQuota += r.ProfitQuota
			stats.Summary.CommissionQuota += r.CommissionQuota
			stats.Summary.RecordCount++
		}
		return nil
	}).Error; err != nil {
		return nil, err
	}

	for _, day := range dayStats {
		day.RevenueUsd = common.QuotaToUSD(day.RevenueQuota)
		day.CostUsd = common.QuotaToUSD(day.CostQuota)
		day.ProfitUsd = common.QuotaToUSD(day.ProfitQuota)
		day.CommissionUsd = common.QuotaToUSD(day.CommissionQuota)
		stats.Days = append(stats.Days, day)
	}
	sort.Slice(stats.Days, func(i, j int) bool {
		return stats.Days[i].Date < stats.Days[j].Date
	})

	stats.Summary.RevenueUsd = common.QuotaToUSD(stats.Summary.RevenueQuota)
	stats.Summary.CostUsd = common.QuotaToUSD(stats.Summary.CostQuota)
	stats.Summary.ProfitUsd = common.QuotaToUSD(stats.Summary.ProfitQuota)
	stats.Summary.CommissionUsd = common.QuotaToUSD(stats.Summary.CommissionQuota)
	return stats, nil
}

func GetCommissionSummary(startTime, endTime int64) ([]*CommissionSummaryItem, error) {
	stats, err := GetCommissionStatsByEmployee(startTime, endTime, 0)
	if err != nil {
		return nil, err
	}
	items := make([]*CommissionSummaryItem, 0, len(stats))
	for _, stat := range stats {
		items = append(items, &CommissionSummaryItem{
			EmployeeUserId:  stat.EmployeeUserId,
			TotalRevenue:    stat.TotalRevenue,
			TotalCost:       stat.TotalCost,
			TotalProfit:     stat.TotalProfit,
			TotalCommission: stat.TotalCommission,
			RecordCount:     stat.RecordCount,
		})
	}
	return items, nil
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

// Deprecated: GetCommissionTotals 已被 AdminCommissionOverview 中的内联计算替代。
// 保留供其他可能的调用方使用。若需在概览中使用，优先通过 GetCommissionStatsByEmployeeWithPlan 取全量再求和。
func GetCommissionTotals(startTime, endTime int64) (CommissionTotals, error) {
	return getCommissionTotalsFromStats(startTime, endTime)
}

func getCommissionTotalsFromDailyRange(startDate, endDate int64) (CommissionTotals, error) {
	var t CommissionTotals
	err := DB.Model(&EmployeeCommissionDailyStat{}).
		Select("COALESCE(SUM(revenue_quota),0) as total_revenue, "+
			"COALESCE(SUM(cost_quota),0) as total_cost, "+
			"COALESCE(SUM(profit_quota),0) as total_profit, "+
			"COALESCE(SUM(commission_quota),0) as total_commission, "+
			"COALESCE(SUM(record_count),0) as record_count").
		Where("stat_date >= ? AND stat_date <= ?", startDate, endDate).
		Scan(&t).Error
	return t, err
}

func getCommissionTotalsByEmployeeFromDailyRange(employeeUserId int, startDate, endDate int64) (CommissionTotals, error) {
	var t CommissionTotals
	err := DB.Model(&EmployeeCommissionDailyStat{}).
		Select("COALESCE(SUM(revenue_quota),0) as total_revenue, "+
			"COALESCE(SUM(cost_quota),0) as total_cost, "+
			"COALESCE(SUM(profit_quota),0) as total_profit, "+
			"COALESCE(SUM(commission_quota),0) as total_commission, "+
			"COALESCE(SUM(record_count),0) as record_count").
		Where("employee_user_id = ? AND stat_date >= ? AND stat_date <= ?", employeeUserId, startDate, endDate).
		Scan(&t).Error
	return t, err
}

func addCommissionTotals(dst *CommissionTotals, src CommissionTotals) {
	dst.TotalRevenue += src.TotalRevenue
	dst.TotalCost += src.TotalCost
	dst.TotalProfit += src.TotalProfit
	dst.TotalCommission += src.TotalCommission
	dst.RecordCount += src.RecordCount
}

// GetCommissionTotalsWithPlan sums commission totals from daily aggregates only.
func GetCommissionTotalsWithPlan(plan *ResolvedQueryPlan) (CommissionTotals, error) {
	var totals CommissionTotals
	for _, r := range plan.CoveredRanges {
		t, err := getCommissionTotalsFromDailyRange(r.StartDate, r.EndDate)
		if err != nil {
			return CommissionTotals{}, err
		}
		addCommissionTotals(&totals, t)
	}
	return totals, nil
}

// GetCommissionTotalsByEmployee 汇总指定员工的提成流量总计。
func GetCommissionTotalsByEmployee(employeeUserId int) (CommissionTotals, error) {
	plan, err := ResolveBusinessStatsDailyOnlyQueryPlan(0, 0)
	if err != nil {
		return CommissionTotals{}, err
	}
	var totals CommissionTotals
	for _, r := range plan.CoveredRanges {
		t, err := getCommissionTotalsByEmployeeFromDailyRange(employeeUserId, r.StartDate, r.EndDate)
		if err != nil {
			return CommissionTotals{}, err
		}
		addCommissionTotals(&totals, t)
	}
	return totals, nil
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

// GetCommissionStatsByEmployee 按员工分组汇总，优先使用日聚合统计。
func GetCommissionStatsByEmployee(startTime, endTime int64, limit int) ([]*CommissionEmployeeStat, error) {
	resolved, err := ResolveBusinessStatsDailyOnlyQueryPlan(startTime, endTime)
	if err != nil {
		return nil, err
	}
	return GetCommissionStatsByEmployeeWithPlan(resolved, limit)
}

// GetCommissionStatsByEmployeeIds 仅汇总指定员工 ID 的提成统计，用于分页列表只查当前页数据。
func GetCommissionStatsByEmployeeIds(employeeUserIds []int) ([]*CommissionEmployeeStat, error) {
	var items []*CommissionEmployeeStat
	tx := DB.Model(&EmployeeCommissionDailyStat{}).
		Select("employee_user_id, " +
			"COALESCE(SUM(revenue_quota),0) as total_revenue, " +
			"COALESCE(SUM(cost_quota),0) as total_cost, " +
			"COALESCE(SUM(profit_quota),0) as total_profit, " +
			"COALESCE(SUM(commission_quota),0) as total_commission, " +
			"COALESCE(SUM(record_count),0) as record_count").
		Group("employee_user_id").
		Order("total_commission DESC")
	if len(employeeUserIds) > 0 {
		tx = tx.Where("employee_user_id IN ?", employeeUserIds)
	}
	err := tx.Scan(&items).Error
	return items, err
}

// CreateCommissionLog 写入提成日志（幂等）。
//
// 通过 log_id 唯一索引 + ON CONFLICT DO NOTHING 实现去重：
// 同一笔消费日志（log_id）重复结算时只会落库一次。
// 返回 inserted 表示本次是否真正插入了新记录；调用方据此决定是否累加汇总，
// 避免并发/重试场景下重复计提。
//
// 注意：log_id<=0 时会存为 NULL，无法依赖唯一索引去重；
// 这类记录直接插入并视为已插入，正常消费链路 log_id 恒为正值。
// GetCustomerCommissionTotal 返回指定员工从指定客户获得的累计提成额度。
func GetCustomerCommissionTotal(employeeUserId, customerUserId int) int64 {
	totals, err := GetCustomerCommissionTotals(employeeUserId, []int{customerUserId})
	if err != nil {
		common.SysLog("failed to get customer commission total: " + err.Error())
		return 0
	}
	return totals[customerUserId]
}

// GetCustomerCommissionTotals returns commission totals for the requested customers
// from the employee-customer daily aggregate table. Paged customer views must not
// aggregate employee_commission_logs directly.
func GetCustomerCommissionTotals(employeeUserId int, customerUserIds []int) (map[int]int64, error) {
	totals := make(map[int]int64, len(customerUserIds))
	if employeeUserId == 0 || len(customerUserIds) == 0 {
		return totals, nil
	}
	type row struct {
		CustomerUserId  int
		CommissionQuota int64
	}
	var rows []row
	err := DB.Model(&EmployeeCustomerCommissionDailyStat{}).
		Select("customer_user_id, COALESCE(SUM(commission_quota), 0) as commission_quota").
		Where("employee_user_id = ? AND customer_user_id IN ?", employeeUserId, customerUserIds).
		Group("customer_user_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		totals[r.CustomerUserId] = r.CommissionQuota
	}
	return totals, nil
}

func CreateCommissionLog(log *EmployeeCommissionLog) (inserted bool, err error) {
	if log.CreatedAt == 0 {
		log.CreatedAt = time.Now().Unix()
	}
	// 明细推入缓冲区，log_id 去重保证 inserted 返回值准确（调用方据此决定是否执行提成结算）
	inserted = CheckAndBufferCommissionLog(log)
	if inserted {
		// 日统计增量写入缓冲区（Redis 或内存），由后台定时刷盘
		BufferCommissionDailyStat(log)
	}
	return inserted, nil
}
