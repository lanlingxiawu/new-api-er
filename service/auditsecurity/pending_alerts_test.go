package auditsecurity

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPendingAlertCountBoundaries(t *testing.T) {
	for _, size := range []int{0, 1, 99, 100, 101} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			db, _ := customerAccessFixture(t)
			for i := range size {
				a := model.SecurityAlert{ActorID: 3, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, Status: []string{"open", "investigating"}[i%2], Visibility: 10, LastSeen: 1}
				require.NoError(t, db.Create(&a).Error)
			}
			count, err := CountEmployeePendingAlerts(t.Context(), db, 1)
			require.NoError(t, err)
			require.Equal(t, min(size, 100), count)
			count, err = CountPendingAlerts(t.Context(), db, common.RoleAdminUser)
			require.NoError(t, err)
			require.Equal(t, min(size, 100), count)
		})
	}
}

func TestPendingCustomerScopeAndPaging(t *testing.T) {
	db, _ := customerAccessFixture(t)
	users := make([]model.User, 201)
	for i := range users {
		users[i] = model.User{Id: 1000 + i, Username: fmt.Sprintf("pending-%d", i), AffCode: fmt.Sprintf("pending-%d", i), Role: 1, Status: 1, InviterId: 1}
	}
	require.NoError(t, db.CreateInBatches(users, 100).Error)
	var want []int64
	for _, customer := range []int{3, 1200} {
		// Hidden rows must not consume a page or hide later visible records.
		for range 25 {
			require.NoError(t, db.Create(&model.SecurityAlert{ActorID: customer, ActorRole: 1, Rule: "unknown", Status: "open"}).Error)
		}
		for i := range 25 {
			a := model.SecurityAlert{ActorID: customer, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, Status: []string{"open", "investigating"}[i%2], LastSeen: 1}
			require.NoError(t, db.Create(&a).Error)
			want = append(want, a.ID)
		}
	}
	filter := PendingCustomerFilter{EmployeeID: 1}
	var got []int64
	for range 4 {
		page, err := ListPendingCustomerAlerts(t.Context(), db, filter)
		require.NoError(t, err)
		require.LessOrEqual(t, len(page.Items), 20)
		for _, item := range page.Items {
			got = append(got, item.ID)
			require.Contains(t, []int{3, 1200}, item.Customer.ID)
		}
		if page.NextCursor == "" {
			break
		}
		filter.Cursor = page.NextCursor
	}
	require.Equal(t, want, got)
	_, err := ListPendingCustomerAlerts(t.Context(), db, PendingCustomerFilter{EmployeeID: 1, CustomerID: 3, Cursor: filter.Cursor})
	require.ErrorIs(t, err, model.ErrCustomerAuditParams)
	_, err = ListPendingCustomerAlerts(t.Context(), db, PendingCustomerFilter{EmployeeID: 2, Cursor: filter.Cursor})
	require.ErrorIs(t, err, model.ErrCustomerAuditParams)
	count, err := CountEmployeePendingAlerts(t.Context(), db, 1)
	require.NoError(t, err)
	require.Equal(t, 50, count)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 1200).Update("inviter_id", 2).Error)
	count, err = CountEmployeePendingAlerts(t.Context(), db, 1)
	require.NoError(t, err)
	require.Equal(t, 25, count)
	require.NoError(t, db.Model(&model.EmployeeProfile{}).Where("user_id = ?", 1).Update("status", 0).Error)
	_, err = CountEmployeePendingAlerts(t.Context(), db, 1)
	require.ErrorIs(t, err, model.ErrCustomerAuditForbidden)
}

func TestPendingAlertVisibilityAndStatus(t *testing.T) {
	db, _ := customerAccessFixture(t)
	archived := int64(100)
	account, role := 3, 1
	rows := []model.SecurityAlert{
		{ActorID: 3, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, Status: "open", Visibility: 10},
		{ActorID: 0, TargetUserID: &account, TargetRole: &role, ResourceType: "account", Rule: "login_failures", RuleVersion: 2, Status: "investigating", Visibility: 100},
		{ActorID: 3, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, Status: "resolved", Visibility: 10},
		{ActorID: 3, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, Status: "false_positive", Visibility: 10},
		{ActorID: 3, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, Status: "open", Visibility: 10, ArchivedAt: &archived},
		{ActorID: 3, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, Status: "open", Visibility: 10, PurgeState: "deleting"},
		{ActorID: 4, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, Status: "open", Visibility: 10},
		{ActorID: 3, ActorRole: 100, Rule: "session_reuse", RuleVersion: 2, Status: "open", Visibility: 100},
		{ActorID: 3, ActorRole: 1, Rule: "session_reuse", RuleVersion: 1, Occurrences: 1, Status: "open", Visibility: 10},
	}
	require.NoError(t, db.Create(&rows).Error)
	count, err := CountEmployeePendingAlerts(t.Context(), db, 1)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	count, err = CountPendingAlerts(t.Context(), db, common.RoleAdminUser)
	require.NoError(t, err)
	require.Equal(t, 3, count)
	count, err = CountPendingAlerts(t.Context(), db, common.RoleRootUser)
	require.NoError(t, err)
	require.Equal(t, 5, count)
	// Successful processing immediately changes the next count and pending list.
	require.NoError(t, db.Model(&model.SecurityAlert{}).Where("id = ?", rows[0].ID).Update("status", "resolved").Error)
	page, err := ListPendingCustomerAlerts(t.Context(), db, PendingCustomerFilter{EmployeeID: 1, CustomerID: 3})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, rows[1].ID, page.Items[0].ID)
}

func TestPendingAlertReadRejectsInFlightScopeChanges(t *testing.T) {
	for _, reader := range []string{"count", "list"} {
		for _, change := range []string{"owner", "employee", "cancel"} {
			t.Run(reader+"/"+change, func(t *testing.T) {
				db, _ := customerAccessFixture(t)
				require.NoError(t, db.Create(&model.SecurityAlert{ActorID: 3, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, Status: "open"}).Error)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				changed := false
				require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:pending-scope-change", func(tx *gorm.DB) {
					if changed || tx.Statement.Table != "audit_security_alerts" || !strings.Contains(tx.Statement.SQL.String(), "rule_version") {
						return
					}
					changed = true
					switch change {
					case "owner":
						require.NoError(t, db.Model(&model.User{}).Where("id = ?", 3).Update("inviter_id", 2).Error)
					case "employee":
						require.NoError(t, db.Model(&model.EmployeeProfile{}).Where("user_id = ?", 1).Update("status", 0).Error)
					case "cancel":
						cancel()
					}
				}))
				t.Cleanup(func() { db.Callback().Query().Remove("test:pending-scope-change") })
				var err error
				if reader == "count" {
					var count int
					count, err = CountEmployeePendingAlerts(ctx, db, 1)
					require.Zero(t, count)
				} else {
					var page EmployeeCustomerAlertPage
					page, err = ListPendingCustomerAlerts(ctx, db, PendingCustomerFilter{EmployeeID: 1})
					require.Empty(t, page.Items)
					require.Empty(t, page.NextCursor)
				}
				require.True(t, changed)
				switch change {
				case "owner":
					require.ErrorIs(t, err, model.ErrCustomerAuditScopeChanged)
				case "employee":
					require.ErrorIs(t, err, model.ErrCustomerAuditForbidden)
				case "cancel":
					require.ErrorIs(t, err, context.Canceled)
				}
			})
		}
	}
}

func TestPendingAlertIndexesUpgradeAndRestart(t *testing.T) {
	db := newWorkerTestDB(t)
	indexes := []string{"idx_sec_customer_pending", "idx_sec_target_pending", "idx_sec_alert_pending"}
	for _, name := range indexes {
		require.True(t, db.Migrator().HasIndex(&model.SecurityAlert{}, name))
		require.NoError(t, db.Migrator().DropIndex(&model.SecurityAlert{}, name))
	}
	recorder := &ddlRecorder{Interface: logger.Default.LogMode(logger.Silent)}
	db.Logger = recorder
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
	require.NotEmpty(t, recorder.statements)
	for _, name := range indexes {
		require.True(t, db.Migrator().HasIndex(&model.SecurityAlert{}, name))
	}
	recorder.statements = nil
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
	require.Empty(t, recorder.statements)
}

func TestHandledCustomerAlertsFilterBeforePagination(t *testing.T) {
	db, f := customerAccessFixture(t)
	for i := range 45 {
		require.NoError(t, db.Create(&model.SecurityAlert{ActorID: 3, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, Status: "open", Visibility: 10, LastSeen: f.End, ID: int64(100 + i)}).Error)
	}
	var want []int64
	for i := range 25 {
		a := model.SecurityAlert{ActorID: 3, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, Status: []string{"resolved", "false_positive"}[i%2], Visibility: 10, LastSeen: f.End - 100, ID: int64(25 - i)}
		require.NoError(t, db.Create(&a).Error)
		want = append(want, a.ID)
	}
	f.View = "handled"
	first, err := ListCustomerAlerts(t.Context(), db, f)
	require.NoError(t, err)
	require.Len(t, first.Items, 20)
	f.Cursor = first.NextCursor
	second, err := ListCustomerAlerts(t.Context(), db, f)
	require.NoError(t, err)
	require.Len(t, second.Items, 5)
	var got []int64
	for _, row := range append(first.Items, second.Items...) {
		got = append(got, row.ID)
	}
	require.Equal(t, want, got)
	f.View = ""
	_, err = ListCustomerAlerts(t.Context(), db, f)
	require.ErrorIs(t, err, model.ErrCustomerAuditParams)
	admin, err := ListAlerts(t.Context(), db, 10, AlertFilter{Start: f.Start, End: f.End, View: "handled", Limit: 50})
	require.NoError(t, err)
	require.Len(t, admin.Items, 25)
	summary, err := SummarizeAlerts(t.Context(), db, 10, AlertFilter{Start: f.Start, End: f.End, View: "handled"})
	require.NoError(t, err)
	require.Zero(t, summary.Counts["open"])
}

func TestPendingAlertQueryPlans(t *testing.T) {
	if os.Getenv("TEST_ADMIN_TOKEN_DSN") == "" {
		t.Skip("real isolated database required")
	}
	db, _ := customerAccessFixture(t)
	rows := make([]model.SecurityAlert, 20000)
	for i := range rows {
		rows[i] = model.SecurityAlert{ActorID: 3, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, Visibility: 10, Status: "resolved", LastSeen: 1}
		if i >= len(rows)-100 {
			rows[i].Status = []string{"open", "investigating"}[i%2]
		}
		if i%3 == 0 {
			account, role := 3, 1
			rows[i].ActorID, rows[i].ActorRole = 0, 0
			rows[i].TargetUserID, rows[i].TargetRole = &account, &role
			rows[i].Rule, rows[i].ResourceType = "login_failures", "account"
		}
	}
	require.NoError(t, db.CreateInBatches(rows, 200).Error)
	statement := "VACUUM ANALYZE audit_security_alerts"
	if db.Dialector.Name() == "mysql" {
		statement = "ANALYZE TABLE audit_security_alerts"
	}
	require.NoError(t, db.Exec(statement).Error)
	var queries []alertPlanQuery
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:pending-alert-plan", func(tx *gorm.DB) {
		query := tx.Statement.SQL.String()
		if tx.Statement.Table == "audit_security_alerts" && strings.Contains(query, "archived_at IS NULL") {
			queries = append(queries, alertPlanQuery{sql: query, vars: append([]any(nil), tx.Statement.Vars...)})
		}
	}))
	t.Cleanup(func() { db.Callback().Query().Remove("test:pending-alert-plan") })
	count, err := CountEmployeePendingAlerts(t.Context(), db, 1)
	require.NoError(t, err)
	require.Equal(t, 100, count)
	f := PendingCustomerFilter{EmployeeID: 1}
	for range 2 {
		page, err := ListPendingCustomerAlerts(t.Context(), db, f)
		require.NoError(t, err)
		require.Len(t, page.Items, 20)
		require.NotEmpty(t, page.NextCursor)
		f.Cursor = page.NextCursor
	}
	for _, role := range []int{10, 100} {
		count, err := CountPendingAlerts(t.Context(), db, role)
		require.NoError(t, err)
		require.Equal(t, 100, count)
	}
	require.GreaterOrEqual(t, len(queries), 14)
	for _, query := range queries {
		explain := "EXPLAIN (ANALYZE, BUFFERS) "
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
		t.Logf("%s pending plan: %s", db.Dialector.Name(), raw)
		require.NotContains(t, query.sql, "SELECT *")
		if db.Dialector.Name() == "mysql" {
			require.Contains(t, []string{"range", "ref"}, plan[0]["type"])
			require.Contains(t, []string{"idx_sec_customer_pending", "idx_sec_target_pending", "idx_sec_alert_pending"}, plan[0]["key"])
		} else {
			require.NotContains(t, string(raw), "Seq Scan")
			require.Contains(t, string(raw), "Index Cond:")
			require.Contains(t, string(raw), "status")
		}
	}
}
