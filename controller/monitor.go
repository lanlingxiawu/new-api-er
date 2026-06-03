package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

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

// TriggerTask immediately executes a named task on this node.
func TriggerTask(c *gin.Context) {
	taskName := c.Param("task")
	if err := service.TriggerTask(taskName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "task triggered"})
}

// GetGroupStatusList returns current availability status for all groups.
func GetGroupStatusList(c *gin.Context) {
	groups, err := model.GetAllGroupStatuses()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": groups})
}

// GetModelStatusList returns current aggregated availability status for all models.
func GetModelStatusList(c *gin.Context) {
	models, err := model.GetAllModelStatuses()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": models})
}
