package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test report 2026-09-29 #8 / gemini D-5: a failed async task refunded the
// balance, the token and the cost ledger, but users.used_quota and
// channels.used_quota kept the pre-consumed amount, so "used" grew by every
// failed task. Settlement must move usage by the same amount as the funding
// and never count the request a second time.

func taskUsedQuotaSeed(t *testing.T, userID, channelID, userUsed, requestCount int, channelUsed int64) {
	t.Helper()
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).
		Updates(map[string]any{"used_quota": userUsed, "request_count": requestCount}).Error)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channelID).
		Update("used_quota", channelUsed).Error)
}

func taskUsedQuotaUser(t *testing.T, userID int) (usedQuota int, requestCount int) {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.Select("used_quota", "request_count").Where("id = ?", userID).First(&user).Error)
	return user.UsedQuota, user.RequestCount
}

func taskUsedQuotaChannel(t *testing.T, channelID int) int64 {
	t.Helper()
	var channel model.Channel
	require.NoError(t, model.DB.Select("used_quota").Where("id = ?", channelID).First(&channel).Error)
	return channel.UsedQuota
}

func taskUsedQuotaDirectWrites(t *testing.T) {
	t.Helper()
	previous := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	t.Cleanup(func() { common.BatchUpdateEnabled = previous })
}

func TestRefundTaskQuota_RevertsUserAndChannelUsedQuota(t *testing.T) {
	truncate(t)
	taskUsedQuotaDirectWrites(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 943001, 943001, 943001
	const preConsumed = 2_800_000
	// Submission counted the task: used_quota and channel usage include it.
	const otherUsage = 88_606
	seedUser(t, userID, 1_000_000)
	seedToken(t, tokenID, userID, "sk-used-refund", 5000)
	seedChannel(t, channelID)
	taskUsedQuotaSeed(t, userID, channelID, otherUsage+preConsumed, 4, preConsumed)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	require.NoError(t, model.DB.Create(task).Error)

	require.True(t, RefundTaskQuota(ctx, task, "upstream task failed"))

	used, requests := taskUsedQuotaUser(t, userID)
	assert.Equal(t, otherUsage, used, "the refunded task no longer counts as used")
	assert.Equal(t, 4, requests, "the request itself still happened")
	assert.Equal(t, int64(0), taskUsedQuotaChannel(t, channelID))
	assert.Equal(t, 1_000_000+preConsumed, getUserQuota(t, userID))
}

// A refund whose funding step fails refunds nothing, so usage must not move
// either; the pending marker keeps the retry possible.
func TestRefundTaskQuota_FundingFailureKeepsUsedQuota(t *testing.T) {
	truncate(t)
	taskUsedQuotaDirectWrites(t)
	ctx := context.Background()

	const userID, channelID, preConsumed = 943002, 943002, 1200
	seedUser(t, userID, 5000)
	seedChannel(t, channelID)
	taskUsedQuotaSeed(t, userID, channelID, preConsumed, 1, preConsumed)
	task := makeTask(userID, channelID, preConsumed, 0, BillingSourceSubscription, 9_999_999)
	require.NoError(t, model.DB.Create(task).Error)

	require.False(t, RefundTaskQuota(ctx, task, "subscription missing"))

	used, requests := taskUsedQuotaUser(t, userID)
	assert.Equal(t, preConsumed, used)
	assert.Equal(t, 1, requests)
	assert.Equal(t, int64(preConsumed), taskUsedQuotaChannel(t, channelID))
}

// Zero-quota tasks were never charged: nothing to revert.
func TestRefundTaskQuota_ZeroQuotaLeavesUsedQuota(t *testing.T) {
	truncate(t)
	taskUsedQuotaDirectWrites(t)
	ctx := context.Background()

	const userID, channelID = 943003, 943003
	seedUser(t, userID, 5000)
	seedChannel(t, channelID)
	taskUsedQuotaSeed(t, userID, channelID, 700, 2, 700)
	task := makeTask(userID, channelID, 0, 0, BillingSourceWallet, 0)

	require.True(t, RefundTaskQuota(ctx, task, "nothing charged"))

	used, requests := taskUsedQuotaUser(t, userID)
	assert.Equal(t, 700, used)
	assert.Equal(t, 2, requests)
	assert.Equal(t, int64(700), taskUsedQuotaChannel(t, channelID))
}

// Settlement below the pre-consumed amount refunds the difference: usage drops
// by it too. Above it, usage grows by the extra charge without counting a
// second request (the submission already counted it).
func TestRecalculateTaskQuota_AdjustsUsedQuotaBothWays(t *testing.T) {
	for _, tc := range []struct {
		name         string
		preConsumed  int
		actualQuota  int
		wantUsed     int
		wantChannel  int64
		wantRequests int
	}{
		{name: "refund difference", preConsumed: 5000, actualQuota: 3000, wantUsed: 3000, wantChannel: 3000, wantRequests: 1},
		{name: "extra charge", preConsumed: 2000, actualQuota: 3000, wantUsed: 3000, wantChannel: 3000, wantRequests: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truncate(t)
			taskUsedQuotaDirectWrites(t)
			ctx := context.Background()

			userID, tokenID, channelID := 943010, 943010, 943010
			if tc.actualQuota > tc.preConsumed {
				userID, tokenID, channelID = 943011, 943011, 943011
			}
			seedUser(t, userID, 10000)
			seedToken(t, tokenID, userID, svcUniq("sk-used-recalc"), 10000)
			seedChannel(t, channelID)
			taskUsedQuotaSeed(t, userID, channelID, tc.preConsumed, 1, int64(tc.preConsumed))
			task := makeTask(userID, channelID, tc.preConsumed, tokenID, BillingSourceWallet, 0)

			RecalculateTaskQuota(ctx, task, tc.actualQuota, "adaptor adjustment", nil)

			used, requests := taskUsedQuotaUser(t, userID)
			assert.Equal(t, tc.wantUsed, used)
			assert.Equal(t, tc.wantRequests, requests)
			assert.Equal(t, tc.wantChannel, taskUsedQuotaChannel(t, channelID))
		})
	}
}
