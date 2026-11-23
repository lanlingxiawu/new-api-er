package model

import "gorm.io/gorm"

// Associations retain every event that entered an alert's evidence, until alert cleanup.
type SecurityAlertEvent struct {
	EventID string `gorm:"primaryKey;type:varchar(64);index:idx_sec_link_alert,priority:2;index:idx_sec_link_time,priority:2;index:idx_sec_link_ip,priority:3"`
	AlertID int64  `gorm:"primaryKey;autoIncrement:false;index:idx_sec_link_alert,priority:1;index:idx_sec_link_time,priority:3;index:idx_sec_link_ip,priority:4"`
	EventAt int64  `gorm:"index:idx_sec_link_time,priority:1;index:idx_sec_link_ip,priority:2"`
	IP      string `gorm:"type:varchar(64);index:idx_sec_link_ip,priority:1"`
}

func (SecurityAlertEvent) TableName() string { return "audit_security_alert_events" }

type SecurityRiskState struct {
	Name     string `gorm:"primaryKey;type:varchar(32)"`
	Revision int64
	// Retained schema only; historical evidence backfill is no longer executed.
	BackfillID    int64
	BackfillEndID int64
	BackfillDone  bool
}

func (SecurityRiskState) TableName() string { return "audit_security_risk_states" }

func AdvanceAuditRiskRevision(tx *gorm.DB) error {
	return tx.Model(&SecurityRiskState{}).Where("name = ?", "risk").UpdateColumn("revision", gorm.Expr("revision + 1")).Error
}
