package auditsecurity

import (
	"fmt"
	"slices"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestChannelRoutineMaintenanceAlertPolicy(t *testing.T) {
	for _, action := range []string{"channel.update", "channel.token_update", "channel.tag_edit"} {
		for _, tc := range []struct {
			name   string
			fields []string
			risk   bool
		}{
			{"group", []string{"group"}, false},
			{"priority", []string{"priority"}, false},
			{"weight", []string{"weight"}, false},
			{"models", []string{"models"}, false},
			{"routine combination", []string{"group", "priority", "weight", "models", "name"}, false},
			{"address", []string{"base_url"}, true},
			{"routine with address", []string{"group", "priority", "weight", "models", "base_url"}, true},
			{"address before routine", []string{"base_url", "models", "group"}, true},
			{"proxy", []string{"proxy"}, true},
			{"proxy setting", []string{"setting"}, true},
			{"settings", []string{"settings"}, true},
			{"key", []string{"key"}, true},
			{"headers", []string{"header_override"}, true},
			{"parameters", []string{"param_override"}, true},
			{"model mapping remains sensitive", []string{"model_mapping"}, true},
			{"duplicate routine before address", append(slices.Repeat([]string{"models"}, 32), "base_url"), true},
		} {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				fieldKey := "changed_fields"
				if action == "channel.tag_edit" {
					fieldKey = "submitted_fields"
				}
				e := Normalize(model.AuditLog{EventId: "current", RequestId: "request", UserId: 7, ActorRole: common.RoleAdminUser, Action: action, Success: true, Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{fieldKey: tc.fields, "count": 8}}}})
				findings := Evaluate(e, nil, operation_setting.DefaultAuditSecuritySetting())
				if !tc.risk {
					require.Empty(t, findings, "routine batches must not produce either single or bulk alerts")
					return
				}
				require.Len(t, findings, 2)
				require.Equal(t, "sensitive_operation", findings[0].Rule)
				require.Equal(t, 2, findings[0].Severity)
				require.Equal(t, "bulk_sensitive_operations", findings[1].Rule)
				require.Equal(t, 3, findings[1].Severity)
			})
		}
	}
	for _, action := range []string{"channel.upstream_apply", "channel.upstream_apply_all"} {
		t.Run(action, func(t *testing.T) {
			e := Normalize(model.AuditLog{Action: action, Success: true, ActorRole: common.RoleAdminUser, Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"count": 100}}}})
			require.Empty(t, Evaluate(e, nil, operation_setting.DefaultAuditSecuritySetting()))
		})
	}
}

func TestChannelRoutineHistoryDoesNotEscalateAddressChange(t *testing.T) {
	var history []model.SecurityEvent
	for i, action := range []string{"channel.update", "channel.token_update", "channel.tag_edit", "channel.upstream_apply", "channel.upstream_apply_all"} {
		history = append(history, Normalize(model.AuditLog{EventId: fmt.Sprint(i), RequestId: fmt.Sprint(i), CreatedAt: 1000, UserId: 7, ActorRole: common.RoleAdminUser, Action: action, Success: true, Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"changed_fields": []string{"group", "models", "priority", "weight"}, "submitted_fields": []string{"models"}, "count": 100}}}}))
	}
	e := Normalize(model.AuditLog{EventId: "address", RequestId: "address", CreatedAt: 1001, UserId: 7, ActorRole: common.RoleAdminUser, Action: "channel.update", Success: true, Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"changed_fields": []string{"base_url"}, "base_url": "https://credential@example.invalid/?key=SECRET"}}}})
	findings := Evaluate(e, history, operation_setting.DefaultAuditSecuritySetting())
	require.Len(t, findings, 1)
	require.Equal(t, "sensitive_operation", findings[0].Rule)
	require.Equal(t, 2, findings[0].Severity)
	require.Equal(t, 1, findings[0].Count)
	require.NotContains(t, e.Facts, "credential")
	require.NotContains(t, e.Facts, "SECRET")
}

func TestHighRiskOperationsDoNotRequireSuspiciousLogin(t *testing.T) {
	for _, action := range []string{"channel.key_view", "channel.delete", "channel.create", "user.quota_add", "user.binding_clear", "user.2fa_disable"} {
		t.Run(action, func(t *testing.T) {
			e := Normalize(model.AuditLog{EventId: action, RequestId: action, CreatedAt: 1000, UserId: 7, ActorRole: common.RoleAdminUser, Action: action, Success: true, Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"id": 9, "target_user_id": 9, "target_role": common.RoleCommonUser}}}})
			findings := Evaluate(e, nil, operation_setting.DefaultAuditSecuritySetting())
			require.NotEmpty(t, findings)
			require.GreaterOrEqual(t, findings[0].Severity, 2)
		})
	}
}

func TestRiskRulesRespectResultsAndActualChanges(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		success      bool
		fields       model.AuditFields
		want         bool
	}{
		{"failed key read", "channel.key_view", false, nil, false},
		{"unchanged status", "channel.status_update", true, model.AuditFields{"changed": false}, false},
		{"display only", "channel.update", true, model.AuditFields{"changed_fields": []string{"name"}}, false},
		{"upstream changed", "channel.update", true, model.AuditFields{"changed_fields": []string{"base_url"}}, true},
		{"legacy incomplete evidence", "channel.update", true, nil, true},
		{"ordinary recharge", "user.topup_complete", true, nil, false},
		{"unchanged quota", "user.quota_override", true, model.AuditFields{"from": 7, "to": 7}, false},
		{"zero affected batch", "channel.delete_batch", true, model.AuditFields{"count": 0}, false},
		{"remark only", "channel.token_update", true, model.AuditFields{"changed_fields": []string{"remark"}}, false},
		{"empty known changes", "channel.update", true, model.AuditFields{"changed_fields": []string{}}, false},
		{"ordinary sign-in", "login", true, nil, false},
		{"ordinary PAT request", "access_token.request", true, nil, false},
		{"PAT rename", "access_token.rename", true, nil, false},
		{"same PAT grant", "access_token.update", true, model.AuditFields{"scopes": []string{"usage:read", "profile:read", "usage:read"}, "previous_scopes": []string{"profile:read", "usage:read"}}, false},
		{"changed PAT grant", "access_token.update", true, model.AuditFields{"scopes": []string{"usage:read"}, "previous_scopes": []string{"profile:read", "usage:read"}}, false},
		{"expanded PAT grant", "access_token.update", true, model.AuditFields{"scopes": []string{"profile:read", "usage:read", "channels:read"}, "previous_scopes": []string{"profile:read", "usage:read"}}, true},
		{"unknown PAT grant", "access_token.update", true, nil, true},
		{"failed modification", "channel.token_update", false, model.AuditFields{"changed_fields": []string{"key"}}, false},
		{"user group remains sensitive", "user.update", true, model.AuditFields{"changed_fields": []string{"group"}}, true},
		{"legacy tag evidence", "channel.tag_edit", true, nil, true},
		{"tag without submitted changes", "channel.tag_edit", true, model.AuditFields{"submitted_fields": []string{}}, false},
		{"tag header with routine fields", "channel.tag_edit", true, model.AuditFields{"submitted_fields": []string{"models", "header_override"}}, true},
		{"failed address change", "channel.update", false, model.AuditFields{"changed_fields": []string{"base_url"}}, false},
		{"unchanged address", "channel.update", true, model.AuditFields{"changed": false, "changed_fields": []string{"base_url"}}, false},
		{"submitted fields do not override actual address", "channel.update", true, model.AuditFields{"changed_fields": []string{"base_url"}, "submitted_fields": []string{"models"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Normalize(model.AuditLog{EventId: "e", Action: tc.action, Success: tc.success, ActorRole: 10, Other: model.AuditOther{Op: &model.AuditOperation{Params: tc.fields}}})
			require.Equal(t, tc.want, len(Evaluate(e, nil, operation_setting.DefaultAuditSecuritySetting())) > 0)
		})
	}
}

func TestNormalizationNeverCopiesCredentials(t *testing.T) {
	entry := model.AuditLog{EventId: "e", Action: "channel.update", ActorRole: 10, UserId: 3, Success: true, Content: "SECRET", UserAgent: "SECRET", TokenRef: "safe-hash", Other: model.AuditOther{RootInfo: model.AuditFields{"key": "SECRET"}, Op: &model.AuditOperation{Params: model.AuditFields{"id": 9, "key": "SECRET", "password": "SECRET", "base_url": "https://SECRET@provider.test/private?token=SECRET", "changed_fields": []string{"key"}, "submitted_fields": []string{"header_override", "SECRET", "https://SECRET@provider.test"}}}}}
	entry.Other.Op.Params["source_baseline"] = map[string]any{"source_first_seen": 100, "baseline_first_seen": 1, "baseline_last_seen": 99}
	e := Normalize(entry)
	require.NotContains(t, e.Facts, "source_baseline", "source timing must come only from the monitoring worker")
	raw, err := common.Marshal(e)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "SECRET")
	require.Equal(t, "channel", e.ResourceType)
	entry.ActorRole = 0
	require.Equal(t, common.RoleRootUser, Normalize(entry).Visibility)
	entry.ActorRole = common.RoleAdminUser
	entry.Other.Op.Params["target_role"] = common.RoleRootUser
	require.Equal(t, common.RoleRootUser, Normalize(entry).Visibility)
}

func TestLoginFailureThresholdAndWindow(t *testing.T) {
	cfg := operation_setting.DefaultAuditSecuritySetting()
	for _, count := range []int{4, 5, 6} {
		history := make([]model.SecurityEvent, 0, count)
		for i := range count - 1 {
			history = append(history, model.SecurityEvent{EventID: string(rune('a' + i)), RequestID: string(rune('a' + i)), At: 1000, Action: "auth.login_failed", SubjectRef: "account:x", IP: "192.0.2.1", Visibility: 100, Reason: "invalid_credentials"})
		}
		e := model.SecurityEvent{EventID: "current", RequestID: "current", At: 1200, Action: "auth.login_failed", SubjectRef: "account:x", IP: "192.0.2.1", Visibility: 100, Reason: "invalid_credentials"}
		findings := Evaluate(e, history, cfg)
		require.Equal(t, count >= 5, len(findings) > 0)
		e.At = 1301
		require.Empty(t, Evaluate(e, history, cfg))
		e.At = 1200
		e.Reason = "service_error"
		require.Empty(t, Evaluate(e, history, cfg))
	}
}

func TestPATChannelChangesAndAffectedTargets(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		fields                  model.AuditFields
		success                 bool
		count, target, findings int
	}{
		{"status", model.AuditFields{"ids": []int{9}, "changed_fields": []string{"status"}}, true, 1, 9, 1},
		{"daily limit", model.AuditFields{"ids": []int{9}, "changed_fields": []string{"daily_limit"}}, true, 1, 9, 1},
		{"tag", model.AuditFields{"ids": []int{9}, "changed_fields": []string{"tag"}}, true, 1, 9, 1},
		{"display only", model.AuditFields{"ids": []int{9}, "changed_fields": []string{"name", "remark"}}, true, 1, 9, 0},
		{"unchanged", model.AuditFields{"ids": []int{9}, "changed": false, "changed_fields": []string{"status"}}, true, 1, 9, 0},
		{"empty targets", model.AuditFields{"ids": []int{}, "changed_fields": []string{"status"}}, true, 0, 0, 0},
		{"explicit zero", model.AuditFields{"ids": []int{9}, "count": 0, "changed_fields": []string{"status"}}, true, 0, 9, 0},
		{"negative count", model.AuditFields{"ids": []int{9}, "count": -1, "changed_fields": []string{"status"}}, true, 0, 9, 0},
		{"legacy missing evidence", nil, true, 1, 0, 1},
		{"failed", model.AuditFields{"ids": []int{9}, "changed_fields": []string{"status"}}, false, 1, 9, 0},
		{"deduplicate", model.AuditFields{"ids": []int{9, 9, 0, -1, 8}, "changed_fields": []string{"status"}}, true, 2, 0, 1},
		{"below bulk threshold", model.AuditFields{"ids": []int{1, 2, 3, 4}, "changed_fields": []string{"status"}}, true, 4, 0, 1},
		{"bulk threshold", model.AuditFields{"ids": []int{1, 2, 3, 4, 5}, "changed_fields": []string{"status"}}, true, 5, 0, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Normalize(model.AuditLog{EventId: "pat", RequestId: "req", UserId: 7, ActorRole: common.RoleAdminUser, Action: "channel.token_update", Success: tc.success, Other: model.AuditOther{Op: &model.AuditOperation{Params: tc.fields}}})
			require.Equal(t, tc.count, e.Count)
			require.Equal(t, tc.target, e.TargetID)
			findings := Evaluate(e, nil, operation_setting.DefaultAuditSecuritySetting())
			require.Len(t, findings, tc.findings)
			if len(findings) > 0 {
				require.Equal(t, tc.count, findings[0].Count)
			}
			if tc.name == "status" || tc.name == "below bulk threshold" || tc.name == "deduplicate" {
				require.Equal(t, 1, findings[0].Severity)
			}
			if tc.name == "bulk threshold" {
				require.Equal(t, 1, findings[0].Severity)
				require.Equal(t, "bulk_sensitive_operations", findings[1].Rule)
				require.Equal(t, 2, findings[1].Severity)
			}
		})
	}
}

func TestOrdinaryPATCreateStaysInBulkWithoutAStandaloneAlert(t *testing.T) {
	cfg := operation_setting.DefaultAuditSecuritySetting()
	ordinary := Normalize(model.AuditLog{EventId: "pat", RequestId: "pat", CreatedAt: 1000, UserId: 7, ActorRole: common.RoleCommonUser, Action: "access_token.generate", Success: true, Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"token_id": 4}}}})
	var facts Facts
	require.NoError(t, common.UnmarshalJsonStr(ordinary.Facts, &facts))
	require.Greater(t, highRisk(ordinary, facts), 0)
	require.Empty(t, Evaluate(ordinary, nil, cfg))
	history := make([]model.SecurityEvent, cfg.BatchThreshold-1)
	for i := range history {
		history[i] = ordinary
		history[i].EventID = fmt.Sprint("older-", i)
		history[i].RequestID = history[i].EventID
		history[i].At = ordinary.At - int64(len(history)-i)
	}
	findings := Evaluate(ordinary, history, cfg)
	require.Len(t, findings, 1)
	require.Equal(t, "bulk_sensitive_operations", findings[0].Rule)
	require.Equal(t, 2, findings[0].Severity)
	admin := Normalize(model.AuditLog{EventId: "admin-pat", RequestId: "admin-pat", CreatedAt: 1000, UserId: 8, ActorRole: common.RoleAdminUser, Action: "access_token.generate", Success: true, Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"token_id": 5}}}})
	adminFindings := Evaluate(admin, nil, cfg)
	require.Len(t, adminFindings, 1)
	require.Equal(t, "sensitive_operation", adminFindings[0].Rule)
	require.Equal(t, 2, adminFindings[0].Severity)
}

func TestPermissionDirectionGradesPromotionAboveRevocation(t *testing.T) {
	for _, tc := range []struct {
		action   string
		severity int
	}{
		{"promote", 3},
		{"demote", 2},
	} {
		t.Run(tc.action, func(t *testing.T) {
			e := Normalize(model.AuditLog{EventId: tc.action, UserId: 7, ActorRole: common.RoleAdminUser, Action: "user.manage", Success: true, Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"action": tc.action, "target_user_id": 9, "target_role": common.RoleCommonUser}}}})
			findings := Evaluate(e, nil, operation_setting.DefaultAuditSecuritySetting())
			require.Len(t, findings, 1)
			require.Equal(t, "sensitive_operation", findings[0].Rule)
			require.Equal(t, tc.severity, findings[0].Severity)
		})
	}
}

func TestPATChannelTargetEvidenceIsBounded(t *testing.T) {
	for _, count := range []int{100, 101} {
		ids := make([]int, count)
		for i := range count {
			ids[i] = i + 1
		}
		e := Normalize(model.AuditLog{Action: "channel.token_update", Success: true, ActorRole: common.RoleAdminUser, Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"ids": ids, "changed_fields": []string{"status"}, "key": "must-not-copy"}}}})
		var facts Facts
		require.NoError(t, common.UnmarshalJsonStr(e.Facts, &facts))
		require.Len(t, facts.IDs, 100)
		require.Equal(t, 100, e.Count)
		require.Equal(t, count > 100, facts.EvidenceIncomplete)
		require.NotContains(t, e.Facts, "must-not-copy")
	}
}
