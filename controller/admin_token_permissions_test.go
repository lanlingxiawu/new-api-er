package controller

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAdminTokenPermissionCommitReportsReloadFailure(t *testing.T) {
	_, identity, target := setupAdminUserTest(t)
	require.NoError(t, model.DB.Model(target).Update("role", common.RoleAdminUser).Error)
	require.NoError(t, model.DB.AutoMigrate(&model.AuthzRole{}, &model.CasbinRule{}))
	require.NoError(t, authz.Init(model.DB))
	proof := issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: service.VerificationScopeAdminUserUpdate, Context: []byte(fmt.Sprintf(`{"user_id":%d}`, target.Id))}, service.VerificationMethodPassword)
	require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register("test:permission_reload_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "casbin_rules" || tx.Statement.Table == "casbin_rule" {
			if _, filtered := tx.Statement.Clauses["WHERE"]; !filtered {
				tx.AddError(errors.New("injected reload failure"))
			}
		}
	}))
	t.Cleanup(func() { model.DB.Callback().Query().Remove("test:permission_reload_failure") })
	body, err := common.Marshal(auditUserBody(target, map[string]any{"admin_permissions": map[string]map[string]bool{authz.ResourceAdminAccessToken: {authz.ActionUse: true}}}))
	require.NoError(t, err)
	response := adminUserRequest("PUT", "/api/user/", string(body), proof, identity, common.RoleRootUser, nil, UpdateUser)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"committed":true`)
	require.NotContains(t, response.Body.String(), "injected reload failure")
	var row model.CasbinRule
	require.NoError(t, model.DB.Where("v0 = ? AND v1 = ? AND v2 = ?", authz.UserSubject(target.Id), authz.ResourceAdminAccessToken, authz.ActionUse).First(&row).Error)
	require.Equal(t, authz.EffectAllow, row.V3)

	response = adminUserRequest(http.MethodGet, "/api/user/:id", "", "", identity, common.RoleRootUser, gin.Params{{Key: "id", Value: fmt.Sprint(target.Id)}}, GetUser)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var actual struct {
		Data struct {
			AdminPermissions map[string]map[string]bool `json:"admin_permissions"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &actual))
	require.True(t, actual.Data.AdminPermissions[authz.ResourceAdminAccessToken][authz.ActionUse], "the readback must use the committed primary-database grant, not the stale Casbin snapshot")
}
