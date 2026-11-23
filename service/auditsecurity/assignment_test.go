package auditsecurity

import (
	"fmt"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func assignmentFixture(t *testing.T) (*gorm.DB, model.SecurityAlert) {
	t.Helper()
	db, f := customerAccessFixture(t)
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}))
	for id := 201; id <= 211; id++ {
		require.NoError(t, db.Create(&model.User{Id: id, Username: "assignment-root-" + strconv.Itoa(id), Role: 100, Status: 1}).Error)
	}
	require.NoError(t, db.Create(&model.User{Id: 212, Username: "assignment-admin", Role: 10, Status: 1}).Error)
	for _, permission := range []authz.Permission{authz.AuditRead, authz.AuditSecurityRead, authz.AuditSecurityManage} {
		require.NoError(t, db.Create(&model.CasbinRule{Ptype: "p", V0: authz.UserSubject(212), V1: permission.Resource, V2: permission.Action, V3: authz.EffectAllow}).Error)
	}
	alert := model.SecurityAlert{Rule: "new_login_source", RuleVersion: 3, ActorID: 3, ActorRole: 1, Visibility: 100, Status: "open", Version: 1, FirstSeen: f.End - 1, LastSeen: f.End, UpdatedAt: f.End, Evidence: "[]"}
	require.NoError(t, db.Create(&alert).Error)
	return db, alert
}

func TestAlertAssignmentFollowsCurrentCustomerOwnership(t *testing.T) {
	db, alert := assignmentFixture(t)
	for _, tc := range []struct {
		name   string
		change func()
		owner  int
	}{
		{"initial", func() {}, 1},
		{"transfer", func() { require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("inviter_id", 2).Error) }, 2},
		{"disabled customer", func() { require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("status", 2).Error) }, 2},
		{"disabled employee", func() { require.NoError(t, db.Model(&model.User{}).Where("id = ?", 2).Update("status", 2).Error) }, 0},
		{"restored employee", func() { require.NoError(t, db.Model(&model.User{}).Where("id = ?", 2).Update("status", 1).Error) }, 2},
		{"disabled profile", func() {
			require.NoError(t, db.Model(&model.EmployeeProfile{}).Where("user_id = ?", 2).Update("status", 2).Error)
		}, 0},
		{"restored profile", func() {
			require.NoError(t, db.Model(&model.EmployeeProfile{}).Where("user_id = ?", 2).Update("status", 1).Error)
		}, 2},
		{"promoted customer", func() { require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("role", 10).Error) }, 0},
		{"ordinary customer", func() { require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("role", 1).Error) }, 2},
		{"employee customer", func() { require.NoError(t, db.Create(&model.EmployeeProfile{UserId: 3, Status: 1}).Error) }, 0},
		{"former employee customer", func() {
			require.NoError(t, db.Model(&model.EmployeeProfile{}).Where("user_id = ?", 3).Update("status", 2).Error)
		}, 2},
		{"deleted customer", func() { require.NoError(t, db.Where("id = ?", 3).Delete(&model.User{}).Error) }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.change()
			detail, err := GetAlert(t.Context(), db, 100, alert.ID)
			require.NoError(t, err)
			if tc.owner == 0 {
				require.Empty(t, detail.Alert.Assignees)
			} else {
				require.Len(t, detail.Alert.Assignees, 1)
				require.Equal(t, tc.owner, detail.Alert.Assignees[0].ID)
				require.Equal(t, "owner", detail.Alert.Assignees[0].Source)
			}
			for _, id := range []int{0, 1, 2} {
				filter := AlertFilter{Start: alert.LastSeen - 10, End: alert.LastSeen + 10, AssigneeID: &id}
				page, err := ListAlerts(t.Context(), db, 100, filter)
				require.NoError(t, err)
				want := 0
				if id == tc.owner {
					want = 1
				}
				require.Len(t, page.Items, want)
				summary, err := SummarizeAlerts(t.Context(), db, 100, filter)
				require.NoError(t, err)
				require.Equal(t, want, summary.Counts["open"])
			}
		})
	}
}

func TestAlertManualAssignmentsAreAtomicAndDoNotExpandAccess(t *testing.T) {
	db, alert := assignmentFixture(t)
	ids := []int{202, 201}
	saved, err := UpdateAlertInvestigation(t.Context(), db, 100, 201, AlertInvestigation{Version: 1, AssigneeIDs: &ids}, alert.ID)
	require.NoError(t, err)
	require.Equal(t, 201, saved.AssigneeID)
	require.Len(t, saved.Assignees, 3)
	require.Equal(t, []int{201, 202}, saved.ManualAssigneeIDs)
	var links []model.SecurityAssignee
	require.NoError(t, db.Where("alert_id = ?", alert.ID).Order("user_id").Find(&links).Error)
	require.Equal(t, []model.SecurityAssignee{{AlertID: alert.ID, UserID: 201}, {AlertID: alert.ID, UserID: 202}}, links)
	for _, invalid := range [][]int{{1}, {212}, {999}, {0}, {-1}, {201, 201}, {201, 202, 203, 204, 205, 206, 207, 208, 209, 210, 211}} {
		_, err := UpdateAlertInvestigation(t.Context(), db, 100, 201, AlertInvestigation{Version: saved.Version, AssigneeIDs: &invalid}, alert.ID)
		require.ErrorIs(t, err, ErrInvalidAlertAssignee)
	}
	_, err = UpdateAlertInvestigation(t.Context(), db, 100, 201, AlertInvestigation{Version: saved.Version, AssigneeIDs: &ids, AssigneeID: &ids[0]}, alert.ID)
	require.ErrorIs(t, err, ErrInvalidAlertAssignee)
	empty := []int{}
	_, err = UpdateAlertInvestigation(t.Context(), db, 100, 201, AlertInvestigation{Version: 1, AssigneeIDs: &empty}, alert.ID)
	require.ErrorIs(t, err, model.ErrSecurityConflict)
	unchanged, err := UpdateAlertInvestigation(t.Context(), db, 100, 201, AlertInvestigation{Version: saved.Version, Note: "keep assignments"}, alert.ID)
	require.NoError(t, err)
	require.Len(t, unchanged.Assignees, 3)
	_, err = GetAlert(t.Context(), db, 10, alert.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = UpdateCustomerAlert(t.Context(), db, 1, 1, 3, alert.ID, AlertInvestigation{Version: unchanged.Version, AssigneeIDs: &ids})
	require.ErrorIs(t, err, model.ErrCustomerAuditParams)
	cleared, err := UpdateAlertInvestigation(t.Context(), db, 100, 201, AlertInvestigation{Version: unchanged.Version, AssigneeIDs: &empty}, alert.ID)
	require.NoError(t, err)
	require.Zero(t, cleared.AssigneeID)
	require.Len(t, cleared.Assignees, 1)
	require.Equal(t, "owner", cleared.Assignees[0].Source)
	detail, err := GetAlert(t.Context(), db, 100, alert.ID)
	require.NoError(t, err)
	require.Len(t, detail.Actions, 3)
	require.Len(t, detail.Actions[0].Assignees, 1)
	require.Len(t, detail.Actions[2].Assignees, 3)
	ids = []int{201, 202, 203, 204, 205, 206, 207, 208, 209, 210}
	saved, err = UpdateAlertInvestigation(t.Context(), db, 100, 201, AlertInvestigation{Version: cleared.Version, AssigneeIDs: &ids}, alert.ID)
	require.NoError(t, err)
	require.Len(t, saved.Assignees, 11)
	for _, id := range ids {
		page, err := ListAlerts(t.Context(), db, 100, AlertFilter{Start: alert.LastSeen - 10, End: alert.LastSeen + 10, AssigneeID: &id})
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
	}
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("inviter_id", 2).Error)
	detail, err = GetAlert(t.Context(), db, 100, alert.ID)
	require.NoError(t, err)
	require.Equal(t, 2, detail.Alert.Assignees[0].ID)
	require.Equal(t, 1, detail.Actions[0].Assignees[0].ID, "history retains the owner at the time of the action")
	require.NoError(t, DeleteAlert(t.Context(), db, 100, alert.ID, saved.Version))
	for range 10 {
		require.NoError(t, model.PurgePendingSecurityAlerts(t.Context(), db))
	}
	var count int64
	require.NoError(t, db.Model(&model.SecurityAssignee{}).Where("alert_id = ?", alert.ID).Count(&count).Error)
	require.Zero(t, count)
}

func TestAssignmentIdentityAndLegacySingleCompatibility(t *testing.T) {
	db, alert := assignmentFixture(t)
	alert.AssigneeID = 201
	require.NoError(t, db.Model(&alert).Update("assignee_id", 201).Error)
	for _, tc := range []struct {
		name, rule         string
		version, actorRole int
		expected           bool
	}{
		{"current", "new_login_source", 3, 1, true},
		{"anonymous", "login_failures", 1, 0, false},
		{"future", "new_login_source", 4, 1, false},
		{"management", "new_login_source", 3, 10, false},
		{"unknown legacy", "new_login_source", 1, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, db.Model(&alert).Updates(map[string]any{"rule": tc.rule, "rule_version": tc.version, "actor_role": tc.actorRole}).Error)
			detail, err := GetAlert(t.Context(), db, 100, alert.ID)
			require.NoError(t, err)
			want := 1
			if tc.expected {
				want++
			}
			require.Len(t, detail.Alert.Assignees, want)
			owner := 1
			page, err := ListAlerts(t.Context(), db, 100, AlertFilter{Start: alert.LastSeen - 10, End: alert.LastSeen + 10, AssigneeID: &owner})
			require.NoError(t, err)
			require.Len(t, page.Items, want-1)
		})
	}
	detectionKey := "assignment-legacy"
	finding := model.SecurityFindingSnapshot{AlertID: alert.ID, ActorID: 3, ActorRole: 1, Rule: "new_login_source", DetectionKey: &detectionKey}
	require.NoError(t, db.Create(&finding).Error)
	require.NoError(t, db.Model(&alert).Updates(map[string]any{"occurrences": 1, "latest_finding_id": finding.ID}).Error)
	detail, err := GetAlert(t.Context(), db, 100, alert.ID)
	require.NoError(t, err)
	require.Len(t, detail.Alert.Assignees, 2)
	owner := 1
	page, err := ListAlerts(t.Context(), db, 100, AlertFilter{Start: alert.LastSeen - 10, End: alert.LastSeen + 10, AssigneeID: &owner})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.NoError(t, db.Model(&finding).Update("actor_id", 4).Error)
	page, err = ListAlerts(t.Context(), db, 100, AlertFilter{Start: alert.LastSeen - 10, End: alert.LastSeen + 10, AssigneeID: &owner})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	legacy := 202
	saved, err := UpdateAlertInvestigation(t.Context(), db, 100, 201, AlertInvestigation{Version: 1, AssigneeID: &legacy}, alert.ID)
	require.NoError(t, err)
	require.Len(t, saved.Assignees, 1)
	require.Equal(t, legacy, saved.Assignees[0].ID)
	exported, err := ExportAlerts(t.Context(), db, 100, AlertFilter{Start: alert.LastSeen - 10, End: alert.LastSeen + 10}, 20)
	require.NoError(t, err)
	require.Equal(t, saved.Assignees, exported.Items[0].Alert.Assignees)
}

func TestCustomerSensitiveRiskIdentityAndAssignment(t *testing.T) {
	db, filter := customerAccessFixture(t)
	for _, tc := range []struct {
		name    string
		alert   map[string]any
		finding map[string]any
		count   int
		stale   bool
		allowed bool
	}{
		{name: "legacy v1", alert: map[string]any{"rule_version": 1}, count: 1, allowed: true},
		{name: "legacy v2", alert: map[string]any{"rule_version": 2}, count: 1, allowed: true},
		{name: "legacy v3", count: 1, allowed: true},
		{name: "legacy unknown header role", alert: map[string]any{"actor_role": 0}, count: 1, allowed: true},
		{name: "legacy null header role", alert: map[string]any{"actor_role": nil}, count: 1, allowed: true},
		{name: "legacy self target", finding: map[string]any{"target_id": 3}, count: 1, allowed: true},
		{name: "legacy account resource", alert: map[string]any{"resource_type": "account"}, count: 1, allowed: true},
		{name: "legacy 100", alert: map[string]any{"occurrences": 100}, count: 100, allowed: true},
		{name: "legacy 101", alert: map[string]any{"occurrences": 101}, count: 101},
		{name: "missing snapshot"},
		{name: "extra snapshot", count: 2},
		{name: "missing latest", alert: map[string]any{"latest_finding_id": 999999}, count: 1},
		{name: "stale latest", alert: map[string]any{"occurrences": 2}, count: 2, stale: true},
		{name: "zero occurrences", alert: map[string]any{"occurrences": 0}, count: 1},
		{name: "null occurrences", alert: map[string]any{"occurrences": nil}, count: 1},
		{name: "null latest", alert: map[string]any{"latest_finding_id": nil}, count: 1},
		{name: "admin header", alert: map[string]any{"actor_role": 10}, count: 1},
		{name: "root header", alert: map[string]any{"actor_role": 100}, count: 1},
		{name: "unknown version", alert: map[string]any{"rule_version": 0}, count: 1},
		{name: "negative version", alert: map[string]any{"rule_version": -1}, count: 1},
		{name: "future version", alert: map[string]any{"rule_version": 6}, count: 1},
		{name: "null version", alert: map[string]any{"rule_version": nil}, count: 1},
		{name: "channel resource", alert: map[string]any{"resource_type": "channel"}, count: 1},
		{name: "null resource", alert: map[string]any{"resource_type": nil}, count: 1},
		{name: "mixed actor", finding: map[string]any{"actor_id": 4}, count: 2, alert: map[string]any{"occurrences": 2}},
		{name: "mixed role", finding: map[string]any{"actor_role": 10}, count: 2, alert: map[string]any{"occurrences": 2}},
		{name: "mixed resource", finding: map[string]any{"resource_type": "account"}, count: 1},
		{name: "mixed rule", finding: map[string]any{"rule": "session_reuse"}, count: 1},
		{name: "mixed version", finding: map[string]any{"rule_version": 2}, count: 1},
		{name: "other target", finding: map[string]any{"target_id": 4}, count: 1},
		{name: "negative target", finding: map[string]any{"target_id": -1}, count: 1},
		{name: "null target", finding: map[string]any{"target_id": nil}, count: 1},
		{name: "null finding actor", finding: map[string]any{"actor_id": nil}, count: 1},
		{name: "null finding role", finding: map[string]any{"actor_role": nil}, count: 1},
		{name: "null finding rule", finding: map[string]any{"rule": nil}, count: 1},
		{name: "null finding resource", finding: map[string]any{"resource_type": nil}, count: 1},
		{name: "null finding version", finding: map[string]any{"rule_version": nil}, count: 1},
		{name: "current without snapshots", alert: map[string]any{"rule_version": 4}, allowed: true},
		{name: "current above legacy bound", alert: map[string]any{"rule_version": 4, "occurrences": 101}, allowed: true},
		{name: "current admin", alert: map[string]any{"rule_version": 4, "actor_role": 10}},
		{name: "current unknown role", alert: map[string]any{"rule_version": 4, "actor_role": 0}},
		{name: "current null role", alert: map[string]any{"rule_version": 4, "actor_role": nil}},
		{name: "current null resource", alert: map[string]any{"rule_version": 4, "resource_type": nil}},
		{name: "verified recovery", alert: map[string]any{"rule_version": 5, "actor_role": 0, "resource_type": "account"}, allowed: true},
		{name: "verified recovery null role", alert: map[string]any{"rule_version": 5, "actor_role": nil, "resource_type": "account"}, allowed: true},
		{name: "recovery wrong resource", alert: map[string]any{"rule_version": 5, "actor_role": 0}},
		{name: "recovery authenticated role", alert: map[string]any{"rule_version": 5, "resource_type": "account"}},
		{name: "recovery admin role", alert: map[string]any{"rule_version": 5, "actor_role": 10, "resource_type": "account"}},
		{name: "legacy bulk needs event proof", alert: map[string]any{"rule": "bulk_sensitive_operations"}, count: 1},
		{name: "current customer bulk", alert: map[string]any{"rule": "bulk_sensitive_operations", "rule_version": 4}, allowed: true},
		{name: "current recovery bulk", alert: map[string]any{"rule": "bulk_sensitive_operations", "rule_version": 5, "actor_role": 0, "resource_type": "account"}, allowed: true},
		{name: "purged current", alert: map[string]any{"rule_version": 4, "purge_state": "purging"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			alert := model.SecurityAlert{Rule: "sensitive_operation", RuleVersion: 3, ActorID: 3, ActorRole: 1, ResourceType: "user", Status: "open", Version: 1, Occurrences: 1, LastSeen: filter.End, Visibility: 10}
			require.NoError(t, db.Create(&alert).Error)
			if len(tc.alert) > 0 {
				require.NoError(t, db.Model(&alert).Updates(tc.alert).Error)
				require.NoError(t, db.First(&alert, alert.ID).Error)
			}
			for i := range tc.count {
				finding := model.SecurityFindingSnapshot{AlertID: alert.ID, Rule: alert.Rule, RuleVersion: alert.RuleVersion, ActorID: 3, ActorRole: 1, ResourceType: alert.ResourceType}
				require.NoError(t, db.Create(&finding).Error)
				if i == tc.count-1 && len(tc.finding) > 0 {
					require.NoError(t, db.Model(&finding).Updates(tc.finding).Error)
				}
				if _, specified := tc.alert["latest_finding_id"]; !specified && (!tc.stale || i == 0) {
					require.NoError(t, db.Model(&alert).Update("latest_finding_id", finding.ID).Error)
				}
			}
			// Load into a fresh value so NULL fields cannot retain prior values.
			var stored model.SecurityAlert
			require.NoError(t, db.First(&stored, alert.ID).Error)
			allowed, err := customerAlertAccessible(db, stored, 3)
			require.NoError(t, err)
			require.Equal(t, tc.allowed, allowed)
			for _, owner := range []int{0, 1} {
				var count int64
				q := db.Model(&model.SecurityAlert{}).Where("audit_security_alerts.id = ?", alert.ID)
				require.NoError(t, filterAlertAssignee(q, owner).Count(&count).Error)
				want := tc.allowed
				if owner == 0 {
					want = !want
				}
				require.Equal(t, want, count == 1, "owner filter %d must agree with employee read authorization", owner)
			}
			detail, err := GetCustomerAlert(t.Context(), db, 1, 3, alert.ID)
			if tc.allowed {
				require.NoError(t, err)
				require.Equal(t, alert.ID, detail.Alert.ID)
			} else {
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			}
		})
	}
}

func TestAssignmentLegacyLatestMustBeNewest(t *testing.T) {
	db, alert := assignmentFixture(t)
	require.NoError(t, db.Model(&alert).Updates(map[string]any{"rule_version": 1, "occurrences": 2}).Error)
	findings := []model.SecurityFindingSnapshot{
		{AlertID: alert.ID, Rule: alert.Rule, ActorID: 3, ActorRole: 1},
		{AlertID: alert.ID, Rule: alert.Rule, ActorID: 3, ActorRole: 1},
	}
	require.NoError(t, db.Create(&findings).Error)
	require.NoError(t, db.Model(&alert).Update("latest_finding_id", findings[0].ID).Error)
	var count int64
	require.NoError(t, filterAlertAssignee(db.Model(&model.SecurityAlert{}), 1).Count(&count).Error)
	require.Zero(t, count, "an existing but stale pointer is not complete identity proof")
	require.NoError(t, filterAlertAssignee(db.Model(&model.SecurityAlert{}), 0).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestCustomerSensitiveRiskAssignmentQueryPlan(t *testing.T) {
	if os.Getenv("TEST_ADMIN_TOKEN_DSN") == "" {
		t.Skip("real isolated database required")
	}
	db, filter := customerAccessFixture(t)
	alerts := make([]model.SecurityAlert, 5000)
	findings := make([]model.SecurityFindingSnapshot, len(alerts))
	for i := range alerts {
		actor := 4
		if i == 0 {
			actor = 3
		}
		id := int64(i + 1)
		alerts[i] = model.SecurityAlert{ID: id, Rule: "sensitive_operation", RuleVersion: 3, ActorID: actor, ActorRole: 1, ResourceType: "user", Visibility: 10, LastSeen: filter.End, Occurrences: 1, LatestFindingID: id}
		findings[i] = model.SecurityFindingSnapshot{ID: id, AlertID: id, Rule: "sensitive_operation", RuleVersion: 3, ActorID: actor, ActorRole: 1, ResourceType: "user"}
	}
	require.NoError(t, db.CreateInBatches(alerts, 100).Error)
	require.NoError(t, db.CreateInBatches(findings, 100).Error)
	if db.Dialector.Name() == "postgres" {
		require.NoError(t, db.Exec("ANALYZE audit_security_findings").Error)
		require.NoError(t, db.Exec("ANALYZE audit_security_alerts").Error)
	} else {
		require.NoError(t, db.Exec("ANALYZE TABLE audit_security_findings, audit_security_alerts").Error)
	}
	var query alertPlanQuery
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:customer-sensitive-plan", func(tx *gorm.DB) {
		if query.sql == "" && tx.Statement.Table == "audit_security_alerts" && strings.Contains(tx.Statement.SQL.String(), "af.alert_id") {
			query = alertPlanQuery{sql: tx.Statement.SQL.String(), vars: append([]any(nil), tx.Statement.Vars...)}
		}
	}))
	t.Cleanup(func() { db.Callback().Query().Remove("test:customer-sensitive-plan") })
	owner := 1
	page, err := ListAlerts(t.Context(), db, 100, AlertFilter{Start: filter.Start, End: filter.End, AssigneeID: &owner})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.NotEmpty(t, query.sql)
	explain := "EXPLAIN "
	if db.Dialector.Name() == "mysql" {
		explain = "EXPLAIN FORMAT=TRADITIONAL "
	}
	var plan []map[string]any
	require.NoError(t, db.Raw(explain+db.Dialector.Explain(query.sql, query.vars...)).Scan(&plan).Error)
	for _, row := range plan {
		for column, value := range row {
			if raw, ok := value.([]byte); ok {
				row[column] = string(raw)
			}
		}
	}
	raw, err := common.Marshal(plan)
	require.NoError(t, err)
	t.Logf("%s sensitive risk assignment PLAN: %s", db.Dialector.Name(), raw)
	indexed := 0
	if db.Dialector.Name() == "mysql" {
		for _, row := range plan {
			if row["table"] != "af" {
				continue
			}
			require.NotEmpty(t, row["key"])
			require.Contains(t, []string{"ref", "eq_ref", "range"}, row["type"], "snapshot proofs must use a bounded lookup")
			indexed++
		}
	} else {
		require.NotContains(t, string(raw), "Seq Scan on audit_security_findings", "do not materialize all findings for a latest-snapshot existence check")
		for _, row := range plan {
			line, _ := row["QUERY PLAN"].(string)
			if strings.Contains(line, "Index Cond:") && strings.Contains(line, "alert_id = ") {
				indexed++
			}
		}
	}
	require.GreaterOrEqual(t, indexed, 3, "legacy count, latest ID and identity checks must retain alert-scoped index access")
}

func TestAssignmentSchemaUpgradeAndRestart(t *testing.T) {
	db, alert := assignmentFixture(t)
	require.NoError(t, db.Model(&alert).Update("assignee_id", 201).Error)
	require.NoError(t, db.Migrator().DropTable(&model.SecurityAssignee{}))
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
	require.True(t, db.Migrator().HasIndex(&model.SecurityAssignee{}, "idx_sec_assignee_user"))
	recorder := &ddlRecorder{Interface: logger.Default.LogMode(logger.Silent)}
	db.Logger = recorder
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
	require.Empty(t, recorder.statements)
	detail, err := GetAlert(t.Context(), db, 100, alert.ID)
	require.NoError(t, err)
	require.Equal(t, 201, detail.Alert.AssigneeID)
	require.Len(t, detail.Alert.Assignees, 2)
}

func TestAssignmentCustomerBulkSensitiveOperationOwner(t *testing.T) {
	db, filter := customerAccessFixture(t)
	state, err := model.ClaimAuditSecurity(t.Context(), db, "assignment-review-bulk")
	require.NoError(t, err)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.IPIntelligenceEnabled, cfg.NotificationEnabled, cfg.AutoBanEnabled = false, false, false
	cfg.BatchThreshold = 3
	for i := range 3 {
		event := Normalize(model.AuditLog{EventId: fmt.Sprintf("assignment-review-password-%d", i), UserId: 3, ActorRole: 1, Action: "user.password_change", Success: true, CreatedAt: filter.End - 3 + int64(i)})
		require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{event}, state, cfg))
	}
	page, err := ListAlerts(t.Context(), db, common.RoleRootUser, AlertFilter{Start: filter.Start, End: filter.End, Rule: "bulk_sensitive_operations"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	t.Logf("customer bulk alert: rule=%s version=%d actor=%d role=%d resource=%s matched=%d owners=%d", page.Items[0].Rule, page.Items[0].RuleVersion, page.Items[0].ActorID, page.Items[0].ActorRole, page.Items[0].ResourceType, page.Items[0].MatchedCount, len(page.Items[0].Assignees))
	require.Equal(t, []model.SecurityAlertAssignee{{ID: 1, Username: "employee-a", Role: 1, Source: "owner"}}, page.Items[0].Assignees)
}

func TestAssignmentUnassignedFilterRetainsNullIdentity(t *testing.T) {
	for _, field := range []string{"actor_role", "rule_version", "rule", "actor_id"} {
		t.Run(field, func(t *testing.T) {
			db, alert := assignmentFixture(t)
			require.NoError(t, db.Model(&alert).Update(field, nil).Error)
			filter := AlertFilter{Start: alert.LastSeen - 1, End: alert.LastSeen + 1}
			page, err := ListAlerts(t.Context(), db, common.RoleRootUser, filter)
			require.NoError(t, err)
			require.Len(t, page.Items, 1)
			require.Empty(t, page.Items[0].Assignees)
			unassigned := 0
			filter.AssigneeID = &unassigned
			page, err = ListAlerts(t.Context(), db, common.RoleRootUser, filter)
			require.NoError(t, err)
			t.Logf("%s=NULL: unfiltered shows unassigned; unassigned filter returns %d rows", field, len(page.Items))
			require.Len(t, page.Items, 1, "an unassigned alert must remain discoverable under the unassigned filter")
			summary, err := SummarizeAlerts(t.Context(), db, 100, filter)
			require.NoError(t, err)
			require.Equal(t, 1, summary.Counts["open"])
			exported, err := ExportAlerts(t.Context(), db, 100, filter, 20)
			require.NoError(t, err)
			require.Len(t, exported.Items, 1)
		})
	}
}

func TestAssignmentSessionReuseNullRoleFilterMatchesDisplay(t *testing.T) {
	db, alert := assignmentFixture(t)
	require.NoError(t, db.Model(&alert).Updates(map[string]any{"rule": "session_reuse", "rule_version": 2, "actor_role": nil}).Error)
	filter := AlertFilter{Start: alert.LastSeen - 1, End: alert.LastSeen + 1}
	page, err := ListAlerts(t.Context(), db, common.RoleRootUser, filter)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Len(t, page.Items[0].Assignees, 1)
	owner := page.Items[0].Assignees[0].ID
	filter.AssigneeID = &owner
	page, err = ListAlerts(t.Context(), db, common.RoleRootUser, filter)
	require.NoError(t, err)
	t.Logf("session_reuse actor_role=NULL: display owner=%d; owner filter returns %d rows", owner, len(page.Items))
	require.Len(t, page.Items, 1, "the owner filter must agree with the displayed owner")
	// Nullable legacy snapshots decode to the same anonymous role zero in Go.
	require.NoError(t, db.Model(&alert).Updates(map[string]any{"rule_version": nil, "occurrences": 1}).Error)
	snapshot := model.SecurityFindingSnapshot{AlertID: alert.ID, Rule: "session_reuse", ActorID: 3}
	require.NoError(t, db.Create(&snapshot).Error)
	require.NoError(t, db.Model(&snapshot).Update("actor_role", nil).Error)
	require.NoError(t, db.Model(&alert).Update("latest_finding_id", snapshot.ID).Error)
	detail, err := GetAlert(t.Context(), db, 100, alert.ID)
	require.NoError(t, err)
	require.Len(t, detail.Alert.Assignees, 1)
	page, err = ListAlerts(t.Context(), db, 100, filter)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	summary, err := SummarizeAlerts(t.Context(), db, 100, filter)
	require.NoError(t, err)
	require.Equal(t, 1, summary.Counts["open"])
	exported, err := ExportAlerts(t.Context(), db, 100, filter, 20)
	require.NoError(t, err)
	require.Len(t, exported.Items, 1)
}

func TestAssignmentCommittedPasswordRecoveryOwner(t *testing.T) {
	previous := operation_setting.GetAuditSecuritySetting()
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"capture_enabled": "true", "suspended": "false"}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"capture_enabled": strconv.FormatBool(previous.CaptureEnabled), "suspended": strconv.FormatBool(previous.Suspended)}))
		for {
			if _, ok := model.TakeSecurityCapture(); !ok {
				break
			}
		}
	})
	for _, cleanupFailed := range []bool{false, true} {
		t.Run(fmt.Sprintf("cleanup_failed=%t", cleanupFailed), func(t *testing.T) {
			db, filter := customerAccessFixture(t)
			state, err := model.ClaimAuditSecurity(t.Context(), db, "assignment-review-recovery")
			require.NoError(t, err)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", "/api/user/reset", nil)
			identity := model.AuditIdentity{ActorID: 3, Role: common.RoleCommonUser, AuthVersion: 1, EffectiveVersion: 2, AuthMethod: "recovery"}
			model.CapturePasswordRecoveryOutcome(ctx, identity, cleanupFailed)
			captured, ok := model.TakeSecurityCapture()
			require.True(t, ok)
			require.Equal(t, 3, captured.UserId)
			require.Zero(t, captured.ActorRole, "recovery must not claim an authenticated operator")
			event := Normalize(captured)
			var facts Facts
			require.NoError(t, common.UnmarshalJsonStr(event.Facts, &facts))
			require.Equal(t, &identity, facts.Identity, "the server-committed target identity is retained")
			cfg := operation_setting.DefaultAuditSecuritySetting()
			cfg.IPIntelligenceEnabled, cfg.NotificationEnabled, cfg.AutoBanEnabled = false, false, false
			require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{event}, state, cfg))
			page, err := ListAlerts(t.Context(), db, common.RoleRootUser, AlertFilter{Start: filter.Start, End: event.At + 1, Rule: "sensitive_operation"})
			require.NoError(t, err)
			require.Len(t, page.Items, 1)
			t.Logf("recovery action=%s actor=%d role=%d resource=%s rule_version=%d owners=%d", event.Action, event.ActorID, event.ActorRole, event.ResourceType, page.Items[0].RuleVersion, len(page.Items[0].Assignees))
			require.Equal(t, []model.SecurityAlertAssignee{{ID: 1, Username: "employee-a", Role: 1, Source: "owner"}}, page.Items[0].Assignees)
			require.Equal(t, 5, page.Items[0].RuleVersion)
			require.Zero(t, page.Items[0].ActorRole)
			alert := page.Items[0]
			for _, owner := range []int{0, 1, 2} {
				f := AlertFilter{Start: filter.Start, End: event.At + 1, Rule: alert.Rule, AssigneeID: &owner}
				filtered, err := ListAlerts(t.Context(), db, 100, f)
				require.NoError(t, err)
				want := 0
				if owner == 1 {
					want = 1
				}
				require.Len(t, filtered.Items, want)
				summary, err := SummarizeAlerts(t.Context(), db, 100, f)
				require.NoError(t, err)
				require.Equal(t, want, summary.Counts["open"])
				exported, err := ExportAlerts(t.Context(), db, 100, f, 20)
				require.NoError(t, err)
				require.Len(t, exported.Items, want)
			}
			detail, err := GetCustomerAlert(t.Context(), db, 1, 3, alert.ID)
			require.NoError(t, err)
			require.Len(t, detail.Events, 1)
			_, err = GetSelfSecurityAlert(t.Context(), db, 3, alert.ID)
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			_, err = GetCustomerAlert(t.Context(), db, 2, 3, alert.ID)
			require.ErrorIs(t, err, model.ErrCustomerAuditForbidden)
		})
	}
}

func TestAssignmentHistoricalCustomerRepair(t *testing.T) {
	db, filter := customerAccessFixture(t)
	for _, kind := range []string{"committed", "cleanup_failed", "bulk", "bulk_recovery"} {
		for _, scenario := range []string{"complete", "no evidence", "wrong role", "other target", "missing snapshot target", "truncated", "extra snapshot", "wrong count"} {
			t.Run(kind+"/"+scenario, func(t *testing.T) {
				bulk := strings.HasPrefix(kind, "bulk")
				events := []model.SecurityEvent{}
				count := 1
				if bulk {
					count = 3
				}
				for i := range count {
					log := model.AuditLog{EventId: fmt.Sprintf("repair-%s-%s-%d", kind, strings.ReplaceAll(scenario, " ", "-"), i), UserId: 3, Action: "auth.password_reset_committed", Success: true, CreatedAt: filter.End - int64(count-i-1), Other: model.AuditOther{Op: &model.AuditOperation{Identity: &model.AuditIdentity{ActorID: 3, Role: 1, AuthMethod: "recovery", AuthVersion: 1, EffectiveVersion: 2}}}}
					if kind == "bulk" {
						log.Action, log.ActorRole = "user.password_change", 1
					}
					if kind == "cleanup_failed" {
						log.Action = "auth.password_reset_cleanup_failed"
					}
					events = append(events, Normalize(log))
				}
				trigger := events[len(events)-1]
				alert := model.SecurityAlert{Rule: "sensitive_operation", RuleVersion: 3, ActorID: 3, ActorRole: trigger.ActorRole, ResourceType: trigger.ResourceType, FirstSeen: trigger.At, LastSeen: trigger.At, Version: 1, Status: "open", Occurrences: 1, MatchedCount: count, Visibility: trigger.Visibility}
				if bulk {
					alert.Rule = "bulk_sensitive_operations"
				}
				if scenario == "wrong role" {
					events[0].ActorRole = 10
				}
				if scenario == "other target" {
					events[0].TargetID = 4
				}
				raw, err := common.Marshal(events)
				require.NoError(t, err)
				alert.Evidence = string(raw)
				if scenario == "no evidence" {
					alert.Evidence = "[]"
				}
				require.NoError(t, db.Create(&alert).Error)
				snapshot := model.SecurityFindingSnapshot{AlertID: alert.ID, Rule: alert.Rule, RuleVersion: 3, ActorID: 3, ActorRole: trigger.ActorRole, ResourceType: trigger.ResourceType, TriggerEventID: trigger.EventID, TriggerAt: trigger.At, MatchedCount: count, Dimensions: model.SecurityDimensions{}, Truncated: scenario == "truncated"}
				if bulk {
					snapshot.Threshold, snapshot.WindowSeconds = 3, 300
				}
				if scenario == "wrong count" {
					snapshot.MatchedCount++
				}
				require.NoError(t, db.Create(&snapshot).Error)
				require.NoError(t, db.Model(&alert).Update("latest_finding_id", snapshot.ID).Error)
				if scenario == "missing snapshot target" {
					require.NoError(t, db.Model(&snapshot).Update("target_id", nil).Error)
				}
				if scenario == "extra snapshot" {
					extra := snapshot
					extra.ID = 0
					require.NoError(t, db.Create(&extra).Error)
				}
				options := LoginTargetRepairOptions{Start: filter.Start, End: filter.End, MinID: alert.ID, MaxID: alert.ID, CustomerAssignments: true}
				page, err := RepairLoginTargetBatch(t.Context(), db, options)
				require.NoError(t, err)
				require.Len(t, page.Items, 1)
				var unchanged model.SecurityAlert
				require.NoError(t, db.First(&unchanged, alert.ID).Error)
				require.Equal(t, 3, unchanged.RuleVersion, "preview must not change historical data")
				valid := scenario == "complete" || scenario == "wrong count" && !bulk
				if valid {
					require.Equal(t, "candidate", page.Items[0].Status)
				} else {
					require.NotEqual(t, "candidate", page.Items[0].Status)
				}
				options.Apply = true
				page, err = RepairLoginTargetBatch(t.Context(), db, options)
				require.NoError(t, err)
				var saved model.SecurityAlert
				require.NoError(t, db.First(&saved, alert.ID).Error)
				if !valid {
					require.NotEqual(t, "repaired", page.Items[0].Status)
					require.Equal(t, 3, saved.RuleVersion)
					require.Equal(t, alert.Version, saved.Version)
					return
				}
				require.Equal(t, "repaired", page.Items[0].Status)
				version := 5
				if kind == "bulk" {
					version = 4
				}
				require.Equal(t, version, saved.RuleVersion)
				require.Equal(t, alert.Version+1, saved.Version)
				require.Equal(t, alert.ActorRole, saved.ActorRole)
				require.Equal(t, alert.Evidence, saved.Evidence)
				require.Equal(t, alert.LastSeen, saved.LastSeen)
				var original model.SecurityFindingSnapshot
				require.NoError(t, db.First(&original, snapshot.ID).Error)
				require.Equal(t, snapshot, original)
				var ownerCount int64
				require.NoError(t, filterAlertAssignee(db.Model(&model.SecurityAlert{}).Where("id = ?", alert.ID), 1).Count(&ownerCount).Error)
				require.EqualValues(t, 1, ownerCount)
				detail, err := GetCustomerAlert(t.Context(), db, 1, 3, alert.ID)
				require.NoError(t, err)
				require.Len(t, detail.Events, count)
				_, err = GetSelfSecurityAlert(t.Context(), db, 3, alert.ID)
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				page, err = RepairLoginTargetBatch(t.Context(), db, options)
				require.NoError(t, err)
				require.Equal(t, "already_linked", page.Items[0].Status)
				require.ErrorIs(t, commitCustomerAssignmentRepair(db.WithContext(t.Context()), alert, version, []string{trigger.EventID}), model.ErrSecurityConflict)
				var actions []model.SecurityAction
				require.NoError(t, db.Where("alert_id = ?", alert.ID).Find(&actions).Error)
				require.Len(t, actions, 1)
				require.Equal(t, "repair", actions[0].Source)
				require.Contains(t, actions[0].Result, trigger.EventID)
			})
		}
	}
}

func TestAssignmentHistoricalRecoveryRequiresEveryTrigger(t *testing.T) {
	db, filter := customerAccessFixture(t)
	for _, keepSource := range []bool{true, false} {
		t.Run(strconv.FormatBool(keepSource), func(t *testing.T) {
			first := Normalize(model.AuditLog{EventId: fmt.Sprintf("older-recovery-%t", keepSource), UserId: 3, Action: "auth.password_reset_committed", Success: true, CreatedAt: filter.End - 1,
				Other: model.AuditOther{Op: &model.AuditOperation{Identity: &model.AuditIdentity{ActorID: 3, Role: 1, AuthMethod: "recovery", AuthVersion: 1, EffectiveVersion: 2}}}})
			last := first
			last.EventID, last.At = fmt.Sprintf("latest-recovery-%t", keepSource), filter.End
			raw, err := common.Marshal([]model.SecurityEvent{last})
			require.NoError(t, err)
			alert := model.SecurityAlert{Rule: "sensitive_operation", RuleVersion: 3, ActorID: 3, ResourceType: "account", FirstSeen: first.At, LastSeen: last.At, Version: 1, Status: "open", Occurrences: 2, Evidence: string(raw)}
			require.NoError(t, db.Create(&alert).Error)
			for _, event := range []model.SecurityEvent{first, last} {
				snapshot := model.SecurityFindingSnapshot{AlertID: alert.ID, Rule: alert.Rule, RuleVersion: 3, ActorID: 3, ResourceType: "account", TriggerEventID: event.EventID, TriggerAt: event.At}
				require.NoError(t, db.Create(&snapshot).Error)
				alert.LatestFindingID = snapshot.ID
			}
			require.NoError(t, db.Model(&alert).Update("latest_finding_id", alert.LatestFindingID).Error)
			if keepSource {
				require.NoError(t, db.Create(&first).Error)
			}
			version, _, status, err := verifyHistoricalCustomerAssignment(db, alert)
			require.NoError(t, err)
			if keepSource {
				require.Equal(t, "candidate", status)
				require.Equal(t, 5, version)
			} else {
				require.Equal(t, "missing_evidence", status)
				require.Zero(t, version)
			}
		})
	}
}
