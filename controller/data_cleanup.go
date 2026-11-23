package controller

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func allowDataCleanup(c *gin.Context, kind string, edit bool) bool {
	if _, err := model.DataCleanupTable(kind); err != nil {
		common.ApiErrorI18n(c, "cleanup.invalid_date")
		return false
	}
	allowed := c.GetInt("role") == common.RoleRootUser
	if kind == model.CleanupErrorLogs {
		permission := authz.SystemSettingsView("operations.logs")
		if edit {
			permission = authz.SystemSettingsEdit("operations.logs")
		}
		allowed = authz.Can(c.GetInt("id"), c.GetInt("role"), permission)
	}
	if !allowed {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": common.TranslateMessage(c, "common.forbidden")})
	}
	return allowed
}

func dataCleanupError(c *gin.Context, err error) {
	logger.LogError(c, "data cleanup: "+err.Error())
	for _, known := range []error{service.ErrCleanupDate, service.ErrCleanupLogDate, service.ErrCleanupScope, service.ErrCleanupDisabled, service.ErrCleanupPreview, service.ErrCleanupBusy, service.ErrCleanupUnsupported, service.ErrCleanupIndex, service.ErrCleanupNodes} {
		if errors.Is(err, known) {
			common.ApiErrorI18n(c, known.Error())
			return
		}
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorI18n(c, "cleanup.preview_expired")
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": common.TranslateMessage(c, "cleanup.failed")})
}

func PreviewDataCleanup(c *gin.Context) {
	var req struct {
		Kind   string `json:"kind"`
		Before int64  `json:"before"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorI18n(c, "cleanup.invalid_date")
		return
	}
	if !allowDataCleanup(c, req.Kind, true) {
		return
	}
	run, err := service.PreviewDataCleanup(c.Request.Context(), c.GetInt("id"), req.Kind, req.Before)
	if err != nil {
		dataCleanupError(c, err)
		return
	}
	common.ApiSuccess(c, run)
}

func CreateDataCleanup(c *gin.Context) {
	var req struct {
		PreviewID string `json:"preview_id"`
		Resume    bool   `json:"resume"`
		Kind      string `json:"kind"`
		Before    int64  `json:"before"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorI18n(c, "cleanup.preview_expired")
		return
	}
	if req.Kind != "" || req.Before != 0 {
		if req.Kind != model.CleanupErrorLogs || req.PreviewID != "" || req.Resume {
			common.ApiErrorI18n(c, "cleanup.invalid_date")
			return
		}
		if !allowDataCleanup(c, req.Kind, true) {
			return
		}
		run, err := service.StartErrorLogCleanup(c.Request.Context(), c.GetInt("id"), req.Before)
		if err != nil {
			dataCleanupError(c, err)
			return
		}
		common.ApiSuccess(c, run)
		return
	}
	if len(req.PreviewID) != 32 {
		common.ApiErrorI18n(c, "cleanup.preview_expired")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	var preview model.DataCleanupRun
	if err := model.DB.WithContext(ctx).Where("id = ? AND user_id = ?", req.PreviewID, c.GetInt("id")).First(&preview).Error; err != nil {
		dataCleanupError(c, err)
		return
	}
	if !allowDataCleanup(c, preview.Kind, true) {
		return
	}
	var run *model.DataCleanupRun
	var err error
	if req.Resume {
		run, err = service.ResumeDataCleanup(ctx, c.GetInt("id"), req.PreviewID)
	} else {
		run, err = service.StartDataCleanup(ctx, c.GetInt("id"), req.PreviewID)
	}
	if err != nil {
		dataCleanupError(c, err)
		return
	}
	common.ApiSuccess(c, run)
}

func GetDataCleanup(c *gin.Context) {
	kind := c.Query("kind")
	if !allowDataCleanup(c, kind, false) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	var run model.DataCleanupRun
	query := model.DB.WithContext(ctx).Where("kind = ?", kind)
	if id := c.Query("id"); id != "" {
		query = query.Where("id = ?", id)
	} else {
		query = query.Where("task_id IS NOT NULL").Order("task_sequence DESC")
	}
	if err := query.First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiSuccess(c, nil)
			return
		}
		dataCleanupError(c, err)
		return
	}
	var task model.SystemTask
	if run.Kind == model.CleanupErrorLogs {
		progress, taskType, err := readErrorLogCleanupProgress(ctx, &run)
		if err != nil {
			dataCleanupError(c, err)
			return
		}
		run.CanResume = taskType == model.SystemTaskTypeErrorLogCleanup && run.Status == "failed" && run.UserID == c.GetInt("id") && run.ErrorKey != "cleanup.index_missing"
		common.ApiSuccess(c, struct {
			*model.DataCleanupRun
			Progress *service.ErrorLogCleanupProgress `json:"progress,omitempty"`
		}{&run, progress})
		return
	}
	if run.TaskID != nil && (run.Status == "pending" || run.Status == "running" || run.Status == "failed") {
		if err := model.DB.WithContext(ctx).Select("status", "type").Where("task_id = ?", *run.TaskID).First(&task).Error; err != nil {
			dataCleanupError(c, err)
			return
		}
		run.Status = string(task.Status)
		if task.Status == model.SystemTaskStatusFailed && run.ErrorKey == "" {
			run.ErrorKey = "cleanup.failed"
		}
	}
	run.CanResume = run.Kind == model.CleanupErrorLogs && task.Type == model.SystemTaskTypeErrorLogCleanup && run.Status == "failed" && run.UserID == c.GetInt("id") && run.ErrorKey != "cleanup.index_missing"
	common.ApiSuccess(c, run)
}
