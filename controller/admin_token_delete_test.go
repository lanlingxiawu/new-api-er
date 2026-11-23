package controller

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHardDeleteReloadsPermissionsBeforeUserIDReuse(t *testing.T) {
	_, identity, target := setupAdminUserTest(t)
	target.Role = common.RoleAdminUser
	require.NoError(t, model.DB.Model(target).Update("role", target.Role).Error)
	require.NoError(t, model.DB.AutoMigrate(&model.AuthzRole{}, &model.CasbinRule{}, &model.ChannelCreator{}, &model.ExternalIdentityClaim{}, &model.Token{}))
	require.NoError(t, authz.Init(model.DB))
	require.NoError(t, authz.SetUserPermissionsInTx(model.DB, target.Id, authz.PermissionsMap{
		authz.ResourceAdminAccessToken: {authz.ActionUse: true},
	}))
	require.NoError(t, authz.ReloadPolicy())
	permission := authz.Permission{Resource: authz.ResourceAdminAccessToken, Action: authz.ActionUse}
	require.True(t, authz.Can(target.Id, common.RoleAdminUser, permission))
	proof := issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: service.VerificationScopeAdminUserDelete, Context: []byte(fmt.Sprintf(`{"user_id":%d}`, target.Id))}, service.VerificationMethodPassword)
	response := adminUserRequest(http.MethodDelete, "/api/user/:id", "", proof, identity, common.RoleRootUser, gin.Params{{Key: "id", Value: fmt.Sprint(target.Id)}}, DeleteUser)
	require.Contains(t, response.Body.String(), `"success":true`, response.Body.String())

	reused := model.User{Id: target.Id, Username: "replacement-after-hard-delete", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, model.DB.Create(&reused).Error)
	assert.False(t, authz.Can(reused.Id, common.RoleAdminUser, permission), "the in-memory policy must not retain the deleted user's grant")
}
