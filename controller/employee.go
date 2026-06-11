package controller

import (
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
	CostRatio float64 `json:"cost_ratio" binding:"gte=0"`
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

	employeeFilter := model.EmployeeFilter{
		UserId:    userId,
		Keyword:   keyword,
		Status:    status,
		SortBy:    sortBy,
		SortOrder: sortOrder,
	}
	employees, total, err := model.GetAllEmployees(page, pageSize, employeeFilter)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	// Attach user info and current performance metrics.
	type EmployeeWithUser struct {
		*model.EmployeeProfile
		Username              string  `json:"username"`
		DisplayName           string  `json:"display_name"`
		Email                 string  `json:"email"`
		CustomerCount         int     `json:"customer_count"`
		TotalConsumptionQuota int64   `json:"total_consumption_quota"`
		TotalConsumptionUsd   float64 `json:"total_consumption_usd"`
		TotalCostQuota        int64   `json:"total_cost_quota"`
		TotalCostUsd          float64 `json:"total_cost_usd"`
		TotalProfitQuota      int64   `json:"total_profit_quota"`
		TotalProfitUsd        float64 `json:"total_profit_usd"`
		TotalCommissionQuota  int64   `json:"total_commission_quota"`
		TotalCommissionUsd    float64 `json:"total_commission_usd"`
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
		userMap                     map[int]*model.User
		extByUserId                 map[int]*model.UserExtension
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
		userMap, err = model.GetUsersByIds(employeeUserIds)
		return err
	})
	eg.Go(func() error {
		var err error
		extByUserId, err = model.GetUserExtensionsByUserIds(employeeUserIds)
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

		// "本期"业绩/提成 = UserExtension 累计值 - 上次重置基准（默认 0），clamp 到 >=0。
		var periodProfitQuota, periodCommissionQuota int64
		if ext, ok := extByUserId[emp.UserId]; ok {
			periodProfitQuota = ext.ProfitTotalQuota
			periodCommissionQuota = ext.CommissionTotalQuota
			if lvl, ok2 := tierLevelsByUserId[emp.UserId]; ok2 {
				periodProfitQuota -= lvl.BaselineProfitQuota
				periodCommissionQuota -= lvl.BaselineCommissionQuota
			}
			if periodProfitQuota < 0 {
				periodProfitQuota = 0
			}
			if periodCommissionQuota < 0 {
				periodCommissionQuota = 0
			}
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
			CurrentProfitQuota:     periodProfitQuota,
			CurrentProfitUsd:       common.QuotaToUSD(periodProfitQuota),
			CurrentCommissionQuota: periodCommissionQuota,
			CurrentCommissionUsd:   common.QuotaToUSD(periodCommissionQuota),
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
			currentLevel, _ := model.GetOrCreateTierLevel(existingEmp.UserId)
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

// AdminListCommissionMonthlyStats GET /api/admin/employee/commission/monthly
func AdminListCommissionMonthlyStats(c *gin.Context) {
	page, pageSize := normalizePage(c)
	employeeUserId, _ := strconv.Atoi(c.Query("employee_user_id"))
	periodStartAt, err := parseOptionalInt64Query(c, "period_start_at")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	timeRange, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	items, total, err := model.GetCommissionMonthlyStats(model.CommissionMonthlyStatFilter{
		EmployeeUserId: employeeUserId,
		PeriodStartAt:  periodStartAt,
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

// AdminCommissionOverview GET /api/admin/employee/overview
func AdminCommissionOverview(c *gin.Context) {
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

	// Build one shared aggregate-only query plan for channel and employee overview queries.
	queryPlan, err := model.ResolveBusinessStatsDailyOnlyQueryPlan(timeRange.StartTime, timeRange.EndTime)
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

	g, _ := errgroup.WithContext(c.Request.Context())
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
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
	}
	empItems := make([]EmployeeStatWithUser, 0, len(byEmployee))
	if len(byEmployee) > 0 {
		empIds := make([]int, 0, len(byEmployee))
		for _, s := range byEmployee {
			empIds = append(empIds, s.EmployeeUserId)
		}
		userMap, _ := model.GetUsersByIdsUnscoped(empIds)
		for _, s := range byEmployee {
			item := EmployeeStatWithUser{CommissionEmployeeStat: s}
			if u := userMap[s.EmployeeUserId]; u != nil {
				item.Username = u.Username
				item.DisplayName = u.DisplayName
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
			"needs_backfill":                model.NeedsBusinessStatsBackfill(),
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
	}
	var profitTotalQuota, commissionTotalQuota int64
	if ext != nil {
		profitTotalQuota = ext.ProfitTotalQuota
		commissionTotalQuota = ext.CommissionTotalQuota
	}
	var baselineProfitQuota, baselineCommissionQuota, baselineResetAt int64
	if tierLevel != nil {
		baselineProfitQuota = tierLevel.BaselineProfitQuota
		baselineCommissionQuota = tierLevel.BaselineCommissionQuota
		baselineResetAt = tierLevel.BaselineResetAt
	}
	currentProfitQuota := profitTotalQuota - baselineProfitQuota
	if currentProfitQuota < 0 {
		currentProfitQuota = 0
	}
	currentCommissionQuota := commissionTotalQuota - baselineCommissionQuota
	if currentCommissionQuota < 0 {
		currentCommissionQuota = 0
	}
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
	ext, err := model.GetUserExtension(userId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	tierLevel, _ := model.GetTierLevelByUserId(userId)
	var baselineProfitQuota, baselineCommissionQuota, baselineResetAt int64
	if tierLevel != nil {
		baselineProfitQuota = tierLevel.BaselineProfitQuota
		baselineCommissionQuota = tierLevel.BaselineCommissionQuota
		baselineResetAt = tierLevel.BaselineResetAt
	}
	currentProfitQuota := ext.ProfitTotalQuota - baselineProfitQuota
	if currentProfitQuota < 0 {
		currentProfitQuota = 0
	}
	currentCommissionQuota := ext.CommissionTotalQuota - baselineCommissionQuota
	if currentCommissionQuota < 0 {
		currentCommissionQuota = 0
	}
	customerConsumptionByUserId, err := model.GetCustomerUsedQuotaTotalsByEmployees([]int{userId})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	customerTotalConsumptionQuota := customerConsumptionByUserId[userId]
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"commission_total_quota":           ext.CommissionTotalQuota,
			"commission_pending_quota":         ext.CommissionPendingQuota,
			"commission_settled_quota":         ext.CommissionSettledQuota,
			"profit_total_quota":               ext.ProfitTotalQuota,
			"revenue_customer_count":           ext.RevenueCustomerCount,
			"customer_total_consumption_quota": customerTotalConsumptionQuota,
			"commission_total_usd":             common.QuotaToUSD(ext.CommissionTotalQuota),
			"commission_pending_usd":           common.QuotaToUSD(ext.CommissionPendingQuota),
			"profit_total_usd":                 common.QuotaToUSD(ext.ProfitTotalQuota),
			"customer_total_consumption_usd":   common.QuotaToUSD(customerTotalConsumptionQuota),
			"baseline_reset_at":                baselineResetAt,
			"baseline_profit_quota":            baselineProfitQuota,
			"baseline_profit_usd":              common.QuotaToUSD(baselineProfitQuota),
			"baseline_commission_quota":        baselineCommissionQuota,
			"baseline_commission_usd":          common.QuotaToUSD(baselineCommissionQuota),
			"current_performance_quota":        currentProfitQuota,
			"current_performance_usd":          common.QuotaToUSD(currentProfitQuota),
			"current_commission_quota":         currentCommissionQuota,
			"current_commission_usd":           common.QuotaToUSD(currentCommissionQuota),
		},
	})
}

// GetMyCommissionMonthlyStats GET /api/user/employee/commission/monthly
func GetMyCommissionMonthlyStats(c *gin.Context) {
	userId := c.GetInt("id")
	if !model.IsEmployee(userId) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "permission denied"})
		return
	}
	page, pageSize := normalizePage(c)
	periodStartAt, err := parseOptionalInt64Query(c, "period_start_at")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	timeRange, err := parseUnixTimeRangeQuery(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	items, total, err := model.GetCommissionMonthlyStats(model.CommissionMonthlyStatFilter{
		EmployeeUserId: userId,
		PeriodStartAt:  periodStartAt,
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
