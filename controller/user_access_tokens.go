package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func AdminClearUserAccessTokens(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok || c.GetBool("use_access_token") || strings.HasPrefix(identity.SessionID, model.AccessTokenSessionPrefix) {
		common.ApiErrorStatus(c, http.StatusForbidden, errors.New(i18n.T(c, i18n.MsgUserAccessTokensDenied)))
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorI18n(c, i18n.MsgUserAccessTokensTargetInvalid)
		return
	}
	target, err := service.AuthorizeUserCredentialDeletion(c.Request.Context(), identity.UserID, id)
	if errors.Is(err, service.ErrVerificationForbidden) {
		common.ApiErrorStatus(c, http.StatusForbidden, errors.New(i18n.T(c, i18n.MsgUserAccessTokensDenied)))
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorI18n(c, i18n.MsgUserAccessTokensTargetInvalid)
		return
	}
	if err != nil {
		logger.LogError(c, common.LogText("Unable to authorize access token clearing: %v", err))
		common.ApiErrorI18n(c, i18n.MsgUserAccessTokensFailed)
		return
	}
	authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserAccessTokensDeleteAll, service.AdminUserContext{UserID: id})
	if authorization == nil {
		return
	}
	result, err := service.ClearUserAccessTokens(c.Request.Context(), identity.UserID, id)
	params := map[string]any{"success": err == nil, "verification_method": authorization.Method}
	if err == nil {
		params["revoked_count"] = result.RevokedCount
		params["legacy_revoked"] = result.LegacyRevoked
		if result.RevokedCount > 0 {
			target := model.AuditTarget("user", id, target.Username)
			target.Consistency = "atomic"
			model.RequestAuditChanges(c).Add(target, model.AuditFields{"access_tokens": true}, model.AuditFields{"access_tokens": false})
		}
	}
	recordManageAuditForUser(c, id, target.Username, "user.access_tokens.delete_all", params)
	if err != nil {
		logger.LogError(c, common.LogText("User access token clearing failed: %v", err))
		if errors.Is(err, service.ErrVerificationForbidden) {
			common.ApiErrorStatus(c, http.StatusForbidden, errors.New(i18n.T(c, i18n.MsgUserAccessTokensDenied)))
			return
		}
		common.ApiErrorI18n(c, i18n.MsgUserAccessTokensFailed)
		return
	}
	common.ApiSuccess(c, result)
}
