package auditsecurity

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestSingleEventResponseIgnoresUnrelatedHistoryTruncation(t *testing.T) {
	for _, ruleID := range []string{"risky_ip_login", "risky_ip_sensitive_operation", "new_source_sensitive_operation"} {
		t.Run(ruleID, func(t *testing.T) {
			db, initial := newCredentialCleanupFixture(t, false, false, false)
			require.Empty(t, initial)
			cfg := operation_setting.GetAuditSecuritySetting()
			oldIP, oldProviders := cfg.IPIntelligenceEnabled, cfg.IPIntelligenceProvidersJSON
			healthMu.RLock()
			oldTruncated := health.HistoryTruncated
			healthMu.RUnlock()
			t.Cleanup(func() {
				require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"ip_intelligence_enabled": fmt.Sprint(oldIP), "ip_intelligence_providers_json": oldProviders}))
				healthMu.Lock()
				health.HistoryTruncated = oldTruncated
				healthMu.Unlock()
			})
			rules, err := operation_setting.ParseAutoBanRules(cfg.AutoBanRulesJSON)
			require.NoError(t, err)
			for i := range rules {
				selected := rules[i].ID == ruleID
				rules[i].Selected, rules[i].ClearAPIKeys, rules[i].ClearAccessTokens = selected, selected, selected
			}
			raw, err := common.Marshal(rules)
			require.NoError(t, err)
			providers := `[{"id":"selfhost","name":"Fixture","type":"custom_ipapi","enabled":true,"priority":1,"endpoint":"https://ip.example.com","capabilities":["abuse"],"daily_limit":1000}]`
			require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"auto_ban_rules_json": string(raw), "ip_intelligence_enabled": fmt.Sprint(ruleID != "new_source_sensitive_operation"), "ip_intelligence_providers_json": providers}))
			cfg = operation_setting.GetAuditSecuritySetting()
			ctx := context.Background()
			state, err := model.ClaimAuditSecurity(ctx, db, "credential-fixture")
			require.NoError(t, err)
			now := time.Now().Unix()
			if ruleID == "new_source_sensitive_operation" {
				history := compromiseEvent("established-management", 901, now-86401, "1.1.1.1", "token.key_view")
				require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{history}, state, cfg))
			}
			action := "access_token.generate"
			if ruleID == "risky_ip_login" {
				action = "login"
			}
			event := compromiseEvent("independent-trigger", 901, now, "8.8.8.8", action)
			var facts Facts
			require.NoError(t, common.UnmarshalJsonStr(event.Facts, &facts))
			facts.Identity.AuthVersion = 7
			if ruleID != "new_source_sensitive_operation" {
				yes := true
				facts.IPIntelligence = &IPIntelligenceSnapshot{IsAbuser: &yes, ObservedAt: now, ExpiresAt: now + 120, Providers: []string{"selfhost"}, FieldProviders: map[string]string{"abuse": "selfhost"}, Sources: []IPDataSource{{ProviderID: "selfhost", Source: "fixture", Capability: "abuse", DataUpdatedAt: now - 5, ExpiresAt: now + 120}}, Evidence: []IPDataEvidence{{ProviderID: "selfhost", Source: "fixture", Capability: "abuse", MatchedNetwork: event.IP, DataUpdatedAt: now - 5, ExpiresAt: now + 120}}}
				event.IPCheckState = "done"
			}
			raw, err = common.Marshal(facts)
			require.NoError(t, err)
			event.Facts = string(raw)
			noise := make([]model.SecurityEvent, 1001)
			for i := range noise {
				noise[i] = compromiseEvent(fmt.Sprintf("unrelated-history-%04d", i), 901, now-30, "1.1.1.1", "access_token.generate")
				if action == "login" {
					noise[i].ActorID, noise[i].ActorRole = 0, 0
					noise[i].Action, noise[i].Success, noise[i].Reason = "auth.login_failed", false, "invalid_credentials"
					noise[i].Visibility = common.RoleRootUser
				}
				noise[i].SubjectRef = event.SubjectRef
			}
			require.NoError(t, db.CreateInBatches(noise, 100).Error)
			history, truncated, err := loadCorrelationHistory(db, event, highRisk(event, facts) > 0)
			require.NoError(t, err)
			require.True(t, truncated, "the unrelated generic correlation really exceeds its limit")
			require.Len(t, history, 1000)
			require.NoError(t, db.Create(&model.Token{UserId: 901, Key: "evidence-scope-fixture-key", Status: common.TokenStatusEnabled}).Error)
			require.NoError(t, db.Create(&model.UserAccessToken{UserId: 901, TokenHash: "evidence-scope-fixture-pat"}).Error)
			require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{event}, state, cfg))
			var alert model.SecurityAlert
			require.NoError(t, db.Where("rule = ?", ruleID).Take(&alert).Error)
			wantSeverity := 2
			if ruleID == "new_source_sensitive_operation" {
				wantSeverity = 1
			}
			require.Equal(t, wantSeverity, alert.Severity)
			var snapshot model.SecurityFindingSnapshot
			require.NoError(t, db.First(&snapshot, alert.LatestFindingID).Error)
			require.False(t, snapshot.Truncated, "the independent rule retains its complete trigger evidence")
			var responses []model.SecurityAction
			require.NoError(t, db.Where("alert_id = ?", alert.ID).Order("id").Find(&responses).Error)
			require.Len(t, responses, 3)
			for _, response := range responses {
				require.Equal(t, "pending", response.Outcome)
			}
			w := worker{db: db}
			require.NoError(t, w.processAccountResponses(ctx))
			responses = nil
			require.NoError(t, db.Where("alert_id = ?", alert.ID).Order("id").Find(&responses).Error)
			for _, response := range responses {
				require.Equal(t, "succeeded", response.Outcome, response.Action)
			}
			var user model.User
			require.NoError(t, db.First(&user, 901).Error)
			require.Equal(t, common.UserStatusDisabled, user.Status)
			require.Nil(t, user.AccessToken)
			var count int64
			require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", 901).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, db.Model(&model.UserAccessToken{}).Where("user_id = ?", 901).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestMultiSourceKeyAccessUsesEventTimeWithinSameSecond(t *testing.T) {
	for _, loginPrefix := range []string{"00-login", "99-login"} {
		t.Run(loginPrefix, func(t *testing.T) {
			var events []model.SecurityEvent
			for i := range 5 {
				events = append(events, compromiseEvent(fmt.Sprintf("%s-%d", loginPrefix, i), 7, 1000, fmt.Sprintf("8.8.8.%d", i+1), "login"))
			}
			for i := range 3 {
				events = append(events, compromiseEvent(fmt.Sprintf("50-key-%d", i), 7, 1000, fmt.Sprintf("8.8.8.%d", i+1), "token.key_view"))
			}
			current := events[len(events)-1]
			findings := EvaluateAccountCompromise(current, events, operation_setting.DefaultAuditSecuritySetting(), false)
			require.Len(t, findings, 1, "random event identifiers cannot change a same-second match")
			require.Equal(t, "multi_source_key_access", findings[0].Rule)
			require.Equal(t, 3, findings[0].Count)
			for i := range 5 {
				events[i].At = current.At + 1
			}
			require.Empty(t, EvaluateAccountCompromise(current, events, operation_setting.DefaultAuditSecuritySetting(), false), "a later login cannot authorize an earlier key read")
		})
	}
}

func TestIncompleteCompromiseHistoryStillBlocksAutomaticResponse(t *testing.T) {
	db, initial := newCredentialCleanupFixture(t, false, false, false)
	require.Empty(t, initial)
	healthMu.RLock()
	oldTruncated := health.HistoryTruncated
	healthMu.RUnlock()
	t.Cleanup(func() {
		healthMu.Lock()
		health.HistoryTruncated = oldTruncated
		healthMu.Unlock()
	})
	cfg := operation_setting.GetAuditSecuritySetting()
	rules, err := operation_setting.ParseAutoBanRules(cfg.AutoBanRulesJSON)
	require.NoError(t, err)
	for i := range rules {
		rules[i].Selected = rules[i].ID == "multi_source_key_access"
	}
	raw, err := common.Marshal(rules)
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"auto_ban_rules_json": string(raw)}))
	cfg = operation_setting.GetAuditSecuritySetting()
	now := time.Now().Unix()
	var history []model.SecurityEvent
	for i := range 1001 {
		history = append(history, compromiseEvent(fmt.Sprintf("bounded-login-%04d", i), 901, now-60, "1.1.1.1", "login"))
	}
	for i := range 5 {
		history = append(history, compromiseEvent(fmt.Sprintf("matching-login-%d", i), 901, now-5, fmt.Sprintf("8.8.8.%d", i+1), "login"))
	}
	for i := range 2 {
		history = append(history, compromiseEvent(fmt.Sprintf("matching-key-%d", i), 901, now-2, fmt.Sprintf("8.8.8.%d", i+1), "token.key_view"))
	}
	event := compromiseEvent("bounded-trigger", 901, now, "8.8.8.3", "token.key_view")
	history = append(history, event)
	for i := range history {
		var facts Facts
		require.NoError(t, common.UnmarshalJsonStr(history[i].Facts, &facts))
		facts.Identity.AuthVersion = 7
		raw, err := common.Marshal(facts)
		require.NoError(t, err)
		history[i].Facts = string(raw)
	}
	event = history[len(history)-1]
	require.NoError(t, db.CreateInBatches(history[:len(history)-1], 100).Error)
	ctx := context.Background()
	state, err := model.ClaimAuditSecurity(ctx, db, "credential-fixture")
	require.NoError(t, err)
	require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{event}, state, cfg))
	var alert model.SecurityAlert
	require.NoError(t, db.Where("rule = ?", "multi_source_key_access").Take(&alert).Error)
	var snapshot model.SecurityFindingSnapshot
	require.NoError(t, db.First(&snapshot, alert.LatestFindingID).Error)
	require.True(t, snapshot.Truncated)
	var response model.SecurityAction
	require.NoError(t, db.Where("alert_id = ? AND action = ?", alert.ID, "disable_account").Take(&response).Error)
	require.Equal(t, "skipped", response.Outcome)
	require.Equal(t, "incomplete_evidence", response.ReasonCode)
	var user model.User
	require.NoError(t, db.First(&user, 901).Error)
	require.Equal(t, common.UserStatusEnabled, user.Status)
}
