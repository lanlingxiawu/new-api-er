package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
)

// withUSDDisplay pins the quota display type to USD (the shipped default) so
// money math is deterministic regardless of test ordering.
func withUSDDisplay(t *testing.T) {
	t.Helper()
	gs := operation_setting.GetGeneralSetting()
	prev := gs.QuotaDisplayType
	gs.QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	t.Cleanup(func() { gs.QuotaDisplayType = prev })
}

// formatWaffoPancakeAmount always renders exactly two decimals.
func TestFormatWaffoPancakeAmount(t *testing.T) {
	assert.Equal(t, "0.00", formatWaffoPancakeAmount(0))
	assert.Equal(t, "12.50", formatWaffoPancakeAmount(12.5))
	assert.Equal(t, "3.14", formatWaffoPancakeAmount(3.14159))
	assert.Equal(t, "100.00", formatWaffoPancakeAmount(100))
}

// getWaffoPancakePayMoney: under USD display, unit price 1.0 and default group
// ratio 1.0, the pay money equals the amount.
func TestGetWaffoPancakePayMoney_USD(t *testing.T) {
	withUSDDisplay(t)
	prevUnit := setting.WaffoPancakeUnitPrice
	setting.WaffoPancakeUnitPrice = 1.0
	t.Cleanup(func() { setting.WaffoPancakeUnitPrice = prevUnit })

	assert.Equal(t, 100.0, getWaffoPancakePayMoney(100, "default"))
	assert.Equal(t, 1.0, getWaffoPancakePayMoney(1, "default"))
}

// normalizeWaffoPancakeTopUpAmount: under USD display the amount is unchanged.
func TestNormalizeWaffoPancakeTopUpAmount_USD(t *testing.T) {
	withUSDDisplay(t)
	assert.EqualValues(t, 100, normalizeWaffoPancakeTopUpAmount(100))
	assert.EqualValues(t, 1, normalizeWaffoPancakeTopUpAmount(1))
}

// resolveWaffoPancakeAdminCreds: body creds win; blank body falls back to the
// persisted settings.
func TestResolveWaffoPancakeAdminCreds(t *testing.T) {
	prevM, prevK := setting.WaffoPancakeMerchantID, setting.WaffoPancakePrivateKey
	setting.WaffoPancakeMerchantID = "persisted-merchant"
	setting.WaffoPancakePrivateKey = "persisted-key"
	t.Cleanup(func() {
		setting.WaffoPancakeMerchantID = prevM
		setting.WaffoPancakePrivateKey = prevK
	})

	// body provided -> body wins (trimmed)
	m, k := resolveWaffoPancakeAdminCreds("  body-m  ", " body-k ")
	assert.Equal(t, "body-m", m)
	assert.Equal(t, "body-k", k)

	// both blank -> persisted fallback
	m, k = resolveWaffoPancakeAdminCreds("", "   ")
	assert.Equal(t, "persisted-merchant", m)
	assert.Equal(t, "persisted-key", k)

	// partial (only merchant) -> returns partial, does NOT fall back
	m, k = resolveWaffoPancakeAdminCreds("only-m", "")
	assert.Equal(t, "only-m", m)
	assert.Equal(t, "", k)
}

// getWaffoPancakeBuyerEmail: nil user, blank email, and populated email.
func TestGetWaffoPancakeBuyerEmail(t *testing.T) {
	assert.Equal(t, "", getWaffoPancakeBuyerEmail(nil))
	assert.Equal(t, "", getWaffoPancakeBuyerEmail(&model.User{Email: "   "}))
	assert.Equal(t, "a@b.com", getWaffoPancakeBuyerEmail(&model.User{Email: "a@b.com"}))
}
