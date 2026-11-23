package auditsecurity

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func enableAutoBanFixture(t *testing.T) operation_setting.AuditSecuritySetting {
	t.Helper()
	old := operation_setting.GetAuditSecuritySetting()
	values := map[string]string{"enabled": "true", "capture_enabled": "true", "suspended": "false", "auto_ban_enabled": "true", "auto_ban_effective_at": fmt.Sprint(time.Now().Unix() - 300), "auto_ban_include_staff": "false", "auto_ban_excluded_user_ids_json": "[]", "auto_ban_rules_json": operation_setting.DefaultAuditSecuritySetting().AutoBanRulesJSON}
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, values))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"enabled": fmt.Sprint(old.Enabled), "capture_enabled": fmt.Sprint(old.CaptureEnabled), "suspended": fmt.Sprint(old.Suspended), "auto_ban_enabled": fmt.Sprint(old.AutoBanEnabled), "auto_ban_effective_at": fmt.Sprint(old.AutoBanEffectiveAt), "auto_ban_include_staff": fmt.Sprint(old.AutoBanIncludeStaff), "auto_ban_excluded_user_ids_json": old.AutoBanExcludedUserIDsJSON, "auto_ban_rules_json": old.AutoBanRulesJSON}))
	})
	return operation_setting.GetAuditSecuritySetting()
}

func TestCorrelatedAccountResponseKeepsEachTargetsEvidence(t *testing.T) {
	db := newWorkerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}))
	cfg := enableAutoBanFixture(t)
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	ctx := context.Background()
	state, err := model.ClaimAuditSecurity(ctx, db, "correlated-response")
	require.NoError(t, err)
	now := time.Now().Unix()
	var events []model.SecurityEvent
	for id := 301; id <= 307; id++ {
		require.NoError(t, db.Create(&model.User{Id: id, Username: fmt.Sprint("correlated-", id), AffCode: fmt.Sprint(id), Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}).Error)
		events = append(events, compromiseEvent(fmt.Sprint("login-", id), id, now-50+int64(id-301), "8.8.8.8", "login"))
	}
	// Only the three readers may be banned; the remaining accounts only signed in.
	for id := 301; id <= 303; id++ {
		events = append(events, compromiseEvent(fmt.Sprint("key-", id), id, now-10+int64(id-301), "8.8.8.8", "token.key_view_batch"))
	}
	require.NoError(t, Apply(ctx, db, state, events, state, cfg))
	var actions []model.SecurityAction
	require.NoError(t, db.Where("action = ?", "disable_account").Order("target_user_id ASC").Find(&actions).Error)
	require.Len(t, actions, 3)
	for i, action := range actions {
		id := 301 + i
		require.Equal(t, id, action.TargetUserID)
		require.Equal(t, fmt.Sprint("key-", id), action.EventID)
		var alert model.SecurityAlert
		require.NoError(t, db.First(&alert, action.AlertID).Error)
		var evidence []model.SecurityEvent
		require.NoError(t, common.UnmarshalJsonStr(alert.Evidence, &evidence))
		logins, readers := map[int]bool{}, map[int]bool{}
		for _, event := range evidence {
			if event.Action == "login" {
				logins[event.ActorID] = true
			}
			if event.Action == "token.key_view_batch" {
				readers[event.ActorID] = true
			}
		}
		require.GreaterOrEqual(t, len(logins), 5)
		require.GreaterOrEqual(t, len(readers), 3)
		require.True(t, logins[id])
		require.True(t, readers[id])
		var snapshot model.SecurityFindingSnapshot
		require.NoError(t, db.First(&snapshot, alert.LatestFindingID).Error)
		require.Equal(t, id, snapshot.ActorID)
		require.Equal(t, action.EventID, snapshot.TriggerEventID)
		require.False(t, snapshot.Truncated)
	}
	w := worker{db: db}
	require.NoError(t, w.processAccountResponses(ctx))
	for id := 301; id <= 307; id++ {
		var user model.User
		require.NoError(t, db.First(&user, id).Error)
		if id <= 303 {
			require.Equal(t, common.UserStatusDisabled, user.Status)
		} else {
			require.Equal(t, common.UserStatusEnabled, user.Status)
		}
	}
	// Reprocessing the source page must not enqueue any duplicate response.
	require.NoError(t, Apply(ctx, db, state, events, state, cfg))
	var count int64
	require.NoError(t, db.Model(&model.SecurityAction{}).Where("action = ?", "disable_account").Count(&count).Error)
	require.EqualValues(t, 3, count)
}

func TestAutomaticBanNotificationWaitsForTheCommittedResult(t *testing.T) {
	db := newWorkerTestDB(t)
	enableAutoBanFixture(t)
	old := operation_setting.GetAuditSecuritySetting()
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"notification_enabled": "true", "notification_emails": "recipient@example.com"}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"notification_enabled": fmt.Sprint(old.NotificationEnabled), "notification_emails": old.NotificationEmails}))
	})
	ctx := context.Background()
	alert := model.SecurityAlert{ActorID: 501, Rule: "session_reuse", Version: 2, Severity: 3}
	require.NoError(t, db.Create(&alert).Error)
	action := model.SecurityAction{AlertID: alert.ID, TargetUserID: 501, Source: "automatic", Action: "disable_account", Outcome: "committed", Result: `{"auth_version":2}`}
	require.NoError(t, db.Create(&action).Error)
	delivery := model.SecurityDelivery{AlertID: alert.ID, Version: 2, Status: "pending", Kind: "email", Destination: "recipient@example.com", CreatedAt: time.Now().Unix()}
	require.NoError(t, db.Create(&delivery).Error)
	w := worker{db: db}
	require.NoError(t, w.deliver(ctx, operation_setting.GetAuditSecuritySetting()))
	require.NoError(t, db.First(&delivery, delivery.ID).Error)
	require.Equal(t, "pending", delivery.Status)
	require.Equal(t, "account_response", delivery.WaitReason)
	require.Zero(t, delivery.Attempts)
	// The fixture server captures the message without sending any real email.
	address, commands := smtpFixture(t, false)
	host, port, err := net.SplitHostPort(address)
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{"EmailProvider": "smtp", "SMTPServer": host, "SMTPPort": port, "SMTPFrom": "sender@example.com"}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	require.NoError(t, db.Model(&action).Update("outcome", "succeeded").Error)
	require.NoError(t, db.Model(&delivery).Update("next_attempt_at", 0).Error)
	require.NoError(t, w.deliver(ctx, operation_setting.GetAuditSecuritySetting()))
	transcript := strings.Join(<-commands, "\n")
	require.Contains(t, transcript, "succeeded")
	require.Contains(t, transcript, "target_user_id")
	require.NotContains(t, transcript, "auth_version")
	require.NoError(t, db.First(&delivery, delivery.ID).Error)
	require.Equal(t, "sent", delivery.Status)
	require.Equal(t, 1, delivery.Attempts)
}

func TestAccountResponseRechecksPauseAndStaffScope(t *testing.T) {
	for _, tc := range []struct{ name, setting, value, reason string }{
		{"paused", "suspended", "true", "disabled"},
		{"master_off", "auto_ban_enabled", "false", "disabled"},
		{"monitor_off", "enabled", "false", "disabled"},
		{"capture_off", "capture_enabled", "false", "disabled"},
		{"employee", "", "", "excluded_account"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newWorkerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}))
			cfg := enableAutoBanFixture(t)
			ctx := context.Background()
			require.NoError(t, db.Create(&model.User{Id: 401, Username: "scope-fixture", AffCode: "401", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}).Error)
			if tc.name == "employee" {
				require.NoError(t, db.Create(&model.EmployeeProfile{UserId: 401, Status: 1}).Error)
			}
			state, err := model.ClaimAuditSecurity(ctx, db, "scope-fixture")
			require.NoError(t, err)
			event := compromiseEvent("scope-replay", 401, time.Now().Unix(), "8.8.8.8", "auth.session_refresh_reuse")
			require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{event}, state, cfg))
			if tc.setting != "" {
				require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{tc.setting: tc.value}))
			}
			w := worker{db: db}
			require.NoError(t, w.processAccountResponses(ctx))
			var action model.SecurityAction
			require.NoError(t, db.Where("action = ?", "disable_account").Take(&action).Error)
			require.Equal(t, "skipped", action.Outcome)
			require.Equal(t, tc.reason, action.ReasonCode)
			var user model.User
			require.NoError(t, db.First(&user, 401).Error)
			require.Equal(t, common.UserStatusEnabled, user.Status)
			require.EqualValues(t, 1, user.AuthVersion)
		})
	}
}

func TestAutomaticResponseAcceptsFreshEvidenceAfterUnexecutedSkip(t *testing.T) {
	for _, reason := range []string{"expired", "incomplete_evidence", "disabled", "excluded_account"} {
		t.Run(reason, func(t *testing.T) {
			db := newWorkerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}))
			cfg := enableAutoBanFixture(t)
			oldRedis := common.RedisEnabled
			common.RedisEnabled = false
			t.Cleanup(func() { common.RedisEnabled = oldRedis })
			ctx := context.Background()
			now := time.Now().Unix()
			require.NoError(t, db.Create(&model.User{Id: 1901, Username: "fresh-after-skip", AffCode: "1901", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}).Error)
			state, err := model.ClaimAuditSecurity(ctx, db, "fresh-after-skip")
			require.NoError(t, err)
			oldEvent := compromiseEvent("old-response-event", 1901, now-121, "8.8.8.8", "auth.session_refresh_reuse")
			oldEvent.ActorRole = 0
			require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{oldEvent}, state, cfg))
			var first model.SecurityAction
			require.NoError(t, db.Where("target_user_id = ?", 1901).Take(&first).Error)
			require.Equal(t, "skipped", first.Outcome)
			require.NoError(t, db.Model(&first).Update("reason_code", reason).Error)
			fresh := compromiseEvent("fresh-response-event", 1901, now-1, "8.8.8.8", "auth.session_refresh_reuse")
			fresh.ActorRole = 0
			require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{fresh}, state, cfg))
			require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{oldEvent, fresh}, state, cfg))
			var actions []model.SecurityAction
			require.NoError(t, db.Where("target_user_id = ?", 1901).Order("id").Find(&actions).Error)
			require.Len(t, actions, 2)
			require.Equal(t, first.ID, actions[0].ID)
			require.Equal(t, "skipped", actions[0].Outcome)
			require.Equal(t, reason, actions[0].ReasonCode)
			require.Equal(t, oldEvent.EventID, actions[0].EventID)
			require.Equal(t, fresh.EventID, actions[1].EventID)
			require.Equal(t, "pending", actions[1].Outcome)
			require.Equal(t, actions[0].AlertID, actions[1].AlertID)
			w := worker{db: db}
			require.NoError(t, w.processAccountResponses(ctx))
			var user model.User
			require.NoError(t, db.First(&user, 1901).Error)
			require.Equal(t, common.UserStatusDisabled, user.Status)
			require.EqualValues(t, 2, user.AuthVersion)
			// A later hit in this episode cannot undo manual recovery.
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", 1901).Updates(map[string]any{"status": common.UserStatusEnabled, "auth_version": 3}).Error)
			fresh.EventID, fresh.RequestID, fresh.At = "after-recovery", "after-recovery", now
			require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{fresh}, state, cfg))
			require.NoError(t, w.processAccountResponses(ctx))
			require.NoError(t, db.First(&user, 1901).Error)
			require.Equal(t, common.UserStatusEnabled, user.Status)
			require.EqualValues(t, 3, user.AuthVersion)
		})
	}
}

func TestAutomaticResponsePreservesPendingAndFinalReceipts(t *testing.T) {
	for _, tc := range []struct{ outcome, reason string }{
		{"pending", ""}, {"committed", ""}, {"sync_pending", "sync_pending"},
		{"succeeded", ""}, {"partial", "sync_failed"}, {"unknown", "unknown"},
		{"skipped", "already_disabled"}, {"skipped", "protected_account"},
	} {
		t.Run(tc.outcome+tc.reason, func(t *testing.T) {
			db := newWorkerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.User{}))
			require.NoError(t, db.Create(&model.User{Id: 1902, Username: "retained", AffCode: "retained", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}).Error)
			cfg := enableAutoBanFixture(t)
			now := time.Now().Unix()
			ctx := context.Background()
			state, err := model.ClaimAuditSecurity(ctx, db, "retained-receipt")
			require.NoError(t, err)
			event := compromiseEvent("retained-first", 1902, now-2, "8.8.8.8", "auth.session_refresh_reuse")
			require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{event}, state, cfg))
			var original model.SecurityAction
			require.NoError(t, db.Where("target_user_id = ?", 1902).Take(&original).Error)
			require.NoError(t, db.Model(&original).Updates(map[string]any{"outcome": tc.outcome, "reason_code": tc.reason}).Error)
			event.EventID, event.RequestID, event.At = "retained-next", "retained-next", now-1
			require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{event}, state, cfg))
			var actions []model.SecurityAction
			require.NoError(t, db.Where("target_user_id = ?", 1902).Find(&actions).Error)
			require.Len(t, actions, 1)
			require.Equal(t, original.EventID, actions[0].EventID)
			require.Equal(t, tc.outcome, actions[0].Outcome)
		})
	}
}

func TestAutomaticResponseDoesNotReviveSkippedReceiptWithOlderEvidence(t *testing.T) {
	db := newWorkerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, db.Create(&model.User{Id: 1903, Username: "older", AffCode: "older", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}).Error)
	cfg := enableAutoBanFixture(t)
	now := time.Now().Unix()
	ctx := context.Background()
	state, err := model.ClaimAuditSecurity(ctx, db, "older-evidence")
	require.NoError(t, err)
	event := compromiseEvent("newer-skipped", 1903, now-1, "8.8.8.8", "auth.session_refresh_reuse")
	require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{event}, state, cfg))
	var original model.SecurityAction
	require.NoError(t, db.Where("target_user_id = ?", 1903).Take(&original).Error)
	require.NoError(t, db.Model(&original).Updates(map[string]any{"outcome": "skipped", "reason_code": "incomplete_evidence"}).Error)
	event.EventID, event.RequestID, event.At = "late-older-event", "late-older-event", now-2
	require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{event}, state, cfg))
	var actions []model.SecurityAction
	require.NoError(t, db.Where("target_user_id = ?", 1903).Find(&actions).Error)
	require.Len(t, actions, 1)
	require.Equal(t, original.EventID, actions[0].EventID)
	require.Equal(t, "skipped", actions[0].Outcome)
}
