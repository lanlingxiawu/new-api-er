package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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

// mjChargeFailTable makes every UPDATE of table fail for the rest of the test.
func mjChargeFailTable(t *testing.T, table string) {
	t.Helper()
	name := "test:mj_charge_fail_" + table
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == table {
			_ = tx.AddError(errors.New("injected " + table + " update failure"))
		}
	}))
	t.Cleanup(func() { _ = model.DB.Callback().Update().Remove(name) })
}

func mjChargeSeed(t *testing.T, userID int, playground bool) (*relaycommon.RelayInfo, *model.Midjourney) {
	t.Helper()
	f := mjRefundFixture{userID: userID, tokenID: userID, channelID: userID, quota: 0,
		userQuota: 10_000, tokenRemain: 10_000}
	task := mjRefundSeed(t, f, true)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", f.tokenID).Update("used_quota", 0).Error)
	require.NoError(t, model.DB.Model(&model.Midjourney{}).Where("id = ?", task.Id).Update("token_id", 0).Error)
	task.TokenId = 0
	var token model.Token
	require.NoError(t, model.DB.Where("id = ?", f.tokenID).First(&token).Error)
	info := &relaycommon.RelayInfo{UserId: userID, TokenId: f.tokenID, TokenKey: token.Key, IsPlayground: playground}
	return info, task
}

func mjChargeRow(t *testing.T, id int) (int, int) {
	t.Helper()
	var row model.Midjourney
	require.NoError(t, model.DB.Select("quota", "token_id").Where("id = ?", id).First(&row).Error)
	return row.Quota, row.TokenId
}

// Review finding L5b: the task stored the full price and token even when the
// deferred charge failed, so a later failure refunded money never taken. The
// task now records what the charge actually took.
func TestChargeMidjourneySubmissionRecordsActualCharge(t *testing.T) {
	t.Run("charged", func(t *testing.T) {
		info, task := mjChargeSeed(t, 945020, false)
		require.True(t, ChargeMidjourneySubmission(info, task, 3000))
		assert.Equal(t, 7000, getUserQuota(t, 945020))
		assert.Equal(t, 7000, getTokenRemainQuota(t, 945020))
		q, tok := mjChargeRow(t, task.Id)
		assert.Equal(t, 3000, q)
		assert.Equal(t, 945020, tok)
	})
	t.Run("playground charges no token", func(t *testing.T) {
		info, task := mjChargeSeed(t, 945021, true)
		require.True(t, ChargeMidjourneySubmission(info, task, 3000))
		assert.Equal(t, 7000, getUserQuota(t, 945021))
		assert.Equal(t, 10_000, getTokenRemainQuota(t, 945021))
		q, tok := mjChargeRow(t, task.Id)
		assert.Equal(t, 3000, q)
		assert.Zero(t, tok)
	})
	t.Run("wallet charge fails", func(t *testing.T) {
		info, task := mjChargeSeed(t, 945022, false)
		mjChargeFailTable(t, "users")
		assert.False(t, ChargeMidjourneySubmission(info, task, 3000), "caller must not log or count usage")
		q, tok := mjChargeRow(t, task.Id)
		assert.Zero(t, q, "nothing taken, nothing refundable")
		assert.Zero(t, tok)
		assert.Equal(t, 10_000, getTokenRemainQuota(t, 945022), "the token is not charged either")
	})
	t.Run("token charge fails", func(t *testing.T) {
		info, task := mjChargeSeed(t, 945023, false)
		mjChargeFailTable(t, "tokens")
		require.True(t, ChargeMidjourneySubmission(info, task, 3000))
		q, tok := mjChargeRow(t, task.Id)
		assert.Equal(t, 3000, q, "the wallet was charged")
		assert.Zero(t, tok, "the token was not, so it must not be refunded")
	})
	t.Run("task not saved", func(t *testing.T) {
		info, _ := mjChargeSeed(t, 945024, false)
		require.True(t, ChargeMidjourneySubmission(info, nil, 500))
		require.True(t, ChargeMidjourneySubmission(info, &model.Midjourney{}, 500))
		assert.Equal(t, 9000, getUserQuota(t, 945024), "the charge still happens")
	})
	t.Run("free submission", func(t *testing.T) {
		info, task := mjChargeSeed(t, 945025, false)
		require.True(t, ChargeMidjourneySubmission(info, task, 0))
		assert.Equal(t, 10_000, getUserQuota(t, 945025))
		q, tok := mjChargeRow(t, task.Id)
		assert.Zero(t, q)
		assert.Zero(t, tok)
	})
}

// A token charge failure followed by a task failure refunds the wallet only.
func TestMidjourneyRefundAfterPartialCharge(t *testing.T) {
	info, task := mjChargeSeed(t, 945030, false)
	func() {
		name := "test:mj_partial_tokens"
		require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
			if tx.Statement != nil && tx.Statement.Table == "tokens" {
				_ = tx.AddError(errors.New("injected tokens update failure"))
			}
		}))
		defer func() { _ = model.DB.Callback().Update().Remove(name) }()
		require.True(t, ChargeMidjourneySubmission(info, task, 2500))
	}()
	require.True(t, RefundMidjourneyQuota(context.Background(), task, "构图失败"))
	assert.Equal(t, 10_000, getUserQuota(t, 945030), "the wallet charge is returned")
	assert.Equal(t, 10_000, getTokenRemainQuota(t, 945030), "the uncharged token is not credited")
}
