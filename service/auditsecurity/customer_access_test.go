package auditsecurity

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func customerAccessFixture(t *testing.T) (*gorm.DB, model.CustomerAuditFilter) {
	t.Helper()
	db := newWorkerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}))
	users := []model.User{
		{Id: 1, Username: "employee-a", Role: 1, Status: 1},
		{Id: 2, Username: "employee-b", Role: 1, Status: 1},
		{Id: 3, Username: "customer-a", Role: 1, Status: 1, InviterId: 1},
		{Id: 4, Username: "customer-b", Role: 1, Status: 1, InviterId: 2},
	}
	require.NoError(t, db.Create(&users).Error)
	require.NoError(t, db.Create(&[]model.EmployeeProfile{{UserId: 1, Status: 1}, {UserId: 2, Status: 1}}).Error)
	now := time.Now().Unix()
	return db, model.CustomerAuditFilter{EmployeeID: 1, CustomerID: 3, Start: now - 86400, End: now}
}

func TestCustomerRecoveryIdentityProof(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*model.AuditLog)
		allowed bool
	}{
		{"committed", func(*model.AuditLog) {}, true},
		{"cleanup failed", func(a *model.AuditLog) { a.Action = "auth.password_reset_cleanup_failed" }, true},
		{"unknown account", func(a *model.AuditLog) { a.UserId = 0 }, false},
		{"authenticated role", func(a *model.AuditLog) { a.ActorRole = 1 }, false},
		{"wrong account", func(a *model.AuditLog) { a.Other.Op.Identity.ActorID = 4 }, false},
		{"administrator", func(a *model.AuditLog) { a.Other.Op.Identity.Role = 10 }, false},
		{"unknown role", func(a *model.AuditLog) { a.Other.Op.Identity.Role = 0 }, false},
		{"wrong method", func(a *model.AuditLog) { a.Other.Op.Identity.AuthMethod = "session" }, false},
		{"missing version", func(a *model.AuditLog) { a.Other.Op.Identity.AuthVersion = 0 }, false},
		{"not committed", func(a *model.AuditLog) { a.Other.Op.Identity.EffectiveVersion = 1 }, false},
		{"rollback", func(a *model.AuditLog) { a.Other.Op.Identity.EffectiveVersion = 0 }, false},
		{"missing proof", func(a *model.AuditLog) { a.Other.Op.Identity = nil }, false},
		{"untrusted params", func(a *model.AuditLog) {
			a.Other.Op.Params["identity"] = a.Other.Op.Identity
			a.Other.Op.Identity = nil
		}, false},
		{"incomplete evidence", func(a *model.AuditLog) { a.Other.Op.Params["evidence_incomplete"] = true }, false},
		{"untrusted target ignored", func(a *model.AuditLog) { a.Other.Op.Params["target_user_id"] = 4 }, true},
		{"failed attempt", func(a *model.AuditLog) { a.Success = false }, false},
		{"recovery request", func(a *model.AuditLog) { a.Action = "auth.recovery_requested" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := model.AuditLog{UserId: 3, Action: "auth.password_reset_committed", Success: true,
				Other: model.AuditOther{Op: &model.AuditOperation{Identity: &model.AuditIdentity{ActorID: 3, Role: 1, AuthMethod: "recovery", AuthVersion: 1, EffectiveVersion: 2}, Params: model.AuditFields{}}}}
			tc.change(&log)
			event := Normalize(log)
			version := 0
			if tc.allowed {
				version = 5
			}
			require.Equal(t, version, customerSensitiveEventVersion(event))
			if tc.allowed {
				require.Zero(t, event.ActorRole)
				event.TargetID = 4
				require.Zero(t, customerSensitiveEventVersion(event))
			}
		})
	}
}

func TestCustomerBulkIdentityIsolation(t *testing.T) {
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.BatchThreshold = 3
	trigger := Normalize(model.AuditLog{EventId: "trigger", UserId: 3, ActorRole: 1, Action: "user.password_change", Success: true, CreatedAt: 1000})
	for _, tc := range []struct {
		name         string
		role, target int
	}{
		{"administrator", 10, 0}, {"unknown role", 0, 0}, {"other target", 1, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unsafe := trigger
			unsafe.EventID, unsafe.At, unsafe.Count, unsafe.ActorRole, unsafe.TargetID = "unsafe", 998, 10, tc.role, tc.target
			previous := trigger
			previous.EventID, previous.At = "previous", 999
			history := []model.SecurityEvent{unsafe, previous}
			for _, finding := range Evaluate(trigger, history, cfg) {
				require.NotEqual(t, "bulk_sensitive_operations", finding.Rule, "unrelated identity must not push customer activity above the threshold")
			}
			previous.EventID = "second"
			history = append(history, previous)
			found := false
			for _, finding := range Evaluate(trigger, history, cfg) {
				if finding.Rule == "bulk_sensitive_operations" {
					found = true
					require.Equal(t, 3, finding.Count)
				}
			}
			require.True(t, found)
		})
	}
}

func TestCustomerSensitiveRiskLifecycle(t *testing.T) {
	db, filter := customerAccessFixture(t)
	ctx := t.Context()
	state, err := model.ClaimAuditSecurity(ctx, db, "customer-sensitive")
	require.NoError(t, err)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.IPIntelligenceEnabled, cfg.NotificationEnabled, cfg.AutoBanEnabled = false, false, false
	yes := true
	settings, err := common.Marshal(dto.UserSetting{AccountSecurityNotifyEnabled: &yes, NotifyType: "email", NotificationEmail: "fixture@example.com"})
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("setting", string(settings)).Error)
	event := Normalize(model.AuditLog{EventId: "customer-password", UserId: 3, ActorRole: 1, Action: "user.password_change", Success: true, CreatedAt: filter.End})
	require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{event}, state, cfg))
	var alert model.SecurityAlert
	require.NoError(t, db.Where("rule = ?", "sensitive_operation").Take(&alert).Error)
	page, err := ListCustomerAlerts(ctx, db, filter)
	require.NoError(t, err)
	require.Len(t, page.Items, 1, "the customer's successful password change must appear in employee risk records")
	require.Equal(t, alert.ID, page.Items[0].ID)
	require.Equal(t, 2, page.Items[0].Severity)
	require.Equal(t, 4, alert.RuleVersion)
	admin, err := GetAlert(ctx, db, common.RoleRootUser, alert.ID)
	require.NoError(t, err)
	require.Equal(t, alert.ID, admin.Alert.ID)
	require.Len(t, admin.Alert.Assignees, 1)
	require.Equal(t, 1, admin.Alert.Assignees[0].ID)
	aggregate, err := ListEmployeeCustomerAlerts(ctx, db, model.EmployeeAuditFilter{EmployeeID: 1, Start: filter.Start, End: filter.End})
	require.NoError(t, err)
	require.Len(t, aggregate.Items, 1)
	require.Equal(t, alert.ID, aggregate.Items[0].ID)
	pending, err := ListPendingCustomerAlerts(ctx, db, PendingCustomerFilter{EmployeeID: 1})
	require.NoError(t, err)
	require.Len(t, pending.Items, 1)
	count, err := CountEmployeePendingAlerts(ctx, db, 1)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	owner := 1
	adminFilter := AlertFilter{Start: filter.Start, End: filter.End, AssigneeID: &owner}
	assigned, err := ListAlerts(ctx, db, common.RoleRootUser, adminFilter)
	require.NoError(t, err)
	require.Len(t, assigned.Items, 1)
	exported, err := ExportAlerts(ctx, db, common.RoleRootUser, adminFilter, 20)
	require.NoError(t, err)
	require.Len(t, exported.Items, 1)
	summary, err := SummarizeAlerts(ctx, db, common.RoleRootUser, adminFilter)
	require.NoError(t, err)
	require.Equal(t, 1, summary.Counts["open"])
	var deliveries int64
	require.NoError(t, db.Model(&model.SecurityDelivery{}).Where("kind = ?", "account").Count(&deliveries).Error)
	require.Zero(t, deliveries, "employee visibility must not introduce personal notifications even with consent")
	_, err = GetSelfSecurityAlert(ctx, db, 3, alert.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = GetCustomerAlert(ctx, db, 2, 3, alert.ID)
	require.ErrorIs(t, err, model.ErrCustomerAuditForbidden)

	evidence, err := common.Marshal([]model.SecurityEvent{
		event,
		{EventID: "admin-evidence", ActorID: 3, ActorRole: 10, ResourceType: "user"},
		{EventID: "other-resource", ActorID: 3, ActorRole: 1, ResourceType: "channel"},
		{EventID: "other-target", ActorID: 3, ActorRole: 1, ResourceType: "user", TargetID: 4},
		{EventID: "other-account", ActorID: 4, ActorRole: 1, ResourceType: "user"},
	})
	require.NoError(t, err)
	require.NoError(t, db.Model(&alert).Updates(map[string]any{"evidence": string(evidence), "conclusion_note": "private admin note"}).Error)
	detail, err := GetCustomerAlert(ctx, db, 1, 3, alert.ID)
	require.NoError(t, err)
	require.Len(t, detail.Events, 1)
	require.Equal(t, event.EventID, detail.Events[0].EventID)
	require.True(t, detail.Events[0].Success)
	saved, err := UpdateCustomerAlert(ctx, db, 1, 1, 3, alert.ID, AlertInvestigation{Version: alert.Version, Status: "resolved"})
	require.NoError(t, err)
	require.Equal(t, alert.ID, saved.ID)
	_, err = UpdateCustomerAlert(ctx, db, 1, 1, 3, alert.ID, AlertInvestigation{Version: alert.Version, Status: "resolved"})
	require.ErrorIs(t, err, model.ErrSecurityConflict)
	count, err = CountEmployeePendingAlerts(ctx, db, 1)
	require.NoError(t, err)
	require.Zero(t, count)
	filter.View = "handled"
	page, err = ListCustomerAlerts(ctx, db, filter)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "resolved", page.Items[0].Status)

	// Source retries and IP enrichment must not create another standalone finding.
	require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{event}, state, cfg))
	require.NoError(t, db.Model(&model.SecurityEvent{}).Where("event_id = ?", event.EventID).Updates(map[string]any{"ip_check_state": "pending", "ip_next_attempt_at": 1}).Error)
	event.IPCheckState = "done"
	require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{event}, state, cfg))
	var snapshots []model.SecurityFindingSnapshot
	require.NoError(t, db.Where("alert_id = ?", alert.ID).Find(&snapshots).Error)
	require.Len(t, snapshots, 1)
	require.Equal(t, 4, snapshots[0].RuleVersion)
	expectedKey := model.SecurityAccountRef(fmt.Sprintf("finding:sensitive_operation:4:3:%s", event.EventID))
	require.Equal(t, &expectedKey, snapshots[0].DetectionKey)
	require.NoError(t, db.First(&alert, alert.ID).Error)
	require.EqualValues(t, 1, alert.Occurrences)
	require.Equal(t, "resolved", alert.Status)
	require.Equal(t, "private admin note", alert.ConclusionNote)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("inviter_id", 2).Error)
	_, err = GetCustomerAlert(ctx, db, 1, 3, alert.ID)
	require.ErrorIs(t, err, model.ErrCustomerAuditForbidden)
	detail, err = GetCustomerAlert(ctx, db, 2, 3, alert.ID)
	require.NoError(t, err)
	require.Equal(t, saved.ID, detail.Alert.ID)
}

func TestCustomerSensitiveRiskVersionsAndUpgrade(t *testing.T) {
	db, filter := customerAccessFixture(t)
	state, err := model.ClaimAuditSecurity(t.Context(), db, "sensitive-versions")
	require.NoError(t, err)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.IPIntelligenceEnabled, cfg.NotificationEnabled, cfg.AutoBanEnabled = false, false, false
	cfg.BatchThreshold = 2
	legacyEvent := Normalize(model.AuditLog{EventId: "legacy-password", UserId: 3, ActorRole: 1, Action: "user.password_change", Success: true, CreatedAt: filter.End - 5})
	legacyKey := model.SecurityAccountRef("finding:sensitive_operation:3:3:" + legacyEvent.EventID)
	legacy := model.SecurityAlert{Rule: "sensitive_operation", RuleVersion: 3, ActorID: 3, ActorRole: 1, ResourceType: "user", SubjectRef: legacyEvent.SubjectRef, Visibility: legacyEvent.Visibility, LastSeen: legacyEvent.At, FirstSeen: legacyEvent.At, Status: "resolved", Version: 2, Occurrences: 1}
	require.NoError(t, db.Create(&legacy).Error)
	snapshot := model.SecurityFindingSnapshot{AlertID: legacy.ID, Rule: legacy.Rule, RuleVersion: 3, ActorID: 3, ActorRole: 1, ResourceType: "user", DetectionKey: &legacyKey, TriggerEventID: legacyEvent.EventID}
	require.NoError(t, db.Create(&snapshot).Error)
	require.NoError(t, db.Model(&legacy).Update("latest_finding_id", snapshot.ID).Error)
	require.NoError(t, db.Create(&legacyEvent).Error)
	fresh := legacyEvent
	fresh.EventID, fresh.At = "new-password", filter.End
	require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{legacyEvent, fresh}, state, cfg))
	page, err := ListCustomerAlerts(t.Context(), db, filter)
	require.NoError(t, err)
	require.Len(t, page.Items, 3)
	require.Equal(t, legacy.ID, page.Items[2].ID)
	require.Equal(t, "resolved", page.Items[2].Status)
	var findings int64
	require.NoError(t, db.Model(&model.SecurityFindingSnapshot{}).Where("rule = ?", legacy.Rule).Count(&findings).Error)
	require.EqualValues(t, 2, findings, "an old receipt must not be replayed under a new detection key")

	for i, tc := range []struct {
		name, action string
		role, target int
		version      int
	}{
		{"self password", "user.password_change", 1, 0, 4},
		{"explicit self target", "user.password_change", 1, -2, 4},
		{"account resource", "access_token.update", 1, 0, 4},
		{"administrator", "user.password_change", 10, 0, 3},
		{"root", "user.password_change", 100, 0, 3},
		{"unknown role", "user.password_change", 0, 0, 3},
		{"other target", "user.password_change", 1, 999, 3},
		{"negative target", "user.password_change", 1, -1, 3},
		{"channel", "channel.key_view", 1, 0, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actor := 100 + i
			target := tc.target
			if target == -2 {
				target = actor
			}
			event := Normalize(model.AuditLog{EventId: tc.name, UserId: actor, ActorRole: tc.role, Action: tc.action, Success: true, CreatedAt: filter.End, Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"target_user_id": target}}}})
			require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{event}, state, cfg))
			var saved model.SecurityAlert
			require.NoError(t, db.Where("rule = ? AND actor_id = ?", "sensitive_operation", actor).Take(&saved).Error)
			require.Equal(t, tc.version, saved.RuleVersion)
			var finding model.SecurityFindingSnapshot
			require.NoError(t, db.Where("alert_id = ?", saved.ID).Take(&finding).Error)
			require.Equal(t, tc.version, finding.RuleVersion)
			key := model.SecurityAccountRef(fmt.Sprintf("finding:sensitive_operation:%d:%d:%s", tc.version, actor, event.EventID))
			require.Equal(t, &key, finding.DetectionKey)
			allowed, err := customerAlertAccessible(db, saved, actor)
			require.NoError(t, err)
			require.Equal(t, tc.version == 4, allowed)
		})
	}

	// The same account and subject may have ordinary and management history.
	adminEvent := fresh
	adminEvent.EventID, adminEvent.ActorRole = "promoted-account", 10
	require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{adminEvent}, state, cfg))
	page, err = ListCustomerAlerts(t.Context(), db, filter)
	require.NoError(t, err)
	require.Len(t, page.Items, 2, "only isolated customer standalone and bulk episodes remain visible after management history joins the legacy episode")
	_, err = GetCustomerAlert(t.Context(), db, 1, 3, legacy.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound, "mixed-role legacy snapshots must fail closed")
	require.EqualValues(t, 1, page.Items[0].Occurrences)
	var current model.SecurityAlert
	require.NoError(t, db.Where("actor_id = ? AND rule = ? AND rule_version = ?", 3, "sensitive_operation", 4).Take(&current).Error)
	ordinaryAgain := fresh
	ordinaryAgain.EventID = "ordinary-again"
	require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{ordinaryAgain}, state, cfg))
	require.NoError(t, db.First(&current, current.ID).Error)
	require.EqualValues(t, 2, current.Occurrences)
	var evidence []model.SecurityEvent
	require.NoError(t, common.UnmarshalJsonStr(current.Evidence, &evidence))
	for _, event := range evidence {
		require.Equal(t, 1, event.ActorRole)
		require.Equal(t, 3, event.ActorID)
	}
	var bulk model.SecurityAlert
	require.NoError(t, db.Where("rule = ? AND actor_id = ? AND rule_version = ?", "bulk_sensitive_operations", 3, 4).Take(&bulk).Error)
	require.Equal(t, 4, bulk.RuleVersion)
	allowed, err := customerAlertAccessible(db, bulk, 3)
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestCustomerAuditIndexUpgradeAndRestart(t *testing.T) {
	db := newWorkerTestDB(t)
	require.True(t, db.Migrator().HasIndex(&model.AuditLog{}, "idx_audit_customer_cursor"))
	require.True(t, db.Migrator().HasIndex(&model.SecurityAction{}, "idx_sec_action_cursor"))
	require.NoError(t, db.Migrator().DropIndex(&model.AuditLog{}, "idx_audit_customer_cursor"))
	recorder := &ddlRecorder{Interface: logger.Default.LogMode(logger.Silent)}
	db.Logger = recorder
	require.NoError(t, db.AutoMigrate(&model.AuditLog{}))
	require.NotEmpty(t, recorder.statements)
	require.True(t, db.Migrator().HasIndex(&model.AuditLog{}, "idx_audit_customer_cursor"))
	recorder.statements = nil
	require.NoError(t, db.AutoMigrate(&model.AuditLog{}))
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
	require.Empty(t, recorder.statements, "customer indexes must not cause repeated startup DDL")
}

func TestEmployeeAuditAggregatePagination(t *testing.T) {
	for _, count := range []int{0, 1, 20, 21, 40, 41, 43} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			db, single := customerAccessFixture(t)
			logs := newWorkerTestDB(t)
			users := make([]model.User, 201)
			for i := range users {
				users[i] = model.User{Id: 1000 + i, Username: fmt.Sprintf("customer-%d", i), AffCode: fmt.Sprintf("page-%d", i), Role: 1, Status: 1, InviterId: 1}
			}
			require.NoError(t, db.CreateInBatches(users, 100).Error)
			f := model.EmployeeAuditFilter{EmployeeID: 1, Start: single.Start, End: single.End}
			var entries []model.AuditLog
			var alerts []model.SecurityAlert
			var expectedEvents []string
			var expectedAlerts []int64
			for i := range count {
				customerID := []int{3, 1000, 1200}[i%3]
				eventID := fmt.Sprintf("%c-visible-%03d", []rune{'a', 'B', 'c'}[i%3], i)
				entries = append(entries, model.AuditLog{EventId: eventID, UserId: customerID, ActorRole: 1, CreatedAt: f.End - 10, Action: "login", Success: true, Ip: "192.0.2.1", RequestId: "owned-request"})
				alerts = append(alerts, model.SecurityAlert{ID: int64(i + 1), ActorID: customerID, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, LastSeen: f.End - 10, Status: "open", Version: 1})
				expectedEvents = append(expectedEvents, eventID)
				expectedAlerts = append(expectedAlerts, int64(i+1))
			}
			for i := range 45 {
				entries = append(entries, model.AuditLog{EventId: fmt.Sprintf("hidden-%03d", i), UserId: 3, ActorRole: 1, CreatedAt: f.End, Other: model.AuditOther{AdminInfo: &model.AuditAdminInfo{AdminID: 2}}})
				alerts = append(alerts, model.SecurityAlert{ID: int64(100 + i), ActorID: 3, ActorRole: 1, Rule: "unknown", LastSeen: f.End})
			}
			entries = append(entries, model.AuditLog{EventId: "hidden-tail", UserId: 3, ActorRole: 1, CreatedAt: f.Start, Other: model.AuditOther{AdminInfo: &model.AuditAdminInfo{AdminID: 2}}})
			alerts = append(alerts, model.SecurityAlert{ID: 999, ActorID: 3, ActorRole: 1, Rule: "unknown", LastSeen: f.Start})
			require.NoError(t, logs.CreateInBatches(entries, 100).Error)
			require.NoError(t, db.CreateInBatches(alerts, 100).Error)
			if len(expectedEvents) > 0 {
				// Event IDs contain mixed-case random suffixes. The aggregate
				// order must match the source database's collation and cursor.
				var orderedEvents []string
				require.NoError(t, logs.Model(&model.AuditLog{}).Where("event_id IN ?", expectedEvents).Order("created_at DESC,event_id DESC").Pluck("event_id", &orderedEvents).Error)
				expectedEvents = orderedEvents
			}
			slices.Reverse(expectedAlerts)
			var gotEvents []string
			for {
				page, err := model.QueryEmployeeAuditLogs(t.Context(), db, logs, f)
				require.NoError(t, err)
				require.Len(t, page.Items, min(20, count-len(gotEvents)))
				for _, item := range page.Items {
					require.Contains(t, []int{3, 1000, 1200}, item.Customer.ID)
					require.NotEmpty(t, item.Customer.Username)
					gotEvents = append(gotEvents, item.EventID)
				}
				if page.NextCursor == "" {
					break
				}
				require.Less(t, len(gotEvents), count)
				f.Cursor = page.NextCursor
			}
			require.Equal(t, expectedEvents, gotEvents)
			f.Cursor = ""
			var gotAlerts []int64
			for {
				page, err := ListEmployeeCustomerAlerts(t.Context(), db, f)
				require.NoError(t, err)
				require.Len(t, page.Items, min(20, count-len(gotAlerts)))
				for _, item := range page.Items {
					require.Contains(t, []int{3, 1000, 1200}, item.Customer.ID)
					gotAlerts = append(gotAlerts, item.ID)
				}
				if page.NextCursor == "" {
					break
				}
				require.Less(t, len(gotAlerts), count)
				f.Cursor = page.NextCursor
			}
			require.Equal(t, expectedAlerts, gotAlerts)
		})
	}
}

func TestEmployeeAuditAggregateAccessAndFilters(t *testing.T) {
	db, single := customerAccessFixture(t)
	f := model.EmployeeAuditFilter{EmployeeID: 1, Start: single.Start, End: single.End}
	require.NoError(t, db.Create(&[]model.User{
		{Id: 5, Username: "disabled-customer", InviterId: 1, Role: 1, Status: 2},
		{Id: 6, Username: "admin-customer", InviterId: 1, Role: 10, Status: 1},
		{Id: 7, Username: "employee-customer", InviterId: 1, Role: 1, Status: 1},
		{Id: 8, Username: "deleted-customer", InviterId: 1, Role: 1, Status: 1},
	}).Error)
	require.NoError(t, db.Create(&model.EmployeeProfile{UserId: 7, Status: 1}).Error)
	require.NoError(t, db.Where("id = ?", 8).Delete(&model.User{}).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 1).Update("inviter_id", 1).Error)
	for _, id := range []int{1, 3, 4, 5, 6, 7, 8} {
		require.NoError(t, db.Create(&model.AuditLog{EventId: fmt.Sprintf("account-%d", id), UserId: id, ActorRole: 1, CreatedAt: f.End, Action: "login", RequestId: fmt.Sprintf("req-%d", id), Ip: "192.0.2.1"}).Error)
		require.NoError(t, db.Create(&model.SecurityAlert{ID: int64(id), ActorID: id, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, LastSeen: f.End}).Error)
	}
	// Each alert may only display responses for its own customer.
	require.NoError(t, db.Create(&[]model.SecurityAction{
		{AlertID: 3, Action: "disable_account", TargetUserID: 3, Outcome: "succeeded"},
		{AlertID: 5, Action: "clear_api_keys", TargetUserID: 5, Outcome: "succeeded"},
		{AlertID: 5, Action: "disable_account", TargetUserID: 4, Outcome: "failed"},
	}).Error)
	page, err := model.QueryEmployeeAuditLogs(t.Context(), db, db, f)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.Equal(t, []int{5, 3}, []int{page.Items[0].Customer.ID, page.Items[1].Customer.ID})
	risk, err := ListEmployeeCustomerAlerts(t.Context(), db, f)
	require.NoError(t, err)
	require.Len(t, risk.Items, 2)
	require.Equal(t, 5, risk.Items[0].Customer.ID)
	require.Nil(t, risk.Items[0].Response)
	require.Len(t, risk.Items[0].Responses, 1)
	require.Equal(t, "clear_api_keys", risk.Items[0].Responses[0].Action)
	require.Equal(t, "disable_account", risk.Items[1].Response.Action)
	f.RequestID, f.IP = "req-3", "192.0.2.1"
	page, err = model.QueryEmployeeAuditLogs(t.Context(), db, db, f)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	f.IP = "192.0.2.2"
	page, err = model.QueryEmployeeAuditLogs(t.Context(), db, db, f)
	require.NoError(t, err)
	require.Empty(t, page.Items)
	f.RequestID, f.IP = "", ""
	require.NoError(t, db.Model(&model.User{}).Where("id IN ?", []int{3, 5}).Update("inviter_id", 2).Error)
	page, err = model.QueryEmployeeAuditLogs(t.Context(), db, db, f)
	require.NoError(t, err)
	require.Empty(t, page.Items)
	risk, err = ListEmployeeCustomerAlerts(t.Context(), db, f)
	require.NoError(t, err)
	require.Empty(t, risk.Items)
	require.NoError(t, db.Model(&model.EmployeeProfile{}).Where("user_id = ?", 1).Update("status", 2).Error)
	_, err = model.QueryEmployeeAuditLogs(t.Context(), db, db, f)
	require.ErrorIs(t, err, model.ErrCustomerAuditForbidden)
	_, err = ListEmployeeCustomerAlerts(t.Context(), db, f)
	require.ErrorIs(t, err, model.ErrCustomerAuditForbidden)
}

func TestEmployeeAuditAggregateCursorBoundary(t *testing.T) {
	db, single := customerAccessFixture(t)
	f := model.EmployeeAuditFilter{EmployeeID: 1, Start: single.Start, End: single.End}
	for i := range 21 {
		require.NoError(t, db.Create(&model.AuditLog{EventId: fmt.Sprintf("cursor-%03d", i), UserId: 3, ActorRole: 1, CreatedAt: f.End}).Error)
		require.NoError(t, db.Create(&model.SecurityAlert{ID: int64(i + 1), ActorID: 3, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, LastSeen: f.End}).Error)
	}
	page, err := model.QueryEmployeeAuditLogs(t.Context(), db, db, f)
	require.NoError(t, err)
	require.NotEmpty(t, page.NextCursor)
	single.Cursor = page.NextCursor
	_, err = model.QueryCustomerAuditLogs(t.Context(), db, db, single)
	require.ErrorIs(t, err, model.ErrCustomerAuditParams)
	for _, mutate := range []func(*model.EmployeeAuditFilter){
		func(v *model.EmployeeAuditFilter) { v.EmployeeID = 2 },
		func(v *model.EmployeeAuditFilter) { v.Start++ },
		func(v *model.EmployeeAuditFilter) { v.RequestID = "different" },
		func(v *model.EmployeeAuditFilter) { v.IP = "192.0.2.2" },
		func(v *model.EmployeeAuditFilter) { v.Cursor = "invalid" },
		func(v *model.EmployeeAuditFilter) { v.Cursor = strings.Repeat("a", 2049) },
		func(v *model.EmployeeAuditFilter) { v.End = v.Start + 31*86400 + 1 },
		func(v *model.EmployeeAuditFilter) { v.Start = 0 },
		func(v *model.EmployeeAuditFilter) { v.End = v.Start - 1 },
	} {
		changed := f
		changed.Cursor = page.NextCursor
		mutate(&changed)
		_, err = model.QueryEmployeeAuditLogs(t.Context(), db, db, changed)
		require.ErrorIs(t, err, model.ErrCustomerAuditParams)
	}
	f.Cursor = page.NextCursor
	_, err = ListEmployeeCustomerAlerts(t.Context(), db, f)
	require.ErrorIs(t, err, model.ErrCustomerAuditParams)
	f.Cursor = ""
	risk, err := ListEmployeeCustomerAlerts(t.Context(), db, f)
	require.NoError(t, err)
	f.Cursor = risk.NextCursor
	_, err = model.QueryEmployeeAuditLogs(t.Context(), db, db, f)
	require.ErrorIs(t, err, model.ErrCustomerAuditParams)
	f.Cursor = ""
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = model.QueryEmployeeAuditLogs(cancelled, db, db, f)
	require.ErrorIs(t, err, context.Canceled)
}

func TestEmployeeAuditAggregateOwnershipChangesDuringRead(t *testing.T) {
	for _, kind := range []string{"audit", "risk"} {
		t.Run(kind, func(t *testing.T) {
			db, single := customerAccessFixture(t)
			f := model.EmployeeAuditFilter{EmployeeID: 1, Start: single.Start, End: single.End}
			require.NoError(t, db.Create(&model.AuditLog{EventId: "moving-customer", UserId: 3, ActorRole: 1, CreatedAt: f.End}).Error)
			require.NoError(t, db.Create(&model.SecurityAlert{ActorID: 3, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, LastSeen: f.End}).Error)
			moved := false
			require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:customer-transfer", func(tx *gorm.DB) {
				if !moved && (tx.Statement.Table == "audit_logs" || tx.Statement.Table == "audit_security_alerts") {
					moved = true
					require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("inviter_id", 2).Error)
				}
			}))
			t.Cleanup(func() { db.Callback().Query().Remove("test:customer-transfer") })
			if kind == "audit" {
				page, err := model.QueryEmployeeAuditLogs(t.Context(), db, db, f)
				require.ErrorIs(t, err, model.ErrCustomerAuditScopeChanged)
				require.Empty(t, page.Items)
			} else {
				page, err := ListEmployeeCustomerAlerts(t.Context(), db, f)
				require.ErrorIs(t, err, model.ErrCustomerAuditScopeChanged)
				require.Empty(t, page.Items)
			}
		})
	}
}

func TestCustomerAuditCurrentOwnershipAndProjection(t *testing.T) {
	db, filter := customerAccessFixture(t)
	logs := newWorkerTestDB(t) // Separate main/log databases are the production topology.
	ctx := context.Background()
	no, yes := false, true
	for i := range 23 {
		entry := model.AuditLog{EventId: fmt.Sprintf("customer-%02d", i), UserId: 3, ActorRole: 1, ActorIsEmployee: &no, CreatedAt: filter.End - 10, Action: "login", Success: true, RequestId: "customer-request"}
		require.NoError(t, logs.Create(&entry).Error)
	}
	for _, entry := range []model.AuditLog{
		{EventId: "foreign", UserId: 4, ActorRole: 1},
		{EventId: "former-admin", UserId: 3, ActorRole: 10},
		{EventId: "former-employee", UserId: 3, ActorRole: 1, ActorIsEmployee: &yes},
		{EventId: "legacy-manager", UserId: 3, ActorRole: 1, Other: model.AuditOther{AdminInfo: &model.AuditAdminInfo{AdminID: 2}}},
	} {
		entry.CreatedAt = filter.End
		require.NoError(t, logs.Create(&entry).Error)
	}
	page, err := model.QueryCustomerAuditLogs(ctx, db, logs, filter)
	require.NoError(t, err)
	require.NotEmpty(t, page.NextCursor)
	seen := map[string]bool{}
	for {
		for _, entry := range page.Items {
			require.Equal(t, 3, entry.UserId)
			require.Nil(t, entry.Other.AdminInfo)
			require.NotContains(t, seen, entry.EventId)
			seen[entry.EventId] = true
		}
		if page.NextCursor == "" {
			break
		}
		filter.Cursor = page.NextCursor
		page, err = model.QueryCustomerAuditLogs(ctx, db, logs, filter)
		require.NoError(t, err)
	}
	require.Len(t, seen, 23)
	filter.Cursor = ""
	filter.CustomerID = 4
	_, err = model.QueryCustomerAuditLogs(ctx, db, logs, filter)
	require.ErrorIs(t, err, model.ErrCustomerAuditForbidden)
	filter.CustomerID = 3
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("inviter_id", 2).Error)
	_, err = model.QueryCustomerAuditLogs(ctx, db, logs, filter)
	require.ErrorIs(t, err, model.ErrCustomerAuditForbidden)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Updates(map[string]any{"inviter_id": 1, "status": 2}).Error)
	require.NoError(t, model.RequireEmployeeCustomerAccess(ctx, db, 1, 3), "disabled customers remain investigable")
	require.NoError(t, db.Model(&model.EmployeeProfile{}).Where("user_id = ?", 1).Update("status", 2).Error)
	require.ErrorIs(t, model.RequireEmployeeCustomerAccess(ctx, db, 1, 3), model.ErrCustomerAuditForbidden)
	require.NoError(t, db.Model(&model.EmployeeProfile{}).Where("user_id = ?", 1).Update("status", 1).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 1).Update("status", 2).Error)
	require.ErrorIs(t, model.RequireEmployeeCustomerAccess(ctx, db, 1, 3), model.ErrCustomerAuditForbidden)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 1).Update("status", 1).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("role", 10).Error)
	require.ErrorIs(t, model.RequireEmployeeCustomerAccess(ctx, db, 1, 3), model.ErrCustomerAuditForbidden)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("role", 1).Error)
	require.NoError(t, db.Create(&model.EmployeeProfile{UserId: 3, Status: 1}).Error)
	require.ErrorIs(t, model.RequireEmployeeCustomerAccess(ctx, db, 1, 3), model.ErrCustomerAuditForbidden)
	require.NoError(t, db.Where("user_id = ?", 3).Delete(&model.EmployeeProfile{}).Error)
	require.NoError(t, db.Where("id = ?", 3).Delete(&model.User{}).Error)
	require.ErrorIs(t, model.RequireEmployeeCustomerAccess(ctx, db, 1, 3), model.ErrCustomerAuditForbidden)
}

func TestCustomerRiskProjectionAndInvestigation(t *testing.T) {
	db, filter := customerAccessFixture(t)
	ctx := context.Background()
	events, err := common.Marshal([]model.SecurityEvent{
		{EventID: "own", ActorID: 3, ActorRole: 1, At: filter.End, Action: "login", IP: "192.0.2.1", Facts: `{"internal":"private"}`},
		{EventID: "other", ActorID: 4, ActorRole: 1, At: filter.End},
		{EventID: "anonymous", At: filter.End},
		{EventID: "admin", ActorID: 3, ActorRole: 10, At: filter.End},
	})
	require.NoError(t, err)
	alert := model.SecurityAlert{Rule: "session_reuse", RuleVersion: 2, ActorID: 3, ActorRole: 1, Visibility: 100, Status: "open", Version: 1, LastSeen: filter.End, Evidence: string(events), ConclusionNote: "private admin note", AssigneeID: 88}
	require.NoError(t, db.Create(&alert).Error)
	require.NoError(t, db.Create(&model.SecurityAction{AlertID: alert.ID, ActorID: 88, ActorRole: 100, Source: "human", Note: "private action note", FromStatus: "open", ToStatus: "investigating", CreatedAt: filter.End - 2}).Error)
	page, err := ListCustomerAlerts(ctx, db, filter)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	detail, err := GetCustomerAlert(ctx, db, 1, 3, alert.ID)
	require.NoError(t, err)
	require.Len(t, detail.Events, 1)
	require.Equal(t, "own", detail.Events[0].EventID)
	require.Equal(t, "admin", detail.Actions[0].Source)
	raw, err := common.Marshal(detail)
	require.NoError(t, err)
	for _, forbidden := range []string{"private", "subject_ref", "assignee_id", "actor_id", "facts", "destination"} {
		require.NotContains(t, string(raw), forbidden)
	}
	conclusion := "authorized"
	updated, err := UpdateCustomerAlert(ctx, db, 1, 1, 3, alert.ID, AlertInvestigation{Version: 1, Status: "resolved", Note: "Customer confirmed this sign-in.", Conclusion: &conclusion})
	require.NoError(t, err)
	require.Equal(t, int64(2), updated.Version)
	require.NoError(t, db.First(&alert, alert.ID).Error)
	require.Equal(t, "private admin note", alert.ConclusionNote)
	require.Equal(t, 88, alert.AssigneeID)
	require.NotNil(t, alert.ClosedAt)
	detail, err = GetCustomerAlert(ctx, db, 1, 3, alert.ID)
	require.NoError(t, err)
	require.Equal(t, "employee", detail.Actions[0].Source)
	require.Equal(t, "Customer confirmed this sign-in.", detail.Actions[0].Note)
	_, err = UpdateCustomerAlert(ctx, db, 1, 1, 3, alert.ID, AlertInvestigation{Version: 1, Status: "open"})
	require.ErrorIs(t, err, model.ErrSecurityConflict)
	_, err = UpdateCustomerAlert(ctx, db, 1, 2, 3, alert.ID, AlertInvestigation{Version: 2, Status: "open"})
	require.ErrorIs(t, err, model.ErrCustomerAuditForbidden)
	_, err = UpdateCustomerAlert(ctx, db, 1, 1, 3, alert.ID, AlertInvestigation{Version: 2, Status: "open"})
	require.NoError(t, err)
	var reopened model.SecurityAlert
	require.NoError(t, db.First(&reopened, alert.ID).Error)
	require.Nil(t, reopened.ClosedAt)
	require.NoError(t, db.Model(&model.SecurityAlert{}).Where("id = ?", alert.ID).Updates(map[string]any{"purge_state": "deleting", "version": gorm.Expr("version + 1")}).Error)
	_, err = UpdateCustomerAlert(ctx, db, 1, 1, 3, alert.ID, AlertInvestigation{Version: 3, Status: "resolved"})
	require.ErrorIs(t, err, model.ErrSecurityConflict)
	_, err = GetCustomerAlert(ctx, db, 1, 3, alert.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestCustomerLegacyRiskIdentityAndCursor(t *testing.T) {
	db, filter := customerAccessFixture(t)
	ctx := context.Background()
	for i := range 23 {
		alert := model.SecurityAlert{Rule: "session_reuse", RuleVersion: 1, ActorID: 3, Visibility: 100, Status: "open", Version: 1, LastSeen: filter.End, Occurrences: 1}
		require.NoError(t, db.Create(&alert).Error)
		finding := model.SecurityFindingSnapshot{AlertID: alert.ID, Rule: "session_reuse", ActorID: 3, ActorRole: 1}
		if i == 20 {
			finding.ActorID = 4
		}
		require.NoError(t, db.Create(&finding).Error)
		if i != 21 {
			require.NoError(t, db.Model(&alert).Update("latest_finding_id", finding.ID).Error)
		}
	}
	page, err := ListCustomerAlerts(ctx, db, filter)
	require.NoError(t, err)
	require.Len(t, page.Items, 20)
	require.NotEmpty(t, page.NextCursor)
	filter.Cursor = page.NextCursor
	page, err = ListCustomerAlerts(ctx, db, filter)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	filter.End++
	_, err = ListCustomerAlerts(ctx, db, filter)
	require.ErrorIs(t, err, model.ErrCustomerAuditParams)
	filter.End--
	filter.EmployeeID = 2
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("inviter_id", 2).Error)
	_, err = ListCustomerAlerts(ctx, db, filter)
	require.ErrorIs(t, err, model.ErrCustomerAuditParams, "cursor is bound to the original employee")
	filter.Cursor = ""
	require.NoError(t, db.Model(&model.SecurityAlert{}).Where("actor_id = ?", 3).Update("occurrences", 101).Error)
	page, err = ListCustomerAlerts(ctx, db, filter)
	require.NoError(t, err)
	require.Empty(t, page.Items)
}

func TestCustomerPaginationSkipsHiddenRows(t *testing.T) {
	for _, kind := range []string{"alerts", "audit"} {
		for _, visibleCount := range []int{0, 1, 20, 21, 43} {
			t.Run(fmt.Sprintf("%s/%d", kind, visibleCount), func(t *testing.T) {
				db, filter := customerAccessFixture(t)
				var expected []string
				// The leading and trailing hidden runs span multiple query batches.
				// Equal timestamps exercise the ID/event ID tie-breaker as well.
				for i := range 42 + 2*visibleCount + 22 {
					visible := i >= 42 && i < 42+2*visibleCount && i%2 == 0
					var id string
					if kind == "alerts" {
						row := model.SecurityAlert{Rule: "session_reuse", RuleVersion: 1, ActorID: filter.CustomerID, ActorRole: 1, LastSeen: filter.End - int64(i/3), Status: "open", Version: 1}
						if visible {
							row.RuleVersion = 2
						}
						require.NoError(t, db.Create(&row).Error)
						id = fmt.Sprint(row.ID)
					} else {
						row := model.AuditLog{EventId: fmt.Sprintf("row-%04d", 1000-i), UserId: filter.CustomerID, ActorRole: 1, Action: "login", CreatedAt: filter.End - int64(i/3)}
						if !visible {
							row.Other.AdminInfo = &model.AuditAdminInfo{AdminID: 2}
						}
						require.NoError(t, db.Create(&row).Error)
						id = row.EventId
					}
					if visible {
						expected = append(expected, id)
					}
				}
				var seen []string
				for {
					var ids []string
					var cursor string
					var lastAt, lastID int64
					var lastEventID string
					if kind == "alerts" {
						page, err := ListCustomerAlerts(t.Context(), db, filter)
						require.NoError(t, err)
						cursor = page.NextCursor
						for _, row := range page.Items {
							ids = append(ids, fmt.Sprint(row.ID))
							lastAt, lastID = row.LastSeen, row.ID
						}
					} else {
						page, err := model.QueryCustomerAuditLogs(t.Context(), db, db, filter)
						require.NoError(t, err)
						cursor = page.NextCursor
						for _, row := range page.Items {
							require.Nil(t, row.Other.AdminInfo)
							ids = append(ids, row.EventId)
							lastAt, lastEventID = row.CreatedAt, row.EventId
						}
					}
					require.Len(t, ids, min(model.CustomerAuditPageSize, visibleCount-len(seen)))
					seen = append(seen, ids...)
					if cursor == "" {
						break
					}
					require.Less(t, len(seen), visibleCount, "next_cursor requires another visible record")
					filter.Cursor = cursor
					position, err := filter.DecodeCursor(kind)
					require.NoError(t, err)
					require.Equal(t, lastAt, position.At)
					require.Equal(t, lastID, position.ID)
					require.Equal(t, lastEventID, position.EventID)
				}
				require.ElementsMatch(t, expected, seen, "pagination must neither lose nor repeat visible records")
			})
		}
	}
}

func TestCustomerRiskEpisodesSeparateAccountAndRole(t *testing.T) {
	db, filter := customerAccessFixture(t)
	ctx := context.Background()
	state, err := model.ClaimAuditSecurity(ctx, db, "customer-episodes")
	require.NoError(t, err)
	events := []model.SecurityEvent{
		{EventID: "reuse-a", ActorID: 3, ActorRole: 1},
		{EventID: "reuse-b", ActorID: 4, ActorRole: 1},
		{EventID: "reuse-admin", ActorID: 3, ActorRole: 10},
	}
	for i := range events {
		events[i].Action, events[i].SubjectRef = "auth.session_refresh_reuse", "same-reused-name"
		events[i].At, events[i].Visibility, events[i].ResourceType = filter.End, 100, "account"
	}
	require.NoError(t, Apply(ctx, db, state, events, state, operation_setting.DefaultAuditSecuritySetting()))
	var alerts []model.SecurityAlert
	require.NoError(t, db.Where("rule = ?", "session_reuse").Find(&alerts).Error)
	require.Len(t, alerts, 3)
	page, err := ListCustomerAlerts(ctx, db, filter)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
}

func TestCustomerVerifiedSessionReuseKeepsUnknownActorPrivate(t *testing.T) {
	db, filter := customerAccessFixture(t)
	ctx := context.Background()
	state, err := model.ClaimAuditSecurity(ctx, db, "verified-session-owner")
	require.NoError(t, err)
	// Match the actual capture in service/auth_session.go: the session owner is
	// verified, but it would be incorrect to label the attacker as that user.
	event := Normalize(model.AuditLog{EventId: "known-previous-refresh", CreatedAt: filter.End, UserId: 3, ActorRole: 0, Action: "auth.session_refresh_reuse", Category: model.AuditCategorySecurity, Ip: "192.0.2.1"})
	require.Zero(t, event.ActorRole)
	require.Equal(t, common.RoleRootUser, event.Visibility)
	require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{event}, state, operation_setting.DefaultAuditSecuritySetting()))
	page, err := ListCustomerAlerts(ctx, db, filter)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	detail, err := GetCustomerAlert(ctx, db, 1, 3, page.Items[0].ID)
	require.NoError(t, err)
	require.Len(t, detail.Events, 1)
	require.Equal(t, "known-previous-refresh", detail.Events[0].EventID)
	_, err = GetCustomerAlert(ctx, db, 1, 4, page.Items[0].ID)
	require.ErrorIs(t, err, model.ErrCustomerAuditForbidden)
	for _, rule := range []string{"login_failures", "password_spray", "new_login_source"} {
		require.False(t, customerRiskRoleAllowed(rule, 0), "unknown login actors cannot grant customer access")
	}
}
