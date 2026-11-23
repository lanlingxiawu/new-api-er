package auditsecurity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

func TestMonitoringHistoryQueriesStayScoped(t *testing.T) {
	cases := map[string]gorm.Dialector{"sqlite": sqlite.Open(filepath.Join(t.TempDir(), "history.db"))}
	if dsn := os.Getenv("AUDIT_TEST_MYSQL_DSN"); dsn != "" {
		require.Contains(t, dsn, "/audit_test")
		cases["mysql"] = mysql.Open(dsn)
	}
	if dsn := os.Getenv("AUDIT_TEST_PG_DSN"); dsn != "" {
		require.Contains(t, dsn, "/audit_test")
		cases["postgres"] = postgres.Open(dsn)
	}
	for name, driver := range cases {
		t.Run(name, func(t *testing.T) {
			db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			pool, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = pool.Close() })
			ctx := context.Background()
			require.NoError(t, model.MigrateAuditSecurity(ctx, db))
			owner := common.NewRequestId()
			state, err := model.ClaimAuditSecurity(ctx, db, owner)
			require.NoError(t, err)
			actor := 946155
			now := time.Now().Unix()
			base := Normalize(model.AuditLog{EventId: owner, UserId: actor, ActorRole: 10, Action: "channel.key_view", Success: true, CreatedAt: now})
			base.SubjectRef = "fixture:" + owner
			t.Cleanup(func() {
				db.Where("actor_id IN ?", []int{actor, actor + 1}).Delete(&model.SecurityEvent{})
				db.Where("actor_id = ?", actor).Delete(&model.SecurityAlert{})
				db.Where("owner = ?", owner).Delete(&model.SecurityState{})
			})
			// Unrelated rows deliberately share the empty IP with the operator.
			noise := make([]model.SecurityEvent, 10050)
			for i := range noise {
				noise[i] = base
				noise[i].EventID = fmt.Sprintf("%s-noise-%d", owner, i)
				noise[i].ActorID = actor + 1
				noise[i].SubjectRef = "noise:" + owner
			}
			require.NoError(t, db.CreateInBatches(noise, 200).Error)
			for i := range 4 {
				e := base
				e.EventID = fmt.Sprintf("%s-related-%d", owner, i)
				require.NoError(t, db.Create(&e).Error)
			}
			var returned int
			var queries []string
			require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:history-budget", func(tx *gorm.DB) {
				_, boundedHistory := tx.Statement.Clauses["LIMIT"]
				if rows, ok := tx.Statement.Dest.(*[]model.SecurityEvent); ok && boundedHistory {
					returned += len(*rows)
					queries = append(queries, tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...))
				}
			}))
			t.Cleanup(func() { db.Callback().Query().Remove("test:history-budget") })
			cfg := operation_setting.DefaultAuditSecuritySetting()
			for _, action := range []string{"channel.update", "channel.token_update", "channel.tag_edit", "channel.upstream_apply", "channel.upstream_apply_all"} {
				ordinary := base
				ordinary.EventID += "-ordinary-" + action
				ordinary.Action = action
				ordinary.Count = 100
				ordinary.Facts = `{"changed_fields":["name","models","group","priority","weight"],"submitted_fields":["models","group","priority","weight"]}`
				require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{ordinary}, state, cfg))
				require.Empty(t, queries, "routine updates must not read correlation history")
				var count int64
				require.NoError(t, db.Model(&model.SecurityEvent{}).Where("event_id = ?", ordinary.EventID).Count(&count).Error)
				require.Zero(t, count, "routine updates must not grow the monitoring history")
			}
			require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{base}, state, cfg))
			require.Equal(t, 4, returned, "do not load other accounts merely because their IP is blank")
			require.Len(t, queries, 1)
			require.NotContains(t, queries[0], " OR ")
			var alert model.SecurityAlert
			require.NoError(t, db.Where("actor_id = ? AND rule = ?", actor, "sensitive_operation").Take(&alert).Error)
			// Record plans against real engines, without changing planner settings.
			if name == "postgres" {
				require.NoError(t, db.Exec("ANALYZE audit_security_events").Error)
			}
			explain := "EXPLAIN "
			if name == "sqlite" {
				explain = "EXPLAIN QUERY PLAN "
			}
			var plan []map[string]any
			require.NoError(t, db.Raw(explain+queries[0]).Scan(&plan).Error)
			encoded, err := common.Marshal(plan)
			require.NoError(t, err)
			t.Logf("%s history plan: %s", name, encoded)
			require.Contains(t, string(encoded), "idx_sec_actor_time")
			// A hot actor cannot cause an unbounded read, and truncation is visible.
			for i := range noise[:1001] {
				noise[i] = base
				noise[i].EventID = fmt.Sprintf("%s-hot-%d", owner, i)
			}
			require.NoError(t, db.CreateInBatches(noise[:1001], 200).Error)
			returned, queries = 0, nil
			healthMu.Lock()
			health.HistoryTruncated = false
			healthMu.Unlock()
			base.EventID += "-hot"
			require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{base}, state, cfg))
			require.Equal(t, 1001, returned)
			require.True(t, Status().HistoryTruncated)
			// Account and IP ranges overlap. Count a failed login only once and
			// ignore ordinary successful actions by that same account/IP.
			failed := base
			failed.Action, failed.Success, failed.Reason = "auth.login_failed", false, "invalid_credentials"
			failed.SubjectRef, failed.IP = "account:"+owner, "192.0.2.41"
			failed.Visibility = common.RoleRootUser
			for i := range 4 {
				failed.EventID = fmt.Sprintf("%s-failed-%d", owner, i)
				require.NoError(t, db.Create(&failed).Error)
			}
			failed.EventID += "-current"
			returned, queries = 0, nil
			require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{failed}, state, cfg))
			require.Len(t, queries, 2)
			require.Equal(t, 8, returned)
			alert = model.SecurityAlert{}
			require.NoError(t, db.Where("rule = ? AND subject_ref = ?", "login_failures", failed.SubjectRef).Take(&alert).Error)
			require.Equal(t, 5, alert.MatchedCount)
		})
	}
}

type ddlRecorder struct {
	logger.Interface
	mu         sync.Mutex
	statements []string
}

func (r *ddlRecorder) Trace(ctx context.Context, start time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	upper := strings.ToUpper(strings.TrimSpace(sql))
	if strings.HasPrefix(upper, "ALTER ") || strings.HasPrefix(upper, "CREATE ") || strings.HasPrefix(upper, "DROP ") {
		r.mu.Lock()
		r.statements = append(r.statements, sql)
		r.mu.Unlock()
	}
}

// Only dedicated disposable databases are accepted. No project .env is loaded.
func TestMonitoringDatabaseLifecycle(t *testing.T) {
	cases := map[string]gorm.Dialector{"sqlite": sqlite.Open(filepath.Join(t.TempDir(), "audit.db"))}
	if dsn := os.Getenv("AUDIT_TEST_MYSQL_DSN"); dsn != "" {
		require.Contains(t, dsn, "/audit_test")
		cases["mysql"] = mysql.Open(dsn)
	}
	if dsn := os.Getenv("AUDIT_TEST_PG_DSN"); dsn != "" {
		require.Contains(t, dsn, "/audit_test")
		cases["postgres"] = postgres.Open(dsn)
	}
	for name, dialect := range cases {
		t.Run(name, func(t *testing.T) {
			rec := &ddlRecorder{Interface: logger.Default.LogMode(logger.Silent)}
			db, err := gorm.Open(dialect, &gorm.Config{Logger: rec})
			require.NoError(t, err)
			pool, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = pool.Close() })
			ctx := context.Background()
			var version string
			versionSQL := "SELECT VERSION()"
			if name == "sqlite" {
				versionSQL = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
			t.Log(name + " version: " + version)
			// Previous installations have audit logs but no monitoring tables.
			require.NoError(t, db.AutoMigrate(&model.AuditLog{}))
			// Management reads resolve current actor usernames from the primary DB.
			require.NoError(t, db.AutoMigrate(&model.User{}))
			require.NoError(t, model.MigrateAuditSecurity(ctx, db))
			rec.statements = nil
			require.NoError(t, model.MigrateAuditSecurity(ctx, db))
			require.Empty(t, rec.statements, "second initialization must not issue DDL")
			owner := "test-" + name
			var priorIDs []int64
			db.Model(&model.SecurityAlert{}).Where("actor_id = ?", 946128).Pluck("id", &priorIDs)
			if len(priorIDs) > 0 {
				db.Where("alert_id IN ?", priorIDs).Delete(&model.SecurityDelivery{})
				db.Where("alert_id IN ?", priorIDs).Delete(&model.SecurityAction{})
				db.Where("id IN ?", priorIDs).Delete(&model.SecurityAlert{})
			}
			db.Where("actor_id = ?", 946128).Delete(&model.SecurityEvent{})
			// Tests clean only their own state/events/alerts; never truncate tables.
			require.NoError(t, db.Where("name = ?", "scanner").Delete(&model.SecurityState{}).Error)
			state, err := model.ClaimAuditSecurity(ctx, db, owner)
			require.NoError(t, err)
			_, err = model.ClaimAuditSecurity(ctx, db, "competitor")
			require.ErrorIs(t, err, model.ErrSecurityConflict)
			now := time.Now().Unix()
			prefix := "fixture-" + name + "-" + time.Now().Format("150405.000000000")
			e := Normalize(model.AuditLog{EventId: prefix, UserId: 946128, ActorRole: 10, Action: "channel.key_view", Success: true, CreatedAt: now, Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"id": 9}}}})
			cfg := operation_setting.DefaultAuditSecuritySetting()
			cfg.NotificationEnabled = true
			cfg.NotificationEmails = "security@example.test"
			next := state
			next.CursorAt = now
			next.CursorEvent = prefix
			require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{e}, next, cfg))
			require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{e}, next, cfg))
			var alerts []model.SecurityAlert
			require.NoError(t, db.Where("actor_id = ?", e.ActorID).Find(&alerts).Error)
			require.Len(t, alerts, 1)
			a := alerts[0]
			require.EqualValues(t, 1, a.Occurrences)
			t.Cleanup(func() {
				db.Where("alert_id = ?", a.ID).Delete(&model.SecurityAction{})
				db.Where("alert_id = ?", a.ID).Delete(&model.SecurityDelivery{})
				db.Where("actor_id = ?", e.ActorID).Delete(&model.SecurityAlert{})
				db.Where("actor_id = ?", e.ActorID).Delete(&model.SecurityEvent{})
				db.Where("name = ? AND owner = ?", "scanner", owner).Delete(&model.SecurityState{})
			})
			detail, err := GetAlert(ctx, db, 10, a.ID)
			require.NoError(t, err)
			require.Len(t, detail.Events, 1)
			require.Len(t, detail.Deliveries, 1)
			require.NoError(t, ActOnAlert(ctx, db, 10, 5, a.ID, a.Version, "investigating", "checking"))
			require.NoError(t, db.Create(&model.SecurityAction{AlertID: a.ID, ActorID: 1, ActorRole: 100, Action: "investigating", Note: "root-private-note", CreatedAt: now}).Error)
			adminDetail, err := GetAlert(ctx, db, 10, a.ID)
			require.NoError(t, err)
			require.Len(t, adminDetail.Actions, 1)
			rootDetail, err := GetAlert(ctx, db, 100, a.ID)
			require.NoError(t, err)
			require.Len(t, rootDetail.Actions, 2)
			require.ErrorIs(t, ActOnAlert(ctx, db, 10, 5, a.ID, a.Version, "resolved", "stale"), model.ErrSecurityConflict)
			// Root and unknown actors cannot leak through detail, list or mutation.
			require.NoError(t, db.Model(&model.SecurityAlert{}).Where("id = ?", a.ID).Update("visibility", 100).Error)
			_, err = GetAlert(ctx, db, 10, a.ID)
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			page, err := ListAlerts(ctx, db, 10, AlertFilter{Start: now - 1, End: now + 1, Limit: 50})
			require.NoError(t, err)
			require.Empty(t, page.Items)
			require.ErrorIs(t, ActOnAlert(ctx, db, 10, 5, a.ID, a.Version+1, "resolved", "hidden"), model.ErrSecurityConflict)
			newState, err := model.ClaimAuditSecurity(ctx, db, owner)
			require.NoError(t, err)
			require.Greater(t, newState.Fence, state.Fence)
			e.EventID += "-stale"
			require.ErrorIs(t, Apply(ctx, db, state, []model.SecurityEvent{e}, next, cfg), model.ErrSecurityConflict)
			var receipts int64
			require.NoError(t, db.Model(&model.SecurityEvent{}).Where("event_id = ?", e.EventID).Count(&receipts).Error)
			require.Zero(t, receipts)
		})
	}
}

func TestNotificationRejectsPrivateDestinations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := publicDial(ctx, "tcp", "127.0.0.1:80")
	require.Error(t, err)
	_, err = publicDial(ctx, "tcp", "[::1]:80")
	require.Error(t, err)
	_, err = publicDial(ctx, "tcp", "100.64.0.1:80")
	require.Error(t, err)
	_, err = publicDial(ctx, "tcp", "198.18.0.1:80")
	require.Error(t, err)
}

func TestScannerPreservesCommittedAnalystAction(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		for _, conflicts := range []int{0, 1, 3} {
			if dialect == "sqlite" && conflicts > 0 {
				continue
			} // SQLite serializes these writers before the read.
			t.Run(dialect+"/conflicts="+string(rune('0'+conflicts)), func(t *testing.T) {
				var driver gorm.Dialector = sqlite.Open(filepath.Join(t.TempDir(), "conflict.db"))
				if dialect != "sqlite" {
					env := "AUDIT_TEST_MYSQL_DSN"
					if dialect == "postgres" {
						env = "AUDIT_TEST_PG_DSN"
					}
					dsn := os.Getenv(env)
					if dsn == "" {
						t.Skip("isolated audit DB required")
					}
					require.Contains(t, dsn, "/audit_test")
					if dialect == "mysql" {
						driver = mysql.Open(dsn)
					} else {
						driver = postgres.Open(dsn)
					}
				}
				db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
				require.NoError(t, err)
				pool, err := db.DB()
				require.NoError(t, err)
				pool.SetMaxOpenConns(4)
				t.Cleanup(func() { require.NoError(t, pool.Close()) })
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				require.NoError(t, model.MigrateAuditSecurity(ctx, db))
				owner := common.NewRequestId()
				state, err := model.ClaimAuditSecurity(ctx, db, owner)
				require.NoError(t, err)
				e := Normalize(model.AuditLog{EventId: common.NewRequestId(), UserId: 946132, ActorRole: common.RoleAdminUser, CreatedAt: time.Now().Unix(), Action: "channel.key_view", Success: true})
				e.SubjectRef = "fixture:" + owner
				a := model.SecurityAlert{Rule: "sensitive_operation", SubjectRef: e.SubjectRef, Visibility: e.Visibility, ActorID: e.ActorID, ResourceType: e.ResourceType, FirstSeen: e.At, LastSeen: e.At, Version: 1, Status: "open", Severity: 2, RuleVersion: 3}
				require.NoError(t, db.Create(&a).Error)
				t.Cleanup(func() {
					db.Where("alert_id = ?", a.ID).Delete(&model.SecurityAction{})
					db.Where("alert_id = ?", a.ID).Delete(&model.SecurityDelivery{})
					db.Where("id = ?", a.ID).Delete(&model.SecurityAlert{})
					db.Where("subject_ref = ?", e.SubjectRef).Delete(&model.SecurityEvent{})
					db.Where("owner = ?", owner).Delete(&model.SecurityState{})
				})
				var fired atomic.Int32
				if conflicts == 0 {
					require.NoError(t, ActOnAlert(ctx, db, common.RoleAdminUser, 43, a.ID, a.Version, "investigating", "review"))
				} else {
					var inAction atomic.Bool
					require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:analyst-action", func(tx *gorm.DB) {
						alert, ok := tx.Statement.Dest.(*model.SecurityAlert)
						if !ok || alert.ID != a.ID || fired.Load() >= int32(conflicts) || !inAction.CompareAndSwap(false, true) {
							return
						}
						defer inAction.Store(false)
						fired.Add(1)
						require.NoError(t, ActOnAlert(ctx, db, common.RoleAdminUser, 43, alert.ID, alert.Version, "investigating", "concurrent review"))
					}))
					t.Cleanup(func() { db.Callback().Query().Remove("test:analyst-action") })
				}
				cfg := operation_setting.DefaultAuditSecuritySetting()
				cfg.NotificationEnabled = true
				cfg.NotificationEmails = "fixture@example.test"
				next := state
				next.CursorAt = e.At
				next.CursorEvent = e.EventID
				events := []model.SecurityEvent{e}
				if conflicts == 3 {
					// Commit work earlier in the batch before the conflicting event,
					// so rollback also has to remove an alert and queued notification.
					earlier := e
					earlier.EventID += "-earlier"
					earlier.SubjectRef += "-earlier"
					events = []model.SecurityEvent{earlier, e}
					t.Cleanup(func() {
						var alertIDs []int64
						db.Model(&model.SecurityAlert{}).Where("subject_ref = ?", earlier.SubjectRef).Pluck("id", &alertIDs)
						if len(alertIDs) > 0 {
							db.Where("alert_id IN ?", alertIDs).Delete(&model.SecurityDelivery{})
							db.Where("id IN ?", alertIDs).Delete(&model.SecurityAlert{})
						}
						db.Where("event_id = ?", earlier.EventID).Delete(&model.SecurityEvent{})
					})
				}
				err = Apply(ctx, db, state, events, next, cfg)
				if conflicts == 3 {
					require.ErrorIs(t, err, model.ErrSecurityConflict)
				} else {
					require.NoError(t, err)
				}
				var stored model.SecurityAlert
				require.NoError(t, db.First(&stored, a.ID).Error)
				require.Equal(t, "investigating", stored.Status)
				var receipts, deliveries, actions int64
				require.NoError(t, db.Model(&model.SecurityEvent{}).Where("event_id = ?", e.EventID).Count(&receipts).Error)
				require.NoError(t, db.Model(&model.SecurityDelivery{}).Where("alert_id = ?", a.ID).Count(&deliveries).Error)
				require.NoError(t, db.Model(&model.SecurityAction{}).Where("alert_id = ?", a.ID).Count(&actions).Error)
				var checkpoint model.SecurityState
				require.NoError(t, db.Where("owner = ?", owner).Take(&checkpoint).Error)
				if conflicts == 3 {
					require.EqualValues(t, 4, stored.Version)
					require.Zero(t, receipts)
					require.Zero(t, deliveries)
					require.Equal(t, state.CursorEvent, checkpoint.CursorEvent)
					require.Equal(t, state.CommitSeq, checkpoint.CommitSeq)
					var earlierReceipts, earlierAlerts int64
					require.NoError(t, db.Model(&model.SecurityEvent{}).Where("event_id = ?", events[0].EventID).Count(&earlierReceipts).Error)
					require.NoError(t, db.Model(&model.SecurityAlert{}).Where("subject_ref = ?", events[0].SubjectRef).Count(&earlierAlerts).Error)
					require.Zero(t, earlierReceipts)
					require.Zero(t, earlierAlerts)
				} else {
					require.EqualValues(t, 3, stored.Version)
					require.EqualValues(t, 1, receipts)
					require.EqualValues(t, 1, deliveries)
					require.Equal(t, e.EventID, checkpoint.CursorEvent)
					// Preserve the existing contract: a new event reopens a resolved episode.
					require.NoError(t, ActOnAlert(ctx, db, common.RoleAdminUser, 43, a.ID, stored.Version, "resolved", "done"))
					e.EventID += "-reopen"
					t.Cleanup(func() { db.Where("event_id = ?", e.EventID).Delete(&model.SecurityEvent{}) })
					require.NoError(t, Apply(ctx, db, state, []model.SecurityEvent{e}, next, cfg))
					require.NoError(t, db.First(&stored, a.ID).Error)
					require.Equal(t, "open", stored.Status)
					require.EqualValues(t, 5, stored.Version)
				}
				require.EqualValues(t, max(1, conflicts), actions)
				require.EqualValues(t, conflicts, fired.Load())
			})
		}
	}
}

func TestDuplicateTriggerEvidenceCollapses(t *testing.T) {
	trigger := model.SecurityEvent{EventID: "evt-1", At: 10, Action: "login", IP: "8.8.8.8"}
	stored := appendTriggerEvidence([]model.SecurityEvent{{EventID: "other", At: 9}, trigger}, trigger, 20)
	require.Len(t, stored, 2)
	require.Equal(t, "evt-1", stored[1].EventID)
	trimmed := appendTriggerEvidence([]model.SecurityEvent{trigger, {EventID: "tail", At: 11}}, trigger, 2)
	require.Len(t, trimmed, 2)
	require.Equal(t, "tail", trimmed[0].EventID)
	require.Equal(t, "evt-1", trimmed[1].EventID)
	require.Len(t, appendTriggerEvidence([]model.SecurityEvent{{EventID: ""}}, model.SecurityEvent{At: 3}, 20), 2)
	collapsed := collapseEvidenceEvents([]model.SecurityEvent{
		{EventID: "evt-1", Facts: "older"},
		{EventID: "evt-1", Facts: "newer"},
		{EventID: ""},
		{EventID: ""},
	})
	require.Len(t, collapsed, 3)
	require.Equal(t, "newer", collapsed[0].Facts)

	db := newWorkerTestDB(t)
	raw, err := common.Marshal([]model.SecurityEvent{
		{EventID: "evt-dup", At: 50, Action: "login", IP: "8.8.8.8", Success: true},
		{EventID: "evt-dup", At: 50, Action: "login", IP: "8.8.8.8", Success: true, Facts: `{"reason":"later"}`},
	})
	require.NoError(t, err)
	alert := model.SecurityAlert{Rule: "ip_abuse", Visibility: 100, Status: "open", Version: 1, Severity: 2, Evidence: string(raw), FirstSeen: 50, LastSeen: 50}
	require.NoError(t, db.Create(&alert).Error)
	require.NoError(t, db.Create(&model.SecurityAlertEvent{EventID: "evt-dup", AlertID: alert.ID, EventAt: 50, IP: "8.8.8.8"}).Error)
	detail, err := GetAlert(t.Context(), db, 100, alert.ID)
	require.NoError(t, err)
	require.Len(t, detail.Events, 1)
	require.Contains(t, detail.Events[0].Facts, "later")
	require.Len(t, detail.IPMarkers, 1)
	require.NotEqual(t, "none", detail.IPMarkers[0].State)
}

func TestHistoricalEvidenceDeduplicatesBeforeAccountViewLimits(t *testing.T) {
	db, filter := customerAccessFixture(t)
	for _, count := range []int{1, 21} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			events := make([]model.SecurityEvent, 0, count*2)
			for i := range count {
				event := model.SecurityEvent{EventID: fmt.Sprintf("duplicate-%d", i), Action: "login", At: filter.End - int64(i), ActorID: 3, ActorRole: 1, IP: "192.0.2.3", Success: true}
				events = append(events, event, event)
			}
			raw, err := common.Marshal(events)
			require.NoError(t, err)
			alert := model.SecurityAlert{Rule: "ip_abuse", RuleVersion: 2, ActorID: 3, ActorRole: 1, Visibility: 100, Status: "open", Version: 1, FirstSeen: filter.Start, LastSeen: filter.End, Evidence: string(raw)}
			require.NoError(t, db.Create(&alert).Error)
			admin, err := GetAlert(t.Context(), db, 100, alert.ID)
			require.NoError(t, err)
			require.Len(t, admin.Events, count)
			self, err := GetSelfSecurityAlert(t.Context(), db, 3, alert.ID)
			require.NoError(t, err)
			customer, err := GetCustomerAlert(t.Context(), db, 1, 3, alert.ID)
			require.NoError(t, err)
			require.Len(t, self.Events, min(count, 20))
			require.Len(t, customer.Events, min(count, 20))
			for i := range min(count, 20) {
				require.Equal(t, filter.End-int64(i), self.Events[i].At)
				require.Equal(t, fmt.Sprintf("duplicate-%d", i), customer.Events[i].EventID)
			}
		})
	}
}

func TestSourceFindingsRetainObservedBaselineTimes(t *testing.T) {
	for _, action := range []string{"login", "token.create"} {
		t.Run(action, func(t *testing.T) {
			db := newWorkerTestDB(t)
			state, err := model.ClaimAuditSecurity(t.Context(), db, "source-timing")
			require.NoError(t, err)
			now := time.Now().Unix()
			cfg := operation_setting.DefaultAuditSecuritySetting()
			log := model.AuditLog{UserId: 31, ActorRole: 1, Action: action, Success: true, Ip: "8.8.8.8", Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"id": 7}}}}
			log.EventId, log.CreatedAt = "known-first", now-2*86400
			first := Normalize(log)
			log.EventId, log.CreatedAt = "known-recent", now-100
			recent := Normalize(log)
			require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{first, recent}, state, cfg))
			log.Ip = "8.8.4.4"
			newFirstSeen := now
			rule := "new_login_source"
			if action == "token.create" {
				newFirstSeen = now - 60
				rule = "new_source_sensitive_operation"
				log.EventId, log.CreatedAt = "source-first", newFirstSeen
				require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{Normalize(log)}, state, cfg))
			}
			log.EventId, log.CreatedAt = "source-trigger", now
			require.NoError(t, Apply(t.Context(), db, state, []model.SecurityEvent{Normalize(log)}, state, cfg))
			var finding model.SecurityFindingSnapshot
			require.NoError(t, db.Where("rule = ? AND trigger_event_id = ?", rule, log.EventId).Take(&finding).Error)
			require.EqualValues(t, newFirstSeen, finding.Dimensions["source_first_seen"])
			require.EqualValues(t, first.At, finding.Dimensions["baseline_first_seen"])
			require.EqualValues(t, recent.At, finding.Dimensions["baseline_last_seen"])
			// Later observations must never rewrite the already retained proof.
			require.NoError(t, db.Model(&model.SecurityBaseline{}).Where("account_id = ?", 31).Update("last_seen", now+600).Error)
			detail, err := GetAlert(t.Context(), db, common.RoleRootUser, finding.AlertID)
			require.NoError(t, err)
			require.Equal(t, finding.Dimensions, detail.Findings[0].Dimensions)
		})
	}
}
