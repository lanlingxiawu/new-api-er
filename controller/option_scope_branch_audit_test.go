package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Branch audit of scoped system-settings access. On main the whole /api/option
// group is RootAuth; the fork opens it to administrators and relies on two
// cooperating checks: RequireSystemSettingsScope authorizes the scope named in
// the request, and the controller restricts the keys to that scope's allowlist.
// These tests drive the real middleware + controller chain for an administrator
// who holds only system_settings.site.notice (view + edit).

const auditScopedAdminID = 71_900_001

// setupScopedOptionAudit isolates options, audit logs and casbin policy in a
// private in-memory SQLite database so nothing leaks into the shared dev DB.
func setupScopedOptionAudit(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	prevDB, prevLogDB := model.DB, model.LOG_DB
	prevMain, prevLog := common.MainDatabaseType(), common.LogDatabaseType()
	prevMaster, prevRedis := common.IsMasterNode, common.RedisEnabled
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.IsMasterNode = true
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Log{}, &model.CasbinRule{}, &model.AuthzRole{}))
	require.NoError(t, authz.Init(db))
	require.NoError(t, authz.SetUserPermissions(auditScopedAdminID, authz.PermissionsMap{
		authz.SystemSettingsResource("site.notice"): {authz.ActionView: true, authz.ActionEdit: true},
	}))

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	snapshot := map[string]string{}
	for _, k := range []string{"Notice", "About", "SMTPToken"} {
		if v, ok := common.OptionMap[k]; ok {
			snapshot[k] = v
		}
	}
	common.OptionMap["Notice"] = "notice-before"
	common.OptionMap["About"] = "about-before"
	common.OptionMap["SMTPToken"] = "smtp-secret"
	common.OptionMapRWMutex.Unlock()
	prevSession := operation_setting.GetUserSessionSetting()

	t.Cleanup(func() {
		operation_setting.ReplaceUserSessionSetting(prevSession)
		common.OptionMapRWMutex.Lock()
		for _, k := range []string{"Notice", "About", "SMTPToken"} {
			if v, ok := snapshot[k]; ok {
				common.OptionMap[k] = v
			} else {
				delete(common.OptionMap, k)
			}
		}
		common.OptionMapRWMutex.Unlock()
		model.DB, model.LOG_DB = prevDB, prevLogDB
		common.SetDatabaseTypes(prevMain, prevLog)
		common.IsMasterNode, common.RedisEnabled = prevMaster, prevRedis
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("id", auditScopedAdminID)
		c.Set("role", common.RoleAdminUser)
		c.Set("username", "scoped-admin")
	})
	engine.GET("/api/option/", middleware.RequireSystemSettingsScope(authz.ActionView), GetOptions)
	engine.PUT("/api/option/", middleware.RequireSystemSettingsScope(authz.ActionEdit), UpdateOption)
	engine.PUT("/api/option/group", middleware.RequireSystemSettingsScope(authz.ActionEdit), UpdateOptionGroup)
	return engine
}

func serveScoped(engine *gin.Engine, method, target, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func scopedOption(key string) string {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return common.OptionMap[key]
}

func assertScopedRejected(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code == http.StatusForbidden {
		return
	}
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"success":false`)
}

func TestBranchAuditScopedAdmin_EditsKeyInsideGrantedScope(t *testing.T) {
	engine := setupScopedOptionAudit(t)
	rec := serveScoped(engine, http.MethodPut, "/api/option/", "application/json",
		`{"scope":"site.notice","key":"Notice","value":"notice-after"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"success":true`)
	assert.Equal(t, "notice-after", scopedOption("Notice"))
}

func TestBranchAuditScopedAdmin_JSONKeyOutsideScopeRejected(t *testing.T) {
	engine := setupScopedOptionAudit(t)
	for _, key := range []string{"About", "SMTPToken", "GroupRatio", "QuotaForNewUser"} {
		rec := serveScoped(engine, http.MethodPut, "/api/option/", "application/json",
			fmt.Sprintf(`{"scope":"site.notice","key":%q,"value":"changed"}`, key))
		assertScopedRejected(t, rec)
	}
	assert.Equal(t, "about-before", scopedOption("About"))
	assert.Equal(t, "smtp-secret", scopedOption("SMTPToken"))
}

func TestBranchAuditScopedAdmin_ScopeWithoutPermissionOrMissingIsRejected(t *testing.T) {
	engine := setupScopedOptionAudit(t)
	for _, body := range []string{
		`{"scope":"site.system-info","key":"About","value":"x"}`, // scope not granted
		`{"key":"About","value":"x"}`,                            // root-only unscoped write
		`{"scope":"no.such-scope","key":"About","value":"x"}`,
	} {
		assertScopedRejected(t, serveScoped(engine, http.MethodPut, "/api/option/", "application/json", body))
	}
	// A body the middleware cannot classify leaves the scope empty: admins fail closed.
	assertScopedRejected(t, serveScoped(engine, http.MethodPut, "/api/option/", "text/plain",
		`{"scope":"site.notice","key":"About","value":"x"}`))
	assert.Equal(t, "about-before", scopedOption("About"))
}

func TestBranchAuditScopedAdmin_GroupSaveOutsideScopeRejected(t *testing.T) {
	engine := setupScopedOptionAudit(t)
	before := operation_setting.GetUserSessionSetting()
	rec := serveScoped(engine, http.MethodPut, "/api/option/group", "application/json",
		fmt.Sprintf(`{"scope":"site.notice","module":"user_session_setting","values":{"hourly_alert_threshold":"%d"}}`,
			before.HourlyAlertThreshold+1))
	assertScopedRejected(t, rec)
	assert.Equal(t, before, operation_setting.GetUserSessionSetting())
}

func TestBranchAuditScopedAdmin_ReadReturnsOnlyScopeKeys(t *testing.T) {
	engine := setupScopedOptionAudit(t)
	rec := serveScoped(engine, http.MethodGet, "/api/option/?scope=site.notice", "", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp struct {
		Success bool `json:"success"`
		Data    []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	require.Len(t, resp.Data, 1)
	assert.Equal(t, "Notice", resp.Data[0].Key)

	assertScopedRejected(t, serveScoped(engine, http.MethodGet, "/api/option/?scope=billing.payment", "", ""))
	assertScopedRejected(t, serveScoped(engine, http.MethodGet, "/api/option/", "", ""))
}
