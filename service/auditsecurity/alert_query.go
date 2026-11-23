package auditsecurity

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

var ErrActivityCursor = errors.New("audit alert activity changed; restart the query")

var resourceAlertRules = []string{"sensitive_operation", "bulk_sensitive_operations", "bulk_key_access"}
var securityAlertRules = []string{
	"sensitive_operation", "bulk_sensitive_operations", "bulk_key_access", "login_failures", "password_spray",
	"login_after_failures", "recovery_requests", "permission_failures", "session_reuse", "new_login_source",
	"anonymous_network", "ip_abuse", "impossible_travel",
	"multi_account_key_access", "multi_source_key_access", "new_source_channel_rewrite",
	"risky_ip_login", "risky_ip_sensitive_operation", "new_source_sensitive_operation",
}

type AlertFilter struct {
	Start, End   int64
	Before       int64
	Cursor       string
	View         string
	Status       string
	Resource     string
	HistoryScope string
	Rule         string
	Severity     int
	ActorID      int
	AssigneeID   *int
	IP           string
	Archived     *bool
	Limit        int
	ViewerID     int `json:"-"`
}

type SecurityCapacity struct {
	Total            int  `json:"total"`
	Open             int  `json:"open"`
	Archived         int  `json:"archived"`
	RetentionUnknown int  `json:"retention_unknown"`
	Truncated        bool `json:"truncated"`
	Warning          bool `json:"warning"`
}

type AlertPage struct {
	Items    []model.SecurityAlert `json:"items"`
	Next     any                   `json:"next_cursor"`
	Revision int64                 `json:"revision,omitempty"`
	Capacity SecurityCapacity      `json:"capacity"`
}

type activityCursor struct {
	Start, End int64
	Version    int
	Revision   int64
	Binding    string
	UpdatedAt  int64
	ID         int64
}

func ValidSecurityRule(rule string) bool {
	for _, candidate := range securityAlertRules {
		if candidate == rule {
			return true
		}
	}
	return false
}

func ListAlerts(ctx context.Context, db *gorm.DB, role int, f AlertFilter) (AlertPage, error) {
	page := AlertPage{Items: []model.SecurityAlert{}, Next: int64(0)}
	view := f.View
	if view == "" {
		view = "id"
	}
	limit := f.Limit
	if limit == 0 {
		limit = 50
	}
	if view == "recent_hits" {
		limit = 20
	}
	var revision int64
	var position activityCursor
	if view == "activity" {
		page.Next = ""
		if err := db.WithContext(ctx).Model(&model.SecurityActivityState{}).Where("name = ?", "activity").Select("revision").Scan(&revision).Error; err != nil {
			return page, err
		}
		bindingFilter := f
		bindingFilter.Cursor = ""
		raw, err := common.Marshal(struct {
			Filter   AlertFilter
			Role     int
			ViewerID int
		}{bindingFilter, role, f.ViewerID})
		if err != nil {
			return page, err
		}
		position = activityCursor{Version: 1, Revision: revision, Binding: fmt.Sprintf("%x", sha256.Sum256(raw)), Start: f.Start, End: f.End}
		if f.Cursor != "" {
			decoded, err := base64.RawURLEncoding.DecodeString(f.Cursor)
			if err != nil || len(decoded) > 2048 || common.Unmarshal(decoded, &position) != nil || position.Version != 1 || position.Revision != revision || position.Binding != fmt.Sprintf("%x", sha256.Sum256(raw)) || position.UpdatedAt < f.Start || position.UpdatedAt > f.End || position.ID <= 0 {
				return page, ErrActivityCursor
			}
		}
		page.Revision = revision
	}

	q := model.SecurityVisibleQuery(db.WithContext(ctx).Model(&model.SecurityAlert{}), role).Where("purge_state = ? OR purge_state IS NULL", "")
	switch view {
	case "recent_hits":
		q = q.Where("last_seen >= ? AND last_seen <= ? AND archived_at IS NULL", f.Start, f.End)
	case "activity":
		q = q.Where("updated_at >= ? AND updated_at <= ?", f.Start, f.End)
		if f.Archived == nil || !*f.Archived {
			q = q.Where("archived_at IS NULL")
		} else {
			q = q.Where("archived_at IS NOT NULL")
		}
		if position.ID > 0 {
			q = q.Where("updated_at < ? OR (updated_at = ? AND id < ?)", position.UpdatedAt, position.UpdatedAt, position.ID)
		}
	case "unresolved":
		q = q.Where("status IN ? AND archived_at IS NULL", []string{"open", "investigating"})
		if f.Start > 0 || f.End > 0 {
			q = q.Where("last_seen >= ? AND last_seen <= ?", f.Start, f.End)
		}
	case "archived":
		q = q.Where("archived_at IS NOT NULL")
		if f.Start > 0 || f.End > 0 {
			q = q.Where("archived_at >= ? AND archived_at <= ?", f.Start, f.End)
		}
	default:
		q = q.Where("last_seen >= ? AND last_seen <= ?", f.Start, f.End)
		if f.Archived == nil || !*f.Archived {
			q = q.Where("archived_at IS NULL")
		} else {
			q = q.Where("archived_at IS NOT NULL")
		}
	}
	if f.Before > 0 && view != "recent_hits" && view != "activity" {
		q = q.Where("id < ?", f.Before)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if view == "handled" {
		q = q.Where("status IN ?", []string{"resolved", "false_positive"})
	}
	if f.Resource != "" {
		q = q.Where("resource_type = ?", f.Resource)
	}
	if f.HistoryScope == "legacy" {
		q = q.Where("rule IN ? AND rule_version < ?", resourceAlertRules, 2)
	}
	if f.Rule != "" {
		q = q.Where("rule = ?", f.Rule)
	}
	if f.Severity > 0 {
		q = q.Where("severity = ?", f.Severity)
	}
	if f.ActorID > 0 {
		q = q.Where("actor_id = ?", f.ActorID)
	}
	if f.AssigneeID != nil {
		q = filterAlertAssignee(q, *f.AssigneeID)
	}
	if f.IP != "" {
		q = q.Where("EXISTS (SELECT 1 FROM audit_security_findings AS f WHERE f.alert_id = audit_security_alerts.id AND f.ip = ?) OR EXISTS (SELECT 1 FROM audit_security_alert_events AS e WHERE e.alert_id = audit_security_alerts.id AND e.ip = ?)", f.IP, f.IP)
	}
	q = q.Omit("evidence")
	switch view {
	case "recent_hits":
		q = q.Order("last_seen DESC").Order("id DESC")
	case "activity":
		q = q.Order("updated_at DESC").Order("id DESC")
	default:
		q = q.Order("id DESC")
	}
	items := make([]model.SecurityAlert, 0, limit+1)
	if err := q.Limit(limit + 1).Find(&items).Error; err != nil {
		return page, err
	}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[len(page.Items)-1]
		if view == "activity" {
			position.UpdatedAt, position.ID = last.UpdatedAt, last.ID
			raw, err := common.Marshal(position)
			if err != nil {
				return page, err
			}
			page.Next = base64.RawURLEncoding.EncodeToString(raw)
		} else if view != "recent_hits" {
			page.Next = last.ID
		}
	} else {
		page.Items = items
	}
	if view == "activity" {
		var end int64
		if err := db.WithContext(ctx).Model(&model.SecurityActivityState{}).Where("name = ?", "activity").Select("revision").Scan(&end).Error; err != nil {
			return page, err
		}
		if end != revision {
			return AlertPage{}, ErrActivityCursor
		}
	}
	capacity, err := GetSecurityCapacity(ctx, db, role)
	if err != nil {
		return page, err
	}
	page.Capacity = capacity
	if err := loadAlertAssignees(ctx, db, page.Items); err != nil {
		return page, err
	}
	if err := loadAlertResponses(ctx, db, page.Items); err != nil {
		return page, err
	}
	if role < common.RoleRootUser {
		for i := range page.Items {
			page.Items[i].CanStop = false
		}
	}
	actors := make([]securityActorName, 0, len(page.Items))
	for i := range page.Items {
		actors = append(actors, securityActorName{page.Items[i].ActorID, &page.Items[i].ActorUsername})
		if page.Items[i].TargetUserID != nil {
			actors = append(actors, securityActorName{*page.Items[i].TargetUserID, &page.Items[i].TargetUsername})
		}
	}
	if err := loadSecurityActorNames(ctx, db, actors); err != nil {
		return page, err
	}
	return page, nil
}

func GetSecurityCapacity(ctx context.Context, db *gorm.DB, role int) (SecurityCapacity, error) {
	var capacity SecurityCapacity
	rows := make([]struct {
		Status     string
		ArchivedAt *int64
		ClosedAt   *int64
	}, 0)
	query := model.SecurityVisibleQuery(db.WithContext(ctx).Model(&model.SecurityAlert{}), role).Where("purge_state = ? OR purge_state IS NULL", "").Select("status,archived_at,closed_at").Limit(10001)
	if err := query.Scan(&rows).Error; err != nil {
		return capacity, err
	}
	for _, row := range rows {
		capacity.Total++
		if row.Status == "open" || row.Status == "investigating" {
			capacity.Open++
		}
		if row.ArchivedAt != nil {
			capacity.Archived++
		}
		if (row.Status == "resolved" || row.Status == "false_positive") && (row.ClosedAt == nil || *row.ClosedAt <= 0) {
			capacity.RetentionUnknown++
		}
	}
	capacity.Truncated = len(rows) > 10000
	capacity.Warning = capacity.Truncated || capacity.Open >= 10000 || capacity.RetentionUnknown >= 1000
	return capacity, nil
}
