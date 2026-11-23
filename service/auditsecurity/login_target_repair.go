package auditsecurity

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

type LoginTargetRepairOptions struct {
	Start, End          int64
	MinID, MaxID        int64
	CursorAt, CursorID  int64
	Apply               bool
	CustomerAssignments bool
}

type LoginTargetRepairItem struct {
	AlertID      int64  `json:"alert_id"`
	Status       string `json:"status"`
	TargetUserID int    `json:"target_user_id,omitempty"`
	RuleVersion  int    `json:"rule_version,omitempty"`
}

type LoginTargetRepairPage struct {
	Items  []LoginTargetRepairItem `json:"items"`
	NextAt int64                   `json:"next_at,omitempty"`
	NextID int64                   `json:"next_id,omitempty"`
}

// RepairLoginTargetBatch is an explicit maintenance operation. It never looks
// up current usernames, starts migrations, or runs from a request/startup hook.
// A caller must supply a bounded date and ID range; report-only is the default.
func RepairLoginTargetBatch(ctx context.Context, db *gorm.DB, options LoginTargetRepairOptions) (LoginTargetRepairPage, error) {
	page := LoginTargetRepairPage{Items: []LoginTargetRepairItem{}}
	if options.Start <= 0 || options.End < options.Start || options.End-options.Start > 31*86400 || options.MinID <= 0 || options.MaxID < options.MinID || options.CursorID < 0 || options.CursorID > 0 && (options.CursorAt < options.Start || options.CursorAt > options.End || options.CursorID < options.MinID || options.CursorID > options.MaxID) {
		return page, errors.New("invalid login target repair range")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db = db.WithContext(ctx)
	query := db.Model(&model.SecurityAlert{}).Select("id,last_seen").Where("last_seen >= ? AND last_seen <= ? AND id >= ? AND id <= ?", options.Start, options.End, options.MinID, options.MaxID)
	if options.CursorID > 0 {
		query = query.Where("last_seen < ? OR (last_seen = ? AND id < ?)", options.CursorAt, options.CursorAt, options.CursorID)
	}
	var candidates []model.SecurityAlert
	if err := query.Order("last_seen DESC").Order("id DESC").Limit(100).Find(&candidates).Error; err != nil {
		return page, err
	}
	for _, candidate := range candidates {
		var alert model.SecurityAlert
		if err := db.Select(customerAlertColumns+",evidence").Where("id = ?", candidate.ID).Take(&alert).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				page.Items = append(page.Items, LoginTargetRepairItem{AlertID: candidate.ID, Status: "purged"})
				continue
			}
			return page, err
		}
		item := LoginTargetRepairItem{AlertID: alert.ID}
		if alert.LastSeen != candidate.LastSeen {
			item.Status = "version_conflict"
		} else if options.CustomerAssignments {
			version, proofIDs, status, err := verifyHistoricalCustomerAssignment(db, alert)
			if err != nil {
				return page, err
			}
			item.Status, item.RuleVersion = status, version
			if version != 0 {
				item.TargetUserID = alert.ActorID
			}
			if options.Apply && status == "candidate" {
				if err := commitCustomerAssignmentRepair(db, alert, version, proofIDs); err != nil {
					if !errors.Is(err, model.ErrSecurityConflict) {
						return page, err
					}
					item.Status = "version_conflict"
				} else {
					item.Status = "repaired"
				}
			}
		} else {
			target, proofIDs, status, err := verifyHistoricalLoginTarget(db, alert)
			if err != nil {
				return page, err
			}
			item.Status = status
			if target != nil {
				item.TargetUserID = target.UserID
				if options.Apply && status == "candidate" {
					if err := commitLoginTargetRepair(db, alert, *target, proofIDs); err != nil {
						if !errors.Is(err, model.ErrSecurityConflict) {
							return page, err
						}
						item.Status = "version_conflict"
					} else {
						item.Status = "repaired"
					}
				}
			}
		}
		page.Items = append(page.Items, item)
	}
	if len(candidates) == 100 {
		last := candidates[len(candidates)-1]
		page.NextAt, page.NextID = last.LastSeen, last.ID
	}
	return page, nil
}

func verifyHistoricalLoginTarget(db *gorm.DB, alert model.SecurityAlert) (*model.AuditLoginTarget, []string, string, error) {
	if alert.PurgeState != "" {
		return nil, nil, "purged", nil
	}
	if target := alertLoginTarget(alert); target != nil {
		return target, nil, "already_linked", nil
	}
	version := loginTargetRuleVersion(alert.Rule)
	if version == 0 || alert.RuleVersion < 1 || alert.RuleVersion > version || alert.ActorID != 0 || alert.ActorRole != 0 || alert.ResourceType != "account" {
		return nil, nil, "unsupported", nil
	}
	if alert.Occurrences < 1 || alert.Occurrences > 100 || alert.LatestFindingID <= 0 || alert.FirstSeen <= 0 || alert.LastSeen < alert.FirstSeen || alert.LastSeen-alert.FirstSeen > 31*86400 {
		return nil, nil, "missing_evidence", nil
	}
	var snapshots []model.SecurityFindingSnapshot
	if err := db.Select("id,rule,rule_version,actor_id,actor_role,resource_type,target_id,target_role,trigger_event_id,trigger_at").Where("alert_id = ?", alert.ID).Order("id DESC").Limit(101).Find(&snapshots).Error; err != nil {
		return nil, nil, "", err
	}
	if int64(len(snapshots)) != alert.Occurrences || snapshots[0].ID != alert.LatestFindingID {
		return nil, nil, "missing_evidence", nil
	}
	var evidence []model.SecurityEvent
	if common.UnmarshalJsonStr(alert.Evidence, &evidence) != nil || len(evidence) == 0 || len(evidence) > 20 {
		return nil, nil, "missing_evidence", nil
	}
	ids := make([]string, 0, len(snapshots)+len(evidence))
	for _, snapshot := range snapshots {
		ids = append(ids, snapshot.TriggerEventID)
	}
	for _, event := range evidence {
		ids = append(ids, event.EventID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if ids[0] == "" {
		return nil, nil, "missing_evidence", nil
	}
	var events []model.SecurityEvent
	if err := db.Select(model.SecurityEventColumns).Where("event_id IN ? AND at >= ? AND at <= ?", ids, alert.FirstSeen-900, alert.LastSeen).Limit(120).Find(&events).Error; err != nil {
		return nil, nil, "", err
	}
	if len(events) != len(ids) {
		return nil, nil, "missing_evidence", nil
	}
	byID := make(map[string]model.SecurityEvent, len(events))
	var target *model.AuditLoginTarget
	for _, event := range events {
		proof := eventLoginTarget(event)
		if proof == nil {
			return nil, nil, "missing_evidence", nil
		}
		if target != nil && *target != *proof {
			return nil, nil, "conflicting_evidence", nil
		}
		target = proof
		byID[event.EventID] = event
	}
	for _, snapshot := range snapshots {
		event := byID[snapshot.TriggerEventID]
		if snapshot.Rule != alert.Rule || snapshot.RuleVersion != alert.RuleVersion || snapshot.ActorID != 0 || snapshot.ActorRole != 0 || snapshot.ResourceType != "account" || snapshot.TargetID != target.UserID || snapshot.TriggerAt != event.At || snapshot.TargetRole != nil && *snapshot.TargetRole != target.Role {
			return nil, nil, "conflicting_evidence", nil
		}
		if alert.Rule == "login_failures" && (event.Action != "auth.login_failed" || event.Reason != "invalid_credentials" && event.Reason != "invalid_code") {
			return nil, nil, "conflicting_evidence", nil
		}
	}
	for _, event := range evidence {
		proof := eventLoginTarget(event)
		source := byID[event.EventID]
		if proof == nil || *proof != *target || event.At != source.At || event.Action != source.Action || event.IP != source.IP || event.RequestID != source.RequestID {
			return nil, nil, "conflicting_evidence", nil
		}
	}
	if alert.TargetUserID != nil && *alert.TargetUserID != 0 && *alert.TargetUserID != target.UserID || alert.TargetRole != nil && *alert.TargetRole != 0 && *alert.TargetRole != target.Role {
		return nil, nil, "conflicting_evidence", nil
	}
	return target, ids, "candidate", nil
}

func commitLoginTargetRepair(db *gorm.DB, alert model.SecurityAlert, target model.AuditLoginTarget, proofIDs []string) error {
	ctx, cancel := context.WithTimeout(db.Statement.Context, 5*time.Second)
	defer cancel()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().Unix()
		result := tx.Model(&model.SecurityAlert{}).Where("id = ? AND version = ? AND (purge_state = ? OR purge_state IS NULL)", alert.ID, alert.Version, "").Updates(map[string]any{
			"target_user_id": target.UserID, "target_role": target.Role, "rule_version": loginTargetRuleVersion(alert.Rule), "version": gorm.Expr("version + 1"), "updated_at": now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return model.ErrSecurityConflict
		}
		raw, err := common.Marshal(struct {
			Kind                string                 `json:"kind"`
			PreviousVersion     int64                  `json:"previous_version"`
			PreviousRuleVersion int                    `json:"previous_rule_version"`
			PreviousTargetID    *int                   `json:"previous_target_id"`
			PreviousTargetRole  *int                   `json:"previous_target_role"`
			Target              model.AuditLoginTarget `json:"login_target"`
			EventIDs            []string               `json:"event_ids"`
		}{"login_target_repair", alert.Version, alert.RuleVersion, alert.TargetUserID, alert.TargetRole, target, proofIDs})
		if err != nil {
			return err
		}
		if err := tx.Create(&model.SecurityAction{AlertID: alert.ID, Action: "investigation", Source: "repair", FromStatus: alert.Status, ToStatus: alert.Status, CreatedAt: now, Result: string(raw)}).Error; err != nil {
			return err
		}
		return model.AdvanceAuditActivityRevision(tx)
	})
}
