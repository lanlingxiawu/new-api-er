package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSecurityStopRequiresRootBoundProofAndIsIdempotent(t *testing.T) {
	kind := os.Getenv("TEST_SECURITY_DIALECT")
	if kind == "" {
		kind = "sqlite"
	}
	db, dsn := newAuditTestDatabase(t, kind, os.Getenv("TEST_"+strings.ToUpper(kind)+"_DSN"))
	previousDB, previousLogDB, previousPath := model.DB, model.LOG_DB, common.SQLitePath
	previousRedis, previousPassword := common.RedisEnabled, common.PasswordLoginEnabled
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	t.Cleanup(func() {
		model.CloseAuditReadPools()
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SQLitePath = previousPath
		common.RedisEnabled, common.PasswordLoginEnabled = previousRedis, previousPassword
		common.SetDatabaseTypes(previousMain, previousLog)
	})
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled = false
	common.PasswordLoginEnabled = true
	dbType := common.DatabaseTypeSQLite
	if kind == "sqlite" {
		common.SQLitePath = dsn
		t.Setenv("SQL_DSN", "")
	} else {
		t.Setenv("SQL_DSN", dsn)
		if kind == "mysql" {
			dbType = common.DatabaseTypeMySQL
		} else {
			dbType = common.DatabaseTypePostgreSQL
		}
	}
	common.SetDatabaseTypes(dbType, dbType)
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, i18n.Init())
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.TwoFA{}, &model.TwoFABackupCode{}, &model.PasskeyCredential{}, &model.AuthFlow{}, &model.Option{}, &model.UserAccessToken{}, &model.EmployeeProfile{}, &model.AuditLog{}))
	require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
	require.NoError(t, model.MigrateAuditSecurity(context.Background(), db))
	require.NoError(t, model.InitAuditReadPools(context.Background()))
	password, err := common.Password2Hash("stop-fixture-password")
	require.NoError(t, err)
	user := model.User{Username: "stop-root", AffCode: "stop-root", Password: password, Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "test")
	require.NoError(t, err)
	identity, err := service.ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)
	active := "stop-fixture"
	group := model.SecurityResponseGroup{TriggerKey: active, ActiveKey: &active, TargetUserID: 200, AuthVersion: 1, State: "active"}
	require.NoError(t, db.Create(&group).Error)
	alert := model.SecurityAlert{ActorID: 200, ActorRole: common.RoleCommonUser, Version: 1, Rule: "risky_ip_login", Evidence: "[]"}
	require.NoError(t, db.Create(&alert).Error)
	require.NoError(t, db.Create(&model.SecurityResponseLink{AlertID: alert.ID, GroupID: group.ID}).Error)
	require.NoError(t, db.Create(&model.SecurityAction{AlertID: alert.ID, ResponseGroupID: &group.ID, TargetUserID: 200, Action: "clear_api_keys", Source: "automatic", Outcome: "pending"}).Error)
	body := fmt.Sprintf(`{"version":1,"response_group_id":%d,"action":"stop_automatic_response","request_id":"stop-http-fixture"}`, group.ID)
	binding := service.AuditSecurityStopContext{AlertID: alert.ID, Version: 1, ResponseGroupID: group.ID, Action: "stop_automatic_response"}
	bindingRaw, err := common.Marshal(binding)
	require.NoError(t, err)
	for _, tc := range []struct {
		name                            string
		role                            int
		pat, proof, wrongGroup, success bool
	}{
		{name: "missing proof", role: common.RoleRootUser},
		{name: "administrator", role: common.RoleAdminUser},
		{name: "personal access token", role: common.RoleRootUser, pat: true},
		{name: "wrong group proof", role: common.RoleRootUser, proof: true, wrongGroup: true},
		{name: "bound proof", role: common.RoleRootUser, proof: true, success: true},
		{name: "retry with fresh proof returns same receipt", role: common.RoleRootUser, proof: true, success: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proofToken := ""
			if tc.proof {
				raw := bindingRaw
				if tc.wrongGroup {
					wrong := binding
					wrong.ResponseGroupID++
					raw, err = common.Marshal(wrong)
					require.NoError(t, err)
				}
				proof, err := service.VerifySecurityInput(identity, service.VerificationInput{Scope: service.VerificationScopeAuditSecurityResponseStop, Method: service.VerificationMethodPassword, Password: "stop-fixture-password", Context: raw})
				require.NoError(t, err)
				proofToken = proof.ProofToken
			}
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Set("id", identity.UserID)
			c.Set("role", tc.role)
			c.Set("session_id", identity.SessionID)
			c.Set("auth_version", identity.UserAuthVersion)
			c.Set("session_version", identity.SessionVersion)
			c.Set("use_access_token", tc.pat)
			c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(alert.ID)}}
			c.Request = httptest.NewRequest(http.MethodPost, "/api/audit/security/alerts/1/responses", strings.NewReader(body))
			c.Request.Header.Set("X-Security-Proof", proofToken)
			RespondToSecurityAlert(c)
			if tc.pat || tc.role != common.RoleRootUser {
				require.Equal(t, http.StatusForbidden, response.Code)
				return
			}
			var result struct {
				Success bool `json:"success"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.Equal(t, tc.success, result.Success, response.Body.String())
		})
	}
	var receipts int64
	require.NoError(t, db.Model(&model.SecurityAction{}).Where("action = ?", "stop_automatic_response").Count(&receipts).Error)
	require.EqualValues(t, 1, receipts)
	var action model.SecurityAction
	require.NoError(t, db.Where("action = ?", "clear_api_keys").Take(&action).Error)
	require.Equal(t, "skipped", action.Outcome)
	require.Equal(t, "stopped", action.ReasonCode)
}
