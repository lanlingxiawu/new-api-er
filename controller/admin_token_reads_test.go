package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminTokenRobotReadCapabilities(t *testing.T) {
	actor := setupAdminTokenAutomation(t)
	db := model.DB
	require.NoError(t, db.AutoMigrate(&model.EmployeeProfile{}))
	if dsn := os.Getenv("TEST_ADMIN_TOKEN_LOG_DSN"); dsn != "" {
		logDB, _ := newAuditTestDatabase(t, "postgres", dsn)
		require.NoError(t, logDB.AutoMigrate(&model.Log{}, &model.AuditLog{}))
		previousDB, previousType := model.LOG_DB, common.LogDatabaseType()
		model.LOG_DB = logDB
		common.SetLogDatabaseType(common.DatabaseTypePostgreSQL)
		model.InitColumnNames()
		t.Cleanup(func() {
			model.LOG_DB = previousDB
			common.SetLogDatabaseType(previousType)
			model.InitColumnNames()
		})
	}
	grants := authz.PermissionsMap{authz.ResourceAdminAccessToken: {
		authz.ActionUse: true, "groups_read": true, "employees_read": true, "users_stats": true,
	}}
	router := gin.New()
	router.Use(middleware.RequestId(), middleware.AccessTokenAudit())
	router.GET("/api/group/", middleware.AdminAuth(), GetGroups)
	router.GET("/api/admin/employee", middleware.AdminAuth(), middleware.RequirePermission(authz.AdminMenuEmployeesView), AdminListEmployees)
	router.GET("/api/log/stat", middleware.AdminAuth(), GetLogsStat)
	router.GET("/api/user/", middleware.AdminAuth(), GetAllUsers)
	router.GET("/api/user/:id", middleware.AdminAuth(), GetUser)
	router.GET("/api/log/", middleware.AdminAuth(), GetAllLogs)
	now := common.GetTimestamp()
	raw, token := createScopedAccessToken(t, actor.Id, now+3600, "group:read", "user:employee_read", "log:stats_read")
	for _, path := range []string{"/api/group/", "/api/admin/employee", "/api/log/stat"} {
		response := accessTokenRequest(router, "GET", path, raw, "", "")
		require.Equal(t, http.StatusForbidden, response.Code, path)
	}
	require.NoError(t, authz.SetUserPermissionsInTx(db, actor.Id, grants))
	scopes, err := service.NormalizeAccessTokenScopes(actor.Id, actor.Role, []string{"group:read", "user:employee_read", "log:stats_read"})
	require.NoError(t, err)
	require.Len(t, scopes, 3)
	_, err = service.NormalizeAccessTokenScopes(actor.Id, actor.Role, []string{"log:read", "group:write", "user:write"})
	require.ErrorIs(t, err, service.ErrAccessTokenScopeForbidden)
	response := accessTokenRequest(router, "GET", "/api/log/stat", raw, "", "")
	require.True(t, decodeResp(t, response).Success, response.Body.String())
	require.Contains(t, response.Body.String(), `"rpm":0`)
	require.Contains(t, response.Body.String(), `"tpm":0`)
	require.Contains(t, response.Body.String(), `"quota":0`)

	previousGroups := ratio_setting.GetGroupRatioCopy()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"vip":2,"default":1}`))
	t.Cleanup(func() {
		encoded, err := common.Marshal(previousGroups)
		require.NoError(t, err)
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(string(encoded)))
	})
	response = accessTokenRequest(router, "GET", "/api/group/", raw, "", "")
	require.JSONEq(t, `{"success":true,"message":"","data":["default","vip"]}`, response.Body.String())
	response = accessTokenRequest(router, "GET", "/api/group/?scope=all", raw, "", "")
	require.False(t, decodeResp(t, response).Success)

	staff := []model.User{
		{Username: "robot-staff-a", DisplayName: "Staff A", Email: "private-a@example.test", Remark: "private remark", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "robot-staff-a"},
		{Username: "robot-staff-b", DisplayName: "Staff B", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "robot-staff-b"},
		{Username: "robot-staff-off", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "robot-staff-off"},
		{Username: "robot-user-off", Role: common.RoleCommonUser, Status: common.UserStatusDisabled, AffCode: "robot-user-off"},
		{Username: "robot-user-deleted", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "robot-user-deleted"},
		{Username: "robot-admin-staff", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AffCode: "robot-admin-staff"},
	}
	profiles := make([]model.EmployeeProfile, len(staff))
	for i := range staff {
		require.NoError(t, db.Create(&staff[i]).Error)
		profiles[i] = model.EmployeeProfile{UserId: staff[i].Id, Status: 1, Remark: "private profile", TargetAmount: 999}
		if i == 2 {
			profiles[i].Status = 2
		}
		require.NoError(t, db.Create(&profiles[i]).Error)
	}
	require.NoError(t, db.Delete(&staff[4]).Error)
	var page struct {
		Success bool `json:"success"`
		Data    struct {
			Items    []map[string]any `json:"items"`
			Total    int64            `json:"total"`
			Page     int              `json:"page"`
			PageSize int              `json:"page_size"`
		} `json:"data"`
	}
	for n := 1; n <= 3; n++ {
		response = accessTokenRequest(router, "GET", fmt.Sprintf("/api/admin/employee?page=%d&page_size=1&status=1", n), raw, "", "")
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &page))
		require.True(t, page.Success, response.Body.String())
		require.EqualValues(t, 2, page.Data.Total)
		require.Equal(t, n, page.Data.Page)
		require.Equal(t, 1, page.Data.PageSize)
		if n == 3 {
			require.Empty(t, page.Data.Items)
			continue
		}
		require.Len(t, page.Data.Items, 1)
		require.Len(t, page.Data.Items[0], 5, "only identity and profile status may leave the server")
		require.EqualValues(t, profiles[2-n].Id, page.Data.Items[0]["id"])
		require.EqualValues(t, staff[2-n].Id, page.Data.Items[0]["user_id"])
		require.NotContains(t, response.Body.String(), "private")
	}
	for _, status := range []int{0, 2} {
		response = accessTokenRequest(router, "GET", fmt.Sprintf("/api/admin/employee?status=%d", status), raw, "", "")
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &page))
		require.True(t, page.Success)
		want := 3
		if status == 2 {
			want = 1
		}
		require.Len(t, page.Data.Items, want)
	}
	for _, query := range []string{"page=0", "page=10001", "page_size=0", "page_size=101", "status=3", "status=x", "status=1&status=2", "keyword=staff", "sort_by=total_profit_quota"} {
		response = accessTokenRequest(router, "GET", "/api/admin/employee?"+query, raw, "", "")
		require.False(t, decodeResp(t, response).Success, query)
	}
	response = accessTokenRequest(router, "GET", "/api/admin/employee?page=10000&page_size=100", raw, "", "")
	require.True(t, decodeResp(t, response).Success)

	target := model.User{Username: "robot_%_customer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "robot-customer"}
	other := model.User{Username: "robot_ab_customer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "robot-other"}
	require.NoError(t, db.Create(&target).Error)
	require.NoError(t, db.Create(&other).Error)
	now = common.GetTimestamp()
	logs := []model.Log{
		{UserId: target.Id, Username: "previous-name", Type: model.LogTypeConsume, Quota: 31, CreatedAt: now - 7200},
		{UserId: target.Id, Username: "previous-name", Type: model.LogTypeConsume, Quota: 70, CreatedAt: now - 120},
		{UserId: target.Id, Username: target.Username, Type: model.LogTypeConsume, Quota: 3, PromptTokens: 2, CompletionTokens: 5, CreatedAt: now - 2},
		{UserId: other.Id, Username: target.Username, Type: model.LogTypeConsume, Quota: 9000, PromptTokens: 200, CreatedAt: now - 2},
		{UserId: target.Id, Username: target.Username, Type: model.LogTypeError, Quota: 6000, CreatedAt: now - 2},
		{UserId: actor.Id, Username: actor.Username, Type: model.LogTypeConsume, Quota: 11, PromptTokens: 17, CreatedAt: now - 2},
		{UserId: staff[4].Id, Username: staff[4].Username, Type: model.LogTypeConsume, Quota: 13, CompletionTokens: 19, CreatedAt: now - 2},
	}
	require.NoError(t, model.LOG_DB.Create(&logs).Error)
	// Both cache modes must preserve the global aggregation and user isolation.
	logSettings := operation_setting.GetLogQuerySetting()
	previousCacheEnabled := logSettings.StatCacheEnabled
	t.Cleanup(func() { logSettings.StatCacheEnabled = previousCacheEnabled })
	for _, cacheEnabled := range []bool{true, false} {
		logSettings.StatCacheEnabled = cacheEnabled
		for _, tc := range []struct {
			name  string
			query string
			quota int
			start int64
			end   int64
		}{
			{name: "default window", quota: 9027},
			{name: "consumption type", query: "type=2", quota: 9027},
			{name: "last hour", start: now - 3600, end: now, quota: 9097},
			{name: "cached completed hours", start: now - 10800, end: now, quota: 9128},
			{name: "historical quota with live rates", start: now - 3600, end: now - 61, quota: 70},
			{name: "thirty day boundary", start: now - 30*86400, end: now, quota: 9128},
			{name: "empty quota window", start: now, end: now},
		} {
			t.Run(fmt.Sprintf("sitewide/cache=%t/%s", cacheEnabled, tc.name), func(t *testing.T) {
				query := tc.query
				if tc.start > 0 {
					query = fmt.Sprintf("start_timestamp=%d&end_timestamp=%d", tc.start, tc.end)
				}
				before := common.GetTimestamp()
				response := accessTokenRequest(router, "GET", "/api/log/stat?"+query, raw, "", "")
				after := common.GetTimestamp()
				require.Equal(t, http.StatusOK, response.Code)
				var result struct {
					Success bool `json:"success"`
					Data    struct {
						model.Stat
						QuotaPerUnit float64 `json:"quota_per_unit"`
						Start        int64   `json:"start_timestamp"`
						End          int64   `json:"end_timestamp"`
					} `json:"data"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				require.True(t, result.Success, response.Body.String())
				require.Equal(t, model.Stat{Quota: tc.quota, Rpm: 4, Tpm: 243}, result.Data.Stat)
				require.Equal(t, common.QuotaPerUnit, result.Data.QuotaPerUnit)
				if tc.start > 0 {
					require.Equal(t, tc.start, result.Data.Start)
					require.Equal(t, tc.end, result.Data.End)
				} else {
					require.GreaterOrEqual(t, result.Data.End, before)
					require.LessOrEqual(t, result.Data.End, after)
					require.Equal(t, int64(60), result.Data.End-result.Data.Start)
				}
				expected, err := common.Marshal(gin.H{"success": true, "message": "", "data": gin.H{
					"quota": tc.quota, "quota_per_unit": common.QuotaPerUnit, "rpm": 4, "tpm": 243,
					"start_timestamp": result.Data.Start, "end_timestamp": result.Data.End,
				}})
				require.NoError(t, err)
				require.JSONEq(t, string(expected), response.Body.String(), "global response only exposes aggregate fields")
			})
		}
	}
	logSettings.StatCacheEnabled = true
	// Warm the global cache first: a user filter must never reuse its totals.
	_, err = model.SumUsedQuotaWithFilter(context.Background(), model.LogFilter{StartTimestamp: now - 3600, EndTimestamp: now})
	require.NoError(t, err)
	for _, selector := range []string{fmt.Sprintf("user_id=%d", target.Id), "username=" + url.QueryEscape(target.Username)} {
		path := fmt.Sprintf("/api/log/stat?%s&start_timestamp=%d&end_timestamp=%d&type=2", selector, now-3600, now)
		response = accessTokenRequest(router, "GET", path, raw, "", "")
		var stat struct {
			Success bool `json:"success"`
			Data    struct {
				model.Stat
				UserID       int     `json:"user_id"`
				QuotaPerUnit float64 `json:"quota_per_unit"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &stat))
		require.True(t, stat.Success, response.Body.String())
		require.Equal(t, model.Stat{Quota: 73, Rpm: 1, Tpm: 7}, stat.Data.Stat)
		require.Equal(t, target.Id, stat.Data.UserID)
		require.Equal(t, common.QuotaPerUnit, stat.Data.QuotaPerUnit)
		response = accessTokenRequest(router, "GET", "/api/log/stat", raw, "", "")
		require.True(t, decodeResp(t, response).Success)
		require.Contains(t, response.Body.String(), `"quota":9027`)
		require.Contains(t, response.Body.String(), `"rpm":4`)
	}
	validStat := fmt.Sprintf("/api/log/stat?user_id=%d&start_timestamp=%d&end_timestamp=%d", target.Id, now-3600, now)
	for _, start := range []int64{now, now - 30*86400} {
		path := fmt.Sprintf("/api/log/stat?user_id=%d&start_timestamp=%d&end_timestamp=%d", target.Id, start, now)
		response = accessTokenRequest(router, "GET", path, raw, "", "")
		require.True(t, decodeResp(t, response).Success, path)
	}
	for _, query := range []string{
		fmt.Sprintf("user_id=%d", target.Id), "username=robot_%25_customer",
		fmt.Sprintf("start_timestamp=%d", now-10), fmt.Sprintf("end_timestamp=%d", now),
		fmt.Sprintf("start_timestamp=&end_timestamp=%d", now), fmt.Sprintf("start_timestamp=%d&end_timestamp=", now-10),
		fmt.Sprintf("start_timestamp=0&end_timestamp=%d", now), fmt.Sprintf("start_timestamp=-1&end_timestamp=%d", now),
		fmt.Sprintf("start_timestamp=x&end_timestamp=%d", now), fmt.Sprintf("start_timestamp=9223372036854775808&end_timestamp=%d", now),
		fmt.Sprintf("start_timestamp=%d&end_timestamp=0", now-10), fmt.Sprintf("start_timestamp=%d&end_timestamp=-1", now-10),
		fmt.Sprintf("start_timestamp=%d&end_timestamp=x", now-10), fmt.Sprintf("start_timestamp=%d&end_timestamp=9223372036854775808", now-10),
		fmt.Sprintf("start_timestamp=%d&end_timestamp=%d", now-30*86400-1, now),
		fmt.Sprintf("start_timestamp=%d&end_timestamp=%d", now, now-1),
		fmt.Sprintf("start_timestamp=%d&end_timestamp=%d", now-10, now+86400),
		"type=", "type=0", "type=5", "type=2&type=2", "group=default", "channel=1", "model_name=x", "username=%ZZ",
		fmt.Sprintf("username=robot_%%25&start_timestamp=%d&end_timestamp=%d", now-10, now),
		fmt.Sprintf("user_id=%d&start_timestamp=%d&end_timestamp=%d", actor.Id, now-10, now),
		fmt.Sprintf("user_id=%d&start_timestamp=%d&end_timestamp=%d", target.Id, now-30*86400-1, now),
		fmt.Sprintf("user_id=%d&start_timestamp=%d&end_timestamp=%d", target.Id, now, now-1),
		fmt.Sprintf("user_id=%d&start_timestamp=%d&end_timestamp=%d", target.Id, now-10, now+86400),
	} {
		response = accessTokenRequest(router, "GET", "/api/log/stat?"+query, raw, "", "")
		require.False(t, decodeResp(t, response).Success, query)
	}
	for _, extra := range []string{"&username=x", "&user_id=2", "&type=5", "&group=default", "&channel=1", "&start_timestamp=1"} {
		response = accessTokenRequest(router, "GET", validStat+extra, raw, "", "")
		require.False(t, decodeResp(t, response).Success, extra)
	}
	for _, selector := range []string{"user_id=0", "user_id=abc", "username=", "username=%FF", "username=abcdefghijklmnopqrstuvwxyz1234567"} {
		path := fmt.Sprintf("/api/log/stat?%s&start_timestamp=%d&end_timestamp=%d", selector, now-10, now)
		response = accessTokenRequest(router, "GET", path, raw, "", "")
		require.False(t, decodeResp(t, response).Success, selector)
	}
	for _, path := range []string{"/api/admin/employee?status=%ZZ", validStat + "&bad=%ZZ"} {
		response = accessTokenRequest(router, "GET", path, raw, "", "")
		require.False(t, decodeResp(t, response).Success)
	}
	// A dashboard session keeps the existing group and statistics contracts.
	_, jwt := createAccessTokenTestSession(t, actor.Id, "robot-read-dashboard")
	response = accessTokenRequest(router, "GET", "/api/group/?dashboard=unchanged", jwt, "", "")
	require.True(t, decodeResp(t, response).Success)
	response = accessTokenRequest(router, "GET", fmt.Sprintf("/api/log/stat?start_timestamp=%d&end_timestamp=%d", now-3600, now), jwt, "", "")
	require.True(t, decodeResp(t, response).Success)
	require.Contains(t, response.Body.String(), `"quota":9097`)
	for _, tc := range []struct{ path, action, scope string }{
		{"/api/group/", "groups_read", "group:read"},
		{"/api/admin/employee", "employees_read", "user:employee_read"},
		{validStat, "users_stats", "log:stats_read"},
		{"/api/log/stat", "users_stats", "log:stats_read"},
	} {
		require.NoError(t, authz.SetUserPermissionsInTx(db, actor.Id, authz.PermissionsMap{authz.ResourceAdminAccessToken: {tc.action: false}}))
		response = accessTokenRequest(router, "GET", tc.path, raw, "", "")
		require.Equal(t, http.StatusForbidden, response.Code, "revocation must be effective without reloading")
		_, err = service.NormalizeAccessTokenScopes(actor.Id, actor.Role, []string{tc.scope})
		require.ErrorIs(t, err, service.ErrAccessTokenScopeForbidden)
		require.NoError(t, authz.SetUserPermissionsInTx(db, actor.Id, grants))
	}
	for _, menu := range []string{authz.ResourceAdminMenuUsers, authz.ResourceAdminMenuEmployees} {
		require.NoError(t, authz.SetUserPermissionsInTx(db, actor.Id, authz.PermissionsMap{menu: {authz.ActionView: false}}))
		paths := []string{"/api/admin/employee"}
		if menu == authz.ResourceAdminMenuUsers {
			paths = append(paths, "/api/group/", validStat, "/api/log/stat")
		}
		for _, path := range paths {
			response = accessTokenRequest(router, "GET", path, raw, "", "")
			require.Equal(t, http.StatusForbidden, response.Code, path)
		}
		_, err = service.NormalizeAccessTokenScopes(actor.Id, actor.Role, []string{"user:employee_read"})
		require.ErrorIs(t, err, service.ErrAccessTokenScopeForbidden)
		require.NoError(t, authz.SetUserPermissionsInTx(db, actor.Id, authz.PermissionsMap{menu: {authz.ActionView: true}}))
	}
	require.NoError(t, authz.SetUserPermissionsInTx(db, actor.Id, authz.PermissionsMap{authz.ResourceAdminAccessToken: {authz.ActionUse: false}}))
	for _, path := range []string{"/api/group/", "/api/admin/employee", validStat, "/api/log/stat"} {
		response = accessTokenRequest(router, "GET", path, raw, "", "")
		require.Equal(t, http.StatusForbidden, response.Code)
	}
	require.NoError(t, authz.SetUserPermissionsInTx(db, actor.Id, grants))
	root := model.User{Username: "robot-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AffCode: "robot-root"}
	require.NoError(t, db.Create(&root).Error)
	rootToken, _ := createScopedAccessToken(t, root.Id, now+3600, "group:read", "user:employee_read", "log:stats_read")
	longToken, _ := createScopedAccessToken(t, actor.Id, now+31*86400, "group:read", "user:employee_read", "log:stats_read")
	require.NotNil(t, actor.AccessToken)
	for _, credential := range []string{rootToken, longToken, *actor.AccessToken} {
		for _, path := range []string{"/api/group/", "/api/admin/employee", validStat, "/api/log/stat"} {
			response = accessTokenRequest(router, "GET", path, credential, "", "")
			require.Equal(t, http.StatusForbidden, response.Code)
		}
	}
	for _, path := range []string{"/api/user/", fmt.Sprintf("/api/user/%d", target.Id), "/api/log/"} {
		response = accessTokenRequest(router, "GET", path, raw, "", "")
		require.Equal(t, http.StatusForbidden, response.Code, path)
	}
	groupOnly, _ := createScopedAccessToken(t, actor.Id, now+3600, "group:read")
	for _, path := range []string{"/api/admin/employee", validStat, "/api/log/stat", "/api/user/", fmt.Sprintf("/api/user/%d", target.Id), "/api/log/"} {
		response = accessTokenRequest(router, "GET", path, groupOnly, "", "")
		require.Equal(t, http.StatusForbidden, response.Code, path)
	}
	for _, path := range []string{validStat, "/api/log/stat"} {
		request := httptest.NewRequest("GET", path, nil)
		request.Header.Set("Authorization", "Bearer "+raw)
		ctx, cancel := context.WithCancel(request.Context())
		cancel()
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request.WithContext(ctx))
		require.Equal(t, http.StatusInternalServerError, recorder.Code)
		require.Contains(t, recorder.Body.String(), `"success":false`)
		require.NotContains(t, recorder.Body.String(), "context canceled")
	}
	_, err = model.UpdateUserAccessToken(actor.Id, token.Id, token.Name, []string{"group:read"})
	require.NoError(t, err)
	response = accessTokenRequest(router, "GET", "/api/log/stat", raw, "", "")
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Contains(t, response.Body.String(), "ACCESS_TOKEN_SCOPE_DENIED")
	_, err = model.UpdateUserAccessToken(actor.Id, token.Id, token.Name, []string{"group:read", "log:stats_read"})
	require.NoError(t, err)
	require.NoError(t, db.Model(token).Update("expires_at", now-1).Error)
	for _, path := range []string{"/api/group/", "/api/log/stat"} {
		response = accessTokenRequest(router, "GET", path, raw, "", "")
		require.Equal(t, http.StatusUnauthorized, response.Code)
	}
	revokedRaw, revokedToken := createScopedAccessToken(t, actor.Id, common.GetTimestamp()+3600, "log:stats_read")
	_, err = model.DeleteUserAccessToken(actor.Id, revokedToken.Id)
	require.NoError(t, err)
	response = accessTokenRequest(router, "GET", "/api/log/stat", revokedRaw, "", "")
	require.Equal(t, http.StatusUnauthorized, response.Code)
}
