package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// waffo_pancake.go — Waffo Pancake payment integration.
// Pure/validation helpers + DB-backed trade-no resolution. No SDK network.
// ===========================================================================

func TestWaffo_NormalizedEventType(t *testing.T) {
	var nilEvt *WaffoPancakeWebhookEvent
	assert.Equal(t, "", nilEvt.NormalizedEventType())
	assert.Equal(t, "order.completed", (&WaffoPancakeWebhookEvent{EventType: "order.completed"}).NormalizedEventType())
}

func TestWaffo_OptionalString(t *testing.T) {
	assert.Nil(t, optionalString(""))
	assert.Nil(t, optionalString("   "))
	got := optionalString("hi")
	require.NotNil(t, got)
	assert.Equal(t, "hi", *got)
}

func TestWaffo_BuyerIdentityFromUserID(t *testing.T) {
	assert.Equal(t, "new-api-user-42", WaffoPancakeBuyerIdentityFromUserID(42))
	assert.Equal(t, "new-api-user-0", WaffoPancakeBuyerIdentityFromUserID(0))
}

func TestWaffo_NewClientFromCreds_Validation(t *testing.T) {
	_, err := newWaffoPancakeClientFromCreds("", "pk")
	assert.Error(t, err)
	_, err = newWaffoPancakeClientFromCreds("mid", "  ")
	assert.Error(t, err)
}

func TestWaffo_CreateCheckoutSession_ParamValidation(t *testing.T) {
	ctx := context.Background()
	_, err := CreateWaffoPancakeCheckoutSession(ctx, nil)
	assert.Error(t, err)
	_, err = CreateWaffoPancakeCheckoutSession(ctx, &WaffoPancakeCreateSessionParams{})
	assert.Error(t, err) // missing buyer identity
	_, err = CreateWaffoPancakeCheckoutSession(ctx, &WaffoPancakeCreateSessionParams{BuyerIdentity: "b"})
	assert.Error(t, err) // missing order merchant external id
}

func TestWaffo_CreateProduct_ParamValidation(t *testing.T) {
	ctx := context.Background()
	// Missing store id.
	_, err := CreateWaffoPancakeProductForPlan(ctx, "m", "k", "", "name", "1.00", "")
	assert.Error(t, err)
	// Missing name.
	_, err = CreateWaffoPancakeProductForPlan(ctx, "m", "k", "store", "", "1.00", "")
	assert.Error(t, err)
	// Missing amount.
	_, err = CreateWaffoPancakeProductForPlan(ctx, "m", "k", "store", "name", "", "")
	assert.Error(t, err)

	// Primary product: missing store id.
	_, err = CreateWaffoPancakePrimaryProduct(ctx, "m", "k", "", "")
	assert.Error(t, err)
	// Primary store: invalid creds.
	_, err = CreateWaffoPancakePrimaryStore(ctx, "", "")
	assert.Error(t, err)
}

func TestWaffo_SaveConfig_Validation(t *testing.T) {
	// Missing required ids => error before any DB write.
	err := SaveWaffoPancakeConfig(context.Background(), "", "pk", "", "", "")
	assert.Error(t, err)
	err = SaveWaffoPancakeConfig(context.Background(), "mid", "pk", "", "store", "")
	assert.Error(t, err) // missing product id
}

// --- ResolveWaffoPancakeTradeNo (DB) ---------------------------------------

func waffoEvent(tradeNo, identity string) *WaffoPancakeWebhookEvent {
	return &WaffoPancakeWebhookEvent{
		EventType: "order.completed",
		Data: WaffoPancakeWebhookData{
			OrderMerchantExternalID:       tradeNo,
			MerchantProvidedBuyerIdentity: identity,
		},
	}
}

func TestWaffo_ResolveTradeNo(t *testing.T) {
	truncate(t)
	const uid = 3101
	tradeNo := "wp-trade-3101"
	topUp := &model.TopUp{
		UserId:          uid,
		TradeNo:         tradeNo,
		Amount:          10,
		Money:           1.0,
		PaymentProvider: model.PaymentProviderWaffoPancake,
		Status:          "pending",
		CreateTime:      1,
	}
	require.NoError(t, model.DB.Create(topUp).Error)
	t.Cleanup(func() { model.DB.Exec("DELETE FROM top_ups WHERE trade_no = ?", tradeNo) })

	// nil event.
	_, err := ResolveWaffoPancakeTradeNo(nil)
	assert.Error(t, err)

	// missing external id.
	_, err = ResolveWaffoPancakeTradeNo(waffoEvent("", "x"))
	assert.Error(t, err)

	// not found.
	_, err = ResolveWaffoPancakeTradeNo(waffoEvent("does-not-exist", "x"))
	assert.Error(t, err)

	// identity mismatch.
	_, err = ResolveWaffoPancakeTradeNo(waffoEvent(tradeNo, "new-api-user-999"))
	assert.Error(t, err)

	// success.
	got, err := ResolveWaffoPancakeTradeNo(waffoEvent(tradeNo, WaffoPancakeBuyerIdentityFromUserID(uid)))
	require.NoError(t, err)
	assert.Equal(t, tradeNo, got)
}

func TestWaffo_ResolveSubscriptionTradeNo(t *testing.T) {
	const uid = 3102
	tradeNo := "wp-sub-3102"
	order := &model.SubscriptionOrder{
		UserId:          uid,
		TradeNo:         tradeNo,
		PaymentProvider: model.PaymentProviderWaffoPancake,
		Status:          "pending",
	}
	require.NoError(t, model.DB.Create(order).Error)
	t.Cleanup(func() { model.DB.Exec("DELETE FROM subscription_orders WHERE trade_no = ?", tradeNo) })

	_, err := ResolveWaffoPancakeSubscriptionTradeNo(nil)
	assert.Error(t, err)
	_, err = ResolveWaffoPancakeSubscriptionTradeNo(waffoEvent("", "x"))
	assert.Error(t, err)
	_, err = ResolveWaffoPancakeSubscriptionTradeNo(waffoEvent("missing", "x"))
	assert.Error(t, err)
	_, err = ResolveWaffoPancakeSubscriptionTradeNo(waffoEvent(tradeNo, "new-api-user-1"))
	assert.Error(t, err)

	got, err := ResolveWaffoPancakeSubscriptionTradeNo(waffoEvent(tradeNo, WaffoPancakeBuyerIdentityFromUserID(uid)))
	require.NoError(t, err)
	assert.Equal(t, tradeNo, got)
}

// --- VerifyConfiguredWaffoPancakeWebhook (bad signature) --------------------

func TestWaffo_VerifyWebhook_BadPayload(t *testing.T) {
	_, err := VerifyConfiguredWaffoPancakeWebhook("{}", "bad-signature")
	assert.Error(t, err)
}
