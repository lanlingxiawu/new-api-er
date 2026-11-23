package auditsecurity

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newCredentialCleanupFixture(t *testing.T, apiKeys, accessTokens, ban bool) (*gorm.DB, []model.SecurityAction) {
	t.Helper()
	return newCredentialCleanupFixtureForRole(t, apiKeys, accessTokens, ban, common.RoleCommonUser)
}

func newCredentialCleanupFixtureForRole(t *testing.T, apiKeys, accessTokens, ban bool, role int) (*gorm.DB, []model.SecurityAction) {
	t.Helper()
	db := newWorkerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}, &model.Token{}, &model.UserAccessToken{}))
	old := operation_setting.GetAuditSecuritySetting()
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = oldRedis
		require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{
			"enabled": fmt.Sprint(old.Enabled), "capture_enabled": fmt.Sprint(old.CaptureEnabled), "suspended": fmt.Sprint(old.Suspended),
			"auto_ban_enabled": fmt.Sprint(old.AutoBanEnabled), "auto_ban_effective_at": fmt.Sprint(old.AutoBanEffectiveAt),
			"auto_ban_include_staff": fmt.Sprint(old.AutoBanIncludeStaff), "auto_ban_excluded_user_ids_json": old.AutoBanExcludedUserIDsJSON, "auto_ban_rules_json": old.AutoBanRulesJSON,
			"auto_ban_exclude_root": fmt.Sprint(old.AutoBanExcludeRoot),
		}))
	})
	cfg := operation_setting.DefaultAuditSecuritySetting()
	rules, err := operation_setting.ParseAutoBanRules(cfg.AutoBanRulesJSON)
	require.NoError(t, err)
	for i := range rules {
		if i >= 4 {
			continue
		}
		rules[i].Selected, rules[i].ClearAPIKeys, rules[i].ClearAccessTokens = ban, apiKeys, accessTokens
	}
	raw, err := common.Marshal(rules)
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{
		"enabled": "true", "capture_enabled": "true", "suspended": "false", "auto_ban_enabled": "true", "auto_ban_effective_at": fmt.Sprint(time.Now().Unix() - 10),
		"auto_ban_include_staff": "false", "auto_ban_excluded_user_ids_json": "[]", "auto_ban_rules_json": string(raw),
		"auto_ban_exclude_root": "true",
	}))
	legacy := "cleanup-fixture-legacy"
	require.NoError(t, db.Create(&model.User{Id: 901, Username: "cleanup-fixture", AffCode: "cleanup", Role: role, Status: common.UserStatusEnabled, AuthVersion: 7, Quota: 999, AccessToken: &legacy}).Error)
	state, err := model.ClaimAuditSecurity(context.Background(), db, "credential-fixture")
	require.NoError(t, err)
	event := compromiseEvent("cleanup-trigger", 901, time.Now().Unix(), "8.8.8.8", "auth.session_refresh_reuse")
	event.ActorRole = role
	var facts Facts
	require.NoError(t, common.UnmarshalJsonStr(event.Facts, &facts))
	facts.Identity.AuthVersion = 7
	facts.Identity.Role = role
	raw, err = common.Marshal(facts)
	require.NoError(t, err)
	event.Facts = string(raw)
	require.NoError(t, Apply(context.Background(), db, state, []model.SecurityEvent{event}, state, operation_setting.GetAuditSecuritySetting()))
	var actions []model.SecurityAction
	require.NoError(t, db.Where("target_user_id = ?", 901).Order("id").Find(&actions).Error)
	return db, actions
}

func TestAutomaticResponseRootExclusionControlsAllActions(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		role                                int
		excludeRoot, includeStaff, employee bool
		excludedIDs, reason                 string
	}{
		{"root_excluded_by_default", common.RoleRootUser, operation_setting.DefaultAuditSecuritySetting().AutoBanExcludeRoot, false, false, "[]", "protected_account"},
		{"root_excluded", common.RoleRootUser, true, false, false, "[]", "protected_account"},
		{"staff_does_not_include_root", common.RoleRootUser, true, true, false, "[]", "protected_account"},
		{"root_included_independently", common.RoleRootUser, false, false, false, "[]", ""},
		{"root_and_staff_included", common.RoleRootUser, false, true, false, "[]", ""},
		{"root_employee_included", common.RoleRootUser, false, false, true, "[]", ""},
		{"excluded_id_wins", common.RoleRootUser, false, true, false, "[901]", "excluded_account"},
		{"administrator_excluded", common.RoleAdminUser, false, false, false, "[]", "excluded_account"},
		{"administrator_included", common.RoleAdminUser, true, true, false, "[]", ""},
		{"employee_excluded", common.RoleCommonUser, false, false, true, "[]", "excluded_account"},
		{"employee_included", common.RoleCommonUser, true, true, true, "[]", ""},
		{"regular_user_included", common.RoleCommonUser, true, false, false, "[]", ""},
		{"unknown_privileged_role", common.RoleRootUser + 1, false, true, false, "[]", "protected_account"},
		{"invalid_role", common.RoleCommonUser + 1, false, true, false, "[]", "invalid_target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, actions := newCredentialCleanupFixtureForRole(t, true, true, true, tc.role)
			require.Len(t, actions, 3)
			require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{
				"auto_ban_exclude_root": fmt.Sprint(tc.excludeRoot), "auto_ban_include_staff": fmt.Sprint(tc.includeStaff), "auto_ban_excluded_user_ids_json": tc.excludedIDs,
			}))
			if tc.employee {
				require.NoError(t, db.Create(&model.EmployeeProfile{UserId: 901, Status: 1}).Error)
			}
			require.NoError(t, db.Create(&model.Token{UserId: 901, Key: "root-scope-key", Status: common.TokenStatusEnabled}).Error)
			require.NoError(t, db.Create(&model.UserAccessToken{UserId: 901, TokenHash: "root-scope-pat"}).Error)
			for _, action := range actions {
				if action.Action == "disable_account" {
					require.NoError(t, processAccountResponse(t.Context(), db, action))
				} else {
					_, err := model.ProcessSecurityCredentialCleanup(t.Context(), db, nil, action.ID)
					require.NoError(t, err)
				}
				require.NoError(t, db.First(&action, action.ID).Error)
				if tc.reason == "" {
					require.Equal(t, "succeeded", action.Outcome, action.Action)
				} else {
					require.Equal(t, "skipped", action.Outcome, action.Action)
				}
				require.Equal(t, tc.reason, action.ReasonCode, action.Action)
			}
			var user model.User
			require.NoError(t, db.First(&user, 901).Error)
			var keys, accessTokens int64
			require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", 901).Count(&keys).Error)
			require.NoError(t, db.Model(&model.UserAccessToken{}).Where("user_id = ?", 901).Count(&accessTokens).Error)
			if tc.reason == "" {
				require.Equal(t, common.UserStatusDisabled, user.Status)
				require.EqualValues(t, 8, user.AuthVersion)
				require.Nil(t, user.AccessToken)
				require.Zero(t, keys)
				require.Zero(t, accessTokens)
			} else {
				require.Equal(t, common.UserStatusEnabled, user.Status)
				require.EqualValues(t, 7, user.AuthVersion)
				require.Equal(t, "cleanup-fixture-legacy", strings.TrimSpace(user.GetAccessToken()))
				require.EqualValues(t, 1, keys)
				require.EqualValues(t, 1, accessTokens)
			}
			require.Equal(t, 999, user.Quota)
		})
	}
}

func TestRootExclusionSwitchDoesNotAllowManualBan(t *testing.T) {
	db, actions := newCredentialCleanupFixtureForRole(t, false, false, true, common.RoleRootUser)
	require.Len(t, actions, 1)
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"auto_ban_exclude_root": "false"}))
	require.NoError(t, db.Model(&model.SecurityAction{}).Where("id = ?", actions[0].ID).Update("source", "human").Error)
	action, err := model.DisableUserForSecurity(t.Context(), db, actions[0].ID)
	require.NoError(t, err)
	require.Equal(t, "skipped", action.Outcome)
	require.Equal(t, "protected_account", action.ReasonCode)
}

func TestRootExclusionPolicyChangeSkipsQueuedActions(t *testing.T) {
	for _, excluded := range []bool{false, true} {
		t.Run(fmt.Sprint(excluded), func(t *testing.T) {
			db, actions := newCredentialCleanupFixtureForRole(t, true, true, true, common.RoleRootUser)
			require.Len(t, actions, 3)
			require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"auto_ban_exclude_root": fmt.Sprint(!excluded)}))
			updated, err := operation_setting.NormalizeAuditSecurityUpdateValues(map[string]string{"auto_ban_exclude_root": fmt.Sprint(excluded)})
			require.NoError(t, err)
			require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, updated))
			for _, action := range actions {
				if action.Action == "disable_account" {
					require.NoError(t, processAccountResponse(t.Context(), db, action))
				} else {
					_, err = model.ProcessSecurityCredentialCleanup(t.Context(), db, nil, action.ID)
					require.NoError(t, err)
				}
				require.NoError(t, db.First(&action, action.ID).Error)
				require.Equal(t, "skipped", action.Outcome)
				require.Equal(t, "expired", action.ReasonCode)
			}
			var user model.User
			require.NoError(t, db.First(&user, 901).Error)
			require.Equal(t, common.UserStatusEnabled, user.Status)
			require.Equal(t, "cleanup-fixture-legacy", strings.TrimSpace(user.GetAccessToken()))
		})
	}
}

func TestAutomaticAPIKeyCleanupIsBoundedAndPreservesNewKeys(t *testing.T) {
	for _, count := range []int{0, 1, 99, 100, 101, 1001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			db, actions := newCredentialCleanupFixture(t, true, false, false)
			require.Len(t, actions, 1)
			action := actions[0]
			require.Equal(t, "clear_api_keys", action.Action)
			for i := range count {
				require.NoError(t, db.Create(&model.Token{UserId: 901, Key: fmt.Sprintf("old-%d", i), Status: common.TokenStatusEnabled}).Error)
			}
			updated, err := model.ProcessSecurityCredentialCleanup(context.Background(), db, nil, action.ID)
			require.NoError(t, err)
			var remaining int64
			require.NoError(t, db.Model(&model.Token{}).Count(&remaining).Error)
			require.EqualValues(t, max(count-100, 0), remaining, "one visit must process at most 100 keys")
			fresh := model.Token{UserId: 901, Key: "fresh-after-start", Status: common.TokenStatusEnabled}
			require.NoError(t, db.Create(&fresh).Error)
			for visit := 0; visit < 15 && updated.Outcome != "succeeded"; visit++ {
				updated, err = model.ProcessSecurityCredentialCleanup(context.Background(), db, nil, action.ID)
				require.NoError(t, err)
			}
			require.Equal(t, "succeeded", updated.Outcome)
			var result model.SecurityResponseResult
			require.NoError(t, common.UnmarshalJsonStr(updated.Result, &result))
			require.True(t, result.CountKnown)
			require.EqualValues(t, count, result.AffectedCount)
			_, err = model.ProcessSecurityCredentialCleanup(context.Background(), db, nil, action.ID)
			require.NoError(t, err)
			require.NoError(t, db.First(&fresh, fresh.Id).Error)
			require.Equal(t, common.TokenStatusEnabled, fresh.Status)
			var user model.User
			require.NoError(t, db.First(&user, 901).Error)
			require.Equal(t, common.UserStatusEnabled, user.Status)
			require.EqualValues(t, 7, user.AuthVersion)
			require.Equal(t, "cleanup-fixture-legacy", strings.TrimSpace(user.GetAccessToken()))
			require.Equal(t, 999, user.Quota)
		})
	}
}

func TestAutomaticAccessTokenCleanupIsIndependentAndIdempotent(t *testing.T) {
	db, actions := newCredentialCleanupFixture(t, false, true, false)
	require.Len(t, actions, 1)
	for i := range 21 {
		require.NoError(t, db.Create(&model.UserAccessToken{UserId: 901, TokenHash: fmt.Sprintf("pat-%d", i), ExpiresAt: 1}).Error)
	}
	key := model.Token{UserId: 901, Key: "unaffected-model-key", Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&key).Error)
	action, err := model.ProcessSecurityCredentialCleanup(context.Background(), db, nil, actions[0].ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", action.Outcome)
	var result model.SecurityResponseResult
	require.NoError(t, common.UnmarshalJsonStr(action.Result, &result))
	require.EqualValues(t, 22, result.AffectedCount)
	fresh := model.UserAccessToken{UserId: 901, TokenHash: "fresh-pat"}
	require.NoError(t, db.Create(&fresh).Error)
	_, err = model.ProcessSecurityCredentialCleanup(context.Background(), db, nil, action.ID)
	require.NoError(t, err)
	var count int64
	require.NoError(t, db.Model(&model.UserAccessToken{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, db.First(&key, key.Id).Error)
	require.Equal(t, common.TokenStatusEnabled, key.Status)
	var user model.User
	require.NoError(t, db.First(&user, 901).Error)
	require.Nil(t, user.AccessToken)
	require.EqualValues(t, 7, user.AuthVersion)
	require.Equal(t, common.UserStatusEnabled, user.Status)
}

func TestAutomaticCleanupAcceptsOnlyItsOwnBanVersion(t *testing.T) {
	for _, manualChange := range []bool{false, true} {
		t.Run(fmt.Sprint(manualChange), func(t *testing.T) {
			db, actions := newCredentialCleanupFixture(t, true, true, true)
			require.Len(t, actions, 3)
			require.NoError(t, db.Create(&model.Token{UserId: 901, Key: "target-key", Status: common.TokenStatusEnabled}).Error)
			require.NoError(t, processAccountResponse(context.Background(), db, actions[0]))
			if manualChange {
				require.NoError(t, db.Model(&model.User{}).Where("id = ?", 901).Updates(map[string]any{"auth_version": 9, "status": common.UserStatusEnabled}).Error)
			}
			for _, action := range actions[1:] {
				updated, err := model.ProcessSecurityCredentialCleanup(context.Background(), db, nil, action.ID)
				require.NoError(t, err)
				if manualChange {
					require.Equal(t, "skipped", updated.Outcome)
					require.Equal(t, "superseded", updated.ReasonCode)
				} else {
					require.Equal(t, "succeeded", updated.Outcome)
				}
			}
		})
	}
}

func TestAutomaticCleanupRecoversOnlyPreparedIDsAfterPause(t *testing.T) {
	address := os.Getenv("AUDIT_TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("requires isolated Redis")
	}
	db, actions := newCredentialCleanupFixture(t, true, false, false)
	ctx := context.Background()
	common.RedisEnabled = true
	broken := redis.NewClient(&redis.Options{MaxRetries: -1, Dialer: func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("fixture outage") }})
	t.Cleanup(func() { _ = broken.Close() })
	client := redis.NewClient(&redis.Options{Addr: address, DB: 15, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	var cacheKeys []string
	for i := range 101 {
		key := fmt.Sprintf("cleanup-%d-%d", time.Now().UnixNano(), i)
		require.NoError(t, db.Create(&model.Token{UserId: 901, Key: key, Status: common.TokenStatusEnabled}).Error)
		cacheKey := "token:" + common.GenerateHMAC(key)
		cacheKeys = append(cacheKeys, cacheKey, "token:fence:{"+cacheKey+"}")
		require.NoError(t, client.HSet(ctx, cacheKey, "Status", common.TokenStatusEnabled).Err())
	}
	t.Cleanup(func() { require.NoError(t, client.Del(ctx, cacheKeys...).Err()) })
	_, err := model.ProcessSecurityCredentialCleanup(ctx, db, broken, actions[0].ID)
	require.Error(t, err)
	var receipt model.SecurityAction
	require.NoError(t, db.First(&receipt, actions[0].ID).Error)
	require.Equal(t, "sync_pending", receipt.Outcome)
	var result model.SecurityResponseResult
	require.NoError(t, common.UnmarshalJsonStr(receipt.Result, &result))
	require.Len(t, result.PendingIDs, 100)
	require.NotContains(t, receipt.Result, "cleanup-", "recovery data must not contain credentials")
	// A concurrent manual removal cannot be counted as this action's deletion.
	require.NoError(t, db.Delete(&model.Token{}, result.PendingIDs[0]).Error)
	fresh := model.Token{UserId: 901, Key: "new-key-after-failed-batch", Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&fresh).Error)
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"suspended": "true"}))
	receipt, err = model.ProcessSecurityCredentialCleanup(ctx, db, client, receipt.ID)
	require.NoError(t, err)
	require.Equal(t, "clearing", receipt.Outcome)
	require.NoError(t, common.UnmarshalJsonStr(receipt.Result, &result))
	require.EqualValues(t, 99, result.AffectedCount)
	for i := range 100 {
		require.Zero(t, client.Exists(ctx, cacheKeys[i*2]).Val())
		require.EqualValues(t, 1, client.Exists(ctx, cacheKeys[i*2+1]).Val())
	}
	receipt, err = model.ProcessSecurityCredentialCleanup(ctx, db, client, receipt.ID)
	require.NoError(t, err)
	require.Equal(t, "partial", receipt.Outcome)
	require.Equal(t, "disabled", receipt.ReasonCode)
	var count int64
	require.NoError(t, db.Model(&model.Token{}).Count(&count).Error)
	require.EqualValues(t, 2, count, "the unstarted batch and new key survive pause")
}

func TestAutomaticCleanupFailureLimitAndTransactionRollback(t *testing.T) {
	db, actions := newCredentialCleanupFixture(t, false, true, false)
	pat := model.UserAccessToken{UserId: 901, TokenHash: "rollback-pat"}
	require.NoError(t, db.Create(&pat).Error)
	// Fail the receipt write, after deleting PATs and clearing legacy fields.
	callback := "fixture:cleanup_receipt_failure"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_security_actions" {
			tx.AddError(errors.New("fixture receipt failure"))
		}
	}))
	_, err := model.ProcessSecurityCredentialCleanup(context.Background(), db, nil, actions[0].ID)
	require.Error(t, err)
	require.NoError(t, db.Callback().Update().Remove(callback))
	require.NoError(t, db.First(&pat, pat.Id).Error)
	var user model.User
	require.NoError(t, db.First(&user, 901).Error)
	require.Equal(t, "cleanup-fixture-legacy", strings.TrimSpace(user.GetAccessToken()))
	// Persistent failures are bounded, with no credential changes or fake count.
	require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "user_access_tokens" {
			tx.AddError(errors.New("fixture deletion failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Delete().Remove(callback) })
	w := worker{db: db}
	for attempt := 1; attempt <= 3; attempt++ {
		require.NoError(t, w.processCredentialCleanup(context.Background(), actions[0]))
		var receipt model.SecurityAction
		require.NoError(t, db.First(&receipt, actions[0].ID).Error)
		require.Equal(t, attempt, receipt.Attempts)
		require.GreaterOrEqual(t, receipt.NextAttemptAt, time.Now().Unix()+9)
		if attempt == 3 {
			require.Equal(t, "failed", receipt.Outcome)
			require.Nil(t, receipt.ResponseSummary().AffectedCount)
		}
	}
}

func TestAutomaticCleanupProjectionAndNotificationWaitForAllActions(t *testing.T) {
	db, actions := newCredentialCleanupFixture(t, true, true, true)
	ctx := context.Background()
	require.NoError(t, processAccountResponse(ctx, db, actions[0]))
	_, err := model.ProcessSecurityCredentialCleanup(ctx, db, nil, actions[1].ID)
	require.NoError(t, err)
	alerts := []model.SecurityAlert{{ID: actions[0].AlertID, ActorID: 901}}
	require.NoError(t, loadAlertResponses(ctx, db, alerts))
	require.Len(t, alerts[0].Responses, 3)
	require.Equal(t, "disable_account", alerts[0].Response.Action)
	_, waiting := automaticResponseMessages("session_reuse", alerts[0].Responses)
	require.True(t, waiting, "ban success must not hide pending cleanup")
	_, err = model.ProcessSecurityCredentialCleanup(ctx, db, nil, actions[2].ID)
	require.NoError(t, err)
	require.NoError(t, loadAlertResponses(ctx, db, alerts))
	messages, waiting := automaticResponseMessages("session_reuse", alerts[0].Responses)
	require.False(t, waiting)
	require.Len(t, messages, 3)
	require.NotContains(t, strings.Join(messages, " "), "auth_version")
	// The same alert with another target must not expose any credential counts.
	alerts[0].ActorID = 902
	require.NoError(t, loadAlertResponses(ctx, db, alerts))
	require.Empty(t, alerts[0].Responses)
	require.Nil(t, alerts[0].Response)
}

func TestAutomaticCleanupRechecksScopeAndDeadline(t *testing.T) {
	for _, reason := range []string{"protected_account", "excluded_account", "superseded", "disabled", "expired", "already_disabled"} {
		t.Run(reason, func(t *testing.T) {
			db, actions := newCredentialCleanupFixture(t, false, true, false)
			switch reason {
			case "protected_account":
				require.NoError(t, db.Model(&model.User{}).Where("id = ?", 901).Update("role", common.RoleRootUser).Error)
			case "excluded_account":
				require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"auto_ban_excluded_user_ids_json": "[901]"}))
			case "superseded":
				require.NoError(t, db.Model(&model.User{}).Where("id = ?", 901).Update("auth_version", 8).Error)
			case "disabled":
				require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"auto_ban_enabled": "false"}))
			case "expired":
				require.NoError(t, db.Model(&model.SecurityAction{}).Where("id = ?", actions[0].ID).Update("event_at", time.Now().Unix()-121).Error)
			case "already_disabled":
				require.NoError(t, db.Model(&model.User{}).Where("id = ?", 901).Update("status", common.UserStatusDisabled).Error)
			}
			action, err := model.ProcessSecurityCredentialCleanup(context.Background(), db, nil, actions[0].ID)
			require.NoError(t, err)
			if reason == "already_disabled" {
				require.Equal(t, "succeeded", action.Outcome, "cleanup is independent of account status")
			} else {
				require.Equal(t, "skipped", action.Outcome)
				require.Equal(t, reason, action.ReasonCode)
				require.Nil(t, action.ResponseSummary().AffectedCount)
			}
		})
	}
	t.Run("ten_minute_deadline", func(t *testing.T) {
		db, actions := newCredentialCleanupFixture(t, true, false, false)
		for i := range 101 {
			require.NoError(t, db.Create(&model.Token{UserId: 901, Key: fmt.Sprintf("deadline-%d", i)}).Error)
		}
		action, err := model.ProcessSecurityCredentialCleanup(context.Background(), db, nil, actions[0].ID)
		require.NoError(t, err)
		var result model.SecurityResponseResult
		require.NoError(t, common.UnmarshalJsonStr(action.Result, &result))
		result.StartedAt = time.Now().Unix() - 600
		raw, err := common.Marshal(result)
		require.NoError(t, err)
		require.NoError(t, db.Model(&action).Update("result", string(raw)).Error)
		action, err = model.ProcessSecurityCredentialCleanup(context.Background(), db, nil, action.ID)
		require.NoError(t, err)
		require.Equal(t, "partial", action.Outcome)
		require.Equal(t, "expired", action.ReasonCode)
		require.EqualValues(t, 100, *action.ResponseSummary().AffectedCount)
		var count int64
		require.NoError(t, db.Model(&model.Token{}).Count(&count).Error)
		require.EqualValues(t, 1, count)
	})
}

func TestAutomaticCleanupLateFailureCannotPenalizeNextBatch(t *testing.T) {
	address := os.Getenv("AUDIT_TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("requires isolated Redis")
	}
	db, actions := newCredentialCleanupFixture(t, true, false, false)
	for i := range 101 {
		require.NoError(t, db.Create(&model.Token{UserId: 901, Key: fmt.Sprintf("late-%d-%d", time.Now().UnixNano(), i)}).Error)
	}
	common.RedisEnabled = true
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	broken := redis.NewClient(&redis.Options{MaxRetries: -1, Dialer: func(context.Context, string, string) (net.Conn, error) {
		close(entered)
		<-release
		return nil, errors.New("late cache failure")
	}})
	t.Cleanup(func() { _ = broken.Close() })
	w := worker{db: db, credentialCache: broken}
	finished := make(chan error, 1)
	go func() { finished <- w.processCredentialCleanup(context.Background(), actions[0]) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not prepare a batch")
	}
	client := redis.NewClient(&redis.Options{Addr: address, DB: 15, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	action, err := model.ProcessSecurityCredentialCleanup(context.Background(), db, client, actions[0].ID)
	require.NoError(t, err)
	require.Equal(t, "clearing", action.Outcome)
	releaseOnce.Do(func() { close(release) })
	require.NoError(t, <-finished)
	require.NoError(t, db.First(&action, action.ID).Error)
	require.Equal(t, "clearing", action.Outcome)
	require.Zero(t, action.Attempts, "a late error must not consume the next batch's retry budget")
	require.Empty(t, action.ReasonCode)
}

func TestAutomaticCleanupCustomerProjectionAndPurgeProtection(t *testing.T) {
	db, filter := customerAccessFixture(t)
	ctx := context.Background()
	alert := model.SecurityAlert{ActorID: 3, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, Status: "open", Version: 1, LastSeen: filter.End}
	require.NoError(t, db.Create(&alert).Error)
	for i, kind := range []string{"clear_api_keys", "clear_access_tokens"} {
		raw, err := common.Marshal(model.SecurityResponseResult{CountKnown: true, AffectedCount: int64(i + 1), PendingIDs: []int{123}, TargetAuthVersion: 99})
		require.NoError(t, err)
		require.NoError(t, db.Create(&model.SecurityAction{AlertID: alert.ID, TargetUserID: 3, Action: kind, Source: "automatic", Outcome: "succeeded", Result: string(raw)}).Error)
	}
	page, err := ListCustomerAlerts(ctx, db, filter)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Len(t, page.Items[0].Responses, 2)
	require.Nil(t, page.Items[0].Response, "legacy response stays ban-only")
	detail, err := GetCustomerAlert(ctx, db, 1, 3, alert.ID)
	require.NoError(t, err)
	require.Len(t, detail.Alert.Responses, 2)
	require.EqualValues(t, 2, *detail.Actions[0].AffectedCount)
	raw, err := common.Marshal(detail)
	require.NoError(t, err)
	for _, private := range []string{"pending_ids", "target_auth_version", "result", "execution_key"} {
		require.NotContains(t, string(raw), `"`+private+`"`)
	}
	_, err = GetCustomerAlert(ctx, db, 2, 3, alert.ID)
	require.ErrorIs(t, err, model.ErrCustomerAuditForbidden)
	// Queue deletion while a later API-key batch is running. Recovery metadata
	// must survive even though it is no longer visible in the alerts list.
	require.NoError(t, db.Model(&model.SecurityAction{}).Where("alert_id = ? AND action = ?", alert.ID, "clear_api_keys").Update("outcome", "clearing").Error)
	require.NoError(t, db.Model(&alert).Update("purge_state", "deleting").Error)
	require.NoError(t, model.PurgePendingSecurityAlerts(ctx, db))
	var count int64
	require.NoError(t, db.Model(&model.SecurityAlert{}).Where("id = ?", alert.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, db.Model(&model.SecurityAction{}).Where("alert_id = ?", alert.ID).Update("outcome", "succeeded").Error)
	for range 4 {
		require.NoError(t, model.PurgePendingSecurityAlerts(ctx, db))
	}
	require.NoError(t, db.Model(&model.SecurityAlert{}).Where("id = ?", alert.ID).Count(&count).Error)
	require.Zero(t, count)
}
