package model

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SecurityResponseGroup struct {
	ID             int64   `json:"id" gorm:"index:idx_sec_group_target_time,priority:5;index:idx_sec_group_cleanup,priority:3"`
	TriggerKey     string  `json:"-" gorm:"type:varchar(96);uniqueIndex:idx_sec_group_trigger"`
	ActiveKey      *string `json:"-" gorm:"type:varchar(96);uniqueIndex:idx_sec_group_active"`
	TargetUserID   int     `json:"-" gorm:"index:idx_sec_group_target_time,priority:1"`
	AuthVersion    int64   `json:"-" gorm:"index:idx_sec_group_target_time,priority:2"`
	TargetRole     int     `json:"-"`
	PolicyAt       int64   `json:"-" gorm:"index:idx_sec_group_target_time,priority:3"`
	TriggerEventID string  `json:"-" gorm:"type:varchar(64)"`
	TriggerAt      int64   `json:"-"`
	LastTriggerAt  int64   `json:"-"`
	CreatedAt      int64   `json:"-" gorm:"index:idx_sec_group_target_time,priority:4;index:idx_sec_group_cleanup,priority:2"`
	State          string  `json:"state" gorm:"type:varchar(16);index:idx_sec_group_cleanup,priority:1"`
	StoppedAt      int64   `json:"-"`
	BanActionID    int64   `json:"-"`
}

func (SecurityResponseGroup) TableName() string { return "audit_security_response_groups" }

type SecurityResponseLink struct {
	AlertID int64 `gorm:"primaryKey;index:idx_sec_group_alert,priority:2"`
	GroupID int64 `gorm:"primaryKey;index:idx_sec_group_alert,priority:1"`
}

func (SecurityResponseLink) TableName() string { return "audit_security_response_links" }

// ClaimSecurityResponseGroup is called before locking/updating any alert. An
// event receipt permanently links later enrichment to the original group.
func ClaimSecurityResponseGroup(tx *gorm.DB, event SecurityEvent, policyAt int64, identity AuditIdentity, now int64) (*SecurityResponseGroup, error) {
	var receipt SecurityEvent
	if err := tx.Select("event_id", "response_group_id").Where("event_id = ?", event.EventID).Take(&receipt).Error; err != nil {
		return nil, err
	}
	var group SecurityResponseGroup
	if receipt.ResponseGroupID != nil {
		err := lockForUpdate(tx).Where("id = ?", *receipt.ResponseGroupID).Take(&group).Error
		return &group, err
	}
	active := SecurityAccountRef(fmt.Sprintf("response-active:%d:%d:%d", event.ActorID, identity.Version(), policyAt))
	err := lockForUpdate(tx).Where("active_key = ?", active).Take(&group).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if group.ID == 0 {
		var legacy int64
		if err := tx.Model(&SecurityAction{}).Where("target_user_id = ? AND outcome IN ? AND response_group_id IS NULL", event.ActorID, []string{"pending", "committed", "sync_pending", "clearing"}).Count(&legacy).Error; err != nil {
			return nil, err
		}
		if legacy > 0 {
			return nil, nil
		}
		var previous SecurityResponseGroup
		if err := tx.Select("id", "last_trigger_at").Where("target_user_id = ? AND auth_version = ? AND policy_at = ?", event.ActorID, identity.Version(), policyAt).Order("created_at DESC, id DESC").Limit(1).Find(&previous).Error; err != nil {
			return nil, err
		}
		if previous.ID > 0 && event.At <= previous.LastTriggerAt {
			return nil, nil
		}
		group = SecurityResponseGroup{TriggerKey: SecurityAccountRef(fmt.Sprintf("response-trigger:%d:%s:%d:%d", event.ActorID, event.EventID, identity.Version(), policyAt)), ActiveKey: &active, TargetUserID: event.ActorID, AuthVersion: identity.Version(), TargetRole: identity.Role, PolicyAt: policyAt, TriggerEventID: event.EventID, TriggerAt: event.At, LastTriggerAt: event.At, CreatedAt: now, State: "active"}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&group).Error; err != nil {
			return nil, err
		}
		if err := lockForUpdate(tx).Where("active_key = ?", active).Take(&group).Error; err != nil {
			return nil, err
		}
	} else if event.At > group.LastTriggerAt {
		group.LastTriggerAt = event.At
		if err := tx.Model(&group).Update("last_trigger_at", event.At).Error; err != nil {
			return nil, err
		}
	}
	if err := tx.Model(&SecurityEvent{}).Where("event_id = ? AND response_group_id IS NULL", event.EventID).Update("response_group_id", group.ID).Error; err != nil {
		return nil, err
	}
	return &group, nil
}

func LockSecurityResponseGroup(tx *gorm.DB, action SecurityAction) (*SecurityResponseGroup, error) {
	if action.ResponseGroupID == nil {
		return nil, nil
	}
	var group SecurityResponseGroup
	err := lockForUpdate(tx).Where("id = ?", *action.ResponseGroupID).Take(&group).Error
	if err != nil {
		return nil, err
	}
	var ids []int64
	if err := tx.Model(&SecurityResponseLink{}).Where("group_id = ?", group.ID).Order("alert_id").Pluck("alert_id", &ids).Error; err != nil {
		return nil, err
	}
	// Every group writer uses this order, including stop and batch recovery.
	for _, id := range ids {
		var alert SecurityAlert
		if err := lockForUpdate(tx).Select("id").Where("id = ?", id).Take(&alert).Error; err != nil {
			return nil, err
		}
	}
	return &group, nil
}

// FinishSecurityResponseGroup releases its active slot only after all committed
// work has a terminal receipt. It must be called while holding the group lock.
func FinishSecurityResponseGroup(tx *gorm.DB, action SecurityAction) error {
	if action.ResponseGroupID == nil {
		return nil
	}
	var active int64
	if err := tx.Model(&SecurityAction{}).Where("response_group_id = ? AND outcome IN ?", *action.ResponseGroupID, []string{"pending", "committed", "sync_pending", "clearing"}).Count(&active).Error; err != nil {
		return err
	}
	if active == 0 {
		return tx.Model(&SecurityResponseGroup{}).Where("id = ?", *action.ResponseGroupID).Updates(map[string]any{"state": "complete", "active_key": nil}).Error
	}
	return nil
}

// InvalidateSecurityResponseAlerts updates every linked view after a receipt
// changes; the caller has already locked the group and canonical alert.
func InvalidateSecurityResponseAlerts(tx *gorm.DB, action SecurityAction) error {
	ids := []int64{action.AlertID}
	if action.ResponseGroupID != nil {
		if err := tx.Model(&SecurityResponseLink{}).Where("group_id = ?", *action.ResponseGroupID).Order("alert_id").Pluck("alert_id", &ids).Error; err != nil {
			return err
		}
	}
	if err := tx.Model(&SecurityAlert{}).Where("id IN ?", ids).Updates(map[string]any{"version": gorm.Expr("version + 1"), "updated_at": time.Now().Unix()}).Error; err != nil {
		return err
	}
	if err := FinishSecurityResponseGroup(tx, action); err != nil {
		return err
	}
	return AdvanceAuditActivityRevision(tx)
}

func StopSecurityResponseGroup(ctx context.Context, db *gorm.DB, identity AuthSessionIdentity, alertID, version, groupID int64, requestID string) (SecurityAction, error) {
	var receipt SecurityAction
	key := SecurityAccountRef(fmt.Sprintf("response-stop:%d:%d:%s", groupID, identity.UserID, requestID))
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		group, err := LockSecurityResponseGroup(tx, SecurityAction{ResponseGroupID: &groupID})
		if err != nil {
			return err
		}
		var link SecurityResponseLink
		if err := tx.Where("alert_id = ? AND group_id = ?", alertID, groupID).Take(&link).Error; err != nil {
			return err
		}
		var alert SecurityAlert
		if err := lockForUpdate(tx).Where("id = ?", alertID).Take(&alert).Error; err != nil {
			return err
		}
		var operator User
		if err := lockForUpdate(tx).Select("id", "role", "status", "auth_version").Where("id = ?", identity.UserID).Take(&operator).Error; err != nil {
			return err
		}
		if operator.Role != common.RoleRootUser || operator.Status != common.UserStatusEnabled || operator.AuthVersion != identity.UserAuthVersion {
			return ErrSecurityConflict
		}
		if _, isPAT := ParseAccessTokenSessionID(identity.SessionID); isPAT {
			return ErrSecurityConflict
		}
		if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
			return ErrSecurityConflict
		}
		if err := tx.Where("execution_key = ?", key).Take(&receipt).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if alert.Version != version || alert.PurgeState != "" || alert.ActorID != group.TargetUserID || group.State != "active" {
			return ErrSecurityConflict
		}
		now := time.Now().Unix()
		if err := tx.Model(group).Update("stopped_at", now).Error; err != nil {
			return err
		}
		if err := tx.Model(&SecurityAction{}).Where("response_group_id = ? AND outcome = ?", groupID, "pending").Updates(map[string]any{"outcome": "skipped", "reason_code": "stopped"}).Error; err != nil {
			return err
		}
		if err := tx.Model(&SecurityAction{}).Where("response_group_id = ? AND outcome = ?", groupID, "clearing").Updates(map[string]any{"outcome": "partial", "reason_code": "stopped"}).Error; err != nil {
			return err
		}
		receipt = SecurityAction{ExecutionKey: &key, AlertID: alertID, ResponseGroupID: &groupID, ActorID: identity.UserID, ActorRole: common.RoleRootUser, TargetUserID: group.TargetUserID, Action: "stop_automatic_response", Source: "human", Outcome: "succeeded", ReasonCode: "stopped", CreatedAt: now}
		if err := tx.Create(&receipt).Error; err != nil {
			return err
		}
		return InvalidateSecurityResponseAlerts(tx, receipt)
	})
	return receipt, err
}
