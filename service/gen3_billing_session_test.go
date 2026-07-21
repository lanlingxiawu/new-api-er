package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// billing_session.go deep paths — NewBillingSession factory (preference
// fallbacks), preConsume trust bypass, shouldTrust, Reserve/rollback, Refund.
// Billing-critical. DB-backed. Values pinned exactly.
// ===========================================================================

func bsCtx(t *testing.T, tokenQuota int) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("token_quota", tokenQuota)
	return c
}

func bsRelayInfo(uid, tid int, key, pref string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId:          uid,
		TokenId:         tid,
		TokenKey:        key,
		OriginModelName: "gpt-4o",
		RequestId:       "req-bs",
		UserSetting:     dto.UserSetting{BillingPreference: pref},
	}
}

// --- NewBillingSession: nil / wallet_only happy / errors --------------------

func TestBS_NewSession_NilRelayInfo(t *testing.T) {
	_, apiErr := NewBillingSession(bsCtx(t, 0), nil, 100)
	require.NotNil(t, apiErr)
}

func TestBS_NewSession_WalletOnly_Deducts(t *testing.T) {
	truncate(t)
	const uid, tid = 3001, 3001
	seedUser(t, uid, 100000)
	seedToken(t, tid, uid, "sk-bs-wallet", 80000)

	info := bsRelayInfo(uid, tid, "sk-bs-wallet", "wallet_only")
	s, apiErr := NewBillingSession(bsCtx(t, 0), info, 2000)
	require.Nil(t, apiErr)
	require.NotNil(t, s)
	assert.Equal(t, 2000, s.GetPreConsumedQuota())
	assert.Equal(t, 98000, getUserQuota(t, uid))
	assert.Equal(t, 78000, getTokenRemainQuota(t, tid))
	assert.Equal(t, BillingSourceWallet, info.BillingSource)
	assert.Equal(t, 2000, info.FinalPreConsumedQuota)
}

func TestBS_NewSession_WalletOnly_InsufficientZeroQuota(t *testing.T) {
	truncate(t)
	const uid = 3002
	seedUser(t, uid, 0)

	info := bsRelayInfo(uid, 0, "", "wallet_only")
	_, apiErr := NewBillingSession(bsCtx(t, 0), info, 2000)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
}

func TestBS_NewSession_WalletOnly_ClampWhenPreExceeds(t *testing.T) {
	truncate(t)
	const uid = 3003
	seedUser(t, uid, 1000)

	info := bsRelayInfo(uid, 0, "", "wallet_only")
	_, apiErr := NewBillingSession(bsCtx(t, 0), info, 5000) // pre > balance
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
	// Nothing deducted on the clamp-reject path.
	assert.Equal(t, 1000, getUserQuota(t, uid))
}

// --- NewBillingSession: trust bypass (preConsume trusted) -------------------

func TestBS_NewSession_TrustBypass_NoDeduction(t *testing.T) {
	truncate(t)
	const uid, tid = 3004, 3004
	// UserQuota must exceed GetTrustQuota() (= 10 * QuotaPerUnit).
	bigQuota := common.GetTrustQuota() + 1_000_000
	seedUser(t, uid, bigQuota)
	seedToken(t, tid, uid, "sk-bs-trust", 999999)

	info := bsRelayInfo(uid, tid, "sk-bs-trust", "wallet_only")
	info.TokenUnlimited = true // token side trusted

	s, apiErr := NewBillingSession(bsCtx(t, 0), info, 3000)
	require.Nil(t, apiErr)
	require.NotNil(t, s)
	// Trusted => effectiveQuota 0, nothing pre-consumed.
	assert.Equal(t, 0, s.GetPreConsumedQuota())
	assert.Equal(t, bigQuota, getUserQuota(t, uid), "trusted path deducts nothing from wallet")
	assert.Equal(t, 999999, getTokenRemainQuota(t, tid), "trusted path deducts nothing from token")
}

// --- NewBillingSession: subscription_first with no active sub => wallet -----

func TestBS_NewSession_SubscriptionFirst_NoSubFallsToWallet(t *testing.T) {
	truncate(t)
	const uid, tid = 3005, 3005
	seedUser(t, uid, 100000)
	seedToken(t, tid, uid, "sk-bs-subfirst", 80000)

	info := bsRelayInfo(uid, tid, "sk-bs-subfirst", "subscription_first")
	s, apiErr := NewBillingSession(bsCtx(t, 0), info, 1500)
	require.Nil(t, apiErr)
	require.NotNil(t, s)
	// No active subscription => wallet path used.
	assert.Equal(t, BillingSourceWallet, info.BillingSource)
	assert.Equal(t, 98500, getUserQuota(t, uid))
}

func TestBS_NewSession_WalletFirst_FallsBackToSubscriptionThenErrors(t *testing.T) {
	truncate(t)
	const uid = 3008
	seedUser(t, uid, 0) // wallet empty => wallet path fails with insufficient quota

	info := bsRelayInfo(uid, 0, "", "wallet_first")
	// Wallet insufficient => fall back to subscription; no active subscription
	// exists so the subscription pre-consume also fails and an error is returned.
	_, apiErr := NewBillingSession(bsCtx(t, 0), info, 2000)
	require.NotNil(t, apiErr)
}

func TestBS_NewSession_SubscriptionOnly_NoSubErrors(t *testing.T) {
	truncate(t)
	const uid = 3009
	seedUser(t, uid, 100000)

	info := bsRelayInfo(uid, 0, "", "subscription_only")
	// subscription_only with no active subscription => error (no wallet fallback).
	_, apiErr := NewBillingSession(bsCtx(t, 0), info, 1000)
	require.NotNil(t, apiErr)
}

// --- shouldTrust decision table --------------------------------------------

func TestBS_ShouldTrust_DecisionTable(t *testing.T) {
	c := bsCtx(t, 0)

	// ForcePreConsume disables trust regardless of everything else.
	sForce := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{ForcePreConsume: true, TokenUnlimited: true, UserQuota: 1 << 40},
		funding:   &WalletFunding{userId: 1},
	}
	assert.False(t, sForce.shouldTrust(c))

	// Wallet + token trusted + quota above trust => trusted.
	big := common.GetTrustQuota() + 1
	sWallet := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{TokenUnlimited: true, UserQuota: big},
		funding:   &WalletFunding{userId: 1},
	}
	assert.True(t, sWallet.shouldTrust(c))

	// Wallet quota not above trust => not trusted.
	sLow := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{TokenUnlimited: true, UserQuota: common.GetTrustQuota()},
		funding:   &WalletFunding{userId: 1},
	}
	assert.False(t, sLow.shouldTrust(c))

	// Token not trusted (limited + context token_quota below trust) => not trusted.
	sTok := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{TokenUnlimited: false, UserQuota: big},
		funding:   &WalletFunding{userId: 1},
	}
	assert.False(t, sTok.shouldTrust(bsCtx(t, 1)))

	// Subscription funding never trusts, even when token+amount would qualify.
	sSub := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{TokenUnlimited: true, UserQuota: big},
		funding:   &SubscriptionFunding{subscriptionId: 1},
	}
	assert.False(t, sSub.shouldTrust(c))
}

// --- Reserve: funding reserved then token fails => rollback -----------------

func TestBS_Reserve_TokenFailRollsBackWallet(t *testing.T) {
	truncate(t)
	const uid, tid = 3006, 3006
	seedUser(t, uid, 100000)
	// Token remain is tiny so reserveToken's PreConsumeTokenQuota fails.
	seedToken(t, tid, uid, "sk-bs-rollback", 100)

	info := bsRelayInfo(uid, tid, "sk-bs-rollback", "wallet_only")
	s := &BillingSession{
		relayInfo:        info,
		funding:          &WalletFunding{userId: uid, consumed: 1000},
		preConsumedQuota: 1000,
		tokenConsumed:    1000,
	}
	// Reserve wants +5000 more; wallet decreases, then token pre-consume fails,
	// so the wallet reservation is rolled back.
	err := s.Reserve(6000)
	require.Error(t, err)
	assert.Equal(t, 100000, getUserQuota(t, uid), "wallet fully rolled back after token failure")
	assert.Equal(t, 1000, s.GetPreConsumedQuota(), "preConsumedQuota unchanged on failure")
}

// --- Refund (async, wallet) -------------------------------------------------

func TestBS_Refund_WalletAsync(t *testing.T) {
	truncate(t)
	const uid, tid = 3007, 3007
	seedUser(t, uid, 50000)
	seedToken(t, tid, uid, "sk-bs-refund", 40000)

	info := bsRelayInfo(uid, tid, "sk-bs-refund", "wallet_only")
	s := &BillingSession{
		relayInfo:        info,
		funding:          &WalletFunding{userId: uid, consumed: 3000},
		preConsumedQuota: 3000,
		tokenConsumed:    3000,
	}
	require.True(t, s.NeedsRefund())

	s.Refund(bsCtx(t, 0))

	// Refund is fire-and-forget via gopool.Go; poll for the async effect.
	require.Eventually(t, func() bool {
		return getUserQuota(t, uid) == 53000 && getTokenRemainQuota(t, tid) == 43000
	}, 3*time.Second, 20*time.Millisecond)

	// Second Refund is a no-op (already refunded).
	s.Refund(bsCtx(t, 0))
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 53000, getUserQuota(t, uid), "double refund guarded")
}

// --- reserveFunding unsupported source --------------------------------------

func TestBS_ReserveFunding_UnsupportedSource(t *testing.T) {
	s := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{},
		funding:   &fakeFunding{},
	}
	err := s.reserveFunding(100)
	require.Error(t, err)
}

// fakeFunding is a FundingSource with an unrecognized Source() to hit the
// default branch of reserveFunding.
type fakeFunding struct{}

func (f *fakeFunding) Source() string          { return "mystery" }
func (f *fakeFunding) PreConsume(amount int) error { return nil }
func (f *fakeFunding) Settle(delta int) error   { return nil }
func (f *fakeFunding) Refund() error            { return nil }

var _ FundingSource = (*fakeFunding)(nil)
var _ = model.DB
