package auditsecurity

import (
	"context"
	"errors"
	"fmt"
	"net"
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

func TestLateLoginsRepairKeyFindingsWithoutDuplicateResponses(t *testing.T) {
	db := newWorkerTestDB(t)
	cfg := enableAutoBanFixture(t)
	ctx := context.Background()
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}))
	require.NoError(t, db.Create(&model.User{Id: 801, Username: "late-user", AffCode: "late-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}).Error)
	state, err := model.ClaimAuditSecurity(ctx, db, "late-login")
	require.NoError(t, err)
	now := time.Now().Unix()
	var logins, keys []model.SecurityEvent
	for i := range 5 {
		ip := fmt.Sprintf("8.8.8.%d", i+1)
		logins = append(logins, compromiseEvent(fmt.Sprint("late-login-", i), 801, now-20, ip, "login"))
		if i < 3 {
			keys = append(keys, compromiseEvent(fmt.Sprint("late-key-", i), 801, now-10+int64(i), ip, "token.key_view"))
		}
	}
	require.NoError(t, Apply(ctx, db, state, keys, state, cfg))
	var count int64
	require.NoError(t, db.Model(&model.SecurityAlert{}).Where("rule = ?", "multi_source_key_access").Count(&count).Error)
	require.Zero(t, count)
	reads := 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:late-window-budget", func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_security_events" {
			reads++
		}
	}))
	require.NoError(t, Apply(ctx, db, state, logins, state, cfg))
	require.NoError(t, db.Callback().Query().Remove("test:late-window-budget"))
	require.LessOrEqual(t, reads, 22, "late correlation must not read history separately for every key event")
	var alert model.SecurityAlert
	require.NoError(t, db.Where("rule = ?", "multi_source_key_access").Take(&alert).Error)
	require.EqualValues(t, 1, alert.Occurrences)
	require.NoError(t, db.Model(&model.SecurityAction{}).Where("action = ?", "disable_account").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, Apply(ctx, db, state, append(logins, keys...), state, cfg))
	var after model.SecurityAlert
	require.NoError(t, db.First(&after, alert.ID).Error)
	require.Equal(t, alert.Occurrences, after.Occurrences)
	require.NoError(t, db.Model(&model.SecurityAction{}).Where("action = ?", "disable_account").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestStoppingCommittedCleanupFinishesOnlyItsPreparedBatch(t *testing.T) {
	db, actions := newCredentialCleanupFixture(t, true, false, false)
	require.NoError(t, db.AutoMigrate(&model.UserSession{}))
	require.NoError(t, db.Create(&model.User{Id: 902, Username: "stop-root", AffCode: "stop-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}).Error)
	require.NoError(t, db.Create(&model.UserSession{SID: "stop-root", UserID: 902, UserAuthVersion: 1, Version: 1, Status: model.UserSessionStatusActive, ExpiresAt: time.Now().Unix() + 300}).Error)
	for i := range 101 {
		require.NoError(t, db.Create(&model.Token{UserId: 901, Key: fmt.Sprint("stop-batch-", i), Status: common.TokenStatusEnabled}).Error)
	}
	common.RedisEnabled = true
	broken := redis.NewClient(&redis.Options{MaxRetries: -1, Dialer: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("fixture cache outage")
	}})
	t.Cleanup(func() { _ = broken.Close() })
	prepared, err := model.ProcessSecurityCredentialCleanup(context.Background(), db, broken, actions[0].ID)
	require.Error(t, err)
	require.Equal(t, "sync_pending", prepared.Outcome)
	var alert model.SecurityAlert
	require.NoError(t, db.First(&alert, prepared.AlertID).Error)
	_, err = model.StopSecurityResponseGroup(context.Background(), db, model.AuthSessionIdentity{UserID: 902, UserAuthVersion: 1, SessionID: "stop-root", SessionVersion: 1}, alert.ID, alert.Version, *prepared.ResponseGroupID, "stop-prepared-batch")
	require.NoError(t, err)
	common.RedisEnabled = false
	finished, err := model.ProcessSecurityCredentialCleanup(context.Background(), db, nil, prepared.ID)
	require.NoError(t, err)
	require.Equal(t, "clearing", finished.Outcome)
	stopped, err := model.ProcessSecurityCredentialCleanup(context.Background(), db, nil, prepared.ID)
	require.NoError(t, err)
	require.Equal(t, "partial", stopped.Outcome)
	require.Equal(t, "stopped", stopped.ReasonCode)
	require.EqualValues(t, 100, *stopped.ResponseSummary().AffectedCount)
	var remaining int64
	require.NoError(t, db.Model(&model.Token{}).Where("user_id = ? AND status = ?", 901, common.TokenStatusEnabled).Count(&remaining).Error)
	require.EqualValues(t, 1, remaining)
	var group model.SecurityResponseGroup
	require.NoError(t, db.First(&group, *prepared.ResponseGroupID).Error)
	require.Equal(t, "complete", group.State)
	require.Nil(t, group.ActiveKey)
}

func TestRecoveryBeforeEnqueueCannotAuthorizeUsingCurrentVersion(t *testing.T) {
	db := newWorkerTestDB(t)
	cfg := enableAutoBanFixture(t)
	ctx := context.Background()
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}))
	require.NoError(t, db.Create(&model.User{Id: 802, Username: "recovered", AffCode: "recovered", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 2}).Error)
	state, err := model.ClaimAuditSecurity(ctx, db, "recovery-before-queue")
	require.NoError(t, err)
	event := compromiseEvent("old-identity", 802, time.Now().Unix(), "8.8.8.8", "auth.session_refresh_reuse")
	require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{event}, state, cfg))
	var action model.SecurityAction
	require.NoError(t, db.Where("target_user_id = ?", 802).Take(&action).Error)
	action, err = model.DisableUserForSecurity(ctx, db, action.ID)
	require.NoError(t, err)
	require.Equal(t, "superseded", action.ReasonCode)
	var user model.User
	require.NoError(t, db.First(&user, 802).Error)
	require.Equal(t, common.UserStatusEnabled, user.Status)
	require.EqualValues(t, 2, user.AuthVersion)
}

func TestNewSourceSingleKeyCreationAndIPRetryKeepOriginalIdentity(t *testing.T) {
	db := newWorkerTestDB(t)
	cfg := enableAutoBanFixture(t)
	ctx := context.Background()
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}))
	now := time.Now().Unix()
	state, err := model.ClaimAuditSecurity(ctx, db, "single-key")
	require.NoError(t, err)
	history := compromiseEvent("known-management", 803, now-86401, "1.1.1.1", "token.key_view")
	require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{history}, state, cfg))
	cfg.IPIntelligenceEnabled = true
	event := compromiseEvent("single-new-key", 803, now-1, "8.8.8.8", "token.create")
	require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{event}, state, cfg))
	var alert model.SecurityAlert
	require.NoError(t, db.Where("rule = ?", "new_source_sensitive_operation").Take(&alert).Error)
	require.EqualValues(t, 1, alert.MatchedCount)
	var actionCount int64
	require.NoError(t, db.Model(&model.SecurityAction{}).Where("alert_id = ?", alert.ID).Count(&actionCount).Error)
	require.Zero(t, actionCount, "new rules default to alerts only")
	var receipt model.SecurityEvent
	require.NoError(t, db.Where("event_id = ?", event.EventID).Take(&receipt).Error)
	var facts Facts
	require.NoError(t, common.UnmarshalJsonStr(receipt.Facts, &facts))
	originalPolicy, observed := facts.LivePolicyAt, facts.FirstObservedAt
	require.NotNil(t, facts.SourceBaseline)
	originalSource := *facts.SourceBaseline
	require.Equal(t, history.At, originalSource.BaselineFirstSeen)
	require.Equal(t, event.At, originalSource.SourceFirstSeen)
	require.NoError(t, db.Model(&model.SecurityBaseline{}).Where("account_id = ?", 803).Update("last_seen", now+600).Error)
	yes := true
	facts.IPIntelligence = &IPIntelligenceSnapshot{IsAbuser: &yes, ObservedAt: now, ExpiresAt: now + 60, Providers: []string{"selfhost"}, FieldProviders: map[string]string{"abuse": "selfhost"}, Sources: []IPDataSource{{ProviderID: "selfhost", Source: "fixture", Capability: "abuse", DataUpdatedAt: now - 5, ExpiresAt: now + 60}}, Evidence: []IPDataEvidence{{ProviderID: "selfhost", Source: "fixture", Capability: "abuse", MatchedNetwork: event.IP, DataUpdatedAt: now - 5, ExpiresAt: now + 60}}}
	raw, err := common.Marshal(facts)
	require.NoError(t, err)
	receipt.Facts = string(raw)
	receipt.IPCheckState = "done"
	state.Name = "ip_retry"
	require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{receipt}, state, cfg))
	var risky model.SecurityAlert
	require.NoError(t, db.Where("rule = ?", "risky_ip_sensitive_operation").Take(&risky).Error)
	require.NoError(t, db.Where("event_id = ?", event.EventID).Take(&receipt).Error)
	require.NoError(t, common.UnmarshalJsonStr(receipt.Facts, &facts))
	require.Equal(t, originalPolicy, facts.LivePolicyAt)
	require.Equal(t, observed, facts.FirstObservedAt)
	require.Equal(t, &originalSource, facts.SourceBaseline, "IP enrichment must retain the source proof observed with the event")
	detail, err := GetAlert(ctx, db, common.RoleRootUser, risky.ID)
	require.NoError(t, err)
	require.Len(t, detail.Events, 1)
	for _, e := range detail.Events {
		require.NotContains(t, e.Facts, "auth_version")
		require.NotContains(t, e.Facts, "first_observed_at")
		require.NotContains(t, e.Facts, "source_baseline")
	}
}

func TestCrossRuleCleanupInheritsOnlyGroupBanAndSharesProjection(t *testing.T) {
	db, actions := newCredentialCleanupFixture(t, true, false, true)
	require.Len(t, actions, 2)
	var group model.SecurityResponseGroup
	require.NoError(t, db.First(&group, *actions[0].ResponseGroupID).Error)
	// A different rule links to the same work; no second ban or cleanup is created.
	var event model.SecurityEvent
	require.NoError(t, db.Where("event_id = ?", actions[0].EventID).Take(&event).Error)
	cfg := operation_setting.GetAuditSecuritySetting()
	rules, err := operation_setting.ParseAutoBanRules(cfg.AutoBanRulesJSON)
	require.NoError(t, err)
	raw, err := common.Marshal(rules)
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"auto_ban_rules_json": string(raw)}))
	alert := model.SecurityAlert{ActorID: 901, Version: 1, Rule: "multi_source_key_access"}
	require.NoError(t, db.Create(&alert).Error)
	require.NoError(t, queueAutomaticAccountResponse(db, alert, model.SecurityFinding{Rule: alert.Rule}, event, model.SecurityState{Name: "scanner"}, cfg, time.Now().Unix(), false, &group))
	token := model.Token{UserId: 901, Key: "group-single-key", Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&token).Error)
	ctx := context.Background()
	ban, err := model.DisableUserForSecurity(ctx, db, actions[0].ID)
	require.NoError(t, err)
	require.Equal(t, "committed", ban.Outcome)
	cleaned, err := model.ProcessSecurityCredentialCleanup(ctx, db, nil, actions[1].ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", cleaned.Outcome)
	require.EqualValues(t, 1, *cleaned.ResponseSummary().AffectedCount)
	views := []model.SecurityAlert{alert}
	require.NoError(t, loadAlertResponses(ctx, db, views))
	require.Len(t, views[0].Responses, 2)
	require.Equal(t, actions[1].ID, views[0].Responses[1].ID)
	var count int64
	require.NoError(t, db.Model(&model.SecurityAction{}).Where("response_group_id = ?", group.ID).Count(&count).Error)
	require.EqualValues(t, 2, count)
}

func TestCompletedCleanupOnlyReopensForANewerAuthenticatedEvent(t *testing.T) {
	db, actions := newCredentialCleanupFixture(t, true, false, false)
	ctx := context.Background()
	first, err := model.ProcessSecurityCredentialCleanup(ctx, db, nil, actions[0].ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", first.Outcome)
	var original model.SecurityEvent
	require.NoError(t, db.Where("event_id = ?", first.EventID).Take(&original).Error)
	// Enrichment of the same event can never reopen a terminal group, even if
	// it discovers another rule. A strictly newer event has its own receipt.
	var facts Facts
	require.NoError(t, common.UnmarshalJsonStr(original.Facts, &facts))
	group, err := model.ClaimSecurityResponseGroup(db, original, facts.LivePolicyAt, *facts.Identity, time.Now().Unix())
	require.NoError(t, err)
	require.Equal(t, "complete", group.State)
	next := original
	next.EventID = "new-authenticated-trigger"
	next.ResponseGroupID = nil
	next.At = original.At + 1
	require.NoError(t, db.Create(&next).Error)
	newGroup, err := model.ClaimSecurityResponseGroup(db, next, facts.LivePolicyAt, *facts.Identity, time.Now().Unix())
	require.NoError(t, err)
	require.NotEqual(t, group.ID, newGroup.ID)
	require.Equal(t, "active", newGroup.State)
	older := original
	older.EventID = "late-old-trigger"
	older.ResponseGroupID = nil
	require.NoError(t, db.Create(&older).Error)
	joined, err := model.ClaimSecurityResponseGroup(db, older, facts.LivePolicyAt, *facts.Identity, time.Now().Unix())
	require.NoError(t, err)
	require.Equal(t, newGroup.ID, joined.ID)
	require.NoError(t, model.FinishSecurityResponseGroup(db, model.SecurityAction{ResponseGroupID: &newGroup.ID}))
	older.EventID = "late-after-completion"
	older.ResponseGroupID = nil
	require.NoError(t, db.Create(&older).Error)
	joined, err = model.ClaimSecurityResponseGroup(db, older, facts.LivePolicyAt, *facts.Identity, time.Now().Unix())
	require.NoError(t, err)
	require.Nil(t, joined)
}

func TestDeletingOrReviewingAnAlertCannotStopSharedAutomaticWork(t *testing.T) {
	db, actions := newCredentialCleanupFixture(t, true, false, false)
	ctx := context.Background()
	var alert model.SecurityAlert
	require.NoError(t, db.First(&alert, actions[0].AlertID).Error)
	require.ErrorIs(t, DeleteAlert(ctx, db, common.RoleRootUser, alert.ID, alert.Version), model.ErrSecurityConflict)
	now := time.Now().Unix()
	require.NoError(t, db.Model(&alert).Updates(map[string]any{"status": "false_positive", "closed_at": now}).Error)
	cleanup, err := CleanupClosedAlerts(ctx, db, common.RoleRootUser, now)
	require.NoError(t, err)
	require.Zero(t, cleanup.Queued)
	require.NoError(t, db.First(&alert, alert.ID).Error)
	require.Empty(t, alert.PurgeState)
	action, err := model.ProcessSecurityCredentialCleanup(ctx, db, nil, actions[0].ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", action.Outcome)
	require.NoError(t, db.First(&alert, alert.ID).Error)
	require.NoError(t, DeleteAlert(ctx, db, common.RoleRootUser, alert.ID, alert.Version))
}

func TestSingleRiskyIPKeyCreationCanClearWithoutBanning(t *testing.T) {
	db := newWorkerTestDB(t)
	cfg := enableAutoBanFixture(t)
	ctx := context.Background()
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}, &model.Token{}))
	require.NoError(t, db.Create(&model.User{Id: 804, Username: "single-risky", AffCode: "single-risky", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}).Error)
	token := model.Token{UserId: 804, Key: "only-model-key", Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&token).Error)
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	oldProviders, oldIP := cfg.IPIntelligenceProvidersJSON, cfg.IPIntelligenceEnabled
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"ip_intelligence_enabled": fmt.Sprint(oldIP), "ip_intelligence_providers_json": oldProviders}))
	})
	providers := `[{"id":"selfhost","name":"Fixture","type":"custom_ipapi","enabled":true,"priority":1,"endpoint":"https://ip.example.com","capabilities":["abuse"],"daily_limit":1000}]`
	_, err := operation_setting.ParseIPIntelligenceProviders(providers)
	require.NoError(t, err)
	rules, err := operation_setting.ParseAutoBanRules(cfg.AutoBanRulesJSON)
	require.NoError(t, err)
	for i := range rules {
		rules[i].Selected = false
		rules[i].ClearAPIKeys = rules[i].ID == "risky_ip_sensitive_operation"
	}
	raw, err := common.Marshal(rules)
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"auto_ban_rules_json": string(raw), "ip_intelligence_enabled": "true", "ip_intelligence_providers_json": providers}))
	cfg = operation_setting.GetAuditSecuritySetting()
	state, err := model.ClaimAuditSecurity(ctx, db, "single-risky")
	require.NoError(t, err)
	now := time.Now().Unix()
	event := compromiseEvent("single-risky-key", 804, now-1, "8.8.8.8", "token.create")
	var facts Facts
	require.NoError(t, common.UnmarshalJsonStr(event.Facts, &facts))
	yes := true
	facts.IPIntelligence = &IPIntelligenceSnapshot{IsAbuser: &yes, ObservedAt: now, ExpiresAt: now + 60, Providers: []string{"selfhost"}, FieldProviders: map[string]string{"abuse": "selfhost"}, Sources: []IPDataSource{{ProviderID: "selfhost", Source: "fixture", Capability: "abuse", DataUpdatedAt: now - 5, ExpiresAt: now + 60}}, Evidence: []IPDataEvidence{{ProviderID: "selfhost", Source: "fixture", Capability: "abuse", MatchedNetwork: event.IP, DataUpdatedAt: now - 5, ExpiresAt: now + 60}}}
	raw, err = common.Marshal(facts)
	require.NoError(t, err)
	event.Facts = string(raw)
	event.IPCheckState = "done"
	require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{event}, state, cfg))
	var actions []model.SecurityAction
	require.NoError(t, db.Find(&actions).Error)
	require.Len(t, actions, 1)
	require.Equal(t, "clear_api_keys", actions[0].Action)
	require.Equal(t, "pending", actions[0].Outcome)
	action, err := model.ProcessSecurityCredentialCleanup(ctx, db, nil, actions[0].ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", action.Outcome)
	require.EqualValues(t, 1, *action.ResponseSummary().AffectedCount)
	var user model.User
	require.NoError(t, db.First(&user, 804).Error)
	require.Equal(t, common.UserStatusEnabled, user.Status)
}
