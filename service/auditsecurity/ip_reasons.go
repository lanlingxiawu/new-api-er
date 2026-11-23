package auditsecurity

import (
	"context"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

type IPRiskReasonPage struct {
	Items      []IPRiskReason `json:"items"`
	NextCursor int64          `json:"next_cursor"`
	Revision   int64          `json:"revision"`
	Coverage   RiskCoverage   `json:"coverage"`
}

func ListIPRiskReasons(ctx context.Context, db, logs *gorm.DB, role int, eventID string, afterID int64, limit int) (IPRiskReasonPage, error) {
	page := IPRiskReasonPage{Items: []IPRiskReason{}}
	if eventID == "" || len(eventID) > 64 || afterID < 0 || limit < 1 || limit > 50 {
		return page, ErrRiskCursor
	}
	entries, err := model.QueryAuditEvents(logs.WithContext(ctx), model.AuditLogFilter{}, role, []string{eventID})
	if err != nil {
		return page, err
	}
	if len(entries) != 1 {
		return page, gorm.ErrRecordNotFound
	}
	entry := entries[0]
	var state model.SecurityRiskState
	if err := db.WithContext(ctx).Where("name = ?", "risk").Take(&state).Error; err != nil {
		return page, err
	}
	page.Revision = state.Revision
	page.Coverage = CurrentRiskCoverage()
	q := db.WithContext(ctx).Table("audit_security_alert_events AS e").Select("a.id,a.rule,a.status,e.event_at,a.severity,a.rule_version").
		Joins("JOIN audit_security_alerts AS a ON a.id = e.alert_id").
		Where("e.event_id = ? AND e.ip = ? AND e.event_at = ? AND a.rule IN ? AND a.status IN ? AND (a.purge_state = ? OR a.purge_state IS NULL)", eventID, entry.Ip, entry.CreatedAt, ipRiskRules, []string{"open", "investigating", "resolved"}, "")
	if role < common.RoleRootUser {
		q = q.Where("a.visibility = ?", common.RoleAdminUser)
	}
	if afterID > 0 {
		q = q.Where("a.id > ?", afterID)
	}
	rows := make([]struct {
		ID          int64
		Rule        string
		Status      string
		EventAt     int64
		Severity    int
		RuleVersion int
	}, 0)
	if err := q.Order("a.id ASC").Limit(limit + 1).Scan(&rows).Error; err != nil {
		return page, err
	}
	if len(rows) > limit {
		page.NextCursor = rows[limit-1].ID
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, IPRiskReason{Rule: row.Rule, AlertID: row.ID, Status: row.Status, At: row.EventAt})
	}
	var endState model.SecurityRiskState
	if err := db.WithContext(ctx).Where("name = ?", "risk").Take(&endState).Error; err != nil {
		return page, err
	}
	if endState.Revision != state.Revision {
		return IPRiskReasonPage{}, ErrRiskCursor
	}
	return page, nil
}
