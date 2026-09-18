package controller

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// topup_infini.go — checkout currency guard (USD only), pre-payment credit
// validation, amount ceiling and exchange-rate resolution.
//
// Billing-critical: the credited quota snapshot is written at order creation
// time, so every guard here runs before model.TopUp.Insert. No test may reach
// infiniPost (Rule 15.4: never call a real payment gateway).
// ---------------------------------------------------------------------------

// infiniTestSettings pins every Infini/pricing input the handlers read and
// restores it afterwards, so cases stay deterministic under the full suite.
func infiniTestSettings(t *testing.T, currenciesJSON string) {
	t.Helper()
	setOptionMapForTest(t)

	prevEnabled := setting.InfiniEnabled
	prevKey, prevSecret, prevHook := setting.InfiniApiKey, setting.InfiniApiSecret, setting.InfiniWebhookSecret
	prevRealtime, prevRate := setting.InfiniUseRealtimeRate, setting.InfiniExchangeRate
	prevCurrency, prevUnitPrice, prevMinTopUp := setting.InfiniCurrency, setting.InfiniUnitPrice, setting.InfiniMinTopUp
	prevPrice := operation_setting.Price
	prevQuotaPerUnit := common.QuotaPerUnit
	prevDisplay := operation_setting.GetGeneralSetting().QuotaDisplayType

	setting.InfiniEnabled = true
	setting.InfiniApiKey = "test-key"
	setting.InfiniApiSecret = "test-secret"
	setting.InfiniWebhookSecret = "test-webhook"
	// Manual rate only: the realtime resolver would reach out to the network.
	setting.InfiniUseRealtimeRate = false
	setting.InfiniExchangeRate = 7.2
	setting.InfiniCurrency = "USD"
	setting.InfiniUnitPrice = 1.0
	setting.InfiniMinTopUp = 1
	operation_setting.Price = 7.2
	common.QuotaPerUnit = 500000
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	setOption("InfiniCurrencies", currenciesJSON)

	t.Cleanup(func() {
		setting.InfiniEnabled = prevEnabled
		setting.InfiniApiKey, setting.InfiniApiSecret, setting.InfiniWebhookSecret = prevKey, prevSecret, prevHook
		setting.InfiniUseRealtimeRate, setting.InfiniExchangeRate = prevRealtime, prevRate
		setting.InfiniCurrency, setting.InfiniUnitPrice, setting.InfiniMinTopUp = prevCurrency, prevUnitPrice, prevMinTopUp
		operation_setting.Price = prevPrice
		common.QuotaPerUnit = prevQuotaPerUnit
		operation_setting.GetGeneralSetting().QuotaDisplayType = prevDisplay
	})
}

// setOptionMapForTest guarantees a writable OptionMap and restores the entry
// this file mutates.
func setOptionMapForTest(t *testing.T) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	prev, had := common.OptionMap["InfiniCurrencies"]
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if had {
			common.OptionMap["InfiniCurrencies"] = prev
		} else {
			delete(common.OptionMap, "InfiniCurrencies")
		}
		common.OptionMapRWMutex.Unlock()
	})
}

func setOption(key, value string) {
	common.OptionMapRWMutex.Lock()
	common.OptionMap[key] = value
	common.OptionMapRWMutex.Unlock()
}

// payResp is the legacy payment envelope: {"message":"error|success","data":...}.
type infiniPayResp struct {
	Message      string  `json:"message"`
	Data         any     `json:"data"`
	ExchangeRate float64 `json:"exchange_rate"`
}

func decodeInfiniResp(t *testing.T, body []byte) infiniPayResp {
	t.Helper()
	var out infiniPayResp
	require.NoError(t, common.Unmarshal(body, &out), "body: %s", string(body))
	return out
}

// A non-USD configured currency must be refused before an order exists: the
// credited quota is converted with a USD->CNY rate, so charging JPY at a JPY
// unit price would over-credit by roughly the JPY/USD factor.
func TestRequestInfiniPayRejectsNonUsdCurrency(t *testing.T) {
	requireDB(t)
	infiniTestSettings(t, `[{"currency":"JPY","unit_price":150,"min_topup":1}]`)
	u := mkUser(t, nil)

	ctx, rec := newCtx(t, http.MethodPost, "/api/user/infini/pay", map[string]any{"amount": 1})
	asUser(ctx, u.Id)
	RequestInfiniPay(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeInfiniResp(t, rec.Body.Bytes())
	assert.Equal(t, "error", resp.Message)
	assert.Contains(t, fmt.Sprint(resp.Data), "JPY")

	var count int64
	require.NoError(t, model.DB.Model(&model.TopUp{}).Where("user_id = ?", u.Id).Count(&count).Error)
	assert.Zero(t, count, "no order may be created for an unsupported currency")
}

// A currency the user asks for explicitly gets the specific reason, even when
// the admin never configured it.
func TestRequestInfiniPayRejectsExplicitNonUsdCurrency(t *testing.T) {
	requireDB(t)
	infiniTestSettings(t, `[{"currency":"USD","unit_price":1,"min_topup":1}]`)
	u := mkUser(t, nil)

	ctx, rec := newCtx(t, http.MethodPost, "/api/user/infini/pay", map[string]any{"amount": 1, "currency": "jpy"})
	asUser(ctx, u.Id)
	RequestInfiniPay(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeInfiniResp(t, rec.Body.Bytes())
	assert.Equal(t, "error", resp.Message)
	assert.Contains(t, fmt.Sprint(resp.Data), "JPY")

	var count int64
	require.NoError(t, model.DB.Model(&model.TopUp{}).Where("user_id = ?", u.Id).Count(&count).Error)
	assert.Zero(t, count)
}

func TestRequestInfiniAmountRejectsNonUsdCurrency(t *testing.T) {
	requireDB(t)
	infiniTestSettings(t, `[{"currency":"USD","unit_price":1,"min_topup":1},{"currency":"EUR","unit_price":0.92,"min_topup":1}]`)
	u := mkUser(t, nil)

	ctx, rec := newCtx(t, http.MethodPost, "/api/user/infini/amount", map[string]any{"amount": 1, "currency": "EUR"})
	asUser(ctx, u.Id)
	RequestInfiniAmount(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeInfiniResp(t, rec.Body.Bytes())
	assert.Equal(t, "error", resp.Message)
	assert.Contains(t, fmt.Sprint(resp.Data), "EUR")
}

// The USD quote must report the pay amount and the rate actually used for the
// credit conversion, and that conversion must match the Stripe path exactly.
func TestRequestInfiniAmountUsdMatchesStripeConversion(t *testing.T) {
	requireDB(t)
	infiniTestSettings(t, `[{"currency":"USD","unit_price":1,"min_topup":1}]`)
	// Pin Stripe to the same manual rate so the comparison is deterministic and
	// no test reaches the live exchange-rate source.
	prevStripeRealtime, prevStripeRate := setting.StripeUseRealtimeRate, setting.StripeUnitPrice
	setting.StripeUseRealtimeRate = false
	setting.StripeUnitPrice = 7.2
	t.Cleanup(func() {
		setting.StripeUseRealtimeRate, setting.StripeUnitPrice = prevStripeRealtime, prevStripeRate
	})
	u := mkUser(t, nil)

	ctx, rec := newCtx(t, http.MethodPost, "/api/user/infini/amount", map[string]any{"amount": 3})
	asUser(ctx, u.Id)
	RequestInfiniAmount(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeInfiniResp(t, rec.Body.Bytes())
	require.Equal(t, "success", resp.Message)
	assert.Equal(t, "3.00", resp.Data)
	assert.InDelta(t, 7.2, resp.ExchangeRate, 0.0001)

	// rate == price => one paid USD credits exactly QuotaPerUnit, same as Stripe.
	infiniQuota := rawQuotaFromPayMoney(decimal.NewFromInt(3), resp.ExchangeRate, operation_setting.Price)
	stripeQuota := rawQuotaFromPayMoney(decimal.NewFromFloat(getStripePayMoney(3, "default")), stripeExchangeRate(), operation_setting.Price)
	assert.Equal(t, int64(1_500_000), infiniQuota)
	assert.Equal(t, infiniQuota, stripeQuota)
}

// An order whose credited quota cannot be settled must be refused before the
// order row exists; otherwise the user pays, creditTopUpQuota rolls the whole
// transaction back, and the order stays pending while the webhook retries.
func TestRequestInfiniPayRejectsQuotaThatCannotBeSettled(t *testing.T) {
	requireDB(t)
	infiniTestSettings(t, `[{"currency":"USD","unit_price":1,"min_topup":1}]`)
	u := mkUser(t, func(u *model.User) { u.Quota = common.MaxWalletQuota - 100 })

	ctx, rec := newCtx(t, http.MethodPost, "/api/user/infini/pay", map[string]any{"amount": 1})
	asUser(ctx, u.Id)
	RequestInfiniPay(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeInfiniResp(t, rec.Body.Bytes())
	assert.Equal(t, "error", resp.Message)
	assert.Equal(t, model.ErrTopUpQuotaLimitExceeded.Error(), fmt.Sprint(resp.Data))

	var count int64
	require.NoError(t, model.DB.Model(&model.TopUp{}).Where("user_id = ?", u.Id).Count(&count).Error)
	assert.Zero(t, count, "no order may be created when the credit cannot be settled")
}

func TestRequestInfiniAmountRejectsQuotaThatCannotBeSettled(t *testing.T) {
	requireDB(t)
	infiniTestSettings(t, `[{"currency":"USD","unit_price":1,"min_topup":1}]`)
	u := mkUser(t, func(u *model.User) { u.Quota = common.MaxWalletQuota - 100 })

	ctx, rec := newCtx(t, http.MethodPost, "/api/user/infini/amount", map[string]any{"amount": 1})
	asUser(ctx, u.Id)
	RequestInfiniAmount(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeInfiniResp(t, rec.Body.Bytes())
	assert.Equal(t, "error", resp.Message)
	assert.Equal(t, model.ErrTopUpQuotaLimitExceeded.Error(), fmt.Sprint(resp.Data))
}

// Boundary: the per-order amount ceiling matches Stripe (10000).
func TestRequestInfiniAmountCeiling(t *testing.T) {
	requireDB(t)
	infiniTestSettings(t, `[{"currency":"USD","unit_price":1,"min_topup":2}]`)
	u := mkUser(t, nil)

	cases := []struct {
		name    string
		amount  int64
		wantErr string
	}{
		{name: "below minimum", amount: 1, wantErr: "充值数量不能小于 2"},
		{name: "at minimum", amount: 2},
		{name: "at ceiling", amount: infiniMaxTopUpAmount},
		{name: "above ceiling", amount: infiniMaxTopUpAmount + 1, wantErr: fmt.Sprintf("充值数量不能大于 %d", infiniMaxTopUpAmount)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, rec := newCtx(t, http.MethodPost, "/api/user/infini/amount", map[string]any{"amount": tc.amount})
			asUser(ctx, u.Id)
			RequestInfiniAmount(ctx)

			require.Equal(t, http.StatusOK, rec.Code)
			resp := decodeInfiniResp(t, rec.Body.Bytes())
			if tc.wantErr == "" {
				require.Equal(t, "success", resp.Message)
				return
			}
			assert.Equal(t, "error", resp.Message)
			assert.Equal(t, tc.wantErr, fmt.Sprint(resp.Data))
		})
	}
}

// rawQuotaFromPayMoney must never hand a truncated (possibly negative) int to
// the order snapshot: decimal.IntPart() silently wraps beyond int64, so the
// wallet ceiling is checked on the decimal before conversion.
func TestRawQuotaFromPayMoneySaturatesAboveWalletCeiling(t *testing.T) {
	prevQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = prevQuotaPerUnit })

	// Comfortably beyond int64 after the QuotaPerUnit multiplication.
	huge := decimal.RequireFromString("1e30")
	quota := rawQuotaFromPayMoney(huge, 7.2, 7.2)
	assert.Equal(t, int64(common.MaxWalletQuota)+1, quota)
	_, err := validateCreditedQuota(decimal.NewFromInt(quota))
	require.EqualError(t, err, "充值额度超出系统可表示范围")

	// Exactly at the ceiling stays representable and is accepted.
	atCeiling := decimal.NewFromInt(int64(common.MaxWalletQuota)).Div(decimal.NewFromInt(500000))
	quota = rawQuotaFromPayMoney(atCeiling, 7.2, 7.2)
	assert.LessOrEqual(t, quota, int64(common.MaxWalletQuota))
	credited, err := validateCreditedQuota(decimal.NewFromInt(quota))
	require.NoError(t, err)
	assert.Positive(t, credited)

	// A dust payment still credits at least one quota, never zero.
	assert.Equal(t, int64(1), rawQuotaFromPayMoney(decimal.RequireFromString("0.0000001"), 7.2, 7.2))
}

// The admin save path refuses an unsupported currency outright, so a
// misconfiguration surfaces immediately instead of after a user has paid.
// Values already stored in the database are untouched by this guard (it only
// runs on save), so an existing non-USD configuration cannot break startup.
func TestUpdateOptionRejectsUnsupportedInfiniCurrency(t *testing.T) {
	cases := []struct {
		name     string
		key      string
		value    string
		contains string
	}{
		{name: "single currency", key: "InfiniCurrency", value: "JPY", contains: "JPY"},
		{name: "currency options", key: "InfiniCurrencies", value: `[{"currency":"EUR","unit_price":0.92,"min_topup":1}]`, contains: "EUR"},
		{name: "malformed options", key: "InfiniCurrencies", value: `{not-an-array`, contains: "JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, rec := newCtx(t, http.MethodPut, "/api/option/", map[string]any{"key": tc.key, "value": tc.value})
			asRoot(ctx, nextTestID())
			UpdateOption(ctx)

			resp := decodeResp(t, rec)
			assert.False(t, resp.Success)
			assert.Contains(t, resp.Message, tc.contains)
		})
	}
}

// infiniExchangeRate mirrors stripeExchangeRate: manual mode always uses the
// configured rate, and a non-positive manual rate never yields 0.
func TestInfiniExchangeRateManualMode(t *testing.T) {
	prevRealtime, prevRate := setting.InfiniUseRealtimeRate, setting.InfiniExchangeRate
	t.Cleanup(func() {
		setting.InfiniUseRealtimeRate, setting.InfiniExchangeRate = prevRealtime, prevRate
	})

	setting.InfiniUseRealtimeRate = false
	setting.InfiniExchangeRate = 9.5
	assert.InDelta(t, 9.5, infiniExchangeRate(), 0.0001)

	setting.InfiniExchangeRate = 0.01
	assert.InDelta(t, 0.01, infiniExchangeRate(), 0.0001)
}
