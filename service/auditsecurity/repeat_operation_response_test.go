package auditsecurity

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestRepeatOperationConfirmationPreservesRiskResponses(t *testing.T) {
	for _, ruleID := range []string{"risky_ip_sensitive_operation", "new_source_sensitive_operation"} {
		t.Run(ruleID, func(t *testing.T) {
			db, initial := newCredentialCleanupFixtureForRole(t, false, false, false, common.RoleAdminUser)
			require.Empty(t, initial)
			cfg := operation_setting.GetAuditSecuritySetting()
			oldIP, oldProviders := cfg.IPIntelligenceEnabled, cfg.IPIntelligenceProvidersJSON
			t.Cleanup(func() {
				require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"ip_intelligence_enabled": fmt.Sprint(oldIP), "ip_intelligence_providers_json": oldProviders}))
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
			require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"auto_ban_rules_json": string(raw), "auto_ban_include_staff": "true", "ip_intelligence_enabled": fmt.Sprint(ruleID == "risky_ip_sensitive_operation"), "ip_intelligence_providers_json": providers}))
			cfg = operation_setting.GetAuditSecuritySetting()
			state, err := model.ClaimAuditSecurity(t.Context(), db, "credential-fixture")
			require.NoError(t, err)
			now := time.Now().Unix()
			first := repeatQuotaAudit(t, "response-repeat-first", now-2)
			first.UserId, first.Other.Op.Identity.ActorID, first.Other.Op.Identity.AuthVersion = 901, 901, 7
			if ruleID == "new_source_sensitive_operation" {
				first.Ip = "1.1.1.1"
				old := first
				old.EventId, old.RequestId, old.CreatedAt = "response-established", "response-established", now-86401
				require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{Normalize(old)}, state, cfg))
			}
			require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{Normalize(first)}, state, cfg))
			var ordinary model.SecurityAlert
			require.NoError(t, db.Where("rule = ? AND repeat_key IS NOT NULL", "sensitive_operation").Order("id DESC").Take(&ordinary).Error)
			_, err = UpdateAlertInvestigation(t.Context(), db, common.RoleRootUser, 742, AlertInvestigation{Version: ordinary.Version, Status: "resolved"}, ordinary.ID)
			require.NoError(t, err)
			second := first
			second.EventId, second.RequestId, second.CreatedAt, second.Ip = "response-repeat-risk", "response-repeat-risk", now, "8.8.8.8"
			event := Normalize(second)
			if ruleID == "risky_ip_sensitive_operation" {
				var facts Facts
				require.NoError(t, common.UnmarshalJsonStr(event.Facts, &facts))
				yes := true
				facts.IPIntelligence = &IPIntelligenceSnapshot{IsAbuser: &yes, ObservedAt: now, ExpiresAt: now + 120, Providers: []string{"selfhost"}, FieldProviders: map[string]string{"abuse": "selfhost"}, Sources: []IPDataSource{{ProviderID: "selfhost", Source: "fixture", Capability: "abuse", DataUpdatedAt: now - 5, ExpiresAt: now + 120}}, Evidence: []IPDataEvidence{{ProviderID: "selfhost", Source: "fixture", Capability: "abuse", MatchedNetwork: event.IP, DataUpdatedAt: now - 5, ExpiresAt: now + 120}}}
				raw, err = common.Marshal(facts)
				require.NoError(t, err)
				event.Facts, event.IPCheckState = string(raw), "done"
			}
			require.NoError(t, db.Create(&model.Token{UserId: 901, Key: "repeat-response-fixture-key", Status: common.TokenStatusEnabled}).Error)
			require.NoError(t, db.Create(&model.UserAccessToken{UserId: 901, TokenHash: "repeat-response-fixture-pat"}).Error)
			require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{event}, state, cfg))
			require.NoError(t, db.First(&ordinary, ordinary.ID).Error)
			require.Equal(t, "resolved", ordinary.Status)
			var risk model.SecurityAlert
			require.NoError(t, db.Where("rule = ?", ruleID).Take(&risk).Error)
			require.Equal(t, "open", risk.Status)
			require.Equal(t, 2, risk.Severity)
			var responses []model.SecurityAction
			require.NoError(t, db.Where("alert_id = ?", risk.ID).Order("id").Find(&responses).Error)
			require.Len(t, responses, 3)
			for _, response := range responses {
				require.Equal(t, "pending", response.Outcome, response.ReasonCode)
			}
			w := worker{db: db}
			require.NoError(t, w.processAccountResponses(t.Context()))
			responses = nil
			require.NoError(t, db.Where("alert_id = ?", risk.ID).Order("id").Find(&responses).Error)
			for _, response := range responses {
				require.Equal(t, "succeeded", response.Outcome, response.ReasonCode)
			}
			var user model.User
			require.NoError(t, db.First(&user, 901).Error)
			require.Equal(t, common.UserStatusDisabled, user.Status)
			require.Nil(t, user.AccessToken)
			var remaining int64
			require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", 901).Count(&remaining).Error)
			require.Zero(t, remaining)
			require.NoError(t, db.Model(&model.UserAccessToken{}).Where("user_id = ?", 901).Count(&remaining).Error)
			require.Zero(t, remaining)
		})
	}
}
