package auditsecurity

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Cleanup and lease tests own an entire disposable database, including state rows.
func newWorkerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_ADMIN_TOKEN_DSN")
	var dialect gorm.Dialector = sqlite.Open(filepath.Join(t.TempDir(), "worker.db"))
	if dsn != "" {
		require.Contains(t, dsn, "/audit_test")
		database := fmt.Sprintf("audit_test_worker_%d", time.Now().UnixNano())
		if strings.HasPrefix(dsn, "postgres") {
			dialect = postgres.Open(dsn)
			u, err := url.Parse(dsn)
			require.NoError(t, err)
			u.Path = "/" + database
			dsn = u.String()
		} else {
			dialect = mysql.Open(dsn)
			cfg, err := drivermysql.ParseDSN(dsn)
			require.NoError(t, err)
			cfg.DBName = database
			dsn = cfg.FormatDSN()
		}
		admin, err := gorm.Open(dialect, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		pool, err := admin.DB()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, pool.Close()) })
		require.NoError(t, admin.Exec("CREATE DATABASE "+database).Error)
		t.Cleanup(func() { require.NoError(t, admin.Exec("DROP DATABASE "+database).Error) })
		if strings.HasPrefix(dsn, "postgres") {
			dialect = postgres.Open(dsn)
		} else {
			dialect = mysql.Open(dsn)
		}
	}
	db, err := gorm.Open(dialect, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	require.NoError(t, model.MigrateAuditSecurity(context.Background(), db))
	require.NoError(t, db.AutoMigrate(&model.AuditLog{}))
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.EmployeeProfile{}))
	return db
}

func TestSourceStartsAtEnableAndPersistsBoundary(t *testing.T) {
	db := newWorkerTestDB(t)
	ctx := context.Background()
	w := worker{db: db, source: db, writer: db, owner: "new-events-fixture"}
	now := time.Now().Unix()
	rows := make([]model.AuditLog, 501)
	for i := range rows {
		rows[i] = model.AuditLog{EventId: fmt.Sprintf("old-%03d", i), CreatedAt: now - 30, UserId: 97531, ActorRole: 10, Action: "channel.key_view", Success: true}
	}
	require.NoError(t, db.CreateInBatches(rows, 100).Error)
	state, err := model.ClaimAuditSecurity(ctx, db, w.owner)
	require.NoError(t, err)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	require.NoError(t, w.scan(ctx, state, cfg))
	var count int64
	require.NoError(t, db.Model(&model.SecurityEvent{}).Count(&count).Error)
	require.Zero(t, count, "first enable must not analyze existing history")
	var start model.SecurityState
	require.NoError(t, db.Where("name = ?", "source_start").Take(&start).Error)
	require.GreaterOrEqual(t, start.CursorAt, now)
	for i := range rows {
		rows[i] = model.AuditLog{EventId: fmt.Sprintf("new-%03d", i), CreatedAt: start.CursorAt, UserId: 97532, ActorRole: 10, Action: "channel.key_view", Success: true}
	}
	require.NoError(t, db.CreateInBatches(rows, 100).Error)
	require.NoError(t, db.Create(&model.AuditLog{EventId: "late-old", CreatedAt: start.CursorAt - 1, UserId: 97531, ActorRole: 10, Action: "channel.key_view", Success: true}).Error)
	state, err = model.ClaimAuditSecurity(ctx, db, w.owner)
	require.NoError(t, err)
	require.NoError(t, w.scan(ctx, state, cfg))
	require.NoError(t, db.Model(&model.SecurityEvent{}).Count(&count).Error)
	require.EqualValues(t, 500, count)
	state, err = model.ClaimAuditSecurity(ctx, db, w.owner)
	require.NoError(t, err)
	w = worker{db: db, source: db, writer: db, owner: w.owner}
	require.NoError(t, w.scan(ctx, state, cfg))
	require.Equal(t, start.CursorAt, w.sourceStartAt, "restart restores the original coverage boundary")
	require.NoError(t, db.Model(&model.SecurityEvent{}).Count(&count).Error)
	require.EqualValues(t, 501, count, "restart resumes new events without processing old ones")
	var saved model.SecurityState
	require.NoError(t, db.Where("name = ?", "scanner").Take(&saved).Error)
	require.GreaterOrEqual(t, saved.CursorAt, start.CursorAt, "overlap cannot cross the enable boundary")
	require.NoError(t, db.Model(&model.SecurityState{}).Where("name = ?", "source_repair").Count(&count).Error)
	require.Zero(t, count)
	state, err = model.ClaimAuditSecurity(ctx, db, w.owner)
	require.NoError(t, err)
	require.NoError(t, w.scan(ctx, state, cfg))
	require.NoError(t, db.Model(&model.SecurityEvent{}).Where("actor_id = ?", 97531).Count(&count).Error)
	require.Zero(t, count)
}

func TestSecurityBatchDeduplicatesReceipts(t *testing.T) {
	db := newWorkerTestDB(t)
	ctx := context.Background()
	state, err := model.ClaimAuditSecurity(ctx, db, "dedup-fixture")
	require.NoError(t, err)
	e := Normalize(model.AuditLog{EventId: "repeated-event", CreatedAt: time.Now().Unix(), UserId: 97533, ActorRole: 10, Action: "channel.key_view", Success: true})
	reads := 0
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:receipt-reads", func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_security_events" && strings.Contains(strings.ToLower(tx.Statement.SQL.String()), "event_id") {
			reads++
		}
	}))
	t.Cleanup(func() { db.Callback().Query().Remove("test:receipt-reads") })
	cfg := operation_setting.DefaultAuditSecuritySetting()
	require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{e, e}, state, cfg))
	reads = 0
	require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{e, e, e}, state, cfg))
	require.Equal(t, 1, reads, "a replayed batch needs one receipt lookup, no per-event count/history")
	var alert model.SecurityAlert
	require.NoError(t, db.Where("actor_id = ?", e.ActorID).Take(&alert).Error)
	require.EqualValues(t, 1, alert.Occurrences)
}

func TestMonitoringDoesNotBackfillOldAlertEvidence(t *testing.T) {
	db := newWorkerTestDB(t)
	previous := primary.Load()
	t.Cleanup(func() { primary.Store(previous) })
	now := time.Now().Unix()
	evidence, err := common.Marshal([]model.SecurityEvent{{EventID: "old-risk", At: now - 600, IP: "203.0.113.9", Action: "auth.session_refresh_reuse"}})
	require.NoError(t, err)
	alert := model.SecurityAlert{Rule: "session_reuse", Status: "open", LastSeen: now - 600, Evidence: string(evidence)}
	require.NoError(t, db.Create(&alert).Error)
	require.NoError(t, db.Model(&model.SecurityRiskState{}).Where("name = ?", "risk").Update("backfill_end_id", alert.ID).Error)
	var before model.SecurityRiskState
	require.NoError(t, db.Where("name = ?", "risk").Take(&before).Error)
	w := worker{db: db, source: db, writer: db, owner: "no-backfill-fixture"}
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.Enabled = true
	for range 2 {
		w.lastScanAttempt = time.Time{}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		done := make(chan struct{})
		w.cycle(ctx, cancel, done, cfg)
		<-done
		require.Empty(t, Status().ErrorCode)
	}
	var stored model.SecurityAlert
	require.NoError(t, db.First(&stored, alert.ID).Error)
	require.Equal(t, alert, stored, "existing evidence and alert fields stay unchanged")
	var count int64
	require.NoError(t, db.Model(&model.SecurityAlertEvent{}).Where("alert_id = ?", alert.ID).Count(&count).Error)
	require.Zero(t, count, "retained JSON evidence must not be supplemented into risk links")
	var after model.SecurityRiskState
	require.NoError(t, db.Where("name = ?", "risk").Take(&after).Error)
	require.Equal(t, before, after)
	// Even an old completed backfill marker must not advertise history coverage.
	require.NoError(t, db.Model(&model.SecurityRiskState{}).Where("name = ?", "risk").Update("backfill_done", true).Error)
	require.False(t, CurrentRiskCoverage().HistoricalComplete)
	require.False(t, CurrentRiskCoverage().BackfillDone)
}

func TestSourceBoundaryFailureDoesNotReadHistory(t *testing.T) {
	db := newWorkerTestDB(t)
	w := worker{db: db, source: db, owner: "boundary-failure-fixture"}
	state, err := model.ClaimAuditSecurity(context.Background(), db, w.owner)
	require.NoError(t, err)
	injected := errors.New("boundary unavailable")
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:boundary-failure", func(tx *gorm.DB) {
		if row, ok := tx.Statement.Dest.(*model.SecurityState); ok && row.Name == "source_start" {
			tx.AddError(injected)
		}
	}))
	t.Cleanup(func() { db.Callback().Create().Remove("test:boundary-failure") })
	reads := 0
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:history-reads", func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_logs" {
			reads++
		}
	}))
	t.Cleanup(func() { db.Callback().Query().Remove("test:history-reads") })
	require.ErrorIs(t, w.scan(context.Background(), state, operation_setting.DefaultAuditSecuritySetting()), injected)
	require.Zero(t, reads)
	require.Zero(t, w.sourceStartAt)
}

func TestLiveSourceResumesCommittedBatchAfterFailure(t *testing.T) {
	db := newWorkerTestDB(t)
	ctx := context.Background()
	w := worker{db: db, source: db, owner: "live-retry-fixture"}
	now := time.Now().Unix()
	require.NoError(t, db.Create(&model.SecurityState{Name: "source_start", CursorAt: now - 600}).Error)
	rows := make([]model.AuditLog, 45)
	for i := range rows {
		rows[i] = model.AuditLog{EventId: fmt.Sprintf("retry-%03d", i), CreatedAt: now - 300, UserId: 97535, ActorRole: 10, Action: "channel.key_view", Success: true}
	}
	require.NoError(t, db.Create(&rows).Error)
	injected := errors.New("live receipt failure")
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:live-receipt-failure", func(tx *gorm.DB) {
		if event, ok := tx.Statement.Dest.(*model.SecurityEvent); ok && event.EventID == "retry-020" {
			tx.AddError(injected)
		}
	}))
	t.Cleanup(func() { db.Callback().Create().Remove("test:live-receipt-failure") })
	state, err := model.ClaimAuditSecurity(ctx, db, w.owner)
	require.NoError(t, err)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	require.ErrorIs(t, w.scan(ctx, state, cfg), injected)
	var checkpoint model.SecurityState
	require.NoError(t, db.Where("name = ?", "scanner").Take(&checkpoint).Error)
	require.Equal(t, "retry-019", checkpoint.CursorEvent)
	var count int64
	require.NoError(t, db.Model(&model.SecurityEvent{}).Where("actor_id = ?", 97535).Count(&count).Error)
	require.EqualValues(t, 20, count, "failed batch must roll back receipts and findings together")
	require.NoError(t, db.Callback().Create().Remove("test:live-receipt-failure"))
	state, err = model.ClaimAuditSecurity(ctx, db, w.owner)
	require.NoError(t, err)
	w = worker{db: db, source: db, owner: w.owner}
	require.NoError(t, w.scan(ctx, state, cfg))
	require.NoError(t, db.Model(&model.SecurityEvent{}).Where("actor_id = ?", 97535).Count(&count).Error)
	require.EqualValues(t, 45, count)
	var alert model.SecurityAlert
	require.NoError(t, db.Where("actor_id = ? AND rule = ?", 97535, "sensitive_operation").Take(&alert).Error)
	require.EqualValues(t, 45, alert.Occurrences)
}

func TestSecurityCleanupDrainsBacklogInBoundedBatches(t *testing.T) {
	db := newWorkerTestDB(t)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	w := worker{db: db}
	old := time.Now().Unix() - int64(cfg.AlertRetentionDays+1)*86400
	alerts := make([]model.SecurityAlert, 101)
	for i := range alerts {
		closedAt := old
		alerts[i] = model.SecurityAlert{ActorID: 97534, LastSeen: old, Status: "resolved", ClosedAt: &closedAt}
	}
	require.NoError(t, db.Create(&alerts).Error)
	actions := make([]model.SecurityAction, 501)
	deliveries := make([]model.SecurityDelivery, 501)
	for i := range actions {
		actions[i].AlertID = alerts[0].ID
		deliveries[i].AlertID = alerts[0].ID
	}
	require.NoError(t, db.CreateInBatches(actions, 100).Error)
	require.NoError(t, db.CreateInBatches(deliveries, 100).Error)
	deleted := map[string]int64{}
	require.NoError(t, db.Callback().Delete().After("gorm:delete").Register("test:cleanup-budget", func(tx *gorm.DB) { deleted[tx.Statement.Table] += tx.RowsAffected }))
	t.Cleanup(func() { db.Callback().Delete().Remove("test:cleanup-budget") })
	for range 250 {
		clear(deleted)
		require.NoError(t, w.cleanup(context.Background(), cfg))
		require.LessOrEqual(t, deleted["audit_security_actions"], int64(500))
		require.LessOrEqual(t, deleted["audit_security_deliveries"], int64(500))
		require.LessOrEqual(t, deleted["audit_security_alerts"], int64(1))
	}
	var count int64
	require.NoError(t, db.Model(&model.SecurityAlert{}).Count(&count).Error)
	require.Zero(t, count, "do not wait an hour while expired alerts remain")
	require.NotZero(t, w.lastCleanup)
}

func TestSecurityCleanupEvidenceRollbackAndRetention(t *testing.T) {
	db := newWorkerTestDB(t)
	cfg := operation_setting.DefaultAuditSecuritySetting()
	w := worker{db: db}
	now := time.Now().Unix()
	closedAt := now - int64(cfg.AlertRetentionDays+1)*86400
	expired := model.SecurityAlert{LastSeen: closedAt, Status: "resolved", ClosedAt: &closedAt}
	recent := model.SecurityAlert{LastSeen: now, Status: "open"}
	require.NoError(t, db.Create(&expired).Error)
	require.NoError(t, db.Create(&recent).Error)
	links := make([]model.SecurityAlertEvent, 501)
	for i := range links {
		links[i] = model.SecurityAlertEvent{AlertID: expired.ID, EventID: fmt.Sprintf("cleanup-%d", i)}
	}
	require.NoError(t, db.CreateInBatches(links, 100).Error)
	retained := model.SecurityAction{AlertID: recent.ID}
	require.NoError(t, db.Create(&retained).Error)
	require.NoError(t, w.cleanup(context.Background(), cfg), "mark the expired alert as deleting before testing the child rollback")
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:risk-revision-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_security_risk_states" {
			tx.AddError(errors.New("injected revision failure"))
		}
	}))
	t.Cleanup(func() { db.Callback().Update().Remove("test:risk-revision-failure") })
	require.Error(t, w.cleanup(context.Background(), cfg))
	var count int64
	require.NoError(t, db.Model(&model.SecurityAlertEvent{}).Where("alert_id = ?", expired.ID).Count(&count).Error)
	require.EqualValues(t, 501, count, "evidence and risk revision must roll back together")
	require.NoError(t, db.Callback().Update().Remove("test:risk-revision-failure"))
	require.NoError(t, w.cleanup(context.Background(), cfg))
	require.NoError(t, db.Model(&model.SecurityAlertEvent{}).Where("alert_id = ?", expired.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	for range 3 {
		require.NoError(t, w.cleanup(context.Background(), cfg))
	}
	require.NoError(t, db.First(&recent, recent.ID).Error)
	require.NoError(t, db.First(&retained, retained.ID).Error)
	require.ErrorIs(t, db.First(&model.SecurityAlert{}, expired.ID).Error, gorm.ErrRecordNotFound)
}
