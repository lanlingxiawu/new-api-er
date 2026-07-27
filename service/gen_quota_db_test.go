package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// quota.go (PostConsumeQuota, PreConsumeTokenQuota) + billing.go (SettleBilling,
// PreConsumeBilling happy path). DB-backed via shared TestMain/seed helpers.
// ===========================================================================

func quotadbRelayInfo(userID, tokenID int, key string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId:        userID,
		TokenId:       tokenID,
		TokenKey:      key,
		BillingSource: BillingSourceWallet,
		UserQuota:     1_000_000, // high so quota-low notify never fires
		UserSetting:   dto.UserSetting{},
	}
}

// --- PostConsumeQuota -------------------------------------------------------

func TestQuotadb_PostConsumeQuota_WalletPositive(t *testing.T) {
	truncate(t)
	const uid, tid = 100, 100
	seedUser(t, uid, 10000)
	seedToken(t, tid, uid, "sk-pcq-pos", 8000)

	info := quotadbRelayInfo(uid, tid, "sk-pcq-pos")
	require.NoError(t, PostConsumeQuota(info, 1500, 0, false))

	assert.Equal(t, 8500, getUserQuota(t, uid))
	assert.Equal(t, 6500, getTokenRemainQuota(t, tid))
	assert.Equal(t, 1500, getTokenUsedQuota(t, tid))
}

func TestQuotadb_PostConsumeQuota_WalletNegativeRefunds(t *testing.T) {
	truncate(t)
	const uid, tid = 101, 101
	seedUser(t, uid, 10000)
	seedToken(t, tid, uid, "sk-pcq-neg", 8000)

	info := quotadbRelayInfo(uid, tid, "sk-pcq-neg")
	require.NoError(t, PostConsumeQuota(info, -500, 0, false))

	assert.Equal(t, 10500, getUserQuota(t, uid), "negative quota returns to wallet")
	assert.Equal(t, 8500, getTokenRemainQuota(t, tid))
}

func TestQuotadb_PostConsumeQuota_PlaygroundSkipsToken(t *testing.T) {
	truncate(t)
	const uid, tid = 102, 102
	seedUser(t, uid, 10000)
	seedToken(t, tid, uid, "sk-pcq-pg", 8000)

	info := quotadbRelayInfo(uid, tid, "sk-pcq-pg")
	info.IsPlayground = true
	require.NoError(t, PostConsumeQuota(info, 1500, 0, false))

	assert.Equal(t, 8500, getUserQuota(t, uid), "wallet still charged")
	assert.Equal(t, 8000, getTokenRemainQuota(t, tid), "token untouched for playground")
}

func TestQuotadb_PostConsumeQuota_SubscriptionMissingId(t *testing.T) {
	truncate(t)
	info := quotadbRelayInfo(103, 0, "")
	info.BillingSource = BillingSourceSubscription
	info.SubscriptionId = 0

	err := PostConsumeQuota(info, 100, 0, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subscription id is missing")
}

func TestQuotadb_PostConsumeQuota_SubscriptionZeroDeltaNoError(t *testing.T) {
	truncate(t)
	const uid, tid = 104, 104
	seedUser(t, uid, 0)
	seedToken(t, tid, uid, "sk-pcq-sub0", 8000)
	info := quotadbRelayInfo(uid, tid, "sk-pcq-sub0")
	info.BillingSource = BillingSourceSubscription
	info.SubscriptionId = 999 // never touched because delta==0
	// quota 0 => delta 0 => subscription untouched, token untouched (quota not >0, so Increase 0)
	require.NoError(t, PostConsumeQuota(info, 0, 0, false))
	assert.Equal(t, 8000, getTokenRemainQuota(t, tid))
}

// --- PreConsumeTokenQuota ---------------------------------------------------

func TestQuotadb_PreConsumeTokenQuota_NegativeRejected(t *testing.T) {
	info := quotadbRelayInfo(0, 0, "")
	err := PreConsumeTokenQuota(info, -1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "负数")
}

func TestQuotadb_PreConsumeTokenQuota_PlaygroundNoop(t *testing.T) {
	info := quotadbRelayInfo(0, 0, "")
	info.IsPlayground = true
	require.NoError(t, PreConsumeTokenQuota(info, 5000))
}

func TestQuotadb_PreConsumeTokenQuota_Insufficient(t *testing.T) {
	truncate(t)
	const uid, tid = 110, 110
	seedUser(t, uid, 10000)
	seedToken(t, tid, uid, "sk-pctq-low", 100)

	info := quotadbRelayInfo(uid, tid, "sk-pctq-low")
	err := PreConsumeTokenQuota(info, 5000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token quota is not enough")
	assert.Equal(t, 100, getTokenRemainQuota(t, tid), "no deduction on rejection")
}

func TestQuotadb_PreConsumeTokenQuota_Success(t *testing.T) {
	truncate(t)
	const uid, tid = 111, 111
	seedUser(t, uid, 10000)
	seedToken(t, tid, uid, "sk-pctq-ok", 5000)

	info := quotadbRelayInfo(uid, tid, "sk-pctq-ok")
	require.NoError(t, PreConsumeTokenQuota(info, 2000))
	assert.Equal(t, 3000, getTokenRemainQuota(t, tid))
}

func TestQuotadb_PreConsumeTokenQuota_UnlimitedSkipsCheck(t *testing.T) {
	truncate(t)
	const uid, tid = 112, 112
	seedUser(t, uid, 10000)
	seedToken(t, tid, uid, "sk-pctq-unl", 100)

	info := quotadbRelayInfo(uid, tid, "sk-pctq-unl")
	info.TokenUnlimited = true
	// Even though remain(100) < 5000, unlimited bypasses the guard and still deducts.
	require.NoError(t, PreConsumeTokenQuota(info, 5000))
	assert.Equal(t, -4900, getTokenRemainQuota(t, tid))
}

// --- billing.go SettleBilling (fallback path, no BillingSession) ------------

func TestQuotadb_SettleBilling_FallbackAdjustsDelta(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)

	const uid, tid = 120, 120
	seedUser(t, uid, 10000)
	seedToken(t, tid, uid, "sk-settle-fb", 8000)

	info := quotadbRelayInfo(uid, tid, "sk-settle-fb")
	info.FinalPreConsumedQuota = 2000
	// actual 3000, pre 2000 => delta +1000 charged
	require.NoError(t, SettleBilling(c, info, 3000))

	assert.Equal(t, 9000, getUserQuota(t, uid))
	assert.Equal(t, 7000, getTokenRemainQuota(t, tid))
}

func TestQuotadb_SettleBilling_FallbackZeroDeltaNoop(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)

	const uid, tid = 121, 121
	seedUser(t, uid, 10000)
	seedToken(t, tid, uid, "sk-settle-zero", 8000)

	info := quotadbRelayInfo(uid, tid, "sk-settle-zero")
	info.FinalPreConsumedQuota = 2000
	require.NoError(t, SettleBilling(c, info, 2000))

	assert.Equal(t, 10000, getUserQuota(t, uid), "no change when actual==preconsumed")
	assert.Equal(t, 8000, getTokenRemainQuota(t, tid))
}

// --- billing.go PreConsumeBilling (happy path creates session + deducts) ----

func TestQuotadb_PreConsumeBilling_WalletHappyPath(t *testing.T) {
	truncate(t)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)

	const uid, tid = 130, 130
	seedUser(t, uid, 10000)
	seedToken(t, tid, uid, "sk-pcb-ok", 8000)

	info := quotadbRelayInfo(uid, tid, "sk-pcb-ok")
	info.UserSetting = dto.UserSetting{BillingPreference: "wallet_only"}

	apiErr := PreConsumeBilling(c, 1500, info)
	require.Nil(t, apiErr)
	require.NotNil(t, info.Billing, "billing session attached")
	assert.Equal(t, 1500, info.Billing.GetPreConsumedQuota())
	assert.Equal(t, 8500, getUserQuota(t, uid))
	assert.Equal(t, 6500, getTokenRemainQuota(t, tid))
}

func TestQuotadb_PreConsumeBilling_ClampRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{
		QuotaClamp: &common.QuotaClamp{Op: "QuotaFromFloat", Kind: common.QuotaClampOverflow, Clamped: common.MaxQuota},
	}
	apiErr := PreConsumeBilling(c, common.MaxQuota, info)
	require.NotNil(t, apiErr)
	assert.Nil(t, info.Billing)
}
