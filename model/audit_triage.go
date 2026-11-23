package model

import (
	"database/sql/driver"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type SecurityDimensions map[string]int

func (d SecurityDimensions) Value() (driver.Value, error) {
	if d == nil {
		return "{}", nil
	}
	raw, err := common.Marshal(d)
	return string(raw), err
}

func (d *SecurityDimensions) Scan(value any) error {
	if value == nil {
		*d = SecurityDimensions{}
		return nil
	}
	var raw []byte
	switch v := value.(type) {
	case string:
		raw = []byte(v)
	case []byte:
		raw = v
	default:
		return errors.New("invalid security dimensions")
	}
	return common.Unmarshal(raw, d)
}

type SecurityFindingSnapshot struct {
	TargetRole       *int               `json:"target_role,omitempty"`
	TargetUsername   string             `json:"target_username,omitempty" gorm:"-:all"`
	AttemptedAccount string             `json:"attempted_account,omitempty" gorm:"type:varchar(128)"`
	ActorUsername    string             `json:"actor_username,omitempty" gorm:"-:all"`
	DetectionKey     *string            `json:"-" gorm:"type:varchar(96);uniqueIndex:idx_sec_finding_detection"`
	ID               int64              `json:"id" gorm:"index:idx_sec_finding_alert_id,priority:2"`
	AlertID          int64              `json:"alert_id" gorm:"index:idx_sec_finding_alert_id,priority:1;index:idx_sec_finding_alert_ip,priority:1"`
	Rule             string             `json:"rule" gorm:"type:varchar(64)"`
	RuleVersion      int                `json:"rule_version"`
	TriggerEventID   string             `json:"trigger_event_id" gorm:"type:varchar(64)"`
	TriggerAt        int64              `json:"trigger_at" gorm:"index:idx_sec_finding_time,priority:1;index:idx_sec_finding_ip_time,priority:2;index:idx_sec_finding_actor_time,priority:2;index:idx_sec_finding_subject_time,priority:2;index:idx_sec_finding_request_time,priority:2"`
	DetectedAt       int64              `json:"detected_at"`
	Source           string             `json:"source" gorm:"type:varchar(16)"`
	SubjectRef       string             `json:"subject_ref" gorm:"type:varchar(96);index:idx_sec_finding_subject_time,priority:1"`
	ActorID          int                `json:"actor_id" gorm:"index:idx_sec_finding_actor_time,priority:1"`
	ActorRole        int                `json:"actor_role"`
	ResourceType     string             `json:"resource_type" gorm:"type:varchar(16)"`
	TargetID         int                `json:"target_id"`
	IP               string             `json:"ip" gorm:"type:varchar(64);index:idx_sec_finding_ip_time,priority:1;index:idx_sec_finding_alert_ip,priority:2"`
	RequestID        string             `json:"request_id" gorm:"type:varchar(64);index:idx_sec_finding_request_time,priority:1"`
	Severity         int                `json:"severity"`
	Threshold        int                `json:"threshold"`
	WindowSeconds    int                `json:"window_seconds"`
	MatchedCount     int                `json:"matched_count"`
	Dimensions       SecurityDimensions `json:"dimensions" gorm:"type:text"`
	Truncated        bool               `json:"truncated"`
}

func (SecurityFindingSnapshot) TableName() string { return "audit_security_findings" }

type SecurityActivityState struct {
	Name     string `gorm:"primaryKey;type:varchar(32)"`
	Revision int64
}

func (SecurityActivityState) TableName() string { return "audit_security_activity_states" }

func AdvanceAuditActivityRevision(tx *gorm.DB) error {
	return tx.Model(&SecurityActivityState{}).Where("name = ?", "activity").UpdateColumn("revision", gorm.Expr("revision + 1")).Error
}

type SecurityCleanupState struct {
	Name          string `gorm:"primaryKey;type:varchar(32)"`
	CycleMaxID    int64
	CursorID      int64
	ClosedCutoff  int64
	ArchiveCutoff int64
	LastComplete  int64
}

func (SecurityCleanupState) TableName() string { return "audit_security_cleanup_states" }

type SecurityGeoBaseline struct {
	AccountID   int    `gorm:"primaryKey"`
	CountryCode string `gorm:"type:varchar(2)"`
	Country     string `gorm:"type:varchar(128)"`
	Latitude    float64
	Longitude   float64
	LastAt      int64  `gorm:"index"`
	LastEventID string `gorm:"type:varchar(64)"`
}

func (SecurityGeoBaseline) TableName() string { return "audit_security_geo_baselines" }

type SecurityIPProviderState struct {
	ProviderID   string `gorm:"primaryKey;type:varchar(64)"`
	Day          string `gorm:"primaryKey;type:varchar(10);index"`
	Used         int64
	Failures     int
	CircuitUntil int64
	LastError    string `gorm:"type:varchar(48)"`
	ConfigHash   string `gorm:"type:varchar(64)"`
	UpdatedAt    int64
}

func (SecurityIPProviderState) TableName() string { return "audit_security_ip_provider_states" }
