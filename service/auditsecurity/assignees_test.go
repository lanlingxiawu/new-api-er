package auditsecurity

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSecurityAssigneesQueryPlan(t *testing.T) {
	db := newWorkerTestDB(t)
	require.True(t, db.Migrator().HasIndex(&model.User{}, "idx_users_security_assignees"))
	if os.Getenv("TEST_ADMIN_TOKEN_DSN") == "" {
		return // Real dialects below verify the optimizer with ordinary users present.
	}
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}))
	users := make([]model.User, 10050)
	for i := range users {
		users[i] = model.User{Username: fmt.Sprintf("assignee-plan-%05d", i), AffCode: fmt.Sprintf("assignee-plan-%05d", i), Role: 1, Status: 1}
		if i%200 == 0 {
			users[i].Role = 100
		}
	}
	require.NoError(t, db.CreateInBatches(users, 100).Error)
	statement := "ANALYZE users"
	if db.Dialector.Name() == "mysql" {
		statement = "ANALYZE TABLE users"
	}
	require.NoError(t, db.Exec(statement).Error)
	alert := model.SecurityAlert{Visibility: 10, Rule: "sensitive_operation", Status: "open"}
	require.NoError(t, db.Create(&alert).Error)
	var queries []alertPlanQuery
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:assignee-plan", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" && !tx.DryRun {
			queries = append(queries, alertPlanQuery{sql: tx.Statement.SQL.String(), vars: append([]any(nil), tx.Statement.Vars...)})
		}
	}))
	t.Cleanup(func() { db.Callback().Query().Remove("test:assignee-plan") })
	options, err := ListAlertAssignees(t.Context(), db, 100, alert.ID)
	require.NoError(t, err)
	require.Len(t, options, 51)
	require.NotEmpty(t, queries)
	for _, query := range queries {
		var plan []map[string]any
		require.NoError(t, db.Raw("EXPLAIN "+db.Dialector.Explain(query.sql, query.vars...)).Scan(&plan).Error)
		for _, row := range plan {
			for column, value := range row {
				if raw, ok := value.([]byte); ok {
					row[column] = string(raw)
				}
			}
		}
		raw, err := common.Marshal(plan)
		require.NoError(t, err)
		t.Logf("%s assignee plan: %s", db.Dialector.Name(), raw)
		require.Contains(t, string(raw), "idx_users_security_assignees")
		require.False(t, strings.Contains(string(raw), "Seq Scan on users"))
	}
}

func TestSecurityAssigneesBeyondFirstCandidateBatch(t *testing.T) {
	db := newWorkerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}))
	users := make([]model.User, 100)
	for i := range users {
		users[i] = model.User{Id: i + 1, Username: fmt.Sprintf("a-denied-%03d", i), AffCode: fmt.Sprintf("denied-%03d", i), Role: 10, Status: 1}
	}
	require.NoError(t, db.Create(&users).Error)
	// All eligible candidates must remain reachable, even beyond 100 results.
	roots := make([]model.User, 101)
	for i := range roots {
		roots[i] = model.User{Id: i + 101, Username: fmt.Sprintf("z-root-%03d", 100-i), AffCode: fmt.Sprintf("root-%03d", i), Role: 100, Status: 1}
	}
	require.NoError(t, db.Create(&roots).Error)
	alert := model.SecurityAlert{Visibility: 10, Rule: "sensitive_operation", Status: "open", Version: 1, LastSeen: time.Now().Unix(), Evidence: "[]"}
	require.NoError(t, db.Create(&alert).Error)
	options, err := ListAlertAssignees(t.Context(), db, 100, alert.ID)
	require.NoError(t, err)
	require.Len(t, options, len(roots))
	for i, option := range options {
		require.Equal(t, roots[100-i].Id, option.ID)
	}
}

func TestSecurityAssigneesRespectAlertVisibilityAndCurrentPermissions(t *testing.T) {
	db := newWorkerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.CasbinRule{}))
	users := []model.User{
		{Id: 201, Username: "admin-allowed", Role: 10, Status: 1},
		{Id: 202, Username: "admin-denied", Role: 10, Status: 1},
		{Id: 203, Username: "admin-disabled", Role: 10, Status: 2},
		{Id: 204, Username: "root", Role: 100, Status: 1},
		{Id: 205, Username: "ordinary", Role: 1, Status: 1},
	}
	require.NoError(t, db.Create(&users).Error)
	for _, permission := range []authz.Permission{authz.AuditRead, authz.AuditSecurityRead, authz.AuditSecurityManage} {
		require.NoError(t, db.Create(&model.CasbinRule{Ptype: "p", V0: authz.RoleSubject(authz.BuiltInRoleAdmin), V1: permission.Resource, V2: permission.Action, V3: authz.EffectAllow}).Error)
	}
	deny := model.CasbinRule{Ptype: "p", V0: authz.UserSubject(202), V1: authz.AuditSecurityManage.Resource, V2: authz.AuditSecurityManage.Action, V3: authz.EffectDeny}
	require.NoError(t, db.Create(&deny).Error)
	alert := model.SecurityAlert{Visibility: 10, Rule: "sensitive_operation", Status: "open", Version: 1, LastSeen: time.Now().Unix(), Evidence: "[]"}
	rootAlert := model.SecurityAlert{Visibility: 100, Rule: "session_reuse", Status: "open", Version: 1, LastSeen: alert.LastSeen, Evidence: "[]"}
	require.NoError(t, db.Create(&alert).Error)
	require.NoError(t, db.Create(&rootAlert).Error)
	options, err := ListAlertAssignees(t.Context(), db, common.RoleRootUser, alert.ID)
	require.NoError(t, err)
	ids := make([]int, 0, len(options))
	for _, option := range options {
		ids = append(ids, option.ID)
	}
	require.ElementsMatch(t, []int{201, 204}, ids)
	options, err = ListAlertAssignees(t.Context(), db, common.RoleRootUser, rootAlert.ID)
	require.NoError(t, err)
	require.Len(t, options, 1)
	require.Equal(t, 204, options[0].ID)
	_, err = ListAlertAssignees(t.Context(), db, common.RoleAdminUser, rootAlert.ID)
	require.Error(t, err)
	for _, id := range []int{202, 203, 205, 999} {
		_, err = UpdateAlertInvestigation(t.Context(), db, 100, 204, AlertInvestigation{Version: 1, AssigneeID: &id}, alert.ID)
		require.ErrorIs(t, err, ErrInvalidAlertAssignee)
	}
	id := 201
	_, err = UpdateAlertInvestigation(t.Context(), db, 100, 204, AlertInvestigation{Version: 1, AssigneeID: &id}, rootAlert.ID)
	require.ErrorIs(t, err, ErrInvalidAlertAssignee)
	// Revoke after listing: saving must query the current policy, not trust cached choices.
	deny.Id, deny.V0 = 0, authz.UserSubject(201)
	require.NoError(t, db.Create(&deny).Error)
	_, err = UpdateAlertInvestigation(t.Context(), db, 100, 204, AlertInvestigation{Version: 1, AssigneeID: &id}, alert.ID)
	require.ErrorIs(t, err, ErrInvalidAlertAssignee)
	id = 204
	saved, err := UpdateAlertInvestigation(t.Context(), db, 100, 204, AlertInvestigation{Version: 1, AssigneeID: &id}, rootAlert.ID)
	require.NoError(t, err)
	require.Equal(t, id, saved.AssigneeID)
	id = 0
	saved, err = UpdateAlertInvestigation(t.Context(), db, 100, 204, AlertInvestigation{Version: saved.Version, AssigneeID: &id}, rootAlert.ID)
	require.NoError(t, err)
	require.Zero(t, saved.AssigneeID)
}
