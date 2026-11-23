package controller

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestManageUserPromoteClearsStaleOverrides(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, authz.Init(db))
	user := model.User{Username: "promote-clean-slate", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	subject := authz.UserSubject(user.Id)
	require.NoError(t, db.Create(&model.CasbinRule{Ptype: "p", V0: subject, V1: authz.ResourceAdminAccessToken, V2: authz.ActionUse, V3: authz.EffectAllow}).Error)
	identity, proof := manageUserProof(t, db, service.VerificationOperation{Scope: service.VerificationScopeAdminUserManage, Context: []byte(fmt.Sprintf(`{"user_id":%d,"action":"promote"}`, user.Id))})
	recorder := performVerifiedManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"promote"}`, user.Id), identity, proof)
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, common.RoleAdminUser, stored.Role)
	var overrides int64
	require.NoError(t, db.Model(&model.CasbinRule{}).Where("v0 = ?", subject).Count(&overrides).Error)
	assert.Zero(t, overrides, "promotion must not resurrect permissions left by a former administrator")
}

func TestManageUserCommittedChangeAttemptsAllFollowups(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, authz.Init(db))
	user := model.User{Username: "demote-followups", Password: "password", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	now := time.Now().Unix()
	require.NoError(t, db.Create(&model.UserSession{SID: "demote-followups-session", UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, RefreshHash: "refresh", LoginMethod: "password", LastActiveAt: now, ExpiresAt: now + 3600}).Error)
	identity, proof := manageUserProof(t, db, service.VerificationOperation{Scope: service.VerificationScopeAdminUserManage, Context: []byte(fmt.Sprintf(`{"user_id":%d,"action":"demote"}`, user.Id))})

	server := miniredis.RunT(t)
	oldRDB := common.RDB
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	common.RedisEnabled = true
	t.Cleanup(func() { _ = common.RDB.Close(); common.RDB = oldRDB })
	reloadFailed := false
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:demote_reload_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" && reloadFailed {
			tx.AddError(errors.New("injected publish failure"))
			return
		}
		if tx.Statement.Table != "casbin_rule" && tx.Statement.Table != "casbin_rules" {
			return
		}
		if _, filtered := tx.Statement.Clauses["WHERE"]; !filtered {
			reloadFailed = true
			tx.AddError(errors.New("injected reload failure"))
		}
	}))
	sessionUpdates := 0
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:demote_revoke_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "user_sessions" {
			sessionUpdates++
			tx.AddError(errors.New("injected revoke failure"))
		}
	}))
	recorder := performVerifiedManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"demote"}`, user.Id), identity, proof)
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"committed":true`)
	assert.NotContains(t, recorder.Body.String(), "injected reload failure")
	assert.NotContains(t, recorder.Body.String(), "injected publish failure")
	assert.NotContains(t, recorder.Body.String(), "injected revoke failure")
	db.Callback().Query().Remove("test:demote_reload_failure")
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, common.RoleCommonUser, stored.Role)
	assert.Positive(t, sessionUpdates, "session revocation must still be attempted after policy reload and cache publication fail")
}
