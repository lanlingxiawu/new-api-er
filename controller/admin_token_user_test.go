package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

var adminTokenRedisDB int

func setupAdminTokenAutomation(t *testing.T) *model.User {
	t.Helper()
	dsn := os.Getenv("TEST_ADMIN_TOKEN_DSN")
	if dsn == "" {
		actor, _ := setupAccessTokenAudit(t)
		return actor
	}
	kind := "mysql"
	if strings.HasPrefix(dsn, "postgres") {
		kind = "postgres"
	}
	db, _ := newAuditTestDatabase(t, kind, dsn)
	actor, _ := initializeAccessTokenAudit(t, db)
	if redisURL := os.Getenv("TEST_ADMIN_TOKEN_REDIS"); redisURL != "" {
		options, err := redis.ParseURL(redisURL)
		require.NoError(t, err)
		// Each isolated SQL database starts its user IDs at one. Give it a
		// separate logical Redis database so cached identities cannot cross tests.
		adminTokenRedisDB++
		options.DB += adminTokenRedisDB
		client := redis.NewClient(options)
		require.NoError(t, client.Ping(context.Background()).Err())
		previous, previousRequestLog, enabled := common.RDB, common.RequestLogRDB, common.RedisEnabled
		common.RDB, common.RequestLogRDB, common.RedisEnabled = client, client, true
		t.Cleanup(func() {
			common.RDB, common.RequestLogRDB, common.RedisEnabled = previous, previousRequestLog, enabled
			require.NoError(t, client.Close())
		})
	}
	return actor
}

func TestAdminTokenRootGrantAndIssuanceLifecycle(t *testing.T) {
	actor := setupAdminTokenAutomation(t)
	require.NoError(t, model.DB.AutoMigrate(&model.UserLoginProtection{}))
	root := model.User{Username: "token-grant-root", Password: "placeholder", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: "token-grant-root"}
	require.NoError(t, model.DB.Create(&root).Error)
	rootIdentity, rootJWT := createAccessTokenTestSession(t, root.Id, "token-grant-root-session")
	router := gin.New()
	router.Use(middleware.RequestId(), middleware.AccessTokenAudit())
	users := router.Group("/api/user", middleware.AdminAuth(), middleware.RequirePermission(authz.AdminMenuUsersView))
	users.PUT("/", UpdateUser)
	users.GET("/search", SearchUsers)
	users.GET("/:id", GetUser)
	router.GET("/api/log/stat", middleware.AdminAuth(), GetLogsStat)
	tokens := router.Group("/api/user/access_tokens", middleware.UserAuth())
	tokens.POST("", CreateAccessToken)
	permissions := map[string]bool{
		authz.ActionUse: true, authz.ActionUsersList: true, authz.ActionUsersQuota: true,
		authz.ActionUsersGroupRatios: true, authz.ActionUsersEmployee: true, authz.ActionTopupsReconcile: true,
		"groups_read": true, "employees_read": true, "users_stats": true,
	}
	body, err := common.Marshal(auditUserBody(actor, map[string]any{"admin_permissions": authz.PermissionsMap{authz.ResourceAdminAccessToken: permissions}}))
	require.NoError(t, err)
	proof := issueSecurityEnrollmentProof(t, rootIdentity, service.VerificationOperation{Scope: service.VerificationScopeAdminUserUpdate, Context: []byte(fmt.Sprintf(`{"user_id":%d}`, actor.Id))}, service.VerificationMethodPassword)
	response := accessTokenRequest(router, "PUT", "/api/user/", rootJWT, proof, string(body))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"success":true`, response.Body.String())
	response = accessTokenRequest(router, "GET", fmt.Sprintf("/api/user/%d", actor.Id), rootJWT, "", "")
	var readback struct {
		Data struct {
			AdminPermissions authz.PermissionsMap `json:"admin_permissions"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &readback))
	for action := range permissions {
		require.True(t, readback.Data.AdminPermissions[authz.ResourceAdminAccessToken][action], action)
	}
	require.False(t, readback.Data.AdminPermissions[authz.ResourceAdminAccessToken][authz.ActionChannelsOwned])

	// A fresh dashboard session after granting permissions issues the bot PAT.
	require.NoError(t, model.DB.First(actor, actor.Id).Error)
	now := common.GetTimestamp()
	identity := service.AuthIdentity{UserID: actor.Id, SessionID: "token-grant-admin-session", SessionVersion: 1, UserAuthVersion: actor.AuthVersion}
	require.NoError(t, model.CreateUserSession(&model.UserSession{SID: identity.SessionID, UserID: actor.Id, Version: 1, UserAuthVersion: actor.AuthVersion, Status: model.UserSessionStatusActive, RefreshHash: "token-grant-refresh", LoginMethod: "password", LastActiveAt: now, ExpiresAt: now + 7200}))
	jwt, _, err := service.IssueAccessToken(identity)
	require.NoError(t, err)
	scopes := []string{"user:read", "user:quota_write", "user:group_ratios_write", "user:employee_assign", "billing:topup_reconcile", "group:read", "user:employee_read", "log:stats_read"}
	proof = issueAccessTokenGenerateProof(t, identity, now+7200, scopes...)
	body, err = common.Marshal(accessTokenCreateRequest{Name: "wecom-bot", Scopes: scopes, ExpiresAt: now + 7200})
	require.NoError(t, err)
	response = accessTokenRequest(router, "POST", "/api/user/access_tokens", jwt, proof, string(body))
	require.Contains(t, response.Body.String(), `"success":true`, response.Body.String())
	var issued struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &issued))
	require.NotEmpty(t, issued.Data.Token)
	response = accessTokenRequest(router, "GET", "/api/log/stat", issued.Data.Token, "", "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.True(t, decodeResp(t, response).Success, response.Body.String())
	response = accessTokenRequest(router, "GET", "/api/user/search?username=missing-customer", issued.Data.Token, "", "")
	require.Contains(t, response.Body.String(), `"success":true`, response.Body.String())
	response = accessTokenRequest(router, "GET", fmt.Sprintf("/api/user/%d", root.Id), issued.Data.Token, "", "")
	require.Equal(t, http.StatusForbidden, response.Code)
}

func TestAdminTokenUserAutomationWithoutSessionOrProof(t *testing.T) {
	actor := setupAdminTokenAutomation(t)
	db := model.DB
	require.NoError(t, db.AutoMigrate(&model.EmployeeProfile{}, &model.CustomerProfile{}))
	require.NoError(t, authz.SetUserPermissionsInTx(db, actor.Id, authz.PermissionsMap{
		authz.ResourceAdminAccessToken: {authz.ActionUse: true, authz.ActionUsersList: true, authz.ActionUsersQuota: true, authz.ActionUsersGroupRatios: true, authz.ActionUsersEmployee: true},
	}))
	oldGroups := ratio_setting.GetGroupRatioCopy()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	t.Cleanup(func() {
		raw, _ := common.Marshal(oldGroups)
		_ = ratio_setting.UpdateGroupRatioByJSONString(string(raw))
	})
	target := model.User{Username: "customer_a", DisplayName: "keep-name", Group: "default", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 100, UsedQuota: 20, GroupRatios: "{}", Password: "private-password", Email: "private@example.test", AffCode: "customer-a"}
	staff := model.User{Username: "staff_a", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "staff-a"}
	require.NoError(t, db.Create(&target).Error)
	require.NoError(t, db.Create(&staff).Error)
	employee := model.EmployeeProfile{UserId: staff.Id, Status: 1}
	require.NoError(t, db.Create(&employee).Error)
	scopes := []string{"user:read", "user:quota_write", "user:group_ratios_write", "user:employee_assign"}
	granted, err := service.NormalizeAccessTokenScopes(actor.Id, actor.Role, scopes)
	require.NoError(t, err, "all explicitly granted capabilities must be issuable")
	raw, _ := createScopedAccessToken(t, actor.Id, common.GetTimestamp()+3600, granted...)
	router := gin.New()
	router.Use(middleware.RequestId(), middleware.AccessTokenAudit())
	users := router.Group("/api/user", middleware.AdminAuth(), middleware.RequirePermission(authz.AdminMenuUsersView))
	users.GET("/", GetAllUsers)
	users.GET("/search", SearchUsers)
	users.GET("/:id", GetUser)
	users.POST("/manage", ManageUser)
	users.PUT("/", UpdateUser)
	users.POST("/", CreateUser)
	users.DELETE("/:id", DeleteUser)
	employees := router.Group("/api/admin/employee", middleware.AdminAuth(), middleware.RequirePermission(authz.AdminMenuEmployeesView))
	employees.POST("/:id/assign-customer", AdminAssignCustomerToEmployee)
	employees.DELETE("/:id/customer/:user_id", AdminUnassignCustomerFromEmployee)
	for _, query := range []string{"username=customer_a", fmt.Sprintf("id=%d", target.Id), "keyword=customer_a&page_size=20&after_id=0"} {
		response := accessTokenRequest(router, "GET", "/api/user/search?"+query, raw, "", "")
		require.Equal(t, 200, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), `"username":"customer_a"`)
		require.Contains(t, response.Body.String(), `"used_quota":20`)
		require.NotContains(t, response.Body.String(), "private")
	}
	for _, query := range []string{"", "p=1&page_size=20", "after_id=0&page_size=20", "username=customer_a", fmt.Sprintf("id=%d", target.Id)} {
		response := accessTokenRequest(router, "GET", "/api/user/?"+query, raw, "", "")
		require.Equal(t, http.StatusForbidden, response.Code, query)
		require.NotContains(t, response.Body.String(), `"username":"customer_a"`)
	}
	for _, query := range []string{"", "keyword=", "username=customer_a&username=staff_a", "username=customer_a&after_id=1", "id=0", "username=customer_a&role=100", "username=customer_a&p=2&page_size=20"} {
		response := accessTokenRequest(router, "GET", "/api/user/search?"+query, raw, "", "")
		require.Contains(t, response.Body.String(), `"success":false`, query)
	}
	for _, query := range []string{"username=customer", "username=" + url.QueryEscape("customer_%")} {
		response := accessTokenRequest(router, "GET", "/api/user/search?"+query, raw, "", "")
		require.Contains(t, response.Body.String(), `"items":[]`)
	}
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/user/manage", fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"add","value":50}`, target.Id)},
		{"POST", "/api/user/manage", fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"subtract","value":10}`, target.Id)},
		{"PUT", "/api/user/", fmt.Sprintf(`{"id":%d,"group_ratios":"{\"default\":0}"}`, target.Id)},
		{"POST", fmt.Sprintf("/api/admin/employee/%d/assign-customer", employee.Id), fmt.Sprintf(`{"user_id":%d}`, target.Id)},
	} {
		response := accessTokenRequest(router, tc.method, tc.path, raw, "", tc.body)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), `"success":true`, response.Body.String())
		require.NotContains(t, response.Body.String(), "private")
	}
	var stored model.User
	require.NoError(t, db.First(&stored, target.Id).Error)
	require.Equal(t, 140, stored.Quota)
	require.JSONEq(t, `{"default":0}`, stored.GroupRatios)
	require.Equal(t, staff.Id, stored.InviterId)
	require.Equal(t, target.DisplayName, stored.DisplayName)
	require.Equal(t, target.Password, stored.Password)

	for _, action := range []string{"disable", "enable", "promote", "demote", "delete"} {
		for _, body := range []string{
			fmt.Sprintf(`{"id":%d,"action":%q}`, target.Id, action),
			fmt.Sprintf(`{"id":%d,"action":%q,"mode":"add","value":1}`, target.Id, action),
		} {
			response := accessTokenRequest(router, "POST", "/api/user/manage", raw, "", body)
			require.Contains(t, response.Body.String(), `"success":false`, action)
			if strings.Contains(body, `"mode"`) {
				require.Equal(t, http.StatusForbidden, response.Code, action)
			}
			var unchanged model.User
			require.NoError(t, db.First(&unchanged, target.Id).Error, action)
			require.Equal(t, stored.Status, unchanged.Status, action)
			require.Equal(t, stored.Role, unchanged.Role, action)
			require.Equal(t, stored.Quota, unchanged.Quota, action)
		}
	}
	for _, body := range []string{
		fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"add","value":1,"role":100}`, target.Id),
		fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"override"}`, target.Id),
		fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"override","value":null}`, target.Id),
		fmt.Sprintf(`{"id":%d,"id":%d,"action":"add_quota","mode":"add","value":1}`, target.Id, staff.Id),
	} {
		response := accessTokenRequest(router, "POST", "/api/user/manage", raw, "", body)
		require.Contains(t, response.Body.String(), `"success":false`, response.Body.String())
	}
	for _, body := range []string{
		fmt.Sprintf(`{"id":%d,"group_ratios":"{}","username":"renamed"}`, target.Id),
		fmt.Sprintf(`{"id":%d,"group_ratios":"{\"default\":-1}"}`, target.Id),
		fmt.Sprintf(`{"id":%d,"group_ratios":"{\"default\":null}"}`, target.Id),
	} {
		response := accessTokenRequest(router, "PUT", "/api/user/", raw, "", body)
		require.Contains(t, response.Body.String(), `"success":false`)
	}
	for _, method := range []string{"GET", "DELETE"} {
		response := accessTokenRequest(router, method, fmt.Sprintf("/api/user/%d", target.Id), raw, "", "")
		require.Equal(t, http.StatusForbidden, response.Code)
	}
	response := accessTokenRequest(router, "DELETE", fmt.Sprintf("/api/admin/employee/%d/customer/%d", employee.Id, target.Id), raw, "", "")
	require.Contains(t, response.Body.String(), `"success":true`)
	writeOnly, _ := createScopedAccessToken(t, actor.Id, common.GetTimestamp()+3600, "user:quota_write")
	body := fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"override","value":0}`, target.Id)
	response = accessTokenRequest(router, "POST", "/api/user/manage", writeOnly, "", body)
	require.Contains(t, response.Body.String(), `"success":true`, "write-only tokens need no prior read or read scope")
	require.NoError(t, authz.SetUserPermissionsInTx(db, actor.Id, authz.PermissionsMap{authz.ResourceAdminAccessToken: {authz.ActionUsersQuota: false}}))
	response = accessTokenRequest(router, "POST", "/api/user/manage", writeOnly, "", body)
	require.Equal(t, http.StatusForbidden, response.Code, "revocation must take effect without reloading a local snapshot")
}
