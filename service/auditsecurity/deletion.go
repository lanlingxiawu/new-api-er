package auditsecurity

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

type AlertCleanupResult struct {
	Queued  int  `json:"queued"`
	HasMore bool `json:"has_more"`
}

func DeleteAlert(ctx context.Context, db *gorm.DB, role int, id, version int64) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		active, err := model.HasActiveSecurityResponseGroup(tx, id)
		if err != nil {
			return err
		}
		if active {
			return model.ErrSecurityConflict
		}
		// A concurrent group attachment also advances the alert version. The
		// compare-and-swap below prevents deleting a newly queued response.
		result := model.SecurityVisibleQuery(tx.Model(&model.SecurityAlert{}), role).
			Where("id = ? AND version = ? AND (purge_state = ? OR purge_state IS NULL)", id, version, "").
			Updates(map[string]any{"purge_state": "deleting", "version": gorm.Expr("version + 1"), "updated_at": time.Now().Unix()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return model.ErrSecurityConflict
		}
		if err := model.AdvanceAuditRiskRevision(tx); err != nil {
			return err
		}
		return model.AdvanceAuditActivityRevision(tx)
	})
}

func CleanupClosedAlerts(ctx context.Context, db *gorm.DB, role int, closedBefore int64) (AlertCleanupResult, error) {
	var result AlertCleanupResult
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, status := range []string{"resolved", "false_positive"} {
			var candidates []model.SecurityAlert
			activeGroups := tx.Table("audit_security_response_links AS l").Select("l.alert_id").Joins("JOIN audit_security_response_groups AS g ON g.id = l.group_id").Where("g.state = ?", "active")
			err := model.SecurityVisibleQuery(tx.Model(&model.SecurityAlert{}), role).
				Select("id,version").Where("status = ? AND closed_at > 0 AND closed_at <= ? AND (purge_state = ? OR purge_state IS NULL)", status, closedBefore, "").
				Where("id NOT IN (?)", activeGroups).
				Order("closed_at ASC").Order("id ASC").Limit(101 - result.Queued).Find(&candidates).Error
			if err != nil {
				return err
			}
			for _, alert := range candidates {
				if result.Queued == 100 {
					result.HasMore = true
					break
				}
				r := tx.Model(&model.SecurityAlert{}).Where("id = ? AND version = ? AND (purge_state = ? OR purge_state IS NULL)", alert.ID, alert.Version, "").
					Updates(map[string]any{"purge_state": "deleting", "version": gorm.Expr("version + 1"), "updated_at": time.Now().Unix()})
				if r.Error != nil {
					return r.Error
				}
				result.Queued += int(r.RowsAffected)
			}
			if result.HasMore {
				break
			}
		}
		if result.Queued == 0 {
			return nil
		}
		if err := model.AdvanceAuditRiskRevision(tx); err != nil {
			return err
		}
		return model.AdvanceAuditActivityRevision(tx)
	})
	if err != nil {
		return AlertCleanupResult{}, err
	}
	return result, nil
}

// A fixed cleanup loop remains active when detection or notifications are off.
// It uses the isolated administration pool, never a relay connection.
func runAlertDeletion(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			advanceAlertDeletion(ctx)
		}
	}
}

func advanceAlertDeletion(ctx context.Context) {
	defer func() {
		if recover() != nil {
			common.SysError("audit security deletion failed")
		}
	}()
	db, err := model.AuditReadDB(false)
	if err == nil {
		err = model.PurgePendingSecurityAlerts(ctx, db)
	}
	if err != nil && ctx.Err() == nil {
		common.SysError("audit security deletion failed: " + err.Error())
	}
}
