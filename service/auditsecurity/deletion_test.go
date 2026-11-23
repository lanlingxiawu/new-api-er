package auditsecurity

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAlertDeletionVisibilityVersionAndEvidence(t *testing.T) {
	db := newWorkerTestDB(t)
	ctx := t.Context()
	alert := model.SecurityAlert{Visibility: common.RoleRootUser, Status: "open", Version: 3, Evidence: "[]"}
	require.NoError(t, db.Create(&alert).Error)
	require.ErrorIs(t, DeleteAlert(ctx, db, common.RoleAdminUser, alert.ID, 3), model.ErrSecurityConflict)
	require.ErrorIs(t, DeleteAlert(ctx, db, common.RoleRootUser, alert.ID, 2), model.ErrSecurityConflict)
	require.NoError(t, db.Create(&model.AuditLog{EventId: "retained-audit"}).Error)
	require.NoError(t, db.Create(&model.SecurityEvent{EventID: "retained-event"}).Error)
	links := make([]model.SecurityAlertEvent, 501)
	for i := range links {
		links[i] = model.SecurityAlertEvent{AlertID: alert.ID, EventID: fmt.Sprintf("delete-%d", i)}
	}
	require.NoError(t, db.CreateInBatches(links, 100).Error)
	require.NoError(t, db.Create(&model.SecurityFindingSnapshot{AlertID: alert.ID}).Error)
	require.NoError(t, db.Create(&model.SecurityAction{AlertID: alert.ID}).Error)
	require.NoError(t, db.Create(&model.SecurityDelivery{AlertID: alert.ID, Status: "pending"}).Error)
	var riskBefore model.SecurityRiskState
	require.NoError(t, db.Where("name = ?", "risk").Take(&riskBefore).Error)
	require.NoError(t, DeleteAlert(ctx, db, common.RoleRootUser, alert.ID, 3))
	require.ErrorIs(t, DeleteAlert(ctx, db, common.RoleRootUser, alert.ID, 3), model.ErrSecurityConflict)
	_, err := GetAlert(ctx, db, common.RoleRootUser, alert.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	var riskAfter model.SecurityRiskState
	require.NoError(t, db.Where("name = ?", "risk").Take(&riskAfter).Error)
	require.Greater(t, riskAfter.Revision, riskBefore.Revision)
	require.NoError(t, model.PurgePendingSecurityAlerts(ctx, db))
	var count int64
	require.NoError(t, db.Model(&model.SecurityAlertEvent{}).Where("alert_id = ?", alert.ID).Count(&count).Error)
	require.EqualValues(t, 1, count, "one bounded batch per cleanup tick")
	for range 6 {
		require.NoError(t, model.PurgePendingSecurityAlerts(ctx, db))
	}
	for _, table := range []any{&model.SecurityAlertEvent{}, &model.SecurityFindingSnapshot{}, &model.SecurityAction{}, &model.SecurityDelivery{}} {
		require.NoError(t, db.Model(table).Where("alert_id = ?", alert.ID).Count(&count).Error)
		require.Zero(t, count)
	}
	require.ErrorIs(t, db.First(&model.SecurityAlert{}, alert.ID).Error, gorm.ErrRecordNotFound)
	require.NoError(t, db.Model(&model.AuditLog{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, db.Model(&model.SecurityEvent{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestAlertCleanupBoundsAndClosure(t *testing.T) {
	db := newWorkerTestDB(t)
	ctx := t.Context()
	old, recent := time.Now().Unix()-86400, time.Now().Unix()
	for _, alert := range []model.SecurityAlert{
		{Visibility: 100, Status: "resolved", ClosedAt: &old},
		{Visibility: 10, Status: "open", ClosedAt: &old},
		{Visibility: 10, Status: "investigating", ClosedAt: &old},
		{Visibility: 10, Status: "resolved"},
		{Visibility: 10, Status: "false_positive", ClosedAt: &recent},
	} {
		alert.Version = 1
		require.NoError(t, db.Create(&alert).Error)
	}
	for i := range 101 {
		status := "resolved"
		if i%2 == 0 {
			status = "false_positive"
		}
		require.NoError(t, db.Create(&model.SecurityAlert{Visibility: 10, Status: status, ClosedAt: &old, Version: 1}).Error)
	}
	result, err := CleanupClosedAlerts(ctx, db, common.RoleAdminUser, old)
	require.NoError(t, err)
	require.Equal(t, 100, result.Queued)
	require.True(t, result.HasMore)
	result, err = CleanupClosedAlerts(ctx, db, common.RoleAdminUser, old)
	require.NoError(t, err)
	require.Equal(t, 1, result.Queued)
	require.False(t, result.HasMore)
	var count int64
	require.NoError(t, db.Model(&model.SecurityAlert{}).Where("purge_state = ?", "").Count(&count).Error)
	require.EqualValues(t, 5, count)
	result, err = CleanupClosedAlerts(ctx, db, common.RoleRootUser, old)
	require.NoError(t, err)
	require.Equal(t, 1, result.Queued)
}

func TestLegacyAccountAlertsRemainDeletableWithoutAccountVisibility(t *testing.T) {
	db, filter := customerAccessFixture(t)
	closed := filter.End - 60
	alerts := []model.SecurityAlert{
		{Status: "open"},
		{Status: "resolved"},
		{Status: "resolved", ClosedAt: &closed},
	}
	for i := range alerts {
		alert := &alerts[i]
		alert.Rule, alert.RuleVersion = "new_login_source", 2
		alert.ActorID, alert.ActorRole, alert.Visibility = 3, 1, 100
		alert.Occurrences, alert.Version = 101, 1
		alert.FirstSeen, alert.LastSeen, alert.Evidence = filter.Start, filter.End, "[]"
		require.NoError(t, db.Create(alert).Error)
		require.NoError(t, db.Create(&model.SecurityFindingSnapshot{AlertID: alert.ID}).Error)
		require.NoError(t, db.Create(&model.SecurityAlertEvent{AlertID: alert.ID, EventID: fmt.Sprintf("old-alert-%d", i)}).Error)
		require.NoError(t, db.Create(&model.SecurityAction{AlertID: alert.ID}).Error)
		require.NoError(t, db.Create(&model.SecurityDelivery{AlertID: alert.ID, Status: "skipped"}).Error)
		_, err := GetSelfSecurityAlert(t.Context(), db, 3, alert.ID)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		_, err = GetCustomerAlert(t.Context(), db, 1, 3, alert.ID)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		_, err = GetAlert(t.Context(), db, common.RoleRootUser, alert.ID)
		require.NoError(t, err, "account visibility must not remove the administrator's delete entry")
	}
	log := model.AuditLog{EventId: "legacy-alert-retained-audit", CreatedAt: filter.End}
	residualEvent := model.SecurityEvent{EventID: log.EventId, At: filter.End}
	require.NoError(t, db.Create(&log).Error)
	require.NoError(t, db.Create(&residualEvent).Error)
	queued, err := CleanupClosedAlerts(t.Context(), db, common.RoleRootUser, closed)
	require.NoError(t, err)
	require.Equal(t, 1, queued.Queued)
	for _, alert := range alerts[:2] {
		require.NoError(t, DeleteAlert(t.Context(), db, common.RoleRootUser, alert.ID, alert.Version), "open and unknown-closure legacy alerts can be explicitly deleted")
	}
	for range 20 {
		require.NoError(t, model.PurgePendingSecurityAlerts(t.Context(), db))
	}
	for _, alert := range alerts {
		require.ErrorIs(t, db.First(&model.SecurityAlert{}, alert.ID).Error, gorm.ErrRecordNotFound)
		for _, table := range []any{&model.SecurityAlertEvent{}, &model.SecurityFindingSnapshot{}, &model.SecurityAction{}, &model.SecurityDelivery{}} {
			var count int64
			require.NoError(t, db.Model(table).Where("alert_id = ?", alert.ID).Count(&count).Error)
			require.Zero(t, count)
		}
	}
	require.NoError(t, db.Where("event_id = ?", log.EventId).Take(&model.AuditLog{}).Error)
	require.NoError(t, db.Where("event_id = ?", residualEvent.EventID).Take(&model.SecurityEvent{}).Error)
}

func TestPendingDeletionWaitsForSendingAndRecoversStale(t *testing.T) {
	db := newWorkerTestDB(t)
	now := time.Now().Unix()
	alert := model.SecurityAlert{Visibility: 10, Status: "open", Version: 1, PurgeState: "deleting"}
	require.NoError(t, db.Create(&alert).Error)
	delivery := model.SecurityDelivery{AlertID: alert.ID, Status: "sending", UpdatedAt: now}
	require.NoError(t, db.Create(&delivery).Error)
	require.NoError(t, model.PurgePendingSecurityAlerts(t.Context(), db))
	require.NoError(t, db.First(&model.SecurityAlert{}, alert.ID).Error)
	require.NoError(t, db.Model(&delivery).UpdateColumn("updated_at", now-31).Error)
	for range 3 {
		require.NoError(t, model.PurgePendingSecurityAlerts(t.Context(), db))
	}
	require.ErrorIs(t, db.First(&model.SecurityAlert{}, alert.ID).Error, gorm.ErrRecordNotFound)
}

func TestDeletionIndexesUpgradeAndRestart(t *testing.T) {
	db := newWorkerTestDB(t)
	recorder := &ddlRecorder{Interface: logger.Default.LogMode(logger.Silent)}
	db.Logger = recorder
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
	require.Empty(t, recorder.statements, "fresh schema restart must not issue DDL")
	for _, index := range []string{"idx_sec_cleanup_closed", "idx_sec_purge_id"} {
		require.NoError(t, db.Migrator().DropIndex(&model.SecurityAlert{}, index))
	}
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
	for _, index := range []string{"idx_sec_cleanup_closed", "idx_sec_purge_id"} {
		require.True(t, db.Migrator().HasIndex(&model.SecurityAlert{}, index))
	}
	recorder.statements = nil
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
	require.Empty(t, recorder.statements, "upgrade restart must not issue DDL")
}

func TestDeletionQueryPlans(t *testing.T) {
	if os.Getenv("TEST_ADMIN_TOKEN_DSN") == "" {
		t.Skip("real isolated database required")
	}
	db := newWorkerTestDB(t)
	closed := time.Now().Unix() - 86400
	rows := make([]model.SecurityAlert, 10050)
	for i := range rows {
		rows[i] = model.SecurityAlert{Visibility: 10, Status: "open", Version: 1}
		if i < 10 {
			rows[i].Status, rows[i].ClosedAt = "resolved", &closed
		}
		if i >= 10 && i < 20 {
			rows[i].PurgeState = "deleting"
		}
	}
	require.NoError(t, db.CreateInBatches(rows, 250).Error)
	if db.Dialector.Name() == "postgres" {
		require.NoError(t, db.Exec("ANALYZE audit_security_alerts").Error)
	} else {
		require.NoError(t, db.Exec("ANALYZE TABLE audit_security_alerts").Error)
	}
	queries := map[string]*gorm.DB{
		"idx_sec_cleanup_closed": model.SecurityVisibleQuery(db.Model(&model.SecurityAlert{}), 10).Select("id,version").Where("status = ? AND closed_at > 0 AND closed_at <= ? AND (purge_state = ? OR purge_state IS NULL)", "resolved", closed, "").Order("closed_at ASC").Order("id ASC").Limit(101),
		"idx_sec_purge_id":       db.Model(&model.SecurityAlert{}).Select("id,version").Where("purge_state = ?", "deleting").Order("id ASC").Limit(20),
	}
	for index, query := range queries {
		statement := query.Session(&gorm.Session{DryRun: true}).Find(&[]model.SecurityAlert{}).Statement
		var plan []map[string]any
		explain := "EXPLAIN "
		if db.Dialector.Name() == "mysql" {
			explain = "EXPLAIN FORMAT=TRADITIONAL "
		}
		require.NoError(t, db.Raw(explain+db.Dialector.Explain(statement.SQL.String(), statement.Vars...)).Scan(&plan).Error)
		for _, row := range plan {
			for key, value := range row {
				if raw, ok := value.([]byte); ok {
					row[key] = string(raw)
				}
			}
		}
		raw, err := common.Marshal(plan)
		require.NoError(t, err)
		t.Logf("%s %s: %s", db.Dialector.Name(), index, raw)
		usesIndex := strings.Contains(string(raw), index)
		if index == "idx_sec_purge_id" {
			// PostgreSQL can prefer the existing purge-state index for a tiny
			// queue, sorting only those candidates instead of scanning alerts.
			usesIndex = usesIndex || strings.Contains(string(raw), "idx_audit_security_alerts_purge_state")
		}
		require.True(t, usesIndex, "query should use a bounded cleanup index: %s", raw)
	}
}

func TestDisabledMonitoringStillDrainsDeletion(t *testing.T) {
	db := newWorkerTestDB(t)
	var dsn string
	switch dialect := db.Dialector.(type) {
	case *mysql.Dialector:
		dsn = dialect.DSN
	case *postgres.Dialector:
		dsn = dialect.DSN
	case *sqlite.Dialector:
		previous := common.SQLitePath
		common.SQLitePath = dialect.DSN
		t.Cleanup(func() { common.SQLitePath = previous })
	default:
		t.Fatal("unexpected test database")
	}
	t.Setenv("SQL_DSN", dsn)
	t.Setenv("LOG_SQL_DSN", dsn)
	require.NoError(t, model.InitAuditReadPools(t.Context()))
	defer model.CloseAuditReadPools()
	previous := operation_setting.GetAuditSecuritySetting()
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{
		"enabled": "false", "capture_enabled": "false", "suspended": "true",
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{
			"enabled": fmt.Sprint(previous.Enabled), "capture_enabled": fmt.Sprint(previous.CaptureEnabled), "suspended": fmt.Sprint(previous.Suspended),
		}))
	})
	alert := model.SecurityAlert{Visibility: 10, Status: "resolved", Version: 1, PurgeState: "deleting"}
	require.NoError(t, db.Create(&alert).Error)
	stop := Start()
	defer stop()
	require.Eventually(t, func() bool {
		var count int64
		return db.Model(&model.SecurityAlert{}).Where("id = ?", alert.ID).Count(&count).Error == nil && count == 0
	}, 15*time.Second, 100*time.Millisecond, "queued deletion must finish even while monitoring is suspended")
}
