package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUpdateUserCannotRestoreGrantAfterConcurrentDemote(t *testing.T) {
	_, identity, target := setupAdminUserTest(t)
	target.Role = common.RoleAdminUser
	require.NoError(t, model.DB.Model(target).Update("role", target.Role).Error)
	require.NoError(t, model.DB.AutoMigrate(&model.AuthzRole{}, &model.CasbinRule{}))
	require.NoError(t, authz.Init(model.DB))
	body, err := common.Marshal(auditUserBody(target, map[string]any{"admin_permissions": map[string]map[string]bool{
		authz.ResourceAdminAccessToken: {authz.ActionUse: true},
	}}))
	require.NoError(t, err)
	updateProof := issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: service.VerificationScopeAdminUserUpdate, Context: []byte(fmt.Sprintf(`{"user_id":%d}`, target.Id))}, service.VerificationMethodPassword)
	demoteProof := issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: service.VerificationScopeAdminUserManage, Context: []byte(fmt.Sprintf(`{"user_id":%d,"action":"demote"}`, target.Id))}, service.VerificationMethodPassword)

	originRead := make(chan struct{})
	resume := make(chan struct{})
	var paused, released atomic.Bool
	release := func() {
		if released.CompareAndSwap(false, true) {
			close(resume)
		}
	}
	t.Cleanup(release)
	require.NoError(t, model.DB.Callback().Query().After("gorm:query").Register("test:permission_demote_barrier", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" && paused.CompareAndSwap(false, true) {
			close(originRead)
			<-resume
		}
	}))
	t.Cleanup(func() { model.DB.Callback().Query().Remove("test:permission_demote_barrier") })
	responses := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		responses <- adminUserRequest(http.MethodPut, "/api/user/", string(body), updateProof, identity, common.RoleRootUser, nil, UpdateUser)
	}()
	select {
	case <-originRead:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "timed out waiting for the permission edit to read the target")
	}
	demoteResponse := adminUserRequest(http.MethodPost, "/api/user/manage", fmt.Sprintf(`{"id":%d,"action":"demote"}`, target.Id), demoteProof, identity, common.RoleRootUser, nil, ManageUser)
	require.Contains(t, demoteResponse.Body.String(), `"success":true`, demoteResponse.Body.String())
	release()
	select {
	case updateResponse := <-responses:
		require.Contains(t, updateResponse.Body.String(), `"success":true`, updateResponse.Body.String())
	case <-time.After(5 * time.Second):
		require.FailNow(t, "timed out waiting for the permission edit to finish")
	}

	var grants int64
	require.NoError(t, model.DB.Model(&model.CasbinRule{}).Where("v0 = ? AND v1 = ?", authz.UserSubject(target.Id), authz.ResourceAdminAccessToken).Count(&grants).Error)
	assert.Zero(t, grants, "a permission edit based on the pre-demote role must not restore management-token access")
	var stored model.User
	require.NoError(t, model.DB.First(&stored, target.Id).Error)
	assert.Equal(t, common.RoleCommonUser, stored.Role)
}
