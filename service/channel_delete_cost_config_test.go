package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"
)

// Channel delete tests that must not run against the shared dev database:
// they live in the service package because its TestMain gives every run an
// isolated database (MySQL database / PostgreSQL schema), and
// model.DeleteDisabledChannel deletes every disabled channel there is.
//
// Channels are inserted without channel_info: the zero ChannelInfo is sent as
// bytes, which PostgreSQL's simple protocol rejects for a json column.

func delCostMkChannel(t *testing.T, id, status int, ratio float64, withConfig bool) {
	t.Helper()
	require.NoError(t, model.DB.Omit("channel_info").
		Create(&model.Channel{Id: id, Name: svcUniq("del-cost"), Key: "sk-test", Status: status}).Error)
	svcCleanupRow(t, &model.Channel{}, id)
	if withConfig {
		require.NoError(t, model.DB.Create(&model.ChannelCostConfig{ChannelId: id, CostRatio: ratio}).Error)
		t.Cleanup(func() { model.DB.Where("channel_id = ?", id).Delete(&model.ChannelCostConfig{}) })
	}
}

func delCostCount(t *testing.T, ids ...int) int64 {
	t.Helper()
	var c int64
	require.NoError(t, model.DB.Model(&model.ChannelCostConfig{}).Where("channel_id IN ?", ids).Count(&c).Error)
	return c
}

// Test report 2026-09-29 #11: "delete disabled channels" must remove the cost
// configs of the channels it deletes and keep everyone else's.
func TestDeleteDisabledChannelRemovesCostConfigs(t *testing.T) {
	truncate(t)
	const manual, auto, auto2, enabled = 944001, 944002, 944003, 944004
	delCostMkChannel(t, manual, common.ChannelStatusManuallyDisabled, 0.5, true)
	delCostMkChannel(t, auto, common.ChannelStatusAutoDisabled, 0.6, true)
	delCostMkChannel(t, auto2, common.ChannelStatusAutoDisabled, 0, false)
	delCostMkChannel(t, enabled, common.ChannelStatusEnabled, 0.7, true)

	n, err := model.DeleteDisabledChannel()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(3))

	assert.EqualValues(t, 0, delCostCount(t, manual, auto, auto2))
	assert.EqualValues(t, 1, delCostCount(t, enabled), "an enabled channel keeps its cost config")
	var remaining int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id IN ?", []int{manual, auto, auto2, enabled}).Count(&remaining).Error)
	assert.EqualValues(t, 1, remaining)
}

// Review finding on #11: the by-status delete removed cost configs through a
// status subquery and the channels through a second status query. Under
// PostgreSQL READ COMMITTED a status change committed between the two
// statements dropped the cost config of a channel that then survived. The ids
// are now selected once with the rows locked, and that list drives both
// deletes: a concurrent status change either lands before the selection (the
// channel is not deleted, its config stays) or waits for the delete.
func TestDeleteChannelByStatusConcurrentStatusChangeKeepsSurvivorConfig(t *testing.T) {
	truncate(t)
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		t.Skip("SQLite has no row locks; its single writer serializes the two transactions")
	}
	const id, uniqueStatus = 944010, 987658
	delCostMkChannel(t, id, uniqueStatus, 0.3, true)

	// Another transaction holds the channel row and moves it out of the status.
	tx := model.DB.Begin()
	require.NoError(t, tx.Error)
	var locked []int
	require.NoError(t, tx.Model(&model.Channel{}).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", id).Pluck("id", &locked).Error)
	require.NoError(t, tx.Model(&model.Channel{}).Where("id = ?", id).Update("status", common.ChannelStatusEnabled).Error)

	done := make(chan struct{})
	var n int64
	var delErr error
	go func() {
		defer close(done)
		n, delErr = model.DeleteChannelByStatus(uniqueStatus)
	}()
	// Let the delete run up to the point where it needs the row.
	time.Sleep(300 * time.Millisecond)
	require.NoError(t, tx.Commit().Error)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("delete did not finish")
	}

	require.NoError(t, delErr)
	assert.EqualValues(t, 0, n, "the channel left the status before the delete could take it")
	var remaining int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", id).Count(&remaining).Error)
	assert.EqualValues(t, 1, remaining)
	assert.EqualValues(t, 1, delCostCount(t, id), "a surviving channel keeps its cost config")
}
