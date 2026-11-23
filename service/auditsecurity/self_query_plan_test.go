package auditsecurity

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSelfSecurityQueryPlans(t *testing.T) {
	if os.Getenv("TEST_ADMIN_TOKEN_DSN") == "" {
		t.Skip("real isolated database required")
	}
	db := newWorkerTestDB(t)
	now := time.Now().Unix()
	alerts := make([]model.SecurityAlert, 10050)
	for i := range alerts {
		actorID := 100 + i%200
		if i%200 == 0 {
			actorID = 31
		}
		alerts[i] = model.SecurityAlert{ActorID: actorID, ActorRole: 1, Rule: "session_reuse", RuleVersion: 2, LastSeen: now - int64(i)}
		if i%3 == 0 {
			role := 1
			alerts[i].ActorID, alerts[i].ActorRole = 0, 0
			alerts[i].Rule, alerts[i].ResourceType = "login_failures", "account"
			alerts[i].TargetUserID, alerts[i].TargetRole = &actorID, &role
		}
		if i%400 == 0 {
			alerts[i].RuleVersion = 1 // incomplete old identities require continued paging
		}
	}
	require.NoError(t, db.CreateInBatches(alerts, 250).Error)
	actions := make([]model.SecurityAction, len(alerts))
	for i := range actions {
		actions[i] = model.SecurityAction{AlertID: alerts[i].ID, TargetUserID: alerts[i].ActorID, Action: "clear_api_keys", Outcome: "succeeded", CreatedAt: now - int64(i)}
	}
	require.NoError(t, db.CreateInBatches(actions, 250).Error)
	for _, table := range []string{"audit_security_alerts", "audit_security_actions"} {
		statement := "ANALYZE " + table
		if db.Dialector.Name() == "mysql" {
			statement = "ANALYZE TABLE " + table
		}
		require.NoError(t, db.Exec(statement).Error)
	}
	captured := map[string][]alertPlanQuery{}
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:self-security-plan", func(tx *gorm.DB) {
		if tx.DryRun {
			return // GORM also invokes callbacks while building nested SQL.
		}
		table := tx.Statement.Table
		if table == "audit_security_alerts" || table == "audit_security_actions" {
			captured[table] = append(captured[table], alertPlanQuery{sql: tx.Statement.SQL.String(), vars: append([]any(nil), tx.Statement.Vars...)})
		}
	}))
	t.Cleanup(func() { db.Callback().Query().Remove("test:self-security-plan") })
	filter := SelfSecurityFilter{UserID: 31, Start: now - 86400, End: now}
	page, err := ListSelfSecurityAlerts(t.Context(), db, filter)
	require.NoError(t, err)
	require.Len(t, page.Items, 20)
	require.NotEmpty(t, page.NextCursor)
	filter.Cursor = page.NextCursor
	next, err := ListSelfSecurityAlerts(t.Context(), db, filter)
	require.NoError(t, err)
	require.Len(t, next.Items, 5)
	require.Empty(t, next.NextCursor)
	alertQueries := captured["audit_security_alerts"]
	require.Greater(t, len(alertQueries), 2)
	for _, query := range alertQueries {
		assertSelfSecurityQueryPlan(t, db, query, "actor_id", "last_seen")
	}
	detail, err := GetSelfSecurityAlert(t.Context(), db, 31, page.Items[0].ID)
	require.NoError(t, err)
	require.Len(t, detail.Responses, 1)
	require.Len(t, captured["audit_security_actions"], 1)
	assertSelfSecurityQueryPlan(t, db, captured["audit_security_actions"][0], "alert_id", "")
}

func assertSelfSecurityQueryPlan(t *testing.T, db *gorm.DB, query alertPlanQuery, accountColumn, timeColumn string) {
	t.Helper()
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
	t.Logf("%s self security query: %s -- PLAN: %s", db.Dialector.Name(), db.Dialector.Explain(query.sql, query.vars...), raw)
	if accountColumn == "actor_id" && assertAlertPagePrimaryLookup(t, db, query, plan, string(raw)) {
		return
	}
	if strings.Contains(query.sql, "target_user_id IN") {
		accountColumn = "target_user_id"
	}
	if db.Dialector.Name() == "mysql" {
		require.NotEmpty(t, plan)
		if accountColumn == "actor_id" {
			require.Equal(t, "idx_sec_customer_time", plan[0]["key"])
		} else if accountColumn == "target_user_id" {
			require.Equal(t, "idx_sec_target_time", plan[0]["key"])
		} else {
			alertIndex := false
			for _, row := range plan {
				if row["table"] != "audit_security_actions" {
					continue
				}
				require.NotEmpty(t, row["key"], "action lookup must not scan the entire action table")
				key, _ := row["key"].(string)
				alertIndex = alertIndex || key == "idx_sec_action_cursor" || key == "idx_sec_action_alert" || key == "idx_sec_response_alert"
			}
			require.True(t, alertIndex, "the latest-action subquery must use an alert-scoped index")
		}
		return
	}
	require.NotContains(t, string(raw), "Seq Scan")
	indexed := false
	for _, row := range plan {
		line, _ := row["QUERY PLAN"].(string)
		if strings.Contains(line, "Index Cond:") && strings.Contains(line, accountColumn+" = ") && strings.Contains(line, timeColumn) {
			indexed = true
		}
	}
	if !indexed && timeColumn != "" {
		// A sparse account can use the pending index's account prefix, applying
		// time bounds as a filter. Verify its actual work, not just an index name.
		accountIndexed, timeFiltered := false, false
		scanned := float64(0)
		actualRows := regexp.MustCompile(`actual time=\S+ rows=([0-9.]+) loops=([0-9]+)`)
		removedRows := regexp.MustCompile(`Rows Removed by Filter: ([0-9]+)`)
		for _, row := range plan {
			line, _ := row["QUERY PLAN"].(string)
			accountIndexed = accountIndexed || strings.Contains(line, "Index Cond:") && strings.Contains(line, accountColumn+" = ")
			timeFiltered = timeFiltered || strings.Contains(line, "Filter:") && strings.Contains(line, timeColumn+" >= ") && strings.Contains(line, timeColumn+" <= ")
			if strings.Contains(line, "Scan") {
				if match := actualRows.FindStringSubmatch(line); match != nil {
					rows, err := strconv.ParseFloat(match[1], 64)
					require.NoError(t, err)
					loops, err := strconv.ParseFloat(match[2], 64)
					require.NoError(t, err)
					scanned += rows * loops
				}
			}
			if match := removedRows.FindStringSubmatch(line); match != nil {
				removed, err := strconv.ParseFloat(match[1], 64)
				require.NoError(t, err)
				scanned += removed
			}
		}
		indexed = accountIndexed && timeFiltered && scanned > 0 && scanned <= 100
	}
	require.True(t, indexed, "personal queries must use an account/time or alert scoped index")
}
