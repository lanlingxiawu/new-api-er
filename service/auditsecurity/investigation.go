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

var ErrNoAlertChanges = errors.New("no alert investigation changes")
var ErrInvalidAlertArchive = errors.New("only closed alerts can be archived")

type AlertInvestigation struct {
	Version             int64
	Status              string
	Note                string
	AssigneeID          *int
	AssigneeIDs         *[]int
	Conclusion          *string
	ObservedOccurrences *int64
	customerID          int // Set only by the customer-scoped service, never bound from JSON.
}

func UpdateAlertInvestigation(ctx context.Context, db *gorm.DB, role, actor int, input AlertInvestigation, id int64) (model.SecurityAlert, error) {
	var result model.SecurityAlert
	manualIDs, assignmentProvided, err := normalizeManualAssignees(input)
	if err != nil {
		return result, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var alert model.SecurityAlert
		var err error
		if input.customerID > 0 {
			if err = model.RequireEmployeeCustomerAccess(ctx, tx, actor, input.customerID); err != nil {
				return err
			}
			alert, err = customerAlert(tx, input.customerID, id)
		} else {
			err = model.SecurityVisibleQuery(tx, role).Where("id = ?", id).Take(&alert).Error
		}
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return model.ErrSecurityConflict
			}
			return err
		}
		if alert.PurgeState != "" {
			return model.ErrSecurityConflict
		}
		for _, assigneeID := range manualIDs {
			var user model.User
			if err := tx.Select("id", "role", "status").Where("id = ?", assigneeID).Take(&user).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrInvalidAlertAssignee
				}
				return err
			}
			allowed, err := securityAssigneeAllowed(ctx, tx, alert.Visibility, user)
			if err != nil {
				return err
			}
			if !allowed {
				return ErrInvalidAlertAssignee
			}
		}
		now := time.Now().Unix()
		if input.ObservedOccurrences != nil {
			observed := *input.ObservedOccurrences
			if input.customerID != 0 || input.Status != "resolved" || input.Note != "" || assignmentProvided || input.Conclusion != nil || alert.RepeatKey == nil || alert.RepeatUntil == nil || *alert.RepeatUntil <= now || alert.ArchivedAt != nil || alert.Rule != "sensitive_operation" || alert.Severity != 2 || (alert.Status != "open" && alert.Status != "investigating") || alert.Conclusion == "malicious" || alert.Conclusion == "false_positive" {
				return model.ErrSecurityConflict
			}
			if observed <= 0 || observed > alert.Occurrences || input.Version < observed || input.Version > alert.Version || alert.Version < alert.Occurrences || input.Version-observed != alert.Version-alert.Occurrences {
				return model.ErrSecurityConflict
			}
		}
		updates := map[string]any{"version": gorm.Expr("version + 1"), "updated_at": now}
		var confirmedOccurrences *int64
		if alert.RepeatKey != nil && input.Status != "" {
			if input.Status == "resolved" {
				count := alert.Occurrences
				if input.ObservedOccurrences != nil {
					count = *input.ObservedOccurrences
				}
				confirmedOccurrences = &count
			}
			updates["confirmed_occurrences"] = confirmedOccurrences
		}
		changed := input.Status != "" || input.Note != ""
		if input.Status != "" && input.Status != alert.Status {
			updates["status"] = input.Status
			changed = true
			switch input.Status {
			case "resolved", "false_positive":
				updates["closed_at"] = now
			case "open", "investigating":
				updates["closed_at"] = nil
				updates["archived_at"] = nil
			}
		}
		assignmentChanged := false
		if assignmentProvided {
			var previous []int
			if err := tx.Model(&model.SecurityAssignee{}).Where("alert_id = ?", id).Order("user_id ASC").Pluck("user_id", &previous).Error; err != nil {
				return err
			}
			if len(previous) == 0 && alert.AssigneeID > 0 {
				previous = []int{alert.AssigneeID}
			}
			assignmentChanged = !slices.Equal(previous, manualIDs)
			if assignmentChanged {
				alert.AssigneeID = 0
				if len(manualIDs) > 0 {
					alert.AssigneeID = manualIDs[0]
				}
				updates["assignee_id"] = alert.AssigneeID
				changed = true
			}
		}
		if input.Conclusion != nil && *input.Conclusion != alert.Conclusion {
			updates["conclusion"] = *input.Conclusion
			if input.customerID == 0 {
				updates["conclusion_note"] = input.Note
			}
			changed = true
		} else if input.customerID == 0 && input.Note != "" && input.Note != alert.ConclusionNote {
			updates["conclusion_note"] = input.Note
			changed = true
		}
		if !changed {
			return ErrNoAlertChanges
		}
		// The row was authorized above, within this transaction. Version checking
		// also prevents stale writes after a scanner reopen or a cleanup request.
		query := tx.Model(&model.SecurityAlert{}).Where("id = ? AND (purge_state = ? OR purge_state IS NULL)", id, "")
		if input.ObservedOccurrences != nil {
			// Only detector hits advance both counters. All manual/response changes
			// advance Version alone and must still conflict with a stale confirmation.
			query = query.Where("version - occurrences = ? AND occurrences >= ? AND status = ? AND severity = ? AND repeat_key = ? AND repeat_until = ? AND repeat_until > ? AND archived_at IS NULL", input.Version-*input.ObservedOccurrences, *input.ObservedOccurrences, alert.Status, 2, *alert.RepeatKey, *alert.RepeatUntil, now)
		} else {
			query = query.Where("version = ?", input.Version)
		}
		r := query.Updates(updates)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return model.ErrSecurityConflict
		}
		if assignmentChanged {
			if err := tx.Where("alert_id = ?", id).Delete(&model.SecurityAssignee{}).Error; err != nil {
				return err
			}
			if len(manualIDs) > 0 {
				links := make([]model.SecurityAssignee, len(manualIDs))
				for i, userID := range manualIDs {
					links[i] = model.SecurityAssignee{AlertID: id, UserID: userID}
				}
				if err := tx.Create(&links).Error; err != nil {
					return err
				}
			}
		}
		assigned := []model.SecurityAlert{alert}
		if err := loadAlertAssignees(ctx, tx, assigned); err != nil {
			return err
		}
		snapshot, err := common.Marshal(struct {
			Assignees            []model.SecurityAlertAssignee `json:"assignees"`
			ConfirmedOccurrences *int64                        `json:"confirmed_occurrences,omitempty"`
		}{assigned[0].Assignees, confirmedOccurrences})
		if err != nil {
			return err
		}
		action := "investigation"
		toStatus := alert.Status
		if input.Status != "" {
			action = input.Status
			toStatus = input.Status
		}
		conclusion := alert.Conclusion
		if input.Conclusion != nil {
			conclusion = *input.Conclusion
		}
		source := "human"
		if input.customerID > 0 {
			source = "employee"
		}
		if err := tx.Create(&model.SecurityAction{
			AlertID: id, ActorID: actor, ActorRole: role, Action: action, Note: input.Note,
			CreatedAt: now, Source: source, FromStatus: alert.Status, ToStatus: toStatus,
			AssigneeID: alert.AssigneeID, Conclusion: conclusion, Result: string(snapshot),
		}).Error; err != nil {
			return err
		}
		if err := model.AdvanceAuditRiskRevision(tx); err != nil {
			return err
		}
		if err := model.AdvanceAuditActivityRevision(tx); err != nil {
			return err
		}
		if err := tx.Where("id = ?", id).Take(&result).Error; err != nil {
			return err
		}
		result.Assignees = assigned[0].Assignees
		result.ManualAssigneeIDs = assigned[0].ManualAssigneeIDs
		return nil
	})
	result.Evidence = ""
	return result, err
}

func ArchiveAlert(ctx context.Context, db *gorm.DB, role, actor int, id, version int64, archived bool) (model.SecurityAlert, error) {
	var result model.SecurityAlert
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var alert model.SecurityAlert
		if err := model.SecurityVisibleQuery(tx, role).Where("id = ?", id).Take(&alert).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return model.ErrSecurityConflict
			}
			return err
		}
		if alert.PurgeState != "" {
			return model.ErrSecurityConflict
		}
		if alert.Status != "resolved" && alert.Status != "false_positive" {
			return ErrInvalidAlertArchive
		}
		now := time.Now().Unix()
		updates := map[string]any{"version": gorm.Expr("version + 1"), "updated_at": now}
		action := "unarchived"
		if archived {
			updates["archived_at"] = now
			action = "archived"
		} else {
			updates["archived_at"] = nil
		}
		r := model.SecurityVisibleQuery(tx.Model(&model.SecurityAlert{}), role).Where("id = ? AND version = ?", id, version).Updates(updates)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return model.ErrSecurityConflict
		}
		if err := tx.Create(&model.SecurityAction{AlertID: id, ActorID: actor, ActorRole: role, Action: action, CreatedAt: now, Source: "human", FromStatus: alert.Status, ToStatus: alert.Status}).Error; err != nil {
			return err
		}
		if err := model.AdvanceAuditActivityRevision(tx); err != nil {
			return err
		}
		return model.SecurityVisibleQuery(tx, role).Where("id = ?", id).Take(&result).Error
	})
	result.Evidence = ""
	return result, err
}
