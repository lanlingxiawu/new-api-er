package auditsecurity

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func loginTargetEvent(id string, at int64, userID, role int) model.SecurityEvent {
	return Normalize(model.AuditLog{EventId: id, CreatedAt: at, Action: "auth.login_failed", Ip: "192.0.2.1", Other: model.AuditOther{Op: &model.AuditOperation{
		Params:      model.AuditFields{"reason": "invalid_credentials", "attempted_account": "customer-a", "subject_ref": model.SecurityAccountRef("customer-a")},
		LoginTarget: &model.AuditLoginTarget{UserID: userID, Role: role},
	}}})
}

func TestLoginTargetNormalizationTrustBoundary(t *testing.T) {
	for _, role := range []int{1, 10, 100} {
		e := loginTargetEvent("target", 100, 3, role)
		require.Zero(t, e.ActorID)
		require.Zero(t, e.ActorRole)
		require.Equal(t, 3, e.TargetID)
		require.Equal(t, "login-target:"+model.SecurityAccountRef("3"), e.SubjectRef)
		var f Facts
		require.NoError(t, common.UnmarshalJsonStr(e.Facts, &f))
		require.Equal(t, role, f.LoginTarget.Role)
		require.Nil(t, f.Identity)
		public := publicSecurityEvent(e)
		require.NotContains(t, public.Facts, "login_target")
		require.Equal(t, role, public.TargetRole)
	}
	for _, target := range []model.AuditLoginTarget{{UserID: 0, Role: 1}, {UserID: -1, Role: 1}, {UserID: 3, Role: 0}, {UserID: 3, Role: 101}} {
		require.Zero(t, loginTargetEvent("invalid", 100, target.UserID, target.Role).TargetID)
	}
	for _, action := range []string{"auth.login_failed", "auth.login_locked", "auth.recovery_requested", "login"} {
		for _, success := range []bool{false, true} {
			a := model.AuditLog{Action: action, Success: success, Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"login_target": model.AuditLoginTarget{UserID: 3, Role: 1}, "target_user_id": 3, "target_role": 1}}}}
			e := Normalize(a)
			require.Zero(t, e.TargetID, "request params cannot supply a login target")
			a.Other.Op.LoginTarget = &model.AuditLoginTarget{UserID: 3, Role: 1}
			e = Normalize(a)
			allowed := !success && (action == "auth.login_failed" || action == "auth.login_locked")
			require.Equal(t, allowed, e.TargetID == 3)
		}
	}
	login := Normalize(model.AuditLog{Action: "login", Success: true, UserId: 3, ActorRole: 1})
	require.Equal(t, loginTargetEvent("target", 100, 3, 1).SubjectRef, login.SubjectRef)
	for _, identifier := range []string{"3", "login-target:3"} {
		unknown := Normalize(model.AuditLog{Action: "auth.login_failed", Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"subject_ref": model.SecurityAccountRef(identifier), "reason": "invalid_credentials"}}}})
		require.NotEqual(t, login.SubjectRef, unknown.SubjectRef, "an arbitrary attempted account cannot collide with a trusted login target")
	}
}

func TestLoginTargetCountsAndEvidenceStayWithinAccountRole(t *testing.T) {
	cfg := operation_setting.DefaultAuditSecuritySetting()
	event := loginTargetEvent("current", 100, 3, 1)
	var history []model.SecurityEvent
	for i := range 4 {
		history = append(history, loginTargetEvent(fmt.Sprint(i), 99, 3, 1))
	}
	require.Equal(t, "login_failures", Evaluate(event, history, cfg)[0].Rule)
	for _, replacement := range []model.SecurityEvent{
		loginTargetEvent("other-role", 99, 3, 10),
		loginTargetEvent("other-account", 99, 4, 1),
		Normalize(model.AuditLog{EventId: "unverified-name", CreatedAt: 99, Action: "auth.login_failed", Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"reason": "invalid_credentials", "subject_ref": event.SubjectRef}}}}),
	} {
		history[0] = replacement
		require.Empty(t, Evaluate(event, history, cfg))
	}
	for _, role := range []int{1, 10, 100} {
		e := loginTargetEvent("evidence", 100, 3, role)
		a := model.SecurityAlert{Rule: "login_failures", RuleVersion: 2, ResourceType: "account", TargetUserID: &e.TargetID, TargetRole: &role}
		require.True(t, accountEventAccessible(a, e, 3, true))
		require.Equal(t, role == 1, accountEventAccessible(a, e, 3, false))
		require.False(t, accountEventAccessible(a, e, 4, true))
		_, reason := automaticResponseEligibility(e, model.SecurityFinding{Rule: "ip_abuse"}, cfg, 100, false)
		require.Equal(t, "incomplete_evidence", reason)
	}
}

func TestLoginTargetAlertAccessAndOwnership(t *testing.T) {
	db, filter := customerAccessFixture(t)
	state, err := model.ClaimAuditSecurity(t.Context(), db, "login-target")
	require.NoError(t, err)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	for i := range 6 {
		e := loginTargetEvent(fmt.Sprintf("target-%d", i), filter.End-6+int64(i), 3, 1)
		require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{e}, state, cfg))
	}
	page, err := ListAlerts(t.Context(), db, 100, AlertFilter{Start: filter.Start, End: filter.End})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	a := page.Items[0]
	require.Zero(t, a.ActorID)
	require.Equal(t, 3, *a.TargetUserID)
	require.Equal(t, "customer-a", a.TargetUsername)
	require.Equal(t, 1, a.Assignees[0].ID)
	self, err := GetSelfSecurityAlert(t.Context(), db, 3, a.ID)
	require.NoError(t, err)
	require.Len(t, self.Events, 6)
	require.Empty(t, self.Responses)
	_, err = GetSelfSecurityAlert(t.Context(), db, 4, a.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	owned, err := ListCustomerAlerts(t.Context(), db, filter)
	require.NoError(t, err)
	require.Len(t, owned.Items, 1)
	_, err = GetCustomerAlert(t.Context(), db, 2, 3, a.ID)
	require.Error(t, err)
	updated, err := UpdateCustomerAlert(t.Context(), db, 1, 1, 3, a.ID, AlertInvestigation{Version: a.Version, Status: "investigating", Note: "checking failed attempts"})
	require.NoError(t, err)
	require.Equal(t, "investigating", updated.Status)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("inviter_id", 2).Error)
	_, err = GetCustomerAlert(t.Context(), db, 1, 3, a.ID)
	require.Error(t, err)
	_, err = GetCustomerAlert(t.Context(), db, 2, 3, a.ID)
	require.NoError(t, err)
	for _, owner := range []int{0, 1, 2} {
		page, err := ListAlerts(t.Context(), db, 100, AlertFilter{Start: filter.Start, End: filter.End, AssigneeID: &owner})
		require.NoError(t, err)
		if owner == 2 {
			require.Len(t, page.Items, 1)
		} else {
			require.Empty(t, page.Items)
		}
	}
	all, err := ListEmployeeCustomerAlerts(t.Context(), db, model.EmployeeAuditFilter{EmployeeID: 2, Start: filter.Start, End: filter.End})
	require.NoError(t, err)
	require.Len(t, all.Items, 1)
	require.Equal(t, 3, all.Items[0].Customer.ID)
}

func TestLoginTargetMigrationUpgradeAndRestart(t *testing.T) {
	db := newWorkerTestDB(t)
	require.True(t, db.Migrator().HasIndex(&model.SecurityAlert{}, "idx_sec_target_time"))
	require.NoError(t, db.Migrator().DropIndex(&model.SecurityAlert{}, "idx_sec_target_time"))
	for _, field := range []string{"target_user_id", "target_role"} {
		require.NoError(t, db.Migrator().DropColumn(&model.SecurityAlert{}, field))
	}
	require.NoError(t, db.Migrator().DropColumn(&model.SecurityFindingSnapshot{}, "target_role"))
	require.NoError(t, db.Omit("target_user_id", "target_role").Create(&model.SecurityAlert{ID: 72, ActorID: 0, AttemptedAccount: "customer-a", Rule: "login_failures", RuleVersion: 1}).Error)
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
	var a model.SecurityAlert
	require.NoError(t, db.First(&a, 72).Error)
	require.Nil(t, a.TargetUserID, "startup must not infer identity from a current username")
	ddl := &migrationDDLLog{Interface: logger.Default.LogMode(logger.Silent)}
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db.Session(&gorm.Session{Logger: ddl})))
	require.Empty(t, ddl.statements)
}

func TestLoginTargetPaginationAndInvalidScope(t *testing.T) {
	db, f := customerAccessFixture(t)
	userID, role := 3, 1
	for i := range 43 {
		a := model.SecurityAlert{ID: int64(i + 1), ActorID: 3, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, LastSeen: f.End - 1, Status: "open"}
		if i%2 == 0 {
			a.ActorID, a.ActorRole, a.Rule, a.ResourceType = 0, 0, "login_failures", "account"
			a.TargetUserID, a.TargetRole = &userID, &role
		}
		require.NoError(t, db.Create(&a).Error)
	}
	for i := range 45 {
		a := model.SecurityAlert{ID: int64(i + 100), Rule: "login_failures", RuleVersion: 3, ResourceType: "account", TargetUserID: &userID, TargetRole: &role, LastSeen: f.End}
		require.NoError(t, db.Create(&a).Error)
	}
	filter := SelfSecurityFilter{UserID: 3, Start: f.Start, End: f.End}
	var got []int64
	for range 4 {
		page, err := ListSelfSecurityAlerts(t.Context(), db, filter)
		require.NoError(t, err)
		for _, row := range page.Items {
			got = append(got, row.ID)
		}
		if page.NextCursor == "" {
			break
		}
		filter.Cursor = page.NextCursor
	}
	var want []int64
	for id := int64(43); id > 0; id-- {
		want = append(want, id)
	}
	require.Equal(t, want, got)
	for _, tc := range []struct {
		rule                 string
		version, actor, role int
		resource             string
	}{
		{"password_spray", 2, 0, 1, "account"}, {"login_failures", 1, 0, 1, "account"},
		{"login_failures", 3, 0, 1, "account"}, {"login_failures", 2, 4, 1, "account"},
		{"login_failures", 2, 0, 0, "account"}, {"login_failures", 2, 0, 1, "user"},
	} {
		a := model.SecurityAlert{Rule: tc.rule, RuleVersion: tc.version, ActorID: tc.actor, ResourceType: tc.resource, TargetUserID: &userID, TargetRole: &tc.role}
		allowed, err := accountAlertAccessible(db, a, 3, true)
		require.NoError(t, err)
		require.False(t, allowed, "%+v", tc)
	}
}

func TestLoginTargetHistoricalRepair(t *testing.T) {
	for _, scenario := range []string{"complete", "name only", "mixed targets", "missing findings", "missing event", "role conflict", "future version"} {
		t.Run(scenario, func(t *testing.T) {
			db, f := customerAccessFixture(t)
			e := loginTargetEvent("historic-target", f.End-5, 3, 1)
			if scenario == "name only" {
				e.TargetID, e.Facts = 0, `{"attempted_account":"customer-a"}`
			}
			evidence := []model.SecurityEvent{e}
			if scenario == "mixed targets" {
				evidence = append(evidence, loginTargetEvent("other-target", f.End-6, 4, 1))
			}
			raw, err := common.Marshal(evidence)
			require.NoError(t, err)
			a := model.SecurityAlert{Rule: "login_failures", RuleVersion: 1, ResourceType: "account", Occurrences: 1, LatestFindingID: 1, FirstSeen: f.End - 5, LastSeen: f.End - 5, Version: 3, Evidence: string(raw), Status: "open"}
			if scenario == "future version" {
				a.RuleVersion = 99
			}
			require.NoError(t, db.Create(&a).Error)
			if scenario != "missing event" {
				require.NoError(t, db.Create(&e).Error)
			}
			snapshot := model.SecurityFindingSnapshot{ID: 1, AlertID: a.ID, Rule: a.Rule, RuleVersion: a.RuleVersion, TriggerEventID: e.EventID, TriggerAt: e.At, TargetID: e.TargetID, ResourceType: "account"}
			if scenario == "role conflict" {
				role := 10
				snapshot.TargetRole = &role
			}
			if scenario != "missing findings" {
				require.NoError(t, db.Create(&snapshot).Error)
			}
			options := LoginTargetRepairOptions{Start: f.Start, End: f.End, MinID: a.ID, MaxID: a.ID}
			report, err := RepairLoginTargetBatch(t.Context(), db, options)
			require.NoError(t, err)
			require.Len(t, report.Items, 1)
			var saved model.SecurityAlert
			require.NoError(t, db.First(&saved, a.ID).Error)
			require.Nil(t, saved.TargetUserID, "report mode never changes data")
			options.Apply = true
			report, err = RepairLoginTargetBatch(t.Context(), db, options)
			require.NoError(t, err)
			require.NoError(t, db.First(&saved, a.ID).Error)
			if scenario != "complete" {
				require.NotEqual(t, "repaired", report.Items[0].Status)
				require.Nil(t, saved.TargetUserID)
				return
			}
			require.Equal(t, "repaired", report.Items[0].Status)
			require.Equal(t, 3, *saved.TargetUserID)
			require.Zero(t, saved.ActorID)
			require.Equal(t, a.Evidence, saved.Evidence)
			require.Equal(t, a.Occurrences, saved.Occurrences)
			require.Equal(t, a.LastSeen, saved.LastSeen)
			var original model.SecurityFindingSnapshot
			require.NoError(t, db.First(&original, snapshot.ID).Error)
			require.Equal(t, snapshot.RuleVersion, original.RuleVersion)
			require.Nil(t, original.TargetRole)
			detail, err := GetSelfSecurityAlert(t.Context(), db, 3, a.ID)
			require.NoError(t, err)
			require.Len(t, detail.Events, 1)
			report, err = RepairLoginTargetBatch(t.Context(), db, options)
			require.NoError(t, err)
			require.Equal(t, "already_linked", report.Items[0].Status)
			var actions []model.SecurityAction
			require.NoError(t, db.Where("alert_id = ?", a.ID).Find(&actions).Error)
			require.Len(t, actions, 1)
			require.Equal(t, "investigation", actions[0].Action)
			require.Contains(t, actions[0].Result, e.EventID)
		})
	}
}

func TestLoginTargetRepairRefusesStaleVersionAndUnboundedRange(t *testing.T) {
	db, f := customerAccessFixture(t)
	for _, options := range []LoginTargetRepairOptions{
		{}, {Start: f.Start, End: f.End, MinID: 0, MaxID: 5},
		{Start: f.Start, End: f.Start + 31*86400 + 1, MinID: 1, MaxID: 5},
		{Start: f.Start, End: f.End, MinID: 1, MaxID: 5, CursorID: 6, CursorAt: f.End},
	} {
		_, err := RepairLoginTargetBatch(t.Context(), db, options)
		require.Error(t, err)
	}
	a := model.SecurityAlert{Rule: "login_failures", RuleVersion: 1, Version: 1, Status: "open"}
	require.NoError(t, db.Create(&a).Error)
	require.NoError(t, db.Model(&a).Update("version", 2).Error)
	a.Version = 1
	err := commitLoginTargetRepair(db.WithContext(t.Context()), a, model.AuditLoginTarget{UserID: 3, Role: 1}, []string{"proof"})
	require.ErrorIs(t, err, model.ErrSecurityConflict)
	var saved model.SecurityAlert
	require.NoError(t, db.First(&saved, a.ID).Error)
	require.Nil(t, saved.TargetUserID)
	var count int64
	require.NoError(t, db.Model(&model.SecurityAction{}).Where("alert_id = ?", a.ID).Count(&count).Error)
	require.Zero(t, count)
}
