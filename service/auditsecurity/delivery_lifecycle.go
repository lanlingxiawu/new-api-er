package auditsecurity

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

func touchAlertActivity(tx *gorm.DB, alertID int64, now int64) error {
	if err := tx.Model(&model.SecurityAlert{}).Where("id = ?", alertID).UpdateColumn("updated_at", now).Error; err != nil {
		return err
	}
	return model.AdvanceAuditActivityRevision(tx)
}

func updateDeliveryLifecycle(ctx context.Context, db *gorm.DB, delivery model.SecurityDelivery, updates map[string]any) error {
	now := time.Now().Unix()
	updates["updated_at"] = now
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.SecurityDelivery{}).Where("id = ?", delivery.ID).Updates(updates).Error; err != nil {
			return err
		}
		return touchAlertActivity(tx, delivery.AlertID, now)
	})
}

func claimDelivery(ctx context.Context, db *gorm.DB, delivery model.SecurityDelivery) (bool, error) {
	now := time.Now().Unix()
	r := db.WithContext(ctx).Model(&model.SecurityDelivery{}).Where("id = ? AND status = ? AND EXISTS (SELECT 1 FROM audit_security_alerts AS a WHERE a.id = audit_security_deliveries.alert_id AND (a.purge_state = ? OR a.purge_state IS NULL))", delivery.ID, "pending", "").Updates(map[string]any{"status": "sending", "wait_reason": "", "updated_at": now})
	return r.RowsAffected == 1, r.Error
}

func deferDueDeliveries(ctx context.Context, db *gorm.DB) error {
	var oldest int64
	if err := db.WithContext(ctx).Model(&model.SecurityDelivery{}).Where("sent_at >= ? AND (kind <> ? OR kind IS NULL)", time.Now().Unix()-3600, "account").Order("sent_at ASC").Limit(1).Pluck("sent_at", &oldest).Error; err != nil {
		return err
	}
	next := time.Now().Unix() + 60
	if oldest > 0 {
		next = oldest + 3600
	}
	var deliveries []model.SecurityDelivery
	if err := db.WithContext(ctx).Where("status = ? AND next_attempt_at <= ? AND (kind <> ? OR kind IS NULL)", "pending", time.Now().Unix(), "account").Order("next_attempt_at ASC").Limit(3).Find(&deliveries).Error; err != nil {
		return err
	}
	for _, delivery := range deliveries {
		if err := updateDeliveryLifecycle(ctx, db, delivery, map[string]any{"wait_reason": "rate_limited", "next_attempt_at": next}); err != nil {
			return err
		}
	}
	return nil
}

func recoverStaleDeliveries(ctx context.Context, db *gorm.DB) error {
	var deliveries []model.SecurityDelivery
	cutoff := time.Now().Unix() - 30
	if err := db.WithContext(ctx).Where("status = ? AND updated_at < ?", "sending", cutoff).Limit(10).Find(&deliveries).Error; err != nil {
		return err
	}
	for _, delivery := range deliveries {
		r := db.WithContext(ctx).Model(&model.SecurityDelivery{}).Where("id = ? AND status = ?", delivery.ID, "sending").Updates(map[string]any{"status": "pending", "wait_reason": "retry_after_restart", "updated_at": time.Now().Unix()})
		if r.Error != nil {
			return r.Error
		}
	}
	return nil
}
