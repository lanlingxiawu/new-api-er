package auditsecurity

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

type SecurityRelation struct {
	TargetRole       *int     `json:"target_role,omitempty"`
	TargetUsername   string   `json:"target_username,omitempty"`
	AttemptedAccount string   `json:"attempted_account,omitempty"`
	ActorUsername    string   `json:"actor_username,omitempty"`
	AlertID          int64    `json:"alert_id"`
	Rule             string   `json:"rule"`
	RuleVersion      int      `json:"rule_version"`
	FindingID        int64    `json:"finding_id"`
	EventID          string   `json:"event_id"`
	At               int64    `json:"at"`
	DetectedAt       int64    `json:"detected_at"`
	Source           string   `json:"source"`
	SubjectRef       string   `json:"subject_ref"`
	ActorID          int      `json:"actor_id"`
	ActorRole        int      `json:"actor_role"`
	ResourceType     string   `json:"resource_type"`
	TargetID         int      `json:"target_id"`
	IP               string   `json:"ip"`
	RequestID        string   `json:"request_id"`
	Severity         int      `json:"severity"`
	MatchedCount     int      `json:"matched_count"`
	Relations        []string `json:"relations"`
}

type RelationCoverage struct {
	Complete bool   `json:"complete"`
	Reason   string `json:"reason"`
}

type RelationPage struct {
	Items      []SecurityRelation `json:"items"`
	NextCursor string             `json:"next_cursor"`
	Coverage   RelationCoverage   `json:"coverage"`
}

type relationCursor struct {
	Start, End int64
	Version    int
	Revision   int64
	Binding    string
	At         int64
	ID         int64
}

func GetAlertRelations(ctx context.Context, db *gorm.DB, role, viewerID int, alertID int64, relation, cursor string, limit, start, end int) (RelationPage, error) {
	page := RelationPage{Items: []SecurityRelation{}, Coverage: RelationCoverage{Reason: "history_limited"}}
	if limit < 1 || limit > 50 || start < 0 || end <= start {
		return page, ErrActivityCursor
	}
	var alert model.SecurityAlert
	if err := model.SecurityVisibleQuery(db.WithContext(ctx), role).Where("id = ? AND (purge_state = ? OR purge_state IS NULL)", alertID, "").Take(&alert).Error; err != nil {
		return page, err
	}
	var current []model.SecurityFindingSnapshot
	if err := db.WithContext(ctx).Where("alert_id = ?", alertID).Order("id DESC").Limit(100).Find(&current).Error; err != nil {
		return page, err
	}
	ips := map[string]bool{}
	requests := map[string]bool{}
	for _, finding := range current {
		if finding.IP != "" {
			ips[finding.IP] = true
		}
		if finding.RequestID != "" {
			requests[finding.RequestID] = true
		}
	}
	if len(current) == 0 {
		var events []model.SecurityEvent
		_ = common.UnmarshalJsonStr(alert.Evidence, &events)
		for _, event := range events {
			if event.IP != "" {
				ips[event.IP] = true
			}
			if event.RequestID != "" {
				requests[event.RequestID] = true
			}
		}
		page.Coverage.Reason = "no_findings"
	}
	if relation == "account" && alert.SubjectRef == "" || relation == "ip" && len(ips) == 0 || relation == "request" && len(requests) == 0 {
		return page, nil
	}
	var revision int64
	if err := db.WithContext(ctx).Model(&model.SecurityActivityState{}).Where("name = ?", "activity").Select("revision").Scan(&revision).Error; err != nil {
		return page, err
	}
	binding := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%d:%d:%d:%d:%d", alertID, relation, role, viewerID, start, end, limit))))
	position := relationCursor{Version: 1, Revision: revision, Binding: binding, Start: int64(start), End: int64(end)}
	if cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(decoded) > 2048 || common.Unmarshal(decoded, &position) != nil || position.Version != 1 || position.Revision != revision || position.Binding != binding || position.At < int64(start) || position.At > int64(end) || position.ID <= 0 {
			return page, ErrActivityCursor
		}
	}
	q := model.SecurityVisibleQuery(db.WithContext(ctx).Table("audit_security_findings AS f").Select("f.*").Joins("JOIN audit_security_alerts AS a ON a.id = f.alert_id"), role).
		Where("f.trigger_at >= ? AND f.trigger_at <= ? AND (a.purge_state = ? OR a.purge_state IS NULL)", start, end, "")
	switch relation {
	case "account":
		q = q.Where("f.subject_ref = ?", alert.SubjectRef)
	case "ip":
		q = q.Where("f.ip IN ?", stringKeys(ips))
	case "request":
		q = q.Where("f.request_id IN ?", stringKeys(requests))
	default:
		return page, ErrActivityCursor
	}
	if position.ID > 0 {
		q = q.Where("f.trigger_at < ? OR (f.trigger_at = ? AND f.id < ?)", position.At, position.At, position.ID)
	}
	var findings []model.SecurityFindingSnapshot
	if err := q.Order("f.trigger_at DESC").Order("f.id DESC").Limit(limit + 1).Find(&findings).Error; err != nil {
		return page, err
	}
	if len(findings) > limit {
		last := findings[limit-1]
		position.At, position.ID = last.TriggerAt, last.ID
		raw, err := common.Marshal(position)
		if err != nil {
			return page, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
		findings = findings[:limit]
	}
	for _, finding := range findings {
		relations := make([]string, 0, 3)
		if finding.SubjectRef == alert.SubjectRef {
			relations = append(relations, "account")
		}
		if ips[finding.IP] && finding.IP != "" {
			relations = append(relations, "ip")
		}
		if requests[finding.RequestID] && finding.RequestID != "" {
			relations = append(relations, "request")
		}
		page.Items = append(page.Items, SecurityRelation{
			TargetRole:       finding.TargetRole,
			AttemptedAccount: finding.AttemptedAccount,
			AlertID:          finding.AlertID, Rule: finding.Rule, RuleVersion: finding.RuleVersion, FindingID: finding.ID,
			EventID: finding.TriggerEventID, At: finding.TriggerAt, DetectedAt: finding.DetectedAt, Source: finding.Source,
			SubjectRef: finding.SubjectRef, ActorID: finding.ActorID, ActorRole: finding.ActorRole,
			ResourceType: finding.ResourceType, TargetID: finding.TargetID, IP: finding.IP, RequestID: finding.RequestID,
			Severity: finding.Severity, MatchedCount: finding.MatchedCount, Relations: relations,
		})
	}
	page.Coverage.Complete = false
	var endRevision int64
	if err := db.WithContext(ctx).Model(&model.SecurityActivityState{}).Where("name = ?", "activity").Select("revision").Scan(&endRevision).Error; err != nil {
		return page, err
	}
	if endRevision != revision {
		return RelationPage{}, ErrActivityCursor
	}
	actors := make([]securityActorName, 0, len(page.Items))
	for i := range page.Items {
		actors = append(actors, securityActorName{page.Items[i].ActorID, &page.Items[i].ActorUsername})
		if page.Items[i].TargetRole != nil {
			actors = append(actors, securityActorName{page.Items[i].TargetID, &page.Items[i].TargetUsername})
		}
	}
	if err := loadSecurityActorNames(ctx, db, actors); err != nil {
		return page, err
	}
	return page, nil
}

func stringKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	return keys
}
