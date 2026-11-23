package auditsecurity

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrRiskCursor = errors.New("audit risk results changed; restart the query")
var highIPRiskRules = []string{"password_spray", "login_after_failures", "session_reuse", "ip_abuse", "impossible_travel", "risky_ip_login", "risky_ip_sensitive_operation", "multi_account_key_access", "multi_source_key_access", "new_source_channel_rewrite", "new_source_sensitive_operation"}
var ipRiskRules = append([]string{"new_login_source", "login_failures", "anonymous_network", "recovery_requests", "permission_failures"}, highIPRiskRules...)

func ipRiskLevel(rule string) int {
	if slices.Contains(highIPRiskRules, rule) {
		return 2
	}
	if slices.Contains(ipRiskRules, rule) {
		return 1
	}
	return 0
}

type IPRiskReason struct {
	Rule    string `json:"rule"`
	AlertID int64  `json:"alert_id"`
	Status  string `json:"status"`
	At      int64  `json:"at"`
}
type IPRiskMarker struct {
	EventID string         `json:"event_id"`
	IP      string         `json:"ip"`
	State   string         `json:"state"`
	Level   string         `json:"level"`
	Reasons []IPRiskReason `json:"reasons"`
	More    bool           `json:"more"`
}
type IPRiskRecord struct {
	*model.AuditLog
	Marker IPRiskMarker `json:"ip_marker"`
}
type RiskCoverage struct {
	HistoricalComplete bool           `json:"historical_complete"`
	BackfillDone       bool           `json:"backfill_done"`
	Health             CoverageHealth `json:"health"`
}

// Coverage exposes availability only; scanner details remain on the root-only health API.
type CoverageHealth struct {
	Enabled          bool `json:"enabled"`
	CaptureEnabled   bool `json:"capture_enabled"`
	Suspended        bool `json:"suspended"`
	Ready            bool `json:"ready"`
	HistoryTruncated bool `json:"history_truncated"`
}

func CurrentRiskCoverage() RiskCoverage {
	status := Status()
	// Keep the response fields for clients, but never claim historical coverage.
	return RiskCoverage{Health: CoverageHealth{
		Enabled: status.Enabled, CaptureEnabled: status.CaptureEnabled, Suspended: status.Suspended,
		Ready: status.Ready, HistoryTruncated: status.HistoryTruncated,
	}}
}

type RiskPage struct {
	Items      []IPRiskRecord `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
	Coverage   RiskCoverage   `json:"coverage"`
}
type riskCursor struct {
	Version  int
	Revision int64
	Binding  string
	At       int64
	Event    string
}

func RiskMarkers(ctx context.Context, db *gorm.DB, logs []*model.AuditLog, role int) ([]IPRiskMarker, error) {
	markers := make([]IPRiskMarker, 0, len(logs))
	if len(logs) == 0 {
		return markers, nil
	}
	ids := make([]string, 0, len(logs))
	for _, row := range logs {
		ids = append(ids, row.EventId)
	}
	type association struct {
		EventID      string
		IP           string
		EventAt      int64
		AlertID      int64
		Rule, Status string
		LastSeen     int64
		Episodes     int
	}
	// Collapse historical episodes by rule and status before limiting displayed reasons.
	// Every active rule is considered when choosing the event's highest risk.
	for _, row := range logs {
		marker := IPRiskMarker{EventID: row.EventId, IP: row.Ip, State: "none", Reasons: []IPRiskReason{}}
		markers = append(markers, marker)
	}
	var links []association
	q := db.WithContext(ctx).Table("audit_security_alert_events AS e").Select("e.event_id,e.ip,e.event_at,MAX(a.id) AS alert_id,a.rule,a.status,MAX(a.last_seen) AS last_seen,COUNT(*) AS episodes").Joins("JOIN audit_security_alerts AS a ON a.id = e.alert_id").Where("e.event_id IN ? AND a.rule IN ? AND a.status IN ? AND (a.purge_state = ? OR a.purge_state IS NULL)", ids, ipRiskRules, []string{"open", "investigating", "resolved"}, "").Group("e.event_id,e.ip,e.event_at,a.rule,a.status")
	if role < common.RoleRootUser {
		q = q.Where("a.visibility = ?", common.RoleAdminUser)
	}
	// The IP rule set times three statuses per event is bounded; the deadline bounds the scan.
	if err := q.Order(clause.OrderBy{Expression: clause.Expr{
		SQL:  "CASE WHEN a.status = 'resolved' THEN 1 ELSE 0 END, CASE WHEN a.rule IN ? THEN 0 ELSE 1 END, MAX(a.id) DESC",
		Vars: []any{highIPRiskRules},
	}}).Scan(&links).Error; err != nil {
		return nil, err
	}
	positions := make(map[string]int, len(logs))
	for i, row := range logs {
		positions[row.EventId] = i
	}
	best := make([]int, len(logs))
	for _, link := range links {
		i, ok := positions[link.EventID]
		if !ok || logs[i].Ip != link.IP || logs[i].CreatedAt != link.EventAt {
			continue
		}
		marker := &markers[i]
		level := ipRiskLevel(link.Rule)
		if link.Status == "open" || link.Status == "investigating" {
			if level > best[i] {
				best[i] = level
			}
			marker.State = "active"
		} else if marker.State == "none" {
			marker.State = "resolved"
		}
		if len(marker.Reasons) < 3 {
			marker.Reasons = append(marker.Reasons, IPRiskReason{link.Rule, link.AlertID, link.Status, link.EventAt})
		} else {
			marker.More = true
		}
		if link.Episodes > 1 {
			marker.More = true
		}
	}
	for i, level := range best {
		if level == 2 {
			markers[i].Level = "high"
		} else if level == 1 {
			markers[i].Level = "attention"
		}
	}
	return markers, nil
}

func QueryRiskRecords(ctx context.Context, db, logs *gorm.DB, f model.AuditLogFilter, role, user int, risk, cursor string, limit int) (RiskPage, error) {
	page := RiskPage{Items: []IPRiskRecord{}}
	if f.StartTimestamp <= 0 || f.EndTimestamp < f.StartTimestamp || f.EndTimestamp-f.StartTimestamp > 31*86400 || limit < 1 || limit > 100 || (risk != "abnormal" && risk != "attention" && risk != "high") {
		return page, ErrRiskCursor
	}
	var state model.SecurityRiskState
	if err := db.WithContext(ctx).Where("name = ?", "risk").Take(&state).Error; err != nil {
		return page, err
	}
	page.Coverage = CurrentRiskCoverage()
	raw, err := common.Marshal(struct {
		Filter     model.AuditLogFilter
		Role, User int
		Risk       string
	}{f, role, user, risk})
	if err != nil {
		return page, err
	}
	binding := fmt.Sprintf("%x", sha256.Sum256(raw))
	position := riskCursor{Version: 1, Revision: state.Revision, Binding: binding}
	if cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(decoded) > 2048 || common.Unmarshal(decoded, &position) != nil || position.Version != 1 || position.Revision != state.Revision || position.Binding != binding || position.At < f.StartTimestamp || position.At > f.EndTimestamp || len(position.Event) > 64 || position.Event == "" {
			return page, ErrRiskCursor
		}
	}
	exhausted := false
	for processed := 0; processed < 1000 && len(page.Items) < limit; {
		batch, err := QueryRiskBatch(ctx, db, logs, f, role, risk, state.Revision, RiskPosition{position.At, position.Event}, 100, limit-len(page.Items))
		if err != nil {
			return page, err
		}
		processed += batch.Scanned
		page.Items = append(page.Items, batch.Items...)
		position.At, position.Event = batch.Position.At, batch.Position.Event
		if batch.Done {
			exhausted = true
			break
		}
	}
	var end model.SecurityRiskState
	if err := db.WithContext(ctx).Where("name = ?", "risk").Take(&end).Error; err != nil {
		return page, err
	}
	if end.Revision != state.Revision {
		return RiskPage{}, ErrRiskCursor
	}
	if !exhausted {
		raw, err := common.Marshal(position)
		if err != nil {
			return page, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}

func StoreRiskEvidence(tx *gorm.DB, alert model.SecurityAlert, evidence []model.SecurityEvent) error {
	// All retained evidence is searchable by IP. Risk classification remains
	// restricted to ipRiskRules in the marker and risk-record queries.
	links := make([]model.SecurityAlertEvent, 0, len(evidence))
	for _, e := range evidence {
		if e.EventID != "" {
			links = append(links, model.SecurityAlertEvent{EventID: e.EventID, AlertID: alert.ID, EventAt: e.At, IP: e.IP})
		}
	}
	if len(links) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(links, 100).Error
}

// RiskPosition is the candidate position, including candidates filtered from output.
type RiskPosition struct {
	At    int64
	Event string
}
type RiskBatch struct {
	Items    []IPRiskRecord
	Position RiskPosition
	Scanned  int
	Done     bool
}

// QueryRiskBatch is shared by the page and export scanner. It never scans more
// than 100 candidates, and pins the revision supplied by the entire operation.
func QueryRiskBatch(ctx context.Context, db, logs *gorm.DB, f model.AuditLogFilter, role int, risk string, revision int64, after RiskPosition, batchSize, resultLimit int) (RiskBatch, error) {
	batch := RiskBatch{Position: after}
	if batchSize < 1 || batchSize > 100 || resultLimit < 1 || resultLimit > 100 || f.StartTimestamp <= 0 || f.EndTimestamp < f.StartTimestamp || f.EndTimestamp-f.StartTimestamp > 31*86400 || (risk != "high" && risk != "attention" && risk != "abnormal") {
		return batch, ErrRiskCursor
	}
	var state model.SecurityRiskState
	if err := db.WithContext(ctx).Where("name = ?", "risk").Take(&state).Error; err != nil {
		return batch, err
	}
	if state.Revision != revision {
		return batch, ErrRiskCursor
	}
	type candidate struct {
		EventID string
		EventAt int64
	}
	candidates := []candidate{}
	q := db.WithContext(ctx).Table("audit_security_alert_events AS e").Select("e.event_id,e.event_at").Joins("JOIN audit_security_alerts AS a ON a.id = e.alert_id").Where("e.event_at >= ? AND e.event_at <= ? AND a.rule IN ? AND a.status IN ? AND (a.purge_state = ? OR a.purge_state IS NULL)", f.StartTimestamp, f.EndTimestamp, ipRiskRules, []string{"open", "investigating"}, "")
	if role < common.RoleRootUser {
		q = q.Where("a.visibility = ?", common.RoleAdminUser)
	}
	if f.IP != "" {
		q = q.Where("e.ip = ?", f.IP)
	}
	if after.Event != "" {
		q = q.Where("e.event_at < ? OR (e.event_at = ? AND e.event_id < ?)", after.At, after.At, after.Event)
	}
	q = q.Group("e.event_at,e.event_id")
	if risk == "high" {
		q = q.Having("MAX(CASE WHEN a.rule IN ? THEN 2 ELSE 1 END) = 2", highIPRiskRules)
	}
	if risk == "attention" {
		q = q.Having("MAX(CASE WHEN a.rule IN ? THEN 2 ELSE 1 END) = 1", highIPRiskRules)
	}
	if err := q.Order("e.event_at DESC").Order("e.event_id DESC").Limit(batchSize).Scan(&candidates).Error; err != nil {
		return batch, err
	}
	if len(candidates) == 0 {
		batch.Done = true
		return batch, nil
	}
	ids := make([]string, 0, len(candidates))
	for _, c := range candidates {
		ids = append(ids, c.EventID)
	}
	matched, err := model.QueryAuditEvents(logs.WithContext(ctx), f, role, ids)
	if err != nil {
		return batch, err
	}
	byID := make(map[string]*model.AuditLog, len(matched))
	for _, row := range matched {
		byID[row.EventId] = row
	}
	markers, err := RiskMarkers(ctx, db, matched, role)
	if err != nil {
		return batch, err
	}
	riskByID := make(map[string]IPRiskMarker, len(markers))
	for _, marker := range markers {
		riskByID[marker.EventID] = marker
	}
	for _, c := range candidates {
		batch.Scanned++
		batch.Position.At, batch.Position.Event = c.EventAt, c.EventID
		if row := byID[c.EventID]; row != nil && row.CreatedAt == c.EventAt {
			marker := riskByID[row.EventId]
			if marker.State == "active" && (risk == "abnormal" || marker.Level == risk) {
				batch.Items = append(batch.Items, IPRiskRecord{row, marker})
			}
		}
		if len(batch.Items) == resultLimit {
			break
		}
	}
	if batch.Position.Event == candidates[len(candidates)-1].EventID && len(candidates) < batchSize {
		batch.Done = true
	}

	var end model.SecurityRiskState
	if err := db.WithContext(ctx).Where("name = ?", "risk").Take(&end).Error; err != nil {
		return batch, err
	}
	if end.Revision != revision {
		return RiskBatch{}, ErrRiskCursor
	}
	return batch, nil
}
