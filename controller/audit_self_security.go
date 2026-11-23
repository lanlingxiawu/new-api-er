package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/auditsecurity"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func GetSelfSecurityAlerts(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	start, end, valid := auditCursorTimeRange(c, 86400)
	if !valid {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	f := auditsecurity.SelfSecurityFilter{UserID: identity.UserID, Start: start, End: end, Cursor: c.Query("cursor")}
	ctx, cancel, db := securityQueryContext(c)
	defer cancel()
	if db == nil {
		return
	}
	page, err := auditsecurity.ListSelfSecurityAlerts(ctx, db, f)
	selfSecurityResponse(c, page, err)
}

func GetSelfSecurityAlert(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	ctx, cancel, db := securityQueryContext(c)
	defer cancel()
	if db == nil {
		return
	}
	detail, err := auditsecurity.GetSelfSecurityAlert(ctx, db, identity.UserID, id)
	selfSecurityResponse(c, detail, err)
}

func selfSecurityResponse(c *gin.Context, data any, err error) {
	switch {
	case err == nil:
		common.ApiSuccess(c, data)
	case errors.Is(err, auditsecurity.ErrSelfSecurityParams):
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ApiErrorI18n(c, "audit_security.not_found")
	default:
		logger.LogError(c, common.LogText("self security query failed: %v", err))
		common.ApiErrorI18n(c, "audit_security.unavailable")
	}
}
