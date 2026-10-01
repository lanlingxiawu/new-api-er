package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// UpdateUserUsedQuota backs async-task refunds and settlement corrections
// (test report 2026-09-29 #8): signed deltas, never a request count, and the
// same result whether written directly or through the batch updater.
func TestUpdateUserUsedQuotaSignedDirectAndBatchDeltas(t *testing.T) {
	previous := common.BatchUpdateEnabled
	t.Cleanup(func() { common.BatchUpdateEnabled = previous })
	common.BatchUpdateEnabled = false

	user := mkUser(t, func(u *User) { u.UsedQuota = 1000; u.RequestCount = 3 })
	channel := mkChannel(t, func(ch *Channel) { ch.UsedQuota = 1000 })
	read := func() (int, int, int64) {
		t.Helper()
		var u User
		require.NoError(t, DB.Select("used_quota", "request_count").Where("id = ?", user.Id).First(&u).Error)
		var ch Channel
		require.NoError(t, DB.Select("used_quota").Where("id = ?", channel.Id).First(&ch).Error)
		return u.UsedQuota, u.RequestCount, ch.UsedQuota
	}

	UpdateUserUsedQuota(user.Id, -200)
	UpdateUserUsedQuota(user.Id, 50)
	UpdateUserUsedQuota(user.Id, 0)
	UpdateChannelUsedQuota(channel.Id, -200)
	UpdateChannelUsedQuota(channel.Id, 50)
	used, requests, channelUsed := read()
	assert.Equal(t, 850, used)
	assert.Equal(t, 3, requests, "adjusting usage never counts a request")
	assert.Equal(t, int64(850), channelUsed)

	common.BatchUpdateEnabled = true
	UpdateUserUsedQuota(user.Id, 400)
	UpdateUserUsedQuota(user.Id, -100)
	UpdateChannelUsedQuota(channel.Id, 400)
	UpdateChannelUsedQuota(channel.Id, -100)
	used, requests, channelUsed = read()
	assert.Equal(t, 850, used, "batch deltas stay queued until the flush")
	assert.Equal(t, int64(850), channelUsed)

	batchUpdate()
	used, requests, channelUsed = read()
	assert.Equal(t, 1150, used)
	assert.Equal(t, 3, requests)
	assert.Equal(t, int64(1150), channelUsed)
}
