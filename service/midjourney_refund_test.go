package service

import (
	"context"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Review finding (follow-up to test report #8): a failed Midjourney task
// refunded only the balance; the token quota and the usage the submission added
// to users.used_quota / channels.used_quota stayed charged, and the refund log
// had no token. RefundMidjourneyQuota reverses all of it, once per task.

type mjRefundFixture struct {
	userID, tokenID, channelID    int
	quota, userQuota, tokenRemain int
	userUsed, requests            int
	channelUsed                   int64
}

func mjRefundSeed(t *testing.T, f mjRefundFixture, withToken bool) *model.Midjourney {
	t.Helper()
	truncate(t)
	prevBatch := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	t.Cleanup(func() { common.BatchUpdateEnabled = prevBatch })

	seedUser(t, f.userID, f.userQuota)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.userID).
		Updates(map[string]any{"used_quota": f.userUsed, "request_count": f.requests}).Error)
	delCostMkChannel(t, f.channelID, common.ChannelStatusEnabled, 0, false)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", f.channelID).Update("used_quota", f.channelUsed).Error)
	task := &model.Midjourney{
		UserId: f.userID, Action: "IMAGINE", MjId: svcUniq("mj"), Status: "IN_PROGRESS", Progress: "50%",
		ChannelId: f.channelID, Quota: f.quota,
	}
	if withToken {
		seedToken(t, f.tokenID, f.userID, svcUniq("sk-mj"), f.tokenRemain)
		require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", f.tokenID).Update("used_quota", f.quota).Error)
		task.TokenId = f.tokenID
	}
	require.NoError(t, task.Insert())
	t.Cleanup(func() { model.DB.Delete(&model.Midjourney{}, task.Id) })
	return task
}

func mjRefundTaskQuota(t *testing.T, id int) int {
	t.Helper()
	var task model.Midjourney
	require.NoError(t, model.DB.Select("quota").Where("id = ?", id).First(&task).Error)
	return task.Quota
}

func TestRefundMidjourneyQuota_ReversesEverything(t *testing.T) {
	f := mjRefundFixture{userID: 945001, tokenID: 945001, channelID: 945001, quota: 5000,
		userQuota: 1000, tokenRemain: 2000, userUsed: 5000 + 700, requests: 3, channelUsed: 5000}
	task := mjRefundSeed(t, f, true)

	require.True(t, RefundMidjourneyQuota(context.Background(), task, "构图失败"))

	assert.Equal(t, f.userQuota+f.quota, getUserQuota(t, f.userID))
	assert.Equal(t, f.tokenRemain+f.quota, getTokenRemainQuota(t, f.tokenID), "the token is refunded too")
	assert.Equal(t, 0, getTokenUsedQuota(t, f.tokenID))
	used, requests := taskUsedQuotaUser(t, f.userID)
	assert.Equal(t, 700, used, "the failed task no longer counts as used")
	assert.Equal(t, f.requests, requests, "the request itself still happened")
	assert.Equal(t, int64(0), taskUsedQuotaChannel(t, f.channelID))
	assert.Zero(t, mjRefundTaskQuota(t, task.Id), "the refund marker is cleared")
	assert.Zero(t, task.Quota)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, f.quota, log.Quota)
	assert.Equal(t, f.tokenID, log.TokenId)
	assert.Equal(t, f.channelID, log.ChannelId)
}

// A second refund of the same task, or a concurrent one from a stale copy,
// refunds nothing.
func TestRefundMidjourneyQuota_OncePerTask(t *testing.T) {
	f := mjRefundFixture{userID: 945002, tokenID: 945002, channelID: 945002, quota: 3000,
		userQuota: 0, tokenRemain: 0, userUsed: 3000, requests: 1, channelUsed: 3000}
	task := mjRefundSeed(t, f, true)

	copies := make([]*model.Midjourney, 4)
	for i := range copies {
		c := *task
		copies[i] = &c
	}
	var wg sync.WaitGroup
	for _, c := range copies {
		wg.Add(1)
		go func(c *model.Midjourney) {
			defer wg.Done()
			assert.True(t, RefundMidjourneyQuota(context.Background(), c, "构图失败"))
		}(c)
	}
	wg.Wait()
	require.True(t, RefundMidjourneyQuota(context.Background(), task, "构图失败"), "already refunded is not an error")

	assert.Equal(t, f.quota, getUserQuota(t, f.userID), "refunded exactly once")
	assert.Equal(t, f.quota, getTokenRemainQuota(t, f.tokenID))
	used, _ := taskUsedQuotaUser(t, f.userID)
	assert.Equal(t, 0, used)
	assert.Equal(t, int64(0), taskUsedQuotaChannel(t, f.channelID))
	drainRelayLogs()
	var refunds int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("user_id = ? AND type = ?", f.userID, model.LogTypeRefund).Count(&refunds).Error)
	assert.EqualValues(t, 1, refunds)
}

// A submission without a charged token (playground) refunds the balance and
// usage but touches no token.
func TestRefundMidjourneyQuota_NoToken(t *testing.T) {
	f := mjRefundFixture{userID: 945003, channelID: 945003, quota: 1200,
		userQuota: 10, userUsed: 1200, requests: 1, channelUsed: 1200}
	task := mjRefundSeed(t, f, false)

	require.True(t, RefundMidjourneyQuota(context.Background(), task, "构图失败"))
	assert.Equal(t, 10+f.quota, getUserQuota(t, f.userID))
	used, _ := taskUsedQuotaUser(t, f.userID)
	assert.Equal(t, 0, used)
	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, 0, log.TokenId)
}

// An unbilled task (quota 0) is not refunded at all.
func TestRefundMidjourneyQuota_ZeroQuota(t *testing.T) {
	f := mjRefundFixture{userID: 945004, channelID: 945004, quota: 0, userQuota: 50, userUsed: 80, requests: 1, channelUsed: 80}
	task := mjRefundSeed(t, f, false)

	require.True(t, RefundMidjourneyQuota(context.Background(), task, "构图失败"))
	assert.Equal(t, 50, getUserQuota(t, f.userID))
	used, _ := taskUsedQuotaUser(t, f.userID)
	assert.Equal(t, 80, used)
	assert.Equal(t, int64(80), taskUsedQuotaChannel(t, f.channelID))
	drainRelayLogs()
	var refunds int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("user_id = ? AND type = ?", f.userID, model.LogTypeRefund).Count(&refunds).Error)
	assert.Zero(t, refunds)
}

// Review finding L5a: the notify handler (Update → Save) and the poller
// (UpdateWithStatus → Select("*")) wrote back whole structs loaded earlier, so
// an out-of-order write could restore a refunded task's quota and let the next
// failure refund it again. Those writes no longer touch quota / token_id.
func TestMidjourneyStatusWritesKeepBillingColumns(t *testing.T) {
	f := mjRefundFixture{userID: 945010, tokenID: 945010, channelID: 945010, quota: 4000,
		userQuota: 0, tokenRemain: 0, userUsed: 4000, requests: 1, channelUsed: 4000}
	task := mjRefundSeed(t, f, true)
	staleNotify := *task // what the notify handler loaded before the refund
	stalePoll := *task

	require.True(t, RefundMidjourneyQuota(context.Background(), task, "构图失败"))
	require.Zero(t, mjRefundTaskQuota(t, task.Id))

	// A late notify puts the task back in progress from its stale copy.
	staleNotify.Status = "IN_PROGRESS"
	staleNotify.Progress = "60%"
	require.NoError(t, staleNotify.Update())
	var row model.Midjourney
	require.NoError(t, model.DB.Where("id = ?", task.Id).First(&row).Error)
	assert.Equal(t, "60%", row.Progress, "status fields are still written")
	assert.Zero(t, row.Quota, "the refunded amount is not restored")
	assert.Equal(t, f.tokenID, row.TokenId)

	// The poller's CAS write from another stale copy.
	stalePoll.Status = "FAILURE"
	stalePoll.Progress = "100%"
	won, err := stalePoll.UpdateWithStatus("IN_PROGRESS")
	require.NoError(t, err)
	require.True(t, won)
	require.NoError(t, model.DB.Where("id = ?", task.Id).First(&row).Error)
	assert.Equal(t, "FAILURE", row.Status)
	assert.Zero(t, row.Quota)

	// Even refunding from the stale copy (quota still set in memory) pays nothing.
	require.True(t, RefundMidjourneyQuota(context.Background(), &stalePoll, "构图失败"))
	assert.Equal(t, f.quota, getUserQuota(t, f.userID), "refunded exactly once")
	assert.Equal(t, f.quota, getTokenRemainQuota(t, f.tokenID))
}
