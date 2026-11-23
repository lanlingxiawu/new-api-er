package auditsecurity

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/logger"
)

type legacySecurityAlert struct {
	ID           int64
	Rule         string `gorm:"type:varchar(64);index:idx_sec_episode,priority:1"`
	SubjectRef   string `gorm:"type:varchar(96);index:idx_sec_episode,priority:2;index:idx_sec_alert_subject,priority:2"`
	Visibility   int    `gorm:"index:idx_sec_episode,priority:3;index:idx_sec_alert_time,priority:1;index:idx_sec_alert_status,priority:1;index:idx_sec_alert_resource,priority:1;index:idx_sec_alert_actor,priority:1;index:idx_sec_alert_subject,priority:1"`
	ActorID      int    `gorm:"index:idx_sec_alert_actor,priority:2"`
	ResourceType string `gorm:"type:varchar(16);index:idx_sec_alert_resource,priority:2"`
	Severity     int
	Status       string `gorm:"type:varchar(16);index:idx_sec_alert_status,priority:2"`
	FirstSeen    int64
	LastSeen     int64 `gorm:"index:idx_sec_episode,priority:4;index:idx_sec_alert_time,priority:2;index:idx_sec_alert_status,priority:3;index:idx_sec_alert_resource,priority:3;index:idx_sec_alert_actor,priority:3;index:idx_sec_alert_subject,priority:3;index"`
	Occurrences  int64
	MatchedCount int
	Version      int64
	Evidence     string `gorm:"type:text"`
	NotifiedAt   int64
	RuleVersion  int
}

func (legacySecurityAlert) TableName() string { return "audit_security_alerts" }

type legacySecurityAction struct {
	ID        int64
	AlertID   int64 `gorm:"index:idx_sec_action_alert,priority:1"`
	ActorID   int
	ActorRole int
	Action    string `gorm:"type:varchar(24)"`
	Note      string `gorm:"type:text"`
	CreatedAt int64  `gorm:"index:idx_sec_action_alert,priority:2;index"`
}

func (legacySecurityAction) TableName() string { return "audit_security_actions" }

type legacySecurityDelivery struct {
	ID            int64
	AlertID       int64 `gorm:"index"`
	Version       int64
	Destination   string `gorm:"type:text"`
	Kind          string `gorm:"type:varchar(16)"`
	Status        string `gorm:"type:varchar(16);index:idx_sec_delivery_due,priority:1"`
	Attempts      int
	NextAttemptAt int64  `gorm:"index:idx_sec_delivery_due,priority:2"`
	CreatedAt     int64  `gorm:"index"`
	SentAt        int64  `gorm:"index"`
	ErrorCode     string `gorm:"type:varchar(48)"`
}

func (legacySecurityDelivery) TableName() string { return "audit_security_deliveries" }

type legacySecurityEvent struct {
	EventID      string `gorm:"primaryKey;type:varchar(64)"`
	At           int64  `gorm:"index:idx_sec_event_time,priority:1;index:idx_sec_actor_time,priority:2;index:idx_sec_subject_time,priority:2;index:idx_sec_ip_time,priority:2"`
	ProcessedAt  int64  `gorm:"index"`
	ActorID      int    `gorm:"index:idx_sec_actor_time,priority:1"`
	ActorRole    int
	TargetID     int
	Visibility   int
	ResourceType string `gorm:"type:varchar(16)"`
	SubjectRef   string `gorm:"type:varchar(96);index:idx_sec_subject_time,priority:1"`
	RequestID    string `gorm:"type:varchar(64)"`
	Action       string `gorm:"type:varchar(128)"`
	IP           string `gorm:"type:varchar(64);index:idx_sec_ip_time,priority:1"`
	TokenRef     string `gorm:"type:varchar(64)"`
	SessionRef   string `gorm:"type:varchar(64)"`
	SourceRef    string `gorm:"type:varchar(64)"`
	Success      bool
	Reason       string `gorm:"type:varchar(64)"`
	Count        int
	Facts        string `gorm:"type:text"`
}

func (legacySecurityEvent) TableName() string { return "audit_security_events" }

func TestLegacySecuritySchemaUpgradeAndRestart(t *testing.T) {
	db := newWorkerTestDB(t)
	tables := []any{
		"audit_security_findings", "audit_security_activity_states", "audit_security_cleanup_states", "audit_security_geo_baselines",
		"audit_security_ip_provider_states", "audit_security_alert_events", "audit_security_actions", "audit_security_deliveries",
		"audit_security_alerts", "audit_security_events", "audit_security_states", "audit_security_baselines", "audit_security_risk_states",
	}
	require.NoError(t, db.Migrator().DropTable(tables...))
	require.NoError(t, db.AutoMigrate(&legacySecurityAlert{}, &legacySecurityAction{}, &legacySecurityDelivery{}, &legacySecurityEvent{}))
	require.NoError(t, db.Create(&legacySecurityEvent{EventID: "legacy-login", At: 10, ProcessedAt: 11, ActorID: 9, Action: "login", Success: true, IP: "8.8.8.8", Facts: "{}"}).Error)
	legacy := legacySecurityAlert{Rule: "sensitive_operation", SubjectRef: "actor:legacy", Visibility: 100, ActorID: 9, ResourceType: "channel", Severity: 2, Status: "open", FirstSeen: 10, LastSeen: 20, Version: 1, RuleVersion: 1, Evidence: "[]"}
	require.NoError(t, db.Create(&legacy).Error)
	require.NoError(t, db.Create(&legacySecurityAction{AlertID: legacy.ID, ActorID: 9, ActorRole: 100, Action: "investigating", CreatedAt: 21}).Error)
	require.NoError(t, db.Create(&legacySecurityDelivery{AlertID: legacy.ID, Kind: "none", Status: "skipped", CreatedAt: 22}).Error)
	recorder := &ddlRecorder{Interface: logger.Default.LogMode(logger.Silent)}
	db.Logger = recorder
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
	for _, column := range []string{"updated_at", "closed_at", "assignee_id", "conclusion", "archived_at", "purge_state", "latest_finding_id", "actor_role", "repeat_key", "repeat_until", "confirmed_occurrences"} {
		require.True(t, db.Migrator().HasColumn(&model.SecurityAlert{}, column), column)
	}
	var migrated model.SecurityAlert
	require.NoError(t, db.First(&migrated, legacy.ID).Error)
	require.Equal(t, "actor:legacy", migrated.SubjectRef)
	require.Equal(t, 1, migrated.RuleVersion)
	require.Zero(t, migrated.PurgeState)
	require.Zero(t, migrated.ActorRole, "legacy actor roles must not be inferred")
	require.Nil(t, migrated.RepeatKey)
	require.Nil(t, migrated.RepeatUntil)
	require.Nil(t, migrated.ConfirmedOccurrences)
	var receipt model.SecurityEvent
	require.NoError(t, db.Where("event_id = ?", "legacy-login").Take(&receipt).Error)
	require.EqualValues(t, 11, receipt.ProcessedAt)
	require.Empty(t, receipt.IPCheckState, "upgrading must not schedule an implicit historical IP scan")
	require.Zero(t, receipt.IPNextAttemptAt)
	require.True(t, db.Migrator().HasIndex(&model.SecurityEvent{}, "idx_sec_ip_pending"))
	require.True(t, db.Migrator().HasIndex(&model.SecurityAlert{}, "idx_sec_cleanup_closed"))
	require.True(t, db.Migrator().HasIndex(&model.SecurityAlert{}, "idx_sec_purge_id"))
	require.True(t, db.Migrator().HasIndex(&model.SecurityAlert{}, "idx_sec_customer_time"))
	require.True(t, db.Migrator().HasIndex(&model.SecurityAlert{}, "idx_sec_repeat_episode"))
	recorder.statements = nil
	require.NoError(t, model.MigrateAuditSecurity(t.Context(), db))
	require.Empty(t, recorder.statements, "upgraded schema must not issue DDL on the second startup")
}
