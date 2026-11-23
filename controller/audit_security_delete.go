package controller

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/auditsecurity"
	"github.com/gin-gonic/gin"
)

func DeleteSecurityAlert(c *gin.Context) {
	if _, ok := middleware.GetSessionAuthIdentity(c); !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	var request struct {
		Version int64 `json:"version"`
	}
	if err != nil || id <= 0 || common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 1024), &request) != nil || request.Version < 1 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	ctx, cancel, db := securityQueryContext(c)
	defer cancel()
	if db == nil {
		return
	}
	err = auditsecurity.DeleteAlert(ctx, db, c.GetInt("role"), id, request.Version)
	if errors.Is(err, model.ErrSecurityConflict) {
		common.ApiErrorI18n(c, "audit_security.conflict")
		return
	}
	if err != nil {
		logger.LogError(c, "audit security delete failed: "+err.Error())
		common.ApiErrorI18n(c, "audit_security.unavailable")
		return
	}
	recordManageAudit(c, "audit_security.delete", map[string]any{"alert_id": id})
	common.ApiSuccess(c, auditsecurity.AlertCleanupResult{Queued: 1})
}

func CleanupSecurityAlerts(c *gin.Context) {
	if _, ok := middleware.GetSessionAuthIdentity(c); !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	var request struct {
		ClosedBefore int64 `json:"closed_before"`
	}
	if common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 1024), &request) != nil || request.ClosedBefore <= 0 || request.ClosedBefore > time.Now().Unix() {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	ctx, cancel, db := securityQueryContext(c)
	defer cancel()
	if db == nil {
		return
	}
	result, err := auditsecurity.CleanupClosedAlerts(ctx, db, c.GetInt("role"), request.ClosedBefore)
	if err != nil {
		logger.LogError(c, "audit security cleanup failed: "+err.Error())
		common.ApiErrorI18n(c, "audit_security.unavailable")
		return
	}
	recordManageAudit(c, "audit_security.cleanup", map[string]any{"closed_before": request.ClosedBefore, "count": result.Queued})
	common.ApiSuccess(c, result)
}
