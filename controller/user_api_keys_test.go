package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAdminUserAPIKeyTest(t *testing.T) (*model.User, service.AuthIdentity, *model.User) {
	t.Helper()
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	operator, identity, target := setupAdminUserTest(t)
	model.InitColumnNames()
	t.Cleanup(func() {
		common.SetDatabaseTypes(previousMain, previousLog)
		model.InitColumnNames()
	})
	require.NoError(t, model.DB.AutoMigrate(&model.Token{}))
	return operator, identity, target
}

func TestAdminClearUserAPIKeysProofAndIsolation(t *testing.T) {
	operator, identity, target := setupAdminUserAPIKeyTest(t)
	key := &model.Token{UserId: target.Id, Key: "clear-target-secret", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	other := &model.Token{UserId: operator.Id, Key: "clear-other-secret", Status: common.TokenStatusEnabled}
	require.NoError(t, model.DB.Create(key).Error)
	require.NoError(t, model.DB.Create(other).Error)
	_, err := model.ValidateUserToken(key.Key)
	require.NoError(t, err)
	params := gin.Params{{Key: "id", Value: fmt.Sprint(target.Id)}}
	op := service.VerificationOperation{Scope: service.VerificationScopeAdminUserAPIKeysDeleteAll, Context: []byte(fmt.Sprintf(`{"user_id":%d}`, target.Id))}
	for _, test := range []struct{ name, proof, code string }{
		{"missing", "", "SECURITY_PROOF_REQUIRED"},
		{"scope", issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: service.VerificationScopeAdminUserDelete, Context: op.Context}, service.VerificationMethodPassword), "SECURITY_PROOF_SCOPE_MISMATCH"},
		{"target", issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: op.Scope, Context: []byte(fmt.Sprintf(`{"user_id":%d}`, operator.Id))}, service.VerificationMethodPassword), "SECURITY_PROOF_CONTEXT_MISMATCH"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := adminUserRequest(http.MethodDelete, "/api/user/2/tokens", "", test.proof, identity, common.RoleRootUser, params, AdminClearUserAPIKeys)
			assert.Equal(t, http.StatusForbidden, response.Code)
			assert.Contains(t, response.Body.String(), test.code)
			require.NoError(t, model.DB.First(&model.Token{}, key.Id).Error)
		})
	}
	expiredProof := issueSecurityEnrollmentProof(t, identity, op, service.VerificationMethodPassword)
	require.NoError(t, model.DB.Model(&model.AuthFlow{}).Where("user_id = ? AND purpose = ?", operator.Id, model.AuthFlowPurposeSecurityProof).Update("expires_at", time.Now().Add(-time.Minute)).Error)
	expiredResponse := adminUserRequest(http.MethodDelete, "/api/user/2/tokens", "", expiredProof, identity, common.RoleRootUser, params, AdminClearUserAPIKeys)
	assert.Contains(t, expiredResponse.Body.String(), "SECURITY_PROOF_EXPIRED")
	require.NoError(t, model.DB.First(&model.Token{}, key.Id).Error)
	proof := issueSecurityEnrollmentProof(t, identity, op, service.VerificationMethodPassword)
	response := adminUserRequest(http.MethodDelete, "/api/user/2/tokens", "", proof, identity, common.RoleRootUser, params, AdminClearUserAPIKeys)
	var result struct {
		Success bool
		Data    service.UserAPIKeyDeletionResult
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, result.Success, response.Body.String())
	assert.EqualValues(t, 1, result.Data.DeletedCount)
	assert.True(t, result.Data.Completed)
	_, err = model.ValidateUserToken(key.Key)
	require.Error(t, err)
	require.NoError(t, model.DB.First(&model.Token{}, other.Id).Error)
	stored, err := model.GetUserById(target.Id, false)
	require.NoError(t, err)
	assert.Equal(t, target.Quota, stored.Quota)
	response = adminUserRequest(http.MethodDelete, "/api/user/2/tokens", "", proof, identity, common.RoleRootUser, params, AdminClearUserAPIKeys)
	assert.Contains(t, response.Body.String(), "SECURITY_PROOF_CONSUMED")
	var audit model.AuditLog
	require.NoError(t, model.LOG_DB.Where("action = ?", "user.api_keys.delete_all").First(&audit).Error)
	auditJSON, err := common.Marshal(audit.Other)
	require.NoError(t, err)
	assert.NotContains(t, string(auditJSON), key.Key)
	assert.NotContains(t, string(auditJSON), proof)
}

func TestAdminClearUserAPIKeysRoleHierarchy(t *testing.T) {
	operator, identity, target := setupAdminUserTest(t)
	for _, role := range []int{common.RoleAdminUser, common.RoleRootUser} {
		require.NoError(t, model.DB.Model(operator).Update("role", common.RoleAdminUser).Error)
		require.NoError(t, model.DB.Model(target).Update("role", role).Error)
		response := adminUserRequest(http.MethodDelete, "/api/user/2/tokens", "", "", identity, common.RoleAdminUser, gin.Params{{Key: "id", Value: fmt.Sprint(target.Id)}}, AdminClearUserAPIKeys)
		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.NotContains(t, response.Body.String(), "SECURITY_PROOF_REQUIRED")
	}
}

func TestAdminClearUserAPIKeysPartialFailureAndFreshProof(t *testing.T) {
	_, identity, target := setupAdminUserAPIKeyTest(t)
	tokens := make([]model.Token, 101)
	for i := range tokens {
		tokens[i] = model.Token{UserId: target.Id, Key: fmt.Sprintf("partial-key-%d", i)}
	}
	require.NoError(t, model.DB.Create(&tokens).Error)
	const callback = "test:partial_key_delete"
	batches := 0
	require.NoError(t, model.DB.Callback().Delete().Before("gorm:delete").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			batches++
			if batches == 2 {
				_ = tx.AddError(errors.New("injected failure"))
			}
		}
	}))
	t.Cleanup(func() { _ = model.DB.Callback().Delete().Remove(callback) })
	op := service.VerificationOperation{Scope: service.VerificationScopeAdminUserAPIKeysDeleteAll, Context: []byte(fmt.Sprintf(`{"user_id":%d}`, target.Id))}
	proof := issueSecurityEnrollmentProof(t, identity, op, service.VerificationMethodPassword)
	params := gin.Params{{Key: "id", Value: fmt.Sprint(target.Id)}}
	response := adminUserRequest(http.MethodDelete, "/api/user/2/tokens", "", proof, identity, common.RoleRootUser, params, AdminClearUserAPIKeys)
	var result struct {
		Success bool
		Data    service.UserAPIKeyDeletionResult
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	assert.False(t, result.Success)
	assert.False(t, result.Data.Completed)
	assert.EqualValues(t, 100, result.Data.DeletedCount)
	assert.NotContains(t, response.Body.String(), "injected failure")
	response = adminUserRequest(http.MethodDelete, "/api/user/2/tokens", "", proof, identity, common.RoleRootUser, params, AdminClearUserAPIKeys)
	assert.Contains(t, response.Body.String(), "SECURITY_PROOF_CONSUMED")
	proof = issueSecurityEnrollmentProof(t, identity, op, service.VerificationMethodPassword)
	response = adminUserRequest(http.MethodDelete, "/api/user/2/tokens", "", proof, identity, common.RoleRootUser, params, AdminClearUserAPIKeys)
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	assert.True(t, result.Success, response.Body.String())
	assert.EqualValues(t, 1, result.Data.DeletedCount)
	assert.True(t, result.Data.Completed)
}

func TestAdminClearUserAPIKeysRechecksRolesBetweenBatches(t *testing.T) {
	operator, _, target := setupAdminUserAPIKeyTest(t)
	tokens := make([]model.Token, 101)
	for i := range tokens {
		tokens[i] = model.Token{UserId: target.Id, Key: fmt.Sprintf("role-key-%d", i)}
	}
	require.NoError(t, model.DB.Create(&tokens).Error)
	const callback = "test:key_delete_role_change"
	require.NoError(t, model.DB.Callback().Delete().After("gorm:commit_or_rollback_transaction").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			require.NoError(t, model.DB.Model(target).Update("role", common.RoleRootUser).Error)
		}
	}))
	t.Cleanup(func() { _ = model.DB.Callback().Delete().Remove(callback) })
	result, err := service.ClearUserAPIKeys(context.Background(), operator.Id, target.Id)
	require.ErrorIs(t, err, service.ErrVerificationForbidden)
	assert.EqualValues(t, 100, result.DeletedCount)
	assert.False(t, result.Completed)
	var count int64
	require.NoError(t, model.DB.Model(&model.Token{}).Where("user_id = ?", target.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestAdminClearUserAPIKeysSessionOnlyAndEmptyDisabledUser(t *testing.T) {
	operator, identity, target := setupAdminUserAPIKeyTest(t)
	require.NoError(t, model.DB.Model(target).Update("status", common.UserStatusDisabled).Error)
	result, err := service.ClearUserAPIKeys(context.Background(), operator.Id, target.Id)
	require.NoError(t, err)
	assert.Zero(t, result.DeletedCount)
	assert.True(t, result.Completed)
	identity.SessionID = "pat:1"
	response := adminUserRequest(http.MethodDelete, "/api/user/2/tokens", "", "", identity, common.RoleRootUser, gin.Params{{Key: "id", Value: fmt.Sprint(target.Id)}}, AdminClearUserAPIKeys)
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.NotContains(t, response.Body.String(), "SECURITY_PROOF_REQUIRED")
}
