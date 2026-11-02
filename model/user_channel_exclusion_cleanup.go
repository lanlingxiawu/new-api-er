package model

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const SystemTaskTypeUserChannelExclusionCleanup = "user_channel_exclusion_cleanup"

// A pending user survives a crash between the DB commit and cache publication.
type UserChannelExclusionCleanup struct {
	ChannelID     int `gorm:"primaryKey;autoIncrement:false;index:idx_exclusion_cleanup_due,priority:2"`
	AfterUserID   int
	PendingUserID *int
	NextAttemptAt int64 `gorm:"index:idx_exclusion_cleanup_due,priority:1"`
	Attempts      int
	LastError     string `gorm:"type:varchar(32)"`
	UpdatedAt     int64
}

// Called before deletion while the channel rows are locked. A failed enqueue
// rolls back channel deletion. No user rows are visited in this transaction.
func enqueueUserChannelExclusionCleanup(tx *gorm.DB, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	var existing []int
	if err := lockForUpdate(tx).Model(&Channel{}).Where("id IN ?", ids).Order("id").Pluck("id", &existing).Error; err != nil {
		return err
	}
	rows := make([]UserChannelExclusionCleanup, len(existing))
	for i, id := range existing {
		rows[i] = UserChannelExclusionCleanup{ChannelID: id}
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, 100).Error
}

func DueUserChannelExclusionCleanups(ctx context.Context) ([]UserChannelExclusionCleanup, error) {
	var rows []UserChannelExclusionCleanup
	err := DB.WithContext(ctx).Where("next_attempt_at <= ?", common.GetTimestamp()).Order("next_attempt_at, channel_id").Limit(10).Find(&rows).Error
	return rows, err
}

// Holding the existing per-type lease row through the short DB transaction
// prevents a stale worker from advancing progress after another runner claims it.
func lockUserChannelExclusionCleanupLease(tx *gorm.DB, taskID, runnerID string) error {
	var lease SystemTaskLock
	err := lockForUpdate(tx).Where("type = ? AND task_id = ? AND locked_by = ? AND locked_until > ?",
		SystemTaskTypeUserChannelExclusionCleanup, taskID, runnerID, common.GetTimestamp()).First(&lease).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrSystemTaskLockLost
	}
	return err
}

// Returns 0 when the queue entry has completed. Pending users are returned
// unchanged on replay, including when their relationship was already deleted.
func PrepareUserChannelExclusionCleanup(ctx context.Context, channelID int, taskID, runnerID string) (int, error) {
	var userID int
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockUserChannelExclusionCleanupLease(tx, taskID, runnerID); err != nil {
			return err
		}
		var queue UserChannelExclusionCleanup
		if err := tx.First(&queue, "channel_id = ?", channelID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if queue.PendingUserID != nil {
			userID = *queue.PendingUserID
			return nil
		}
		var relation UserChannelExclusion
		err := tx.Where("channel_id = ? AND user_id > ?", channelID, queue.AfterUserID).Order("user_id").First(&relation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Delete(&queue).Error
		}
		if err != nil {
			return err
		}
		userID = relation.UserID
		var user User
		err = tx.Unscoped().Select("id", "profile_version").First(&user, userID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		userExists := err == nil
		if err := tx.Where("user_id = ? AND channel_id = ?", userID, channelID).Delete(&UserChannelExclusion{}).Error; err != nil {
			return err
		}
		if userExists {
			ids := make([]int, 0)
			if err := tx.Model(&UserChannelExclusion{}).Where("user_id = ?", userID).Order("channel_id").Pluck("channel_id", &ids).Error; err != nil {
				return err
			}
			raw, err := common.Marshal(ids)
			if err != nil {
				return err
			}
			result := tx.Unscoped().Model(&User{}).Where("id = ? AND profile_version = ?", userID, user.ProfileVersion).
				Updates(map[string]any{"excluded_channel_ids": string(raw), "profile_version": gorm.Expr("profile_version + ?", 1)})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("user exclusion profile changed concurrently")
			}
		}
		return tx.Model(&queue).Updates(map[string]any{"pending_user_id": userID, "updated_at": common.GetTimestamp()}).Error
	})
	return userID, err
}

func PublishUserChannelExclusionCleanup(ctx context.Context, userID int) error {
	if !common.RedisEnabled {
		return nil
	}
	var user User
	err := DB.WithContext(ctx).Unscoped().First(&user, userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !user.DeletedAt.Valid {
		return writeUserCacheContext(ctx, user.ToBaseUser(), false)
	}
	// Never revive a soft-deleted user's hash. Fence delayed profile fills only.
	const script = `if tonumber(redis.call('GET', KEYS[1]) or '0') < tonumber(ARGV[1]) then redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[2]) end return 1`
	return common.RDB.Eval(ctx, script, []string{getUserProfileVersionKey(userID)}, user.ProfileVersion, userAuthFenceTTLSeconds()).Err()
}

func AcknowledgeUserChannelExclusionCleanup(ctx context.Context, channelID, userID int, taskID, runnerID string) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockUserChannelExclusionCleanupLease(tx, taskID, runnerID); err != nil {
			return err
		}
		return tx.Model(&UserChannelExclusionCleanup{}).Where("channel_id = ? AND pending_user_id = ?", channelID, userID).
			Updates(map[string]any{"after_user_id": userID, "pending_user_id": nil, "attempts": 0, "last_error": "", "updated_at": common.GetTimestamp()}).Error
	})
}

func DeferUserChannelExclusionCleanup(ctx context.Context, channelID int, reason, taskID, runnerID string) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockUserChannelExclusionCleanupLease(tx, taskID, runnerID); err != nil {
			return err
		}
		return tx.Model(&UserChannelExclusionCleanup{}).Where("channel_id = ?", channelID).Updates(map[string]any{
			"next_attempt_at": time.Now().Add(time.Minute).Unix(), "attempts": gorm.Expr("attempts + ?", 1), "last_error": reason, "updated_at": common.GetTimestamp(),
		}).Error
	})
}
