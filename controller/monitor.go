package controller

// monitor.go — Admin API handlers for the task scheduler and monitoring system.
//
// Routes (all under /api/admin/monitor, admin-only):
//   GET  /scheduler/status          — list all task configs with run history
//   GET  /scheduler/locks           — list active distributed locks
//   PUT  /scheduler/config/:task    — update interval/enabled/timeout for a task
//   POST /scheduler/trigger/:task   — immediately run a task on this node
//
// User-facing monitoring (optional, guarded by UserAuth):
//   GET  /group/status              — current group statuses
//   GET  /model/status              — current model statuses

import (
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// ─── admin: scheduler ────────────────────────────────────────────────────────

// GetSchedulerStatus returns all scheduler_configs with last-run info.
func GetSchedulerStatus(c *gin.Context) {
	configs, err := service.GetTaskSchedulerStatus()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": configs})
}

// GetSchedulerLocks returns all currently active distributed locks.
func GetSchedulerLocks(c *gin.Context) {
	locks, err := model.GetActiveLocks()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": locks})
}

// UpdateSchedulerConfig updates enabled/interval_seconds/timeout_seconds for a task.
func UpdateSchedulerConfig(c *gin.Context) {
	taskName := c.Param("task")

	type req struct {
		Enabled         *int `json:"enabled"`
		IntervalSeconds *int `json:"interval_seconds"`
		TimeoutSeconds  *int `json:"timeout_seconds"`
	}
	var body req
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	updates := map[string]interface{}{}
	if body.Enabled != nil {
		if *body.Enabled != 0 && *body.Enabled != 1 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "enabled must be 0 or 1"})
			return
		}
		updates["enabled"] = *body.Enabled
	}
	if body.IntervalSeconds != nil {
		if *body.IntervalSeconds < 10 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "interval_seconds must be >= 10"})
			return
		}
		updates["interval_seconds"] = *body.IntervalSeconds
	}
	if body.TimeoutSeconds != nil {
		updates["timeout_seconds"] = *body.TimeoutSeconds
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "no fields to update"})
		return
	}

	if err := model.UpdateSchedulerConfigFields(taskName, updates); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// TriggerTask immediately executes a named task on this node (ignores next_run_time).
func TriggerTask(c *gin.Context) {
	taskName := c.Param("task")
	if err := service.TriggerTask(taskName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "task triggered"})
}

// ─── user-facing: status ──────────────────────────────────────────────────────

// GetGroupStatusList returns current health status for all groups.
func GetGroupStatusList(c *gin.Context) {
	groups, err := model.GetAllGroupStatuses()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": groups})
}

// GetGroupStatusHistoryHandler returns hourly history for a group.
func GetGroupStatusHistoryHandler(c *gin.Context) {
	groupName := c.Param("group")
	daysStr := c.DefaultQuery("days", "7")
	days, _ := strconv.Atoi(daysStr)
	if days <= 0 || days > 90 {
		days = 7
	}

	startTime := nowTs() - int64(days*24*3600)
	hist, err := model.GetGroupStatusHistory(groupName, startTime)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"group": groupName, "history": hist}})
}

// GetModelStatusList returns current health status for all models.
func GetModelStatusList(c *gin.Context) {
	models, err := model.GetAllModelStatuses()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": models})
}

// GetModelStatusHistoryHandler returns hourly history for a model.
func GetModelStatusHistoryHandler(c *gin.Context) {
	modelName := c.Param("model")
	if modelName == "" {
		modelName = c.Param("id")
	}
	daysStr := c.DefaultQuery("days", "7")
	days, _ := strconv.Atoi(daysStr)
	if days <= 0 || days > 90 {
		days = 7
	}

	startTime := nowTs() - int64(days*24*3600)
	hist, err := model.GetModelStatusHistory(modelName, startTime)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"model": modelName, "history": hist}})
}

// ─── user-facing: alerts ──────────────────────────────────────────────────────

// GetAlerts returns alerts for the authenticated user.
func GetAlerts(c *gin.Context) {
	userID := c.GetInt("id")
	status := c.DefaultQuery("status", "unread")
	limitStr := c.DefaultQuery("limit", "20")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	alerts, err := model.GetUserAlerts(userID, status, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	unread, _ := model.CountUnreadAlerts(userID)
	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"unread_count": unread,
		"data":         alerts,
	})
}

// MarkAlertAsRead marks a single alert as read.
func MarkAlertAsRead(c *gin.Context) {
	userID := c.GetInt("id")
	alertIDStr := c.Param("id")
	alertID, err := strconv.ParseInt(alertIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid alert id"})
		return
	}

	type req struct {
		ActionTaken string `json:"action_taken"`
	}
	var body req
	_ = c.ShouldBindJSON(&body)

	if err := model.MarkAlertRead(alertID, userID, body.ActionTaken); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func nowTs() int64 {
	return time.Now().Unix()
}
