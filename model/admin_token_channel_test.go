package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCreatedChannelAvoidsUnnecessaryWrites(t *testing.T) {
	db := newAdminTokenModelDB(t)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}, &ChannelCreator{}))
	remark := "same"
	channels := []Channel{{Name: "same", Remark: &remark, Key: "fixture", Models: "a,b", Group: "default,other", Status: common.ChannelStatusEnabled}}
	require.NoError(t, BatchInsertChannels(channels, 97531))
	id := channels[0].Id
	t.Cleanup(func() {
		db.Where("channel_id = ?", id).Delete(&ChannelCreator{})
		db.Where("channel_id = ?", id).Delete(&Ability{})
		db.Where("id = ?", id).Delete(&Channel{})
	})
	writes := map[string]int64{}
	count := func(tx *gorm.DB) { writes[tx.Statement.Table] += tx.RowsAffected }
	require.NoError(t, db.Callback().Create().After("gorm:create").Register("test:channel-writes", count))
	require.NoError(t, db.Callback().Update().After("gorm:update").Register("test:channel-writes", count))
	require.NoError(t, db.Callback().Delete().After("gorm:delete").Register("test:channel-writes", count))
	t.Cleanup(func() {
		db.Callback().Create().Remove("test:channel-writes")
		db.Callback().Update().Remove("test:channel-writes")
		db.Callback().Delete().Remove("test:channel-writes")
	})
	for _, updates := range []map[string]any{{"name": "same", "remark": "same"}, {"status": common.ChannelStatusEnabled, "daily_limit_disabled_at": int64(0), "daily_limit_disabled_date": int64(0)}} {
		clear(writes)
		changes, err := UpdateCreatedChannelsWithChanges(context.Background(), 97531, []int{id}, updates, DailyLimitEdit{})
		require.NoError(t, err)
		require.Empty(t, changes.IDs)
		require.False(t, changes.CacheChanged)
		require.Zero(t, writes["channels"], "no-op must not update the shared channel row")
		require.Zero(t, writes["abilities"], "no-op must not rebuild routing abilities")
	}
	require.NoError(t, db.Model(&Channel{}).Where("id = ?", id).Update("daily_limit_disabled_at", int64(123)).Error)
	changes, err := UpdateCreatedChannelsWithChanges(context.Background(), 97531, []int{id}, map[string]any{"status": common.ChannelStatusEnabled, "daily_limit_disabled_at": 0}, DailyLimitEdit{})
	require.NoError(t, err)
	require.Empty(t, changes.IDs, "internal daily-limit reset is not a configuration change")
	require.True(t, changes.CacheChanged, "internal state reset must still reach the relay cache")
	minutes := 15
	for _, daily := range []DailyLimitEdit{{}, {RecoverMinutes: &minutes}, {RecoverMinutes: &minutes}} {
		clear(writes)
		_, err := UpdateCreatedChannelsWithChanges(context.Background(), 97531, []int{id}, map[string]any{"remark": "changed"}, daily)
		require.NoError(t, err)
		require.Zero(t, writes["abilities"], "display and daily-limit changes do not change routing abilities")
	}
	for _, updates := range []map[string]any{{"status": common.ChannelStatusManuallyDisabled}, {"status": common.ChannelStatusEnabled}, {"tag": "new", "priority": int64(7), "weight": uint(0)}, {"models": "a,b,c"}, {"group": "default"}} {
		clear(writes)
		changes, err := UpdateCreatedChannelsWithChanges(context.Background(), 97531, []int{id}, updates, DailyLimitEdit{})
		require.NoError(t, err)
		require.Equal(t, []int{id}, changes.IDs)
		var channel Channel
		require.NoError(t, db.First(&channel, id).Error)
		var abilities []Ability
		require.NoError(t, db.Where("channel_id = ?", id).Find(&abilities).Error)
		for _, ability := range abilities {
			require.Equal(t, channel.Status == common.ChannelStatusEnabled, ability.Enabled)
			require.Equal(t, channel.Priority, ability.Priority)
			require.Equal(t, uint(channel.GetWeight()), ability.Weight)
			require.Equal(t, channel.Tag, ability.Tag)
		}
		if _, models := updates["models"]; !models {
			if _, groups := updates["group"]; !groups {
				require.EqualValues(t, len(abilities), writes["abilities"], "simple routing changes update once rather than delete and insert")
			}
		}
	}
	clear(writes)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = UpdateCreatedChannelsWithChanges(cancelled, 97531, []int{id}, map[string]any{"name": "cancelled"}, DailyLimitEdit{})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, writes["channels"])
}
