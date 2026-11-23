package auditsecurity

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCaptureSourceScanWithoutHistoricalBackfill(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		kind      common.DatabaseType
	}{{"sqlite", "", common.DatabaseTypeSQLite}, {"mysql", "AUDIT_TEST_MYSQL_DSN", common.DatabaseTypeMySQL}, {"postgres", "AUDIT_TEST_PG_DSN", common.DatabaseTypePostgreSQL}, {"clickhouse", "AUDIT_TEST_CH_DSN", common.DatabaseTypeClickHouse}} {
		t.Run(tc.name, func(t *testing.T) {
			dsn := os.Getenv(tc.env)
			if tc.env != "" && dsn == "" {
				t.Skip("isolated database not configured")
			}
			if tc.env != "" {
				require.Contains(t, dsn, "/audit_test")
			} else {
				dsn = "local"
				oldPath := common.SQLitePath
				common.SQLitePath = filepath.Join(t.TempDir(), "source.db")
				t.Cleanup(func() { common.SQLitePath = oldPath })
			}
			testSourceLifecycle(t, dsn, tc.kind)
		})
	}
}

func TestScanIntervalChangesWithoutRestart(t *testing.T) {
	for _, capture := range []bool{false, true} {
		t.Run(fmt.Sprintf("capture=%t", capture), func(t *testing.T) {
			db := newWorkerTestDB(t)
			previous := primary.Load()
			primary.Store(db)
			t.Cleanup(func() { primary.Store(previous) })
			w := worker{db: db, source: db, writer: db, owner: "interval-test", lastScanAttempt: time.Now().Add(-30 * time.Second)}
			cfg := operation_setting.DefaultAuditSecuritySetting()
			cfg.Enabled, cfg.CaptureEnabled, cfg.ScanIntervalSeconds = true, capture, 60
			for _, seconds := range []int{60, 5} {
				cfg.ScanIntervalSeconds = seconds
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				done := make(chan struct{})
				w.cycle(ctx, cancel, done, cfg)
				<-done
				var states int64
				require.NoError(t, db.Model(&model.SecurityState{}).Where("name <> ?", "source_start").Count(&states).Error)
				if seconds == 60 {
					require.Zero(t, states, "capture polling must not override the scan interval")
				} else {
					require.EqualValues(t, 1, states, "the due cycle initializes the live scan cursor")
				}
				var boundary model.SecurityState
				require.NoError(t, db.Where("name = ?", "source_start").Take(&boundary).Error)
				require.Positive(t, boundary.CursorAt, "coverage starts before capture, independently of scan scheduling")
			}
		})
	}

}

func testSourceLifecycle(t *testing.T, dsn string, kind common.DatabaseType) {
	t.Setenv("LOG_SQL_DSN", dsn)
	source, err := model.OpenAuditSecurityDB(t.Context(), true)
	require.NoError(t, err)
	pool, err := source.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	oldDB, oldType := model.LOG_DB, common.LogDatabaseType()
	model.LOG_DB = source
	common.SetLogDatabaseType(kind)
	t.Cleanup(func() { model.LOG_DB = oldDB; common.SetLogDatabaseType(oldType) })
	require.NoError(t, model.MigrateAuditLogs())
	db := newWorkerTestDB(t)
	ctx := context.Background()
	require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"capture_enabled": "true", "suspended": "false"}))
	t.Cleanup(func() {
		_ = config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"capture_enabled": "false"})
	})
	owner := "clickhouse-fixture"
	w := worker{db: db, source: source, writer: source, owner: owner}
	cfg := operation_setting.DefaultAuditSecuritySetting()
	state, err := model.ClaimAuditSecurity(ctx, db, owner)
	require.NoError(t, err)
	require.NoError(t, w.scan(ctx, state, cfg))
	id := common.NewRequestId()
	sourceIDs := []string{id}
	t.Cleanup(func() {
		if kind == common.DatabaseTypeClickHouse {
			require.NoError(t, source.Exec("ALTER TABLE audit_logs DELETE WHERE event_id IN ?", sourceIDs).Error)
		} else {
			require.NoError(t, source.Where("event_id IN ?", sourceIDs).Delete(&model.AuditLog{}).Error)
		}
	})
	now := time.Now().Unix()
	model.QueueSecurityAudit(model.AuditLog{EventId: id, UserId: 774198, ActorRole: 100, Action: "channel.key_view", CreatedAt: now, Success: true, Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"id": 17}}}})
	require.NoError(t, w.capture(ctx))
	state, err = model.ClaimAuditSecurity(ctx, db, owner)
	require.NoError(t, err)
	require.NoError(t, w.scan(ctx, state, cfg))
	var receipt model.SecurityEvent
	require.NoError(t, db.Where("event_id = ?", id).Take(&receipt).Error)
	require.Equal(t, 17, receipt.TargetID)
	lateID := common.NewRequestId()
	sourceIDs = append(sourceIDs, lateID)
	model.QueueSecurityAudit(model.AuditLog{EventId: lateID, UserId: 774198, ActorRole: 100, Action: "channel.delete", CreatedAt: now - 600, Success: true})
	require.NoError(t, w.capture(ctx))
	state, err = model.ClaimAuditSecurity(ctx, db, owner)
	require.NoError(t, err)
	require.NoError(t, w.scan(ctx, state, cfg))
	var count int64
	require.NoError(t, db.Model(&model.SecurityEvent{}).Where("event_id IN ?", []string{id, lateID}).Count(&count).Error)
	require.EqualValues(t, 1, count, "late insertion of pre-enable history must not trigger historical processing")
	for _, value := range []string{strings.Repeat("x", 20000), strings.Repeat("\u754c", 6000)} {
		largeID := common.NewRequestId()
		sourceIDs = append(sourceIDs, largeID)
		model.QueueSecurityAudit(model.AuditLog{EventId: largeID, UserId: 774198, ActorRole: 100, Action: "channel.update", CreatedAt: now, Success: true, Other: model.AuditOther{Op: &model.AuditOperation{Params: model.AuditFields{"untrusted_large_value": value}}}})
		require.NoError(t, w.capture(ctx))
		state, err = model.ClaimAuditSecurity(ctx, db, owner)
		require.NoError(t, err)
		require.NoError(t, w.scan(ctx, state, cfg))
		var limited model.SecurityEvent
		require.NoError(t, db.Where("event_id = ?", largeID).Take(&limited).Error)
		require.Contains(t, limited.Facts, `"evidence_incomplete":true`, "the metadata budget is measured in bytes, not characters")
	}
	// The display budget is independent of the detection budget. Exercise the
	// persisted source query, not just Normalize with an already decoded row.
	changes := model.NewAuditChanges()
	channelIDs := make([]int, 30)
	for i := range channelIDs {
		channelIDs[i] = i + 1
		changes.Add(model.AuditTarget("channel", i+1, "fixture"), model.AuditFields{"models": "old"}, model.AuditFields{"models": strings.Repeat("model,", 170)})
	}
	scanned := make(map[string]model.AuditLog)
	require.NoError(t, source.Callback().Query().After("gorm:query").Register("test:source-metadata", func(tx *gorm.DB) {
		if rows, ok := tx.Statement.Dest.(*[]model.AuditLog); ok {
			for _, row := range *rows {
				scanned[row.EventId] = row
			}
		}
	}))
	t.Cleanup(func() { source.Callback().Query().Remove("test:source-metadata") })
	for _, tc := range []struct {
		actor        int
		action       string
		params       model.AuditFields
		admin        *model.AuditAdminInfo
		changes      *model.AuditChanges
		wantAlerts   int
		wantCount    int
		wantTargetID int
	}{
		{774200, "channel.token_update", model.AuditFields{"changed_fields": []string{"models"}, "ids": channelIDs, "count": 30}, nil, changes, 0, 30, 0},
		{774201, "channel.token_update", model.AuditFields{"changed_fields": []string{"key"}, "ids": channelIDs, "count": 30}, &model.AuditAdminInfo{AdminID: 774211, AdminRole: common.RoleRootUser}, changes, 2, 30, 0},
		{774202, "channel.update", model.AuditFields{"changed_fields": []string{"key"}, "id": 17}, nil, changes, 1, 1, 17},
		{774203, "channel.update", model.AuditFields{"changed_fields": []string{"key"}, "id": 18}, nil, nil, 1, 1, 18},
	} {
		entry := model.AuditLog{EventId: common.NewRequestId(), UserId: tc.actor, ActorRole: common.RoleAdminUser, Action: tc.action, CreatedAt: now, Success: true, Other: model.AuditOther{Op: &model.AuditOperation{Params: tc.params}, AdminInfo: tc.admin, Changes: tc.changes}}
		if tc.changes != nil {
			raw, err := common.Marshal(entry.Other)
			require.NoError(t, err)
			require.Greater(t, len(raw), 16384)
		}
		sourceIDs = append(sourceIDs, entry.EventId)
		model.QueueSecurityAudit(entry)
		require.NoError(t, w.capture(ctx))
		state, err = model.ClaimAuditSecurity(ctx, db, owner)
		require.NoError(t, err)
		require.NoError(t, w.scan(ctx, state, cfg))
		actual, ok := scanned[entry.EventId]
		require.True(t, ok)
		require.Nil(t, actual.Other.Changes, "display values must not be transferred to the scanner")
		expected := Normalize(entry)
		require.Equal(t, expected, Normalize(actual), "source projection must preserve every detection fact")
		var alerts []model.SecurityAlert
		require.NoError(t, db.Where("actor_id = ?", expected.ActorID).Find(&alerts).Error)
		require.Len(t, alerts, tc.wantAlerts)
		for _, alert := range alerts {
			require.Equal(t, tc.wantCount, alert.MatchedCount)
			require.Equal(t, expected.Visibility, alert.Visibility)
		}
		if tc.wantAlerts > 0 {
			var event model.SecurityEvent
			require.NoError(t, db.Where("event_id = ?", entry.EventId).Take(&event).Error)
			require.Equal(t, tc.wantCount, event.Count)
			require.Equal(t, tc.wantTargetID, event.TargetID)
			var expectedFacts, storedFacts Facts
			require.NoError(t, common.UnmarshalJsonStr(expected.Facts, &expectedFacts))
			require.NoError(t, common.UnmarshalJsonStr(event.Facts, &storedFacts))
			require.GreaterOrEqual(t, storedFacts.FirstObservedAt, now)
			storedFacts.FirstObservedAt = 0
			require.Equal(t, expectedFacts, storedFacts)
		}
	}
	require.NoError(t, source.Callback().Query().Remove("test:source-metadata"))
	// More than one page at the same timestamp must resume without dropping a
	// PAT denial or consuming the whole backlog in one scan interval.
	pageAt := now + 1
	pageIDs := make([]string, 501)
	for i := range pageIDs {
		pageIDs[i] = fmt.Sprintf("%s-%03d", id, i)
		sourceIDs = append(sourceIDs, pageIDs[i])
		entry := model.AuditLog{EventId: pageIDs[i], UserId: 774199, ActorRole: 10, Action: "access_token.request", CreatedAt: pageAt, Success: true}
		if i >= 491 {
			entry.Category, entry.Status, entry.TokenRef, entry.Success = model.AuditCategoryAccessToken, 403, id, false
		}
		model.QueueSecurityAudit(entry)
	}
	for range 3 {
		require.NoError(t, w.capture(ctx))
	}
	// Keep the plan check meaningful: old audit volume must be excluded by
	// the time index, rather than letting a tiny all-matching table hide a scan.
	if kind != common.DatabaseTypeClickHouse {
		oldRequest := "old-" + id
		t.Cleanup(func() { require.NoError(t, source.Where("request_id = ?", oldRequest).Delete(&model.AuditLog{}).Error) })
		oldRows := make([]model.AuditLog, 10050)
		for i := range oldRows {
			oldRows[i] = model.AuditLog{EventId: fmt.Sprintf("%s-%05d", oldRequest, i), CreatedAt: pageAt - 864000, RequestId: oldRequest}
		}
		require.NoError(t, source.CreateInBatches(oldRows, 200).Error)
	}
	state, err = model.ClaimAuditSecurity(ctx, db, owner)
	require.NoError(t, err)
	state.CursorAt, state.CursorEvent, state.WindowEnd = pageAt, "", pageAt
	reads := 0
	var sourceSQL string
	require.NoError(t, source.Callback().Query().After("gorm:query").Register("test:source-budget", func(tx *gorm.DB) {
		if rows, ok := tx.Statement.Dest.(*[]model.AuditLog); ok {
			reads += len(*rows)
			sourceSQL = tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
		}
	}))
	t.Cleanup(func() { source.Callback().Query().Remove("test:source-budget") })
	require.NoError(t, w.scan(ctx, state, cfg))
	require.Equal(t, 500, reads, "one interval must not drain multiple source batches")
	explain := "EXPLAIN "
	switch kind {
	case common.DatabaseTypeSQLite:
		explain = "EXPLAIN QUERY PLAN "
	case common.DatabaseTypeClickHouse:
		explain = "EXPLAIN indexes = 1 "
	}
	var plan []map[string]any
	if kind == common.DatabaseTypePostgreSQL {
		require.NoError(t, source.Exec("ANALYZE audit_logs").Error)
	}
	require.NoError(t, source.Raw(explain+sourceSQL).Scan(&plan).Error)
	encoded, err := common.Marshal(plan)
	require.NoError(t, err)
	t.Logf("source plan: %s", encoded)
	if kind != common.DatabaseTypeClickHouse {
		require.Contains(t, strings.ToLower(string(encoded)), "idx_audit", "old source history must be excluded through an index")
	}
	state, err = model.ClaimAuditSecurity(ctx, db, owner)
	require.NoError(t, err)
	require.Equal(t, pageIDs[499], state.CursorEvent)
	require.Equal(t, pageAt, state.WindowEnd)
	require.Equal(t, state.CursorAt, Status().CursorAt)
	require.NoError(t, w.scan(ctx, state, cfg))
	require.Equal(t, 501, reads)
	var denied model.SecurityAlert
	require.NoError(t, db.Where("rule = ? AND subject_ref = ?", "permission_failures", "token:"+id).Take(&denied).Error)
	require.Equal(t, 10, denied.MatchedCount)
	require.NoError(t, db.Model(&model.SecurityEvent{}).Where("actor_id = ?", 774199).Count(&count).Error)
	require.EqualValues(t, 10, count, "ordinary PAT reads do not need monitoring receipts")
}

func TestAuditSourceEmployeeCompatibility(t *testing.T) {
	dsn := os.Getenv("AUDIT_TEST_CH_DSN")
	if dsn == "" {
		t.Skip("isolated ClickHouse required")
	}
	require.Contains(t, dsn, "/audit_test")
	t.Setenv("LOG_SQL_DSN", dsn)
	admin, err := model.OpenAuditSecurityDB(t.Context(), true)
	require.NoError(t, err)
	pool, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	for _, shape := range []string{"fresh", "missing", "UInt8"} {
		t.Run(shape, func(t *testing.T) {
			database := fmt.Sprintf("audit_test_employee_%d", time.Now().UnixNano())
			require.NoError(t, admin.Exec("CREATE DATABASE "+database).Error)
			t.Cleanup(func() { require.NoError(t, admin.Exec("DROP DATABASE "+database).Error) })
			u, err := url.Parse(dsn)
			require.NoError(t, err)
			u.Path = "/" + database
			t.Setenv("LOG_SQL_DSN", u.String())
			db, err := model.OpenAuditSecurityDB(t.Context(), true)
			require.NoError(t, err)
			p, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, p.Close()) })
			oldDB, oldKind := model.LOG_DB, common.LogDatabaseType()
			model.LOG_DB = db
			common.SetLogDatabaseType(common.DatabaseTypeClickHouse)
			t.Cleanup(func() { model.LOG_DB = oldDB; common.SetLogDatabaseType(oldKind) })
			require.NoError(t, model.MigrateAuditLogs())
			if shape == "missing" {
				require.NoError(t, db.Exec("ALTER TABLE audit_logs DROP COLUMN actor_is_employee").Error)
			} else if shape == "UInt8" {
				require.NoError(t, db.Exec("ALTER TABLE audit_logs MODIFY COLUMN actor_is_employee Nullable(UInt8)").Error)
			}
			require.NoError(t, model.MigrateAuditLogs())
			rec := &ddlRecorder{Interface: logger.Default.LogMode(logger.Silent)}
			model.LOG_DB = db.Session(&gorm.Session{Logger: rec})
			require.NoError(t, model.MigrateAuditLogs())
			require.Empty(t, rec.statements)
			require.NoError(t, config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"capture_enabled": "true", "suspended": "false"}))
			t.Cleanup(func() {
				_ = config.GlobalConfig.UpdateFromMap(operation_setting.AuditSecurityModule, map[string]string{"capture_enabled": "false"})
			})
			w := worker{writer: db}
			no, yes := false, true
			for i, employee := range []*bool{nil, &no, &yes} {
				for _, capture := range []bool{false, true} {
					id := fmt.Sprintf("employee-%d-%t", i, capture)
					entry := model.AuditLog{EventId: id, Username: "fixture", UserId: 97531, ActorRole: common.RoleCommonUser, ActorIsEmployee: employee, CreatedAt: time.Now().Unix(), Success: true}
					if capture {
						model.QueueSecurityAudit(entry)
						require.NoError(t, w.capture(context.Background()))
					} else {
						model.RecordAuditLog(nil, entry)
					}
					rows, total, err := model.QueryAuditLogs(db, model.AuditLogFilter{}, 0, 20, common.RoleRootUser)
					require.NoError(t, err)
					require.EqualValues(t, i*2+1+map[bool]int{false: 0, true: 1}[capture], total)
					found := false
					for _, row := range rows {
						if row.EventId == id {
							found = true
							require.Equal(t, employee, row.ActorIsEmployee)
						}
					}
					require.True(t, found)
				}
			}
		})
	}
}
