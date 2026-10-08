package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test report 2026-09-29 #11: deleting a channel left its channel_cost_configs
// row behind. Every delete path now removes it in the channel's transaction;
// history tables are kept (see channel_cost_config.go).

func mkChannelCostConfig(t *testing.T, channelID int, ratio float64) {
	t.Helper()
	require.NoError(t, DB.Create(&ChannelCostConfig{ChannelId: channelID, CostRatio: ratio, Remark: "test"}).Error)
	t.Cleanup(func() {
		if DB != nil {
			DB.Where("channel_id = ?", channelID).Delete(&ChannelCostConfig{})
		}
	})
}

func countChannelCostConfigs(t *testing.T, channelIDs ...int) int64 {
	t.Helper()
	var n int64
	require.NoError(t, DB.Model(&ChannelCostConfig{}).Where("channel_id IN ?", channelIDs).Count(&n).Error)
	return n
}

func TestChannelDeleteRemovesCostConfig(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, nil)
	keep := mkChannel(t, nil)
	mkChannelCostConfig(t, ch.Id, 0.6)
	mkChannelCostConfig(t, keep.Id, 0.7)

	require.NoError(t, (&Channel{Id: ch.Id}).Delete())

	assert.EqualValues(t, 0, countChannelCostConfigs(t, ch.Id))
	assert.EqualValues(t, 1, countChannelCostConfigs(t, keep.Id), "other channels keep theirs")
}

func TestChannelDeleteWithoutCostConfig(t *testing.T) {
	requireDB(t)
	ch := mkChannel(t, nil)
	require.NoError(t, (&Channel{Id: ch.Id}).Delete())
	assert.EqualValues(t, 0, countChannelCostConfigs(t, ch.Id))
}

func TestBatchDeleteChannelsRemovesCostConfigs(t *testing.T) {
	requireDB(t)
	c1 := mkChannel(t, nil)
	c2 := mkChannel(t, nil)
	c3 := mkChannel(t, nil) // no cost config
	keep := mkChannel(t, nil)
	mkChannelCostConfig(t, c1.Id, 0.5)
	mkChannelCostConfig(t, c2.Id, 1.2)
	mkChannelCostConfig(t, keep.Id, 0.9)

	n, err := BatchDeleteChannels([]int{c1.Id, c2.Id, c3.Id})
	require.NoError(t, err)
	assert.EqualValues(t, 3, n)
	assert.EqualValues(t, 0, countChannelCostConfigs(t, c1.Id, c2.Id, c3.Id))
	assert.EqualValues(t, 1, countChannelCostConfigs(t, keep.Id))
}

func TestDeleteChannelByStatusRemovesCostConfigs(t *testing.T) {
	requireDB(t)
	// A status value no other row shares, so shared data is never touched.
	const uniqueStatus = 987655
	ch := mkChannel(t, func(c *Channel) { c.Status = uniqueStatus })
	keep := mkChannel(t, nil)
	mkChannelCostConfig(t, ch.Id, 0.4)
	mkChannelCostConfig(t, keep.Id, 0.8)

	n, err := DeleteChannelByStatus(uniqueStatus)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	assert.EqualValues(t, 0, countChannelCostConfigs(t, ch.Id))
	assert.EqualValues(t, 1, countChannelCostConfigs(t, keep.Id), "a channel with another status keeps its config")
}

func TestDeleteChannelCostConfigsHelpersNoop(t *testing.T) {
	requireDB(t)
	assert.NoError(t, deleteChannelCostConfigsTx(DB, nil))
	n, err := DeleteChannelByStatus(987657) // no channel has it
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
}
