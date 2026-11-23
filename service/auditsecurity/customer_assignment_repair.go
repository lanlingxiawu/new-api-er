package auditsecurity

import (
	"context"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

// Historical recovery needs the original server identity. Bulk findings also
// need every contributing event: a snapshot proves only its trigger's role.
// This verifier is used only by explicit, bounded maintenance, never reads or
// background replay. It leaves raw audit events and finding snapshots intact.
func verifyHistoricalCustomerAssignment(db *gorm.DB, alert model.SecurityAlert) (int, []string, string, error) {
	if alert.PurgeState != "" {
		return 0, nil, "purged", nil
	}
	if alert.Rule != "sensitive_operation" && alert.Rule != "bulk_sensitive_operations" {
		return 0, nil, "unsupported", nil
	}
	allowed, err := customerSensitiveAlertAccessible(db, alert, alert.ActorID)
	if err != nil {
		return 0, nil, "", err
	}
	if allowed {
		return alert.RuleVersion, nil, "already_linked", nil
	}
	if alert.ActorID <= 0 || (alert.ActorRole != 0 && alert.ActorRole != common.RoleCommonUser) ||
		(alert.ResourceType != "user" && alert.ResourceType != "account") || alert.RuleVersion < 1 || alert.RuleVersion >= customerSensitiveOperationVersion {
		return 0, nil, "unsupported", nil
	}
	if alert.Occurrences < 1 || alert.Occurrences > 100 || alert.LatestFindingID <= 0 ||
		alert.FirstSeen <= 0 || alert.LastSeen < alert.FirstSeen || alert.LastSeen-alert.FirstSeen > 31*86400 {
		return 0, nil, "missing_evidence", nil
	}
	// An episode's rolling evidence does not identify each older bulk window.
	if alert.Rule == "bulk_sensitive_operations" && alert.Occurrences != 1 {
		return 0, nil, "missing_evidence", nil
	}
	var snapshots []struct {
		ID, TriggerAt                             int64
		Rule, ResourceType, TriggerEventID        string
		RuleVersion, ActorID, ActorRole, TargetID *int
		MatchedCount, Threshold, WindowSeconds    int
		Truncated                                 bool
	}
	if err := db.Model(&model.SecurityFindingSnapshot{}).
		Select("id,rule,rule_version,actor_id,actor_role,resource_type,target_id,trigger_event_id,trigger_at,matched_count,threshold,window_seconds,truncated").
		Where("alert_id = ?", alert.ID).Order("id DESC").Limit(101).Find(&snapshots).Error; err != nil {
		return 0, nil, "", err
	}
	if int64(len(snapshots)) != alert.Occurrences || snapshots[0].ID != alert.LatestFindingID {
		return 0, nil, "missing_evidence", nil
	}
	var evidence []model.SecurityEvent
	if common.UnmarshalJsonStr(alert.Evidence, &evidence) != nil || len(evidence) == 0 || len(evidence) > 20 {
		return 0, nil, "missing_evidence", nil
	}
	byID := make(map[string]model.SecurityEvent, len(evidence)+len(snapshots))
	for _, event := range evidence {
		if event.EventID == "" || byID[event.EventID].EventID != "" {
			return 0, nil, "conflicting_evidence", nil
		}
		byID[event.EventID] = event
	}
	var missing []string
	for _, snapshot := range snapshots {
		if snapshot.TriggerEventID == "" {
			return 0, nil, "missing_evidence", nil
		}
		if _, ok := byID[snapshot.TriggerEventID]; !ok {
			missing = append(missing, snapshot.TriggerEventID)
		}
	}
	if len(missing) > 0 {
		var events []model.SecurityEvent
		if err := db.Select(model.SecurityEventColumns).Where("event_id IN ? AND at >= ? AND at <= ?", missing, alert.FirstSeen, alert.LastSeen).Limit(100).Find(&events).Error; err != nil {
			return 0, nil, "", err
		}
		for _, event := range events {
			byID[event.EventID] = event
		}
	}
	version := 0
	proofIDs := make([]string, 0, len(byID))
	for id, event := range byID {
		proof := customerSensitiveEventVersion(event)
		if proof == 0 || event.ActorID != alert.ActorID || event.ActorRole != alert.ActorRole || event.ResourceType != alert.ResourceType ||
			event.At < alert.FirstSeen-300 || event.At > alert.LastSeen || (version != 0 && proof != version) {
			return 0, nil, "conflicting_evidence", nil
		}
		version = proof
		proofIDs = append(proofIDs, id)
	}
	for _, snapshot := range snapshots {
		event, ok := byID[snapshot.TriggerEventID]
		if !ok {
			return 0, nil, "missing_evidence", nil
		}
		if snapshot.Truncated || snapshot.Rule != alert.Rule || snapshot.RuleVersion == nil || *snapshot.RuleVersion != alert.RuleVersion ||
			snapshot.ActorID == nil || *snapshot.ActorID != event.ActorID || snapshot.ActorRole == nil || *snapshot.ActorRole != event.ActorRole ||
			snapshot.ResourceType != event.ResourceType || snapshot.TargetID == nil || *snapshot.TargetID != event.TargetID ||
			snapshot.TriggerAt != event.At || event.At < alert.FirstSeen || event.At > alert.LastSeen {
			return 0, nil, "conflicting_evidence", nil
		}
	}
	if alert.Rule == "bulk_sensitive_operations" {
		snapshot := snapshots[0]
		trigger := byID[snapshot.TriggerEventID]
		if snapshot.Threshold <= 0 || snapshot.WindowSeconds != 300 || snapshot.MatchedCount < snapshot.Threshold || snapshot.MatchedCount != alert.MatchedCount {
			return 0, nil, "missing_evidence", nil
		}
		for _, event := range evidence {
			if event.At < trigger.At-300 || event.At > trigger.At || event.Visibility != trigger.Visibility {
				return 0, nil, "conflicting_evidence", nil
			}
		}
		cfg := operation_setting.DefaultAuditSecuritySetting()
		cfg.BatchThreshold = snapshot.Threshold
		matched := false
		for _, finding := range Evaluate(trigger, evidence, cfg) {
			if finding.Rule == alert.Rule && finding.Count == snapshot.MatchedCount {
				matched = true
			}
		}
		if !matched {
			return 0, nil, "missing_evidence", nil
		}
	} else if version != customerRecoveryOperationVersion {
		// Ordinary legacy sensitive operations already have a snapshot-only
		// proof path. Do not override a rejection from that stricter contract.
		return 0, nil, "conflicting_evidence", nil
	}
	slices.Sort(proofIDs)
	return version, proofIDs, "candidate", nil
}

func commitCustomerAssignmentRepair(db *gorm.DB, alert model.SecurityAlert, version int, proofIDs []string) error {
	ctx, cancel := context.WithTimeout(db.Statement.Context, 5*time.Second)
	defer cancel()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().Unix()
		result := tx.Model(&model.SecurityAlert{}).Where("id = ? AND version = ? AND (purge_state = ? OR purge_state IS NULL)", alert.ID, alert.Version, "").
			Updates(map[string]any{"rule_version": version, "version": gorm.Expr("version + 1"), "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return model.ErrSecurityConflict
		}
		raw, err := common.Marshal(struct {
			Kind                string   `json:"kind"`
			PreviousVersion     int64    `json:"previous_version"`
			PreviousRuleVersion int      `json:"previous_rule_version"`
			RuleVersion         int      `json:"rule_version"`
			EventIDs            []string `json:"event_ids"`
		}{"customer_assignment_repair", alert.Version, alert.RuleVersion, version, proofIDs})
		if err != nil {
			return err
		}
		if err := tx.Create(&model.SecurityAction{AlertID: alert.ID, Action: "investigation", Source: "repair", FromStatus: alert.Status, ToStatus: alert.Status, CreatedAt: now, Result: string(raw)}).Error; err != nil {
			return err
		}
		return model.AdvanceAuditActivityRevision(tx)
	})
}
