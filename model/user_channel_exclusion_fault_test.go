package model

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func exclusionCleanupLease(t *testing.T) SystemTaskLock {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&SystemTaskLock{}))
	lease := SystemTaskLock{Type: SystemTaskTypeUserChannelExclusionCleanup, TaskID: uniq("task"), LockedBy: uniq("worker"), LockedUntil: time.Now().Add(time.Minute).Unix()}
	require.NoError(t, DB.Create(&lease).Error)
	t.Cleanup(func() { DB.Where("type = ? AND task_id = ?", lease.Type, lease.TaskID).Delete(&SystemTaskLock{}) })
	return lease
}

// Inject errors at real SQL transaction boundaries, then assert persisted state.
func TestUserChannelExclusionTransactionRollback(t *testing.T) {
	requireDB(t)
	for _, mode := range []string{"single", "batch", "status"} {
		t.Run("enqueue/"+mode, func(t *testing.T) {
			ch := mkChannelWithAbilities(t, func(ch *Channel) { ch.Status = 987644; ch.Models = "rollback-model" })
			injected := errors.New("injected queue insert failure")
			name := uniq("exclusion_fault")
			require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
				if tx.Statement.Table == "user_channel_exclusion_cleanups" {
					tx.AddError(injected)
				}
			}))
			defer DB.Callback().Create().Remove(name)
			var err error
			switch mode {
			case "single":
				err = ch.Delete()
			case "batch":
				_, err = BatchDeleteChannels([]int{ch.Id})
			case "status":
				_, err = deleteChannelsByStatus(987644)
			}
			require.ErrorIs(t, err, injected)
			require.NoError(t, DB.First(&Channel{}, ch.Id).Error)
			var count int64
			require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", ch.Id).Count(&count).Error)
			require.EqualValues(t, 1, count)
			require.NoError(t, DB.Model(&UserChannelExclusionCleanup{}).Where("channel_id = ?", ch.Id).Count(&count).Error)
			require.Zero(t, count)
		})
	}
	t.Run("projection", func(t *testing.T) {
		ch := mkChannel(t, nil)
		u := mkUser(t, func(u *User) { u.ExcludedChannelIDs = fmt.Sprintf("[%d]", ch.Id) })
		lease := exclusionCleanupLease(t)
		require.NoError(t, DB.Create(&UserChannelExclusion{UserID: u.Id, ChannelID: ch.Id}).Error)
		t.Cleanup(func() {
			DB.Where("user_id = ?", u.Id).Delete(&UserChannelExclusion{})
			DB.Where("channel_id = ?", ch.Id).Delete(&UserChannelExclusionCleanup{})
		})
		require.NoError(t, ch.Delete())
		injected := errors.New("injected projection update failure")
		name := uniq("exclusion_fault")
		require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
			if tx.Statement.Table == "users" {
				tx.AddError(injected)
			}
		}))
		_, err := PrepareUserChannelExclusionCleanup(context.Background(), ch.Id, lease.TaskID, lease.LockedBy)
		require.NoError(t, DB.Callback().Update().Remove(name))
		require.ErrorIs(t, err, injected)
		require.NoError(t, DB.First(&UserChannelExclusion{}, "user_id = ? AND channel_id = ?", u.Id, ch.Id).Error)
		var stored User
		require.NoError(t, DB.First(&stored, u.Id).Error)
		require.Equal(t, u.ExcludedChannelIDs, stored.ExcludedChannelIDs)
		require.Equal(t, u.ProfileVersion, stored.ProfileVersion)
		var queue UserChannelExclusionCleanup
		require.NoError(t, DB.First(&queue, "channel_id = ?", ch.Id).Error)
		require.Nil(t, queue.PendingUserID)
		id, err := PrepareUserChannelExclusionCleanup(context.Background(), ch.Id, lease.TaskID, lease.LockedBy)
		require.NoError(t, err)
		require.Equal(t, u.Id, id)
	})
}

func TestUserChannelExclusionRedisFailureRecovery(t *testing.T) {
	u := cachedTestUser(t)
	ch := mkChannel(t, nil)
	lease := exclusionCleanupLease(t)
	ctx := context.Background()
	require.NoError(t, DB.Model(u).Update("excluded_channel_ids", fmt.Sprintf("[%d]", ch.Id)).Error)
	require.NoError(t, DB.Create(&UserChannelExclusion{UserID: u.Id, ChannelID: ch.Id}).Error)
	require.NoError(t, PublishUserAuthCache(u.Id))
	require.NoError(t, cacheDecrUserQuota(u.Id, 37))
	t.Cleanup(func() {
		DB.Where("user_id = ?", u.Id).Delete(&UserChannelExclusion{})
		DB.Where("channel_id = ?", ch.Id).Delete(&UserChannelExclusionCleanup{})
		common.RDB.Del(ctx, getUserProfileVersionKey(u.Id))
	})
	require.NoError(t, ch.Delete())
	id, err := PrepareUserChannelExclusionCleanup(ctx, ch.Id, lease.TaskID, lease.LockedBy)
	require.NoError(t, err)
	require.Equal(t, u.Id, id)
	healthy := common.RDB
	broken := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	require.NoError(t, broken.Close())
	common.RDB = broken
	err = PublishUserChannelExclusionCleanup(ctx, id)
	common.RDB = healthy
	require.Error(t, err)
	require.NoError(t, DeferUserChannelExclusionCleanup(ctx, ch.Id, "cache_publish", lease.TaskID, lease.LockedBy))
	var queue UserChannelExclusionCleanup
	require.NoError(t, DB.First(&queue, "channel_id = ?", ch.Id).Error)
	require.Equal(t, &u.Id, queue.PendingUserID)
	require.Zero(t, queue.AfterUserID)
	require.Equal(t, 1, queue.Attempts)
	require.Greater(t, queue.NextAttemptAt, common.GetTimestamp())
	// A restarted worker replays the same pending user after Redis recovers.
	id, err = PrepareUserChannelExclusionCleanup(ctx, ch.Id, lease.TaskID, lease.LockedBy)
	require.NoError(t, err)
	require.Equal(t, u.Id, id)
	require.NoError(t, PublishUserChannelExclusionCleanup(ctx, id))
	require.NoError(t, AcknowledgeUserChannelExclusionCleanup(ctx, ch.Id, id, lease.TaskID, lease.LockedBy))
	base, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)
	require.Equal(t, "[]", base.ExcludedChannelIDs)
	require.Equal(t, 463, base.Quota)
}

func TestUserChannelExclusionConcurrentSaveDelete(t *testing.T) {
	requireDB(t)
	lease := exclusionCleanupLease(t)
	for iteration := range 10 {
		ch := mkChannel(t, nil)
		keep := mkChannel(t, nil)
		u := mkUser(t, nil)
		t.Cleanup(func() {
			DB.Where("user_id = ?", u.Id).Delete(&UserChannelExclusion{})
			DB.Where("channel_id = ?", ch.Id).Delete(&UserChannelExclusionCleanup{})
		})
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, deleting := range []bool{false, true} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				var err error
				// Deadlock/SQLite busy victims retry the whole transaction, never a partial save.
				for range 20 {
					if deleting {
						err = ch.Delete()
					} else {
						err = DB.Transaction(func(tx *gorm.DB) error {
							ids, _, err := ReplaceUserChannelExclusions(tx, u.Id, []int{ch.Id, keep.Id})
							if err != nil {
								return err
							}
							raw, err := common.Marshal(ids)
							if err != nil {
								return err
							}
							return tx.Model(&User{}).Where("id = ?", u.Id).Updates(map[string]any{"excluded_channel_ids": string(raw), "profile_version": gorm.Expr("profile_version + 1")}).Error
						})
					}
					if err == nil {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
				results <- err
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		for err := range results {
			require.NoError(t, err, "iteration %d", iteration)
		}
		for range 3 {
			id, err := PrepareUserChannelExclusionCleanup(context.Background(), ch.Id, lease.TaskID, lease.LockedBy)
			require.NoError(t, err)
			if id == 0 {
				break
			}
			require.NoError(t, AcknowledgeUserChannelExclusionCleanup(context.Background(), ch.Id, id, lease.TaskID, lease.LockedBy))
		}
		var stored User
		require.NoError(t, DB.First(&stored, u.Id).Error)
		require.Equal(t, fmt.Sprintf("[%d]", keep.Id), stored.ExcludedChannelIDs)
		var ids []int
		require.NoError(t, DB.Model(&UserChannelExclusion{}).Where("user_id = ?", u.Id).Pluck("channel_id", &ids).Error)
		require.Equal(t, []int{keep.Id}, ids)
	}
}
