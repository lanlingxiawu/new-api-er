package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Stripe webhook enablement is DELIBERATELY decoupled from compliance and the
// display toggle: an in-flight paid Checkout Session must still be able to
// settle. It depends only on the webhook secret.
func TestStripeWebhookEnabled_OnlyNeedsWebhookSecret(t *testing.T) {
	prev := setting.StripeWebhookSecret
	prevConfirmed := operation_setting.GetPaymentSetting().ComplianceConfirmed
	t.Cleanup(func() {
		setting.StripeWebhookSecret = prev
		operation_setting.GetPaymentSetting().ComplianceConfirmed = prevConfirmed
	})

	// compliance OFF, but webhook secret present -> still enabled
	operation_setting.GetPaymentSetting().ComplianceConfirmed = false
	setting.StripeWebhookSecret = "whsec_123"
	assert.True(t, isStripeWebhookEnabled())

	setting.StripeWebhookSecret = "   "
	assert.False(t, isStripeWebhookEnabled())
}

// Stripe top-up requires compliance + display toggle + api secret + webhook secret.
func TestStripeTopUpEnabled_ConditionMatrix(t *testing.T) {
	prevEnabled := setting.StripeEnabled
	prevApi := setting.StripeApiSecret
	prevWebhook := setting.StripeWebhookSecret
	t.Cleanup(func() {
		setting.StripeEnabled = prevEnabled
		setting.StripeApiSecret = prevApi
		setting.StripeWebhookSecret = prevWebhook
	})
	withPaymentComplianceConfirmed(t)

	setting.StripeEnabled = true
	setting.StripeApiSecret = "sk_test"
	setting.StripeWebhookSecret = "whsec"
	assert.True(t, isStripeTopUpEnabled())

	// display toggle off -> disabled
	setting.StripeEnabled = false
	assert.False(t, isStripeTopUpEnabled())
	setting.StripeEnabled = true

	// missing api secret -> disabled
	setting.StripeApiSecret = ""
	assert.False(t, isStripeTopUpEnabled())
}

// Stripe top-up requires compliance: with compliance off it is disabled even
// when everything else is configured.
func TestStripeTopUpEnabled_RequiresCompliance(t *testing.T) {
	prevEnabled := setting.StripeEnabled
	prevApi := setting.StripeApiSecret
	prevWebhook := setting.StripeWebhookSecret
	prevConfirmed := operation_setting.GetPaymentSetting().ComplianceConfirmed
	t.Cleanup(func() {
		setting.StripeEnabled = prevEnabled
		setting.StripeApiSecret = prevApi
		setting.StripeWebhookSecret = prevWebhook
		operation_setting.GetPaymentSetting().ComplianceConfirmed = prevConfirmed
	})

	operation_setting.GetPaymentSetting().ComplianceConfirmed = false
	setting.StripeEnabled = true
	setting.StripeApiSecret = "sk_test"
	setting.StripeWebhookSecret = "whsec"
	assert.False(t, isStripeTopUpEnabled())
}

// Creem webhook requires compliance + api key + non-empty products + webhook secret.
func TestCreemWebhookEnabled_ConditionMatrix(t *testing.T) {
	prevApi := setting.CreemApiKey
	prevProducts := setting.CreemProducts
	prevWebhook := setting.CreemWebhookSecret
	t.Cleanup(func() {
		setting.CreemApiKey = prevApi
		setting.CreemProducts = prevProducts
		setting.CreemWebhookSecret = prevWebhook
	})
	withPaymentComplianceConfirmed(t)

	setting.CreemApiKey = "creem_key"
	setting.CreemProducts = `[{"id":"p1"}]`
	setting.CreemWebhookSecret = "creem_whsec"
	assert.True(t, isCreemWebhookEnabled())

	// empty products list "[]" -> top-up disabled -> webhook disabled
	setting.CreemProducts = "[]"
	assert.False(t, isCreemWebhookEnabled())
	setting.CreemProducts = `[{"id":"p1"}]`

	// missing webhook secret -> disabled
	setting.CreemWebhookSecret = ""
	assert.False(t, isCreemWebhookEnabled())
}

// Waffo Pancake webhook == top-up: compliance + merchant + private key + product id.
func TestWaffoPancakeWebhookEnabled_ConditionMatrix(t *testing.T) {
	prevM := setting.WaffoPancakeMerchantID
	prevK := setting.WaffoPancakePrivateKey
	prevP := setting.WaffoPancakeProductID
	t.Cleanup(func() {
		setting.WaffoPancakeMerchantID = prevM
		setting.WaffoPancakePrivateKey = prevK
		setting.WaffoPancakeProductID = prevP
	})
	withPaymentComplianceConfirmed(t)

	setting.WaffoPancakeMerchantID = "m"
	setting.WaffoPancakePrivateKey = "k"
	setting.WaffoPancakeProductID = "p"
	assert.True(t, isWaffoPancakeWebhookEnabled())

	setting.WaffoPancakeProductID = ""
	assert.False(t, isWaffoPancakeWebhookEnabled())
}

// Epay webhook requires compliance + epay creds + at least one pay method.
func TestEpayWebhookEnabled_ConditionMatrix(t *testing.T) {
	prevAddr := operation_setting.PayAddress
	prevId := operation_setting.EpayId
	prevKey := operation_setting.EpayKey
	prevMethods := operation_setting.PayMethods
	t.Cleanup(func() {
		operation_setting.PayAddress = prevAddr
		operation_setting.EpayId = prevId
		operation_setting.EpayKey = prevKey
		operation_setting.PayMethods = prevMethods
	})
	withPaymentComplianceConfirmed(t)

	operation_setting.PayAddress = "https://pay.example.com"
	operation_setting.EpayId = "1000"
	operation_setting.EpayKey = "secret"
	operation_setting.PayMethods = []map[string]string{{"name": "alipay"}}
	assert.True(t, isEpayWebhookEnabled())

	// no pay methods -> disabled
	operation_setting.PayMethods = nil
	assert.False(t, isEpayWebhookEnabled())
	operation_setting.PayMethods = []map[string]string{{"name": "alipay"}}

	// missing epay key -> webhook not configured -> disabled
	operation_setting.EpayKey = ""
	assert.False(t, isEpayWebhookEnabled())
}

func TestEpayCallbacksVerifyBeforeSettlement(t *testing.T) {
	withPaymentComplianceConfirmed(t)
	originalPayAddress := operation_setting.PayAddress
	originalEpayID := operation_setting.EpayId
	originalEpayKey := operation_setting.EpayKey
	originalPayMethods := operation_setting.PayMethods
	originalServerAddress := system_setting.ServerAddress
	originalDB := model.DB
	t.Cleanup(func() {
		operation_setting.PayAddress = originalPayAddress
		operation_setting.EpayId = originalEpayID
		operation_setting.EpayKey = originalEpayKey
		operation_setting.PayMethods = originalPayMethods
		system_setting.ServerAddress = originalServerAddress
		model.DB = originalDB
	})
	operation_setting.PayAddress = "https://pay.example.com"
	operation_setting.EpayId = "epay_id"
	operation_setting.EpayKey = "test-key"
	operation_setting.PayMethods = []map[string]string{{"type": "alipay"}}
	system_setting.ServerAddress = "https://dashboard.example.com"
	// Rejected callbacks and valid non-success events must never reach settlement.
	// A nil database makes an accidental attempt to settle fail the test.
	model.DB = nil
	require.True(t, isEpayWebhookEnabled())

	// Both ambiguous maps below had this same valid signing string in v0.0.4.
	// This must fail because of parameter ambiguity, not a mismatched signature.
	ambiguousSign := epay.MD5String("money=1.00&name=item&out_trade_no=other&out_trade_no=order&pid=epay_id&trade_status=TRADE_SUCCESS&type=alipay", "test-key")
	pendingSign := epay.MD5String("money=1.00&name=item%26other&out_trade_no=order&pid=epay_id&trade_status=WAIT_BUYER_PAY&type=alipay", "test-key")
	cases := []struct {
		name         string
		productName  string
		tradeNo      string
		status       string
		sign         string
		validPending bool
	}{
		{"separator in product name", "item&out_trade_no=other", "order", epay.StatusTradeSuccess, ambiguousSign, false},
		{"separator in order number", "item", "other&out_trade_no=order", epay.StatusTradeSuccess, ambiguousSign, false},
		{"invalid signature", "item", "order", epay.StatusTradeSuccess, "invalid", false},
		{"missing signature", "item", "order", epay.StatusTradeSuccess, "", false},
		{"valid pending with literal percent escape", "item%26other", "order", "WAIT_BUYER_PAY", pendingSign, true},
	}
	endpoints := []struct {
		name             string
		path             string
		handler          gin.HandlerFunc
		status           int
		rejectedBody     string
		pendingBody      string
		rejectedLocation string
		pendingLocation  string
	}{
		{
			name: "topup notify", path: "/api/user/epay/notify", handler: EpayNotify,
			status: http.StatusOK, rejectedBody: "fail", pendingBody: "success",
		},
		{
			name: "subscription notify", path: "/api/subscription/epay/notify", handler: SubscriptionEpayNotify,
			status: http.StatusOK, rejectedBody: "fail", pendingBody: "fail",
		},
		{
			name: "subscription return", path: "/api/subscription/epay/return", handler: SubscriptionEpayReturn,
			status:           http.StatusFound,
			rejectedLocation: "https://dashboard.example.com/wallet?pay=fail",
			pendingLocation:  "https://dashboard.example.com/wallet?pay=pending",
		},
	}
	router := gin.New()
	for _, endpoint := range endpoints {
		router.Any(endpoint.path, endpoint.handler)
	}
	for _, endpoint := range endpoints {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			for _, tc := range cases {
				t.Run(endpoint.name+"/"+method+"/"+tc.name, func(t *testing.T) {
					params := url.Values{
						"pid": {"epay_id"}, "type": {"alipay"}, "money": {"1.00"},
						"name": {tc.productName}, "out_trade_no": {tc.tradeNo},
						"trade_status": {tc.status}, "sign_type": {"MD5"},
					}
					if tc.sign != "" {
						params.Set("sign", tc.sign)
					}
					request := httptest.NewRequest(method, endpoint.path+"?"+params.Encode(), nil)
					if method == http.MethodPost {
						request = httptest.NewRequest(method, endpoint.path, strings.NewReader(params.Encode()))
						request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					}
					recorder := httptest.NewRecorder()
					require.NotPanics(t, func() { router.ServeHTTP(recorder, request) })
					assert.Equal(t, endpoint.status, recorder.Code)
					wantBody, wantLocation := endpoint.rejectedBody, endpoint.rejectedLocation
					if tc.validPending {
						wantBody, wantLocation = endpoint.pendingBody, endpoint.pendingLocation
					}
					if wantBody != "" {
						assert.Equal(t, wantBody, recorder.Body.String())
					}
					assert.Equal(t, wantLocation, recorder.Header().Get("Location"))
				})
			}
		}
	}
}
