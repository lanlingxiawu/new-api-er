package model

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUserChannelExclusionDeletionQueue(t *testing.T) {
	requireDB(t)
	require.NoError(t, DB.AutoMigrate(&UserChannelExclusion{}, &UserChannelExclusionCleanup{}))
	u := mkUser(t, nil)
	ch := mkChannel(t, nil)
	t.Cleanup(func() {
		DB.Where("user_id = ?", u.Id).Delete(&UserChannelExclusion{})
		DB.Where("channel_id = ?", ch.Id).Delete(&UserChannelExclusionCleanup{})
	})
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		kept, removed, err := ReplaceUserChannelExclusions(tx, u.Id, []int{ch.Id})
		require.Empty(t, removed)
		require.Equal(t, []int{ch.Id}, kept)
		if err != nil {
			return err
		}
		raw, err := common.Marshal(kept)
		if err != nil {
			return err
		}
		return tx.Model(u).Update("excluded_channel_ids", string(raw)).Error
	}))
	require.NoError(t, ch.Delete())
	var queue UserChannelExclusionCleanup
	require.NoError(t, DB.First(&queue, "channel_id = ?", ch.Id).Error)
	// The relationship remains discoverable until projection and pending ID
	// commit together, so a crash cannot orphan a stale cached projection.
	var count int64
	require.NoError(t, DB.Model(&UserChannelExclusion{}).Where("user_id = ?", u.Id).Count(&count).Error)
	require.EqualValues(t, 1, count)
	_, err := PrepareUserChannelExclusionCleanup(context.Background(), ch.Id, "missing-lease", "missing-runner")
	require.ErrorIs(t, err, ErrSystemTaskLockLost)
	require.NoError(t, DB.First(&queue, "channel_id = ?", ch.Id).Error)
	require.Nil(t, queue.PendingUserID)
}

func TestParseUserChannelExclusions(t *testing.T) {
	for _, raw := range []string{"", "[]", " [ ] "} {
		ids, err := ParseUserChannelExclusions(raw)
		require.NoError(t, err)
		require.Empty(t, ids)
	}
	ids, err := ParseUserChannelExclusions("[3,1,3,2]")
	require.NoError(t, err)
	require.Equal(t, []int{1, 2, 3}, ids)
	for _, raw := range []string{"null", "{}", "[0]", "[-1]", "[1.5]", "[\"1\"]", "[true]", "[", "[9223372036854775808]", "[" + strings.Repeat("1,", 256) + "1]", strings.Repeat(" ", 8193)} {
		_, err := ParseUserChannelExclusions(raw)
		require.Error(t, err, raw)
	}
	_, err = ParseUserChannelExclusions("[" + strings.Repeat("1,", 255) + "1]")
	require.NoError(t, err)
}

func TestUserChannelExclusionFilter(t *testing.T) {
	policy := &dto.UserChannelExclusionPolicy{IDs: []int{2, 4}}
	filters := []dto.ChannelFilter{{Kind: dto.FilterUserExcludedChannels, UserExclusions: policy}}
	for _, id := range []int{1, 2, 3, 4} {
		ok, kind := ChannelSatisfiesFilters(&Channel{Id: id}, "model", filters)
		require.Equal(t, id%2 != 0, ok)
		if !ok {
			require.Equal(t, dto.FilterUserExcludedChannels, kind)
		}
	}
	policy.Invalid = true
	ok, _ := ChannelSatisfiesFilters(&Channel{Id: 1}, "model", filters)
	require.False(t, ok)
}

func BenchmarkParseUserChannelExclusions(b *testing.B) {
	for _, size := range []int{0, 1, 8, 64, 256} {
		ids := make([]int, size)
		for i := range ids {
			ids[i] = 1000000 + i
		}
		raw, _ := common.Marshal(ids)
		value := string(raw)
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _ = ParseUserChannelExclusions(value)
			}
		})
	}
}
