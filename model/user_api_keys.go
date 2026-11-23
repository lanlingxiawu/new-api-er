package model

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var ErrUserAPIKeyClearForbidden = errors.New("API key clearing is forbidden")

// UserTokenDeletionUpperBound freezes the scope without loading any credentials.
func UserTokenDeletionUpperBound(ctx context.Context, userID int) (int, error) {
	var token Token
	err := DB.WithContext(ctx).Select("id").Where("user_id = ?", userID).Order("deleted_at DESC, id DESC").Limit(1).Find(&token).Error
	return token.Id, err
}

// DeleteUserTokensBatch is the fail-closed administrative revocation path.
// Disabled rows survive cache failures, so a fresh operation can safely retry.
// Redis is never called while holding a database transaction or row lock.
func DeleteUserTokensBatch(ctx context.Context, operatorID, userID, upperID int) (int64, bool, error) {
	if operatorID <= 0 || userID <= 0 || operatorID == userID {
		return 0, false, ErrUserAPIKeyClearForbidden
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var ids []int
	var tokens []Token
	done := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Match access-token clearing: user rows are locked in a fixed order,
		// and role/status changes cannot commit between authorization and disable.
		var operator, target User
		for _, id := range []int{min(operatorID, userID), max(operatorID, userID)} {
			user := &operator
			if id == userID {
				user = &target
			}
			if err := lockForUpdate(tx).Select("id", "role", "status").First(user, id).Error; err != nil {
				return err
			}
		}
		if operator.Status != common.UserStatusEnabled || operator.Role < common.RoleAdminUser || operator.Role <= target.Role {
			return ErrUserAPIKeyClearForbidden
		}
		if err := tx.Model(&Token{}).Where("user_id = ?", userID).Order("deleted_at, id").Limit(100).Pluck("id", &ids).Error; err != nil {
			return fmt.Errorf("select API keys for revocation: %w", err)
		}
		done = len(ids) < 100
		// Apply the upper bound after the covered scan, avoiding MySQL index merge.
		for len(ids) > 0 && ids[len(ids)-1] > upperID {
			ids = ids[:len(ids)-1]
			done = true
		}
		if len(ids) == 0 {
			return nil
		}
		if err := tx.Select("id", "key").Where("user_id = ? AND id IN ?", userID, ids).Find(&tokens).Error; err != nil {
			return fmt.Errorf("load API keys for revocation: %w", err)
		}
		return tx.Model(&Token{}).Where("user_id = ? AND id IN ?", userID, ids).Update("status", common.TokenStatusDisabled).Error
	})
	if err != nil {
		return 0, false, err
	}
	if len(ids) == 0 {
		return 0, true, nil
	}
	if common.RedisEnabled {
		// Each script atomically fences and evicts one credential. Bounded
		// pipelines avoid per-key network round trips; every command is checked.
		const script = `redis.call('SET', KEYS[2], '1', 'EX', ARGV[1]); return redis.call('DEL', KEYS[1])`
		pipe := common.RDB.Pipeline()
		defer pipe.Close()
		for _, token := range tokens {
			pipe.Eval(ctx, script, []string{getTokenCacheKey(token.Key), getTokenCacheFenceKey(token.Key)}, tokenCacheFenceSeconds)
		}
		commands, err := pipe.Exec(ctx)
		if err != nil {
			return 0, false, fmt.Errorf("invalidate API key caches: %w", err)
		}
		for _, command := range commands {
			if err := command.Err(); err != nil {
				return 0, false, fmt.Errorf("invalidate API key cache: %w", err)
			}
		}
	}
	result := DB.WithContext(ctx).Where("user_id = ? AND id IN ?", userID, ids).Delete(&Token{})
	if result.Error != nil {
		// A driver timeout can leave the commit outcome uncertain. Reconcile
		// only this batch, with a separate short budget, and never re-enable it.
		checkCtx, checkCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer checkCancel()
		var deleted int64
		checkErr := DB.WithContext(checkCtx).Unscoped().Model(&Token{}).Where("user_id = ? AND id IN ? AND deleted_at IS NOT NULL", userID, ids).Count(&deleted).Error
		if checkErr != nil {
			return 0, false, fmt.Errorf("delete API keys and reconcile failed: %w", result.Error)
		}
		return deleted, false, fmt.Errorf("delete API keys: %w", result.Error)
	}
	return result.RowsAffected, done, nil
}
