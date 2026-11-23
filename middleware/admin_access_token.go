package middleware

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
)

import "github.com/gin-gonic/gin"

func enforceAdminAccessToken(c *gin.Context, lookup *accessTokenLookup, minRole int, route, scope string, abort bool) bool {
	management := minRole >= common.RoleAdminUser || service.IsManagementScope(scope)
	if !management {
		if !service.AdminTokenPersonalScopeDenied(scope) {
			return true
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 500*time.Millisecond)
		defer cancel()
		var user model.User
		if err := model.DB.WithContext(ctx).Select("id", "role").First(&user, lookup.user.Id).Error; err != nil {
			if abort {
				logger.LogError(c, "token credential escalation check failed: "+err.Error())
				abortWithWebMessage(c, http.StatusInternalServerError, "AUTH_INTERNAL_ERROR", common.NewMessage("Please try again later."))
			}
			return false
		}
		if user.Role < common.RoleAdminUser {
			return true
		}
	}
	reason := "admin_access_token_module_denied"
	module := service.AdminTokenRouteModule(route)
	switch {
	case lookup.legacy:
		reason = "legacy_management_token_denied"
	case lookup.user.Role == common.RoleRootUser:
		reason = "root_management_token_denied"
	case !management || module == "":
	case !service.AdminTokenLifetimeValid(lookup.token):
		reason = "admin_access_token_lifetime_denied"
	default:
		user, grants, err := service.ReadAdminTokenAuthorization(c.Request.Context(), lookup.user.Id, module, authz.ChannelRead, authz.ChannelWrite, authz.ChannelOperate, authz.ChannelSensitiveWrite, authz.AdminMenuChannelsView, authz.AdminMenuUsersView, authz.AdminMenuEmployeesView)
		if err == nil {
			lookup.user.Role = user.Role
			lookup.user.Status = user.Status
			lookup.user.AuthVersion = user.AuthVersion
			c.Set("admin_token_permissions", grants)
			c.Set("admin_token_module", module)
			return true
		}
		if !errors.Is(err, service.ErrAdminTokenDenied) {
			if abort {
				logger.LogError(c, "administrator token authorization lookup failed: "+err.Error())
				abortWithWebMessage(c, http.StatusInternalServerError, "AUTH_INTERNAL_ERROR", common.NewMessage("Please try again later."))
			}
			return false
		}
		reason = "admin_access_token_disabled"
	}
	if abort {
		setAccessTokenAuditParam(c, "failure_reason", reason)
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage(service.ErrAdminTokenDenied.Error()), gin.H{"code": "ADMIN_ACCESS_TOKEN_DISABLED", "message": i18n.T(c, i18n.MsgAdminTokenDenied)})
		c.Abort()
	}
	return false
}

// RequirePATPermission keeps field/action checks at the same boundary as the
// account permission, including sensitive branches inside existing handlers.
func RequirePATPermission(c *gin.Context, p authz.Permission) bool {
	if !c.GetBool("use_access_token") {
		return authz.Can(c.GetInt("id"), c.GetInt("role"), p)
	}
	value, _ := c.Get("admin_token_permissions")
	grants, _ := value.(map[authz.Permission]bool)
	// Menu access is an account requirement, not a second token scope.
	return grants[p] && (p == authz.AdminMenuChannelsView || p == authz.AdminMenuUsersView || p == authz.AdminMenuEmployeesView || service.AccessTokenScopeGranted(c.GetStringSlice("access_token_scopes"), service.AccessTokenScopeOf(p)))
}
