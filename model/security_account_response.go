package model

import (
	"context"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

// SecurityResponseColumns keeps worker reads independent of investigation notes.
const SecurityResponseColumns = "id,alert_id,actor_id,actor_role,action,source,outcome,reason_code,target_user_id,event_id,event_at,detected_at,created_at,next_attempt_at,attempts,result,response_group_id"

// Stored only in the action receipt; never included in a dashboard projection.
type SecurityResponseResult struct {
	EvidenceExpiresAt   int64                         `json:"evidence_expires_at,omitempty"`
	PolicyAt            int64                         `json:"policy_at"`
	Rule                operation_setting.AutoBanRule `json:"rule"`
	AuthVersion         int64                         `json:"auth_version"`
	OperatorAuthVersion int64                         `json:"operator_auth_version,omitempty"`
	TargetAuthVersion   int64                         `json:"target_auth_version,omitempty"`
	TargetRole          int                           `json:"target_role,omitempty"`
	BanActionID         int64                         `json:"ban_action_id,omitempty"`
	StartedAt           int64                         `json:"started_at,omitempty"`
	UpperID             int                           `json:"upper_id,omitempty"`
	PendingIDs          []int                         `json:"pending_ids,omitempty"`
	BatchComplete       bool                          `json:"batch_complete,omitempty"`
	AffectedCount       int64                         `json:"affected_count,omitempty"`
	CountKnown          bool                          `json:"count_known,omitempty"`
}

// ResponseSummary exposes only confirmed counts, never the recovery metadata.
func (a SecurityAction) ResponseSummary() SecurityAccountResponse {
	response := SecurityAccountResponse{ResponseGroupID: a.ResponseGroupID, ID: a.ID, Action: a.Action, Source: a.Source, Outcome: a.Outcome, ReasonCode: a.ReasonCode, TargetUserID: a.TargetUserID, CreatedAt: a.CreatedAt}
	if a.Action == "clear_api_keys" || a.Action == "clear_access_tokens" {
		var result SecurityResponseResult
		if common.UnmarshalJsonStr(a.Result, &result) == nil && result.CountKnown && result.AffectedCount >= 0 {
			response.AffectedCount = &result.AffectedCount
		}
	}
	return response
}

// DisableUserForSecurity commits a narrow status/version update and its receipt
// atomically. Redis is deliberately absent from this transaction.
func DisableUserForSecurity(ctx context.Context, db *gorm.DB, actionID int64) (SecurityAction, error) {
	var action SecurityAction
	if err := db.WithContext(ctx).Where("id = ? AND action = ?", actionID, "disable_account").Take(&action).Error; err != nil {
		return action, err
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		group, err := LockSecurityResponseGroup(tx, action)
		if err != nil {
			return err
		}
		var alert SecurityAlert
		if err := lockForUpdate(tx).Where("id = ?", action.AlertID).Take(&alert).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("id = ?", actionID).Take(&action).Error; err != nil {
			return err
		}
		if action.Outcome != "pending" {
			return nil
		}
		var result SecurityResponseResult
		if err := common.UnmarshalJsonStr(action.Result, &result); err != nil {
			return err
		}
		reason := ""
		if group != nil && group.StoppedAt > 0 {
			reason = "stopped"
		}
		if alert.PurgeState != "" || action.TargetUserID <= 0 || action.TargetUserID != alert.ActorID {
			reason = "invalid_target"
		}
		cfg := operation_setting.GetAuditSecuritySetting()
		if reason == "" && action.Source == "automatic" {
			rules, err := operation_setting.ParseAutoBanRules(cfg.AutoBanRulesJSON)
			if err != nil {
				return err
			}
			selected := false
			for _, r := range rules {
				if r == result.Rule && r.Selected {
					selected = true
				}
			}
			switch {
			case !cfg.Enabled || cfg.Suspended || !cfg.AutoBanEnabled || !selected || result.Rule.ID == "session_reuse" && !cfg.CaptureEnabled:
				reason = "disabled"
			case result.PolicyAt != cfg.AutoBanEffectiveAt || action.EventAt <= cfg.AutoBanEffectiveAt || action.EventAt < time.Now().Unix()-120 || action.EventAt > time.Now().Unix():
				reason = "expired"
			case result.EvidenceExpiresAt > 0 && result.EvidenceExpiresAt <= time.Now().Unix():
				reason = "expired"
			}
		}
		var user User
		if reason == "" {
			err := lockForUpdate(tx).Select("id", "role", "status", "auth_version").Where("id = ?", action.TargetUserID).Take(&user).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				reason = "invalid_target"
			} else if err != nil {
				return err
			}
		}
		if reason == "" {
			switch {
			case user.Role >= common.RoleRootUser && (user.Role != common.RoleRootUser || action.Source != "automatic" || cfg.AutoBanExcludeRoot):
				reason = "protected_account"
			case user.Status != common.UserStatusEnabled:
				reason = "already_disabled"
			case user.Role != common.RoleCommonUser && user.Role != common.RoleAdminUser && user.Role != common.RoleRootUser:
				reason = "invalid_target"
			}
		}
		if reason == "" && action.Source == "automatic" {
			if result.TargetAuthVersion > 0 && (user.AuthVersion != result.TargetAuthVersion || user.Role != result.TargetRole) {
				reason = "superseded"
			}
			excluded, err := operation_setting.ParseAutoBanExcludedUsers(cfg.AutoBanExcludedUserIDsJSON)
			if err != nil {
				return err
			}
			if slices.Contains(excluded, user.Id) {
				reason = "excluded_account"
			}
			if reason == "" && !cfg.AutoBanIncludeStaff && user.Role != common.RoleRootUser {
				var employeeCount int64
				if err := tx.Model(&EmployeeProfile{}).Where("user_id = ? AND status = ?", user.Id, 1).Count(&employeeCount).Error; err != nil {
					return err
				}
				if user.Role >= common.RoleAdminUser || employeeCount > 0 {
					reason = "excluded_account"
				}
			}
		}
		if reason == "" && action.Source != "automatic" {
			var operator User
			if err := tx.Select("id", "role", "status", "auth_version").Where("id = ?", action.ActorID).Take(&operator).Error; err != nil {
				return err
			}
			if operator.Role <= user.Role || operator.Status != common.UserStatusEnabled || operator.AuthVersion != result.OperatorAuthVersion {
				reason = "forbidden"
			}
		}
		if reason != "" {
			action.Outcome, action.ReasonCode = "skipped", reason
		} else {
			if user.AuthVersion < 1 || user.AuthVersion == math.MaxInt64 {
				return ErrUserAuthVersionConflict
			}
			result.AuthVersion = user.AuthVersion + 1
			r := tx.Model(&User{}).Where("id = ? AND auth_version = ? AND status = ? AND role = ?", user.Id, user.AuthVersion, common.UserStatusEnabled, user.Role).Updates(map[string]any{"status": common.UserStatusDisabled, "auth_version": result.AuthVersion})
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected != 1 {
				return ErrUserAuthVersionConflict
			}
			action.Outcome = "committed"
		}
		raw, err := common.Marshal(result)
		if err != nil {
			return err
		}
		action.Result = string(raw)
		if err := tx.Model(&SecurityAction{}).Where("id = ? AND outcome = ?", action.ID, "pending").Updates(map[string]any{"outcome": action.Outcome, "reason_code": action.ReasonCode, "result": action.Result}).Error; err != nil {
			return err
		}
		return InvalidateSecurityResponseAlerts(tx, action)
	})
	return action, err
}

// PublishSecurityAccountState uses the independent monitoring pool and a bounded
// context. It preserves quota and re-reads before declaring propagation complete.
func PublishSecurityAccountState(ctx context.Context, db *gorm.DB, userID int, committedVersion int64) (bool, error) {
	columns := []string{"id", "username", "email", "role", "status", "quota", "group", "setting", "auth_version", "profile_version", "group_ratios", "excluded_channel_ids", "stream_response_timeout", "stream_response_timeout_mode", "stream_total_timeout", "non_stream_response_timeout", "non_stream_total_timeout", "non_stream_timeout_billing", "retry_times"}
	for range 3 {
		var user User
		if err := db.WithContext(ctx).Select(columns).Where("id = ?", userID).Take(&user).Error; err != nil {
			return false, err
		}
		if user.AuthVersion < committedVersion {
			return false, ErrUserAuthVersionConflict
		}
		if err := writeUserCacheContext(ctx, user.ToBaseUser(), false); err != nil {
			return false, err
		}
		var verified User
		if err := db.WithContext(ctx).Select("auth_version", "profile_version", "status", "role").Where("id = ?", userID).Take(&verified).Error; err != nil {
			return false, err
		}
		if user.AuthVersion == verified.AuthVersion && user.ProfileVersion == verified.ProfileVersion && user.Status == verified.Status && user.Role == verified.Role {
			return user.AuthVersion != committedVersion || user.Status != common.UserStatusDisabled, nil
		}
	}
	return false, ErrUserAuthVersionConflict
}

func FinishSecurityAccountResponse(ctx context.Context, db *gorm.DB, action SecurityAction, outcome, reason string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := LockSecurityResponseGroup(tx, action); err != nil {
			return err
		}
		var alert SecurityAlert
		if err := lockForUpdate(tx).Select("id").Where("id = ?", action.AlertID).Take(&alert).Error; err != nil {
			return err
		}
		r := tx.Model(&SecurityAction{}).Where("id = ? AND outcome = ? AND attempts = ? AND result = ?", action.ID, action.Outcome, action.Attempts, action.Result).Updates(map[string]any{"outcome": outcome, "reason_code": reason, "attempts": action.Attempts + 1, "next_attempt_at": time.Now().Unix() + 10})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			return nil
		}
		return InvalidateSecurityResponseAlerts(tx, action)
	})
}
