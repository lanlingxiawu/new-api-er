package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/service/settingsaccess"
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

// 员工页的「提成周期重置」卡片保存走 /api/option，所以必须有一个已登记的 scope；
// 权限门槛跟随员工管理菜单，与同一张卡片上的「立即重置」「安全切换」保持一致。
// 不带 scope 时非 root 管理员会被判「参数错误」直接 abort——这正是卡片保存失败的原因。
func TestRequireSystemSettingsScope_CommissionTierResetUsesEmployeeMenuPermission(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:settings-auth-commission?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}))
	previousMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	require.NoError(t, authz.Init(db))
	require.NoError(t, authz.SetUserPermissions(71004, authz.PermissionsMap{
		authz.ResourceAdminMenuEmployees: {authz.ActionView: true},
	}))
	require.NoError(t, authz.SetUserPermissions(71005, authz.PermissionsMap{
		authz.ResourceAdminMenuEmployees: {authz.ActionView: false},
	}))

	const body = `{"scope":"employees.commission-tier-reset","key":"commission_tier_reset_setting.reset_day","value":"5"}`
	editCtx, _ := systemSettingsContext(t, http.MethodPut, "/api/option/", body)
	editCtx.Set("id", 71004)
	editCtx.Set("role", common.RoleAdminUser)
	RequireSystemSettingsScope(authz.ActionEdit)(editCtx)
	assert.False(t, editCtx.IsAborted(), "能管理员工的管理员必须能保存提成周期重置配置")
	assert.Equal(t, settingsaccess.ScopeCommissionTierReset, editCtx.GetString(SystemSettingsScopeContextKey))

	deniedCtx, deniedRecorder := systemSettingsContext(t, http.MethodPut, "/api/option/", body)
	deniedCtx.Set("id", 71005)
	deniedCtx.Set("role", common.RoleAdminUser)
	RequireSystemSettingsScope(authz.ActionEdit)(deniedCtx)
	assert.True(t, deniedCtx.IsAborted(), "没有员工管理权限的管理员不能保存")
	assert.Equal(t, http.StatusForbidden, deniedRecorder.Code)
}

// 该 scope 只放行提成周期重置这一组键，不能变成写任意配置的后门。
func TestCommissionTierResetScopeAllowsOnlyItsOwnKeys(t *testing.T) {
	for _, key := range []string{
		"commission_tier_reset_setting.enabled",
		"commission_tier_reset_setting.period_mode",
		"commission_tier_reset_setting.reset_day",
		"commission_tier_reset_setting.reset_hour",
		"commission_tier_reset_setting.reset_minute",
		"commission_tier_reset_setting.reset_second",
		"commission_tier_reset_setting.timezone",
	} {
		assert.True(t, settingsaccess.AllowsOption(settingsaccess.ScopeCommissionTierReset, key), key)
	}
	// last_reset_at 由重置任务维护，不接受手工写入。
	assert.False(t, settingsaccess.AllowsOption(settingsaccess.ScopeCommissionTierReset, "commission_tier_reset_setting.last_reset_at"))
	assert.False(t, settingsaccess.AllowsOption(settingsaccess.ScopeCommissionTierReset, "SystemName"))
	assert.True(t, settingsaccess.AllowsGroup(settingsaccess.ScopeCommissionTierReset, "commission_tier_reset_setting", map[string]string{"reset_day": "5"}))
	assert.False(t, settingsaccess.AllowsGroup(settingsaccess.ScopeCommissionTierReset, "commission_tier_reset_setting", map[string]string{"last_reset_at": "1"}))
}
