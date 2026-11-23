package auditsecurity

import (
	"context"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

type AlertSummary struct {
	Counts    map[string]int   `json:"counts"`
	Truncated bool             `json:"truncated"`
	Capacity  SecurityCapacity `json:"capacity"`
}

func SummarizeAlerts(ctx context.Context, db *gorm.DB, role int, filter AlertFilter) (AlertSummary, error) {
	result := AlertSummary{Counts: map[string]int{"open": 0, "high": 0, "critical": 0}}
	q := model.SecurityVisibleQuery(db.WithContext(ctx).Model(&model.SecurityAlert{}), role).
		Where("last_seen >= ? AND last_seen <= ? AND (purge_state = ? OR purge_state IS NULL)", filter.Start, filter.End, "")
	if filter.Archived == nil || !*filter.Archived {
		q = q.Where("archived_at IS NULL")
	} else {
		q = q.Where("archived_at IS NOT NULL")
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.View == "handled" {
		q = q.Where("status IN ?", []string{"resolved", "false_positive"})
	}
	if filter.Resource != "" {
		q = q.Where("resource_type = ?", filter.Resource)
	}
	if filter.HistoryScope == "legacy" {
		q = q.Where("rule IN ? AND rule_version < ?", resourceAlertRules, 2)
	}
	if filter.Rule != "" {
		q = q.Where("rule = ?", filter.Rule)
	}
	if filter.Severity > 0 {
		q = q.Where("severity = ?", filter.Severity)
	}
	if filter.ActorID > 0 {
		q = q.Where("actor_id = ?", filter.ActorID)
	}
	if filter.AssigneeID != nil {
		q = filterAlertAssignee(q, *filter.AssigneeID)
	}
	if filter.IP != "" {
		q = q.Where("EXISTS (SELECT 1 FROM audit_security_findings AS f WHERE f.alert_id = audit_security_alerts.id AND f.ip = ?) OR EXISTS (SELECT 1 FROM audit_security_alert_events AS e WHERE e.alert_id = audit_security_alerts.id AND e.ip = ?)", filter.IP, filter.IP)
	}
	var rows []struct {
		Severity int
		Status   string
	}
	if err := q.Select("severity,status").Limit(10001).Scan(&rows).Error; err != nil {
		return result, err
	}
	for _, row := range rows {
		if row.Status == "open" || row.Status == "investigating" {
			result.Counts["open"]++
		}
		if row.Severity == 2 {
			result.Counts["high"]++
		}
		if row.Severity == 3 {
			result.Counts["critical"]++
		}
	}
	result.Truncated = len(rows) > 10000
	capacity, err := GetSecurityCapacity(ctx, db, role)
	result.Capacity = capacity
	return result, err
}
