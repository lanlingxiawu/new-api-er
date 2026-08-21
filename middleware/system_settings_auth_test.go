package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRequireSystemSettingsScope_RootCompatibilityWithoutScope(t *testing.T) {
	ctx, recorder := systemSettingsContext(t, http.MethodGet, "/api/option/", "")
	ctx.Set("role", common.RoleRootUser)
	RequireSystemSettingsScope(authz.ActionView)(ctx)

	assert.False(t, ctx.IsAborted())
	assert.Equal(t, http.StatusOK, recorder.Code)
}

func TestRequireSystemSettingsScope_AdminRequiresKnownScope(t *testing.T) {
	for _, url := range []string{"/api/option/", "/api/option/?scope=unknown.scope"} {
		ctx, _ := systemSettingsContext(t, http.MethodGet, url, "")
		ctx.Set("role", common.RoleAdminUser)
		RequireSystemSettingsScope(authz.ActionView)(ctx)
		assert.True(t, ctx.IsAborted())
	}
}

func TestRequireSystemSettingsScope_AdminPermissionAndBodyExtraction(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:settings-auth?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}))
	previousMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	require.NoError(t, authz.Init(db))
	require.NoError(t, authz.SetUserPermissions(71001, authz.PermissionsMap{
		authz.SystemSettingsResource("site.notice"): {
			authz.ActionView: true,
			authz.ActionEdit: false,
		},
	}))

	viewCtx, _ := systemSettingsContext(t, http.MethodGet, "/api/option/?scope=site.notice", "")
	viewCtx.Set("id", 71001)
	viewCtx.Set("role", common.RoleAdminUser)
	RequireSystemSettingsScope(authz.ActionView)(viewCtx)
	assert.False(t, viewCtx.IsAborted())
	assert.Equal(t, "site.notice", viewCtx.GetString(SystemSettingsScopeContextKey))

	editCtx, recorder := systemSettingsContext(t, http.MethodPut, "/api/option/", `{"scope":"site.notice","key":"Notice","value":"hello"}`)
	editCtx.Set("id", 71001)
	editCtx.Set("role", common.RoleAdminUser)
	RequireSystemSettingsScope(authz.ActionEdit)(editCtx)
	assert.True(t, editCtx.IsAborted())
	assert.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestRequireSystemSettingsScope_ChannelProfitPreviewUsesSensitiveChannelPermission(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:settings-auth-channel?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}))
	previousMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	require.NoError(t, authz.Init(db))
	require.NoError(t, authz.SetUserPermissions(71002, authz.PermissionsMap{
		authz.ResourceChannel: {authz.ActionSensitiveWrite: true},
	}))

	viewCtx, _ := systemSettingsContext(t, http.MethodGet, "/api/option/?scope=channel.profit-preview", "")
	viewCtx.Set("id", 71002)
	viewCtx.Set("role", common.RoleAdminUser)
	RequireSystemSettingsScope(authz.ActionView)(viewCtx)
	assert.False(t, viewCtx.IsAborted())
	assert.Equal(t, "channel.profit-preview", viewCtx.GetString(SystemSettingsScopeContextKey))

	otherScopeCtx, otherScopeRecorder := systemSettingsContext(t, http.MethodGet, "/api/option/?scope=billing.group-pricing", "")
	otherScopeCtx.Set("id", 71002)
	otherScopeCtx.Set("role", common.RoleAdminUser)
	RequireSystemSettingsScope(authz.ActionView)(otherScopeCtx)
	assert.True(t, otherScopeCtx.IsAborted())
	assert.Equal(t, http.StatusForbidden, otherScopeRecorder.Code)

	editCtx, editRecorder := systemSettingsContext(t, http.MethodPut, "/api/option/", `{"scope":"channel.profit-preview","key":"GroupRatio","value":"{}"}`)
	editCtx.Set("id", 71002)
	editCtx.Set("role", common.RoleAdminUser)
	RequireSystemSettingsScope(authz.ActionEdit)(editCtx)
	assert.True(t, editCtx.IsAborted())
	assert.Equal(t, http.StatusForbidden, editRecorder.Code)
}

func TestRequireSystemSettingsScope_VeridropUsesIndependentMenuPermission(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:settings-auth-veridrop?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}))
	previousMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	require.NoError(t, authz.Init(db))
	require.NoError(t, authz.SetUserPermissions(71003, authz.PermissionsMap{
		authz.ResourceAdminMenuVeridropDetection: {authz.ActionView: true, authz.ActionEdit: false},
	}))

	viewCtx, _ := systemSettingsContext(t, http.MethodGet, "/api/option/?scope=veridrop-detection", "")
	viewCtx.Set("id", 71003)
	viewCtx.Set("role", common.RoleAdminUser)
	RequireSystemSettingsScope(authz.ActionView)(viewCtx)
	assert.False(t, viewCtx.IsAborted())

	editCtx, recorder := systemSettingsContext(t, http.MethodPut, "/api/option/group", `{"scope":"veridrop-detection","module":"veridrop_monitor_setting","values":{"enabled":"true"}}`)
	editCtx.Set("id", 71003)
	editCtx.Set("role", common.RoleAdminUser)
	RequireSystemSettingsScope(authz.ActionEdit)(editCtx)
	assert.True(t, editCtx.IsAborted())
	assert.Equal(t, http.StatusForbidden, recorder.Code)
}

func systemSettingsContext(t *testing.T, method string, url string, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, url, strings.NewReader(body))
	if body != "" {
		ctx.Request.Header.Set("Content-Type", "application/json")
	}
	return ctx, recorder
}
