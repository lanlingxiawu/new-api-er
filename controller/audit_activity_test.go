package controller

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAuditActorFilterRoleBoundary(t *testing.T) {
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		for _, actor := range []string{"", "user", "employee", "unclassified_user", "admin", "root", "unknown", "invalid"} {
			allowed := actor != "invalid" && (actor != "admin" || role >= common.RoleAdminUser) &&
				((actor != "root" && actor != "unknown") || role >= common.RoleRootUser)
			t.Run(fmt.Sprintf("%d/%s", role, actor), func(t *testing.T) {
				for _, path := range []string{"/api/audit", "/api/audit/routes", "/api/audit/ip-markers", "/api/audit/ip-records"} {
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Set("id", 3)
					c.Set("role", role)
					c.Request = httptest.NewRequest("GET", path+"?start_timestamp=1&end_timestamp=2&actor_type="+actor+"&role=100&viewer_role=100", nil)
					_, ok := parseAuditFilter(c, true)
					require.Equal(t, allowed, ok, path)
				}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Set("role", role)
				c.Request = httptest.NewRequest("POST", "/api/audit/export/jobs?role=100", strings.NewReader(fmt.Sprintf(`{"filters":{"start_timestamp":1,"end_timestamp":2,"actor_type":%q}}`, actor)))
				c.Request.Header.Set("Content-Type", "application/json")
				_, _, err := parseAuditExportRequest(c, false)
				require.Equal(t, allowed, err == nil, "export")
			})
		}
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("role", common.RoleRootUser)
	c.Set("log_export_access", model.LogExportAccess{Role: common.RoleAdminUser})
	c.Request = httptest.NewRequest("POST", "/api/audit/export/jobs", strings.NewReader(`{"filters":{"start_timestamp":1,"end_timestamp":2,"actor_type":"root"}}`))
	c.Request.Header.Set("Content-Type", "application/json")
	_, _, err := parseAuditExportRequest(c, false)
	require.Error(t, err, "use the freshly verified export role, not an older authentication snapshot")
}

func TestAuditActivityLastUseAfterDemotion(t *testing.T) {
	user, token := setupAccessTokenAudit(t)
	for _, row := range []model.AuditLog{
		{EventId: "common-use", UserId: user.Id, ActorRole: common.RoleCommonUser, CreatedAt: 100, Ip: "192.0.2.1", Category: model.AuditCategoryAccessToken, TokenRef: model.AccessTokenFingerprint(token)},
		{EventId: "admin-use", UserId: user.Id, ActorRole: common.RoleAdminUser, CreatedAt: 200, Ip: "192.0.2.2", Category: model.AuditCategoryAccessToken, TokenRef: model.AccessTokenFingerprint(token)},
	} {
		require.NoError(t, model.LOG_DB.Create(&row).Error)
	}
	status, err := model.GetUserAccessTokenStatus(user.Id)
	require.NoError(t, err)
	require.Equal(t, "192.0.2.2", status.LastUsedIp)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("role", common.RoleCommonUser).Error)
	status, err = model.GetUserAccessTokenStatus(user.Id)
	require.NoError(t, err)
	require.Equal(t, "192.0.2.1", status.LastUsedIp)
	require.EqualValues(t, 100, *status.LastUsedAt)
}

func TestAuditActivityFilterBoundary(t *testing.T) {
	for _, tc := range []struct {
		query string
		valid bool
	}{
		{"ip=192.0.2.1&auth_method=session&request_id=request&success=false", true},
		{"user_id=999&username=root&self_view=false", true},
		{"actor_type=root", false}, {"route=/api/user/login", false}, {"route_missing=true", false},
		{"token_ref=" + strings.Repeat("a", 64), false},
		{"category=access_token&token_ref=" + strings.Repeat("a", 64), true},
		{"category=access_token&token_ref=" + strings.Repeat("a", 64) + "&exclude_token_ref=" + strings.Repeat("b", 64), false},
	} {
		engine := gin.New()
		engine.GET("/api/audit/self", func(c *gin.Context) {
			c.Set("id", 3)
			filter, valid := parseAuditFilter(c, false)
			require.Equal(t, tc.valid, valid, tc.query)
			require.Equal(t, 3, filter.UserId)
			require.True(t, filter.SelfView)
			require.Empty(t, filter.Username)
		})
		engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/audit/self?"+tc.query, nil))
	}
	for _, field := range []string{"actor_type", "route", "route_missing", "token_ref"} {
		value := any("root")
		if field == "route" {
			value = "/api/user/login"
		}
		if field == "route_missing" {
			value = true
		}
		if field == "token_ref" {
			value = strings.Repeat("a", 64)
		}
		body, err := common.Marshal(map[string]any{"filters": map[string]any{field: value, "start_timestamp": 1, "end_timestamp": 2}})
		require.NoError(t, err)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader(string(body)))
		_, _, err = parseAuditExportRequest(c, true)
		require.Error(t, err, field)
	}
	engine := gin.New()
	engine.GET("/api/audit/self/routes", func(c *gin.Context) { c.Set("id", 3); GetAuditRoutes(c) })
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest("GET", "/api/audit/self/routes?"+url.Values{"start_timestamp": {"1"}, "end_timestamp": {"2"}}.Encode(), nil))
	require.JSONEq(t, `{"success":true,"message":"","data":{"items":[]}}`, response.Body.String())
}

func TestEmployeeAuditFilterRejectsUnusedFilters(t *testing.T) {
	for _, query := range []string{"actor_type=root", "route=/internal", "token_ref=private", "ip=fe80::1%25eth0"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/?"+query, nil)
		_, err := employeeCustomerAuditFilter(c, 3)
		require.ErrorIs(t, err, model.ErrCustomerAuditParams, query)
	}
}

func TestAuditActivityOldExportDownloads(t *testing.T) {
	exportManagementFixture(t)
	job := mkReadyExportJob(t, 42, "private-old-audit-data")
	job.DataSource, job.SelfExport = model.LogExportDataAudit, true
	job.Filters.UserId = 42
	job.AuditFilters = &model.AuditExportFilter{AuditLogFilter: model.AuditLogFilter{UserId: 42, SelfView: true}}
	model.UpdateLogExportJob(job)
	access := model.LogExportAccess{UserID: 42, Role: common.RoleCommonUser, Scope: "audit_self"}
	c, rec := newCtx(t, "GET", "/api/audit/self/export/jobs/"+job.JobID, nil)
	c.Params = gin.Params{{Key: "job_id", Value: job.JobID}}
	c.Set("log_export_access", access)
	GetLogExportJob(c)
	require.True(t, decodeResp(t, rec).Success, "old jobs remain visible for deletion")
	c, rec = newCtx(t, "GET", "/api/audit/self/export/jobs/"+job.JobID+"/download-url", nil)
	c.Params = gin.Params{{Key: "job_id", Value: job.JobID}}
	c.Set("log_export_access", access)
	GetLogExportDownloadURL(c)
	require.False(t, decodeResp(t, rec).Success)
	for _, partial := range []bool{false, true} {
		token, err := model.CreateManagedLogExportDownloadToken(job.JobID, 42, 1, "audit_self")
		require.NoError(t, err)
		c, rec = newCtx(t, "GET", "/dl/log-export/"+token, nil)
		c.Params = gin.Params{{Key: "token", Value: token}}
		if partial {
			c.Request.Header.Set("Range", "bytes=0-5")
		}
		DownloadLogExport(c)
		require.False(t, decodeResp(t, rec).Success)
		require.NotContains(t, rec.Body.String(), "private-old")
	}
}

func TestAuditActivityExportDowngradeDownloadURL(t *testing.T) {
	exportManagementFixture(t)
	job := mkReadyExportJob(t, 42, "higher-role-audit-data")
	job.DataSource, job.SelfExport = model.LogExportDataAudit, true
	job.CreatorRole, job.AuditProjectionVersion = common.RoleRootUser, model.AuditActivityProjectionVersion
	job.Filters.UserId = 42
	job.AuditFilters = &model.AuditExportFilter{AuditLogFilter: model.AuditLogFilter{UserId: 42, SelfView: true}}
	model.UpdateLogExportJob(job)
	for _, role := range []int{common.RoleAdminUser, common.RoleCommonUser} {
		c, rec := newCtx(t, "GET", "/api/audit/self/export/jobs/"+job.JobID+"/download-url", nil)
		c.Params = gin.Params{{Key: "job_id", Value: job.JobID}}
		c.Set("log_export_access", model.LogExportAccess{UserID: 42, Role: role, Scope: "audit_self"})
		GetLogExportDownloadURL(c)
		require.Equal(t, 403, rec.Code)
		var response struct{ Success bool }
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &response))
		require.False(t, response.Success)
	}
}
