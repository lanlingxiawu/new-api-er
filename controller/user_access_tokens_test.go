package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func createClearAccessTokenFixture(t *testing.T, userID int, raw string) *model.UserAccessToken {
	t.Helper()
	token := &model.UserAccessToken{UserId: userID, Name: "clear fixture", TokenHash: model.AccessTokenFingerprint(raw), CreatedAt: time.Now().Unix()}
	require.NoError(t, token.SetScopes([]string{"profile:read"}))
	require.NoError(t, model.DB.Create(token).Error)
	return token
}

func TestAdminClearUserAccessTokensProofAndAuthentication(t *testing.T) {
	operator, identity, target := setupAdminUserAPIKeyTest(t)
	const raw, legacy = "nap_clear-target-secret", "clear-legacy-secret"
	token := createClearAccessTokenFixture(t, target.Id, raw)
	other := createClearAccessTokenFixture(t, operator.Id, "nap_clear-other-secret")
	require.NoError(t, model.DB.Model(target).Updates(map[string]any{"access_token": legacy, "access_token_created_at": int64(123)}).Error)
	key := &model.Token{UserId: target.Id, Key: "clear-preserved-model-key", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, model.DB.Create(key).Error)
	bundle, err := service.CreateLoginSession(target.Id, "password", "127.0.0.1", "clear-test")
	require.NoError(t, err)
	targetIdentity, err := service.ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)
	patIdentity := model.AuthSessionIdentity{UserID: target.Id, SessionID: model.AccessTokenSessionID(token.Id), UserAuthVersion: 1, SessionVersion: model.AccessTokenSessionVersion}
	require.NoError(t, model.ValidateAccessTokenIdentity(patIdentity))
	router := gin.New()
	router.GET("/api/user/self", middleware.UserAuth(), func(c *gin.Context) { common.ApiSuccess(c, nil) })
	request := func(credential string) int {
		r := httptest.NewRequest(http.MethodGet, "/api/user/self", nil)
		r.Header.Set("Authorization", "Bearer "+credential)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w.Code
	}
	for _, credential := range []string{raw, legacy, bundle.AccessToken} {
		require.Equal(t, http.StatusOK, request(credential))
	}
	params := gin.Params{{Key: "id", Value: fmt.Sprint(target.Id)}}
	op := service.VerificationOperation{Scope: service.VerificationScopeAdminUserAccessTokensDeleteAll, Context: []byte(fmt.Sprintf(`{"user_id":%d}`, target.Id))}
	for _, test := range []struct{ name, proof, code string }{
		{"missing", "", "SECURITY_PROOF_REQUIRED"},
		{"scope", issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: service.VerificationScopeAdminUserAPIKeysDeleteAll, Context: op.Context}, service.VerificationMethodPassword), "SECURITY_PROOF_SCOPE_MISMATCH"},
		{"target", issueSecurityEnrollmentProof(t, identity, service.VerificationOperation{Scope: op.Scope, Context: []byte(fmt.Sprintf(`{"user_id":%d}`, operator.Id))}, service.VerificationMethodPassword), "SECURITY_PROOF_CONTEXT_MISMATCH"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := adminUserRequest(http.MethodDelete, "/api/user/2/access_tokens", "", test.proof, identity, common.RoleRootUser, params, AdminClearUserAccessTokens)
			assert.Equal(t, http.StatusForbidden, response.Code)
			assert.Contains(t, response.Body.String(), test.code)
			require.NoError(t, model.DB.First(&model.UserAccessToken{}, token.Id).Error)
		})
	}
	expiredProof := issueSecurityEnrollmentProof(t, identity, op, service.VerificationMethodPassword)
	require.NoError(t, model.DB.Model(&model.AuthFlow{}).Where("user_id = ? AND purpose = ?", operator.Id, model.AuthFlowPurposeSecurityProof).Update("expires_at", time.Now().Add(-time.Minute)).Error)
	response := adminUserRequest(http.MethodDelete, "/api/user/2/access_tokens", "", expiredProof, identity, common.RoleRootUser, params, AdminClearUserAccessTokens)
	assert.Contains(t, response.Body.String(), "SECURITY_PROOF_EXPIRED")
	proof := issueSecurityEnrollmentProof(t, identity, op, service.VerificationMethodPassword)
	response = adminUserRequest(http.MethodDelete, "/api/user/2/access_tokens", "", proof, identity, common.RoleRootUser, params, AdminClearUserAccessTokens)
	var result struct {
		Success bool
		Data    model.UserAccessTokenClearResult
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, result.Success, response.Body.String())
	assert.EqualValues(t, 2, result.Data.RevokedCount)
	for _, credential := range []string{raw, legacy} {
		assert.Equal(t, http.StatusUnauthorized, request(credential))
	}
	assert.Equal(t, http.StatusOK, request(bundle.AccessToken))
	_, _, err = service.ValidateLoginSession(targetIdentity)
	require.NoError(t, err)
	_, err = model.ValidateUserToken(key.Key)
	require.NoError(t, err)
	require.NoError(t, model.DB.First(&model.UserAccessToken{}, other.Id).Error)
	var stored model.User
	require.NoError(t, model.DB.First(&stored, target.Id).Error)
	assert.Nil(t, stored.AccessToken)
	assert.Nil(t, stored.AccessTokenCreatedAt)
	assert.Equal(t, target.AuthVersion, stored.AuthVersion)
	assert.Equal(t, target.Quota, stored.Quota)
	assert.ErrorIs(t, model.ValidateAccessTokenIdentity(patIdentity), model.ErrUserSessionInactive)
	assert.ErrorIs(t, model.DB.Transaction(func(tx *gorm.DB) error { return model.ValidateAuthSessionWithTx(tx, patIdentity) }), model.ErrUserSessionInactive)
	require.NoError(t, model.TouchUserAccessToken(token.Id, "127.0.0.1", time.Now().Unix()))
	_, err = model.UpdateUserAccessToken(target.Id, token.Id, "late rename", []string{"profile:read"})
	assert.ErrorIs(t, err, model.ErrAccessTokenNotFound)
	assert.Equal(t, http.StatusUnauthorized, request(raw))
	response = adminUserRequest(http.MethodDelete, "/api/user/2/access_tokens", "", proof, identity, common.RoleRootUser, params, AdminClearUserAccessTokens)
	assert.Contains(t, response.Body.String(), "SECURITY_PROOF_CONSUMED")
	var audit model.AuditLog
	require.NoError(t, model.LOG_DB.Where("action = ?", "user.access_tokens.delete_all").First(&audit).Error)
	auditJSON, err := common.Marshal(audit.Other)
	require.NoError(t, err)
	for _, secret := range []string{raw, legacy, token.TokenHash, proof} {
		assert.NotContains(t, string(auditJSON), secret)
	}
	assert.Contains(t, string(auditJSON), `"revoked_count":2`)
}

func TestUserAccessTokensClearCountsAndRollback(t *testing.T) {
	for _, count := range []int{0, 1, 20, 27} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			operator, _, target := setupAdminUserTest(t)
			for i := range count {
				token := createClearAccessTokenFixture(t, target.Id, fmt.Sprintf("nap_count_%d", i))
				require.NoError(t, model.DB.Model(token).Update("expires_at", int64(1)).Error)
			}
			require.NoError(t, model.DB.Model(target).Update("status", common.UserStatusDisabled).Error)
			result, err := service.ClearUserAccessTokens(context.Background(), operator.Id, target.Id)
			require.NoError(t, err)
			assert.EqualValues(t, count, result.RevokedCount)
			assert.False(t, result.LegacyRevoked)
			result, err = service.ClearUserAccessTokens(context.Background(), operator.Id, target.Id)
			require.NoError(t, err)
			assert.Zero(t, result.RevokedCount)
		})
	}
	for _, failure := range []string{"delete", "legacy update"} {
		t.Run(failure, func(t *testing.T) {
			operator, identity, target := setupAdminUserTest(t)
			token := createClearAccessTokenFixture(t, target.Id, "nap_rollback")
			require.NoError(t, model.DB.Model(target).Update("access_token", "legacy-rollback").Error)
			const callback = "test:access_tokens_clear_failure"
			injected := errors.New("injected sensitive database detail")
			if failure == "delete" {
				require.NoError(t, model.DB.Callback().Delete().Before("gorm:delete").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "user_access_tokens" {
						_ = tx.AddError(injected)
					}
				}))
			} else {
				require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "users" {
						_ = tx.AddError(injected)
					}
				}))
			}
			t.Cleanup(func() {
				_ = model.DB.Callback().Delete().Remove(callback)
				_ = model.DB.Callback().Update().Remove(callback)
			})
			op := service.VerificationOperation{Scope: service.VerificationScopeAdminUserAccessTokensDeleteAll, Context: []byte(fmt.Sprintf(`{"user_id":%d}`, target.Id))}
			proof := issueSecurityEnrollmentProof(t, identity, op, service.VerificationMethodPassword)
			params := gin.Params{{Key: "id", Value: fmt.Sprint(target.Id)}}
			response := adminUserRequest(http.MethodDelete, "/api/user/2/access_tokens", "", proof, identity, common.RoleRootUser, params, AdminClearUserAccessTokens)
			assert.Contains(t, response.Body.String(), `"success":false`)
			assert.NotContains(t, response.Body.String(), injected.Error())
			require.NoError(t, model.DB.First(&model.UserAccessToken{}, token.Id).Error)
			var stored model.User
			require.NoError(t, model.DB.First(&stored, target.Id).Error)
			assert.Equal(t, "legacy-rollback", strings.TrimRight(stored.GetAccessToken(), " "))
			response = adminUserRequest(http.MethodDelete, "/api/user/2/access_tokens", "", proof, identity, common.RoleRootUser, params, AdminClearUserAccessTokens)
			assert.Contains(t, response.Body.String(), "SECURITY_PROOF_CONSUMED")
			_ = model.DB.Callback().Delete().Remove(callback)
			_ = model.DB.Callback().Update().Remove(callback)
			result, err := service.ClearUserAccessTokens(context.Background(), operator.Id, target.Id)
			require.NoError(t, err)
			assert.EqualValues(t, 2, result.RevokedCount)
			assert.True(t, result.LegacyRevoked)
		})
	}
}

func TestUserAccessTokensClearAuthorizationAndCancellation(t *testing.T) {
	operator, identity, target := setupAdminUserTest(t)
	token := createClearAccessTokenFixture(t, target.Id, "nap_permission")
	for _, test := range []struct{ operatorRole, targetRole, status int }{
		{common.RoleCommonUser, common.RoleCommonUser, common.UserStatusEnabled},
		{common.RoleAdminUser, common.RoleAdminUser, common.UserStatusEnabled},
		{common.RoleAdminUser, common.RoleRootUser, common.UserStatusEnabled},
		{common.RoleRootUser, common.RoleCommonUser, common.UserStatusDisabled},
	} {
		require.NoError(t, model.DB.Model(operator).Updates(map[string]any{"role": test.operatorRole, "status": test.status}).Error)
		require.NoError(t, model.DB.Model(target).Update("role", test.targetRole).Error)
		_, err := service.ClearUserAccessTokens(context.Background(), operator.Id, target.Id)
		assert.ErrorIs(t, err, service.ErrVerificationForbidden)
		response := adminUserRequest(http.MethodDelete, "/api/user/2/access_tokens", "", "", identity, common.RoleRootUser, gin.Params{{Key: "id", Value: fmt.Sprint(target.Id)}}, AdminClearUserAccessTokens)
		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.NotContains(t, response.Body.String(), "SECURITY_PROOF_REQUIRED")
	}
	require.NoError(t, model.DB.Model(operator).Updates(map[string]any{"role": common.RoleRootUser, "status": common.UserStatusEnabled}).Error)
	_, err := service.ClearUserAccessTokens(context.Background(), operator.Id, operator.Id)
	assert.ErrorIs(t, err, service.ErrVerificationForbidden)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.ClearUserAccessTokens(ctx, operator.Id, target.Id)
	assert.ErrorIs(t, err, context.Canceled)
	identity.SessionID = "pat:1"
	response := adminUserRequest(http.MethodDelete, "/api/user/2/access_tokens", "", "", identity, common.RoleRootUser, gin.Params{{Key: "id", Value: fmt.Sprint(target.Id)}}, AdminClearUserAccessTokens)
	assert.Equal(t, http.StatusForbidden, response.Code)
	require.NoError(t, model.DB.First(&model.UserAccessToken{}, token.Id).Error)
	require.NoError(t, model.DB.Delete(target).Error)
	_, err = service.ClearUserAccessTokens(context.Background(), operator.Id, target.Id)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestUserAccessTokensClearConcurrentCreateAndClear(t *testing.T) {
	operator, _, target := setupAdminUserTest(t)
	createClearAccessTokenFixture(t, target.Id, "nap_before_clear")
	deleting, proceed := make(chan struct{}), make(chan struct{})
	const callback = "test:access_tokens_clear_create"
	require.NoError(t, model.DB.Callback().Delete().After("gorm:delete").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "user_access_tokens" {
			close(deleting)
			<-proceed
		}
	}))
	t.Cleanup(func() { _ = model.DB.Callback().Delete().Remove(callback) })
	cleared := make(chan error, 1)
	go func() {
		result, err := service.ClearUserAccessTokens(context.Background(), operator.Id, target.Id)
		if err == nil && result.RevokedCount != 1 {
			err = fmt.Errorf("revoked %d, want 1", result.RevokedCount)
		}
		cleared <- err
	}()
	select {
	case <-deleting:
	case <-time.After(10 * time.Second):
		t.Fatal("clear did not enter transaction")
	}
	created := make(chan error, 1)
	newToken := &model.UserAccessToken{Name: "after clear", TokenHash: model.AccessTokenFingerprint("nap_after_clear")}
	go func() { created <- model.CreateUserAccessToken(target.Id, newToken, 20) }()
	close(proceed)
	require.NoError(t, <-cleared)
	require.NoError(t, <-created)
	_ = model.DB.Callback().Delete().Remove(callback)
	require.NoError(t, model.DB.First(&model.UserAccessToken{}, newToken.Id).Error)
	var wg sync.WaitGroup
	results := make(chan model.UserAccessTokenClearResult, 2)
	errorsCh := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			result, err := service.ClearUserAccessTokens(context.Background(), operator.Id, target.Id)
			results <- result
			errorsCh <- err
		})
	}
	wg.Wait()
	require.NoError(t, <-errorsCh)
	require.NoError(t, <-errorsCh)
	assert.EqualValues(t, 1, (<-results).RevokedCount+(<-results).RevokedCount)
}

func TestAdminClearUserAccessTokensRechecksChangedRole(t *testing.T) {
	operator, identity, target := setupAdminUserTest(t)
	token := createClearAccessTokenFixture(t, target.Id, "nap_role_changed")
	op := service.VerificationOperation{Scope: service.VerificationScopeAdminUserAccessTokensDeleteAll, Context: []byte(fmt.Sprintf(`{"user_id":%d}`, target.Id))}
	proof := issueSecurityEnrollmentProof(t, identity, op, service.VerificationMethodPassword)
	const callback = "test:access_tokens_target_role_changed"
	changed := false
	require.NoError(t, model.DB.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		user, ok := tx.Statement.Dest.(*model.User)
		if ok && user.Id == target.Id && !changed {
			changed = true
			require.NoError(t, model.DB.Model(target).Update("role", operator.Role).Error)
		}
	}))
	t.Cleanup(func() { _ = model.DB.Callback().Query().Remove(callback) })
	response := adminUserRequest(http.MethodDelete, "/api/user/2/access_tokens", "", proof, identity, common.RoleRootUser, gin.Params{{Key: "id", Value: fmt.Sprint(target.Id)}}, AdminClearUserAccessTokens)
	assert.True(t, changed)
	assert.Equal(t, http.StatusForbidden, response.Code)
	require.NoError(t, model.DB.First(&model.UserAccessToken{}, token.Id).Error)
}

func TestUserAccessTokensClearLegacyOnly(t *testing.T) {
	operator, _, target := setupAdminUserTest(t)
	require.NoError(t, model.DB.Model(target).Updates(map[string]any{"access_token": "legacy-only", "access_token_created_at": int64(100)}).Error)
	// Even retired legacy credentials must be removed from persistent storage.
	require.NoError(t, model.DB.Model(&model.Option{}).Where(&model.Option{Key: "LegacyAccessTokenRetireAt"}).Update("value", "1").Error)
	require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
	result, err := service.ClearUserAccessTokens(context.Background(), operator.Id, target.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.RevokedCount)
	assert.True(t, result.LegacyRevoked)
	var stored model.User
	require.NoError(t, model.DB.First(&stored, target.Id).Error)
	assert.Nil(t, stored.AccessToken)
	assert.Nil(t, stored.AccessTokenCreatedAt)
}

func TestUserAccessTokensClearStorageIndex(t *testing.T) {
	_, _, _ = setupAdminUserTest(t)
	tokens := make([]model.UserAccessToken, 10000)
	for i := range tokens {
		tokens[i] = model.UserAccessToken{UserId: i/20 + 1000, TokenHash: model.AccessTokenFingerprint(fmt.Sprintf("index-pat-%d", i)), Scopes: "[]"}
	}
	require.NoError(t, model.DB.CreateInBatches(tokens, 250).Error)
	dialect := model.DB.Dialector.Name()
	if dialect == "mysql" {
		require.NoError(t, model.DB.Exec("ANALYZE TABLE user_access_tokens").Error)
	} else {
		require.NoError(t, model.DB.Exec("ANALYZE user_access_tokens").Error)
	}
	query := model.DB.ToSQL(func(tx *gorm.DB) *gorm.DB { return tx.Where("user_id = ?", 1250).Delete(&model.UserAccessToken{}) })
	prefix := "EXPLAIN "
	if dialect == "mysql" {
		prefix = "EXPLAIN FORMAT=TRADITIONAL "
	}
	if dialect == "sqlite" {
		prefix = "EXPLAIN QUERY PLAN "
	}
	rows, err := model.DB.Raw(prefix + query).Rows()
	require.NoError(t, err)
	columns, err := rows.Columns()
	require.NoError(t, err)
	var plan strings.Builder
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		require.NoError(t, rows.Scan(pointers...))
		for i, value := range values {
			if bytes, ok := value.([]byte); ok {
				value = string(bytes)
			}
			fmt.Fprintf(&plan, "%s=%v ", columns[i], value)
		}
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	t.Logf("%s\n%s", query, plan.String())
	assert.Contains(t, plan.String(), "idx_user_access_tokens_user_id")
}

func TestUserAccessTokensClearMySQLWithPostgresAudit(t *testing.T) {
	if os.Getenv("TEST_SECURITY_DIALECT") != "mysql" {
		t.Skip("mixed database contract runs with MySQL as the primary database")
	}
	dsn := os.Getenv("LOG_SQL_DSN")
	if !strings.HasPrefix(dsn, "postgres") {
		t.Fatal("PostgreSQL log database is required for this test")
	}
	_, identity, target := setupAdminUserTest(t)
	previous := model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	logDB, _ := newAuditTestDatabase(t, "postgres", dsn)
	require.NoError(t, logDB.AutoMigrate(&model.AuditLog{}))
	model.LOG_DB = logDB
	common.SetDatabaseTypes(previousMain, common.DatabaseTypePostgreSQL)
	model.InitColumnNames()
	t.Cleanup(func() {
		model.LOG_DB = previous
		common.SetDatabaseTypes(previousMain, previousLog)
		model.InitColumnNames()
	})
	createClearAccessTokenFixture(t, target.Id, "nap_mixed_database")
	op := service.VerificationOperation{Scope: service.VerificationScopeAdminUserAccessTokensDeleteAll, Context: []byte(fmt.Sprintf(`{"user_id":%d}`, target.Id))}
	proof := issueSecurityEnrollmentProof(t, identity, op, service.VerificationMethodPassword)
	response := adminUserRequest(http.MethodDelete, "/api/user/2/access_tokens", "", proof, identity, common.RoleRootUser, gin.Params{{Key: "id", Value: fmt.Sprint(target.Id)}}, AdminClearUserAccessTokens)
	assert.Contains(t, response.Body.String(), `"success":true`)
	var audit model.AuditLog
	require.NoError(t, logDB.Where("action = ?", "user.access_tokens.delete_all").First(&audit).Error)
	assert.Equal(t, identity.UserID, audit.UserId)
	assert.True(t, audit.Success)
}
