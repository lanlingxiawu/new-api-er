package model

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// topup.go — order create/query/complete, amount->quota conversion, provider
// guard, trade-no uniqueness, status transitions. Billing-critical: verify
// exact credited quota and that no order can be completed (credited) twice.
//
// TradeNo is UNIQUE; every fixture uses a unique trade number. Rows are cleaned
// up by id.
// ---------------------------------------------------------------------------

// mkTopUp inserts a pending TopUp with a unique trade_no and auto-cleanup.
func mkTopUp(t *testing.T, userId int, mut func(tp *TopUp)) *TopUp {
	t.Helper()
	requireDB(t)
	tp := &TopUp{
		Id:              nextTestID(),
		UserId:          userId,
		Amount:          10,
		Money:           10.0,
		TradeNo:         uniq("trade"),
		PaymentMethod:   PaymentMethodStripe,
		PaymentProvider: PaymentProviderStripe,
		PaymentCurrency: "usd",
		CreateTime:      common.GetTimestamp(),
		Status:          common.TopUpStatusPending,
	}
	if mut != nil {
		mut(tp)
	}
	require.NoError(t, tp.Insert())
	deleteByID(t, &TopUp{}, tp.Id)
	return tp
}

func TestTopUp_InsertGetUpdate(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	tp := mkTopUp(t, u.Id, nil)

	got := GetTopUpById(tp.Id)
	require.NotNil(t, got)
	assert.Equal(t, tp.TradeNo, got.TradeNo)

	byTrade := GetTopUpByTradeNo(tp.TradeNo)
	require.NotNil(t, byTrade)
	assert.Equal(t, tp.Id, byTrade.Id)

	// Update via Save
	tp.Status = common.TopUpStatusFailed
	require.NoError(t, tp.Update())
	assert.Equal(t, common.TopUpStatusFailed, GetTopUpById(tp.Id).Status)

	// Missing lookups return nil
	assert.Nil(t, GetTopUpById(nextTestID()))
	assert.Nil(t, GetTopUpByTradeNo(uniq("nope")))
}

func TestTopUpPlatformPaymentStatusPersistence(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	tp := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.PaymentProvider = PaymentProviderAlipay
	})

	rows, err := GetTopUpsForPlatformStatus([]string{tp.TradeNo, uniq("missing")})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, tp.Id, rows[0].Id)
	assert.Equal(t, tp.TradeNo, rows[0].TradeNo)
	assert.Equal(t, PaymentProviderAlipay, rows[0].PaymentProvider)

	checkedAt := common.GetTimestamp()
	require.NoError(t, UpdateTopUpPlatformPaymentStatus(
		tp.Id,
		PlatformPaymentStatusCredited,
		"TRADE_SUCCESS",
		checkedAt,
	))

	reloaded := GetTopUpById(tp.Id)
	require.NotNil(t, reloaded)
	assert.Equal(t, PlatformPaymentStatusCredited, reloaded.PlatformPaymentStatus)
	assert.Equal(t, "TRADE_SUCCESS", reloaded.PlatformPaymentStatusRaw)
	assert.Equal(t, checkedAt, reloaded.PlatformPaymentStatusCheckedAt)
}

func TestUpdateTopUpPlatformPaymentStatusValidation(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	tp := mkTopUp(t, u.Id, nil)

	assert.Error(t, UpdateTopUpPlatformPaymentStatus(tp.Id, "invalid", "raw", common.GetTimestamp()))
	assert.Error(t, UpdateTopUpPlatformPaymentStatus(tp.Id, PlatformPaymentStatusCredited, "", common.GetTimestamp()))
	assert.Error(t, UpdateTopUpPlatformPaymentStatus(tp.Id, PlatformPaymentStatusCredited, "TRADE_SUCCESS", 0))
	assert.ErrorIs(t, UpdateTopUpPlatformPaymentStatus(nextTestID(), PlatformPaymentStatusCredited, "TRADE_SUCCESS", common.GetTimestamp()), ErrTopUpNotFound)

	reloaded := GetTopUpById(tp.Id)
	require.NotNil(t, reloaded)
	assert.Empty(t, reloaded.PlatformPaymentStatus)
	assert.Empty(t, reloaded.PlatformPaymentStatusRaw)
	assert.Zero(t, reloaded.PlatformPaymentStatusCheckedAt)
}

// ---------------------------------------------------------------------------
// UpdatePendingTopUpStatus
// ---------------------------------------------------------------------------

func TestUpdatePendingTopUpStatus(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)

	// empty trade no
	assert.Error(t, UpdatePendingTopUpStatus("", "", common.TopUpStatusFailed))

	// not found
	assert.ErrorIs(t, UpdatePendingTopUpStatus(uniq("missing"), "", common.TopUpStatusFailed), ErrTopUpNotFound)

	// provider mismatch
	tp := mkTopUp(t, u.Id, func(tp *TopUp) { tp.PaymentProvider = PaymentProviderStripe })
	assert.ErrorIs(t, UpdatePendingTopUpStatus(tp.TradeNo, PaymentProviderInfini, common.TopUpStatusFailed), ErrPaymentMethodMismatch)

	// happy path: pending -> failed (empty expected provider skips provider check)
	require.NoError(t, UpdatePendingTopUpStatus(tp.TradeNo, "", common.TopUpStatusFailed))
	assert.Equal(t, common.TopUpStatusFailed, GetTopUpById(tp.Id).Status)

	// now not pending -> status invalid
	assert.ErrorIs(t, UpdatePendingTopUpStatus(tp.TradeNo, "", common.TopUpStatusSuccess), ErrTopUpStatusInvalid)
}

// ---------------------------------------------------------------------------
// Recharge (Stripe) — currency + amount window validation, snapshot crediting
// ---------------------------------------------------------------------------

func TestRecharge_Stripe_HappyPath(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *User) { u.Quota = 0 })
	// Money 10.00 => expectedCents = 1000; Amount 50000 is the locked quota snapshot.
	tp := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.Money = 10.0
		tp.Amount = 50000
		tp.PaymentCurrency = "usd"
	})

	// notified 1000 cents == expected upper bound, currency matches.
	require.NoError(t, Recharge(tp.TradeNo, "cus_123", "1.2.3.4", 1000, "USD"))

	reloaded, _ := GetUserById(u.Id, false)
	assert.Equal(t, 50000, reloaded.Quota) // credited the Amount snapshot exactly
	assert.Equal(t, common.TopUpStatusSuccess, GetTopUpById(tp.Id).Status)

	// Double recharge: order no longer pending -> generic failure, no re-credit.
	err := Recharge(tp.TradeNo, "cus_123", "1.2.3.4", 1000, "USD")
	assert.Error(t, err)
	reloaded2, _ := GetUserById(u.Id, false)
	assert.Equal(t, 50000, reloaded2.Quota) // unchanged
}

func TestRecharge_Stripe_PromoLowerAmountAccepted(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *User) { u.Quota = 0 })
	tp := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.Money = 10.0
		tp.Amount = 50000
		tp.PaymentCurrency = "usd"
	})
	// Promo code -> lower actual paid (500 cents < 1000) still credits full snapshot.
	require.NoError(t, Recharge(tp.TradeNo, "cus", "ip", 500, "usd"))
	reloaded, _ := GetUserById(u.Id, false)
	assert.Equal(t, 50000, reloaded.Quota)
}

func TestRecharge_Stripe_Rejections(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *User) { u.Quota = 0 })

	// empty reference id
	assert.Error(t, Recharge("", "c", "ip", 100, "usd"))
	// not found
	assert.Error(t, Recharge(uniq("missing"), "c", "ip", 100, "usd"))

	// provider mismatch (non-stripe order)
	nonStripe := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.PaymentProvider = PaymentProviderInfini
		tp.PaymentMethod = PaymentMethodInfini
	})
	assert.Error(t, Recharge(nonStripe.TradeNo, "c", "ip", 100, "usd"))
	assert.Equal(t, common.TopUpStatusPending, GetTopUpById(nonStripe.Id).Status)

	// currency mismatch
	curBad := mkTopUp(t, u.Id, func(tp *TopUp) { tp.PaymentCurrency = "usd"; tp.Money = 10 })
	assert.Error(t, Recharge(curBad.TradeNo, "c", "ip", 1000, "eur"))
	assert.Equal(t, common.TopUpStatusPending, GetTopUpById(curBad.Id).Status)

	// notified cents <= 0
	zero := mkTopUp(t, u.Id, func(tp *TopUp) { tp.PaymentCurrency = "usd"; tp.Money = 10 })
	assert.Error(t, Recharge(zero.TradeNo, "c", "ip", 0, "usd"))

	// notified cents > expected (overpay) rejected — guards against overcredit
	over := mkTopUp(t, u.Id, func(tp *TopUp) { tp.PaymentCurrency = "usd"; tp.Money = 10; tp.Amount = 50000 })
	assert.Error(t, Recharge(over.TradeNo, "c", "ip", 1001, "usd"))
	assert.Equal(t, common.TopUpStatusPending, GetTopUpById(over.Id).Status)

	// invalid quota snapshot (Amount <= 0)
	badQuota := mkTopUp(t, u.Id, func(tp *TopUp) { tp.PaymentCurrency = "usd"; tp.Money = 10; tp.Amount = 0 })
	assert.Error(t, Recharge(badQuota.TradeNo, "c", "ip", 1000, "usd"))

	// user quota untouched throughout
	reloaded, _ := GetUserById(u.Id, false)
	assert.Equal(t, 0, reloaded.Quota)
}

// ---------------------------------------------------------------------------
// ManualCompleteTopUp — provider-dependent conversion + idempotency
// ---------------------------------------------------------------------------

func TestManualCompleteTopUp_StripeSnapshot(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *User) { u.Quota = 0 })
	// Stripe/Infini: Amount is the quota snapshot, used directly.
	tp := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.PaymentProvider = PaymentProviderStripe
		tp.Amount = 33333
	})
	require.NoError(t, ManualCompleteTopUp(tp.TradeNo, "ip"))
	reloaded, _ := GetUserById(u.Id, false)
	assert.Equal(t, 33333, reloaded.Quota)
	assert.Equal(t, common.TopUpStatusSuccess, GetTopUpById(tp.Id).Status)

	// Idempotent: completing again returns nil and does NOT re-credit.
	require.NoError(t, ManualCompleteTopUp(tp.TradeNo, "ip"))
	reloaded2, _ := GetUserById(u.Id, false)
	assert.Equal(t, 33333, reloaded2.Quota)
}

func TestManualCompleteTopUp_QuotaPerUnitConversion(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *User) { u.Quota = 0 })
	// Non-stripe/infini: Amount (USD) * QuotaPerUnit.
	amount := int64(2)
	tp := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.PaymentProvider = PaymentProviderEpay
		tp.PaymentMethod = "epay"
		tp.Amount = amount
	})
	expected := int(decimal.NewFromInt(amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart())
	require.NoError(t, ManualCompleteTopUp(tp.TradeNo, "ip"))
	reloaded, _ := GetUserById(u.Id, false)
	assert.Equal(t, expected, reloaded.Quota)
}

func TestManualCompleteTopUp_Rejections(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)

	assert.Error(t, ManualCompleteTopUp("", "ip"))
	assert.Error(t, ManualCompleteTopUp(uniq("missing"), "ip"))

	// non-pending, non-success -> error
	failed := mkTopUp(t, u.Id, func(tp *TopUp) { tp.Status = common.TopUpStatusFailed })
	assert.Error(t, ManualCompleteTopUp(failed.TradeNo, "ip"))

	// invalid amount (0) on a non-snapshot provider -> invalid quota error
	badEpay := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.PaymentProvider = PaymentProviderEpay
		tp.Amount = 0
	})
	assert.Error(t, ManualCompleteTopUp(badEpay.TradeNo, "ip"))
}

// ---------------------------------------------------------------------------
// Provider-specific Recharge* variants (structural mirror). One happy path +
// provider guard + idempotency per provider.
// ---------------------------------------------------------------------------

func TestRechargeCreem(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *User) { u.Quota = 0; u.Email = "" })
	// Creem credits Amount directly (int64) and can backfill an empty email.
	tp := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.PaymentProvider = PaymentProviderCreem
		tp.PaymentMethod = PaymentMethodCreem
		tp.Amount = 8888
	})
	require.NoError(t, RechargeCreem(tp.TradeNo, "new@example.com", "Name", "ip"))
	reloaded, _ := GetUserById(u.Id, true)
	assert.Equal(t, 8888, reloaded.Quota)
	assert.Equal(t, "new@example.com", reloaded.Email) // backfilled

	// provider mismatch -> RechargeCreem wraps it into a generic error.
	wrong := mkTopUp(t, u.Id, func(tp *TopUp) { tp.PaymentProvider = PaymentProviderStripe })
	assert.Error(t, RechargeCreem(wrong.TradeNo, "", "", "ip"))
}

func TestRechargeCreem_EmailNotOverwritten(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *User) { u.Quota = 0; u.Email = "existing@example.com" })
	tp := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.PaymentProvider = PaymentProviderCreem
		tp.PaymentMethod = PaymentMethodCreem
		tp.Amount = 100
	})
	require.NoError(t, RechargeCreem(tp.TradeNo, "other@example.com", "N", "ip"))
	reloaded, _ := GetUserById(u.Id, true)
	assert.Equal(t, "existing@example.com", reloaded.Email) // unchanged
}

func TestRechargeWaffo(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *User) { u.Quota = 0 })
	amount := int64(3)
	tp := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.PaymentProvider = PaymentProviderWaffo
		tp.PaymentMethod = PaymentMethodWaffo
		tp.Amount = amount
	})
	expected := int(decimal.NewFromInt(amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart())
	require.NoError(t, RechargeWaffo(tp.TradeNo, "ip"))
	reloaded, _ := GetUserById(u.Id, false)
	assert.Equal(t, expected, reloaded.Quota)

	// Idempotent: second call is a no-op (already success).
	require.NoError(t, RechargeWaffo(tp.TradeNo, "ip"))
	reloaded2, _ := GetUserById(u.Id, false)
	assert.Equal(t, expected, reloaded2.Quota)

	// provider guard
	wrong := mkTopUp(t, u.Id, func(tp *TopUp) { tp.PaymentProvider = PaymentProviderStripe })
	assert.Error(t, RechargeWaffo(wrong.TradeNo, "ip"))
}

func TestRechargeWaffoPancake(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *User) { u.Quota = 0 })
	amount := int64(4)
	tp := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.PaymentProvider = PaymentProviderWaffoPancake
		tp.PaymentMethod = PaymentMethodWaffoPancake
		tp.Amount = amount
	})
	expected := int(decimal.NewFromInt(amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart())
	require.NoError(t, RechargeWaffoPancake(tp.TradeNo))
	reloaded, _ := GetUserById(u.Id, false)
	assert.Equal(t, expected, reloaded.Quota)
	// idempotent
	require.NoError(t, RechargeWaffoPancake(tp.TradeNo))
	reloaded2, _ := GetUserById(u.Id, false)
	assert.Equal(t, expected, reloaded2.Quota)
}

func TestRechargeAlipay(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *User) { u.Quota = 0 })
	amount := int64(5)
	tp := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.PaymentProvider = PaymentProviderAlipay
		tp.PaymentMethod = PaymentMethodAlipay
		tp.Amount = amount
	})
	expected := int(decimal.NewFromInt(amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart())
	require.NoError(t, RechargeAlipay(tp.TradeNo, "ip"))
	reloaded, _ := GetUserById(u.Id, false)
	assert.Equal(t, expected, reloaded.Quota)
	require.NoError(t, RechargeAlipay(tp.TradeNo, "ip")) // idempotent
	reloaded2, _ := GetUserById(u.Id, false)
	assert.Equal(t, expected, reloaded2.Quota)

	wrong := mkTopUp(t, u.Id, func(tp *TopUp) { tp.PaymentProvider = PaymentProviderStripe })
	assert.Error(t, RechargeAlipay(wrong.TradeNo, "ip"))
}

func TestRechargeWechat(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *User) { u.Quota = 0 })
	amount := int64(6)
	tp := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.PaymentProvider = PaymentProviderWechat
		tp.PaymentMethod = PaymentMethodWechat
		tp.Amount = amount
	})
	expected := int(decimal.NewFromInt(amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart())
	require.NoError(t, RechargeWechat(tp.TradeNo, "ip"))
	reloaded, _ := GetUserById(u.Id, false)
	assert.Equal(t, expected, reloaded.Quota)
	require.NoError(t, RechargeWechat(tp.TradeNo, "ip")) // idempotent
	reloaded2, _ := GetUserById(u.Id, false)
	assert.Equal(t, expected, reloaded2.Quota)
}

func TestRechargeInfini(t *testing.T) {
	requireDB(t)
	u := mkUser(t, func(u *User) { u.Quota = 0 })
	// Infini: Amount is the quota snapshot, used directly.
	tp := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.PaymentProvider = PaymentProviderInfini
		tp.PaymentMethod = PaymentMethodInfini
		tp.Amount = 12321
	})
	require.NoError(t, RechargeInfini(tp.TradeNo, "ip"))
	reloaded, _ := GetUserById(u.Id, false)
	assert.Equal(t, 12321, reloaded.Quota)
	require.NoError(t, RechargeInfini(tp.TradeNo, "ip")) // idempotent
	reloaded2, _ := GetUserById(u.Id, false)
	assert.Equal(t, 12321, reloaded2.Quota)

	wrong := mkTopUp(t, u.Id, func(tp *TopUp) { tp.PaymentProvider = PaymentProviderStripe })
	assert.Error(t, RechargeInfini(wrong.TradeNo, "ip"))

	// empty trade no
	assert.Error(t, RechargeInfini("", "ip"))
}

func TestRecharge_ProviderGuards_EmptyAndMismatch(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)

	// empty trade no on every variant
	assert.Error(t, RechargeCreem("", "", "", "ip"))
	assert.Error(t, RechargeWaffo("", "ip"))
	assert.Error(t, RechargeWaffoPancake(""))
	assert.Error(t, RechargeAlipay("", "ip"))
	assert.Error(t, RechargeWechat("", "ip"))

	// provider mismatch on the variants not covered elsewhere
	wrongPancake := mkTopUp(t, u.Id, func(tp *TopUp) { tp.PaymentProvider = PaymentProviderStripe })
	assert.Error(t, RechargeWaffoPancake(wrongPancake.TradeNo))
	wrongWechat := mkTopUp(t, u.Id, func(tp *TopUp) { tp.PaymentProvider = PaymentProviderStripe })
	assert.Error(t, RechargeWechat(wrongWechat.TradeNo, "ip"))

	// non-pending (failed) status -> error on idempotent-style providers
	failedInfini := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.PaymentProvider = PaymentProviderInfini
		tp.Status = common.TopUpStatusFailed
	})
	assert.Error(t, RechargeInfini(failedInfini.TradeNo, "ip"))
}

// ---------------------------------------------------------------------------
// Listing / filtering / keyset export
// ---------------------------------------------------------------------------

func TestListTopUps_Filters(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	base := common.GetTimestamp()
	a := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.Status = common.TopUpStatusSuccess
		tp.PaymentMethod = PaymentMethodStripe
		tp.CreateTime = base - 100
	})
	b := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.Status = common.TopUpStatusPending
		tp.PaymentMethod = PaymentMethodAlipay
		tp.CreateTime = base - 50
	})

	page := &common.PageInfo{Page: 1, PageSize: 100}

	// by user -> both
	rows, total, err := ListTopUps(TopUpListFilter{UserId: u.Id}, page)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Len(t, rows, 2)
	// ordered id desc
	assert.Equal(t, b.Id, rows[0].Id)

	// status filter
	_, total, err = ListTopUps(TopUpListFilter{UserId: u.Id, Status: common.TopUpStatusSuccess}, page)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)

	// payment method filter
	_, total, err = ListTopUps(TopUpListFilter{UserId: u.Id, PaymentMethod: PaymentMethodAlipay}, page)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)

	// keyword by trade_no exact
	rows, total, err = ListTopUps(TopUpListFilter{UserId: u.Id, Keyword: a.TradeNo}, page)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	assert.Equal(t, a.Id, rows[0].Id)

	// time range excluding the older one
	_, total, err = ListTopUps(TopUpListFilter{UserId: u.Id, StartTime: base - 60}, page)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)

	// end time excluding the newer one
	_, total, err = ListTopUps(TopUpListFilter{UserId: u.Id, EndTime: base - 60}, page)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)

	// BeforeID cursor: only rows with id < b.Id
	rows, _, err = ListTopUps(TopUpListFilter{UserId: u.Id, BeforeID: int64(b.Id)}, page)
	require.NoError(t, err)
	for _, r := range rows {
		assert.Less(t, int64(r.Id), int64(b.Id))
	}
}

func TestListTopUps_EnforceWindow(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	// A row older than the 30-day window must be excluded when EnforceWindow.
	old := mkTopUp(t, u.Id, func(tp *TopUp) {
		tp.CreateTime = common.GetTimestamp() - topUpQueryWindowSeconds - 3600
	})
	page := &common.PageInfo{Page: 1, PageSize: 100}

	_, total, err := ListTopUps(TopUpListFilter{UserId: u.Id, EnforceWindow: true}, page)
	require.NoError(t, err)
	assert.EqualValues(t, 0, total)

	// Without EnforceWindow the old row is visible.
	_, total, err = ListTopUps(TopUpListFilter{UserId: u.Id, EnforceWindow: false}, page)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	_ = old
}

func TestListTopUps_InvalidKeyword(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	page := &common.PageInfo{Page: 1, PageSize: 10}
	// consecutive % is rejected by sanitizeLikePattern.
	_, _, err := ListTopUps(TopUpListFilter{UserId: u.Id, Keyword: "a%%b"}, page)
	assert.Error(t, err)
}

func TestFetchTopUpExportBatch(t *testing.T) {
	requireDB(t)
	u := mkUser(t, nil)
	a := mkTopUp(t, u.Id, nil)
	b := mkTopUp(t, u.Id, nil)
	c := mkTopUp(t, u.Id, nil)

	// limit <= 0 -> nil, nil
	batch, err := FetchTopUpExportBatch(TopUpListFilter{UserId: u.Id}, math.MaxInt64, 0)
	require.NoError(t, err)
	assert.Nil(t, batch)

	// first keyset page (before = MaxInt64) returns all three, id desc
	batch, err = FetchTopUpExportBatch(TopUpListFilter{UserId: u.Id}, math.MaxInt64, 2)
	require.NoError(t, err)
	require.Len(t, batch, 2)
	assert.Equal(t, c.Id, batch[0].Id)
	assert.Equal(t, b.Id, batch[1].Id)

	// next page continues from last id
	batch2, err := FetchTopUpExportBatch(TopUpListFilter{UserId: u.Id}, int64(b.Id), 2)
	require.NoError(t, err)
	require.Len(t, batch2, 1)
	assert.Equal(t, a.Id, batch2[0].Id)

	// invalid keyword surfaces error
	_, err = FetchTopUpExportBatch(TopUpListFilter{UserId: u.Id, Keyword: "a%%b"}, math.MaxInt64, 2)
	assert.Error(t, err)
}

func TestTopUpQueryCutoff(t *testing.T) {
	// Cutoff is exactly now - window.
	before := common.GetTimestamp() - topUpQueryWindowSeconds
	got := topUpQueryCutoff()
	assert.GreaterOrEqual(t, got, before)
	assert.LessOrEqual(t, got, common.GetTimestamp()-topUpQueryWindowSeconds+2)
}
