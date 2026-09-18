package setting

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setOptionMapFresh replaces common.OptionMap with a fresh, non-nil map (the
// package-level default is nil) and restores the original on cleanup.
func setOptionMapFresh(t *testing.T) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	orig := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = orig
		common.OptionMapRWMutex.Unlock()
	})
}

func setOption(key, value string) {
	common.OptionMapRWMutex.Lock()
	common.OptionMap[key] = value
	common.OptionMapRWMutex.Unlock()
}

// ---------------------------------------------------------------------------
// payment_infini.go
// ---------------------------------------------------------------------------

func saveInfiniSingleCurrency(t *testing.T) {
	t.Helper()
	oc, ou, om, os := InfiniCurrency, InfiniUnitPrice, InfiniMinTopUp, InfiniSandbox
	t.Cleanup(func() {
		InfiniCurrency, InfiniUnitPrice, InfiniMinTopUp, InfiniSandbox = oc, ou, om, os
	})
}

func TestGetInfiniBaseUrl_Production(t *testing.T) {
	saveInfiniSingleCurrency(t)
	InfiniSandbox = false
	assert.Equal(t, "https://openapi.infini.money", GetInfiniBaseUrl())
}

func TestGetInfiniBaseUrl_Sandbox(t *testing.T) {
	saveInfiniSingleCurrency(t)
	InfiniSandbox = true
	assert.Equal(t, "https://openapi-sandbox.infini.money", GetInfiniBaseUrl())
}

func TestGetInfiniCurrencyOptions_FromOptionMap(t *testing.T) {
	setOptionMapFresh(t)
	saveInfiniSingleCurrency(t)
	setOption("InfiniCurrencies", `[{"currency":"EUR","unit_price":0.9,"min_topup":5}]`)

	opts := GetInfiniCurrencyOptions()
	require.Len(t, opts, 1)
	assert.Equal(t, "EUR", opts[0].Currency)
	assert.Equal(t, 0.9, opts[0].UnitPrice)
	assert.Equal(t, 5, opts[0].MinTopUp)
}

func TestGetInfiniCurrencyOptions_EmptyStringFallsBack(t *testing.T) {
	setOptionMapFresh(t)
	saveInfiniSingleCurrency(t)
	// OptionMap has no InfiniCurrencies => "" => single-currency fallback.
	InfiniCurrency = "GBP"
	InfiniUnitPrice = 2.0
	InfiniMinTopUp = 3

	opts := GetInfiniCurrencyOptions()
	require.Len(t, opts, 1)
	assert.Equal(t, "GBP", opts[0].Currency)
	assert.Equal(t, 2.0, opts[0].UnitPrice)
	assert.Equal(t, 3, opts[0].MinTopUp)
}

func TestGetInfiniCurrencyOptions_EmptyArrayFallsBack(t *testing.T) {
	setOptionMapFresh(t)
	saveInfiniSingleCurrency(t)
	setOption("InfiniCurrencies", `[]`)
	InfiniCurrency = "USD"
	InfiniUnitPrice = 1.0
	InfiniMinTopUp = 1

	opts := GetInfiniCurrencyOptions()
	require.Len(t, opts, 1)
	assert.Equal(t, "USD", opts[0].Currency)
}

func TestGetInfiniCurrencyOptions_InvalidJSONFallsBack(t *testing.T) {
	setOptionMapFresh(t)
	saveInfiniSingleCurrency(t)
	setOption("InfiniCurrencies", `{not-an-array`)
	InfiniCurrency = "USD"
	InfiniUnitPrice = 1.0
	InfiniMinTopUp = 1

	opts := GetInfiniCurrencyOptions()
	require.Len(t, opts, 1)
	assert.Equal(t, "USD", opts[0].Currency)
}

func TestGetInfiniCurrencyOptions_ValidJSONButEmptyDecodedFallsBack(t *testing.T) {
	setOptionMapFresh(t)
	saveInfiniSingleCurrency(t)
	// Well-formed JSON array of the right type but zero elements => len==0 path.
	setOption("InfiniCurrencies", `[ ]`)
	InfiniCurrency = ""
	InfiniUnitPrice = 0
	InfiniMinTopUp = 0

	opts := GetInfiniCurrencyOptions()
	require.Len(t, opts, 1)
	// Empty currency defaults to USD; unitPrice<=0 => 1.0; minTopUp<1 => 1.
	assert.Equal(t, "USD", opts[0].Currency)
	assert.Equal(t, 1.0, opts[0].UnitPrice)
	assert.Equal(t, 1, opts[0].MinTopUp)
}

func TestGetInfiniCurrencyOptions_SingleCurrencyDefaults(t *testing.T) {
	setOptionMapFresh(t)
	saveInfiniSingleCurrency(t)
	// All fallback branches: empty currency, non-positive price, sub-1 topup.
	InfiniCurrency = ""
	InfiniUnitPrice = -5
	InfiniMinTopUp = 0

	opts := GetInfiniCurrencyOptions()
	require.Len(t, opts, 1)
	assert.Equal(t, "USD", opts[0].Currency)
	assert.Equal(t, 1.0, opts[0].UnitPrice)
	assert.Equal(t, 1, opts[0].MinTopUp)
}

// Infini settles in USD only: the credited quota is converted with a USD->CNY
// rate, so any other currency would credit the wrong amount while the webhook's
// same-currency amount check still passes.
func TestUnsupportedInfiniCurrency(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty is allowed (defaults to USD)", input: "", want: ""},
		{name: "blank is allowed", input: "   ", want: ""},
		{name: "usd", input: "USD", want: ""},
		{name: "lowercase usd", input: " usd ", want: ""},
		{name: "eur rejected", input: "eur", want: "EUR"},
		{name: "jpy rejected", input: "JPY", want: "JPY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, UnsupportedInfiniCurrency(tc.input))
			assert.Equal(t, tc.want == "" && strings.TrimSpace(tc.input) != "", IsInfiniCurrencySupported(tc.input))
		})
	}
}

func TestUnsupportedInfiniCurrencyInJSON(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty", input: "", want: ""},
		{name: "empty array", input: "[]", want: ""},
		{name: "usd only", input: `[{"currency":"USD","unit_price":1,"min_topup":1}]`, want: ""},
		{name: "first unsupported", input: `[{"currency":"JPY","unit_price":150,"min_topup":1}]`, want: "JPY"},
		{name: "second unsupported", input: `[{"currency":"usd","unit_price":1,"min_topup":1},{"currency":"eur","unit_price":0.92,"min_topup":1}]`, want: "EUR"},
		{name: "malformed json", input: `{not-an-array`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := UnsupportedInfiniCurrencyInJSON(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// Unsupported currencies already stored in the database must keep loading (no
// startup failure); they are simply not offered to users.
func TestGetSupportedInfiniCurrencyOptions_FiltersUnsupported(t *testing.T) {
	setOptionMapFresh(t)
	saveInfiniSingleCurrency(t)
	setOption("InfiniCurrencies", `[{"currency":"JPY","unit_price":150,"min_topup":1},{"currency":"USD","unit_price":1,"min_topup":2}]`)

	all := GetInfiniCurrencyOptions()
	require.Len(t, all, 2, "raw config stays readable")

	supported := GetSupportedInfiniCurrencyOptions()
	require.Len(t, supported, 1)
	assert.Equal(t, "USD", supported[0].Currency)
	assert.Equal(t, 2, supported[0].MinTopUp)

	setOption("InfiniCurrencies", `[{"currency":"EUR","unit_price":0.92,"min_topup":1}]`)
	assert.Empty(t, GetSupportedInfiniCurrencyOptions())
}

func TestSetInfiniCurrencies_WritesOptionMap(t *testing.T) {
	setOptionMapFresh(t)
	err := SetInfiniCurrencies([]constant.InfiniCurrencyOption{
		{Currency: "JPY", UnitPrice: 150, MinTopUp: 100},
	})
	require.NoError(t, err)

	common.OptionMapRWMutex.RLock()
	stored := common.OptionMap["InfiniCurrencies"]
	common.OptionMapRWMutex.RUnlock()
	assert.JSONEq(t, `[{"currency":"JPY","unit_price":150,"min_topup":100}]`, stored)
}

func TestSetInfiniCurrencies_RoundTrip(t *testing.T) {
	setOptionMapFresh(t)
	saveInfiniSingleCurrency(t)
	in := []constant.InfiniCurrencyOption{{Currency: "USD", UnitPrice: 1, MinTopUp: 1}}
	require.NoError(t, SetInfiniCurrencies(in))
	assert.Equal(t, in, GetInfiniCurrencyOptions())
}

func TestInfiniCurrencies2JsonString(t *testing.T) {
	// Serializes the constant defaults (USD single currency).
	assert.JSONEq(t, `[{"currency":"USD","unit_price":1,"min_topup":1}]`, InfiniCurrencies2JsonString())
}

// ---------------------------------------------------------------------------
// payment_waffo.go
// ---------------------------------------------------------------------------

func TestGetWaffoPayMethods_EmptyReturnsDefaults(t *testing.T) {
	setOptionMapFresh(t)
	// No WaffoPayMethods key => defaults copy.
	got := GetWaffoPayMethods()
	assert.Equal(t, constant.DefaultWaffoPayMethods, got)
}

func TestGetWaffoPayMethods_ReturnsIndependentDefaultCopy(t *testing.T) {
	setOptionMapFresh(t)
	got := GetWaffoPayMethods()
	require.NotEmpty(t, got)
	// Mutating the returned slice must not corrupt the shared defaults.
	got[0].Name = "tampered"
	assert.NotEqual(t, "tampered", constant.DefaultWaffoPayMethods[0].Name)
}

func TestGetWaffoPayMethods_FromOptionMap(t *testing.T) {
	setOptionMapFresh(t)
	setOption("WaffoPayMethods", `[{"name":"Crypto","icon":"c","payMethodType":"CRYPTO","payMethodName":"CRYPTO"}]`)
	got := GetWaffoPayMethods()
	require.Len(t, got, 1)
	assert.Equal(t, "Crypto", got[0].Name)
}

func TestGetWaffoPayMethods_InvalidJSONReturnsDefaults(t *testing.T) {
	setOptionMapFresh(t)
	setOption("WaffoPayMethods", `{bad`)
	got := GetWaffoPayMethods()
	assert.Equal(t, constant.DefaultWaffoPayMethods, got)
}

func TestSetWaffoPayMethods_WritesOptionMap(t *testing.T) {
	setOptionMapFresh(t)
	in := []constant.WaffoPayMethod{{Name: "Card", Icon: "i", PayMethodType: "CREDITCARD", PayMethodName: ""}}
	require.NoError(t, SetWaffoPayMethods(in))

	got := GetWaffoPayMethods()
	assert.Equal(t, in, got)
}

func TestWaffoPayMethods2JsonString(t *testing.T) {
	out := WaffoPayMethods2JsonString()
	// Must round-trip back to the defaults.
	setOptionMapFresh(t)
	setOption("WaffoPayMethods", out)
	assert.Equal(t, constant.DefaultWaffoPayMethods, GetWaffoPayMethods())
}

// ---------------------------------------------------------------------------
// Var-only payment configs — defaults sanity (documents intended baselines)
// ---------------------------------------------------------------------------

func TestPaymentDefaults(t *testing.T) {
	assert.Equal(t, 1, AlipayMinTopUp)
	assert.Equal(t, "[]", CreemProducts)
	assert.False(t, CreemTestMode)
	assert.Equal(t, 1.0, InfiniUnitPrice)
	assert.Equal(t, 1, InfiniMinTopUp)
	assert.True(t, InfiniUseRealtimeRate)
	assert.Equal(t, 8.0, InfiniExchangeRate)
	assert.True(t, StripeEnabled)
	assert.Equal(t, 8.0, StripeUnitPrice)
	assert.True(t, StripeUseRealtimeRate)
	assert.Equal(t, 1, StripeMinTopUp)
	assert.False(t, StripePromotionCodesEnabled)
	assert.Equal(t, 1.0, WaffoUnitPrice)
	assert.Equal(t, 1, WaffoMinTopUp)
	assert.Equal(t, 1.0, WaffoPancakeUnitPrice)
	assert.Equal(t, 1, WaffoPancakeMinTopUp)
	assert.Equal(t, 1, WechatMinTopUp)
}

func TestMidjourneyDefaults(t *testing.T) {
	assert.False(t, MjNotifyEnabled)
	assert.False(t, MjAccountFilterEnabled)
	assert.False(t, MjModeClearEnabled)
	assert.True(t, MjForwardUrlEnabled)
	assert.True(t, MjActionCheckSuccessEnabled)
}
