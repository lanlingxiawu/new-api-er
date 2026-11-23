package controller

import (
	"errors"
	"fmt"
	"github.com/QuantumNous/new-api/model"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func AdminClearUserAPIKeys(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok || c.GetBool("use_access_token") || strings.HasPrefix(identity.SessionID, "pat:") {
		common.ApiErrorStatus(c, http.StatusForbidden, errors.New(i18n.T(c, i18n.MsgUserAPIKeysDenied)))
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorI18n(c, i18n.MsgUserAPIKeysTargetInvalid)
		return
	}
	target, err := service.AuthorizeUserCredentialDeletion(c.Request.Context(), identity.UserID, id)
	if errors.Is(err, service.ErrVerificationForbidden) {
		common.ApiErrorStatus(c, http.StatusForbidden, errors.New(i18n.T(c, i18n.MsgUserAPIKeysDenied)))
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorI18n(c, i18n.MsgUserAPIKeysTargetInvalid)
		return
	}
	if err != nil {
		logger.LogError(c, fmt.Sprintf("Unable to check API key deletion permissions: %v", err))
		common.ApiErrorI18n(c, i18n.MsgUserAPIKeysFailed)
		return
	}
	authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserAPIKeysDeleteAll, service.AdminUserContext{UserID: id})
	if authorization == nil {
		return
	}
	result, err := service.ClearUserAPIKeys(c.Request.Context(), identity.UserID, id)
	if result.DeletedCount > 0 {
		model.RequestAuditChanges(c).Add(model.AuditTarget("user", id, target.Username), model.AuditFields{"api_keys": true}, model.AuditFields{"api_keys": false})
	}
	recordManageAuditForUser(c, id, target.Username, "user.api_keys.delete_all", map[string]any{
		"deleted_count": result.DeletedCount, "completed": result.Completed, "success": err == nil,
		"upper_token_id": result.UpperID, "verification_method": authorization.Method,
	})
	if err != nil {
		// Log the failure, never Redis commands or credential-bearing structs.
		logger.LogError(c, fmt.Sprintf("User API key deletion did not complete: %v", err))
		common.ApiErrorStatus(c, http.StatusOK, errors.New(i18n.T(c, i18n.MsgUserAPIKeysIncomplete, map[string]any{"Count": result.DeletedCount})), gin.H{"data": result})
		return
	}
	common.ApiSuccess(c, result)
}
