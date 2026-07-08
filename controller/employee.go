package controller

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
)

// ============================================================================
// Request and response structures
// ============================================================================

type CreateEmployeeRequest struct {
	UserId int    `json:"user_id" binding:"required"`
	TierId int64  `json:"tier_id"` // initial tier, 0 means unbound
	Remark string `json:"remark"`
}

type UpdateEmployeeRequest struct {
	TierId int64  `json:"tier_id"` // changed tier, 0 means unchanged
	Status int    `json:"status" binding:"required,min=1,max=2"`
	Remark string `json:"remark"`
}

type UpsertChannelCostRequest struct {
	ChannelId int     `json:"channel_id" binding:"required"`
	CostRatio float64 `json:"cost_ratio" binding:"gte=0,lte=1000"`
	Remark    string  `json:"remark"`
}

func normalizePrefixedPage(c *gin.Context, prefix string) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery(prefix+"page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery(prefix+"page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return page, pageSize
}

// ============================================================================
// Admin employee management
// ============================================================================

// AdminListEmployees GET /api/admin/employee
func AdminListEmployees(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	userId, _ := strconv.Atoi(c.Query("user_id"))
	status, _ := strconv.Atoi(c.Query("status"))
	keyword := strings.TrimSpace(c.Query("keyword"))
	sortBy := strings.TrimSpace(c.Query("sort_by"))
	sortOrder := strings.ToLower(strings.TrimSpace(c.DefaultQuery("sort_order", "desc")))
	if sortOrder != "asc" {
		sortOrder = "desc"
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	currentPeriod := model.ResolveCommissionMonthlyPeriod(time.Now().Unix())
	employeeFilter := model.EmployeeFilter{
		UserId:        userId,
		Keyword:       keyword,
		Status:        status,
		SortBy:        sortBy,
		SortOrder:     sortOrder,
		PeriodStartAt: currentPeriod.PeriodStartAt,
	}
	employees, total, err := model.GetAllEmployees(page, pageSize, employeeFilter)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	// Attach user info and current performance metrics.
	type EmployeeWithUser struct {
		*model.EmployeeProfile
		Username               string  `json:"username"`
		DisplayName            string  `json:"display_name"`
		Email                  string  `json:"email"`
		CustomerCount          int     `json:"customer_count"`
		TotalConsumptionQuota  int64   `json:"total_consumption_quota"`
		TotalConsumptionUsd    float64 `json:"total_consumption_usd"`
		TotalCostQuota         int64   `json:"total_cost_quota"`
		TotalCostUsd           float64 `json:"total_cost_usd"`
		TotalProfitQuota       int64   `json:"total_profit_quota"`
		TotalProfitUsd         float64 `json:"total_profit_usd"`
		TotalCommissionQuota   int64   `json:"total_commission_quota"`
		TotalCommissionUsd     float64 `json:"total_commission_usd"`
		PeriodConsumptionQuota int64   `json:"period_consumption_quota"`
		PeriodConsumptionUsd   float64 `json:"period_consumption_usd"`
		PeriodCostQuota        int64   `json:"period_cost_quota"`
		PeriodCostUsd          float64 `json:"period_cost_usd"`
		PeriodProfitQuota      int64   `json:"period_profit_quota"`
		PeriodProfitUsd        float64 `json:"period_profit_usd"`
		PeriodCommissionQuota  int64   `json:"period_commission_quota"`
		PeriodCommissionUsd    float64 `json:"period_commission_usd"`
		PeriodStartAt          int64   `json:"period_start_at"`
		PeriodEndAt            int64   `json:"period_end_at"`
		PeriodKey              string  `json:"period_key"`
		PeriodTimezone         string  `json:"period_timezone"`
		// CurrentProfitQuota/CurrentCommissionQuota 为"本期"值（自上次月度重置以来的增量，
		// 重置功能未启用/未触发过时等价于对应累计值），与 TotalProfit/TotalCommission（历史累计）含义不同。
		CurrentProfitQuota     int64   `json:"current_performance_quota"`
		CurrentProfitUsd       float64 `json:"current_performance_usd"`
		CurrentCommissionQuota int64   `json:"current_commission_quota"`
		CurrentCommissionUsd   float64 `json:"current_commission_usd"`
		CurrentTierId          int64   `json:"current_tier_id"`
		CurrentTierLevel       int     `json:"current_tier_level"`
		CurrentTierGroup       string  `json:"current_tier_group"`
		CurrentTierRate        float64 `json:"current_tier_rate"`
		CurrentTierThreshold   float64 `json:"current_tier_threshold_usd"`
		NextTierId             int64   `json:"next_tier_id"`
		NextTierLevel          int     `json:"next_tier_level"`
		NextTierGroup          string  `json:"next_tier_group"`
		NextTierRate           float64 `json:"next_tier_rate"`
		NextTierThreshold      float64 `json:"next_tier_threshold_usd"`
	}

	employeeUserIds := make([]int, 0, len(employees))
	for _, emp := range employees {
		employeeUserIds = append(employeeUserIds, emp.UserId)
	}

	var (
		profitStats                 []*model.CommissionEmployeeStat
		customerConsumptionByUserId map[int]int64
		customerCountsByUserId      map[int]int
		tierLevelsByUserId          map[int]*model.EmployeeTierLevel
		currentPeriodStatsByUserId  map[int]model.EmployeeCurrentResetPeriodStat
		userMap                     map[int]*model.User
	)
	eg, _ := errgroup.WithContext(c.Request.Context())
	eg.Go(func() error {
		var err error
		profitStats, err = model.GetCommissionStatsByEmployeeIds(employeeUserIds)
		return err
	})
	eg.Go(func() error {
		var err error
		customerConsumptionByUserId, err = model.GetCustomerUsedQuotaTotalsByEmployees(employeeUserIds)
		return err
	})
	eg.Go(func() error {
		var err error
		customerCountsByUserId, err = model.GetCustomerCountsByEmployees(employeeUserIds)
		return err
	})
	eg.Go(func() error {
		var err error
		tierLevelsByUserId, err = model.GetTierLevelsByUserIds(employeeUserIds)
		return err
	})
	eg.Go(func() error {
		var err error
		currentPeriodStatsByUserId, err = model.GetCurrentResetPeriodStatsByEmployeeUserIds(employeeUserIds)
		return err
	})
	eg.Go(func() error {
		var err error
		userMap, err = model.GetUsersByIds(employeeUserIds)
		return err
	})
	if err := eg.Wait(); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	statsByUserId := make(map[int]*model.CommissionEmployeeStat, len(profitStats))
	for _, s := range profitStats {
		statsByUserId[s.EmployeeUserId] = s
	}
	allTiers := model.GetAllTiersCached()
	tierById := make(map[int64]*model.EmployeeCommissionTier, len(allTiers))
	for _, t := range allTiers {
		tierById[t.Id] = t
	}
	findNextTier := func(currentLevel int, group string, currentProfitUsd float64) *model.EmployeeCommissionTier {
		for _, t := range allTiers {
			if group != "" && t.Group != group {
				continue
			}
			if t.Level <= currentLevel {
				continue
			}
			if t.ThresholdUsd > currentProfitUsd {
				return t
			}
		}
		return nil
	}

	items := make([]EmployeeWithUser, 0, len(employees))
	for _, emp := range employees {
		totalConsumptionQuota := customerConsumptionByUserId[emp.UserId]
		var totalCostQuota, totalProfitQuota, totalCommissionQuota int64
		if stat := statsByUserId[emp.UserId]; stat != nil {
			totalCostQuota = stat.TotalCost
			totalProfitQuota = stat.TotalProfit
			totalCommissionQuota = stat.TotalCommission
		}
		periodConsumptionQuota := int64(0)
		periodCostQuota := int64(0)
		periodProfitQuota := int64(0)
		periodCommissionQuota := int64(0)
		periodStartAt := currentPeriod.PeriodStartAt
		periodEndAt := currentPeriod.PeriodEndAt
		periodKey := currentPeriod.PeriodKey
		periodTimezone := currentPeriod.Timezone
		if stat, ok := currentPeriodStatsByUserId[emp.UserId]; ok {
			periodConsumptionQuota = stat.RevenueQuota
			periodCostQuota = stat.CostQuota
			periodProfitQuota = stat.ProfitQuota
			periodCommissionQuota = stat.CommissionQuota
			periodStartAt = stat.ResetStartedAt
			periodEndAt = stat.ResetEndedAt
			periodKey = stat.PeriodKey
			periodTimezone = stat.Timezone
		}

		item := EmployeeWithUser{
			EmployeeProfile:        emp,
			CustomerCount:          customerCountsByUserId[emp.UserId],
			TotalConsumptionQuota:  totalConsumptionQuota,
			TotalConsumptionUsd:    common.QuotaToUSD(totalConsumptionQuota),
			TotalCostQuota:         totalCostQuota,
			TotalCostUsd:           common.QuotaToUSD(totalCostQuota),
			TotalProfitQuota:       totalProfitQuota,
			TotalProfitUsd:         common.QuotaToUSD(totalProfitQuota),
			TotalCommissionQuota:   totalCommissionQuota,
			TotalCommissionUsd:     common.QuotaToUSD(totalCommissionQuota),
			PeriodConsumptionQuota: periodConsumptionQuota,
			PeriodConsumptionUsd:   common.QuotaToUSD(periodConsumptionQuota),
			PeriodCostQuota:        periodCostQuota,
			PeriodCostUsd:          common.QuotaToUSD(periodCostQuota),
			PeriodProfitQuota:      periodProfitQuota,
			PeriodProfitUsd:        common.QuotaToUSD(periodProfitQuota),
			PeriodCommissionQuota:  periodCommissionQuota,
			PeriodCommissionUsd:    common.QuotaToUSD(periodCommissionQuota),
			PeriodStartAt:          periodStartAt,
			PeriodEndAt:            periodEndAt,
			PeriodKey:              periodKey,
			PeriodTimezone:         periodTimezone,
			CurrentProfitQuota:     periodProfitQuota,
			CurrentProfitUsd:       common.QuotaToUSD(periodProfitQuota),
			// CurrentCommissionQuota 在等级利率确定后赋值（提成 = 本期利润 × 当前等级利率）。
		}
		currentLevel := 0
		currentGroup := ""
		if lvl, ok := tierLevelsByUserId[emp.UserId]; ok && lvl.TierId != 0 {
			item.CurrentTierId = lvl.TierId
			if t, ok2 := tierById[lvl.TierId]; ok2 {
				item.CurrentTierLevel = t.Level
				item.CurrentTierGroup = t.Group
				item.CurrentTierRate = t.Rate
				item.CurrentTierThreshold = t.ThresholdUsd
				currentLevel = t.Level
				currentGroup = t.Group
			}
		}
		// 提成模型：升级后所有本期业绩统一按当前等级利率重算，commission = profit × rate。
		correctedCommission := service.CalcCommissionQuota(periodProfitQuota, item.CurrentTierRate)
		item.CurrentCommissionQuota = correctedCommission
		item.CurrentCommissionUsd = common.QuotaToUSD(correctedCommission)
		item.PeriodCommissionQuota = correctedCommission
		item.PeriodCommissionUsd = common.QuotaToUSD(correctedCommission)
		if nextTier := findNextTier(currentLevel, currentGroup, item.CurrentProfitUsd); nextTier != nil {
			item.NextTierId = nextTier.Id
			item.NextTierLevel = nextTier.Level
			item.NextTierGroup = nextTier.Group
			item.NextTierRate = nextTier.Rate
			item.NextTierThreshold = nextTier.ThresholdUsd
		}
		if u := userMap[emp.UserId]; u != nil {
			item.Username = u.Username
			item.DisplayName = u.DisplayName
			item.Email = u.Email
			item.EmployeeProfile.Remark = u.Remark
		}
		items = append(items, item)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":     items,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// AdminCreateEmployee POST /api/admin/employee
func AdminCreateEmployee(c *gin.Context) {
	var req CreateEmployeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	user, err := model.GetUserById(req.UserId, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "user not found"})
		return
	}
	if req.TierId > 0 {
		exists, err := model.TierExists(req.TierId)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		if !exists {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "tier not found"})
			return
		}
	}
	emp := &model.EmployeeProfile{
		UserId: req.UserId,
		Status: 1,
		Remark: req.Remark,
	}
	if emp.Remark == "" {
		emp.Remark = user.Remark
	}
	if err := model.CreateEmployee(emp); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if req.TierId > 0 {
		operatedBy := c.GetInt("id")
		if err := model.SetTierLevel(req.UserId, req.TierId, "manual", operatedBy, "", 0); err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": emp})
}

// AdminUpdateEmployee PUT /api/admin/employee/:id
func AdminUpdateEmployee(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid id"})
		return
	}
	var req UpdateEmployeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	emp := &model.EmployeeProfile{
		Id:     id,
		Status: req.Status,
		Remark: req.Remark,
	}
	if err := model.UpdateEmployee(emp); err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "employee profile not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if req.TierId > 0 {
		existingEmp, _ := model.GetEmployeeById(id)
		if existingEmp != nil {
			currentLevel, _ := model.GetOrCreateTierLevel(existingEmp.UserId, false)
			if currentLevel == nil || currentLevel.TierId != req.TierId {
				operatedBy := c.GetInt("id")
				if err := model.SetTierLevel(existingEmp.UserId, req.TierId, "manual", operatedBy, "", 0); err != nil {
					c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
					return
				}
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AdminDeleteEmployee DELETE /api/admin/employee/:id
func AdminDeleteEmployee(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid id"})
		return
	}
	if err := model.DisableEmployee(id); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// Admin manual performance adjustment（手工业绩调整）
// 设计见 docs/employee-performance-adjustment-design.md。
// ============================================================================

type AddEmployeePerformanceRequest struct {
	ProfitUsd     float64 `json:"profit_usd" binding:"required"` // 业绩金额（USD，可负，非 0）
	Reason        string  `json:"reason"`
	PeriodStartAt int64   `json:"period_start_at"` // 0=当前周期；>0=补录到该历史周期
}

// AdminAddEmployeePerformance POST /api/admin/employee/:id/performance
func AdminAddEmployeePerformance(c *gin.Context) {
	empId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid employee id"})
		return
	}
	var req AddEmployeePerformanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	emp, err := model.GetEmployeeById(empId)
	if err != nil || emp == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "employee not found"})
		return
	}
	// 用户可控金额 → quota：走集中式饱和转换（int32 边界 + NaN 处理），符合项目 billing 安全不变量。
	// 手工调整是管理员单次操作，越界时显式报错而非静默钳制，避免把一笔巨额调整悄悄截断。
	profitQuotaInt, clamp := common.QuotaFromDecimalChecked(
		decimal.NewFromFloat(req.ProfitUsd).Mul(decimal.NewFromFloat(common.QuotaPerUnit)),
	)
	if clamp != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "profit amount out of range"})
		return
	}
	profitQuota := int64(profitQuotaInt)
	if profitQuota == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "profit amount is too small"})
		return
	}
	operatedBy := c.GetInt("id")
	result, err := model.AddEmployeePerformance(emp.UserId, profitQuota, strings.TrimSpace(req.Reason), operatedBy, req.PeriodStartAt)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// AdminRevertPerformanceAdjustment POST /api/admin/employee/performance/:logId/revert
func AdminRevertPerformanceAdjustment(c *gin.Context) {
	logId, err := strconv.Atoi(c.Param("logId"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid id"})
		return
	}
	operatedBy := c.GetInt("id")
	if err := model.RevertEmployeePerformance(logId, operatedBy); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// Admin commission logs
// ============================================================================

// AdminListCommissionLogs GET /api/admin/employee/commission
func AdminListCommissionLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	employeeUserId, _ := strconv.Atoi(c.Query("employee_user_id"))
	customerUserId, _ := strconv.Atoi(c.Query("customer_user_id"))
	channelId, _ := strconv.Atoi(c.Query("channel_id"))
	modelName := strings.TrimSpace(c.Query("model_name"))
	lossStatus := strings.TrimSpace(c.Query("loss_status"))
	timeRange, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	logs, total, err := model.GetCommissionLogs(model.CommissionLogFilter{
		EmployeeUserId: employeeUserId,
		CustomerUserId: customerUserId,
		ModelName:      modelName,
		ChannelId:      channelId,
		LossStatus:     lossStatus,
		StartTime:      timeRange.StartTime,
		EndTime:        timeRange.EndTime,
		Page:           page,
		PageSize:       pageSize,
	})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	// 按页解析渠道名称（兼容已删除渠道），明细表本身不存名称快照，避免大表回填
	channelNames := model.ResolveChannelDisplayNames(commissionLogChannelIds(logs))
	type LogWithChannel struct {
		*model.EmployeeCommissionLog
		ChannelName string `json:"channel_name"`
	}
	items := make([]LogWithChannel, 0, len(logs))
	for _, l := range logs {
		items = append(items, LogWithChannel{
			EmployeeCommissionLog: l,
			ChannelName:           channelNames[l.ChannelId],
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":     items,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

func commissionLogChannelIds(logs []*model.EmployeeCommissionLog) []int {
	ids := make([]int, 0, len(logs))
	for _, l := range logs {
		ids = append(ids, l.ChannelId)
	}
	return ids
}

// AdminListCommissionChannelOptions GET /api/admin/employee/commission/channels
// 渠道筛选下拉选项（含已删除渠道的历史名称快照）。
func AdminListCommissionChannelOptions(c *gin.Context) {
	page, pageSize := normalizePage(c)
	options, total, err := model.ListChannelDisplayOptions(page, pageSize)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":     options,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// AdminCommissionSummary GET /api/admin/employee/commission/summary
func AdminCommissionSummary(c *gin.Context) {
	timeRange, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	items, err := model.GetCommissionSummary(timeRange.StartTime, timeRange.EndTime)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": items})
}

// AdminListCommissionResetPeriodStats GET /api/admin/employee/commission/monthly
func AdminListCommissionResetPeriodStats(c *gin.Context) {
	page, pageSize := normalizePage(c)
	employeeUserId, _ := strconv.Atoi(c.Query("employee_user_id"))
	timeRange, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	items, total, err := model.GetCommissionResetPeriodStats(model.CommissionResetPeriodStatFilter{
		EmployeeUserId: employeeUserId,
		StartTime:      timeRange.StartTime,
		EndTime:        timeRange.EndTime,
		Page:           page,
		PageSize:       pageSize,
	})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":     items,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// AdminCommissionCalendarStats GET /api/admin/employee/commission/calendar
func AdminCommissionCalendarStats(c *gin.Context) {
	employeeUserId, _ := strconv.Atoi(c.Query("employee_user_id"))
	timeRange, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if timeRange.StartTime == 0 || timeRange.EndTime == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "start_time and end_time are required"})
		return
	}
	stats, err := model.GetCommissionCalendarStats(timeRange.StartTime, timeRange.EndTime, employeeUserId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": stats})
}

// AdminCommissionMonthlyExport GET /api/admin/employee/commission/monthly-export
// 返回所查月份下每个在职员工的业绩/分红/等级/名字（供前端生成 CSV 导出）。
func AdminCommissionMonthlyExport(c *gin.Context) {
	timeRange, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if timeRange.StartTime == 0 || timeRange.EndTime == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "start_time and end_time are required"})
		return
	}
	data, err := model.GetCommissionMonthlyEmployeeExport(timeRange.StartTime, timeRange.EndTime)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// AdminCommissionOverview GET /api/admin/employee/overview
func AdminCommissionOverview(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	timeRange, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	channelPage, channelPageSize := normalizePrefixedPage(c, "channel_")
	employeePage, employeePageSize := normalizePrefixedPage(c, "employee_")
	channelKeyword := strings.ToLower(strings.TrimSpace(c.Query("channel_keyword")))
	channelSortBy := strings.TrimSpace(c.DefaultQuery("channel_sort_by", "est_profit_quota"))
	channelSortOrder := strings.ToLower(strings.TrimSpace(c.DefaultQuery("channel_sort_order", "desc")))
	if channelSortOrder != "asc" {
		channelSortOrder = "desc"
	}

	// Business overview is intentionally daily-aggregate only. Do not fill gaps
	// from ledger/detail tables here; those large scans belong to detail views.
	queryPlan, err := model.ResolveBusinessStatsDailyOnlyQueryPlanWithContext(ctx, timeRange.StartTime, timeRange.EndTime)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	var (
		channelStats   []*model.ConsumptionCostChannelStat
		channelSummary model.ConsumptionCostChannelSummary
		channelTotal   int64
		byEmployee     []*model.CommissionEmployeeStat
		employeeTotal  int64
		totals         model.CommissionTotals
	)

	g, groupCtx := errgroup.WithContext(ctx)
	queryPlan.Context = groupCtx
	g.Go(func() error {
		var err error
		channelStats, channelTotal, err = model.GetConsumptionCostByChannelPageWithPlan(queryPlan, channelPage, channelPageSize, channelKeyword, channelSortBy, channelSortOrder)
		return err
	})
	g.Go(func() error {
		var err error
		channelSummary, err = model.GetConsumptionCostChannelSummaryWithPlan(queryPlan)
		return err
	})
	// Fetch paged employee stats and totals concurrently.
	g.Go(func() error {
		var err error
		byEmployee, employeeTotal, err = model.GetCommissionStatsByEmployeePageWithPlan(queryPlan, employeePage, employeePageSize)
		return err
	})
	g.Go(func() error {
		var err error
		totals, err = model.GetCommissionTotalsWithPlan(queryPlan)
		return err
	})

	if err := g.Wait(); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	// Reuse the shared channel summary for platform totals.
	costTotals := channelSummary.Totals

	platformConsumption := costTotals.TotalRevenue
	platformCostQuota := costTotals.TotalCost
	platformProfitQuota := platformConsumption - platformCostQuota
	var platformGrossMargin float64
	if platformConsumption != 0 {
		platformGrossMargin = float64(platformProfitQuota) / float64(platformConsumption)
	}

	type ChannelProfitItem struct {
		ChannelId        int     `json:"channel_id"`
		ChannelName      string  `json:"channel_name"`
		ConsumptionQuota int64   `json:"consumption_quota"`
		EstCostQuota     int64   `json:"est_cost_quota"`
		EstProfitQuota   int64   `json:"est_profit_quota"`
		EstGrossMargin   float64 `json:"est_gross_margin"`
		CostRatio        float64 `json:"cost_ratio"`
	}
	channelProfit := make([]ChannelProfitItem, 0, len(channelStats))
	for _, s := range channelStats {
		item := ChannelProfitItem{
			ChannelId:        s.ChannelId,
			ConsumptionQuota: s.TotalRevenue,
			EstCostQuota:     s.TotalCost,
			EstProfitQuota:   s.TotalRevenue - s.TotalCost,
			CostRatio:        s.CostRatio,
		}
		if s.TotalRevenue != 0 {
			item.EstGrossMargin = float64(item.EstProfitQuota) / float64(s.TotalRevenue)
		}
		item.ChannelName = s.ChannelName
		channelProfit = append(channelProfit, item)
	}
	type EmployeeStatWithUser struct {
		*model.CommissionEmployeeStat
		Username        string  `json:"username"`
		DisplayName     string  `json:"display_name"`
		Remark          string  `json:"remark"`
		CurrentTierRate float64 `json:"current_tier_rate"`
	}
	empItems := make([]EmployeeStatWithUser, 0, len(byEmployee))
	if len(byEmployee) > 0 {
		empIds := make([]int, 0, len(byEmployee))
		for _, s := range byEmployee {
			empIds = append(empIds, s.EmployeeUserId)
		}
		userMap, _ := model.GetUsersByIdsUnscopedWithContext(ctx, empIds)
		tierLevels, _ := model.GetTierLevelsByUserIds(empIds)
		allTiers := model.GetAllTiersCached()
		tierById := make(map[int64]*model.EmployeeCommissionTier, len(allTiers))
		for _, t := range allTiers {
			tierById[t.Id] = t
		}
		for _, s := range byEmployee {
			item := EmployeeStatWithUser{CommissionEmployeeStat: s}
			if u := userMap[s.EmployeeUserId]; u != nil {
				item.Username = u.Username
				item.DisplayName = u.DisplayName
				item.Remark = u.Remark
			}
			if lvl, ok := tierLevels[s.EmployeeUserId]; ok && lvl.TierId != 0 {
				if t, ok2 := tierById[lvl.TierId]; ok2 {
					item.CurrentTierRate = t.Rate
				}
			}
			empItems = append(empItems, item)
		}
	}

	var grossMargin float64
	if totals.TotalRevenue != 0 {
		grossMargin = float64(totals.TotalProfit) / float64(totals.TotalRevenue)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"platform": gin.H{
				"total_consumption_quota":  platformConsumption,
				"total_consumption_usd":    common.QuotaToUSD(platformConsumption),
				"request_count":            costTotals.RecordCount,
				"est_cost_quota":           platformCostQuota,
				"est_cost_usd":             common.QuotaToUSD(platformCostQuota),
				"est_profit_quota":         platformProfitQuota,
				"est_profit_usd":           common.QuotaToUSD(platformProfitQuota),
				"est_gross_margin":         platformGrossMargin,
				"profitable_channel_count": channelSummary.ProfitableChannelCount,
				"loss_channel_count":       channelSummary.LossChannelCount,
			},
			"commission": gin.H{
				"total_revenue_quota":    totals.TotalRevenue,
				"total_cost_quota":       totals.TotalCost,
				"total_profit_quota":     totals.TotalProfit,
				"total_commission_quota": totals.TotalCommission,
				"record_count":           totals.RecordCount,
				"gross_margin":           grossMargin,
				"total_revenue_usd":      common.QuotaToUSD(totals.TotalRevenue),
				"total_cost_usd":         common.QuotaToUSD(totals.TotalCost),
				"total_profit_usd":       common.QuotaToUSD(totals.TotalProfit),
				"total_commission_usd":   common.QuotaToUSD(totals.TotalCommission),
			},
			"by_employee":                   empItems,
			"by_employee_total":             employeeTotal,
			"by_employee_page":              employeePage,
			"by_employee_page_size":         employeePageSize,
			"by_channel_platform":           channelProfit,
			"by_channel_platform_total":     channelTotal,
			"by_channel_platform_page":      channelPage,
			"by_channel_platform_page_size": channelPageSize,
		},
	})
}

// AdminChannelProfitPage GET /api/admin/employee/overview/channels
func AdminChannelProfitPage(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	timeRange, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	page, pageSize := normalizePrefixedPage(c, "channel_")
	keyword := strings.ToLower(strings.TrimSpace(c.Query("channel_keyword")))
	sortBy := strings.TrimSpace(c.DefaultQuery("channel_sort_by", "est_profit_quota"))
	sortOrder := strings.ToLower(strings.TrimSpace(c.DefaultQuery("channel_sort_order", "desc")))
	if sortOrder != "asc" {
		sortOrder = "desc"
	}

	queryPlan, err := model.ResolveBusinessStatsDailyOnlyQueryPlanWithContext(ctx, timeRange.StartTime, timeRange.EndTime)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	stats, total, err := model.GetConsumptionCostByChannelPageWithPlan(queryPlan, page, pageSize, keyword, sortBy, sortOrder)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	type channelProfitItem struct {
		ChannelId        int     `json:"channel_id"`
		ChannelName      string  `json:"channel_name"`
		ConsumptionQuota int64   `json:"consumption_quota"`
		EstCostQuota     int64   `json:"est_cost_quota"`
		EstProfitQuota   int64   `json:"est_profit_quota"`
		EstGrossMargin   float64 `json:"est_gross_margin"`
		CostRatio        float64 `json:"cost_ratio"`
	}
	items := make([]channelProfitItem, 0, len(stats))
	for _, s := range stats {
		item := channelProfitItem{
			ChannelId:        s.ChannelId,
			ConsumptionQuota: s.TotalRevenue,
			EstCostQuota:     s.TotalCost,
			EstProfitQuota:   s.TotalRevenue - s.TotalCost,
			CostRatio:        s.CostRatio,
			ChannelName:      s.ChannelName,
		}
		if s.TotalRevenue != 0 {
			item.EstGrossMargin = float64(item.EstProfitQuota) / float64(s.TotalRevenue)
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":     items,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// ============================================================================
// Admin channel cost configuration
// ============================================================================

// AdminListChannelCosts GET /api/admin/channel/cost
func AdminListChannelCosts(c *gin.Context) {
	configs, err := model.GetAllChannelCostConfigs()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": configs})
}

// AdminUpsertChannelCost POST /api/admin/channel/cost
func AdminUpsertChannelCost(c *gin.Context) {
	var req UpsertChannelCostRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := model.UpsertChannelCostConfig(req.ChannelId, req.CostRatio, req.Remark); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AdminDeleteChannelCost DELETE /api/admin/channel/cost/:channel_id
func AdminDeleteChannelCost(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("channel_id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid channel id"})
		return
	}
	if err := model.DeleteChannelCostConfig(channelId); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// Admin tiered commission configuration
// ============================================================================

type CreateTierRequest struct {
	Level        int     `json:"level" binding:"required,min=1"`
	Group        string  `json:"group"`
	ThresholdUsd float64 `json:"threshold_usd" binding:"gte=0"`
	Rate         float64 `json:"rate" binding:"gte=0,lte=1"`
}

type UpdateTierRequest struct {
	Level        int     `json:"level" binding:"required,min=1"`
	Group        string  `json:"group"`
	ThresholdUsd float64 `json:"threshold_usd" binding:"gte=0"`
	Rate         float64 `json:"rate" binding:"gte=0,lte=1"`
}

type SetEmployeeTierRequest struct {
	TierId int64  `json:"tier_id"` // 0 = clear tier
	Source string `json:"source"`  // manual / custom
	Remark string `json:"remark"`
}

// AdminListTiers GET /api/admin/employee/tiers
func AdminListTiers(c *gin.Context) {
	if c.Query("page") != "" || c.Query("page_size") != "" {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
		if page < 1 {
			page = 1
		}
		if pageSize < 1 || pageSize > 100 {
			pageSize = 20
		}
		tiers, total, err := model.GetTiers(page, pageSize)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"items":     tiers,
				"total":     total,
				"page":      page,
				"page_size": pageSize,
			},
		})
		return
	}

	tiers, err := model.GetAllTiers()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": tiers})
}

// AdminCreateTier POST /api/admin/employee/tiers
func AdminCreateTier(c *gin.Context) {
	var req CreateTierRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	group := strings.TrimSpace(req.Group)
	if group == "" {
		group = "通用"
	}
	tier := &model.EmployeeCommissionTier{
		Level:        req.Level,
		Group:        group,
		ThresholdUsd: req.ThresholdUsd,
		Rate:         req.Rate,
	}
	if err := model.CreateTier(tier); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": tier})
}

// AdminUpdateTier PUT /api/admin/employee/tiers/:id
func AdminUpdateTier(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid id"})
		return
	}
	var req UpdateTierRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	updateGroup := strings.TrimSpace(req.Group)
	if updateGroup == "" {
		updateGroup = "通用"
	}
	tier := &model.EmployeeCommissionTier{
		Id:           id,
		Level:        req.Level,
		Group:        updateGroup,
		ThresholdUsd: req.ThresholdUsd,
		Rate:         req.Rate,
	}
	if err := model.UpdateTier(tier); err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "tier not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AdminDeleteTier DELETE /api/admin/employee/tiers/:id
func AdminDeleteTier(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid id"})
		return
	}
	if err := model.DeleteTier(id); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AdminSetEmployeeTier POST /api/admin/employee/:id/tier
func AdminSetEmployeeTier(c *gin.Context) {
	empId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid employee id"})
		return
	}
	var req SetEmployeeTierRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	emp, err := model.GetEmployeeById(empId)
	if err != nil || emp == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "employee not found"})
		return
	}
	source := req.Source
	if source != "manual" && source != "custom" {
		source = "manual"
	}
	operatedBy := c.GetInt("id")
	if err := model.SetTierLevel(emp.UserId, req.TierId, source, operatedBy, req.Remark, 0); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AdminListTierLogs GET /api/admin/employee/tiers/logs
func AdminListTierLogs(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Query("user_id"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	logs, total, err := model.GetTierLogs(model.TierLogFilter{
		UserId:   userId,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":     logs,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// AdminGetTierResetConfig GET /api/admin/employee/tiers/reset-config
func AdminGetTierResetConfig(c *gin.Context) {
	cfg := operation_setting.GetCommissionTierResetSetting()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"enabled":       cfg.Enabled,
			"period_mode":   cfg.PeriodMode,
			"reset_day":     cfg.ResetDay,
			"reset_hour":    cfg.ResetHour,
			"reset_minute":  cfg.ResetMinute,
			"reset_second":  cfg.ResetSecond,
			"timezone":      cfg.Timezone,
			"last_reset_at": cfg.LastResetAt,
			"next_reset_at": service.NextScheduledResetAt(time.Now()),
		},
	})
}

// AdminTriggerTierReset POST /api/admin/employee/tiers/reset-now
// 立即执行一次提成等级与"本期业绩/本期提成"重置：将所有员工等级重置为各自分组最低等级，
// 并刷新周期基准（不影响历史累计数据、台账明细及待结算/已结算提成余额）。
func AdminTriggerTierReset(c *gin.Context) {
	operatedBy := c.GetInt("id")
	// 用 time.Now() 作为 resetAt，使 BaselineResetAt 取当前时刻，与重置前的 reset_started_at
	// 形成区别，从而让日历只显示重置后产生的新数据，达到"清空"旧统计的效果。
	resetAt := time.Now().Unix()
	processed, err := service.RunCommissionTierResetNow(resetAt, operatedBy)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"processed": processed,
			"reset_at":  resetAt,
		},
	})
}

// AdminSwitchCommissionPeriod POST /api/admin/employee/tiers/switch-period
// 在管理员切换统计口径（统计方式 / 每月重置日 / 时区）后调用，把"本期"安全对齐到新口径下
// 的本期起点 P，并按两个开关分别控制：reset_tiers 是否重置员工等级、include_period_data
// 是否把按新边界属于本期的数据归并进本期。默认（false/true）= 保留等级 + 数据不丢。
// 成功后把 last_reset_at arm 到 P，避免定时任务补跑一次重置。
func AdminSwitchCommissionPeriod(c *gin.Context) {
	var req struct {
		ResetTiers        bool  `json:"reset_tiers"`
		IncludePeriodData *bool `json:"include_period_data"`
	}
	// 空 body / 缺省字段视为默认值：不重置等级、纳入本期数据。
	_ = c.ShouldBindJSON(&req)
	includePeriodData := true
	if req.IncludePeriodData != nil {
		includePeriodData = *req.IncludePeriodData
	}

	operatedBy := c.GetInt("id")
	periodStart := model.ResolveCommissionMonthlyPeriod(time.Now().Unix()).PeriodStartAt

	processed, err := model.SwitchCommissionPeriod(periodStart, req.ResetTiers, includePeriodData, operatedBy)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	// 把 last_reset_at 对齐到本期起点，避免定时任务把这次口径切换当成"错过的边界"补跑重置。
	service.ArmCommissionTierResetAt(periodStart)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"processed":       processed,
			"period_start_at": periodStart,
			"reset_tiers":     req.ResetTiers,
			"include_period":  req.IncludePeriodData,
			"next_reset_at":   service.NextScheduledResetAt(time.Now()),
		},
	})
}

// ============================================================================
// Employee self-service endpoints.
// ============================================================================

// GetMyEmployeeProfile GET /api/user/employee/profile
func GetMyEmployeeProfile(c *gin.Context) {
	userId := c.GetInt("id")
	emp := model.GetEmployeeByUserId(userId)
	if emp == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "current user is not an employee"})
		return
	}
	ext, _ := model.GetUserExtension(userId)
	tierLevel, tier := model.GetTierLevelByUserId(userId)
	tierInfo := gin.H{"tier_id": int64(0), "tier_level": 0, "tier_group": "", "tier_rate": 0.0, "tier_threshold_usd": 0.0}
	if tierLevel != nil && tierLevel.TierId != 0 && tier != nil {
		tierInfo = gin.H{
			"tier_id":            tierLevel.TierId,
			"tier_level":         tier.Level,
			"tier_group":         tier.Group,
			"tier_rate":          tier.Rate,
			"tier_threshold_usd": tier.ThresholdUsd,
		}
		// Find the next tier: same group, level+1. Used by the employee self-service UI.
		allTiers := model.GetAllTiersCached()
		for _, t := range allTiers {
			if t.Group == tier.Group && t.Level == tier.Level+1 {
				tierInfo["next_tier"] = gin.H{
					"tier_id":            t.Id,
					"tier_level":         t.Level,
					"tier_rate":          t.Rate,
					"tier_threshold_usd": t.ThresholdUsd,
				}
				break
			}
		}
	}
	var baselineProfitQuota, baselineCommissionQuota, baselineResetAt int64
	if tierLevel != nil {
		baselineProfitQuota = tierLevel.BaselineProfitQuota
		baselineCommissionQuota = tierLevel.BaselineCommissionQuota
		baselineResetAt = tierLevel.BaselineResetAt
	}
	// 当期利润/提成从 employee_commission_reset_period_daily_stats 聚合。
	// 提成模型：升级后所有本期业绩统一按当前等级利率重算（非逐笔累加），
	// 因此 current_commission_quota = SUM(profit_quota) × current_tier_rate。
	profilePeriodStats, _ := model.GetCurrentResetPeriodStatsByEmployeeUserIds([]int{userId})
	currentPeriodStat := profilePeriodStats[userId]
	currentProfitQuota := currentPeriodStat.ProfitQuota
	var currentTierRate float64
	if tier != nil {
		currentTierRate = tier.Rate
	}
	currentCommissionQuota := service.CalcCommissionQuota(currentProfitQuota, currentTierRate)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"profile":   emp,
			"extension": ext,
			"tier":      tierInfo,
			"period": gin.H{
				"baseline_reset_at":         baselineResetAt,
				"baseline_profit_quota":     baselineProfitQuota,
				"baseline_profit_usd":       common.QuotaToUSD(baselineProfitQuota),
				"baseline_commission_quota": baselineCommissionQuota,
				"baseline_commission_usd":   common.QuotaToUSD(baselineCommissionQuota),
				"current_performance_quota": currentProfitQuota,
				"current_performance_usd":   common.QuotaToUSD(currentProfitQuota),
				"current_commission_quota":  currentCommissionQuota,
				"current_commission_usd":    common.QuotaToUSD(currentCommissionQuota),
			},
		},
	})
}

// GetMyCommissionLogs GET /api/user/employee/commission
func GetMyCommissionLogs(c *gin.Context) {
	userId := c.GetInt("id")
	if !model.IsEmployee(userId) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "permission denied"})
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	customerUserId, _ := strconv.Atoi(c.Query("customer_user_id"))
	channelId, _ := strconv.Atoi(c.Query("channel_id"))
	modelName := strings.TrimSpace(c.Query("model_name"))
	lossStatus := strings.TrimSpace(c.Query("loss_status"))
	timeRange, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	logs, total, err := model.GetCommissionLogs(model.CommissionLogFilter{
		EmployeeUserId: userId,
		CustomerUserId: customerUserId,
		ModelName:      modelName,
		ChannelId:      channelId,
		LossStatus:     lossStatus,
		StartTime:      timeRange.StartTime,
		EndTime:        timeRange.EndTime,
		Page:           page,
		PageSize:       pageSize,
	})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	// 注意：员工端不返回渠道名称（渠道名可能包含上游内部信息），仅管理员端展示
	type SafeLog struct {
		*model.EmployeeCommissionLog
		CustomerUserIdMasked string `json:"customer_user_id_masked"`
		CustomerUserId       int    `json:"customer_user_id,omitempty"`
	}
	safeItems := make([]SafeLog, 0, len(logs))
	for _, l := range logs {
		safeItems = append(safeItems, SafeLog{
			EmployeeCommissionLog: l,
			CustomerUserIdMasked:  maskUserId(l.CustomerUserId),
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":     safeItems,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// GetMyCommissionSummary GET /api/user/employee/commission/summary
func GetMyCommissionSummary(c *gin.Context) {
	userId := c.GetInt("id")
	if !model.IsEmployee(userId) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "permission denied"})
		return
	}
	ext, _ := model.GetUserExtension(userId)

	_, summaryTier := model.GetTierLevelByUserId(userId)
	var summaryTierRate float64
	if summaryTier != nil {
		summaryTierRate = summaryTier.Rate
	}

	// 当期利润/提成从 employee_commission_reset_period_daily_stats 聚合。
	// 提成模型：升级后所有本期业绩统一按当前等级利率重算（非逐笔累加），
	// 因此 current_commission_quota = SUM(profit_quota) × current_tier_rate。
	summaryPeriodStats, _ := model.GetCurrentResetPeriodStatsByEmployeeUserIds([]int{userId})
	currentPeriodStat := summaryPeriodStats[userId]
	currentProfitQuota := currentPeriodStat.ProfitQuota
	currentCommissionQuota := service.CalcCommissionQuota(currentProfitQuota, summaryTierRate)

	// 全历史累计从 employee_commission_daily_stats 聚合（employee_user_id 有独立索引），
	// 与管理端"员工管理"历史总计保持同源，无需单独维护快照。
	var profitTotalQuota, commissionTotalQuota int64
	if allTimeStats, err2 := model.GetCommissionStatsByEmployeeIds([]int{userId}); err2 == nil && len(allTimeStats) > 0 {
		profitTotalQuota = allTimeStats[0].TotalProfit
		commissionTotalQuota = allTimeStats[0].TotalCommission
	}

	customerConsumptionByUserId, err := model.GetCustomerUsedQuotaTotalsByEmployees([]int{userId})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	customerTotalConsumptionQuota := customerConsumptionByUserId[userId]

	var commissionPendingQuota, commissionSettledQuota int64
	if ext != nil {
		commissionPendingQuota = ext.CommissionPendingQuota
		commissionSettledQuota = ext.CommissionSettledQuota
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"commission_total_quota":           commissionTotalQuota,
			"commission_pending_quota":         commissionPendingQuota,
			"commission_settled_quota":         commissionSettledQuota,
			"profit_total_quota":               profitTotalQuota,
			"customer_total_consumption_quota": customerTotalConsumptionQuota,
			"commission_total_usd":             common.QuotaToUSD(commissionTotalQuota),
			"commission_pending_usd":           common.QuotaToUSD(commissionPendingQuota),
			"profit_total_usd":                 common.QuotaToUSD(profitTotalQuota),
			"customer_total_consumption_usd":   common.QuotaToUSD(customerTotalConsumptionQuota),
			"current_performance_quota":        currentProfitQuota,
			"current_performance_usd":          common.QuotaToUSD(currentProfitQuota),
			"current_commission_quota":         currentCommissionQuota,
			"current_commission_usd":           common.QuotaToUSD(currentCommissionQuota),
		},
	})
}

// GetMyCommissionResetPeriodStats GET /api/user/employee/commission/monthly
func GetMyCommissionResetPeriodStats(c *gin.Context) {
	userId := c.GetInt("id")
	if !model.IsEmployee(userId) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "permission denied"})
		return
	}
	page, pageSize := normalizePage(c)
	timeRange, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	items, total, err := model.GetCommissionResetPeriodStats(model.CommissionResetPeriodStatFilter{
		EmployeeUserId: userId,
		StartTime:      timeRange.StartTime,
		EndTime:        timeRange.EndTime,
		Page:           page,
		PageSize:       pageSize,
	})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":     items,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// GetMyCommissionCalendarStats GET /api/user/employee/commission/calendar
func GetMyCommissionCalendarStats(c *gin.Context) {
	userId := c.GetInt("id")
	if !model.IsEmployee(userId) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "permission denied"})
		return
	}
	timeRange, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if timeRange.StartTime == 0 || timeRange.EndTime == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "start_time and end_time are required"})
		return
	}
	stats, err := model.GetCommissionCalendarStats(timeRange.StartTime, timeRange.EndTime, userId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": stats})
}

// AdminAssignCustomerToEmployee POST /api/admin/employee/:id/assign-customer
func AdminAssignCustomerToEmployee(c *gin.Context) {
	employeeId, _ := strconv.Atoi(c.Param("id"))
	if employeeId <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid employee id"})
		return
	}

	var req struct {
		UserId int `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	emp, err := model.GetEmployeeById(employeeId)
	if err != nil || emp == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "employee not found"})
		return
	}
	if emp.Status != 1 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "employee is disabled"})
		return
	}
	if err := validateCustomerBinding(emp.UserId, req.UserId); err != nil {
		if errors.Is(err, errMutualInvitation) {
			logBlockedMutualInvitation("admin_assign_customer", emp.UserId, req.UserId, c.GetInt("id"))
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	if err := model.UpdateUserInviterId(req.UserId, emp.UserId); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	// Keep customer_profiles in sync so customer_count is accurate and the
	// search picker shows the correct "already assigned" state.
	_ = model.UpsertCustomerProfileEmployee(req.UserId, emp.UserId)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AdminListEmployeeCustomers GET /api/admin/employee/:id/customers
func AdminListEmployeeCustomers(c *gin.Context) {
	employeeId, err := strconv.Atoi(c.Param("id"))
	if err != nil || employeeId <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid employee id"})
		return
	}

	emp, err := model.GetEmployeeById(employeeId)
	if err != nil || emp == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "employee not found"})
		return
	}

	page, pageSize := normalizePage(c)
	customerUserId, _ := strconv.Atoi(c.Query("customer_user_id"))
	status, _ := strconv.Atoi(c.Query("status"))
	keyword := strings.TrimSpace(c.Query("keyword"))
	customers, total, err := model.GetInvitedCustomersByEmployeeWithFilter(model.InvitedCustomerFilter{
		EmployeeUserId: emp.UserId,
		CustomerUserId: customerUserId,
		Keyword:        keyword,
		Status:         status,
		Page:           page,
		PageSize:       pageSize,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}

	items, err := buildInvitedCustomersWithUser(emp.UserId, customers)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccess(c, gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// maskUserId masks a user ID as #XXXX.
// AdminUnassignCustomerFromEmployee DELETE /api/admin/employee/:id/customer/:user_id
func AdminUnassignCustomerFromEmployee(c *gin.Context) {
	employeeId, err := strconv.Atoi(c.Param("id"))
	if err != nil || employeeId <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid employee id"})
		return
	}
	customerUserId, err := strconv.Atoi(c.Param("user_id"))
	if err != nil || customerUserId <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid customer id"})
		return
	}

	emp, err := model.GetEmployeeById(employeeId)
	if err != nil || emp == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "employee not found"})
		return
	}
	if emp.UserId == customerUserId {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "cannot unassign employee from themselves"})
		return
	}

	customerUser, err := model.GetUserById(customerUserId, false)
	if err != nil || customerUser == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "user not found"})
		return
	}
	if customerUser.Role != common.RoleCommonUser {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "user must be a common user"})
		return
	}

	if err := model.UnassignCustomerFromEmployee(customerUserId, emp.UserId); err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "customer is not assigned to this employee"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func maskUserId(id int) string {
	s := strconv.Itoa(id)
	if len(s) <= 4 {
		return "#" + s
	}
	return "#****" + s[len(s)-4:]
}
