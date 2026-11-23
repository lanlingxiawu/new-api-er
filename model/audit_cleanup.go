package model

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// CleanupAuditSecurityAlerts advances a persistent scan and removes at most one
// bounded child batch per call. Skipped alerts never block later candidates.
func CleanupAuditSecurityAlerts(ctx context.Context, db *gorm.DB, closedCutoff int64, archiveCutoffs ...int64) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	archiveCutoff := closedCutoff
	if len(archiveCutoffs) > 0 {
		archiveCutoff = archiveCutoffs[0]
	}
	drained := false
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state SecurityCleanupState
		if err := lockForUpdate(tx.Where("name = ?", "alerts")).Take(&state).Error; err != nil {
			return err
		}
		if state.CycleMaxID == 0 {
			if err := tx.Model(&SecurityAlert{}).Select("COALESCE(MAX(id),0)").Scan(&state.CycleMaxID).Error; err != nil {
				return err
			}
			state.CursorID = 0
			state.ClosedCutoff = closedCutoff
			state.ArchiveCutoff = archiveCutoff
		}
		if state.CycleMaxID == 0 {
			drained = true
			state.LastComplete = time.Now().Unix()
			return tx.Save(&state).Error
		}
		var alerts []SecurityAlert
		if err := lockForUpdate(tx.Where("id > ? AND id <= ?", state.CursorID, state.CycleMaxID).Order("id ASC").Limit(20)).Find(&alerts).Error; err != nil {
			return err
		}
		if len(alerts) == 0 {
			drained = true
			state.CycleMaxID = 0
			state.CursorID = 0
			state.ClosedCutoff = 0
			state.ArchiveCutoff = 0
			state.LastComplete = time.Now().Unix()
			return tx.Save(&state).Error
		}
		for i := range alerts {
			alert := &alerts[i]
			active, err := HasActiveSecurityResponseGroup(tx, alert.ID)
			if err != nil {
				return err
			}
			if active {
				continue
			}
			if alert.PurgeState == "" {
				if alert.Status != "resolved" && alert.Status != "false_positive" {
					continue
				}
				if alert.ClosedAt == nil || *alert.ClosedAt <= 0 {
					// Legacy rows without a recorded closure stay untouched. Do not
					// reconstruct history just to make them eligible for cleanup.
					continue
				}
				cutoff := state.ClosedCutoff
				if alert.ArchivedAt != nil {
					cutoff = state.ArchiveCutoff
				}
				if *alert.ClosedAt > cutoff {
					continue
				}
				now := time.Now().Unix()
				result := tx.Model(&SecurityAlert{}).Where("id = ? AND version = ? AND (purge_state = ? OR purge_state IS NULL)", alert.ID, alert.Version, "").Updates(map[string]any{"purge_state": "deleting", "version": gorm.Expr("version + 1"), "updated_at": now})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return ErrSecurityConflict
				}
				if err := AdvanceAuditActivityRevision(tx); err != nil {
					return err
				}
				return tx.Save(&state).Error
			}
			if alert.PurgeState != "deleting" {
				continue
			}
			var sending int64
			if err := tx.Model(&SecurityDelivery{}).Where("alert_id = ? AND status = ?", alert.ID, "sending").Count(&sending).Error; err != nil {
				return err
			}
			if sending > 0 {
				continue
			}
			removed, err := purgeSecurityAlertBatch(tx, alert)
			if err != nil {
				return err
			}
			if removed {
				state.CursorID = alert.ID
			}
			return tx.Save(&state).Error
		}
		state.CursorID = alerts[len(alerts)-1].ID
		return tx.Save(&state).Error
	})
	return drained, err
}

// PurgePendingSecurityAlerts drains one batch independently of retention scans.
// The shared state lock serializes cleanup across nodes and the retention worker.
func PurgePendingSecurityAlerts(ctx context.Context, db *gorm.DB) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state SecurityCleanupState
		if err := lockForUpdate(tx.Where("name = ?", "alerts")).Take(&state).Error; err != nil {
			return err
		}
		var alerts []SecurityAlert
		if err := lockForUpdate(tx.Select("id,version").Where("purge_state = ?", "deleting").Order("id ASC").Limit(20)).Find(&alerts).Error; err != nil {
			return err
		}
		for _, alert := range alerts {
			// Notification I/O has an eight-second timeout. A stale claim must not
			// keep deletion stuck after a restart or disabled notifications.
			var staleIDs []int64
			if err := tx.Model(&SecurityDelivery{}).Where("alert_id = ? AND status = ? AND updated_at < ?", alert.ID, "sending", time.Now().Unix()-30).Limit(500).Pluck("id", &staleIDs).Error; err != nil {
				return err
			}
			if len(staleIDs) > 0 {
				return tx.Model(&SecurityDelivery{}).Where("id IN ? AND status = ? AND updated_at < ?", staleIDs, "sending", time.Now().Unix()-30).Updates(map[string]any{"status": "cancelled", "wait_reason": "alert_unavailable"}).Error
			}
			var sending int64
			if err := tx.Model(&SecurityDelivery{}).Where("alert_id = ? AND status = ?", alert.ID, "sending").Count(&sending).Error; err != nil {
				return err
			}
			if sending > 0 {
				continue
			}
			_, err := purgeSecurityAlertBatch(tx, &alert)
			return err
		}
		return nil
	})
}

func purgeSecurityAlertBatch(tx *gorm.DB, alert *SecurityAlert) (bool, error) {
	groupActive, err := HasActiveSecurityResponseGroup(tx, alert.ID)
	if err != nil || groupActive {
		return false, err
	}
	var active int64
	if err := tx.Model(&SecurityAction{}).Where("alert_id = ? AND outcome IN ?", alert.ID, []string{"pending", "committed", "sync_pending", "clearing"}).Count(&active).Error; err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	links, err := deleteAlertLinksBatch(tx, alert.ID)
	if err != nil {
		return false, err
	}
	if links > 0 {
		return false, AdvanceAuditRiskRevision(tx)
	}
	for _, child := range []any{&SecurityDelivery{}, &SecurityFindingSnapshot{}, &SecurityAction{}} {
		deleted, err := deleteAlertChildBatch(tx, child, alert.ID)
		if err != nil || deleted > 0 {
			return false, err
		}
	}
	if err := tx.Where("alert_id = ?", alert.ID).Delete(&SecurityResponseLink{}).Error; err != nil {
		return false, err
	}
	if err := tx.Where("alert_id = ?", alert.ID).Delete(&SecurityAssignee{}).Error; err != nil {
		return false, err
	}
	result := tx.Where("id = ? AND version = ?", alert.ID, alert.Version).Delete(&SecurityAlert{})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected != 1 {
		return false, ErrSecurityConflict
	}
	return true, AdvanceAuditActivityRevision(tx)
}

func deleteAlertLinksBatch(tx *gorm.DB, alertID int64) (int, error) {
	var eventIDs []string
	if err := tx.Model(&SecurityAlertEvent{}).Where("alert_id = ?", alertID).Limit(500).Pluck("event_id", &eventIDs).Error; err != nil {
		return 0, err
	}
	if len(eventIDs) == 0 {
		return 0, nil
	}
	result := tx.Where("alert_id = ? AND event_id IN ?", alertID, eventIDs).Delete(&SecurityAlertEvent{})
	return len(eventIDs), result.Error
}

func deleteAlertChildBatch(tx *gorm.DB, child any, alertID int64) (int, error) {
	var ids []int64
	children := tx.Model(child).Where("alert_id = ?", alertID)
	if _, ok := child.(*SecurityAction); ok {
		children = children.Where("response_group_id IS NULL")
	}
	if err := children.Limit(500).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	query := tx.Where("id IN ?", ids)
	if _, ok := child.(*SecurityDelivery); ok {
		query = query.Where("status <> ?", "sending")
	}
	result := query.Delete(child)
	if result.Error != nil {
		return 0, result.Error
	}
	return len(ids), nil
}

func HasActiveSecurityResponseGroup(tx *gorm.DB, alertID int64) (bool, error) {
	groups := tx.Model(&SecurityResponseLink{}).Select("group_id").Where("alert_id = ?", alertID)
	var active int64
	err := tx.Model(&SecurityResponseGroup{}).Where("id IN (?) AND state = ?", groups, "active").Count(&active).Error
	return active > 0, err
}

// Terminal receipts outlive every linked alert, so deleting one alert cannot
// remove another alert's response history. The original event window has ended.
func CleanupSecurityResponseGroups(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var groups []SecurityResponseGroup
		linked := tx.Model(&SecurityResponseLink{}).Select("group_id").Where("group_id = audit_security_response_groups.id")
		if err := tx.Select("id").Where("state = ? AND created_at < ? AND NOT EXISTS (?)", "complete", time.Now().Unix()-172800, linked).Order("created_at, id").Limit(100).Find(&groups).Error; err != nil {
			return err
		}
		for _, group := range groups {
			if err := tx.Where("response_group_id = ?", group.ID).Delete(&SecurityAction{}).Error; err != nil {
				return err
			}
			if err := tx.Delete(&group).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
