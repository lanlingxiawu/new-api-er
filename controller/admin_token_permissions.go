package controller

import (
	"errors"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

func writeCommittedPermissionError(c *gin.Context, err error) {
	logger.LogError(c, "permissions saved but post-commit processing failed: "+err.Error())
	common.ApiErrorStatus(c, 200, errors.New(i18n.T(c, i18n.MsgAdminTokenPermissionsCommitted)), gin.H{"code": "ADMIN_PERMISSIONS_COMMITTED", "committed": true})
}

func validateAdminTokenPermissionEditor(c *gin.Context, permissions map[string]map[string]bool) bool {
	if c.GetInt("role") != common.RoleRootUser || c.GetBool("use_access_token") {
		common.ApiErrorStatus(c, 403, common.NewMessage("Unauthorized, insufficient privileges"))
		return false
	}
	if _, ok := permissions[authz.ResourceAdminAccessToken]; !ok {
		return true
	}
	if _, ok := middleware.GetSessionAuthIdentity(c); !ok {
		common.ApiErrorStatus(c, 403, common.NewMessage("a dashboard login session is required"))
		return false
	}
	for action := range permissions[authz.ResourceAdminAccessToken] {
		if !slices.Contains(authz.AdminAccessTokenPermissions(), authz.Permission{Resource: authz.ResourceAdminAccessToken, Action: action}) {
			common.ApiErrorT(c, "Invalid parameters")
			return false
		}
	}
	return true
}
