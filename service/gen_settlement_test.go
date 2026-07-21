package service

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// consume_settlement.go (FinalizeConsumptionSettlement) + billing_session.go
// (BillingSession.Settle / Reserve / NeedsRefund / GetPreConsumedQuota).
// Billing-core settlement flows. DB-backed.
// ===========================================================================

// --- FinalizeConsumptionSettlement -----------------------------------------

func TestSettleflow_Finalize_NilRelayInfoReturnsZero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	assert.Equal(t, 0, FinalizeConsumptionSettlement(c, nil, ConsumptionSettlementParams{}))
}

func TestSettleflow_Finalize_DefaultsFromRelayInfoAndLogs(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)

	info := &relaycommon.RelayInfo{
		UserId:          500,
		TokenId:         77,
		UsingGroup:      "vip",
		OriginModelName: "gpt-4o",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: 88},
	}
	// Quota 0 + CountUsage false + async commission => no wallet/token mutation and
	// the async commission is a no-op (quota==0). We only assert log defaulting.
	logID := FinalizeConsumptionSettlement(c, info, ConsumptionSettlementParams{
		PromptTokens:           10,
		CompletionTokens:       5,
		Quota:                  0,
		CountUsage:             false,
		AsyncCostAndCommission: true,
		Other:                  map[string]interface{}{},
	})
	require.Greater(t, logID, 0)

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, "gpt-4o", log.ModelName, "ModelName defaulted from relayInfo.OriginModelName")
	assert.Equal(t, 88, log.ChannelId, "ChannelId defaulted from ChannelMeta")
	assert.Equal(t, "vip", log.Group, "Group defaulted from UsingGroup")
	assert.Equal(t, 77, log.TokenId)
}

func TestSettleflow_Finalize_CountUsageChargesWallet(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)

	const uid, tid = 501, 501
	seedUser(t, uid, 10000)
	seedToken(t, tid, uid, "sk-finalize", 8000)

	info := &relaycommon.RelayInfo{
		UserId:          uid,
		TokenId:         tid,
		TokenKey:        "sk-finalize",
		BillingSource:   BillingSourceWallet,
		UserQuota:       10000,
		OriginModelName: "m",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: 9},
	}
	// CountUsage true => used-quota counters; SettleBilling fallback (no session,
	// FinalPreConsumedQuota 0) deducts the full quota from wallet+token.
	logID := FinalizeConsumptionSettlement(c, info, ConsumptionSettlementParams{
		Quota:                  1200,
		CountUsage:             true,
		AsyncCostAndCommission: true,
		Other:                  map[string]interface{}{},
	})
	require.Greater(t, logID, 0)
	assert.Equal(t, 8800, getUserQuota(t, uid))
	assert.Equal(t, 6800, getTokenRemainQuota(t, tid))
}

// --- BillingSession.Settle (wallet, synchronous) ---------------------------

func settleflowWalletSession(t *testing.T, uid, tid, pre int, key string) *BillingSession {
	t.Helper()
	info := &relaycommon.RelayInfo{UserId: uid, TokenId: tid, TokenKey: key, BillingSource: BillingSourceWallet}
	return &BillingSession{
		relayInfo:        info,
		funding:          &WalletFunding{userId: uid, consumed: pre},
		preConsumedQuota: pre,
		tokenConsumed:    pre,
	}
}

func TestSettleflow_SessionSettle_PositiveDelta(t *testing.T) {
	truncate(t)
	const uid, tid = 510, 510
	seedUser(t, uid, 10000)
	seedToken(t, tid, uid, "sk-sess-pos", 8000)

	s := settleflowWalletSession(t, uid, tid, 2000, "sk-sess-pos")
	require.NoError(t, s.Settle(3000)) // delta +1000

	assert.Equal(t, 9000, getUserQuota(t, uid))
	assert.Equal(t, 7000, getTokenRemainQuota(t, tid))
}

func TestSettleflow_SessionSettle_NegativeDelta(t *testing.T) {
	truncate(t)
	const uid, tid = 511, 511
	seedUser(t, uid, 10000)
	seedToken(t, tid, uid, "sk-sess-neg", 8000)

	s := settleflowWalletSession(t, uid, tid, 2000, "sk-sess-neg")
	require.NoError(t, s.Settle(1000)) // delta -1000 => refund

	assert.Equal(t, 11000, getUserQuota(t, uid))
	assert.Equal(t, 9000, getTokenRemainQuota(t, tid))
}

func TestSettleflow_SessionSettle_ZeroDeltaAndIdempotent(t *testing.T) {
	truncate(t)
	const uid, tid = 512, 512
	seedUser(t, uid, 10000)
	seedToken(t, tid, uid, "sk-sess-zero", 8000)

	s := settleflowWalletSession(t, uid, tid, 2000, "sk-sess-zero")
	require.NoError(t, s.Settle(2000)) // delta 0
	assert.Equal(t, 10000, getUserQuota(t, uid))

	// Second settle is a no-op (already settled), even with a different quota.
	require.NoError(t, s.Settle(5000))
	assert.Equal(t, 10000, getUserQuota(t, uid), "settled session ignores further settle")
	assert.Equal(t, 8000, getTokenRemainQuota(t, tid))
}

// --- BillingSession.Reserve / accessors ------------------------------------

func TestSettleflow_SessionReserve_IncreasesPreConsume(t *testing.T) {
	truncate(t)
	const uid, tid = 513, 513
	seedUser(t, uid, 10000)
	seedToken(t, tid, uid, "sk-sess-res", 8000)

	s := settleflowWalletSession(t, uid, tid, 1000, "sk-sess-res")
	require.NoError(t, s.Reserve(3000)) // delta +2000

	assert.Equal(t, 3000, s.GetPreConsumedQuota())
	assert.Equal(t, 8000, getUserQuota(t, uid), "extra 2000 reserved from wallet")
	assert.Equal(t, 6000, getTokenRemainQuota(t, tid))
}

func TestSettleflow_SessionReserve_NoopWhenTargetNotHigher(t *testing.T) {
	truncate(t)
	const uid, tid = 514, 514
	seedUser(t, uid, 10000)
	seedToken(t, tid, uid, "sk-sess-res2", 8000)

	s := settleflowWalletSession(t, uid, tid, 3000, "sk-sess-res2")
	require.NoError(t, s.Reserve(3000)) // target == pre => noop
	require.NoError(t, s.Reserve(1000)) // target < pre => noop

	assert.Equal(t, 3000, s.GetPreConsumedQuota())
	assert.Equal(t, 10000, getUserQuota(t, uid))
}

func TestSettleflow_SessionNeedsRefund_Transitions(t *testing.T) {
	truncate(t)
	const uid, tid = 515, 515
	seedUser(t, uid, 10000)
	seedToken(t, tid, uid, "sk-sess-nr", 8000)

	s := settleflowWalletSession(t, uid, tid, 2000, "sk-sess-nr")
	assert.True(t, s.NeedsRefund(), "tokenConsumed>0 and not settled => needs refund")

	require.NoError(t, s.Settle(2000))
	assert.False(t, s.NeedsRefund(), "settled session no longer needs refund")
}
