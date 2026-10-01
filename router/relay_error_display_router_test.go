package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/service/settingsaccess"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Review item 5, through the real router and middleware: an admin who may view
// only another settings scope gets past RequireSystemSettingsScope by naming
// that scope, and must still be refused by the relay error display handlers;
// an admin allowed this feature's scope, and root without a scope, are served.
//
// The router package has no database harness, so this uses SQLite in memory
// (Rule 15.5 fallback). Nothing here is dialect specific: users are found by
// access token and permissions are casbin rows.
func TestRelayErrorDisplayRoutesServeOnlyTheirOwnScope(t *testing.T) {
	require.NoError(t, i18n.Init())
	db, err := gorm.Open(sqlite.Open("file:relay-error-display-routes?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.CasbinRule{}, &model.AuthzRole{}, &model.Log{}))
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMaster, previousRedis := common.IsMasterNode, common.RedisEnabled
	model.DB, model.LOG_DB = db, db
	common.IsMasterNode, common.RedisEnabled = true, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.IsMasterNode, common.RedisEnabled = previousMaster, previousRedis
	})
	require.NoError(t, authz.Init(db))

	users := map[string]*model.User{}
	for name, role := range map[string]int{"red-other": common.RoleAdminUser, "red-own": common.RoleAdminUser, "red-root": common.RoleRootUser} {
		token := strings.Repeat(name[4:5], 32-len(name)) + name
		user := &model.User{Username: name, Password: "unused-password", Role: role, Status: common.UserStatusEnabled, AccessToken: &token, AuthVersion: 1}
		require.NoError(t, db.Create(user).Error)
		users[name] = user
	}
	require.NoError(t, authz.SetUserPermissions(users["red-other"].Id, authz.PermissionsMap{
		authz.SystemSettingsResource("site.notice"): {authz.ActionView: true},
	}))
	require.NoError(t, authz.SetUserPermissions(users["red-own"].Id, authz.PermissionsMap{
		authz.SystemSettingsResource(settingsaccess.ScopeRelayErrorDisplay): {authz.ActionView: true},
	}))

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	serve := func(user, method, path, body string) (int, bool) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+*users[user].AccessToken)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		var out struct {
			Success bool `json:"success"`
		}
		_ = common.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out.Success
	}
	preview := func(scope string) string {
		return `{"scope":"` + scope + `","setting":{"enabled":true,"hide_upstream_errors":true},"sample":{"source":"upstream","message":"x"}}`
	}
	own := settingsaccess.ScopeRelayErrorDisplay
	for _, tc := range []struct {
		name, user, method, path, body string
		status                         int
		success                        bool
	}{
		{"status, other scope", "red-other", http.MethodGet, "/api/option/relay-error-display/status?scope=site.notice", "", http.StatusOK, false},
		{"presets, other scope", "red-other", http.MethodGet, "/api/option/relay-error-display/presets?scope=site.notice", "", http.StatusOK, false},
		{"preview, other scope", "red-other", http.MethodPost, "/api/option/relay-error-display/preview", preview("site.notice"), http.StatusOK, false},
		{"status, own scope without its permission", "red-other", http.MethodGet, "/api/option/relay-error-display/status?scope=" + own, "", http.StatusForbidden, false},
		{"status, own scope", "red-own", http.MethodGet, "/api/option/relay-error-display/status?scope=" + own, "", http.StatusOK, true},
		{"presets, own scope", "red-own", http.MethodGet, "/api/option/relay-error-display/presets?scope=" + own, "", http.StatusOK, true},
		{"preview, own scope", "red-own", http.MethodPost, "/api/option/relay-error-display/preview", preview(own), http.StatusOK, true},
		{"status, root without scope", "red-root", http.MethodGet, "/api/option/relay-error-display/status", "", http.StatusOK, true},
		{"preview, root without scope", "red-root", http.MethodPost, "/api/option/relay-error-display/preview", preview(""), http.StatusOK, true},
	} {
		status, success := serve(tc.user, tc.method, tc.path, tc.body)
		assert.Equal(t, tc.status, status, tc.name)
		assert.Equal(t, tc.success, success, tc.name)
	}
}
