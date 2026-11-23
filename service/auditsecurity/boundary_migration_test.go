package auditsecurity

import (
	"context"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestBoundaryMigrationUpgradesPreviousSchemaAndRestartsWithoutDDL(t *testing.T) {
	db := newWorkerTestDB(t)
	// Remove only this feature's additions in an isolated test-owned database,
	// then migrate populated pre-boundary rows through the actual entry point.
	require.NoError(t, db.Migrator().DropTable(&model.SecurityResponseLink{}, &model.SecurityResponseGroup{}))
	require.NoError(t, db.Migrator().DropIndex(&model.SecurityAction{}, "idx_sec_action_group"))
	require.NoError(t, db.Migrator().DropColumn(&model.SecurityAction{}, "response_group_id"))
	require.NoError(t, db.Migrator().DropColumn(&model.SecurityEvent{}, "response_group_id"))
	require.NoError(t, db.Migrator().DropIndex(&model.SecurityFindingSnapshot{}, "idx_sec_finding_detection"))
	require.NoError(t, db.Migrator().DropColumn(&model.SecurityFindingSnapshot{}, "detection_key"))
	require.NoError(t, db.Omit("response_group_id").Create(&model.SecurityAction{AlertID: 1, Outcome: "sync_pending", Action: "clear_api_keys", Result: `{"pending_ids":[1],"started_at":1}`}).Error)
	require.NoError(t, db.Omit("response_group_id").Create(&model.SecurityEvent{EventID: "legacy-retained", At: 1}).Error)
	require.NoError(t, model.MigrateAuditSecurity(context.Background(), db))
	var action model.SecurityAction
	require.NoError(t, db.First(&action).Error)
	require.Nil(t, action.ResponseGroupID)
	require.Equal(t, "sync_pending", action.Outcome)
	require.Equal(t, `{"pending_ids":[1],"started_at":1}`, action.Result)
	var event model.SecurityEvent
	require.NoError(t, db.First(&event).Error)
	require.Nil(t, event.ResponseGroupID)
	for range 2 {
		ddl := &migrationDDLLog{Interface: logger.Default.LogMode(logger.Silent)}
		require.NoError(t, model.MigrateAuditSecurity(context.Background(), db.Session(&gorm.Session{Logger: ddl})))
		require.Empty(t, ddl.statements)
	}
}

func TestBoundaryResponseQueryPlans(t *testing.T) {
	db := newWorkerTestDB(t)
	var groups []model.SecurityResponseGroup
	var links []model.SecurityResponseLink
	var actions []model.SecurityAction
	for i := 1; i <= 2000; i++ {
		id := int64(i)
		key := fmt.Sprint("group-plan-", i)
		group := model.SecurityResponseGroup{ID: id, TargetUserID: i, AuthVersion: 1, PolicyAt: 1, TriggerKey: key, ActiveKey: &key, CreatedAt: 1, State: "active"}
		if i == 3 {
			group.ActiveKey = nil
			group.State = "complete"
		}
		groups = append(groups, group)
		if i != 3 {
			links = append(links, model.SecurityResponseLink{AlertID: id, GroupID: id})
		}
		actions = append(actions, model.SecurityAction{AlertID: id, ResponseGroupID: &id, TargetUserID: i, Action: "clear_api_keys", Outcome: "succeeded"})
	}
	require.NoError(t, db.CreateInBatches(groups, 100).Error)
	require.NoError(t, db.CreateInBatches(links, 100).Error)
	require.NoError(t, db.CreateInBatches(actions, 100).Error)
	if db.Dialector.Name() == "postgres" {
		for _, table := range []string{"audit_security_response_groups", "audit_security_response_links", "audit_security_actions"} {
			require.NoError(t, db.Exec("ANALYZE "+table).Error)
		}
	}
	linked := db.Model(&model.SecurityResponseLink{}).Select("group_id").Where("group_id = audit_security_response_groups.id")
	for _, test := range []struct {
		name  string
		query *gorm.DB
	}{
		{"active group", db.Model(&model.SecurityResponseGroup{}).Where("active_key = ?", "group-plan-4").Limit(1)},
		{"previous group", db.Model(&model.SecurityResponseGroup{}).Select("id,last_trigger_at").Where("target_user_id = ? AND auth_version = ? AND policy_at = ?", 4, 1, 1).Order("created_at DESC,id DESC").Limit(1)},
		{"group links", db.Model(&model.SecurityResponseLink{}).Select("alert_id").Where("group_id = ?", 4).Order("alert_id")},
		{"shared receipts", db.Model(&model.SecurityAction{}).Select(model.SecurityResponseColumns).Where("response_group_id IN ?", []int{4, 5})},
		{"orphan cleanup", db.Model(&model.SecurityResponseGroup{}).Select("id").Where("state = ? AND created_at < ? AND NOT EXISTS (?)", "complete", 100, linked).Order("created_at,id").Limit(100)},
	} {
		statement := test.query.Session(&gorm.Session{DryRun: true}).Find(&[]map[string]any{}).Statement
		prefix := "EXPLAIN "
		if db.Dialector.Name() == "sqlite" {
			prefix = "EXPLAIN QUERY PLAN "
		}
		var plan []map[string]any
		require.NoError(t, db.Raw(prefix+statement.SQL.String(), statement.Vars...).Scan(&plan).Error)
		require.NotEmpty(t, plan)
		for _, row := range plan {
			for key, value := range row {
				if b, ok := value.([]byte); ok {
					row[key] = string(b)
				}
			}
		}
		t.Logf("%s %s: %v", db.Dialector.Name(), test.name, plan)
	}
}
