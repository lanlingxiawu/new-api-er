package auditsecurity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAlertEvidenceIPFilterAcrossRules(t *testing.T) {
	db := newWorkerTestDB(t)
	state, err := model.ClaimAuditSecurity(t.Context(), db, "evidence-ip-filter")
	require.NoError(t, err)
	now := time.Now().Unix()
	events := []model.SecurityEvent{
		{EventID: "key-first", At: now - 2, ActorID: 17, ActorRole: 100, Visibility: 100, SubjectRef: "actor:17", ResourceType: "channel", Action: "channel.key_view", TargetID: 1, Success: true, Count: 1, Facts: "{}", IP: "192.0.2.1"},
		{EventID: "key-second", At: now - 1, ActorID: 17, ActorRole: 100, Visibility: 100, SubjectRef: "actor:17", ResourceType: "channel", Action: "channel.key_view", TargetID: 2, Success: true, Count: 1, Facts: "{}", IP: "192.0.2.2"},
	}
	next := state
	next.CursorAt, next.CursorEvent = events[1].At, events[1].EventID
	cfg := operation_setting.DefaultAuditSecuritySetting()
	cfg.KeyViewThreshold = 2
	require.NoError(t, Apply(t.Context(), db, state, events, next, cfg))
	var alert model.SecurityAlert
	require.NoError(t, db.Where("rule = ?", "bulk_key_access").Take(&alert).Error)
	detail, err := GetAlert(t.Context(), db, 100, alert.ID)
	require.NoError(t, err)
	require.Len(t, detail.Events, 2)
	filter := AlertFilter{Start: now - 60, End: now + 1, Rule: "bulk_key_access", IP: events[0].IP, Limit: 50}
	page, err := ListAlerts(t.Context(), db, 100, filter)
	require.NoError(t, err)
	require.Len(t, page.Items, 1, "an alert must be searchable by retained evidence IPs")
	require.Equal(t, alert.ID, page.Items[0].ID)
	summary, err := SummarizeAlerts(t.Context(), db, 100, filter)
	require.NoError(t, err)
	require.Equal(t, 1, summary.Counts["open"])
	exported, err := ExportAlerts(t.Context(), db, 100, filter, 100)
	require.NoError(t, err)
	require.Len(t, exported.Items, 1)
	require.Equal(t, alert.ID, exported.Items[0].Alert.ID)
	markers, err := RiskMarkers(t.Context(), db, []*model.AuditLog{{EventId: events[0].EventID, CreatedAt: events[0].At, Ip: events[0].IP}}, 100)
	require.NoError(t, err)
	require.Len(t, markers, 1)
	require.Equal(t, "none", markers[0].State, "resource alerts must not mark an IP as risky")
	page, err = ListAlerts(t.Context(), db, 10, filter)
	require.NoError(t, err)
	require.Empty(t, page.Items, "IP matching must not bypass alert visibility")
}

// Exercise actual queries in isolated databases, including the same migrations twice.
func TestAuditFilterEnhancements(t *testing.T) {
	dialects := map[string]gorm.Dialector{"sqlite": sqlite.Open(filepath.Join(t.TempDir(), "audit.db"))}
	if dsn := os.Getenv("AUDIT_TEST_MYSQL_DSN"); dsn != "" {
		require.Contains(t, dsn, "/audit_test")
		dialects["mysql"] = mysql.Open(dsn)
	}
	if dsn := os.Getenv("AUDIT_TEST_PG_DSN"); dsn != "" {
		require.Contains(t, dsn, "/audit_test")
		dialects["postgres"] = postgres.Open(dsn)
	}
	for name, dialect := range dialects {
		t.Run(name, func(t *testing.T) {
			recorder := &ddlRecorder{Interface: logger.Default.LogMode(logger.Silent)}
			db, err := gorm.Open(dialect, &gorm.Config{Logger: recorder})
			require.NoError(t, err)
			pool, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, pool.Close()) })
			require.NoError(t, db.AutoMigrate(&model.AuditLog{}))
			require.NoError(t, model.MigrateAuditSecurity(context.Background(), db))
			recorder.statements = nil
			require.NoError(t, db.AutoMigrate(&model.AuditLog{}))
			require.NoError(t, model.MigrateAuditSecurity(context.Background(), db))
			require.Empty(t, recorder.statements, "restarting must not alter the audit schema")
			prefix := common.NewRequestId()
			yes, no := true, false
			rows := []model.AuditLog{
				{EventId: prefix + "a", Username: prefix, UserId: 31, ActorRole: 1, ActorIsEmployee: &yes, CreatedAt: 100, Ip: "203.0.113.1", AuthMethod: "session", Route: "/api/user/self"},
				{EventId: prefix + "b", Username: prefix, UserId: 31, ActorRole: 1, ActorIsEmployee: &no, CreatedAt: 101, Ip: "203.0.113.1", AuthMethod: "access_token", Route: "/api/user/self"},
				{EventId: prefix + "c", Username: prefix, UserId: 31, ActorRole: 1, CreatedAt: 102},
				{EventId: prefix + "d", Username: prefix, UserId: 32, ActorRole: 100, CreatedAt: 103, Route: "/root-only"},
				{EventId: prefix + "e", Username: prefix, UserId: 33, ActorRole: 0, CreatedAt: 104, Route: "/unknown-only"},
			}
			require.NoError(t, db.Create(&rows).Error)
			t.Cleanup(func() { db.Where("username = ?", prefix).Delete(&model.AuditLog{}) })
			for _, tc := range []struct {
				actor string
				count int
			}{{"", 3}, {"employee", 1}, {"user", 1}, {"unclassified_user", 1}, {"root", 0}, {"unknown", 0}, {"admin", 0}} {
				got, total, err := model.QueryAuditLogs(db, model.AuditLogFilter{Username: prefix, ActorType: tc.actor}, 0, 20, 10)
				require.NoError(t, err)
				require.Len(t, got, tc.count)
				require.EqualValues(t, tc.count, total)
			}
			got, total, err := model.QueryAuditLogs(db, model.AuditLogFilter{Username: prefix, IP: "203.0.113.1", AuthMethod: "access_token", ActorType: "employee"}, 0, 20, 100)
			require.NoError(t, err)
			require.Empty(t, got)
			require.Zero(t, total)
			got, total, err = model.QueryAuditLogs(db, model.AuditLogFilter{Username: prefix}, 0, 20, 100)
			require.NoError(t, err)
			require.Len(t, got, 5)
			require.EqualValues(t, 5, total) // Old API has no implicit time window.
			routes, err := model.QueryAuditRoutes(db, model.AuditLogFilter{UserId: 31, StartTimestamp: 99, EndTimestamp: 105}, 10, "", "", 50)
			require.NoError(t, err)
			require.Equal(t, []string{"/api/user/self"}, routes.Items)
			for _, filter := range []model.AuditLogFilter{{IP: "203.0.113.1"}, {ActorType: "employee"}, {Route: "/api/user/self"}} {
				filter.StartTimestamp, filter.EndTimestamp = 99, 105
				querySQL := db.ToSQL(func(tx *gorm.DB) *gorm.DB {
					return model.AuditLogQuery(tx, filter, 10).Select(model.AuditLogColumns).Order("created_at DESC").Order("event_id DESC").Limit(20).Find(&[]model.AuditLog{})
				})
				explain := "EXPLAIN "
				if name == "sqlite" {
					explain = "EXPLAIN QUERY PLAN "
				}
				var plan []map[string]any
				require.NoError(t, db.Raw(explain+querySQL).Scan(&plan).Error)
				encoded, err := common.Marshal(plan)
				require.NoError(t, err)
				t.Logf("audit filter plan (%s): %s", querySQL, encoded)
			}

			alerts := []model.SecurityAlert{{Rule: "login_failures", Status: "open", Visibility: 100, SubjectRef: prefix}, {Rule: "password_spray", Status: "open", Visibility: 100, SubjectRef: prefix}}
			require.NoError(t, db.Create(&alerts).Error)
			links := []model.SecurityAlertEvent{{EventID: rows[0].EventId, EventAt: 100, IP: rows[0].Ip, AlertID: alerts[0].ID}, {EventID: rows[0].EventId, EventAt: 100, IP: rows[0].Ip, AlertID: alerts[1].ID}, {EventID: rows[1].EventId, EventAt: 101, IP: rows[1].Ip, AlertID: alerts[0].ID}}
			require.NoError(t, db.Create(&links).Error)
			t.Cleanup(func() {
				db.Where("alert_id IN ?", []int64{alerts[0].ID, alerts[1].ID}).Delete(&model.SecurityAlertEvent{})
				db.Where("subject_ref = ?", prefix).Delete(&model.SecurityAlert{})
			})
			f := model.AuditLogFilter{Username: prefix, StartTimestamp: 99, EndTimestamp: 105}
			page, err := QueryRiskRecords(context.Background(), db, db, f, 100, 1, "attention", "", 1)
			require.NoError(t, err)
			require.Len(t, page.Items, 1)
			require.Equal(t, rows[1].EventId, page.Items[0].EventId)
			page, err = QueryRiskRecords(context.Background(), db, db, f, 100, 1, "abnormal", "", 1)
			require.NoError(t, err)
			require.Len(t, page.Items, 1)
			require.NotEmpty(t, page.NextCursor)
			next, err := QueryRiskRecords(context.Background(), db, db, f, 100, 1, "abnormal", page.NextCursor, 1)
			require.NoError(t, err)
			require.Len(t, next.Items, 1)
			require.Equal(t, rows[0].EventId, next.Items[0].EventId)
			_, err = QueryRiskRecords(context.Background(), db, db, f, 100, 2, "abnormal", page.NextCursor, 1)
			require.ErrorIs(t, err, ErrRiskCursor)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return model.AdvanceAuditRiskRevision(tx) }))
			_, err = QueryRiskRecords(context.Background(), db, db, f, 100, 1, "abnormal", page.NextCursor, 1)
			require.ErrorIs(t, err, ErrRiskCursor)
			hidden, err := QueryRiskRecords(context.Background(), db, db, f, 10, 1, "abnormal", "", 20)
			require.NoError(t, err)
			require.Empty(t, hidden.Items)
			require.NoError(t, db.Model(&alerts[1]).Update("status", "false_positive").Error)
			markers, err := RiskMarkers(context.Background(), db, []*model.AuditLog{&rows[0], &rows[1], &rows[2]}, 100)
			require.NoError(t, err)
			require.Equal(t, "attention", markers[0].Level)
			require.Equal(t, "none", markers[2].State, "unrelated operations cannot inherit risk")
			require.NoError(t, db.Model(&alerts[0]).Update("status", "resolved").Error)
			markers, err = RiskMarkers(context.Background(), db, []*model.AuditLog{&rows[0]}, 100)
			require.NoError(t, err)
			require.Equal(t, "resolved", markers[0].State)
			require.Empty(t, markers[0].Level)
			require.NoError(t, db.Model(&alerts[0]).Update("status", "open").Error)
			// Exhausting the candidate budget must preserve a continuation even on an empty page.
			sparseLinks := make([]model.SecurityAlertEvent, 1001)
			for i := range sparseLinks {
				sparseLinks[i] = model.SecurityAlertEvent{EventID: fmt.Sprintf("%s-sparse-%04d", prefix, i), EventAt: 105, AlertID: alerts[0].ID}
			}
			require.NoError(t, db.CreateInBatches(sparseLinks, 100).Error)
			sparse, err := QueryRiskRecords(context.Background(), db, db, f, 100, 1, "attention", "", 1)
			require.NoError(t, err)
			require.Empty(t, sparse.Items)
			require.NotEmpty(t, sparse.NextCursor)
			sparse, err = QueryRiskRecords(context.Background(), db, db, f, 100, 1, "attention", sparse.NextCursor, 1)
			require.NoError(t, err)
			require.Len(t, sparse.Items, 1)
			require.Equal(t, rows[1].EventId, sparse.Items[0].EventId)
			t.Run("bounded transactions retain evidence and resume after rollback", func(t *testing.T) {
				owner := common.NewRequestId()
				state, err := model.ClaimAuditSecurity(context.Background(), db, owner)
				require.NoError(t, err)
				t.Cleanup(func() {
					var ids []int64
					db.Model(&model.SecurityAlert{}).Where("subject_ref = ?", owner).Pluck("id", &ids)
					if len(ids) > 0 {
						db.Where("alert_id IN ?", ids).Delete(&model.SecurityAlertEvent{})
					}
					db.Where("subject_ref = ?", owner).Delete(&model.SecurityAlert{})
					db.Where("subject_ref = ?", owner).Delete(&model.SecurityEvent{})
					db.Where("owner = ?", owner).Delete(&model.SecurityState{})
				})
				events := make([]model.SecurityEvent, 41)
				for i := range events {
					events[i] = model.SecurityEvent{EventID: fmt.Sprintf("%s-%03d", owner, i), At: time.Now().Unix() + int64(i), Action: "auth.session_refresh_reuse", SubjectRef: owner, Visibility: 100, IP: "203.0.113.9"}
				}
				next := state
				next.CursorAt, next.CursorEvent = events[40].At, events[40].EventID
				injected := errors.New("injected batch failure")
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:audit-batch-rollback", func(tx *gorm.DB) {
					if event, ok := tx.Statement.Dest.(*model.SecurityEvent); ok && event.EventID == events[20].EventID {
						tx.AddError(injected)
					}
				}))
				cfg := operation_setting.DefaultAuditSecuritySetting()
				err = Apply(context.Background(), db, state, events, next, cfg)
				db.Callback().Create().Remove("test:audit-batch-rollback")
				require.ErrorIs(t, err, injected)
				var checkpoint model.SecurityState
				require.NoError(t, db.Where("name = ?", "scanner").Take(&checkpoint).Error)
				require.Equal(t, events[19].EventID, checkpoint.CursorEvent)
				var count int64
				require.NoError(t, db.Model(&model.SecurityEvent{}).Where("subject_ref = ?", owner).Count(&count).Error)
				require.EqualValues(t, 20, count)
				require.NoError(t, Apply(context.Background(), db, checkpoint, events[20:], next, cfg))
				var alert model.SecurityAlert
				require.NoError(t, db.Where("subject_ref = ? AND rule = ?", owner, "session_reuse").Take(&alert).Error)
				require.NoError(t, db.Model(&model.SecurityAlertEvent{}).Where("alert_id = ?", alert.ID).Count(&count).Error)
				require.EqualValues(t, 41, count, "links retain evidence rotated out of the 20-item alert detail")
				// Live overlap replay must retain its checkpoint and avoid duplicate links.
				require.NoError(t, db.Where("name = ?", "scanner").Take(&checkpoint).Error)
				err = Apply(context.Background(), db, checkpoint, events[:21], checkpoint, cfg)
				require.NoError(t, err)
				require.NoError(t, db.Model(&model.SecurityAlertEvent{}).Where("alert_id = ?", alert.ID).Count(&count).Error)
				require.EqualValues(t, 41, count)
			})
			t.Logf("%s: filters, old time contract, event-level risk, cursor revision and visibility verified (%s)", name, fmt.Sprint(len(rows)))
		})
	}
}

func TestAuditFilterUpgrade(t *testing.T) {
	dialects := map[string]gorm.Dialector{"sqlite": sqlite.Open(filepath.Join(t.TempDir(), "upgrade.db"))}
	if dsn := os.Getenv("AUDIT_TEST_MYSQL_UPGRADE_DSN"); dsn != "" {
		require.Contains(t, dsn, "/audit_test")
		dialects["mysql"] = mysql.Open(dsn)
	}
	if dsn := os.Getenv("AUDIT_TEST_PG_UPGRADE_DSN"); dsn != "" {
		require.Contains(t, dsn, "/audit_test")
		dialects["postgres"] = postgres.Open(dsn)
	}
	for name, driver := range dialects {
		t.Run(name, func(t *testing.T) {
			recorder := &ddlRecorder{Interface: logger.Default.LogMode(logger.Silent)}
			db, err := gorm.Open(driver, &gorm.Config{Logger: recorder})
			require.NoError(t, err)
			pool, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, pool.Close()) })
			require.NoError(t, db.Table("audit_logs").AutoMigrate(&auditLogBeforeFilters{}))
			require.False(t, db.Migrator().HasColumn("audit_logs", "actor_is_employee"), "fixture must start with the prior-release schema")
			id := common.NewRequestId()
			require.NoError(t, db.Table("audit_logs").Create(&auditLogBeforeFilters{EventId: id, UserId: 1, ActorRole: 1, CreatedAt: 100}).Error)
			t.Cleanup(func() { db.Where("event_id = ?", id).Delete(&model.AuditLog{}) })
			require.NoError(t, db.AutoMigrate(&model.AuditLog{}))
			require.NoError(t, model.MigrateAuditSecurity(context.Background(), db))
			var record model.AuditLog
			require.NoError(t, db.Where("event_id = ?", id).Take(&record).Error)
			require.Nil(t, record.ActorIsEmployee, "historical identity must remain unknown after upgrade")
			recorder.statements = nil
			require.NoError(t, db.AutoMigrate(&model.AuditLog{}))
			require.NoError(t, model.MigrateAuditSecurity(context.Background(), db))
			require.Empty(t, recorder.statements, "second startup after upgrade must issue no DDL")
		})
	}
}

// Prior-release schema, before employee snapshots and filter indexes.
type auditLogBeforeFilters struct {
	Id         int              `json:"id"`
	EventId    string           `json:"event_id" gorm:"type:varchar(64);uniqueIndex;index:idx_audit_time_event,priority:2"`
	UserId     int              `json:"user_id" gorm:"index:idx_audit_user_time,priority:1"`
	Username   string           `json:"username" gorm:"type:varchar(64);index"`
	ActorRole  int              `json:"actor_role"` // Immutable role of the actor when the event began, not the log owner.
	CreatedAt  int64            `json:"created_at" gorm:"type:bigint;index:idx_audit_user_time,priority:2;index:idx_audit_token_time,priority:2;index;index:idx_audit_time_event,priority:1"`
	Category   string           `json:"category" gorm:"type:varchar(24);index"`
	Action     string           `json:"action" gorm:"type:varchar(128)"`
	TokenRef   string           `json:"token_ref" gorm:"type:varchar(64);index:idx_audit_token_time,priority:1"`
	AuthMethod string           `json:"auth_method" gorm:"type:varchar(24)"`
	Ip         string           `json:"ip" gorm:"type:varchar(64)"`
	UserAgent  string           `json:"user_agent" gorm:"type:varchar(512)"`
	Method     string           `json:"method" gorm:"type:varchar(16)"`
	Route      string           `json:"route" gorm:"type:varchar(255)"`
	Status     int              `json:"status"`
	Success    bool             `json:"success"`
	RequestId  string           `json:"request_id" gorm:"type:varchar(64);index"`
	Content    string           `json:"content" gorm:"type:text"`
	Other      model.AuditOther `json:"other" gorm:"type:json"`
}
