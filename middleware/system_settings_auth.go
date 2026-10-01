package middleware

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/service/settingsaccess"
	"github.com/gin-gonic/gin"
)

// SystemSettingsScopeContextKey carries the scope this middleware authorized.
// It is set on every request the middleware lets through: the granted scope for
// a scoped caller, "" for a root caller that named no scope. Handlers must
// authorize against this value instead of re-reading the scope themselves.
const SystemSettingsScopeContextKey = "system_settings_scope"

type systemSettingsScopeRequest struct {
	Scope string `json:"scope"`
}

func RequireSystemSettingsScope(action string) func(c *gin.Context) {
	return func(c *gin.Context) {
		scope := strings.TrimSpace(c.Query("scope"))
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			// Handlers behind this middleware decode the body as JSON. Accepting any
			// other media type would let the scope come from a different reading of
			// the same bytes (form / multipart) than the one the handler acts on.
			if c.ContentType() != gin.MIMEJSON {
				common.ApiErrorI18n(c, i18n.MsgInvalidParams)
				c.Abort()
				return
			}
			var request systemSettingsScopeRequest
			if err := common.UnmarshalBodyReusable(c, &request); err != nil {
				common.ApiErrorI18n(c, i18n.MsgInvalidParams)
				c.Abort()
				return
			}
			scope = strings.TrimSpace(request.Scope)
		}

		role := c.GetInt("role")
		if scope == "" {
			if role == common.RoleRootUser {
				c.Set(SystemSettingsScopeContextKey, "")
				c.Next()
				return
			}
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			c.Abort()
			return
		}
		if _, ok := settingsaccess.Resolve(scope); !ok {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			c.Abort()
			return
		}

		permission := authz.Permission{Resource: authz.SystemSettingsResource(scope), Action: action}
		if scope == settingsaccess.ScopeVeridropDetection {
			permission = authz.Permission{Resource: authz.ResourceAdminMenuVeridropDetection, Action: action}
		}
		allowed := authz.Can(c.GetInt("id"), role, permission)
		if !allowed && scope == settingsaccess.ScopeChannelProfitPreview && action == authz.ActionView {
			allowed = authz.Can(c.GetInt("id"), role, authz.ChannelSensitiveWrite)
		}
		if !allowed {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": common.TranslateMessage(c, i18n.MsgAuthInsufficientPrivilege),
			})
			return
		}

		c.Set(SystemSettingsScopeContextKey, scope)
		c.Next()
	}
}
