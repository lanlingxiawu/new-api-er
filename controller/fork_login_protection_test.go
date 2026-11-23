package controller

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func setupLoginProtectionTest(t *testing.T) (*model.User, service.AuthIdentity) {
	t.Helper()
	previous := operation_setting.GetLoginProtectionSetting()
	operation_setting.ReplaceLoginProtectionSetting(operation_setting.LoginProtectionSetting{MaxFailures: 5})
	t.Cleanup(func() { operation_setting.ReplaceLoginProtectionSetting(previous) })
	user, identity := setupSecurityEnrollmentTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.UserLoginProtection{}))
	operation_setting.ReplaceLoginProtectionSetting(operation_setting.LoginProtectionSetting{Enabled: true, MaxFailures: 5})
	previousLogin := common.PasswordLoginEnabled
	common.PasswordLoginEnabled = true
	t.Cleanup(func() { common.PasswordLoginEnabled = previousLogin })
	return user, identity
}

func TestLoginAttemptedAccountAudit(t *testing.T) {
	for _, scenario := range []string{"password", "email", "missing account", "encrypted password", "MFA", "MFA lock", "expired flow", "forged flow", "passkey"} {
		t.Run(scenario, func(t *testing.T) {
			user, _ := setupLoginProtectionTest(t)
			previous := operation_setting.GetAuditSecuritySetting()
			require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"capture_enabled": "true", "suspended": "false"}))
			t.Cleanup(func() {
				require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"capture_enabled": fmt.Sprint(previous.CaptureEnabled), "suspended": fmt.Sprint(previous.Suspended)}))
			})
			for {
				if _, ok := model.TakeSecurityCapture(); !ok {
					break
				}
			}
			want := user.Username
			path := "/api/user/login"
			handler := Login
			request := map[string]any{"username": user.Username, "password": "do-not-log-password"}
			request["login_target"] = map[string]int{"user_id": user.Id + 100, "role": common.RoleRootUser}
			if scenario == "email" {
				want = "attempted@example.com"
				require.NoError(t, model.DB.Model(user).Update("email", want).Error)
				request["username"] = want
			} else if scenario == "missing account" {
				want = "missing-account"
				request["username"] = want
			} else if scenario == "encrypted password" {
				common.PasswordLoginEncryptionEnabled = true
				request["password_encrypted"] = "do-not-log-ciphertext"
				request["encryption_key_id"] = "invalid-key"
			} else if scenario != "password" {
				require.NoError(t, model.DB.Create(&model.TwoFA{UserId: user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}).Error)
				if scenario == "passkey" {
					newSecurityLoginPasskey(t, user.Id)
				}
				challenge, err := service.StartLoginVerification(user, "password", nil)
				require.NoError(t, err)
				require.NotNil(t, challenge)
				path, handler = "/api/user/login/verify", VerifyLogin
				request = map[string]any{"flow_token": challenge.FlowToken, "code": "do-not-log-code", "username": "forged-account"}
				if scenario == "MFA lock" {
					for range 4 {
						_, err := model.RecordLoginFailure(user.Id, user.AuthVersion)
						require.NoError(t, err)
					}
				} else if scenario == "expired flow" {
					require.NoError(t, model.DB.Model(&model.AuthFlow{}).Where("user_id = ? AND purpose = ?", user.Id, model.AuthFlowPurposeLoginVerification).Update("expires_at", time.Now().Add(-time.Minute)).Error)
				} else if scenario == "forged flow" {
					request["flow_token"] = "not-a-valid-flow"
				} else if scenario == "passkey" {
					path, handler = "/api/user/login/passkey/finish", LoginPasskeyFinish
					request["passkey_flow_token"] = "do-not-log-flow"
					request["credential"] = map[string]string{"type": "invalid-credential-type"}
				}
			}
			body, err := common.Marshal(request)
			require.NoError(t, err)
			router := gin.New()
			router.POST(path, handler)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("POST", path, strings.NewReader(string(body))))
			require.Contains(t, response.Body.String(), `"success":false`)
			require.NotContains(t, response.Body.String(), want)
			require.Empty(t, response.Result().Cookies())
			var entries []model.AuditLog
			for {
				entry, ok := model.TakeSecurityCapture()
				if !ok {
					break
				}
				entries = append(entries, entry)
			}
			if scenario == "expired flow" || scenario == "forged flow" {
				require.Empty(t, entries, "invalid flows must never manufacture an account from request input")
				return
			}
			count := 1
			if scenario == "MFA lock" {
				count = 2
			}
			require.Len(t, entries, count)
			for _, entry := range entries {
				require.Zero(t, entry.UserId)
				require.Empty(t, entry.Username)
				require.Nil(t, entry.Other.Op.Identity)
				if scenario == "missing account" || scenario == "encrypted password" {
					require.Nil(t, entry.Other.Op.LoginTarget)
				} else {
					require.Equal(t, &model.AuditLoginTarget{UserID: user.Id, Role: user.Role}, entry.Other.Op.LoginTarget)
				}
				require.Equal(t, want, entry.Other.Op.Params["attempted_account"])
				require.Equal(t, model.SecurityAccountRef(want), entry.Other.Op.Params["subject_ref"])
				encoded, err := common.Marshal(entry)
				require.NoError(t, err)
				require.NotContains(t, string(encoded), "do-not-log-")
				require.NotContains(t, string(encoded), "forged-account")
				if token, ok := request["flow_token"].(string); ok {
					require.NotContains(t, string(encoded), token)
				}
			}
		})
	}
}

func TestLoginProtectionPasswordAliasesAndLock(t *testing.T) {
	user, _ := setupLoginProtectionTest(t)
	require.NoError(t, model.DB.Model(user).Update("email", "protected@example.com").Error)
	for i := range 5 {
		identifier := user.Username
		if i%2 == 1 {
			identifier = "protected@example.com"
		}
		body, err := common.Marshal(map[string]string{"username": identifier, "password": "wrong-password"})
		require.NoError(t, err)
		response := securityEnrollmentRequest("POST", "/api/user/login", string(body), "", service.AuthIdentity{}, Login)
		assert.Contains(t, response.Body.String(), `"success":false`)
		assert.NotContains(t, response.Body.String(), "access_token")
		var state model.UserLoginProtection
		require.NoError(t, model.DB.First(&state, "user_id = ?", user.Id).Error)
		assert.Equal(t, i+1, state.FailedAttempts)
		assert.Equal(t, i == 4, state.LockedAt > 0)
	}
	_, err := service.CreateLoginSessionAtAuthVersion(user.Id, user.AuthVersion, "passkey", "127.0.0.1", "test")
	assert.ErrorIs(t, err, model.ErrLoginLocked)
	valid := securityEnrollmentRequest("POST", "/api/user/login", `{"username":"enrollment-user","password":"enrollment-password"}`, "", service.AuthIdentity{}, Login)
	assert.Contains(t, valid.Body.String(), "contact sales")
	assert.Empty(t, valid.Result().Cookies())
	wrong := securityEnrollmentRequest("POST", "/api/user/login", `{"username":"enrollment-user","password":"wrong-password"}`, "", service.AuthIdentity{}, Login)
	missing := securityEnrollmentRequest("POST", "/api/user/login", `{"username":"missing-user","password":"wrong-password"}`, "", service.AuthIdentity{}, Login)
	assert.JSONEq(t, wrong.Body.String(), missing.Body.String())
	var count int64
	require.NoError(t, model.DB.Model(&model.UserLoginProtection{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestLoginProtectionSuccessUnlockAndDisable(t *testing.T) {
	user, identity := setupLoginProtectionTest(t)
	for range 4 {
		locked, err := model.RecordLoginFailure(user.Id, user.AuthVersion)
		require.NoError(t, err)
		assert.False(t, locked)
	}
	_, err := service.CreateLoginSessionAtAuthVersion(user.Id, user.AuthVersion, "passkey", "127.0.0.1", "test")
	require.NoError(t, err)
	var state model.UserLoginProtection
	require.NoError(t, model.DB.First(&state, "user_id = ?", user.Id).Error)
	assert.Zero(t, state.FailedAttempts)
	for range 5 {
		_, err = model.RecordLoginFailure(user.Id, user.AuthVersion)
		require.NoError(t, err)
	}
	_, _, err = service.ValidateLoginSession(identity)
	require.NoError(t, err, "existing sessions remain usable")
	operation_setting.ReplaceLoginProtectionSetting(operation_setting.LoginProtectionSetting{MaxFailures: 5})
	_, err = service.CreateLoginSession(user.Id, "passkey", "127.0.0.1", "disabled")
	require.NoError(t, err)
	operation_setting.ReplaceLoginProtectionSetting(operation_setting.LoginProtectionSetting{Enabled: true, MaxFailures: 5})
	assert.ErrorIs(t, model.CheckLoginProtection(user.Id, user.Role), model.ErrLoginLocked)
	changed, err := model.UnlockUserLogin(user.Id)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = model.UnlockUserLogin(user.Id)
	require.NoError(t, err)
	assert.False(t, changed)
	_, err = service.CreateLoginSession(user.Id, "passkey", "127.0.0.1", "unlocked")
	require.NoError(t, err)
	current, err := model.GetUserById(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, user.Status, current.Status)
	assert.Equal(t, user.AuthVersion, current.AuthVersion)
}

func TestLoginProtectionConcurrentFailuresAndRoot(t *testing.T) {
	user, _ := setupLoginProtectionTest(t)
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	start := make(chan struct{})
	for range 12 {
		wg.Go(func() {
			<-start
			_, err := model.RecordLoginFailure(user.Id, user.AuthVersion)
			errors <- err
		})
	}
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	var state model.UserLoginProtection
	require.NoError(t, model.DB.First(&state, "user_id = ?", user.Id).Error)
	assert.Equal(t, 5, state.FailedAttempts)
	assert.Positive(t, state.LockedAt)
	require.NoError(t, model.DB.Model(user).Update("role", common.RoleRootUser).Error)
	require.NoError(t, model.PublishUserAuthCache(user.Id))
	_, err := service.CreateLoginSession(user.Id, "passkey", "127.0.0.1", "root")
	require.NoError(t, err)
	locked, err := model.RecordLoginFailure(user.Id, user.AuthVersion)
	require.NoError(t, err)
	assert.False(t, locked)
}

func TestLoginProtectionConfigurationValidation(t *testing.T) {
	setupLoginProtectionTest(t)
	for _, value := range []string{"0", "101", "-1", "1.5", "999999999999999999999"} {
		t.Run(value, func(t *testing.T) {
			_, err := model.SaveConfigGroup("login_protection_setting", map[string]string{"max_failures": value})
			require.Error(t, err)
			assert.Equal(t, 5, operation_setting.GetLoginProtectionSetting().MaxFailures)
		})
	}
	for _, limit := range []int{1, 5, 100} {
		applied, err := model.SaveConfigGroup("login_protection_setting", map[string]string{"enabled": "true", "max_failures": fmt.Sprint(limit)})
		require.NoError(t, err)
		assert.True(t, applied)
		assert.Equal(t, limit, operation_setting.GetLoginProtectionSetting().MaxFailures)
	}
	_, err := model.SaveConfigGroup("login_protection_setting", map[string]string{"unknown": "true"})
	require.Error(t, err)
}

func TestLoginProtectionVerificationCompletion(t *testing.T) {
	user, _ := setupLoginProtectionTest(t)
	factor := &model.TwoFA{UserId: user.Id, Secret: "JBSWY3DPEHPK3PXP", IsEnabled: true}
	require.NoError(t, model.DB.Create(factor).Error)
	for range 4 {
		_, err := model.RecordLoginFailure(user.Id, user.AuthVersion)
		require.NoError(t, err)
	}
	challenge, err := service.StartLoginVerification(user, "password", nil)
	require.NoError(t, err)
	require.NotNil(t, challenge)
	var state model.UserLoginProtection
	require.NoError(t, model.DB.First(&state, "user_id = ?", user.Id).Error)
	assert.Equal(t, 4, state.FailedAttempts, "primary authentication must not clear failures")
	_, _, err = service.VerifyLoginCode(challenge.FlowToken, "invalid-code", "127.0.0.1", "test")
	assert.ErrorIs(t, err, model.ErrLoginLocked)
	code, err := totp.GenerateCode(factor.Secret, time.Now())
	require.NoError(t, err)
	_, _, err = service.VerifyLoginCode(challenge.FlowToken, code, "127.0.0.1", "test")
	assert.ErrorIs(t, err, model.ErrLoginLocked, "a pending challenge cannot bypass the lock")
	for _, method := range []string{"password", "wechat", "oauth:github", "oauth:telegram"} {
		_, err := service.StartLoginVerification(user, method, nil)
		assert.ErrorIs(t, err, model.ErrLoginLocked)
	}
	_, err = model.UnlockUserLogin(user.Id)
	require.NoError(t, err)
	_, err = model.RecordLoginFailure(user.Id, user.AuthVersion)
	require.NoError(t, err)
	bundle, _, err := service.VerifyLoginCode(challenge.FlowToken, code, "127.0.0.1", "test")
	require.NoError(t, err)
	assert.NotEmpty(t, bundle.AccessToken)
	require.NoError(t, model.DB.First(&state, "user_id = ?", user.Id).Error)
	assert.Zero(t, state.FailedAttempts)
	_, _, err = service.VerifyLoginCode(challenge.FlowToken, code, "127.0.0.1", "test")
	require.Error(t, err, "flow replay must remain rejected")
	require.NoError(t, model.DB.First(&state, "user_id = ?", user.Id).Error)
	assert.Zero(t, state.FailedAttempts)
}

func TestLoginProtectionRejectedSessionPreservesFailures(t *testing.T) {
	user, _ := setupLoginProtectionTest(t)
	_, err := model.RecordLoginFailure(user.Id, user.AuthVersion)
	require.NoError(t, err)
	injected := errors.New("injected session create failure")
	require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register("test:login-protection-session", func(tx *gorm.DB) {
		if tx.Statement.Table == "user_sessions" {
			tx.AddError(injected)
		}
	}))
	t.Cleanup(func() { _ = model.DB.Callback().Create().Remove("test:login-protection-session") })
	_, err = service.CreateLoginSession(user.Id, "passkey", "127.0.0.1", "test")
	assert.ErrorIs(t, err, injected)
	var state model.UserLoginProtection
	require.NoError(t, model.DB.First(&state, "user_id = ?", user.Id).Error)
	assert.Equal(t, 1, state.FailedAttempts)
	require.NoError(t, model.DB.Callback().Create().Remove("test:login-protection-session"))
	previous := operation_setting.GetUserSessionSetting()
	t.Cleanup(func() { operation_setting.ReplaceUserSessionSetting(previous) })
	limited := previous
	limited.ActiveLimit = 1
	operation_setting.ReplaceUserSessionSetting(limited)
	_, err = service.CreateLoginSession(user.Id, "passkey", "127.0.0.1", "test")
	assert.ErrorIs(t, err, model.ErrUserSessionLimit)
	require.NoError(t, model.DB.First(&state, "user_id = ?", user.Id).Error)
	assert.Equal(t, 1, state.FailedAttempts)
}

func TestLoginProtectionStateFailureDeniesNewSession(t *testing.T) {
	user, _ := setupLoginProtectionTest(t)
	injected := errors.New("injected protection read failure")
	require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register("test:login-protection-read", func(tx *gorm.DB) {
		if tx.Statement.Table == "user_login_protections" {
			tx.AddError(injected)
		}
	}))
	t.Cleanup(func() { _ = model.DB.Callback().Query().Remove("test:login-protection-read") })
	_, err := service.CreateLoginSession(user.Id, "passkey", "127.0.0.1", "test")
	assert.ErrorIs(t, err, injected)
	count, err := model.CountActiveUserSessions(user.Id, time.Now().Unix())
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)
}

func TestLoginProtectionUnlockRequiresBoundProof(t *testing.T) {
	operator, identity := setupLoginProtectionTest(t)
	require.NoError(t, model.DB.Model(operator).Update("role", common.RoleRootUser).Error)
	require.NoError(t, model.PublishUserAuthCache(operator.Id))
	target := &model.User{Username: "locked-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, Group: "default", AffCode: "locked-aff"}
	require.NoError(t, model.DB.Create(target).Error)
	for range 5 {
		_, err := model.RecordLoginFailure(target.Id, target.AuthVersion)
		require.NoError(t, err)
	}
	body := fmt.Sprintf(`{"id":%d,"action":"unlock_login"}`, target.Id)
	for _, invalidID := range []int{0, -1, target.Id + 100} {
		invalid := adminUserRequest("POST", "/api/user/manage", fmt.Sprintf(`{"id":%d,"action":"unlock_login"}`, invalidID), "", identity, common.RoleRootUser, nil, ManageUser)
		assert.Contains(t, invalid.Body.String(), "User does not exist")
	}
	noSession := identity
	noSession.SessionID = ""
	pat := adminUserRequest("POST", "/api/user/manage", body, "", noSession, common.RoleRootUser, nil, ManageUser)
	assert.Equal(t, http.StatusForbidden, pat.Code)
	peer := adminUserRequest("POST", "/api/user/manage", body, "", identity, common.RoleCommonUser, nil, ManageUser)
	assert.Contains(t, peer.Body.String(), "No permission")
	missing := adminUserRequest("POST", "/api/user/manage", body, "", identity, common.RoleRootUser, nil, ManageUser)
	assert.NotContains(t, missing.Body.String(), `"success":true`)
	assert.ErrorIs(t, model.CheckLoginProtection(target.Id, target.Role), model.ErrLoginLocked)
	operation := service.VerificationOperation{Scope: service.VerificationScopeAdminUserManage, Context: []byte(fmt.Sprintf(`{"user_id":%d,"action":"unlock_login"}`, target.Id))}
	wrongOperation := service.VerificationOperation{Scope: service.VerificationScopeAdminUserManage, Context: []byte(fmt.Sprintf(`{"user_id":%d,"action":"disable"}`, target.Id))}
	wrongProof := issueSecurityEnrollmentProof(t, identity, wrongOperation, service.VerificationMethodPassword)
	wrongAction := adminUserRequest("POST", "/api/user/manage", body, wrongProof, identity, common.RoleRootUser, nil, ManageUser)
	assert.NotContains(t, wrongAction.Body.String(), `"success":true`)
	assert.ErrorIs(t, model.CheckLoginProtection(target.Id, target.Role), model.ErrLoginLocked)
	proof := issueSecurityEnrollmentProof(t, identity, operation, service.VerificationMethodPassword)
	response := adminUserRequest("POST", "/api/user/manage", body, proof, identity, common.RoleRootUser, nil, ManageUser)
	assert.Contains(t, response.Body.String(), `"success":true`)
	require.NoError(t, model.CheckLoginProtection(target.Id, target.Role))
	replay := adminUserRequest("POST", "/api/user/manage", body, proof, identity, common.RoleRootUser, nil, ManageUser)
	assert.NotContains(t, replay.Body.String(), `"success":true`)
	var audit model.AuditLog
	require.NoError(t, model.LOG_DB.Where("action = ?", "user.manage").Last(&audit).Error)
	auditJSON, err := common.Marshal(audit)
	require.NoError(t, err)
	assert.Contains(t, string(auditJSON), "unlock_login")
	assert.NotContains(t, string(auditJSON), proof)
}

func TestLoginProtectionIgnoresObsoleteAccounts(t *testing.T) {
	user, _ := setupLoginProtectionTest(t)
	for _, attempt := range []struct {
		id      int
		version int64
	}{{user.Id + 100, 1}, {user.Id, user.AuthVersion + 1}} {
		locked, err := model.RecordLoginFailure(attempt.id, attempt.version)
		require.NoError(t, err)
		assert.False(t, locked)
	}
	require.NoError(t, model.DB.Model(user).Update("status", common.UserStatusDisabled).Error)
	locked, err := model.RecordLoginFailure(user.Id, user.AuthVersion)
	require.NoError(t, err)
	assert.False(t, locked)
	var count int64
	require.NoError(t, model.DB.Model(&model.UserLoginProtection{}).Count(&count).Error)
	assert.Zero(t, count, "missing, disabled and stale accounts must not create state")
}

func TestLoginProtectionMigrationAndPageState(t *testing.T) {
	user, _ := setupLoginProtectionTest(t)
	var migrationSQL strings.Builder
	logged := model.DB.Session(&gorm.Session{Logger: gormlogger.New(log.New(&migrationSQL, "", 0), gormlogger.Config{LogLevel: gormlogger.Info})})
	require.NoError(t, logged.AutoMigrate(&model.UserLoginProtection{}))
	statements := strings.ToUpper(migrationSQL.String())
	assert.NotContains(t, statements, "CREATE TABLE")
	assert.NotContains(t, statements, "ALTER TABLE")
	assert.NotContains(t, statements, "CREATE INDEX")
	_, err := model.RecordLoginFailure(user.Id, user.AuthVersion)
	require.NoError(t, err)
	users := []*model.User{user, {Id: user.Id + 100, Role: common.RoleCommonUser}}
	require.NoError(t, model.FillUserLoginProtection(users))
	assert.Equal(t, 1, users[0].LoginProtection.FailedAttempts)
	assert.False(t, users[0].LoginProtection.Locked)
	assert.Zero(t, users[1].LoginProtection.FailedAttempts)
	assert.False(t, users[1].LoginProtection.Locked)
	explain := "EXPLAIN SELECT user_id, locked_at FROM user_login_protections WHERE user_id = ?"
	if model.DB.Dialector.Name() == "sqlite" {
		explain = "EXPLAIN QUERY PLAN SELECT user_id, locked_at FROM user_login_protections WHERE user_id = ?"
	} else if model.DB.Dialector.Name() == "mysql" {
		explain = "EXPLAIN FORMAT=TRADITIONAL SELECT user_id, locked_at FROM user_login_protections WHERE user_id = ?"
	}
	rows, err := model.DB.Raw(explain, user.Id).Rows()
	require.NoError(t, err)
	defer rows.Close()
	columns, err := rows.Columns()
	require.NoError(t, err)
	var plan strings.Builder
	for rows.Next() {
		values := make([]any, len(columns))
		for i := range values {
			values[i] = new(any)
		}
		require.NoError(t, rows.Scan(values...))
		for _, value := range values {
			v := *(value.(*any))
			if bytes, ok := v.([]byte); ok {
				v = string(bytes)
			}
			fmt.Fprint(&plan, v, " ")
		}
	}
	require.NoError(t, rows.Err())
	t.Log("primary-key query plan:", plan.String())
	assert.Regexp(t, `(?i)(primary|pkey)`, plan.String())
}

func TestLoginProtectionSuccessRacingFinalFailure(t *testing.T) {
	user, _ := setupLoginProtectionTest(t)
	for range 4 {
		_, err := model.RecordLoginFailure(user.Id, user.AuthVersion)
		require.NoError(t, err)
	}
	start := make(chan struct{})
	failed := make(chan error, 1)
	signedIn := make(chan error, 1)
	go func() {
		<-start
		_, err := model.RecordLoginFailure(user.Id, user.AuthVersion)
		failed <- err
	}()
	go func() {
		<-start
		_, err := service.CreateLoginSession(user.Id, "passkey", "127.0.0.1", "test")
		signedIn <- err
	}()
	close(start)
	require.NoError(t, <-failed)
	loginErr := <-signedIn
	var state model.UserLoginProtection
	require.NoError(t, model.DB.First(&state, "user_id = ?", user.Id).Error)
	if state.LockedAt != 0 {
		assert.ErrorIs(t, loginErr, model.ErrLoginLocked)
		assert.Equal(t, 5, state.FailedAttempts)
	} else {
		assert.NoError(t, loginErr)
		assert.Equal(t, 1, state.FailedAttempts)
	}
}
