package auditsecurity

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// These two schemas are the pre-response version. They intentionally omit the
// new fields/indexes so the upgrade test exercises existing populated tables.
type previousSecurityAction struct {
	ID         int64 `gorm:"index:idx_sec_action_cursor,priority:3"`
	AlertID    int64 `gorm:"index:idx_sec_action_alert,priority:1;index:idx_sec_action_cursor,priority:1"`
	ActorID    int
	ActorRole  int
	Action     string `gorm:"type:varchar(32)"`
	Note       string `gorm:"type:text"`
	CreatedAt  int64  `gorm:"index:idx_sec_action_alert,priority:2;index;index:idx_sec_action_cursor,priority:2"`
	Source     string `gorm:"type:varchar(16)"`
	FromStatus string `gorm:"type:varchar(16)"`
	ToStatus   string `gorm:"type:varchar(16)"`
	EventID    string `gorm:"type:varchar(64)"`
	EventAt    int64
	DetectedAt int64
	AssigneeID int
	Conclusion string `gorm:"type:varchar(32)"`
}

func (previousSecurityAction) TableName() string { return "audit_security_actions" }

type previousSecurityBaseline struct {
	ID        int64
	AccountID int    `gorm:"uniqueIndex:idx_sec_baseline,priority:1"`
	SourceRef string `gorm:"type:varchar(64);uniqueIndex:idx_sec_baseline,priority:2"`
	FirstSeen int64
	LastSeen  int64 `gorm:"index"`
}

func (previousSecurityBaseline) TableName() string { return "audit_security_baselines" }

type migrationDDLLog struct {
	logger.Interface
	statements []string
}

func (l *migrationDDLLog) Trace(_ context.Context, _ time.Time, query func() (string, int64), _ error) {
	sql, _ := query()
	upper := strings.ToUpper(strings.TrimSpace(sql))
	if strings.HasPrefix(upper, "ALTER ") || strings.HasPrefix(upper, "CREATE ") || strings.HasPrefix(upper, "DROP ") {
		l.statements = append(l.statements, sql)
	}
}

func TestAccountResponseMigrationFreshUpgradeAndRestart(t *testing.T) {
	db := newWorkerTestDB(t) // Always an isolated disposable database owned by this test.
	require.False(t, db.Migrator().HasColumn("audit_security_actions", "affected_count"))
	require.False(t, db.Migrator().HasColumn("audit_security_alerts", "responses"))
	ctx := context.Background()
	for _, upgrade := range []bool{false, true} {
		if upgrade {
			require.NoError(t, db.Migrator().DropTable(&model.SecurityAction{}, &model.SecurityBaseline{}))
			require.NoError(t, db.AutoMigrate(&previousSecurityAction{}, &previousSecurityBaseline{}))
			require.NoError(t, db.Create(&[]previousSecurityAction{{AlertID: 3, Action: "investigating", Note: "retained"}, {AlertID: 3, Action: "resolved"}}).Error)
			require.NoError(t, db.Create(&previousSecurityBaseline{ID: 1, AccountID: 3, SourceRef: "known", FirstSeen: 10, LastSeen: 20}).Error)
			require.NoError(t, model.MigrateAuditSecurity(ctx, db))
			var actions []model.SecurityAction
			require.NoError(t, db.Order("id").Find(&actions).Error)
			require.Len(t, actions, 2)
			require.Nil(t, actions[0].ExecutionKey)
			require.Nil(t, actions[1].ExecutionKey)
			require.Equal(t, "retained", actions[0].Note)
			var baseline model.SecurityBaseline
			require.NoError(t, db.First(&baseline).Error)
			require.EqualValues(t, 10, baseline.FirstSeen)
			require.Empty(t, baseline.Kind)
		}
		for range 2 {
			log := &migrationDDLLog{Interface: logger.Default.LogMode(logger.Silent)}
			require.NoError(t, model.MigrateAuditSecurity(ctx, db.Session(&gorm.Session{Logger: log})))
			require.Empty(t, log.statements, "restart must not repeat DDL; upgrade=%v", upgrade)
		}
	}
	key := "one-response-only"
	require.NoError(t, db.Create(&model.SecurityAction{ExecutionKey: &key, AlertID: 3, Action: "disable_account", Outcome: "pending"}).Error)
	require.Error(t, db.Create(&model.SecurityAction{ExecutionKey: &key, AlertID: 3, Action: "disable_account", Outcome: "pending"}).Error)
}

func TestAccountResponseQueryPlans(t *testing.T) {
	db := newWorkerTestDB(t)
	var actions []model.SecurityAction
	var events []model.SecurityEvent
	var baselines []model.SecurityBaseline
	for i := range 2000 {
		outcome := "succeeded"
		if i == 1999 {
			outcome = "pending"
		}
		actions = append(actions, model.SecurityAction{AlertID: int64(i + 1), Action: "disable_account", Outcome: outcome, NextAttemptAt: int64(i), AccountNotifyPending: i == 1998})
		events = append(events, model.SecurityEvent{EventID: fmt.Sprint("plan-", i), ActorID: i + 1, IP: fmt.Sprintf("8.%d.%d.1", i/255, i%255), At: 1000})
		baselines = append(baselines, model.SecurityBaseline{AccountID: i + 1, Kind: "management", SourceRef: fmt.Sprint(i), FirstSeen: 100, LastSeen: 1000})
	}
	require.NoError(t, db.CreateInBatches(actions, 100).Error)
	require.NoError(t, db.CreateInBatches(events, 100).Error)
	require.NoError(t, db.CreateInBatches(baselines, 100).Error)
	if db.Dialector.Name() == "postgres" {
		for _, table := range []string{"audit_security_actions", "audit_security_events", "audit_security_baselines"} {
			require.NoError(t, db.Exec("ANALYZE "+table).Error)
		}
	}
	queries := []struct {
		name  string
		query *gorm.DB
	}{
		{"due", db.Model(&model.SecurityAction{}).Where("outcome IN ? AND next_attempt_at <= ?", []string{"pending", "committed", "sync_pending", "clearing"}, 3000).Order("next_attempt_at ASC").Order("id ASC").Limit(5)},
		{"personal_notify", db.Model(&model.SecurityAction{}).Select("id,alert_id,target_user_id").Where("account_notify_pending = ? AND outcome NOT IN ?", true, []string{"pending", "committed", "sync_pending", "clearing"}).Order("id").Limit(20)},
		{"personal_cooldown", db.Model(&model.SecurityDelivery{}).Select("id,status,created_at,sent_at,destination").Where("alert_id = ? AND recipient_user_id = ? AND created_at >= ?", 3, 3, 100).Order("created_at DESC").Limit(100)},
		{"personal_last_sent", db.Model(&model.SecurityDelivery{}).Select("sent_at").Where("alert_id = ? AND recipient_user_id = ? AND sent_at > ?", 3, 3, 100).Order("sent_at DESC").Limit(1)},
		{"manual_assignee", db.Model(&model.SecurityAssignee{}).Where("user_id = ?", 3)},
		{"latest_response", db.Model(&model.SecurityAction{}).Select("MAX(id)").Where("alert_id IN ? AND action IN ?", []int{3, 4}, []string{"disable_account", "clear_api_keys", "clear_access_tokens"}).Group("alert_id, action")},
		{"actor_window", db.Model(&model.SecurityEvent{}).Select(model.SecurityEventColumns).Where("actor_id = ? AND at >= ? AND at <= ?", 3, 400, 1000).Order("at DESC").Limit(1001)},
		{"ip_window", db.Model(&model.SecurityEvent{}).Select(model.SecurityEventColumns).Where("ip = ? AND at >= ? AND at <= ?", "8.0.2.1", 400, 1000).Order("at DESC").Limit(1001)},
		{"management_baseline", db.Model(&model.SecurityBaseline{}).Select("id").Where("account_id = ? AND kind = ? AND first_seen <= ? AND last_seen >= ? AND source_ref <> ?", 3, "management", 900, 100, "new").Limit(1)},
	}
	for _, test := range queries {
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
				if bytes, ok := value.([]byte); ok {
					row[key] = string(bytes)
				}
			}
		}
		t.Logf("%s %s: %v", db.Dialector.Name(), test.name, plan)
	}
}
