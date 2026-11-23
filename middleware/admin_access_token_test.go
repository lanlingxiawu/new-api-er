package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminTokenGateRejectsLeaksBeforeHandler(t *testing.T) {
	setupDashboardAuthMiddlewareTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.CasbinRule{}))
	u := createMiddlewarePATUser(t, "admin-gate", "legacy-gate")
	require.NoError(t, model.DB.Model(u).Update("role", common.RoleAdminUser).Error)
	token := &model.UserAccessToken{CreatedAt: common.GetTimestamp(), ExpiresAt: common.GetTimestamp() + 3600}
	require.NoError(t, token.SetScopes([]string{"user:read", "user:write", "api_key:write", "channel:read"}))
	lookup := &accessTokenLookup{token: token, user: &model.UserBase{Id: u.Id, Role: common.RoleAdminUser}}
	check := func(path string, minRole int) bool {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		return enforceAdminAccessToken(c, lookup, minRole, path, "user:read", true)
	}
	require.False(t, check("GET /api/user/search", common.RoleAdminUser))
	for _, a := range []string{authz.ActionUse, authz.ActionUsersList} {
		require.NoError(t, model.DB.Create(&model.CasbinRule{Ptype: "p", V0: authz.UserSubject(u.Id), V1: authz.ResourceAdminAccessToken, V2: a, V3: authz.EffectAllow}).Error)
	}
	require.True(t, check("GET /api/user/search", common.RoleAdminUser))
	require.False(t, check("GET /api/user/", common.RoleAdminUser))
	require.False(t, check("GET /api/user/:id", common.RoleAdminUser))
	require.False(t, check("GET /api/channel/", common.RoleAdminUser))
	require.NoError(t, model.DB.Create(&model.CasbinRule{Ptype: "p", V0: authz.UserSubject(u.Id), V1: authz.ResourceAdminAccessToken, V2: authz.ActionChannelsOwned, V3: authz.EffectAllow}).Error)
	require.True(t, check("GET /api/channel/", common.RoleAdminUser))
	for _, path := range []string{"GET /api/channel/test/:id", "GET /api/channel/update_balance/:id", "GET /api/channel/fetch_models/:id", "POST /api/channel/fetch_models", "GET /api/channel/test", "GET /api/channel/default_base_urls"} {
		require.False(t, check(path, common.RoleAdminUser), path)
	}
	lookup.legacy = true
	require.False(t, check("GET /api/user/search", common.RoleAdminUser))
	lookup.legacy = false
	token.ExpiresAt = 0
	require.False(t, check("GET /api/user/search", common.RoleAdminUser))
	token.ExpiresAt = token.CreatedAt + service.AdminTokenMaxLifetimeSeconds + 1
	require.False(t, check("GET /api/user/search", common.RoleAdminUser))
	token.ExpiresAt = token.CreatedAt + 3600
	require.NoError(t, model.DB.Model(u).Update("role", common.RoleRootUser).Error)
	require.False(t, check("GET /api/user/search", common.RoleAdminUser))
	require.NoError(t, model.DB.Model(u).Update("role", common.RoleAdminUser).Error)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	require.False(t, enforceAdminAccessToken(c, lookup, common.RoleCommonUser, "POST /api/token/", "api_key:write", true))
	require.NoError(t, model.DB.Where("v0 = ? AND v1 = ? AND v2 = ?", authz.UserSubject(u.Id), authz.ResourceAdminAccessToken, authz.ActionUse).Delete(&model.CasbinRule{}).Error)
	require.False(t, check("GET /api/user/search", common.RoleAdminUser))
}
