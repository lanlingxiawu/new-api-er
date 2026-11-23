package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/auditsecurity"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func ArchiveSecurityAlert(c *gin.Context) {
	if _, ok := middleware.GetSessionAuthIdentity(c); !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	var request struct {
		Version  int64 `json:"version"`
		Archived *bool `json:"archived"`
	}
	if common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 8192), &request) != nil || request.Version < 1 || request.Archived == nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	ctx, cancel, db := securityQueryContext(c)
	defer cancel()
	if db == nil {
		return
	}
	alert, err := auditsecurity.ArchiveAlert(ctx, db, c.GetInt("role"), c.GetInt("id"), id, request.Version, *request.Archived)
	if errors.Is(err, model.ErrSecurityConflict) {
		common.ApiErrorI18n(c, "audit_security.conflict")
		return
	}
	if errors.Is(err, auditsecurity.ErrInvalidAlertArchive) {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if err != nil {
		logger.LogError(c, "audit security archive failed")
		common.ApiErrorI18n(c, "audit_security.unavailable")
		return
	}
	recordManageAudit(c, "audit_security.archive", map[string]any{"alert_id": id, "archived": *request.Archived})
	common.ApiSuccess(c, alert)
}

func GetSecurityAlertRelations(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	relation := c.DefaultQuery("relation", "account")
	limit := securityQueryInt(c, "limit", 20)
	start, end, valid := auditCursorTimeRange(c, 31*86400)
	if !valid || limit < 1 || limit > 50 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	ctx, cancel, db := securityQueryContext(c)
	defer cancel()
	if db == nil {
		return
	}
	page, err := auditsecurity.GetAlertRelations(ctx, db, c.GetInt("role"), c.GetInt("id"), id, relation, c.Query("cursor"), limit, int(start), int(end))
	if errors.Is(err, auditsecurity.ErrActivityCursor) {
		common.ApiErrorI18n(c, "audit_security.activity_changed")
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorI18n(c, "audit_security.not_found")
		return
	}
	if err != nil {
		logger.LogError(c, "audit security relations failed")
		common.ApiErrorI18n(c, "audit_security.unavailable")
		return
	}
	common.ApiSuccess(c, page)
}

func GetAuditIPReasons(c *gin.Context) {
	eventID := c.Query("event_id")
	afterID := securityQueryInt64(c, "after_id", 0)
	limit := securityQueryInt(c, "limit", 20)
	if eventID == "" || len(eventID) > 64 || afterID < 0 || limit < 1 || limit > 50 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	ctx, cancel, db := securityQueryContext(c)
	defer cancel()
	if db == nil {
		return
	}
	logs, err := model.AuditReadDB(true)
	if err != nil {
		logger.LogError(c, "audit log storage unavailable")
		common.ApiErrorI18n(c, "audit_security.unavailable")
		return
	}
	page, err := auditsecurity.ListIPRiskReasons(ctx, db, logs, c.GetInt("role"), eventID, afterID, limit)
	if errors.Is(err, auditsecurity.ErrRiskCursor) {
		common.ApiErrorI18n(c, "audit_security.activity_changed")
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorI18n(c, "audit_security.not_found")
		return
	}
	if err != nil {
		logger.LogError(c, "audit IP reasons failed")
		common.ApiErrorI18n(c, "audit_security.unavailable")
		return
	}
	common.ApiSuccess(c, page)
}

func GetSecurityAssignees(c *gin.Context) {
	id, err := strconv.ParseInt(c.Query("alert_id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	ctx, cancel, db := securityQueryContext(c)
	defer cancel()
	if db == nil {
		return
	}
	users, err := auditsecurity.ListAlertAssignees(ctx, db, c.GetInt("role"), id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorI18n(c, "audit_security.not_found")
		return
	}
	if err != nil {
		logger.LogError(c, fmt.Sprintf("audit security assignees failed: %v", err))
		common.ApiErrorI18n(c, "audit_security.unavailable")
		return
	}
	common.ApiSuccess(c, users)
}

func ExportSecurityAlerts(c *gin.Context) {
	if _, ok := middleware.GetSessionAuthIdentity(c); !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	filter, ok := securityAlertFilter(c)
	if !ok || filter.View == "activity" || filter.View == "recent_hits" {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	ctx, cancel, db := securityQueryContext(c)
	defer cancel()
	if db == nil {
		return
	}
	result, err := auditsecurity.ExportAlerts(ctx, db, c.GetInt("role"), filter, filter.Limit)
	if err != nil {
		logger.LogError(c, "audit security export failed")
		common.ApiErrorI18n(c, "audit_security.unavailable")
		return
	}
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=audit-security-%d.json", result.ExportedAt))
	recordManageAudit(c, "audit_security.export", map[string]any{"count": len(result.Items)})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

func securityQueryInt(c *gin.Context, key string, fallback int) int {
	return int(securityQueryInt64(c, key, int64(fallback)))
}

func securityQueryInt64(c *gin.Context, key string, fallback int64) int64 {
	if c.Query(key) == "" {
		return fallback
	}
	value, err := strconv.ParseInt(c.Query(key), 10, 64)
	if err != nil {
		return -1
	}
	return value
}
