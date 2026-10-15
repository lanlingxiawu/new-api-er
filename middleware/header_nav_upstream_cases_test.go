package middleware

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func withHeaderNavModules(t *testing.T, raw string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	prev, had := common.OptionMap["HeaderNavModules"]
	common.OptionMap["HeaderNavModules"] = raw
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if had {
			common.OptionMap["HeaderNavModules"] = prev
			return
		}
		delete(common.OptionMap, "HeaderNavModules")
	})
}

func TestParseHeaderNavBool(t *testing.T) {
	cases := []struct {
		name     string
		value    any
		fallback bool
		want     bool
	}{
		{"bool true", true, false, true},
		{"bool false", false, true, false},
		{"string true", "true", false, true},
		{"string 1", "1", false, true},
		{"string false", "false", true, false},
		{"string 0", "0", true, false},
		{"string mixed case", "TrUe", false, true},
		{"string unknown -> fallback", "maybe", true, true},
		{"float 1", float64(1), false, true},
		{"float 0", float64(0), true, false},
		{"float other -> fallback", float64(2), true, true},
		{"int 1", 1, false, true},
		{"int 0", 0, true, false},
		{"int other -> fallback", 5, true, true},
		{"nil -> fallback", nil, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, parseHeaderNavBool(tc.value, tc.fallback))
		})
	}
}

func TestParseHeaderNavAccess(t *testing.T) {
	fb := headerNavAccess{Enabled: true, RequireAuth: false}

	require.Equal(t, headerNavAccess{Enabled: false, RequireAuth: false}, parseHeaderNavAccess(false, fb))
	require.Equal(t, headerNavAccess{Enabled: false, RequireAuth: false}, parseHeaderNavAccess("false", fb))
	require.Equal(t, headerNavAccess{Enabled: false, RequireAuth: false}, parseHeaderNavAccess(float64(0), fb))

	got := parseHeaderNavAccess(map[string]any{"enabled": true, "requireAuth": true}, fb)
	require.Equal(t, headerNavAccess{Enabled: true, RequireAuth: true}, got)

	// unknown type -> fallback
	require.Equal(t, fb, parseHeaderNavAccess(12345, fb))
}

func TestGetHeaderNavAccess_EmptyOptionFallback(t *testing.T) {
	withHeaderNavModules(t, "")
	acc := getHeaderNavAccess("pricing")
	require.True(t, acc.Enabled)
	require.False(t, acc.RequireAuth)
}

func TestGetHeaderNavAccess_InvalidJSONFallback(t *testing.T) {
	withHeaderNavModules(t, "{not-json")
	acc := getHeaderNavAccess("pricing")
	require.True(t, acc.Enabled)
}

func TestGetHeaderNavAccess_ModuleDisabled(t *testing.T) {
	withHeaderNavModules(t, `{"pricing":{"enabled":false}}`)
	acc := getHeaderNavAccess("pricing")
	require.False(t, acc.Enabled)
}

func runHeaderNav(t *testing.T, handler gin.HandlerFunc, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	r.GET("/api/test", handler, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"success": true}) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	if authenticated {
		// 上游 #6329 改为无状态鉴权：authHelper 的 PAT 回退校验 users.access_token。
		// 造一个带 32 位 access_token 的用户，用它作 Bearer 模拟已登录。
		accessToken := strings.ReplaceAll(uniq("hn"), "_", "")
		for len(accessToken) < 32 {
			accessToken += "0"
		}
		accessToken = accessToken[:32]
		mkUser(t, func(u *model.User) { u.SetAccessToken(accessToken) })
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	r.ServeHTTP(rec, req)
	return rec
}

func TestHeaderNavModuleAuth_DefaultPublicAccess(t *testing.T) {
	withHeaderNavModules(t, "")
	rec := runHeaderNav(t, HeaderNavModuleAuth("pricing"), false)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestHeaderNavModuleAuth_DisabledForbidden(t *testing.T) {
	withHeaderNavModules(t, `{"pricing":{"enabled":false,"requireAuth":false}}`)
	rec := runHeaderNav(t, HeaderNavModuleAuth("pricing"), false)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHeaderNavModuleAuth_RequireAuthRejectsAnonymous(t *testing.T) {
	withHeaderNavModules(t, `{"pricing":{"enabled":true,"requireAuth":true}}`)
	rec := runHeaderNav(t, HeaderNavModuleAuth("pricing"), false)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHeaderNavModuleAuth_RequireAuthAllowsLoggedIn(t *testing.T) {
	withHeaderNavModules(t, `{"pricing":{"enabled":true,"requireAuth":true}}`)
	rec := runHeaderNav(t, HeaderNavModuleAuth("pricing"), true)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestHeaderNavModulePublicOrUserAuth_PublicAccess(t *testing.T) {
	withHeaderNavModules(t, "")
	rec := runHeaderNav(t, HeaderNavModulePublicOrUserAuth("pricing"), false)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestHeaderNavModulePublicOrUserAuth_DisabledRequiresLogin(t *testing.T) {
	withHeaderNavModules(t, `{"pricing":{"enabled":false,"requireAuth":false}}`)
	rec := runHeaderNav(t, HeaderNavModulePublicOrUserAuth("pricing"), false)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHeaderNavModulePublicOrUserAuth_DisabledAllowsLoggedIn(t *testing.T) {
	withHeaderNavModules(t, `{"pricing":{"enabled":false,"requireAuth":false}}`)
	rec := runHeaderNav(t, HeaderNavModulePublicOrUserAuth("pricing"), true)
	require.Equal(t, http.StatusOK, rec.Code)
}

func performHeaderNavRequest(t *testing.T, handler gin.HandlerFunc, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/test", handler, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	var accessToken string
	if authenticated {
		previousDB, previousRedis := model.DB, common.RedisEnabled
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}))
		model.DB = db
		common.RedisEnabled = false
		require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
		t.Cleanup(func() {
			model.DB = previousDB
			common.RedisEnabled = previousRedis
		})
		accessToken = "header-nav-pat"
		user := model.User{
			Username:    "tester",
			Password:    "unused-password-hash",
			Role:        common.RoleCommonUser,
			Status:      common.UserStatusEnabled,
			Group:       "default",
			AuthVersion: 1,
		}
		user.SetAccessToken(accessToken)
		require.NoError(t, db.Create(&user).Error)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	if authenticated {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestHeaderNavModuleAuthAllowsDefaultPublicAccess(t *testing.T) {
	withHeaderNavModules(t, "")

	recorder := performHeaderNavRequest(t, HeaderNavModuleAuth("pricing"), false)

	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestHeaderNavModuleAuthRejectsDisabledPricing(t *testing.T) {
	raw := `{"pricing":{"enabled":false,"requireAuth":false}}`
	withHeaderNavModules(t, raw)

	recorder := performHeaderNavRequest(t, HeaderNavModuleAuth("pricing"), false)

	require.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestDisabledPricingRequiresRootAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name       string
		module     string
		options    string
		role       int
		credential string
		wantStatus int
	}{
		{name: "root", role: common.RoleRootUser, wantStatus: http.StatusOK},
		{name: "root with login required", options: `{"pricing":{"enabled":false,"requireAuth":true}}`, role: common.RoleRootUser, wantStatus: http.StatusOK},
		{name: "root with legacy disabled setting", options: `{"pricing":false}`, role: common.RoleRootUser, wantStatus: http.StatusOK},
		{name: "ordinary user", role: common.RoleCommonUser, wantStatus: http.StatusForbidden},
		{name: "non-root administrator", role: common.RoleAdminUser, wantStatus: http.StatusForbidden},
		{name: "expired root token", role: common.RoleRootUser, credential: "expired", wantStatus: http.StatusUnauthorized},
		{name: "tampered root token", role: common.RoleRootUser, credential: "tampered", wantStatus: http.StatusUnauthorized},
		{name: "revoked root session", role: common.RoleRootUser, credential: "revoked", wantStatus: http.StatusUnauthorized},
		{name: "disabled root account", role: common.RoleRootUser, credential: "banned", wantStatus: http.StatusUnauthorized},
		{name: "root cannot bypass disabled rankings", module: "rankings", options: `{"rankings":false}`, role: common.RoleRootUser, wantStatus: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupDashboardAuthMiddlewareTest(t)
			gin.SetMode(gin.TestMode)
			options := tc.options
			if options == "" {
				options = `{"pricing":{"enabled":false,"requireAuth":false}}`
			}
			withHeaderNavModules(t, options)
			user := model.User{
				Username: "pricing-reader", Password: "unused-password-hash", Role: tc.role,
				Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
			}
			require.NoError(t, model.DB.Create(&user).Error)
			bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "header-nav-test")
			require.NoError(t, err)
			token := bundle.AccessToken
			switch tc.credential {
			case "expired":
				identity, internal, err := service.ParseDashboardAccessToken(token)
				require.NoError(t, err)
				require.True(t, internal)
				token = issueExpiredDashboardAccessToken(t, identity)
			case "tampered":
				token = tamperDashboardToken(token)
			case "revoked":
				revoked, err := model.RevokeUserSession(user.Id, bundle.Session.SID, "test")
				require.NoError(t, err)
				require.True(t, revoked)
			case "banned":
				require.NoError(t, model.DB.Model(&user).Update("status", common.UserStatusDisabled).Error)
			}

			module := tc.module
			if module == "" {
				module = "pricing"
			}
			handled := false
			router := gin.New()
			router.GET("/api/"+module, HeaderNavModuleAuth(module), func(c *gin.Context) {
				handled = true
				assert.Equal(t, user.Id, c.GetInt("id"))
				assert.Equal(t, common.RoleRootUser, c.GetInt("role"))
				c.JSON(http.StatusOK, gin.H{"success": true})
			})

			response := middlewareBearerRequest(router, "/api/"+module, token)

			assert.Equal(t, tc.wantStatus, response.Code)
			assert.Equal(t, tc.wantStatus == http.StatusOK, handled)
		})
	}
}

func TestHeaderNavModuleAuthRequiresLoginForPricing(t *testing.T) {
	raw := `{"pricing":{"enabled":true,"requireAuth":true}}`
	withHeaderNavModules(t, raw)

	recorder := performHeaderNavRequest(t, HeaderNavModuleAuth("pricing"), false)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestHeaderNavModuleAuthRequiresLoginForRankings(t *testing.T) {
	raw := `{"rankings":{"enabled":true,"requireAuth":true}}`
	withHeaderNavModules(t, raw)

	recorder := performHeaderNavRequest(t, HeaderNavModuleAuth("rankings"), false)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestHeaderNavModuleAuthRejectsLegacyDisabledModule(t *testing.T) {
	raw := `{"rankings":false}`
	withHeaderNavModules(t, raw)

	recorder := performHeaderNavRequest(t, HeaderNavModuleAuth("rankings"), false)

	require.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestHeaderNavModulePublicOrUserAuthAllowsDefaultPublicAccess(t *testing.T) {
	withHeaderNavModules(t, "")

	recorder := performHeaderNavRequest(t, HeaderNavModulePublicOrUserAuth("pricing"), false)

	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestHeaderNavModulePublicOrUserAuthRequiresLoginWhenDisabled(t *testing.T) {
	raw := `{"pricing":{"enabled":false,"requireAuth":false}}`
	withHeaderNavModules(t, raw)

	recorder := performHeaderNavRequest(t, HeaderNavModulePublicOrUserAuth("pricing"), false)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestHeaderNavModulePublicOrUserAuthAllowsLoggedInWhenDisabled(t *testing.T) {
	raw := `{"pricing":{"enabled":false,"requireAuth":false}}`
	withHeaderNavModules(t, raw)

	recorder := performHeaderNavRequest(t, HeaderNavModulePublicOrUserAuth("pricing"), true)

	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestHeaderNavModulePublicOrUserAuthRequiresLoginWhenRequireAuth(t *testing.T) {
	raw := `{"pricing":{"enabled":true,"requireAuth":true}}`
	withHeaderNavModules(t, raw)

	recorder := performHeaderNavRequest(t, HeaderNavModulePublicOrUserAuth("pricing"), false)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestHeaderNavModulePublicOrUserAuthRequiresLoginForLegacyDisabledModule(t *testing.T) {
	raw := `{"pricing":false}`
	withHeaderNavModules(t, raw)

	recorder := performHeaderNavRequest(t, HeaderNavModulePublicOrUserAuth("pricing"), false)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestHeaderNavPublicRouteRejectsExpiredInternalAccessToken(t *testing.T) {
	setupDashboardAuthMiddlewareTest(t)
	withHeaderNavModules(t, "")
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/api/test", HeaderNavModuleAuth("pricing"), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	request.Header.Set("Authorization", "Bearer "+issueExpiredDashboardAccessToken(t, service.AuthIdentity{
		UserID: 1, SessionID: "expired-header-nav-session", UserAuthVersion: 1, SessionVersion: 1,
	}))
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusUnauthorized, response.Code)
	require.Contains(t, response.Body.String(), "AUTH_TOKEN_EXPIRED")
}
