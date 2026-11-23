package auditsecurity

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestRepeatOperationMigrationFreshUpgradeAndRestart(t *testing.T) {
	db := newWorkerTestDB(t)
	for _, upgrade := range []bool{false, true} {
		if upgrade {
			// Reconstruct the previous schema in this test's owned database only.
			require.NoError(t, db.Migrator().DropIndex(&model.SecurityAlert{}, "idx_sec_repeat_episode"))
			for _, column := range []string{"repeat_key", "repeat_until", "confirmed_occurrences"} {
				require.NoError(t, db.Migrator().DropColumn(&model.SecurityAlert{}, column))
			}
			require.NoError(t, db.Omit("repeat_key", "repeat_until", "confirmed_occurrences").Create(&model.SecurityAlert{ID: 45, SubjectRef: "actor:legacy", Version: 5, Occurrences: 3, Evidence: "[]", Status: "resolved"}).Error)
			require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
			var alert model.SecurityAlert
			require.NoError(t, db.First(&alert, 45).Error)
			require.Equal(t, "actor:legacy", alert.SubjectRef)
			require.Equal(t, "resolved", alert.Status)
			require.EqualValues(t, 5, alert.Version)
			require.EqualValues(t, 3, alert.Occurrences)
			require.Nil(t, alert.RepeatKey)
			require.Nil(t, alert.RepeatUntil)
			require.Nil(t, alert.ConfirmedOccurrences)
		}
		require.True(t, db.Migrator().HasIndex(&model.SecurityAlert{}, "idx_sec_repeat_episode"))
		for range 2 {
			ddl := &migrationDDLLog{Interface: logger.Default.LogMode(logger.Silent)}
			require.NoError(t, model.MigrateAuditSecurity(t.Context(), db.Session(&gorm.Session{Logger: ddl})))
			require.Empty(t, ddl.statements, "startup cannot repeat DDL; upgrade=%v", upgrade)
		}
	}
}

func TestRepeatOperationLookupUsesCoveringIndex(t *testing.T) {
	db := newWorkerTestDB(t)
	rows := make([]model.SecurityAlert, 4000)
	for i := range rows {
		key := fmt.Sprintf("scope-%04d", i/4)
		until := int64(100000 + (i%4+1)*3600)
		rows[i] = model.SecurityAlert{RepeatKey: &key, RepeatUntil: &until, Evidence: "[]"}
		if i%4 == 3 {
			archived := int64(100000)
			rows[i].ArchivedAt = &archived
		}
	}
	require.NoError(t, db.CreateInBatches(rows, 100).Error)
	analyze := "ANALYZE audit_security_alerts"
	if db.Dialector.Name() == "mysql" {
		analyze = "ANALYZE TABLE audit_security_alerts"
	}
	require.NoError(t, db.Exec(analyze).Error)
	query := db.Model(&model.SecurityAlert{}).Select("id").Where("repeat_key = ? AND repeat_until > ? AND repeat_until <= ? AND (purge_state = ? OR purge_state IS NULL) AND archived_at IS NULL", "scope-0042", 100000, 186400, "").Order("repeat_until DESC").Limit(1)
	var found model.SecurityAlert
	require.NoError(t, query.Take(&found).Error)
	require.EqualValues(t, rows[170].ID, found.ID, "the newest archived episode is excluded")
	stmt := query.Session(&gorm.Session{DryRun: true}).Find(&model.SecurityAlert{}).Statement
	prefix := "EXPLAIN "
	if db.Dialector.Name() == "sqlite" {
		prefix = "EXPLAIN QUERY PLAN "
	}
	planRows, err := db.Raw(prefix+stmt.SQL.String(), stmt.Vars...).Rows()
	require.NoError(t, err)
	defer planRows.Close()
	columns, err := planRows.Columns()
	require.NoError(t, err)
	var plan []string
	for planRows.Next() {
		values := make([]sql.RawBytes, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		require.NoError(t, planRows.Scan(pointers...))
		for _, value := range values {
			plan = append(plan, string(value))
		}
	}
	require.NoError(t, planRows.Err())
	joined := strings.Join(plan, " | ")
	require.Contains(t, strings.ToLower(joined), "idx_sec_repeat_episode")
	t.Log(joined)
}
