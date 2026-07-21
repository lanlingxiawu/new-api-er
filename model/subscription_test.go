package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func subMakePlan(t *testing.T, mut func(*SubscriptionPlan)) *SubscriptionPlan {
	t.Helper()
	requireDB(t)
	p := &SubscriptionPlan{
		Title:         uniq("plan"),
		Currency:      "USD",
		PriceAmount:   0,
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       true,
		TotalAmount:   0,
	}
	if mut != nil {
		mut(p)
	}
	require.NoError(t, DB.Create(p).Error)
	deleteByID(t, &SubscriptionPlan{}, p.Id)
	t.Cleanup(func() { InvalidateSubscriptionPlanCache(p.Id) })
	return p
}

func subMakeSub(t *testing.T, userID, planID int, mut func(*UserSubscription)) *UserSubscription {
	t.Helper()
	requireDB(t)
	s := &UserSubscription{
		UserId:              userID,
		PlanId:              planID,
		AmountTotal:         0,
		AmountUsed:          0,
		StartTime:           time.Now().Unix() - 60,
		EndTime:             time.Now().Unix() + 86400,
		Status:              "active",
		Source:              "order",
		AllowWalletOverflow: true,
	}
	if mut != nil {
		mut(s)
	}
	require.NoError(t, DB.Create(s).Error)
	deleteByID(t, &UserSubscription{}, s.Id)
	return s
}

func subCleanupSubs(t *testing.T, userID int) {
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Where("user_id = ?", userID).Delete(&UserSubscription{})
		}
	})
}

func subCleanupTopUp(t *testing.T, tradeNo string) {
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Where("trade_no = ?", tradeNo).Delete(&TopUp{})
		}
	})
}

func subCleanupPreConsume(t *testing.T, requestID string) {
	t.Cleanup(func() {
		if DB != nil {
			DB.Unscoped().Where("request_id = ?", requestID).Delete(&SubscriptionPreConsumeRecord{})
		}
	})
}

func subCountSubs(t *testing.T, userID, planID int) int64 {
	t.Helper()
	var c int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ? AND plan_id = ?", userID, planID).Count(&c).Error)
	return c
}

func subReloadSub(t *testing.T, id int) *UserSubscription {
	t.Helper()
	var s UserSubscription
	require.NoError(t, DB.First(&s, id).Error)
	return &s
}

// ---------------------------------------------------------------------------
// Pure logic
// ---------------------------------------------------------------------------

func TestSubscription_NormalizeResetPeriod(t *testing.T) {
	assert.Equal(t, SubscriptionResetDaily, NormalizeResetPeriod(" daily "))
	assert.Equal(t, SubscriptionResetWeekly, NormalizeResetPeriod("weekly"))
	assert.Equal(t, SubscriptionResetMonthly, NormalizeResetPeriod("monthly"))
	assert.Equal(t, SubscriptionResetCustom, NormalizeResetPeriod("custom"))
	assert.Equal(t, SubscriptionResetNever, NormalizeResetPeriod("never"))
	assert.Equal(t, SubscriptionResetNever, NormalizeResetPeriod(""))
	assert.Equal(t, SubscriptionResetNever, NormalizeResetPeriod("bogus"))
	assert.Equal(t, SubscriptionResetNever, NormalizeResetPeriod("Daily")) // case sensitive
}

func TestSubscription_CalcPlanEndTime(t *testing.T) {
	start := time.Date(2021, 3, 15, 10, 0, 0, 0, time.UTC)

	_, err := calcPlanEndTime(start, nil)
	assert.Error(t, err)

	// duration_value <= 0 with non-custom unit -> error
	_, err = calcPlanEndTime(start, &SubscriptionPlan{DurationUnit: SubscriptionDurationMonth, DurationValue: 0})
	assert.Error(t, err)

	got, err := calcPlanEndTime(start, &SubscriptionPlan{DurationUnit: SubscriptionDurationYear, DurationValue: 2})
	require.NoError(t, err)
	assert.Equal(t, start.AddDate(2, 0, 0).Unix(), got)

	got, err = calcPlanEndTime(start, &SubscriptionPlan{DurationUnit: SubscriptionDurationMonth, DurationValue: 1})
	require.NoError(t, err)
	assert.Equal(t, start.AddDate(0, 1, 0).Unix(), got)

	got, err = calcPlanEndTime(start, &SubscriptionPlan{DurationUnit: SubscriptionDurationDay, DurationValue: 10})
	require.NoError(t, err)
	assert.Equal(t, start.Add(240*time.Hour).Unix(), got)

	got, err = calcPlanEndTime(start, &SubscriptionPlan{DurationUnit: SubscriptionDurationHour, DurationValue: 5})
	require.NoError(t, err)
	assert.Equal(t, start.Add(5*time.Hour).Unix(), got)

	// custom: value irrelevant, custom_seconds must be > 0
	got, err = calcPlanEndTime(start, &SubscriptionPlan{DurationUnit: SubscriptionDurationCustom, DurationValue: 0, CustomSeconds: 3600})
	require.NoError(t, err)
	assert.Equal(t, start.Add(3600*time.Second).Unix(), got)

	_, err = calcPlanEndTime(start, &SubscriptionPlan{DurationUnit: SubscriptionDurationCustom, CustomSeconds: 0})
	assert.Error(t, err)

	_, err = calcPlanEndTime(start, &SubscriptionPlan{DurationUnit: "weird", DurationValue: 1})
	assert.Error(t, err)
}

func TestSubscription_CalcNextResetTime(t *testing.T) {
	base := time.Date(2021, 3, 15, 10, 0, 0, 0, time.UTC) // a Monday

	assert.EqualValues(t, 0, calcNextResetTime(base, nil, 0))
	assert.EqualValues(t, 0, calcNextResetTime(base, &SubscriptionPlan{QuotaResetPeriod: "never"}, 0))

	// daily -> next midnight
	wantDaily := time.Date(2021, 3, 16, 0, 0, 0, 0, time.UTC).Unix()
	assert.Equal(t, wantDaily, calcNextResetTime(base, &SubscriptionPlan{QuotaResetPeriod: "daily"}, 0))
	// daily but endUnix before next -> 0
	assert.EqualValues(t, 0, calcNextResetTime(base, &SubscriptionPlan{QuotaResetPeriod: "daily"}, wantDaily-1))

	// weekly from Monday -> next Monday (+7d)
	wantWeekly := time.Date(2021, 3, 22, 0, 0, 0, 0, time.UTC).Unix()
	assert.Equal(t, wantWeekly, calcNextResetTime(base, &SubscriptionPlan{QuotaResetPeriod: "weekly"}, 0))
	// weekly from a Sunday -> next Monday
	sunday := time.Date(2021, 3, 14, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, time.Date(2021, 3, 15, 0, 0, 0, 0, time.UTC).Unix(),
		calcNextResetTime(sunday, &SubscriptionPlan{QuotaResetPeriod: "weekly"}, 0))

	// monthly -> first of next month
	wantMonthly := time.Date(2021, 4, 1, 0, 0, 0, 0, time.UTC).Unix()
	assert.Equal(t, wantMonthly, calcNextResetTime(base, &SubscriptionPlan{QuotaResetPeriod: "monthly"}, 0))
	// monthly but endUnix before next -> 0
	assert.EqualValues(t, 0, calcNextResetTime(base, &SubscriptionPlan{QuotaResetPeriod: "monthly"}, wantMonthly-10))

	// custom seconds <= 0 -> 0
	assert.EqualValues(t, 0, calcNextResetTime(base, &SubscriptionPlan{QuotaResetPeriod: "custom", QuotaResetCustomSeconds: 0}, 0))
	// custom valid
	assert.Equal(t, base.Add(100*time.Second).Unix(),
		calcNextResetTime(base, &SubscriptionPlan{QuotaResetPeriod: "custom", QuotaResetCustomSeconds: 100}, 0))
}

func TestSubscription_CalcBalanceQuota(t *testing.T) {
	// QuotaPerUnit is 500000 in this build
	assert.EqualValues(t, 500*1000.0, common.QuotaPerUnit)

	got, err := calcSubscriptionBalanceQuota(0)
	require.NoError(t, err)
	assert.Equal(t, 0, got)

	got, err = calcSubscriptionBalanceQuota(-5)
	require.NoError(t, err)
	assert.Equal(t, 0, got)

	got, err = calcSubscriptionBalanceQuota(1.0)
	require.NoError(t, err)
	assert.Equal(t, 500000, got)

	// ceil rounding: 0.0000021 * 500000 = 1.05 -> 2
	got, err = calcSubscriptionBalanceQuota(0.0000021)
	require.NoError(t, err)
	assert.Equal(t, 2, got)

	// exact: 0.000002 * 500000 = 1.0 -> 1
	got, err = calcSubscriptionBalanceQuota(0.000002)
	require.NoError(t, err)
	assert.Equal(t, 1, got)

	// QuotaPerUnit misconfigured -> error
	orig := common.QuotaPerUnit
	common.QuotaPerUnit = 0
	defer func() { common.QuotaPerUnit = orig }()
	_, err = calcSubscriptionBalanceQuota(1.0)
	assert.Error(t, err)
}

func TestSubscription_NormalizeDefaultsAndHooks(t *testing.T) {
	p := &SubscriptionPlan{}
	p.NormalizeDefaults()
	require.NotNil(t, p.AllowBalancePay)
	require.NotNil(t, p.AllowWalletOverflow)
	assert.True(t, *p.AllowBalancePay)
	assert.True(t, *p.AllowWalletOverflow)

	// explicit false is preserved
	p2 := &SubscriptionPlan{AllowBalancePay: common.GetPointer(false), AllowWalletOverflow: common.GetPointer(false)}
	p2.NormalizeDefaults()
	assert.False(t, *p2.AllowBalancePay)
	assert.False(t, *p2.AllowWalletOverflow)

	// hooks populate timestamps
	require.NoError(t, p.BeforeCreate(nil))
	assert.NotZero(t, p.CreatedAt)
	assert.NotZero(t, p.UpdatedAt)
	p.UpdatedAt = 0
	require.NoError(t, p.BeforeUpdate(nil))
	assert.NotZero(t, p.UpdatedAt)

	s := &UserSubscription{}
	require.NoError(t, s.BeforeCreate(nil))
	assert.NotZero(t, s.CreatedAt)
	s.UpdatedAt = 0
	require.NoError(t, s.BeforeUpdate(nil))
	assert.NotZero(t, s.UpdatedAt)

	r := &SubscriptionPreConsumeRecord{}
	require.NoError(t, r.BeforeCreate(nil))
	assert.NotZero(t, r.CreatedAt)
	r.UpdatedAt = 0
	require.NoError(t, r.BeforeUpdate(nil))
	assert.NotZero(t, r.UpdatedAt)
}

// ---------------------------------------------------------------------------
// Plan / order DB access
// ---------------------------------------------------------------------------

func TestSubscription_PlanGetByIdAndCache(t *testing.T) {
	_, err := GetSubscriptionPlanById(0)
	assert.Error(t, err)

	_, err = GetSubscriptionPlanById(900_000_555)
	assert.Error(t, err)

	p := subMakePlan(t, func(p *SubscriptionPlan) { p.TotalAmount = 1234 })
	got, err := GetSubscriptionPlanById(p.Id)
	require.NoError(t, err)
	assert.Equal(t, int64(1234), got.TotalAmount)
	assert.NotNil(t, got.AllowBalancePay) // NormalizeDefaults applied

	// cached read returns the same
	got2, err := GetSubscriptionPlanById(p.Id)
	require.NoError(t, err)
	assert.Equal(t, p.Id, got2.Id)

	// invalidation no-op for bad id
	InvalidateSubscriptionPlanCache(0)
	InvalidateSubscriptionPlanCache(p.Id)
}

func TestSubscription_OrderInsertGetUpdate(t *testing.T) {
	u := mkUser(t, nil)
	p := subMakePlan(t, nil)

	assert.Nil(t, GetSubscriptionOrderByTradeNo(""))

	tradeNo := uniq("trade")
	order := &SubscriptionOrder{
		UserId:          u.Id,
		PlanId:          p.Id,
		Money:           12.5,
		TradeNo:         tradeNo,
		PaymentProvider: "stripe",
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, order.Insert())
	deleteByID(t, &SubscriptionOrder{}, order.Id)
	assert.NotZero(t, order.CreateTime)

	got := GetSubscriptionOrderByTradeNo(tradeNo)
	require.NotNil(t, got)
	assert.Equal(t, u.Id, got.UserId)

	// not found -> nil
	assert.Nil(t, GetSubscriptionOrderByTradeNo("nonexistent-trade-xyz"))

	// update
	got.Status = common.TopUpStatusSuccess
	require.NoError(t, got.Update())
	assert.Equal(t, common.TopUpStatusSuccess, GetSubscriptionOrderByTradeNo(tradeNo).Status)
}

func TestSubscription_CountUserSubscriptionsByPlan(t *testing.T) {
	_, err := CountUserSubscriptionsByPlan(0, 1)
	assert.Error(t, err)
	_, err = CountUserSubscriptionsByPlan(1, 0)
	assert.Error(t, err)

	u := mkUser(t, nil)
	p := subMakePlan(t, nil)
	subMakeSub(t, u.Id, p.Id, nil)
	subMakeSub(t, u.Id, p.Id, nil)

	n, err := CountUserSubscriptionsByPlan(u.Id, p.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)
}

// ---------------------------------------------------------------------------
// CreateUserSubscriptionFromPlanTx
// ---------------------------------------------------------------------------

func TestSubscription_CreateFromPlanTx(t *testing.T) {
	// nil tx
	_, err := CreateUserSubscriptionFromPlanTx(nil, 1, &SubscriptionPlan{Id: 1}, "order")
	assert.Error(t, err)

	u := mkUser(t, nil)
	subCleanupSubs(t, u.Id)
	p := subMakePlan(t, func(p *SubscriptionPlan) { p.TotalAmount = 5000 })

	// invalid plan / user guards inside a tx
	require.Error(t, DB.Transaction(func(tx *gorm.DB) error {
		_, e := CreateUserSubscriptionFromPlanTx(tx, u.Id, nil, "order")
		return e
	}))
	require.Error(t, DB.Transaction(func(tx *gorm.DB) error {
		_, e := CreateUserSubscriptionFromPlanTx(tx, 0, p, "order")
		return e
	}))

	var sub *UserSubscription
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var e error
		sub, e = CreateUserSubscriptionFromPlanTx(tx, u.Id, p, "order")
		return e
	}))
	require.NotNil(t, sub)
	assert.Equal(t, int64(5000), sub.AmountTotal)
	assert.EqualValues(t, 0, sub.AmountUsed)
	assert.Equal(t, "active", sub.Status)
	assert.Greater(t, sub.EndTime, time.Now().Unix())
}

func TestSubscription_CreateFromPlanTx_MaxPurchaseAndUpgrade(t *testing.T) {
	u := mkUser(t, func(u *User) { u.Group = "default" })
	subCleanupSubs(t, u.Id)
	p := subMakePlan(t, func(p *SubscriptionPlan) {
		p.MaxPurchasePerUser = 1
		p.UpgradeGroup = "subvip"
	})

	var sub *UserSubscription
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var e error
		sub, e = CreateUserSubscriptionFromPlanTx(tx, u.Id, p, "order")
		return e
	}))
	require.NotNil(t, sub)
	assert.Equal(t, "subvip", sub.UpgradeGroup)
	assert.Equal(t, "default", sub.PrevUserGroup)

	// user group was elevated
	var reloaded User
	require.NoError(t, DB.Select("id", "group").First(&reloaded, u.Id).Error)
	assert.Equal(t, "subvip", reloaded.Group)

	// second purchase exceeds MaxPurchasePerUser
	err := DB.Transaction(func(tx *gorm.DB) error {
		_, e := CreateUserSubscriptionFromPlanTx(tx, u.Id, p, "order")
		return e
	})
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// CompleteSubscriptionOrder: idempotency, no double-grant, provider guard
// ---------------------------------------------------------------------------

func TestSubscription_CompleteOrder(t *testing.T) {
	u := mkUser(t, nil)
	subCleanupSubs(t, u.Id)
	p := subMakePlan(t, func(p *SubscriptionPlan) { p.TotalAmount = 1000 })

	tradeNo := uniq("ctrade")
	subCleanupTopUp(t, tradeNo)
	order := &SubscriptionOrder{
		UserId:          u.Id,
		PlanId:          p.Id,
		Money:           9.99,
		TradeNo:         tradeNo,
		PaymentProvider: "stripe",
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, order.Insert())
	deleteByID(t, &SubscriptionOrder{}, order.Id)

	// not found
	assert.ErrorIs(t, CompleteSubscriptionOrder("no-such-trade", "", "", ""), ErrSubscriptionOrderNotFound)

	// provider mismatch -> guarded, no subscription created
	assert.ErrorIs(t, CompleteSubscriptionOrder(tradeNo, "", "creem", ""), ErrPaymentMethodMismatch)
	assert.EqualValues(t, 0, subCountSubs(t, u.Id, p.Id))

	// success: creates exactly one subscription + a success TopUp, updates payment method
	require.NoError(t, CompleteSubscriptionOrder(tradeNo, "payload-json", "stripe", "alipay"))
	assert.EqualValues(t, 1, subCountSubs(t, u.Id, p.Id))

	reloadedOrder := GetSubscriptionOrderByTradeNo(tradeNo)
	require.NotNil(t, reloadedOrder)
	assert.Equal(t, common.TopUpStatusSuccess, reloadedOrder.Status)
	assert.Equal(t, "alipay", reloadedOrder.PaymentMethod)
	assert.NotZero(t, reloadedOrder.CompleteTime)

	topup := GetTopUpByTradeNo(tradeNo)
	require.NotNil(t, topup)
	assert.Equal(t, common.TopUpStatusSuccess, topup.Status)
	assert.InDelta(t, 9.99, topup.Money, 0.001)

	// idempotent: completing again must NOT grant a second subscription
	require.NoError(t, CompleteSubscriptionOrder(tradeNo, "payload-json", "stripe", "alipay"))
	assert.EqualValues(t, 1, subCountSubs(t, u.Id, p.Id))
}

func TestSubscription_CompleteOrder_ExistingTopUp(t *testing.T) {
	u := mkUser(t, nil)
	subCleanupSubs(t, u.Id)
	p := subMakePlan(t, func(p *SubscriptionPlan) { p.TotalAmount = 500 })

	tradeNo := uniq("mtrade")
	subCleanupTopUp(t, tradeNo)
	order := &SubscriptionOrder{
		UserId:          u.Id,
		PlanId:          p.Id,
		Money:           7.77,
		TradeNo:         tradeNo,
		PaymentMethod:   "wechat",
		PaymentProvider: "stripe",
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, order.Insert())
	deleteByID(t, &SubscriptionOrder{}, order.Id)

	// a TopUp with the same trade_no already exists (empty method) -> update branch
	existing := &TopUp{
		UserId:     u.Id,
		Money:      0,
		TradeNo:    tradeNo,
		CreateTime: 0,
		Status:     common.TopUpStatusPending,
	}
	require.NoError(t, DB.Create(existing).Error)
	deleteByID(t, &TopUp{}, existing.Id)

	require.NoError(t, CompleteSubscriptionOrder(tradeNo, "", "stripe", ""))

	topup := GetTopUpByTradeNo(tradeNo)
	require.NotNil(t, topup)
	assert.Equal(t, common.TopUpStatusSuccess, topup.Status)
	assert.InDelta(t, 7.77, topup.Money, 0.001)
	assert.Equal(t, "wechat", topup.PaymentMethod) // filled from the order
	assert.EqualValues(t, 1, subCountSubs(t, u.Id, p.Id))
}

func TestSubscription_CompleteOrder_StatusInvalid(t *testing.T) {
	u := mkUser(t, nil)
	subCleanupSubs(t, u.Id)
	p := subMakePlan(t, nil)

	tradeNo := uniq("etrade")
	order := &SubscriptionOrder{
		UserId:  u.Id,
		PlanId:  p.Id,
		TradeNo: tradeNo,
		Status:  common.TopUpStatusExpired, // neither pending nor success
	}
	require.NoError(t, order.Insert())
	deleteByID(t, &SubscriptionOrder{}, order.Id)

	assert.ErrorIs(t, CompleteSubscriptionOrder(tradeNo, "", "", ""), ErrSubscriptionOrderStatusInvalid)
}

func TestSubscription_ExpireOrder(t *testing.T) {
	u := mkUser(t, nil)
	p := subMakePlan(t, nil)

	// empty trade guard
	assert.Error(t, ExpireSubscriptionOrder("", ""))

	// not found
	assert.ErrorIs(t, ExpireSubscriptionOrder("missing-trade", ""), ErrSubscriptionOrderNotFound)

	tradeNo := uniq("xtrade")
	order := &SubscriptionOrder{
		UserId:          u.Id,
		PlanId:          p.Id,
		TradeNo:         tradeNo,
		PaymentProvider: "stripe",
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, order.Insert())
	deleteByID(t, &SubscriptionOrder{}, order.Id)

	// provider mismatch
	assert.ErrorIs(t, ExpireSubscriptionOrder(tradeNo, "creem"), ErrPaymentMethodMismatch)

	// pending -> expired
	require.NoError(t, ExpireSubscriptionOrder(tradeNo, "stripe"))
	assert.Equal(t, common.TopUpStatusExpired, GetSubscriptionOrderByTradeNo(tradeNo).Status)

	// already non-pending -> no-op nil, unchanged
	require.NoError(t, ExpireSubscriptionOrder(tradeNo, "stripe"))
	assert.Equal(t, common.TopUpStatusExpired, GetSubscriptionOrderByTradeNo(tradeNo).Status)
}

func TestSubscription_AdminBind(t *testing.T) {
	_, err := AdminBindSubscription(0, 1, "")
	assert.Error(t, err)

	u := mkUser(t, func(u *User) { u.Group = "default" })
	subCleanupSubs(t, u.Id)
	subCleanupUserLogs(t, u.Id)
	p := subMakePlan(t, func(p *SubscriptionPlan) { p.UpgradeGroup = "admvip" })

	msg, err := AdminBindSubscription(u.Id, p.Id, "note")
	require.NoError(t, err)
	assert.Contains(t, msg, "admvip")
	assert.EqualValues(t, 1, subCountSubs(t, u.Id, p.Id))

	// plan without upgrade group -> empty message
	u2 := mkUser(t, nil)
	subCleanupSubs(t, u2.Id)
	p2 := subMakePlan(t, nil)
	msg, err = AdminBindSubscription(u2.Id, p2.Id, "note")
	require.NoError(t, err)
	assert.Equal(t, "", msg)
}

// ---------------------------------------------------------------------------
// PurchaseSubscriptionWithBalance (billing integrity)
// ---------------------------------------------------------------------------

func TestSubscription_PurchaseWithBalance(t *testing.T) {
	_, _ = 0, 0
	// invalid args
	assert.Error(t, PurchaseSubscriptionWithBalance(0, 1))
	assert.Error(t, PurchaseSubscriptionWithBalance(1, 0))

	// happy path: price 1.0 -> 500000 quota deducted
	u := mkUser(t, func(u *User) { u.Quota = 1_000_000 })
	subCleanupSubs(t, u.Id)
	subCleanupUserLogs(t, u.Id)
	p := subMakePlan(t, func(p *SubscriptionPlan) { p.PriceAmount = 1.0; p.TotalAmount = 2000 })

	require.NoError(t, PurchaseSubscriptionWithBalance(u.Id, p.Id))
	assert.Equal(t, 500_000, reloadQuota(t, u.Id))
	assert.EqualValues(t, 1, subCountSubs(t, u.Id, p.Id))
	// a success balance order was recorded
	var orderCount int64
	require.NoError(t, DB.Model(&SubscriptionOrder{}).
		Where("user_id = ? AND plan_id = ? AND payment_provider = ?", u.Id, p.Id, PaymentProviderBalance).
		Count(&orderCount).Error)
	assert.EqualValues(t, 1, orderCount)
	t.Cleanup(func() { DB.Unscoped().Where("user_id = ?", u.Id).Delete(&SubscriptionOrder{}) })

	// free plan: no deduction, still creates a subscription
	uf := mkUser(t, func(u *User) { u.Quota = 100 })
	subCleanupSubs(t, uf.Id)
	subCleanupUserLogs(t, uf.Id)
	pf := subMakePlan(t, func(p *SubscriptionPlan) { p.PriceAmount = 0 })
	require.NoError(t, PurchaseSubscriptionWithBalance(uf.Id, pf.Id))
	assert.Equal(t, 100, reloadQuota(t, uf.Id))
	assert.EqualValues(t, 1, subCountSubs(t, uf.Id, pf.Id))
	t.Cleanup(func() { DB.Unscoped().Where("user_id = ?", uf.Id).Delete(&SubscriptionOrder{}) })

	// insufficient balance
	ui := mkUser(t, func(u *User) { u.Quota = 100 })
	pi := subMakePlan(t, func(p *SubscriptionPlan) { p.PriceAmount = 1.0 })
	assert.Error(t, PurchaseSubscriptionWithBalance(ui.Id, pi.Id))
	assert.Equal(t, 100, reloadQuota(t, ui.Id)) // unchanged

	// disabled plan (Enabled has a GORM default:true, so force it off explicitly)
	ud := mkUser(t, func(u *User) { u.Quota = 1_000_000 })
	pd := subMakePlan(t, func(p *SubscriptionPlan) { p.PriceAmount = 1.0 })
	require.NoError(t, DB.Model(&SubscriptionPlan{}).Where("id = ?", pd.Id).Update("enabled", false).Error)
	InvalidateSubscriptionPlanCache(pd.Id)
	assert.Error(t, PurchaseSubscriptionWithBalance(ud.Id, pd.Id))

	// balance pay disallowed
	ub := mkUser(t, func(u *User) { u.Quota = 1_000_000 })
	pb := subMakePlan(t, func(p *SubscriptionPlan) { p.AllowBalancePay = common.GetPointer(false); p.PriceAmount = 1.0 })
	assert.Error(t, PurchaseSubscriptionWithBalance(ub.Id, pb.Id))

	// negative price
	un := mkUser(t, func(u *User) { u.Quota = 1_000_000 })
	pn := subMakePlan(t, func(p *SubscriptionPlan) { p.PriceAmount = -1.0 })
	assert.Error(t, PurchaseSubscriptionWithBalance(un.Id, pn.Id))
}

// ---------------------------------------------------------------------------
// Active-subscription queries
// ---------------------------------------------------------------------------

func TestSubscription_ActiveQueries(t *testing.T) {
	// invalid userId guards
	_, err := GetAllActiveUserSubscriptions(0)
	assert.Error(t, err)
	_, err = HasActiveUserSubscription(0)
	assert.Error(t, err)
	_, err = GetAllUserSubscriptions(0)
	assert.Error(t, err)
	_, err = UserActiveSubscriptionsAllowWalletOverflow(0)
	assert.Error(t, err)

	u := mkUser(t, nil)
	p := subMakePlan(t, nil)
	now := time.Now().Unix()
	// active in the future
	subMakeSub(t, u.Id, p.Id, func(s *UserSubscription) { s.EndTime = now + 86400 })
	// status expired
	subMakeSub(t, u.Id, p.Id, func(s *UserSubscription) { s.Status = "expired"; s.EndTime = now + 86400 })
	// active flag but already ended
	subMakeSub(t, u.Id, p.Id, func(s *UserSubscription) { s.EndTime = now - 10 })

	active, err := GetAllActiveUserSubscriptions(u.Id)
	require.NoError(t, err)
	assert.Len(t, active, 1)

	has, err := HasActiveUserSubscription(u.Id)
	require.NoError(t, err)
	assert.True(t, has)

	all, err := GetAllUserSubscriptions(u.Id)
	require.NoError(t, err)
	assert.Len(t, all, 3)
}

func TestSubscription_WalletOverflow(t *testing.T) {
	p := subMakePlan(t, nil)
	now := time.Now().Unix()

	// user with a strict (no-overflow) active subscription -> blocked
	uStrict := mkUser(t, nil)
	subMakeSub(t, uStrict.Id, p.Id, func(s *UserSubscription) { s.AllowWalletOverflow = false; s.EndTime = now + 86400 })
	ok, err := UserActiveSubscriptionsAllowWalletOverflow(uStrict.Id)
	require.NoError(t, err)
	assert.False(t, ok)

	// user with only permissive subscriptions -> allowed
	uOk := mkUser(t, nil)
	subMakeSub(t, uOk.Id, p.Id, func(s *UserSubscription) { s.AllowWalletOverflow = true; s.EndTime = now + 86400 })
	ok, err = UserActiveSubscriptionsAllowWalletOverflow(uOk.Id)
	require.NoError(t, err)
	assert.True(t, ok)

	// user with no subscriptions -> allowed (nothing blocks)
	uNone := mkUser(t, nil)
	ok, err = UserActiveSubscriptionsAllowWalletOverflow(uNone.Id)
	require.NoError(t, err)
	assert.True(t, ok)
}

// ---------------------------------------------------------------------------
// Admin invalidate / delete
// ---------------------------------------------------------------------------

func TestSubscription_AdminInvalidate(t *testing.T) {
	_, err := AdminInvalidateUserSubscription(0)
	assert.Error(t, err)

	// simple cancel (no group transition)
	u := mkUser(t, nil)
	sub := subMakeSub(t, u.Id, 12345, nil)
	msg, err := AdminInvalidateUserSubscription(sub.Id)
	require.NoError(t, err)
	assert.Equal(t, "", msg)
	reloaded := subReloadSub(t, sub.Id)
	assert.Equal(t, "cancelled", reloaded.Status)
	assert.NotZero(t, reloaded.EndTime)

	// cancel with downgrade back to previous group
	uv := mkUser(t, func(u *User) { u.Group = "invip" })
	subCleanupSubs(t, uv.Id)
	subv := subMakeSub(t, uv.Id, 12345, func(s *UserSubscription) {
		s.UpgradeGroup = "invip"
		s.PrevUserGroup = "default"
	})
	msg, err = AdminInvalidateUserSubscription(subv.Id)
	require.NoError(t, err)
	assert.Contains(t, msg, "default")
	var reloadedUser User
	require.NoError(t, DB.Select("id", "group").First(&reloadedUser, uv.Id).Error)
	assert.Equal(t, "default", reloadedUser.Group)
}

func TestSubscription_AdminDelete(t *testing.T) {
	_, err := AdminDeleteUserSubscription(0)
	assert.Error(t, err)

	u := mkUser(t, nil)
	sub := subMakeSub(t, u.Id, 12345, nil)
	_, err = AdminDeleteUserSubscription(sub.Id)
	require.NoError(t, err)
	var count int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", sub.Id).Count(&count).Error)
	assert.EqualValues(t, 0, count)
}

// ---------------------------------------------------------------------------
// Admin reset (per-user and per-plan)
// ---------------------------------------------------------------------------

func TestSubscription_AdminResetByUser(t *testing.T) {
	_, err := AdminResetUserSubscriptionsByPlan(0, 1, false)
	assert.Error(t, err)

	u := mkUser(t, nil)
	subCleanupSubs(t, u.Id)
	p := subMakePlan(t, func(p *SubscriptionPlan) {
		p.QuotaResetPeriod = SubscriptionResetMonthly
		p.TotalAmount = 1000
	})
	now := time.Now().Unix()
	subMakeSub(t, u.Id, p.Id, func(s *UserSubscription) {
		s.AmountTotal = 1000
		s.AmountUsed = 500
		s.EndTime = now + 5*365*86400 // far future so reset time stays within range
	})

	res, err := AdminResetUserSubscriptionsByPlan(u.Id, p.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 1, res.ResetCount)
	assert.Equal(t, 1, res.UserCount)
	// amount_used reset to zero
	active, _ := GetAllActiveUserSubscriptions(u.Id)
	require.Len(t, active, 1)
	assert.EqualValues(t, 0, active[0].Subscription.AmountUsed)

	// no active subscription for another plan -> error
	pOther := subMakePlan(t, nil)
	_, err = AdminResetUserSubscriptionsByPlan(u.Id, pOther.Id, false)
	assert.Error(t, err)
}

func TestSubscription_AdminResetByPlan(t *testing.T) {
	_, err := AdminResetPlanSubscriptions(0, false)
	assert.Error(t, err)

	p := subMakePlan(t, func(p *SubscriptionPlan) {
		p.QuotaResetPeriod = SubscriptionResetMonthly
		p.TotalAmount = 1000
	})
	now := time.Now().Unix()
	u1 := mkUser(t, nil)
	u2 := mkUser(t, nil)
	subMakeSub(t, u1.Id, p.Id, func(s *UserSubscription) { s.AmountTotal = 1000; s.AmountUsed = 300; s.EndTime = now + 5*365*86400 })
	subMakeSub(t, u2.Id, p.Id, func(s *UserSubscription) { s.AmountTotal = 1000; s.AmountUsed = 700; s.EndTime = now + 5*365*86400 })

	res, err := AdminResetPlanSubscriptions(p.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 2, res.UserCount)
	assert.True(t, res.AdvanceResetTime)

	for _, uid := range []int{u1.Id, u2.Id} {
		active, _ := GetAllActiveUserSubscriptions(uid)
		require.Len(t, active, 1)
		assert.EqualValues(t, 0, active[0].Subscription.AmountUsed)
		assert.Greater(t, active[0].Subscription.NextResetTime, int64(0)) // advanced
	}
}

// ---------------------------------------------------------------------------
// ExpireDueSubscriptions
// ---------------------------------------------------------------------------

func TestSubscription_ExpireDue(t *testing.T) {
	p := subMakePlan(t, nil)
	now := time.Now().Unix()

	// user whose active subscription has already ended, with a group downgrade
	u := mkUser(t, func(u *User) { u.Group = "duevip" })
	subCleanupSubs(t, u.Id)
	sub := subMakeSub(t, u.Id, p.Id, func(s *UserSubscription) {
		s.EndTime = now - 10
		s.UpgradeGroup = "duevip"
		s.PrevUserGroup = "default"
	})

	n, err := ExpireDueSubscriptions(100000)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, 1)

	assert.Equal(t, "expired", subReloadSub(t, sub.Id).Status)
	var reloadedUser User
	require.NoError(t, DB.Select("id", "group").First(&reloadedUser, u.Id).Error)
	assert.Equal(t, "default", reloadedUser.Group) // downgraded on expiry
}

// ---------------------------------------------------------------------------
// PreConsume / Refund / Post-consume (billing integrity + idempotency)
// ---------------------------------------------------------------------------

func subCreateSubViaPlan(t *testing.T, userID int, plan *SubscriptionPlan) *UserSubscription {
	t.Helper()
	var sub *UserSubscription
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var e error
		sub, e = CreateUserSubscriptionFromPlanTx(tx, userID, plan, "order")
		return e
	}))
	deleteByID(t, &UserSubscription{}, sub.Id)
	return sub
}

func TestSubscription_PreConsumeIdempotentAndInsufficient(t *testing.T) {
	// arg guards
	_, err := PreConsumeUserSubscription("r", 0, "m", 0, 1)
	assert.Error(t, err)
	_, err = PreConsumeUserSubscription("  ", 1, "m", 0, 1)
	assert.Error(t, err)
	_, err = PreConsumeUserSubscription("r", 1, "m", 0, 0)
	assert.Error(t, err)

	u := mkUser(t, nil)
	subCleanupSubs(t, u.Id)
	p := subMakePlan(t, func(p *SubscriptionPlan) { p.TotalAmount = 1000 })
	sub := subCreateSubViaPlan(t, u.Id, p)

	reqID := uniq("req")
	subCleanupPreConsume(t, reqID)
	res, err := PreConsumeUserSubscription(reqID, u.Id, "gpt", 0, 300)
	require.NoError(t, err)
	assert.EqualValues(t, 300, res.PreConsumed)
	assert.EqualValues(t, 0, res.AmountUsedBefore)
	assert.EqualValues(t, 300, res.AmountUsedAfter)
	assert.EqualValues(t, 1000, res.AmountTotal)
	assert.Equal(t, sub.Id, res.UserSubscriptionId)
	assert.EqualValues(t, 300, subReloadSub(t, sub.Id).AmountUsed)

	// SECURITY / integrity: replaying the same requestId must NOT double-consume
	res2, err := PreConsumeUserSubscription(reqID, u.Id, "gpt", 0, 300)
	require.NoError(t, err)
	assert.EqualValues(t, 300, res2.PreConsumed)
	assert.EqualValues(t, 300, subReloadSub(t, sub.Id).AmountUsed) // still 300, not 600

	// insufficient: remaining is 700, ask for 800
	reqID2 := uniq("req")
	subCleanupPreConsume(t, reqID2)
	_, err = PreConsumeUserSubscription(reqID2, u.Id, "gpt", 0, 800)
	assert.Error(t, err)
	assert.EqualValues(t, 300, subReloadSub(t, sub.Id).AmountUsed) // unchanged
}

func TestSubscription_PreConsumeUnlimited(t *testing.T) {
	u := mkUser(t, nil)
	subCleanupSubs(t, u.Id)
	p := subMakePlan(t, func(p *SubscriptionPlan) { p.TotalAmount = 0 }) // unlimited
	sub := subCreateSubViaPlan(t, u.Id, p)

	reqID := uniq("req")
	subCleanupPreConsume(t, reqID)
	res, err := PreConsumeUserSubscription(reqID, u.Id, "gpt", 0, 9_000_000)
	require.NoError(t, err)
	assert.EqualValues(t, 9_000_000, res.PreConsumed)
	assert.Equal(t, sub.Id, res.UserSubscriptionId)
}

func TestSubscription_PreConsumePicksSubscriptionWithRoom(t *testing.T) {
	u := mkUser(t, nil)
	subCleanupSubs(t, u.Id)
	p := subMakePlan(t, func(p *SubscriptionPlan) { p.TotalAmount = 1000 })
	now := time.Now().Unix()
	// first (earlier end) is exhausted; second has room
	subMakeSub(t, u.Id, p.Id, func(s *UserSubscription) {
		s.AmountTotal = 100
		s.AmountUsed = 100
		s.EndTime = now + 1000
	})
	roomy := subMakeSub(t, u.Id, p.Id, func(s *UserSubscription) {
		s.AmountTotal = 1000
		s.AmountUsed = 0
		s.EndTime = now + 2000
	})

	reqID := uniq("req")
	subCleanupPreConsume(t, reqID)
	res, err := PreConsumeUserSubscription(reqID, u.Id, "gpt", 0, 300)
	require.NoError(t, err)
	assert.Equal(t, roomy.Id, res.UserSubscriptionId)
	assert.EqualValues(t, 300, subReloadSub(t, roomy.Id).AmountUsed)
}

func TestSubscription_RefundPreConsume(t *testing.T) {
	// guards
	assert.Error(t, RefundSubscriptionPreConsume(""))
	assert.Error(t, RefundSubscriptionPreConsume("no-such-request"))

	u := mkUser(t, nil)
	subCleanupSubs(t, u.Id)
	p := subMakePlan(t, func(p *SubscriptionPlan) { p.TotalAmount = 1000 })
	sub := subCreateSubViaPlan(t, u.Id, p)

	reqID := uniq("req")
	subCleanupPreConsume(t, reqID)
	_, err := PreConsumeUserSubscription(reqID, u.Id, "gpt", 0, 400)
	require.NoError(t, err)
	assert.EqualValues(t, 400, subReloadSub(t, sub.Id).AmountUsed)

	// refund returns quota and marks the record refunded
	require.NoError(t, RefundSubscriptionPreConsume(reqID))
	assert.EqualValues(t, 0, subReloadSub(t, sub.Id).AmountUsed)

	// idempotent: a second refund is a no-op
	require.NoError(t, RefundSubscriptionPreConsume(reqID))
	assert.EqualValues(t, 0, subReloadSub(t, sub.Id).AmountUsed)

	// a refunded request cannot be pre-consumed again under the same id
	_, err = PreConsumeUserSubscription(reqID, u.Id, "gpt", 0, 100)
	assert.Error(t, err)
}

func TestSubscription_ResetDue(t *testing.T) {
	u := mkUser(t, nil)
	subCleanupSubs(t, u.Id)
	p := subMakePlan(t, func(p *SubscriptionPlan) {
		p.QuotaResetPeriod = SubscriptionResetMonthly
		p.TotalAmount = 1000
	})
	now := time.Now().Unix()
	sub := subMakeSub(t, u.Id, p.Id, func(s *UserSubscription) {
		s.AmountTotal = 1000
		s.AmountUsed = 500
		s.LastResetTime = now - 90*86400 // well in the past
		s.NextResetTime = now - 10        // due
		s.EndTime = now + 5*365*86400
	})

	n, err := ResetDueSubscriptions(100000)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, 1)
	assert.EqualValues(t, 0, subReloadSub(t, sub.Id).AmountUsed)
}

func TestSubscription_CleanupPreConsumeRecords(t *testing.T) {
	u := mkUser(t, nil)
	rec := &SubscriptionPreConsumeRecord{
		RequestId:          uniq("req"),
		UserId:             u.Id,
		UserSubscriptionId: 999,
		PreConsumed:        10,
		Status:            "consumed",
	}
	require.NoError(t, DB.Create(rec).Error)
	deleteByID(t, &SubscriptionPreConsumeRecord{}, rec.Id)

	// force an old updated_at (bypass hooks with UpdateColumn)
	old := time.Now().Unix() - 10000
	require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).Where("id = ?", rec.Id).
		UpdateColumn("updated_at", old).Error)

	deleted, err := CleanupSubscriptionPreConsumeRecords(3600)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, deleted, int64(1))

	var count int64
	require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).Where("id = ?", rec.Id).Count(&count).Error)
	assert.EqualValues(t, 0, count)
}

func TestSubscription_PlanInfoByUserSubscriptionId(t *testing.T) {
	_, err := GetSubscriptionPlanInfoByUserSubscriptionId(0)
	assert.Error(t, err)

	u := mkUser(t, nil)
	p := subMakePlan(t, func(p *SubscriptionPlan) { p.Title = "Gold Plan " + uniq("t") })
	sub := subMakeSub(t, u.Id, p.Id, nil)

	info, err := GetSubscriptionPlanInfoByUserSubscriptionId(sub.Id)
	require.NoError(t, err)
	assert.Equal(t, p.Id, info.PlanId)
	assert.Equal(t, p.Title, info.PlanTitle)

	// second call hits the info cache and returns the same
	info2, err := GetSubscriptionPlanInfoByUserSubscriptionId(sub.Id)
	require.NoError(t, err)
	assert.Equal(t, info.PlanTitle, info2.PlanTitle)
}

func TestSubscription_PostConsumeDelta(t *testing.T) {
	assert.Error(t, PostConsumeUserSubscriptionDelta(0, 100))

	u := mkUser(t, nil)
	p := subMakePlan(t, nil)
	sub := subMakeSub(t, u.Id, p.Id, func(s *UserSubscription) { s.AmountTotal = 1000; s.AmountUsed = 200 })

	// delta 0 -> no-op nil
	require.NoError(t, PostConsumeUserSubscriptionDelta(sub.Id, 0))
	assert.EqualValues(t, 200, subReloadSub(t, sub.Id).AmountUsed)

	// positive delta
	require.NoError(t, PostConsumeUserSubscriptionDelta(sub.Id, 300))
	assert.EqualValues(t, 500, subReloadSub(t, sub.Id).AmountUsed)

	// negative below zero -> floored at 0
	require.NoError(t, PostConsumeUserSubscriptionDelta(sub.Id, -100000))
	assert.EqualValues(t, 0, subReloadSub(t, sub.Id).AmountUsed)

	// exceeding total -> error, unchanged
	err := PostConsumeUserSubscriptionDelta(sub.Id, 2000)
	assert.Error(t, err)
	assert.EqualValues(t, 0, subReloadSub(t, sub.Id).AmountUsed)
}

func subCleanupUserLogs(t *testing.T, userID int) {
	t.Cleanup(func() {
		if LOG_DB != nil {
			LOG_DB.Unscoped().Where("user_id = ?", userID).Delete(&Log{})
		}
	})
}

// sanity: subscription sentinel errors are distinct
func TestSubscription_SentinelErrors(t *testing.T) {
	assert.NotErrorIs(t, ErrSubscriptionOrderNotFound, ErrSubscriptionOrderStatusInvalid)
	assert.Error(t, fmt.Errorf("wrap: %w", ErrSubscriptionOrderNotFound))
}
