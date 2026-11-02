package model

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUserChannelExclusionSelectionPriorities(t *testing.T) {
	requireDB(t)
	group := uniq("exclusion")
	high := mkChannelWithAbilities(t, func(ch *Channel) {
		ch.Group = group
		ch.Models = "exclusion-model"
		ch.Priority = common.GetPointer(int64(100))
	})
	low := mkChannelWithAbilities(t, func(ch *Channel) {
		ch.Group = group
		ch.Models = "exclusion-model"
		ch.Priority = common.GetPointer(int64(1))
	})
	for _, memory := range []bool{false, true} {
		t.Run(fmt.Sprint(memory), func(t *testing.T) {
			previous := common.MemoryCacheEnabled
			t.Cleanup(func() { common.MemoryCacheEnabled = previous })
			common.MemoryCacheEnabled = memory
			if memory {
				InitChannelCache()
			}
			filters := []dto.ChannelFilter{{Kind: dto.FilterUserExcludedChannels, UserExclusions: &dto.UserChannelExclusionPolicy{IDs: []int{high.Id}}}}
			ch, err := GetRandomSatisfiedChannel(group, "exclusion-model", 0, filters)
			require.NoError(t, err)
			require.NotNil(t, ch)
			require.Equal(t, low.Id, ch.Id)
			ch, err = GetRandomSatisfiedChannel(group, "exclusion-model", 0, nil)
			require.NoError(t, err)
			require.NotNil(t, ch)
			require.Equal(t, high.Id, ch.Id, "other users retain the original candidates")
			filters[0].UserExclusions.Invalid = true
			ch, err = GetRandomSatisfiedChannel(group, "exclusion-model", 0, filters)
			require.NoError(t, err)
			require.Nil(t, ch)
		})
	}
}

func TestUserChannelExclusionCleanupReplay(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	ch := mkChannel(t, nil)
	keep := mkChannel(t, nil)
	u := mkUser(t, func(u *User) { u.ExcludedChannelIDs = fmt.Sprintf("[%d,%d]", ch.Id, keep.Id); u.Quota = 783 })
	require.NoError(t, DB.Create(&[]UserChannelExclusion{{UserID: u.Id, ChannelID: ch.Id}, {UserID: u.Id, ChannelID: keep.Id}}).Error)
	require.NoError(t, DB.AutoMigrate(&SystemTaskLock{}))
	lease := SystemTaskLock{Type: SystemTaskTypeUserChannelExclusionCleanup, TaskID: uniq("task"), LockedBy: uniq("worker"), LockedUntil: time.Now().Add(time.Minute).Unix()}
	require.NoError(t, DB.Create(&lease).Error)
	t.Cleanup(func() {
		DB.Where("type = ? AND task_id = ?", lease.Type, lease.TaskID).Delete(&SystemTaskLock{})
		DB.Where("channel_id = ?", ch.Id).Delete(&UserChannelExclusionCleanup{})
		DB.Where("user_id = ?", u.Id).Delete(&UserChannelExclusion{})
	})
	require.NoError(t, ch.Delete())
	id, err := PrepareUserChannelExclusionCleanup(ctx, ch.Id, lease.TaskID, lease.LockedBy)
	require.NoError(t, err)
	require.Equal(t, u.Id, id)
	var row User
	require.NoError(t, DB.First(&row, u.Id).Error)
	require.Equal(t, fmt.Sprintf("[%d]", keep.Id), row.ExcludedChannelIDs)
	require.Equal(t, u.ProfileVersion+1, row.ProfileVersion)
	require.Equal(t, 783, row.Quota)
	// Restart after committing projection, before publishing cache.
	id, err = PrepareUserChannelExclusionCleanup(ctx, ch.Id, lease.TaskID, lease.LockedBy)
	require.NoError(t, err)
	require.Equal(t, u.Id, id)
	// Concurrent admin save is retained by replay, which publishes latest state.
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, _, err := ReplaceUserChannelExclusions(tx, u.Id, nil)
		if err != nil {
			return err
		}
		return tx.Model(&User{}).Where("id = ?", u.Id).Updates(map[string]any{"excluded_channel_ids": "[]", "profile_version": gorm.Expr("profile_version + 1")}).Error
	}))
	require.NoError(t, PublishUserChannelExclusionCleanup(ctx, id))
	require.ErrorIs(t, AcknowledgeUserChannelExclusionCleanup(ctx, ch.Id, id, "stale", lease.LockedBy), ErrSystemTaskLockLost)
	require.NoError(t, AcknowledgeUserChannelExclusionCleanup(ctx, ch.Id, id, lease.TaskID, lease.LockedBy))
	id, err = PrepareUserChannelExclusionCleanup(ctx, ch.Id, lease.TaskID, lease.LockedBy)
	require.NoError(t, err)
	require.Zero(t, id)
	require.ErrorIs(t, DB.First(&UserChannelExclusionCleanup{}, "channel_id = ?", ch.Id).Error, gorm.ErrRecordNotFound)
	require.NoError(t, DB.First(&row, u.Id).Error)
	require.Equal(t, "[]", row.ExcludedChannelIDs)
}

func TestUserChannelExclusionLogPrivacy(t *testing.T) {
	other := `{"admin_info":{"user_channel_routing":{"reason":"user_channel_excluded","channel_ids":[123]},"channel_name":"private"}}`
	require.NotContains(t, formatLogOtherJSON(other, logOtherVisibilityUser), "user_channel_routing")
	require.Contains(t, formatLogOtherJSON(other, logOtherVisibilityAdmin), "user_channel_routing")
	logs := []*Log{{Other: other}}
	formatEmployeeLogs(logs)
	require.Contains(t, logs[0].Other, "user_channel_routing")
	require.NotContains(t, logs[0].Other, "private")
	columns, err := ResolveLogExportColumns([]string{"created_at", "user_channel_routing_reason", "user_channel_routing_ids"}, false)
	require.NoError(t, err)
	require.Equal(t, []string{"created_at"}, columns.Keys)
}

func TestUserChannelExclusionCleanupDeletedUsers(t *testing.T) {
	requireDB(t)
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
	for _, hard := range []bool{false, true} {
		t.Run(fmt.Sprint(hard), func(t *testing.T) {
			ch := mkChannel(t, nil)
			u := mkUser(t, func(u *User) { u.ExcludedChannelIDs = fmt.Sprintf("[%d]", ch.Id) })
			require.NoError(t, DB.Create(&UserChannelExclusion{UserID: u.Id, ChannelID: ch.Id}).Error)
			lease := SystemTaskLock{Type: SystemTaskTypeUserChannelExclusionCleanup, TaskID: uniq("task"), LockedBy: uniq("worker"), LockedUntil: time.Now().Add(time.Minute).Unix()}
			require.NoError(t, DB.Create(&lease).Error)
			t.Cleanup(func() {
				DB.Where("type = ? AND task_id = ?", lease.Type, lease.TaskID).Delete(&SystemTaskLock{})
				DB.Where("channel_id = ?", ch.Id).Delete(&UserChannelExclusionCleanup{})
				DB.Where("user_id = ?", u.Id).Delete(&UserChannelExclusion{})
			})
			require.NoError(t, ch.Delete())
			// Model a user disappearing before the worker reads its pending relation.
			query := DB
			if hard {
				query = query.Unscoped()
			}
			require.NoError(t, query.Delete(u).Error)
			id, err := PrepareUserChannelExclusionCleanup(context.Background(), ch.Id, lease.TaskID, lease.LockedBy)
			require.NoError(t, err)
			require.Equal(t, u.Id, id)
			require.NoError(t, PublishUserChannelExclusionCleanup(context.Background(), id))
			require.NoError(t, AcknowledgeUserChannelExclusionCleanup(context.Background(), ch.Id, id, lease.TaskID, lease.LockedBy))
			var count int64
			require.NoError(t, DB.Model(&UserChannelExclusion{}).Where("user_id = ?", id).Count(&count).Error)
			require.Zero(t, count)
			var stored User
			err = DB.Unscoped().First(&stored, id).Error
			if hard {
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			} else {
				require.NoError(t, err)
				require.True(t, stored.DeletedAt.Valid)
				require.Equal(t, "[]", stored.ExcludedChannelIDs)
			}
		})
	}
}

func TestUserChannelExclusionCachePreservesQuotaAndFence(t *testing.T) {
	u := cachedTestUser(t)
	ctx := context.Background()
	t.Cleanup(func() { common.RDB.Del(ctx, getUserProfileVersionKey(u.Id)) })
	require.NoError(t, cacheDecrUserQuota(u.Id, 37))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", u.Id).Updates(map[string]any{"excluded_channel_ids": "[12]", "profile_version": gorm.Expr("profile_version + 1")}).Error)
	require.NoError(t, PublishUserAuthCache(u.Id))
	base, err := cacheGetUserBase(u.Id)
	require.NoError(t, err)
	require.Equal(t, "[12]", base.ExcludedChannelIDs)
	require.Equal(t, 463, base.Quota)
	require.NoError(t, populateUserCache(*u)) // Delayed old fill cannot restore [] or quota.
	base, err = cacheGetUserBase(u.Id)
	require.NoError(t, err)
	require.Equal(t, "[12]", base.ExcludedChannelIDs)
	require.Equal(t, 463, base.Quota)
	require.NoError(t, common.RDB.HDel(ctx, getUserCacheKey(u.Id), "ExcludedChannelIDs").Err())
	_, err = cacheGetUserBase(u.Id)
	require.Error(t, err, "a missing policy field is not an empty policy")
	base, err = GetUserCache(u.Id)
	require.NoError(t, err)
	require.Equal(t, "[12]", base.ExcludedChannelIDs)
	base, err = cacheGetUserBase(u.Id)
	require.NoError(t, err)
	require.Equal(t, 463, base.Quota)
}

func TestUserChannelExclusionAllDeletionPaths(t *testing.T) {
	requireDB(t)
	for _, mode := range []string{"single", "batch", "status"} {
		t.Run(mode, func(t *testing.T) {
			ch := mkChannelWithAbilities(t, func(ch *Channel) { ch.Status = 987643; ch.Models = "deletion-model" })
			t.Cleanup(func() { DB.Where("channel_id = ?", ch.Id).Delete(&UserChannelExclusionCleanup{}) })
			switch mode {
			case "single":
				require.NoError(t, ch.Delete())
			case "batch":
				count, err := BatchDeleteChannels([]int{ch.Id})
				require.NoError(t, err)
				require.EqualValues(t, 1, count)
			case "status":
				count, err := deleteChannelsByStatus(987643)
				require.NoError(t, err)
				require.EqualValues(t, 1, count)
			}
			var count int64
			require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", ch.Id).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, DB.Model(&UserChannelExclusionCleanup{}).Where("channel_id = ?", ch.Id).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestUserChannelExclusionSelfUpdateCannotOverwrite(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *User) { u.ExcludedChannelIDs = "[12]"; u.GroupRatios = `{"default":0.8}` })
	stale := *u
	require.NoError(t, DB.Model(u).Updates(map[string]any{"excluded_channel_ids": "[13]", "profile_version": gorm.Expr("profile_version + 1")}).Error)
	stale.DisplayName = "self-edit"
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return stale.UpdateWithTx(tx, false) }))
	var stored User
	require.NoError(t, DB.First(&stored, u.Id).Error)
	require.Equal(t, "[13]", stored.ExcludedChannelIDs)
	require.Equal(t, `{"default":0.8}`, stored.GroupRatios)
	require.Equal(t, "self-edit", stored.DisplayName)
	require.Equal(t, u.ProfileVersion+2, stored.ProfileVersion)
}
