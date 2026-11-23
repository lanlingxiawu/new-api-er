package auditsecurity

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func repeatQuotaAudit(t *testing.T, id string, at int64) model.AuditLog {
	t.Helper()
	ref := model.AccessTokenFingerprint("synthetic-repeat-credential")
	grant := model.AccessTokenFingerprint("synthetic-repeat-grant")
	raw, err := common.Marshal(map[string]any{
		"actor_id": 740, "role": common.RoleAdminUser, "auth_version": 1, "auth_method": "access_token",
		"credential_kind": "pat", "credential_ref": ref, "credential_grant": grant,
	})
	require.NoError(t, err)
	var identity model.AuditIdentity
	require.NoError(t, common.Unmarshal(raw, &identity))
	return model.AuditLog{
		EventId: id, RequestId: id, CreatedAt: at, UserId: 740, ActorRole: common.RoleAdminUser,
		Category: model.AuditCategoryOperation, AuthMethod: "access_token", TokenRef: ref,
		Action: "user.quota_add", Ip: "8.8.8.8", UserAgent: "repeat-operation-test", Success: true,
		Other: model.AuditOther{Op: &model.AuditOperation{Identity: &identity, Params: model.AuditFields{
			"target_user_id": 741, "target_role": common.RoleCommonUser, "quota": 100, "from": 1000, "to": 1100,
		}}},
	}
}

func TestRepeatOperationConfirmationAndFixedWindow(t *testing.T) {
	db := newWorkerTestDB(t)
	state, err := model.ClaimAuditSecurity(t.Context(), db, "repeat-operation")
	require.NoError(t, err)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.IPIntelligenceEnabled, cfg.NotificationEnabled, cfg.AutoBanEnabled = false, false, false
	at := time.Now().Unix() - 1800
	first := Normalize(repeatQuotaAudit(t, "repeat-first", at))
	require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{first}, state, cfg))
	var alert model.SecurityAlert
	require.NoError(t, db.Where("rule = ?", "sensitive_operation").Take(&alert).Error)
	require.Equal(t, 3, alert.RuleVersion, "employee visibility must preserve the existing administrator repeat key version")
	saved, err := UpdateAlertInvestigation(t.Context(), db, common.RoleRootUser, 742, AlertInvestigation{Version: alert.Version, Status: "resolved"}, alert.ID)
	require.NoError(t, err)
	repeat := Normalize(repeatQuotaAudit(t, "repeat-later", at+1200))
	require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{repeat}, state, cfg))
	var alerts []model.SecurityAlert
	require.NoError(t, db.Where("rule = ?", "sensitive_operation").Order("id").Find(&alerts).Error)
	require.Len(t, alerts, 1, "an identical later operation belongs to the fixed daily episode")
	require.Equal(t, "resolved", alerts[0].Status)
	require.EqualValues(t, 2, alerts[0].Occurrences)
	require.Equal(t, saved.ClosedAt, alerts[0].ClosedAt)
	require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{repeat}, state, cfg))
	require.NoError(t, db.First(&alert, alert.ID).Error)
	require.EqualValues(t, 2, alert.Occurrences, "replayed source rows do not inflate counts")
	end := Normalize(repeatQuotaAudit(t, "repeat-end", at+86400))
	require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{end}, state, cfg))
	require.NoError(t, db.Where("rule = ?", "sensitive_operation").Order("id").Find(&alerts).Error)
	require.Len(t, alerts, 2)
	require.Equal(t, "open", alerts[1].Status)
	var findings int64
	require.NoError(t, db.Model(&model.SecurityFindingSnapshot{}).Where("rule = ?", "sensitive_operation").Count(&findings).Error)
	require.EqualValues(t, 3, findings, "every distinct operation is still detected")
}

func TestRepeatOperationScopeAndEvidence(t *testing.T) {
	db := newWorkerTestDB(t)
	state, err := model.ClaimAuditSecurity(t.Context(), db, "repeat-scope")
	require.NoError(t, err)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.IPIntelligenceEnabled, cfg.NotificationEnabled, cfg.AutoBanEnabled = false, false, false
	for i, tc := range []struct {
		name   string
		merge  bool
		change func(*model.AuditLog)
	}{
		{"same amount different ordinary recipient", true, func(a *model.AuditLog) {
			a.Other.Op.Params["target_user_id"] = 742
			a.Other.Op.Params["from"] = 2000
			a.Other.Op.Params["to"] = 2100
		}},
		{"mapped IPv4", true, func(a *model.AuditLog) { a.Ip = "::ffff:8.8.8.8" }},
		{"different credential", false, func(a *model.AuditLog) {
			a.TokenRef = model.AccessTokenFingerprint("other")
			a.Other.Op.Identity.CredentialRef = a.TokenRef
		}},
		{"different IP", false, func(a *model.AuditLog) { a.Ip = "1.1.1.1" }},
		{"changed grants", false, func(a *model.AuditLog) {
			a.Other.Op.Identity.CredentialGrant = model.AccessTokenFingerprint("other-grant")
		}},
		{"changed authentication version", false, func(a *model.AuditLog) { a.Other.Op.Identity.AuthVersion++ }},
		{"different amount", false, func(a *model.AuditLog) { a.Other.Op.Params["quota"] = 200; a.Other.Op.Params["to"] = 1200 }},
		{"subtract instead of add", false, func(a *model.AuditLog) { a.Action = "user.quota_subtract"; a.Other.Op.Params["to"] = 900 }},
		{"different client", false, func(a *model.AuditLog) { a.UserAgent = "another-client" }},
		{"browser", false, func(a *model.AuditLog) { a.Other.Op.Identity.AuthMethod = "session" }},
		{"unknown target role", false, func(a *model.AuditLog) { delete(a.Other.Op.Params, "target_role") }},
		{"administrator recipient", false, func(a *model.AuditLog) { a.Other.Op.Params["target_role"] = common.RoleAdminUser }},
		{"missing credential evidence", false, func(a *model.AuditLog) { a.Other.Op.Identity.CredentialRef = "" }},
		{"missing client", false, func(a *model.AuditLog) { a.UserAgent = "" }},
		{"inconsistent amount", false, func(a *model.AuditLog) { a.Other.Op.Params["to"] = 1001 }},
		{"missing balance evidence", false, func(a *model.AuditLog) { delete(a.Other.Op.Params, "from") }},
		{"target token is not caller", false, func(a *model.AuditLog) {
			a.Other.Op.Identity = nil
			a.Other.Op.Params["token_id"] = 999
			a.Other.Op.Params["identity"] = map[string]any{"credential_ref": a.TokenRef}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := repeatQuotaAudit(t, fmt.Sprintf("scope-%d-first", i), time.Now().Unix()-2)
			first.UserId = 1500 + i
			first.Other.Op.Identity.ActorID = first.UserId
			second := repeatQuotaAudit(t, fmt.Sprintf("scope-%d-second", i), first.CreatedAt+1)
			second.UserId = first.UserId
			second.Other.Op.Identity.ActorID = first.UserId
			tc.change(&second)
			require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{Normalize(first), Normalize(second)}, state, cfg))
			var alerts []model.SecurityAlert
			require.NoError(t, db.Where("actor_id = ? AND rule = ?", first.UserId, "sensitive_operation").Order("id").Find(&alerts).Error)
			count := 2
			if tc.merge {
				count = 1
			}
			require.Len(t, alerts, count)
			require.NotNil(t, alerts[0].RepeatKey)
			if !tc.merge {
				var evidence []model.SecurityEvent
				require.NoError(t, common.UnmarshalJsonStr(alerts[0].Evidence, &evidence))
				require.Len(t, evidence, 1, "another scope must not be added as a repeat")
				require.Equal(t, first.EventId, evidence[0].EventID)
			}
		})
	}
}

func TestRepeatOperationSnapshotConfirmation(t *testing.T) {
	db := newWorkerTestDB(t)
	state, err := model.ClaimAuditSecurity(t.Context(), db, "repeat-confirm")
	require.NoError(t, err)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.IPIntelligenceEnabled, cfg.NotificationEnabled, cfg.AutoBanEnabled = false, false, false
	at := time.Now().Unix() - 10
	require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{Normalize(repeatQuotaAudit(t, "confirm-first", at))}, state, cfg))
	var shown model.SecurityAlert
	require.NoError(t, db.Where("rule = ?", "sensitive_operation").Take(&shown).Error)
	require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{Normalize(repeatQuotaAudit(t, "confirm-unseen", at+1))}, state, cfg))
	confirmed, err := UpdateAlertInvestigation(t.Context(), db, common.RoleRootUser, 742, AlertInvestigation{Version: shown.Version, Status: "resolved", ObservedOccurrences: &shown.Occurrences}, shown.ID)
	require.NoError(t, err, "a pure duplicate cannot force the user to refresh and confirm again")
	require.Equal(t, "resolved", confirmed.Status)
	require.EqualValues(t, 1, *confirmed.ConfirmedOccurrences)
	require.EqualValues(t, 2, confirmed.Occurrences)
	var manual model.SecurityAction
	require.NoError(t, db.Where("alert_id = ? AND source = ?", shown.ID, "human").Take(&manual).Error)
	require.Contains(t, manual.Result, `"confirmed_occurrences":1`)
	_, err = UpdateAlertInvestigation(t.Context(), db, common.RoleRootUser, 742, AlertInvestigation{Version: shown.Version, Status: "resolved", ObservedOccurrences: &shown.Occurrences}, shown.ID)
	require.ErrorIs(t, err, model.ErrSecurityConflict, "a stale request cannot repeat a committed manual action")
	opened, err := UpdateAlertInvestigation(t.Context(), db, common.RoleRootUser, 742, AlertInvestigation{Version: confirmed.Version, Status: "open"}, shown.ID)
	require.NoError(t, err)
	_, err = UpdateAlertInvestigation(t.Context(), db, common.RoleRootUser, 742, AlertInvestigation{Version: confirmed.Version, Status: "resolved", ObservedOccurrences: &confirmed.Occurrences}, shown.ID)
	require.ErrorIs(t, err, model.ErrSecurityConflict, "manual reopening must not be overwritten")
	require.Equal(t, "open", opened.Status)
}

func TestRepeatOperationClosedAndLateBoundaries(t *testing.T) {
	db := newWorkerTestDB(t)
	state, err := model.ClaimAuditSecurity(t.Context(), db, "repeat-boundaries")
	require.NoError(t, err)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.IPIntelligenceEnabled, cfg.NotificationEnabled, cfg.AutoBanEnabled = false, false, false
	for i, kind := range []string{"false_positive", "malicious", "archived", "purging", "before_start", "before_end"} {
		t.Run(kind, func(t *testing.T) {
			first := repeatQuotaAudit(t, "boundary-first-"+kind, time.Now().Unix()-10)
			first.UserId, first.Other.Op.Identity.ActorID = 1700+i, 1700+i
			require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{Normalize(first)}, state, cfg))
			var original model.SecurityAlert
			require.NoError(t, db.Where("actor_id = ? AND rule = ?", first.UserId, "sensitive_operation").Take(&original).Error)
			input := AlertInvestigation{Version: original.Version, Status: "resolved"}
			if kind == "false_positive" {
				input.Status = kind
			}
			if kind == "malicious" {
				input.Conclusion = &kind
			}
			closed, err := UpdateAlertInvestigation(t.Context(), db, common.RoleRootUser, 742, input, original.ID)
			require.NoError(t, err)
			if kind == "archived" {
				_, err = ArchiveAlert(t.Context(), db, common.RoleRootUser, 742, original.ID, closed.Version, true)
				require.NoError(t, err)
			}
			if kind == "purging" {
				require.NoError(t, db.Model(&model.SecurityAlert{}).Where("id = ?", original.ID).Updates(map[string]any{"purge_state": "pending", "version": gorm.Expr("version + 1")}).Error)
			}
			second := first
			second.EventId, second.RequestId, second.CreatedAt = "boundary-next-"+kind, "boundary-next-"+kind, first.CreatedAt+1
			if kind == "before_start" {
				second.CreatedAt = first.CreatedAt - 1
			}
			if kind == "before_end" {
				second.CreatedAt = first.CreatedAt + 86399
			}
			require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{Normalize(second)}, state, cfg))
			var alerts []model.SecurityAlert
			require.NoError(t, db.Where("actor_id = ? AND rule = ?", first.UserId, "sensitive_operation").Order("id").Find(&alerts).Error)
			if kind == "archived" || kind == "purging" || kind == "before_start" {
				require.Len(t, alerts, 2)
				require.EqualValues(t, 1, alerts[0].Occurrences)
				require.Equal(t, "open", alerts[1].Status)
			} else {
				require.Len(t, alerts, 1)
				require.EqualValues(t, 2, alerts[0].Occurrences)
				require.Equal(t, original.RepeatUntil, alerts[0].RepeatUntil, "hits never extend the fixed window")
				if kind == "before_end" {
					require.Equal(t, "resolved", alerts[0].Status)
				} else {
					require.Equal(t, "open", alerts[0].Status, "false-positive or malicious findings must return for review")
					require.Nil(t, alerts[0].ConfirmedOccurrences)
				}
			}
		})
	}
}

func TestRepeatOperationSnapshotRejectsInvalidScope(t *testing.T) {
	db := newWorkerTestDB(t)
	for _, kind := range []string{"ordinary", "expired", "archived", "purging", "critical", "malicious", "false_positive", "note", "assignee", "conclusion", "future_version", "unseen_count", "zero_count", "manual_revision"} {
		t.Run(kind, func(t *testing.T) {
			key, until, observed := "valid-repeat-key", time.Now().Unix()+3600, int64(3)
			alert := model.SecurityAlert{Rule: "sensitive_operation", Status: "open", Severity: 2, Version: 5, Occurrences: 3, Visibility: common.RoleAdminUser, RepeatKey: &key, RepeatUntil: &until}
			input := AlertInvestigation{Version: 5, Status: "resolved", ObservedOccurrences: &observed}
			switch kind {
			case "ordinary":
				alert.RepeatKey = nil
			case "expired":
				until = time.Now().Unix() - 1
			case "archived":
				now := time.Now().Unix()
				alert.ArchivedAt = &now
			case "purging":
				alert.PurgeState = "pending"
			case "critical":
				alert.Severity = 3
			case "malicious", "false_positive":
				alert.Conclusion = kind
			case "note":
				input.Note = "must use exact version"
			case "assignee":
				id := 0
				input.AssigneeID = &id
			case "conclusion":
				conclusion := "authorized"
				input.Conclusion = &conclusion
			case "future_version":
				input.Version++
			case "unseen_count":
				observed++
			case "zero_count":
				observed = 0
			case "manual_revision":
				alert.Version++
			}
			require.NoError(t, db.Create(&alert).Error)
			_, err := UpdateAlertInvestigation(t.Context(), db, common.RoleRootUser, 742, input, alert.ID)
			require.ErrorIs(t, err, model.ErrSecurityConflict)
			var stored model.SecurityAlert
			require.NoError(t, db.First(&stored, alert.ID).Error)
			require.Equal(t, "open", stored.Status)
			require.Nil(t, stored.ConfirmedOccurrences)
		})
	}
}

func TestRepeatOperationConfirmationAtomicPredicate(t *testing.T) {
	for _, change := range []string{"hit", "manual"} {
		t.Run(change, func(t *testing.T) {
			db := newWorkerTestDB(t)
			key, until, observed := "valid-repeat-key", time.Now().Unix()+3600, int64(3)
			alert := model.SecurityAlert{Rule: "sensitive_operation", Status: "open", Severity: 2, Version: 5, Occurrences: 3, Visibility: common.RoleAdminUser, RepeatKey: &key, RepeatUntil: &until}
			require.NoError(t, db.Create(&alert).Error)
			injected := false
			// Change the row after the authorization read, immediately before SQL
			// update. This verifies the WHERE predicate itself on every dialect.
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:repeat-cas", func(tx *gorm.DB) {
				if injected || tx.Statement.Table != "audit_security_alerts" {
					return
				}
				injected = true
				updates := map[string]any{"version": gorm.Expr("version + 1")}
				if change == "hit" {
					updates["occurrences"] = gorm.Expr("occurrences + 1")
				}
				err := tx.Session(&gorm.Session{NewDB: true}).Model(&model.SecurityAlert{}).Where("id = ?", alert.ID).Updates(updates).Error
				if err != nil {
					tx.AddError(err)
				}
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove("test:repeat-cas")) })
			saved, err := UpdateAlertInvestigation(t.Context(), db, common.RoleRootUser, 742, AlertInvestigation{Version: 5, Status: "resolved", ObservedOccurrences: &observed}, alert.ID)
			require.True(t, injected)
			if change == "manual" {
				require.ErrorIs(t, err, model.ErrSecurityConflict)
			} else {
				require.NoError(t, err)
				require.EqualValues(t, 4, saved.Occurrences)
				require.EqualValues(t, 3, *saved.ConfirmedOccurrences)
				require.EqualValues(t, 7, saved.Version)
			}
		})
	}
}

func TestRepeatOperationBatchDetectionSurvivesConfirmation(t *testing.T) {
	db := newWorkerTestDB(t)
	state, err := model.ClaimAuditSecurity(t.Context(), db, "repeat-batch")
	require.NoError(t, err)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.IPIntelligenceEnabled, cfg.NotificationEnabled, cfg.AutoBanEnabled = false, false, false
	now := time.Now().Unix() - 30
	require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{Normalize(repeatQuotaAudit(t, "batch-first", now))}, state, cfg))
	var first model.SecurityAlert
	require.NoError(t, db.Where("rule = ?", "sensitive_operation").Take(&first).Error)
	_, err = UpdateAlertInvestigation(t.Context(), db, common.RoleRootUser, 742, AlertInvestigation{Version: first.Version, Status: "resolved"}, first.ID)
	require.NoError(t, err)
	for i := 1; i < cfg.BatchThreshold; i++ {
		require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{Normalize(repeatQuotaAudit(t, fmt.Sprintf("batch-repeat-%d", i), now+int64(i)))}, state, cfg))
	}
	require.NoError(t, db.First(&first, first.ID).Error)
	require.Equal(t, "resolved", first.Status)
	require.EqualValues(t, cfg.BatchThreshold, first.Occurrences)
	var batch model.SecurityAlert
	require.NoError(t, db.Where("rule = ?", "bulk_sensitive_operations").Take(&batch).Error)
	require.Equal(t, "open", batch.Status)
	require.Equal(t, 3, batch.Severity)
	require.Equal(t, cfg.BatchThreshold, batch.MatchedCount)
	_, err = UpdateAlertInvestigation(t.Context(), db, common.RoleRootUser, 742, AlertInvestigation{Version: batch.Version, Status: "resolved"}, batch.ID)
	require.NoError(t, err)
	require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{Normalize(repeatQuotaAudit(t, "batch-again", now+int64(cfg.BatchThreshold)))}, state, cfg))
	require.NoError(t, db.First(&batch, batch.ID).Error)
	require.Equal(t, "open", batch.Status, "batch warnings still reopen after another hit")
}

func TestRepeatOperationRechecksEpisodeAfterIndexLookup(t *testing.T) {
	for _, kind := range []string{"archived", "purging"} {
		t.Run(kind, func(t *testing.T) {
			db := newWorkerTestDB(t)
			state, err := model.ClaimAuditSecurity(t.Context(), db, "repeat-lookup-race")
			require.NoError(t, err)
			cfg := operation_setting.DefaultAuditSecuritySetting()
			cfg.IPIntelligenceEnabled, cfg.NotificationEnabled, cfg.AutoBanEnabled = false, false, false
			now := time.Now().Unix()
			require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{Normalize(repeatQuotaAudit(t, "lookup-first", now-1))}, state, cfg))
			reads := 0
			// Simulate the full row read seeing a change after the covering lookup.
			// Both transactions cannot write concurrently on SQLite; this exercises
			// the same eligibility boundary deterministically on all three engines.
			require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:repeat-lookup", func(tx *gorm.DB) {
				alert, ok := tx.Statement.Dest.(*model.SecurityAlert)
				if !ok || alert.RepeatKey == nil || alert.ID == 0 {
					return
				}
				reads++
				if kind == "archived" {
					alert.ArchivedAt = &now
				} else {
					alert.PurgeState = "deleting"
				}
			}))
			err = Apply(t.Context(), db, state, []model.SecurityEvent{Normalize(repeatQuotaAudit(t, "lookup-second", now))}, state, cfg)
			require.NoError(t, db.Callback().Query().Remove("test:repeat-lookup"))
			require.ErrorIs(t, err, model.ErrSecurityConflict)
			require.Equal(t, 3, reads, "bounded retries re-check the complete row")
			var alerts []model.SecurityAlert
			require.NoError(t, db.Where("rule = ?", "sensitive_operation").Find(&alerts).Error)
			require.Len(t, alerts, 1)
			require.EqualValues(t, 1, alerts[0].Occurrences, "an ineligible episode cannot silently absorb another hit")
			var receipts int64
			require.NoError(t, db.Model(&model.SecurityEvent{}).Where("event_id = ?", "lookup-second").Count(&receipts).Error)
			require.Zero(t, receipts, "retry must retain the source event for the next scan")
		})
	}
}
