package controller

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
)

// ============================================================================
// 请求/响应结构
// ============================================================================

type CreateEmployeeRequest struct {
	UserId         int     `json:"user_id" binding:"required"`
	CommissionRate float64 `json:"commission_rate" binding:"required,min=0,max=1"`
	TargetAmount   float64 `json:"target_amount"` // USD 金额，0=不设限
	Remark         string  `json:"remark"`
}

type UpdateEmployeeRequest struct {
	CommissionRate float64 `json:"commission_rate" binding:"required,min=0,max=1"`
	TargetAmount   float64 `json:"target_amount"` // USD 金额，0=不设限
	Status         int     `json:"status" binding:"required,min=1,max=2"`
	Remark         string  `json:"remark"`
}

type UpsertChannelCostRequest struct {
	ChannelId int `json:"channel_id" binding:"required"`
	// cost_ratio = 模型基础价 × 上游倍率 的折扣系数。不设上限（上游倍率可能很高，
	// 需允许 >1 以覆盖真实采购价）；允许为 0（零成本/免费渠道），故用 gte 而非 required。
	CostRatio float64 `json:"cost_ratio" binding:"gte=0"`
	Remark    string  `json:"remark"`
}

// ============================================================================
// 管理员：员工管理
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

	isComputedSort := sortBy == "total_consumption_quota" ||
		sortBy == "customer_count" ||
		sortBy == "total_cost_quota" ||
		sortBy == "total_profit_quota" ||
		sortBy == "total_commission_quota" ||
		sortBy == "current_performance_quota" ||
		sortBy == "current_tier_rate"

	queryPage := page
	queryPageSize := pageSize
	if isComputedSort {
		queryPage = 1
		queryPageSize = 1000000
	}
	employeeFilter := model.EmployeeFilter{
		UserId:    userId,
		Keyword:   keyword,
		Status:    status,
		SortBy:    sortBy,
		SortOrder: sortOrder,
	}
	if isComputedSort {
		employeeFilter.SortBy = ""
		employeeFilter.SortOrder = ""
	}
	employees, total, err := model.GetAllEmployees(queryPage, queryPageSize, employeeFilter)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	// 附加用户名 + 当前业绩（利润）
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
		CurrentProfitQuota    int64   `json:"current_performance_quota"`
		CurrentProfitUsd      float64 `json:"current_performance_usd"`
		CurrentTierId         int64   `json:"current_tier_id"`
		CurrentTierLevel      int     `json:"current_tier_level"`
		CurrentTierRate       float64 `json:"current_tier_rate"`
	}

	employeeUserIds := make([]int, 0, len(employees))
	for _, emp := range employees {
		employeeUserIds = append(employeeUserIds, emp.UserId)
	}

	// 并行拉取当前页所需的 5 类附加数据
	var (
		profitStats                 []*model.CommissionEmployeeStat
		customerConsumptionByUserId map[int]int64
		customerCountsByUserId      map[int]int
		tierLevelsByUserId          map[int]*model.EmployeeTierLevel
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

	items := make([]EmployeeWithUser, 0, len(employees))
	for _, emp := range employees {
		totalConsumptionQuota := customerConsumptionByUserId[emp.UserId]
		var totalCostQuota, totalProfitQuota, totalCommissionQuota int64
		if stat := statsByUserId[emp.UserId]; stat != nil {
			totalCostQuota = stat.TotalCost
			totalProfitQuota = stat.TotalProfit
			totalCommissionQuota = stat.TotalCommission
		}
		item := EmployeeWithUser{
			EmployeeProfile:       emp,
			CustomerCount:         customerCountsByUserId[emp.UserId],
			TotalConsumptionQuota: totalConsumptionQuota,
			TotalConsumptionUsd:   common.QuotaToUSD(totalConsumptionQuota),
			TotalCostQuota:        totalCostQuota,
			TotalCostUsd:          common.QuotaToUSD(totalCostQuota),
			TotalProfitQuota:      totalProfitQuota,
			TotalProfitUsd:        common.QuotaToUSD(totalProfitQuota),
			TotalCommissionQuota:  totalCommissionQuota,
			TotalCommissionUsd:    common.QuotaToUSD(totalCommissionQuota),
			CurrentProfitQuota:    totalProfitQuota,
			CurrentProfitUsd:      common.QuotaToUSD(totalProfitQuota),
		}
		if lvl, ok := tierLevelsByUserId[emp.UserId]; ok && lvl.TierId != 0 {
			item.CurrentTierId = lvl.TierId
			if t, ok2 := tierById[lvl.TierId]; ok2 {
				item.CurrentTierLevel = t.Level
				item.CurrentTierRate = t.Rate
			}
		}
		if u := userMap[emp.UserId]; u != nil {
			item.Username = u.Username
			item.DisplayName = u.DisplayName
			item.Email = u.Email
		}
		items = append(items, item)
	}

	if isComputedSort {
		desc := sortOrder == "desc"
		sort.SliceStable(items, func(i, j int) bool {
			var left, right int64
			switch sortBy {
			case "customer_count":
				left, right = int64(items[i].CustomerCount), int64(items[j].CustomerCount)
			case "total_consumption_quota":
				left, right = items[i].TotalConsumptionQuota, items[j].TotalConsumptionQuota
			case "total_cost_quota":
				left, right = items[i].TotalCostQuota, items[j].TotalCostQuota
			case "total_profit_quota", "current_performance_quota":
				left, right = items[i].TotalProfitQuota, items[j].TotalProfitQuota
			case "total_commission_quota":
				left, right = items[i].TotalCommissionQuota, items[j].TotalCommissionQuota
			case "current_tier_rate":
				if desc {
					return items[i].CurrentTierRate > items[j].CurrentTierRate
				}
				return items[i].CurrentTierRate < items[j].CurrentTierRate
			}
			if desc {
				return left > right
			}
			return left < right
		})
		start := (page - 1) * pageSize
		if start >= len(items) {
			items = items[:0]
		} else {
			end := start + pageSize
			if end > len(items) {
				end = len(items)
			}
			items = items[start:end]
		}
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
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "用户不存在"})
		return
	}
	emp := &model.EmployeeProfile{
		UserId:         req.UserId,
		CommissionRate: req.CommissionRate,
		TargetAmount:   req.TargetAmount,
		Status:         1,
		Remark:         req.Remark,
	}
	if err := model.CreateEmployee(emp); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": emp})
}

// AdminUpdateEmployee PUT /api/admin/employee/:id
func AdminUpdateEmployee(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的 ID"})
		return
	}
	var req UpdateEmployeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	emp := &model.EmployeeProfile{
		Id:             id,
		CommissionRate: req.CommissionRate,
		TargetAmount:   req.TargetAmount,
		Status:         req.Status,
		Remark:         req.Remark,
	}
	if err := model.UpdateEmployee(emp); err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "员工档案不存在"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AdminDeleteEmployee DELETE /api/admin/employee/:id
func AdminDeleteEmployee(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的 ID"})
		return
	}
	if err := model.DisableEmployee(id); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// 管理员：提成日志
// ============================================================================

// AdminListCommissionLogs GET /api/admin/employee/commission
func AdminListCommissionLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	employeeUserId, _ := strconv.Atoi(c.Query("employee_user_id"))
	customerUserId, _ := strconv.Atoi(c.Query("customer_user_id"))
	channelId, _ := strconv.Atoi(c.Query("channel_id"))
	modelName := strings.TrimSpace(c.Query("model_name"))
	startTime, _ := strconv.ParseInt(c.Query("start_time"), 10, 64)
	endTime, _ := strconv.ParseInt(c.Query("end_time"), 10, 64)
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
		StartTime:      startTime,
		EndTime:        endTime,
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
	startTime, _ := strconv.ParseInt(c.Query("start_time"), 10, 64)
	endTime, _ := strconv.ParseInt(c.Query("end_time"), 10, 64)
	items, err := model.GetCommissionSummary(startTime, endTime)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": items})
}

// AdminCommissionOverview GET /api/admin/employee/overview
func AdminCommissionOverview(c *gin.Context) {
	startTime, _ := strconv.ParseInt(c.Query("start_time"), 10, 64)
	endTime, _ := strconv.ParseInt(c.Query("end_time"), 10, 64)

	var (
		costTotals   model.ConsumptionCostTotals
		channelStats []*model.ConsumptionCostChannelStat
		totals       model.CommissionTotals
		byEmployee   []*model.CommissionEmployeeStat
	)

	g, _ := errgroup.WithContext(c.Request.Context())
	g.Go(func() error {
		var err error
		costTotals, err = model.GetConsumptionCostTotals(startTime, endTime)
		return err
	})
	g.Go(func() error {
		var err error
		channelStats, err = model.GetConsumptionCostByChannel(startTime, endTime)
		return err
	})
	g.Go(func() error {
		var err error
		totals, err = model.GetCommissionTotals(startTime, endTime)
		return err
	})
	g.Go(func() error {
		var err error
		byEmployee, err = model.GetCommissionStatsByEmployee(startTime, endTime)
		return err
	})

	if err := g.Wait(); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

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
			CostRatio:        model.GetChannelCostRatio(s.ChannelId),
		}
		if s.TotalRevenue != 0 {
			item.EstGrossMargin = float64(item.EstProfitQuota) / float64(s.TotalRevenue)
		}
		if ch, e := model.CacheGetChannel(s.ChannelId); e == nil && ch != nil {
			item.ChannelName = ch.Name
		}
		channelProfit = append(channelProfit, item)
	}
	sort.Slice(channelProfit, func(i, j int) bool {
		return channelProfit[i].EstProfitQuota > channelProfit[j].EstProfitQuota
	})

	// 批量拉取员工用户信息，避免 N+1 查询
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
		userMap, _ := model.GetUsersByIds(empIds)
		for _, s := range byEmployee {
			item := EmployeeStatWithUser{CommissionEmployeeStat: s}
			if u := userMap[s.EmployeeUserId]; u != nil {
				item.Username = u.Username
				item.DisplayName = u.DisplayName
			}
			empItems = append(empItems, item)
		}
	}

	var profitableChannelCount, lossChannelCount int
	for _, item := range channelProfit {
		if item.EstProfitQuota > 0 {
			profitableChannelCount++
		} else if item.EstProfitQuota < 0 {
			lossChannelCount++
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
				"profitable_channel_count": profitableChannelCount,
				"loss_channel_count":       lossChannelCount,
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
			"by_employee":         empItems,
			"by_channel_platform": channelProfit,
		},
	})
}

// ============================================================================
// 管理员：渠道成本配置
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
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的渠道 ID"})
		return
	}
	if err := model.DeleteChannelCostConfig(channelId); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// 管理员：阶梯提成等级配置
// ============================================================================

type CreateTierRequest struct {
	Level        int     `json:"level" binding:"required,min=1"`
	ThresholdUsd float64 `json:"threshold_usd" binding:"gte=0"`
	Rate         float64 `json:"rate" binding:"required,min=0,max=1"`
}

type UpdateTierRequest struct {
	Level        int     `json:"level" binding:"required,min=1"`
	ThresholdUsd float64 `json:"threshold_usd" binding:"gte=0"`
	Rate         float64 `json:"rate" binding:"required,min=0,max=1"`
}

type SetEmployeeTierRequest struct {
	TierId int64  `json:"tier_id"` // 0 = 清除等级
	Source string `json:"source"`  // manual / custom
	Remark string `json:"remark"`
}

// AdminListTiers GET /api/admin/employee/tiers
func AdminListTiers(c *gin.Context) {
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
	tier := &model.EmployeeCommissionTier{
		Level:        req.Level,
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
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的 ID"})
		return
	}
	var req UpdateTierRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	tier := &model.EmployeeCommissionTier{
		Id:           id,
		Level:        req.Level,
		ThresholdUsd: req.ThresholdUsd,
		Rate:         req.Rate,
	}
	if err := model.UpdateTier(tier); err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "等级不存在"})
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
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的 ID"})
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
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "无效的员工 ID"})
		return
	}
	var req SetEmployeeTierRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	emp, err := model.GetEmployeeById(empId)
	if err != nil || emp == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "员工不存在"})
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

// ============================================================================
// 员工自查接口（仅本人数据）
// ============================================================================

// GetMyEmployeeProfile GET /api/user/employee/profile
func GetMyEmployeeProfile(c *gin.Context) {
	userId := c.GetInt("id")
	emp := model.GetEmployeeByUserId(userId)
	if emp == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "您不是员工"})
		return
	}
	ext, _ := model.GetUserExtension(userId)
	tierLevel, tier := model.GetTierLevelByUserId(userId)
	tierInfo := gin.H{"tier_id": int64(0), "tier_level": 0, "tier_rate": 0.0}
	if tierLevel != nil && tierLevel.TierId != 0 && tier != nil {
		tierInfo = gin.H{
			"tier_id":    tierLevel.TierId,
			"tier_level": tier.Level,
			"tier_rate":  tier.Rate,
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"profile":   emp,
			"extension": ext,
			"tier":      tierInfo,
		},
	})
}

// GetMyCommissionLogs GET /api/user/employee/commission
func GetMyCommissionLogs(c *gin.Context) {
	userId := c.GetInt("id")
	if !model.IsEmployee(userId) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "权限不足"})
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	customerUserId, _ := strconv.Atoi(c.Query("customer_user_id"))
	channelId, _ := strconv.Atoi(c.Query("channel_id"))
	modelName := strings.TrimSpace(c.Query("model_name"))
	startTime, _ := strconv.ParseInt(c.Query("start_time"), 10, 64)
	endTime, _ := strconv.ParseInt(c.Query("end_time"), 10, 64)
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
		StartTime:      startTime,
		EndTime:        endTime,
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
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "权限不足"})
		return
	}
	ext, err := model.GetUserExtension(userId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	totals, err := model.GetCommissionTotalsByEmployee(userId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"commission_total_quota":           ext.CommissionTotalQuota,
			"commission_pending_quota":         ext.CommissionPendingQuota,
			"commission_settled_quota":         ext.CommissionSettledQuota,
			"profit_total_quota":               totals.TotalProfit,
			"revenue_customer_count":           ext.RevenueCustomerCount,
			"customer_total_consumption_quota": totals.TotalRevenue,
			"commission_total_usd":             common.QuotaToUSD(ext.CommissionTotalQuota),
			"commission_pending_usd":           common.QuotaToUSD(ext.CommissionPendingQuota),
			"profit_total_usd":                 common.QuotaToUSD(totals.TotalProfit),
			"customer_total_consumption_usd":   common.QuotaToUSD(totals.TotalRevenue),
		},
	})
}

// AdminAssignCustomerToEmployee POST /api/admin/employee/:id/assign-customer
// 将某个用户的 inviter_id 改为该员工的 user_id，完成客户分配。
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
	if emp.UserId == req.UserId {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "cannot assign employee to themselves"})
		return
	}

	customerUser, err := model.GetUserById(req.UserId, false)
	if err != nil || customerUser == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "user not found"})
		return
	}
	if customerUser.Role != common.RoleCommonUser || customerUser.Status != common.UserStatusEnabled {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "user must be an enabled common user"})
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

// maskUserId 将用户 ID 脱敏为 #XXXX 格式
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
