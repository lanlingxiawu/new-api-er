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
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAdminTokenChannelHTTPRequiresCurrentGrantOwnershipAndProof(t *testing.T) {
	user, _ := setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.ChannelCreator{}, &model.Ability{}, &model.ChannelCostConfig{}))
	require.NoError(t, authz.SetUserPermissionsInTx(model.DB, user.Id, authz.PermissionsMap{authz.ResourceAdminAccessToken: {authz.ActionUse: true, authz.ActionChannelsOwned: true}}))
	raw, token := createScopedAccessToken(t, user.Id, common.GetTimestamp()+3600, "channel:read", "channel:write", "channel:operate")
	require.NoError(t, model.DB.Create(&model.TwoFA{UserId: user.Id, Secret: "fixture-only", IsEnabled: true}).Error)
	channels := []model.Channel{{Name: "own", Key: "private-key", Models: "model-a", Group: "default"}}
	others := []model.Channel{{Name: "other", Key: "other-key", Models: "model-b", Group: "default"}}
	require.NoError(t, model.BatchInsertChannels(channels, user.Id))
	require.NoError(t, model.BatchInsertChannels(others, user.Id+1))
	router := gin.New()
	router.Use(middleware.RequestId(), middleware.AccessTokenAudit())
	group := router.Group("/api/channel", middleware.AdminAuth(), middleware.RequirePermission(authz.AdminMenuChannelsView), AdminTokenChannelRequest)
	for _, route := range []struct {
		method, path string
		permission   authz.Permission
	}{
		{"GET", "/:id", authz.ChannelRead}, {"GET", "/models", authz.ChannelRead},
		{"POST", "/:id/status", authz.ChannelOperate}, {"POST", "/status/batch", authz.ChannelOperate},
		{"GET", "/test/:id", authz.ChannelOperate},
	} {
		middleware.DeclareAccessTokenPermissionRoute(route.method, "/api/channel"+route.path, route.permission)
		group.Handle(route.method, route.path, middleware.RequirePermission(route.permission), func(c *gin.Context) {
			require.Fail(t, "PAT request must be handled or rejected before the original handler")
		})
	}
	path := fmt.Sprintf("/api/channel/%d", channels[0].Id)
	response := accessTokenRequest(router, "GET", path, raw, "", "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"name":"own"`)
	require.NotContains(t, response.Body.String(), "private-key")
	response = accessTokenRequest(router, "GET", fmt.Sprintf("/api/channel/%d", others[0].Id), raw, "", "")
	require.Equal(t, http.StatusForbidden, response.Code)
	response = accessTokenRequest(router, "GET", "/api/channel/models", raw, "", "")
	require.Contains(t, response.Body.String(), `"items":["model-a"]`)
	require.NotContains(t, response.Body.String(), "model-b")
	response = accessTokenRequest(router, "GET", fmt.Sprintf("/api/channel/test/%d", channels[0].Id), raw, "", "")
	require.Equal(t, http.StatusForbidden, response.Code)
	response = accessTokenRequest(router, "POST", "/api/channel/status/batch", raw, "", fmt.Sprintf(`{"ids":[%d,%d],"status":2}`, channels[0].Id, others[0].Id))
	require.Equal(t, http.StatusForbidden, response.Code)
	response = accessTokenRequest(router, "POST", path+"/status", raw, "", `{"status":2}`)
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Contains(t, response.Body.String(), "SECURITY_PROOF_REQUIRED")
	identity := service.AuthIdentity{UserID: user.Id, SessionID: model.AccessTokenSessionID(token.Id), UserAuthVersion: 1, SessionVersion: model.AccessTokenSessionVersion}
	context, err := common.Marshal(service.AdminChannelOperation{Action: "POST /api/channel/:id/status", ChannelID: channels[0].Id, Query: map[string]string{}, Body: []byte(`{"status":2}`)})
	require.NoError(t, err)
	binding, err := service.BindAuthorizedVerificationOperation(identity, service.VerificationOperation{Scope: service.VerificationScopeAdminChannelChange, Context: context})
	require.NoError(t, err)
	proof, _, err := service.IssueSecurityProof(identity, service.VerificationMethodTwoFA, binding)
	require.NoError(t, err)
	response = accessTokenRequest(router, "POST", path+"/status", raw, proof, `{"status":2}`)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"success":true`)
	var stored model.Channel
	require.NoError(t, model.DB.First(&stored, channels[0].Id).Error)
	require.Equal(t, common.ChannelStatusManuallyDisabled, stored.Status)
	response = accessTokenRequest(router, "POST", path+"/status", raw, proof, `{"status":2}`)
	require.Equal(t, http.StatusForbidden, response.Code)
	require.NoError(t, authz.SetUserPermissionsInTx(model.DB, user.Id, authz.PermissionsMap{authz.ResourceAdminAccessToken: {authz.ActionUse: false}}))
	response = accessTokenRequest(router, "GET", path, raw, "", "")
	require.Equal(t, http.StatusForbidden, response.Code, "revocation is effective without reloading the node's Casbin snapshot")
}

func TestAdminTokenUserListProjection(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	old := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = old })
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}))
	for i, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		require.NoError(t, db.Create(&model.User{Username: strings.Repeat("u", i+1), Role: role, Email: "private@example.test", Password: "secret", AffCode: strings.Repeat("a", i+1)}).Error)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("GET", "/api/user/search?keyword=u&page_size=1", nil)
	c.Set("role", common.RoleAdminUser)
	listAdminTokenUsers(c)
	require.Equal(t, 200, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"username":"u"`)
	for _, secret := range []string{"private@example.test", "secret", `"email"`, `"username":"uu"`} {
		require.NotContains(t, recorder.Body.String(), secret)
	}
	recorder = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("GET", "/api/user/?exclude_admins=false", nil)
	listAdminTokenUsers(c)
	require.Contains(t, recorder.Body.String(), `"success":false`)
}

func TestAdminTokenChannelListCannotReturnOtherOwnersOrCredentials(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	old := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = old })
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelCreator{}, &model.Ability{}))
	for _, owner := range []int{41, 42, 0} {
		require.NoError(t, model.BatchInsertChannels([]model.Channel{{Name: strings.Repeat("x", owner+1), Key: "shortkey", Setting: common.GetPointer(`{"proxy":"http://user:password@proxy"}`), HeaderOverride: common.GetPointer(`{"Authorization":"nested-secret"}`)}}, owner))
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("GET", "/api/channel/", nil)
	c.Set("id", 42)
	readAdminTokenChannels(c, service.AdminChannelOperation{Action: "GET /api/channel/"}, nil)
	require.Equal(t, 200, rec.Code)
	var response struct {
		Data struct {
			Items []adminTokenChannelItem `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &response))
	require.Len(t, response.Data.Items, 1)
	require.Equal(t, strings.Repeat("x", 43), response.Data.Items[0].Name)
	for _, secret := range []string{"shortkey", "password", "nested-secret", "header_override", "setting", "base_url"} {
		require.NotContains(t, rec.Body.String(), secret)
	}
}
