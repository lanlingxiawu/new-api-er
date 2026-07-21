package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
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
