package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// topup.go: pay-money computation, min-topup normalization by display type,
// epay client construction, order locking, and request-handler input guards.

func setQuotaDisplayType(t *testing.T, v string) {
	t.Helper()
	gs := operation_setting.GetGeneralSetting()
	prev := gs.QuotaDisplayType
	gs.QuotaDisplayType = v
	t.Cleanup(func() { gs.QuotaDisplayType = prev })
}

func TestGetPayMoney_CurrencyDisplayLinear(t *testing.T) {
	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeUSD)
	ratio := common.GetTopupGroupRatio("default")
	if ratio == 0 {
		ratio = 1
	}
	// No AmountDiscount configured for these amounts -> discount 1.0.
	expected1 := 1 * operation_setting.Price * ratio
	assert.InDelta(t, expected1, getPayMoney(1, "default"), 1e-9)
	// Linear in amount when no per-amount discount applies.
	assert.InDelta(t, 10*operation_setting.Price*ratio, getPayMoney(10, "default"), 1e-9)
}

func TestGetPayMoney_TokensDisplayDividesByQuotaPerUnit(t *testing.T) {
	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeTokens)
	ratio := common.GetTopupGroupRatio("default")
	if ratio == 0 {
		ratio = 1
	}
	// In TOKENS mode the amount (tokens) is divided by QuotaPerUnit first.
	amount := int64(common.QuotaPerUnit) // == 1 unit
	expected := 1 * operation_setting.Price * ratio
	assert.InDelta(t, expected, getPayMoney(amount, "default"), 1e-6)
}

func TestGetMinTopup_ByDisplayType(t *testing.T) {
	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeUSD)
	assert.Equal(t, int64(operation_setting.MinTopUp), getMinTopup())

	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeTokens)
	assert.Equal(t, int64(float64(operation_setting.MinTopUp)*common.QuotaPerUnit), getMinTopup())
}

func TestGetEpayClient_NilWhenUnconfigured(t *testing.T) {
	prevAddr, prevId, prevKey := operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey
	operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey = "", "", ""
	t.Cleanup(func() {
		operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey = prevAddr, prevId, prevKey
	})
	assert.Nil(t, GetEpayClient())
}

func TestLockUnlockOrder_RefCountCleanup(t *testing.T) {
	key := uniq("order")
	LockOrder(key)
	// While held, the ref-counted mutex exists in the map.
	_, ok := orderLocks.Load(key)
	assert.True(t, ok)
	UnlockOrder(key)
	// Last releaser removes the entry.
	_, ok = orderLocks.Load(key)
	assert.False(t, ok, "order lock entry must be cleaned up on final unlock")

	// Unlocking an unknown key is a no-op (must not panic).
	UnlockOrder("never-locked")
}

func TestLockOrder_DistinctKeysIndependent(t *testing.T) {
	k1, k2 := uniq("o"), uniq("o")
	LockOrder(k1)
	LockOrder(k2) // different key must not block
	UnlockOrder(k1)
	UnlockOrder(k2)
}

func TestRequestEpay_BadJSON(t *testing.T) {
	ctx, rec := newRawCtx(t, "POST", "/api/user/pay", "{ not json")
	ctx.Set("id", 1)
	RequestEpay(ctx)
	assert.Equal(t, 200, rec.Code)
	var out map[string]any
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "error", out["message"])
}

func TestRequestEpay_BelowMinAmount(t *testing.T) {
	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeUSD)
	ctx, rec := newCtx(t, "POST", "/api/user/pay", EpayRequest{Amount: 0, PaymentMethod: "alipay"})
	ctx.Set("id", 1)
	RequestEpay(ctx)
	var out map[string]any
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "error", out["message"])
}

func TestRequestAmount_Guards(t *testing.T) {
	setQuotaDisplayType(t, operation_setting.QuotaDisplayTypeUSD)
	// Bad JSON.
	ctx, rec := newRawCtx(t, "POST", "/api/user/amount", "nope")
	ctx.Set("id", 1)
	RequestAmount(ctx)
	var out map[string]any
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "error", out["message"])

	// Below minimum.
	ctx2, rec2 := newCtx(t, "POST", "/api/user/amount", AmountRequest{Amount: 0})
	ctx2.Set("id", 1)
	RequestAmount(ctx2)
	var out2 map[string]any
	require.NoError(t, common.Unmarshal(rec2.Body.Bytes(), &out2))
	assert.Equal(t, "error", out2["message"])
}

func TestAdminCompleteTopUp_EmptyTradeNo(t *testing.T) {
	ctx, rec := newCtx(t, "POST", "/api/topup/complete", AdminCompleteTopupRequest{TradeNo: ""})
	ctx.Set("id", 1)
	AdminCompleteTopUp(ctx)
	resp := decodeResp(t, rec)
	assert.False(t, resp.Success)
}
