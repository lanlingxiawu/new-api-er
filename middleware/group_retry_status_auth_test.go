package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupRetryStatusRequiresAuthorizedDashboardSession(t *testing.T) {
	setupDashboardAuthMiddlewareTest(t)
	previousMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	require.NoError(t, model.DB.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}))
	require.NoError(t, authz.Init(model.DB))
	const path = "/api/option/group-retry/status"
	router := gin.New()
	router.GET(path, AdminAuth(), RequireSystemSettingsScope(authz.ActionView), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	anonymous := httptest.NewRecorder()
	router.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusUnauthorized, anonymous.Code)
	for _, test := range []struct {
		name    string
		role    int
		granted bool
		status  int
	}{
		{"user", common.RoleCommonUser, false, http.StatusForbidden},
		{"admin_denied", common.RoleAdminUser, false, http.StatusForbidden},
		{"admin_granted", common.RoleAdminUser, true, http.StatusNoContent},
		{"root", common.RoleRootUser, false, http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			legacy := "audit-legacy-" + test.name
			user := createMiddlewarePATUser(t, "audit-"+test.name, legacy)
			require.NoError(t, model.DB.Model(user).Update("role", test.role).Error)
			if test.granted {
				require.NoError(t, authz.SetUserPermissions(user.Id, authz.PermissionsMap{
					authz.SystemSettingsResource("system-tuning.group-retry-times"): {authz.ActionView: true},
				}))
			}
			now := time.Now().Unix()
			session := &model.UserSession{
				SID: common.GetUUID(), UserID: user.Id, Version: 1, UserAuthVersion: user.AuthVersion,
				Status: model.UserSessionStatusActive, RefreshHash: common.GetUUID(),
				LoginMethod: "password", LastActiveAt: now, ExpiresAt: now + 3600,
			}
			require.NoError(t, model.CreateUserSession(session))
			token, _, err := service.IssueAccessToken(service.AuthIdentity{
				UserID: user.Id, SessionID: session.SID, UserAuthVersion: user.AuthVersion, SessionVersion: 1,
			})
			require.NoError(t, err)
			url := path + "?scope=system-tuning.group-retry-times"
			assert.Equal(t, test.status, middlewareBearerRequest(router, url, token).Code)
			if test.role == common.RoleRootUser {
				assert.Equal(t, http.StatusNoContent, middlewareBearerRequest(router, path, token).Code)
			}
			code := "AUTH_SESSION_REQUIRED"
			if test.role < common.RoleAdminUser {
				code = "AUTH_INSUFFICIENT_PRIVILEGE"
			}
			for _, scopes := range [][]string{{"option:read"}, {"profile:read"}} {
				pat, _ := createMiddlewareScopedToken(t, user.Id, 0, scopes...)
				response := middlewareBearerRequest(router, url, pat)
				assert.Equal(t, http.StatusForbidden, response.Code)
				assert.Contains(t, response.Body.String(), code)
			}
			response := middlewareBearerRequest(router, url, legacy)
			assert.Equal(t, http.StatusForbidden, response.Code)
			assert.Contains(t, response.Body.String(), code)
		})
	}
}
