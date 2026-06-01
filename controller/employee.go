package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// ============================================================================
// 请求/响应结构
// ============================================================================

type CreateEmployeeRequest struct {
	UserId         int     `json:"user_id" binding:"required"`
	CommissionRate float64 `json:"commission_rate" binding:"required,min=0,max=1"`
	TargetQuota    int64   `json:"target_quota"`
	Remark         string  `json:"remark"`
}

type UpdateEmployeeRequest struct {
	CommissionRate float64 `json:"commission_rate" binding:"required,min=0,max=1"`
	TargetQuota    int64   `json:"target_quota"`
	Status         int     `json:"status" binding:"required,min=1,max=2"`
	Remark         string  `json:"remark"`
}

type UpsertChannelCostRequest struct {
	ChannelId int     `json:"channel_id" binding:"required"`
	CostRatio float64 `json:"cost_ratio" binding:"required,min=0,max=10"`
	Remark    string  `json:"remark"`
}

// ============================================================================
// 管理员：员工管理
// ============================================================================

// AdminListEmployees GET /api/admin/employee
func AdminListEmployees(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	employees, total, err := model.GetAllEmployees(page, pageSize)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	// 附加用户名信息
	type EmployeeWithUser struct {
		*model.EmployeeProfile
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
	}
	items := make([]EmployeeWithUser, 0, len(employees))
	for _, emp := range employees {
		u, _ := model.GetUserById(emp.UserId, false)
		item := EmployeeWithUser{EmployeeProfile: emp}
		if u != nil {
			item.Username = u.Username
			item.DisplayName = u.DisplayName
			item.Email = u.Email
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

	// 确认用户存在
	user, err := model.GetUserById(req.UserId, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "用户不存在"})
		return
	}

	emp := &model.EmployeeProfile{
		UserId:         req.UserId,
		CommissionRate: req.CommissionRate,
		TargetQuota:    req.TargetQuota,
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
		TargetQuota:    req.TargetQuota,
		Status:         req.Status,
		Remark:         req.Remark,
	}
	if err := model.UpdateEmployee(emp); err != nil {
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
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"profile":   emp,
			"extension": ext,
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
		StartTime:      startTime,
		EndTime:        endTime,
		Page:           page,
		PageSize:       pageSize,
	})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	// 脱敏：隐藏完整 customer_user_id，仅展示末四位哈希
	type SafeLog struct {
		*model.EmployeeCommissionLog
		CustomerUserIdMasked string `json:"customer_user_id_masked"`
		CustomerUserId       int    `json:"customer_user_id,omitempty"` // 覆盖原字段，员工不可见
	}
	safeItems := make([]SafeLog, 0, len(logs))
	for _, l := range logs {
		safeLog := SafeLog{
			EmployeeCommissionLog: l,
			CustomerUserIdMasked:  maskUserId(l.CustomerUserId),
		}
		safeLog.EmployeeCommissionLog.CustomerUserId = 0 // 清空原始字段
		safeItems = append(safeItems, safeLog)
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

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"commission_total_quota":   ext.CommissionTotalQuota,
			"commission_pending_quota": ext.CommissionPendingQuota,
			"commission_settled_quota": ext.CommissionSettledQuota,
			"revenue_total_quota":      ext.RevenueTotalQuota,
			"revenue_customer_count":   ext.RevenueCustomerCount,
			// USD 换算（方便前端展示）
			"commission_total_usd":   common.QuotaToUSD(ext.CommissionTotalQuota),
			"commission_pending_usd": common.QuotaToUSD(ext.CommissionPendingQuota),
			"revenue_total_usd":      common.QuotaToUSD(ext.RevenueTotalQuota),
		},
	})
}

// maskUserId 将用户 ID 脱敏为 #XXXX 格式
func maskUserId(id int) string {
	s := strconv.Itoa(id)
	if len(s) <= 4 {
		return "#" + s
	}
	return "#****" + s[len(s)-4:]
}
