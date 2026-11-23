package auditsecurity

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPendingIPQueryUsesDueIndex(t *testing.T) {
	if os.Getenv("TEST_ADMIN_TOKEN_DSN") == "" {
		t.Skip("real isolated database required")
	}
	db := newWorkerTestDB(t)
	now := time.Now().Unix()
	events := make([]model.SecurityEvent, 10050)
	for i := range events {
		events[i] = model.SecurityEvent{EventID: fmt.Sprintf("ip-plan-%05d", i), At: now, Action: "login", Success: true, IP: "8.8.8.8", IPCheckState: "done"}
		if i < 10 {
			events[i].IPCheckState, events[i].IPNextAttemptAt = "pending", now-1
		}
	}
	require.NoError(t, db.CreateInBatches(events, 250).Error)
	if db.Dialector.Name() == "postgres" {
		require.NoError(t, db.Exec("ANALYZE audit_security_events").Error)
	} else {
		require.NoError(t, db.Exec("ANALYZE TABLE audit_security_events").Error)
	}
	var query alertPlanQuery
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:pending-ip-plan", func(tx *gorm.DB) {
		if query.sql == "" && tx.Statement.Table == "audit_security_events" {
			query = alertPlanQuery{sql: tx.Statement.SQL.String(), vars: append([]any(nil), tx.Statement.Vars...)}
		}
	}))
	t.Cleanup(func() { db.Callback().Query().Remove("test:pending-ip-plan") })
	lease, err := model.ClaimAuditSecurity(t.Context(), db, "ip-plan")
	require.NoError(t, err)
	w := worker{db: db}
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.IPIntelligenceEnabled = true
	remaining := 0
	require.NoError(t, w.retryIPIntelligence(t.Context(), lease, cfg, &remaining))
	require.NotEmpty(t, query.sql)
	var plan []map[string]any
	explain := "EXPLAIN "
	if db.Dialector.Name() == "mysql" {
		explain = "EXPLAIN FORMAT=TRADITIONAL "
	}
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
	t.Logf("%s pending IP query: %s -- PLAN: %s", db.Dialector.Name(), db.Dialector.Explain(query.sql, query.vars...), raw)
	if db.Dialector.Name() == "mysql" {
		require.NotEmpty(t, plan)
		require.Equal(t, "idx_sec_ip_pending", plan[0]["key"])
	} else {
		require.True(t, strings.Contains(string(raw), "Index Scan using idx_sec_ip_pending"))
	}
}

type alertPlanQuery struct {
	sql  string
	vars []any
}

func TestSourceBaselineEvidenceQueryPlans(t *testing.T) {
	if os.Getenv("TEST_ADMIN_TOKEN_DSN") == "" {
		t.Skip("real isolated database required")
	}
	db := newWorkerTestDB(t)
	now := time.Now().Unix()
	baselines := make([]model.SecurityBaseline, 10050)
	for i := range baselines {
		kind := ""
		if i%2 == 1 {
			kind = "management"
		}
		baselines[i] = model.SecurityBaseline{AccountID: 31 + i/2, Kind: kind, SourceRef: fmt.Sprintf("baseline-plan-%05d", i), FirstSeen: now - 2*86400, LastSeen: now - 100}
	}
	require.NoError(t, db.CreateInBatches(baselines, 250).Error)
	statement := "ANALYZE audit_security_baselines"
	if db.Dialector.Name() == "mysql" {
		statement = "ANALYZE TABLE audit_security_baselines"
	}
	require.NoError(t, db.Exec(statement).Error)
	var captured []alertPlanQuery
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:source-baseline-plan", func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_security_baselines" && strings.Contains(tx.Statement.SQL.String(), "first_seen <=") {
			captured = append(captured, alertPlanQuery{sql: tx.Statement.SQL.String(), vars: append([]any(nil), tx.Statement.Vars...)})
		}
	}))
	t.Cleanup(func() { db.Callback().Query().Remove("test:source-baseline-plan") })
	state, err := model.ClaimAuditSecurity(t.Context(), db, "source-baseline-plan")
	require.NoError(t, err)
	for _, action := range []string{"login", "token.create"} {
		event := Normalize(model.AuditLog{EventId: "baseline-plan-" + action, UserId: 31, ActorRole: 1, Action: action, Success: true, Ip: "8.8.4.4", CreatedAt: now, Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"id": 7}}}})
		require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{event}, state, operation_setting.DefaultAuditSecuritySetting()))
	}
	require.Len(t, captured, 2, "exercise both login and management source queries")
	for _, query := range captured {
		var plan []map[string]any
		explain := "EXPLAIN (ANALYZE, BUFFERS) "
		if db.Dialector.Name() == "mysql" {
			explain = "EXPLAIN FORMAT=TRADITIONAL "
		}
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
		t.Logf("%s source baseline query: %s -- PLAN: %s", db.Dialector.Name(), db.Dialector.Explain(query.sql, query.vars...), raw)
		if db.Dialector.Name() == "mysql" {
			require.NotEmpty(t, plan)
			require.Contains(t, []string{"idx_sec_baseline", "idx_sec_baseline_maturity"}, plan[0]["key"])
		} else {
			require.NotContains(t, string(raw), "Seq Scan")
			require.Contains(t, string(raw), "idx_sec_baseline")
			require.Contains(t, string(raw), "account_id = 31")
		}
	}
}

func TestCustomerPaginationQueryPlans(t *testing.T) {
	if os.Getenv("TEST_ADMIN_TOKEN_DSN") == "" {
		t.Skip("real isolated database required")
	}
	db, filter := customerAccessFixture(t)
	logs := make([]model.AuditLog, 10050)
	alerts := make([]model.SecurityAlert, len(logs))
	for i := range logs {
		actorID := 100 + i%200
		if i%200 == 0 {
			actorID = filter.CustomerID
		}
		at := filter.End - int64(i)
		logs[i] = model.AuditLog{EventId: fmt.Sprintf("customer-plan-%05d", i), UserId: actorID, ActorRole: 1, Action: "login", CreatedAt: at}
		alerts[i] = model.SecurityAlert{ActorID: actorID, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, LastSeen: at}
		if i%400 == 0 {
			logs[i].Other.AdminInfo = &model.AuditAdminInfo{AdminID: 2}
			alerts[i].RuleVersion = 1
		}
	}
	require.NoError(t, db.CreateInBatches(logs, 250).Error)
	require.NoError(t, db.CreateInBatches(alerts, 250).Error)
	for _, table := range []string{"audit_logs", "audit_security_alerts"} {
		statement := "ANALYZE " + table
		if db.Dialector.Name() == "mysql" {
			statement = "ANALYZE TABLE " + table
		}
		require.NoError(t, db.Exec(statement).Error)
	}
	captured := map[string][]alertPlanQuery{}
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:customer-pagination-plan", func(tx *gorm.DB) {
		table := tx.Statement.Table
		if table == "audit_logs" || table == "audit_security_alerts" {
			captured[table] = append(captured[table], alertPlanQuery{sql: tx.Statement.SQL.String(), vars: append([]any(nil), tx.Statement.Vars...)})
		}
	}))
	t.Cleanup(func() { db.Callback().Query().Remove("test:customer-pagination-plan") })
	auditPage, err := model.QueryCustomerAuditLogs(t.Context(), db, db, filter)
	require.NoError(t, err)
	require.Len(t, auditPage.Items, 20)
	require.NotEmpty(t, auditPage.NextCursor)
	alertPage, err := ListCustomerAlerts(t.Context(), db, filter)
	require.NoError(t, err)
	require.Len(t, alertPage.Items, 20)
	require.NotEmpty(t, alertPage.NextCursor)
	for table, index := range map[string]string{"audit_logs": "idx_audit_customer_cursor", "audit_security_alerts": "idx_sec_customer_time"} {
		require.Greater(t, len(captured[table]), 1, "the test must exercise continuation queries through hidden rows")
		for _, query := range captured[table] {
			var plan []map[string]any
			explain := "EXPLAIN (ANALYZE, BUFFERS) "
			if db.Dialector.Name() == "mysql" {
				explain = "EXPLAIN FORMAT=TRADITIONAL "
			}
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
			t.Logf("%s %s pagination query: %s -- PLAN: %s", db.Dialector.Name(), table, db.Dialector.Explain(query.sql, query.vars...), raw)
			if table == "audit_security_alerts" && assertAlertPagePrimaryLookup(t, db, query, plan, string(raw)) {
				continue
			}
			expectedIndex := index
			if strings.Contains(query.sql, "target_user_id IN") {
				expectedIndex = "idx_sec_target_time"
			}
			if db.Dialector.Name() == "mysql" {
				require.NotEmpty(t, plan)
				require.Equal(t, expectedIndex, plan[0]["key"])
			} else {
				// PostgreSQL may choose another existing index or a bitmap plan
				// for a small continuation range. Require indexed account/time
				// bounds rather than one particular index name.
				require.NotContains(t, string(raw), "Seq Scan")
				accountColumn, timeColumn := "actor_id", "last_seen"
				if expectedIndex == "idx_sec_target_time" {
					accountColumn = "target_user_id"
				}
				if table == "audit_logs" {
					accountColumn, timeColumn = "user_id", "created_at"
				}
				indexed := false
				for _, row := range plan {
					line, _ := row["QUERY PLAN"].(string)
					if strings.Contains(line, "Index Cond:") && strings.Contains(line, accountColumn+" = ") && strings.Contains(line, timeColumn) {
						indexed = true
					}
				}
				require.True(t, indexed, "customer pagination must retain an index scoped by account and time")
			}
		}
	}
}

func TestEmployeeAuditAggregateQueryPlans(t *testing.T) {
	if os.Getenv("TEST_ADMIN_TOKEN_DSN") == "" {
		t.Skip("real isolated database required")
	}
	db, single := customerAccessFixture(t)
	users := make([]model.User, 20000)
	logs := make([]model.AuditLog, 40000)
	alerts := make([]model.SecurityAlert, len(logs))
	for i := range users {
		inviter := 2
		if i%50 == 0 {
			inviter = single.EmployeeID
		}
		users[i] = model.User{Id: 100 + i, Username: fmt.Sprintf("aggregate-plan-%05d", i), AffCode: fmt.Sprintf("plan-%05d", i), Role: 1, Status: 1, InviterId: inviter}
	}
	for i := range logs {
		actorID, at := 100+i%len(users), single.End-int64(i)
		logs[i] = model.AuditLog{EventId: fmt.Sprintf("aggregate-plan-%05d", i), UserId: actorID, ActorRole: 1, Action: "login", CreatedAt: at}
		alerts[i] = model.SecurityAlert{ActorID: actorID, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, LastSeen: at}
	}
	require.NoError(t, db.CreateInBatches(users, 250).Error)
	require.NoError(t, db.CreateInBatches(logs, 250).Error)
	require.NoError(t, db.CreateInBatches(alerts, 250).Error)
	for _, table := range []string{"users", "employee_profiles", "audit_logs", "audit_security_alerts"} {
		statement := "VACUUM ANALYZE " + table
		if db.Dialector.Name() == "mysql" {
			statement = "ANALYZE TABLE " + table
		}
		require.NoError(t, db.Exec(statement).Error)
	}
	captured := map[string][]alertPlanQuery{}
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:employee-audit-plan", func(tx *gorm.DB) {
		table, query := tx.Statement.Table, tx.Statement.SQL.String()
		if table == "audit_logs" || table == "audit_security_alerts" || table == "users" && strings.Contains(query, "ORDER BY id ASC") {
			captured[table] = append(captured[table], alertPlanQuery{sql: query, vars: append([]any(nil), tx.Statement.Vars...)})
		}
	}))
	t.Cleanup(func() { db.Callback().Query().Remove("test:employee-audit-plan") })
	auditFilter := model.EmployeeAuditFilter{EmployeeID: single.EmployeeID, Start: single.Start, End: single.End}
	alertFilter := auditFilter
	for range 2 {
		auditPage, err := model.QueryEmployeeAuditLogs(t.Context(), db, db, auditFilter)
		require.NoError(t, err)
		require.Len(t, auditPage.Items, 20)
		require.NotEmpty(t, auditPage.NextCursor)
		auditFilter.Cursor = auditPage.NextCursor
		alertPage, err := ListEmployeeCustomerAlerts(t.Context(), db, alertFilter)
		require.NoError(t, err)
		require.Len(t, alertPage.Items, 20)
		require.NotEmpty(t, alertPage.NextCursor)
		alertFilter.Cursor = alertPage.NextCursor
	}
	for table, index := range map[string]string{
		"users": "idx_users_customer_audit", "audit_logs": "idx_audit_customer_cursor", "audit_security_alerts": "idx_sec_customer_time",
	} {
		require.Greater(t, len(captured[table]), 2, "exercise multiple ownership batches and page continuation")
		for _, query := range captured[table] {
			var plan []map[string]any
			explain := "EXPLAIN (ANALYZE, BUFFERS) "
			if db.Dialector.Name() == "mysql" {
				explain = "EXPLAIN FORMAT=TRADITIONAL "
			}
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
			t.Logf("%s aggregate %s SQL: %s -- PLAN: %s", db.Dialector.Name(), table, db.Dialector.Explain(query.sql, query.vars...), raw)
			if table == "audit_security_alerts" && assertAlertPagePrimaryLookup(t, db, query, plan, string(raw)) {
				continue
			}
			expectedIndex := index
			if strings.Contains(query.sql, "target_user_id IN") {
				expectedIndex = "idx_sec_target_time"
			}
			if db.Dialector.Name() == "mysql" {
				indexed := false
				for _, row := range plan {
					key, _ := row["key"].(string)
					// The pending index shares the account prefix. MySQL may
					// prefer it for a sparse ownership batch; bound its work as
					// well as checking the access type, rather than its name alone.
					pendingIndex := "idx_sec_customer_pending"
					if expectedIndex == "idx_sec_target_time" {
						pendingIndex = "idx_sec_target_pending"
					}
					pendingLookup := table == "audit_security_alerts" && key == pendingIndex
					if pendingLookup {
						var estimatedRows int64
						_, err := fmt.Sscan(fmt.Sprint(row["rows"]), &estimatedRows)
						require.NoError(t, err)
						require.LessOrEqual(t, estimatedRows, int64(1000), "a sparse ownership batch must not scan the alert history")
					}
					if key == expectedIndex || pendingLookup || table == "audit_logs" && slices.Contains([]string{"idx_audit_user_time", "idx_audit_user_route", "idx_audit_logs_event_id"}, key) {
						require.Contains(t, []string{"range", "ref", "const"}, row["type"], "use a bounded account/event lookup, not a full index scan")
						indexed = true
					}
				}
				require.True(t, indexed, "aggregate queries must use the scoped index")
			} else {
				require.NotContains(t, string(raw), "Seq Scan on "+table)
				// PostgreSQL can prefer a time-index scan with early LIMIT to
				// sorting all customer matches. Require indexed scope bounds,
				// not one optimizer-dependent index choice.
				columns := map[string][]string{
					"users": {"inviter_id"}, "audit_logs": {"user_id", "event_id", "created_at"}, "audit_security_alerts": {"actor_id", "last_seen"},
				}[table]
				if expectedIndex == "idx_sec_target_time" {
					columns = []string{"target_user_id", "last_seen"}
				}
				indexed := false
				for _, row := range plan {
					line, _ := row["QUERY PLAN"].(string)
					if strings.Contains(line, "Index Cond:") {
						for _, column := range columns {
							indexed = indexed || strings.Contains(line, column)
						}
					}
				}
				require.True(t, indexed, "aggregate reads need an indexed customer, time, or known-event bound")
			}
		}
	}
}

// The second step of an alert page loads only IDs selected by the scoped,
// time-bounded index scan. Verify the size and PK plan instead of applying the
// candidate-scan assertion to this distinct lookup.
func assertAlertPagePrimaryLookup(t *testing.T, db *gorm.DB, query alertPlanQuery, plan []map[string]any, raw string) bool {
	t.Helper()
	if !strings.Contains(query.sql, "WHERE id IN (") {
		return false
	}
	require.NotEmpty(t, query.vars)
	require.LessOrEqual(t, len(query.vars), model.CustomerAuditPageSize+1)
	require.NotContains(t, query.sql, "SELECT *")
	if db.Dialector.Name() == "mysql" {
		require.NotEmpty(t, plan)
		require.Equal(t, "PRIMARY", plan[0]["key"])
		require.Contains(t, []string{"range", "const"}, plan[0]["type"])
	} else {
		require.NotContains(t, raw, "Seq Scan")
		require.Contains(t, raw, "audit_security_alerts_pkey")
		require.Contains(t, raw, "Index Cond:")
	}
	return true
}

func TestAlertQueryPlans(t *testing.T) {
	if os.Getenv("TEST_ADMIN_TOKEN_DSN") == "" {
		t.Skip("real isolated database required")
	}
	db := newWorkerTestDB(t)
	previousDatabaseType := common.MainDatabaseType()
	if db.Dialector.Name() == "postgres" {
		common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	} else {
		common.SetMainDatabaseType(common.DatabaseTypeMySQL)
	}
	t.Cleanup(func() { common.SetMainDatabaseType(previousDatabaseType) })
	now := time.Now().Unix()
	rules := []string{"sensitive_operation", "bulk_sensitive_operations", "bulk_key_access", "login_after_failures", "password_spray", "session_reuse", "anonymous_network"}
	alerts := make([]model.SecurityAlert, 10050)
	for i := range alerts {
		alerts[i] = model.SecurityAlert{
			Rule: rules[i%len(rules)], SubjectRef: "actor:plan", Visibility: 10, ActorID: 7,
			ResourceType: "channel", Severity: 2, Status: "open", FirstSeen: now - int64(i%100),
			LastSeen: now - int64(i%120*86400), UpdatedAt: now - int64(i%120*86400), RuleVersion: i%2 + 1,
		}
		if i%10 != 0 {
			alerts[i].Visibility = 100
		}
		if i%5 == 0 {
			alerts[i].Status = "resolved"
		}
	}
	require.NoError(t, db.CreateInBatches(alerts, 250).Error)
	require.NoError(t, db.Exec("UPDATE audit_security_alerts SET purge_state = NULL WHERE id % 19 = 0").Error)
	require.NoError(t, db.Exec("UPDATE audit_security_alerts SET archived_at = ? WHERE id % 23 = 0", now).Error)
	require.NoError(t, db.Exec("UPDATE audit_security_alerts SET closed_at = ? WHERE id % 29 = 0", now-100).Error)
	if db.Dialector.Name() == "postgres" {
		require.NoError(t, db.Exec("ANALYZE audit_security_alerts").Error)
	} else if db.Dialector.Name() == "mysql" {
		require.NoError(t, db.Exec("ANALYZE TABLE audit_security_alerts").Error)
	}

	captured := map[string]alertPlanQuery{}
	currentLabel := ""
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:alert-plan-capture", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*[]model.SecurityAlert); !ok || currentLabel == "" {
			return
		}
		if _, exists := captured[currentLabel]; exists {
			return
		}
		captured[currentLabel] = alertPlanQuery{sql: tx.Statement.SQL.String(), vars: append([]any(nil), tx.Statement.Vars...)}
	}))
	t.Cleanup(func() { db.Callback().Query().Remove("test:alert-plan-capture") })
	explain := func(label string) {
		query, ok := captured[label]
		require.True(t, ok, "missing captured SQL for "+label)
		var plan []map[string]any
		require.NoError(t, db.Raw("EXPLAIN "+db.Dialector.Explain(query.sql, query.vars...)).Scan(&plan).Error)
		encoded, err := common.Marshal(plan)
		require.NoError(t, err)
		t.Logf("%s %s SQL: %s -- PLAN: %s", db.Dialector.Name(), label, db.Dialector.Explain(query.sql, query.vars...), encoded)
	}
	run := func(label string, role int, filter AlertFilter) {
		t.Run(label, func(t *testing.T) {
			currentLabel = label
			_, err := ListAlerts(t.Context(), db, role, filter)
			require.NoError(t, err)
			explain(label)
			currentLabel = ""
		})
	}
	for _, tc := range []struct {
		label string
		role  int
		view  string
	}{
		{"recent_hits_root", 100, "recent_hits"},
		{"recent_hits_admin", 10, "recent_hits"},
		{"history_scope_legacy", 100, "id"},
		{"activity_root", 100, "activity"},
		{"activity_admin", 10, "activity"},
	} {
		filter := AlertFilter{View: tc.view, Start: now - 86400, End: now, Limit: 50}
		if tc.view == "id" {
			filter.HistoryScope = "legacy"
		}
		run(tc.label, tc.role, filter)
	}
	t.Run("cleanup_candidates", func(t *testing.T) {
		currentLabel = "cleanup_candidates"
		_, err := model.CleanupAuditSecurityAlerts(t.Context(), db, now-100)
		require.NoError(t, err)
		explain("cleanup_candidates")
		currentLabel = ""
	})
}
