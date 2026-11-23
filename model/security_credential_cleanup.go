package model

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
)

// ProcessSecurityCredentialCleanup advances at most one bounded batch. Every
// destructive DB operation commits with its receipt. Cache I/O holds no DB lock.
// db and cache belong to the monitor; this path never falls back to global DB/RDB.
func ProcessSecurityCredentialCleanup(ctx context.Context, db *gorm.DB, cache *redis.Client, actionID int64) (SecurityAction, error) {
	action, err := prepareSecurityCredentialCleanup(ctx, db, actionID)
	if err != nil || action.Action != "clear_api_keys" || action.Outcome != "sync_pending" {
		return action, err
	}
	var result SecurityResponseResult
	if err := common.UnmarshalJsonStr(action.Result, &result); err != nil {
		return action, err
	}
	if len(result.PendingIDs) == 0 || len(result.PendingIDs) > 100 || result.StartedAt == 0 {
		return action, errors.New("invalid credential cleanup receipt")
	}
	if common.RedisEnabled {
		if cache == nil {
			return action, errors.New("credential cleanup cache unavailable")
		}
		// Include manually deleted rows: their cache may still need fencing.
		var tokens []Token
		if err := db.WithContext(ctx).Unscoped().Select("id", "key").Where("user_id = ? AND id IN ?", action.TargetUserID, result.PendingIDs).Find(&tokens).Error; err != nil {
			return action, err
		}
		const script = `redis.call('SET', KEYS[2], '1', 'EX', ARGV[1]); return redis.call('DEL', KEYS[1])`
		pipe := cache.Pipeline()
		defer pipe.Close()
		for _, token := range tokens {
			pipe.Eval(ctx, script, []string{getTokenCacheKey(token.Key), getTokenCacheFenceKey(token.Key)}, tokenCacheFenceSeconds)
		}
		if len(tokens) > 0 {
			commands, err := pipe.Exec(ctx)
			if err != nil {
				return action, err
			}
			for _, command := range commands {
				if err := command.Err(); err != nil {
					return action, err
				}
			}
		}
	}
	prepared := action
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := LockSecurityResponseGroup(tx, action); err != nil {
			return err
		}
		var alert SecurityAlert
		if err := lockForUpdate(tx).Select("id").Where("id = ?", action.AlertID).Take(&alert).Error; err != nil {
			return err
		}
		var current SecurityAction
		if err := lockForUpdate(tx).Select(SecurityResponseColumns).Where("id = ?", action.ID).Take(&current).Error; err != nil {
			return err
		}
		if current.Outcome != "sync_pending" || current.Result != action.Result {
			action = current
			return nil
		}
		// A committed batch finishes even if policy/identity changed. It can
		// delete only the IDs already disabled, never choose another batch here.
		deleted := tx.Where("user_id = ? AND id IN ?", action.TargetUserID, result.PendingIDs).Delete(&Token{})
		if deleted.Error != nil {
			return deleted.Error
		}
		result.AffectedCount += deleted.RowsAffected
		result.PendingIDs = nil
		action.Outcome, action.ReasonCode, action.Attempts = "clearing", "", 0
		action.NextAttemptAt = time.Now().Unix() + 1
		if result.BatchComplete {
			action.Outcome = "succeeded"
		}
		return saveSecurityCleanupReceipt(tx, &action, result)
	})
	if err != nil {
		return prepared, err
	}
	return action, err
}

func prepareSecurityCredentialCleanup(ctx context.Context, db *gorm.DB, actionID int64) (SecurityAction, error) {
	var action SecurityAction
	if err := db.WithContext(ctx).Select(SecurityResponseColumns).Where("id = ? AND action IN ? AND source = ?", actionID, []string{"clear_api_keys", "clear_access_tokens"}, "automatic").Take(&action).Error; err != nil {
		return action, err
	}
	original := action
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := LockSecurityResponseGroup(tx, action); err != nil {
			return err
		}
		var alert SecurityAlert
		if err := lockForUpdate(tx).Select("id", "actor_id", "purge_state").Where("id = ?", action.AlertID).Take(&alert).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Select(SecurityResponseColumns).Where("id = ?", action.ID).Take(&action).Error; err != nil {
			return err
		}
		if action.Outcome != "pending" && action.Outcome != "clearing" {
			return nil
		}
		var result SecurityResponseResult
		if err := common.UnmarshalJsonStr(action.Result, &result); err != nil {
			return err
		}
		user, reason, err := authorizeSecurityCleanup(tx, alert, action, result)
		if err != nil {
			return err
		}
		if reason == "dependency_pending" {
			action.NextAttemptAt = time.Now().Unix() + 1
			return saveSecurityCleanupReceipt(tx, &action, result)
		}
		if reason != "" {
			action.Outcome, action.ReasonCode = "skipped", reason
			if result.StartedAt > 0 {
				action.Outcome = "partial"
			}
			return saveSecurityCleanupReceipt(tx, &action, result)
		}
		if result.StartedAt == 0 {
			result.StartedAt, result.CountKnown = time.Now().Unix(), true
			if action.Action == "clear_api_keys" {
				var upper Token
				if err := tx.Select("id").Where("user_id = ?", user.Id).Order("deleted_at DESC, id DESC").Limit(1).Find(&upper).Error; err != nil {
					return err
				}
				result.UpperID = upper.Id
			}
		}
		action.ReasonCode = ""
		if action.Action == "clear_access_tokens" {
			count, err := DeleteUserAccessTokensWithTx(tx, user.Id)
			if err != nil {
				return err
			}
			if strings.TrimSpace(user.GetAccessToken()) != "" {
				count++
			}
			if user.AccessToken != nil || user.AccessTokenCreatedAt != nil {
				if err := tx.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]any{"access_token": nil, "access_token_created_at": nil}).Error; err != nil {
					return err
				}
			}
			result.AffectedCount = count
			action.Outcome = "succeeded"
			return saveSecurityCleanupReceipt(tx, &action, result)
		}
		var ids []int
		if err := tx.Model(&Token{}).Where("user_id = ?", user.Id).Order("deleted_at, id").Limit(100).Pluck("id", &ids).Error; err != nil {
			return err
		}
		result.BatchComplete = len(ids) < 100
		for len(ids) > 0 && ids[len(ids)-1] > result.UpperID {
			ids = ids[:len(ids)-1]
			result.BatchComplete = true
		}
		result.PendingIDs = ids
		if len(ids) == 0 {
			action.Outcome = "succeeded"
		} else {
			if err := tx.Model(&Token{}).Where("user_id = ? AND id IN ?", user.Id, ids).Update("status", common.TokenStatusDisabled).Error; err != nil {
				return err
			}
			action.Outcome = "sync_pending"
		}
		return saveSecurityCleanupReceipt(tx, &action, result)
	})
	if err != nil {
		return original, err
	}
	return action, err
}

// All new batches require current policy and identity. Previously prepared IDs
// bypass this check only to finish their existing revocation, not to expand it.
func authorizeSecurityCleanup(tx *gorm.DB, alert SecurityAlert, action SecurityAction, result SecurityResponseResult) (User, string, error) {
	var user User
	if action.ResponseGroupID != nil {
		var group SecurityResponseGroup
		if err := tx.Where("id = ?", *action.ResponseGroupID).Take(&group).Error; err != nil {
			return user, "", err
		}
		if group.StoppedAt > 0 {
			return user, "stopped", nil
		}
		result.BanActionID = group.BanActionID
	}
	if alert.PurgeState != "" || action.TargetUserID <= 0 || action.TargetUserID != alert.ActorID {
		return user, "invalid_target", nil
	}
	cfg := operation_setting.GetAuditSecuritySetting()
	rules, err := operation_setting.ParseAutoBanRules(cfg.AutoBanRulesJSON)
	if err != nil {
		return user, "", err
	}
	if !cfg.Enabled || cfg.Suspended || !cfg.AutoBanEnabled || !slices.Contains(rules, result.Rule) || !result.Rule.AllowsAction(action.Action) || result.Rule.ID == "session_reuse" && !cfg.CaptureEnabled {
		return user, "disabled", nil
	}
	now := time.Now().Unix()
	if result.StartedAt == 0 && result.EvidenceExpiresAt > 0 && result.EvidenceExpiresAt <= now {
		return user, "expired", nil
	}
	if result.PolicyAt != cfg.AutoBanEffectiveAt || action.EventAt <= cfg.AutoBanEffectiveAt || action.EventAt > now || result.StartedAt == 0 && action.EventAt < now-120 || result.StartedAt > 0 && now-result.StartedAt >= 600 {
		return user, "expired", nil
	}
	expectedVersion := result.TargetAuthVersion
	if result.BanActionID != 0 {
		var ban SecurityAction
		query := tx.Select("result", "outcome").Where("id = ? AND target_user_id = ? AND action = ?", result.BanActionID, action.TargetUserID, "disable_account")
		if action.ResponseGroupID != nil {
			query = query.Where("response_group_id = ?", *action.ResponseGroupID)
		} else {
			query = query.Where("alert_id = ? AND event_id = ?", action.AlertID, action.EventID)
		}
		if err := query.Take(&ban).Error; err != nil {
			return user, "", err
		}
		var receipt SecurityResponseResult
		if err := common.UnmarshalJsonStr(ban.Result, &receipt); err != nil {
			return user, "", err
		}
		if receipt.AuthVersion != 0 {
			if receipt.AuthVersion != expectedVersion+1 || receipt.TargetAuthVersion != expectedVersion {
				return user, "superseded", nil
			}
			expectedVersion = receipt.AuthVersion
		} else if ban.Outcome == "pending" {
			return user, "dependency_pending", nil
		}
	}
	err = lockForUpdate(tx).Select("id", "role", "status", "auth_version", "access_token", "access_token_created_at").Where("id = ?", action.TargetUserID).Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return user, "invalid_target", nil
	}
	if err != nil {
		return user, "", err
	}
	if user.Role >= common.RoleRootUser && (user.Role != common.RoleRootUser || cfg.AutoBanExcludeRoot) {
		return user, "protected_account", nil
	}
	if user.Role != common.RoleCommonUser && user.Role != common.RoleAdminUser && user.Role != common.RoleRootUser {
		return user, "invalid_target", nil
	}
	if expectedVersion <= 0 || user.AuthVersion != expectedVersion || user.Role != result.TargetRole {
		return user, "superseded", nil
	}
	excluded, err := operation_setting.ParseAutoBanExcludedUsers(cfg.AutoBanExcludedUserIDsJSON)
	if err != nil {
		return user, "", err
	}
	if slices.Contains(excluded, user.Id) {
		return user, "excluded_account", nil
	}
	if !cfg.AutoBanIncludeStaff && user.Role != common.RoleRootUser {
		var employees int64
		if err := tx.Model(&EmployeeProfile{}).Where("user_id = ? AND status = ?", user.Id, 1).Count(&employees).Error; err != nil {
			return user, "", err
		}
		if user.Role >= common.RoleAdminUser || employees > 0 {
			return user, "excluded_account", nil
		}
	}
	return user, "", nil
}

// Caller owns alert/action locks. Receipt changes and dashboard invalidation
// commit together, including for a zero-count cleanup.
func saveSecurityCleanupReceipt(tx *gorm.DB, action *SecurityAction, result SecurityResponseResult) error {
	raw, err := common.Marshal(result)
	if err != nil {
		return err
	}
	action.Result = string(raw)
	if err := tx.Model(&SecurityAction{}).Where("id = ?", action.ID).Updates(map[string]any{"outcome": action.Outcome, "reason_code": action.ReasonCode, "result": action.Result, "attempts": action.Attempts, "next_attempt_at": action.NextAttemptAt}).Error; err != nil {
		return err
	}
	return InvalidateSecurityResponseAlerts(tx, *action)
}
