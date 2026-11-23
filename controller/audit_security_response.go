package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/auditsecurity"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func RespondToSecurityAlert(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	var request struct {
		ResponseGroupID int64  `json:"response_group_id"`
		Version         int64  `json:"version"`
		Action          string `json:"action"`
		RequestID       string `json:"request_id"`
	}
	if err != nil || id <= 0 || common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 4096), &request) != nil || request.Version < 1 || (request.Action != "disable_account" && request.Action != "stop_automatic_response") || len(request.RequestID) < 8 || len(request.RequestID) > 64 {
		rejectSecurityAccountResponse(c, i18n.MsgInvalidParams)
		return
	}
	if request.Action == "stop_automatic_response" && c.GetInt("role") != common.RoleRootUser {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	ctx, cancel, db := securityQueryContext(c)
	defer cancel()
	if db == nil {
		return
	}
	detail, err := auditsecurity.GetAlert(ctx, db, c.GetInt("role"), id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		rejectSecurityAccountResponse(c, "audit_security.not_found")
		return
	}
	if err != nil {
		logger.LogError(c, "security response target lookup failed")
		common.ApiErrorI18n(c, "audit_security.unavailable")
		return
	}
	if request.Action == "stop_automatic_response" {
		if request.ResponseGroupID <= 0 {
			rejectSecurityAccountResponse(c, i18n.MsgInvalidParams)
			return
		}
		proofContext := service.AuditSecurityStopContext{AlertID: id, Version: request.Version, ResponseGroupID: request.ResponseGroupID, Action: request.Action}
		if requireAdminUserProof(c, service.VerificationScopeAuditSecurityResponseStop, proofContext) == nil {
			return
		}
		response, err := model.StopSecurityResponseGroup(ctx, db, identity, id, request.Version, request.ResponseGroupID, request.RequestID)
		if errors.Is(err, model.ErrSecurityConflict) || errors.Is(err, gorm.ErrRecordNotFound) {
			rejectSecurityAccountResponse(c, "audit_security.response_conflict")
			return
		}
		if err != nil {
			logger.LogError(c, "security response stop failed")
			common.ApiErrorI18n(c, "audit_security.unavailable")
			return
		}
		common.ApiSuccess(c, response)
		return
	}
	if detail.Alert.ActorID <= 0 {
		rejectSecurityAccountResponse(c, "audit_security.response_invalid_target")
		return
	}
	if requireAdminUserProof(c, service.VerificationScopeAdminUserManage, service.AdminUserManageContext{UserID: detail.Alert.ActorID, Action: "disable"}) == nil {
		return
	}
	response, err := auditsecurity.CreateManualAccountResponse(ctx, db, c.GetInt("role"), identity, id, request.Version, request.RequestID)
	if errors.Is(err, model.ErrSecurityConflict) {
		rejectSecurityAccountResponse(c, "audit_security.response_conflict")
		return
	}
	if err != nil {
		logger.LogError(c, "security account response failed")
		common.ApiErrorI18n(c, "audit_security.unavailable")
		return
	}
	recordManageAudit(c, "audit_security.disable_account", map[string]any{"alert_id": id, "target_user_id": response.TargetUserID, "outcome": response.Outcome})
	common.ApiSuccess(c, response)
}

// Only preflight and transactional conflicts prove that this request did not
// create an action. Unavailable/ambiguous results must never receive this code.
func rejectSecurityAccountResponse(c *gin.Context, key string) {
	common.ApiErrorStatus(c, http.StatusOK, errors.New(common.TranslateMessage(c, key)), gin.H{"code": "AUDIT_SECURITY_RESPONSE_REJECTED"})
}
