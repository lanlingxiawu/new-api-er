//go:build audit_uncommitted

package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUncommittedAuditMidjourneyFailureBeforeCharge(t *testing.T) {
	fixture := mjRefundFixture{userID: 945991, tokenID: 945991, channelID: 945991,
		userQuota: 10000, tokenRemain: 10000}
	task := mjRefundSeed(t, fixture, true)
	// Mirror submission: insert a task with quota=0, then charge in its defer.
	require.NoError(t, model.DB.Model(task).Update("token_id", 0).Error)
	task.TokenId = 0
	poller := *task
	poller.Status, poller.Progress, poller.FailReason = "FAILURE", "100%", "mock failure"
	won, err := poller.UpdateWithStatus("IN_PROGRESS")
	require.NoError(t, err)
	require.True(t, won)
	require.True(t, RefundMidjourneyQuota(context.Background(), &poller, "mock failure"))
	var token model.Token
	require.NoError(t, model.DB.First(&token, fixture.tokenID).Error)
	info := &relaycommon.RelayInfo{UserId: fixture.userID, TokenId: fixture.tokenID, TokenKey: token.Key}
	require.True(t, ChargeMidjourneySubmission(info, task, 500))
	assert.Equal(t, fixture.userQuota, getUserQuota(t, fixture.userID),
		"a terminal failed task must not remain charged after the deferred submit settlement")
	assert.Equal(t, fixture.tokenRemain, getTokenRemainQuota(t, fixture.tokenID))
}

func TestUncommittedAuditAbsorbedCostMustNotDependOnRetailGroup(t *testing.T) {
	c, info, _ := nonStreamTimeoutContext(t, NonStreamTimeoutBillingRefund, 0)
	info.PriceData.GroupRatioInfo.GroupRatio = 1
	_, ordinary := nonStreamTimeoutPlan(c, info)
	info.PriceData.GroupRatioInfo.GroupRatio = 10
	_, markedUp := nonStreamTimeoutPlan(c, info)
	assert.Equal(t, ordinary["absorbed_quota_min"], markedUp["absorbed_quota_min"],
		"same upstream, usage, model price and cost factor: retail markup cannot multiply the platform's upstream cost")
}
