package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func savePaymentSetting(t *testing.T) {
	t.Helper()
	orig := paymentSetting
	t.Cleanup(func() { paymentSetting = orig })
}

func TestGetPaymentSetting_ReturnsGlobalPointer(t *testing.T) {
	savePaymentSetting(t)
	got := GetPaymentSetting()
	require.NotNil(t, got)
	assert.Same(t, &paymentSetting, got)
}

// GetUserExportMaxRows: boundary at 0 for the <=0 fallback.
func TestGetUserExportMaxRows_Boundaries(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero-falls-back", 0, DefaultUserExportMaxRows},
		{"negative-falls-back", -1, DefaultUserExportMaxRows},
		{"one-lower-valid", 1, 1},
		{"large-valid", 55000, 55000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &PaymentSetting{UserExportMaxRows: c.in}
			assert.Equal(t, c.want, s.GetUserExportMaxRows())
		})
	}
}

// IsPaymentComplianceConfirmed: 2x2 condition matrix over confirmed × version.
func TestIsPaymentComplianceConfirmed_ConditionMatrix(t *testing.T) {
	savePaymentSetting(t)
	cases := []struct {
		name      string
		confirmed bool
		version   string
		want      bool
	}{
		{"confirmed-and-current-version", true, CurrentComplianceTermsVersion, true},
		{"confirmed-but-stale-version", true, "v0", false},
		{"confirmed-but-empty-version", true, "", false},
		{"not-confirmed-current-version", false, CurrentComplianceTermsVersion, false},
		{"not-confirmed-stale-version", false, "v0", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			paymentSetting.ComplianceConfirmed = c.confirmed
			paymentSetting.ComplianceTermsVersion = c.version
			assert.Equal(t, c.want, IsPaymentComplianceConfirmed())
		})
	}
}

func TestPaymentSetting_Defaults(t *testing.T) {
	assert.Equal(t, DefaultUserExportMaxRows, paymentSetting.UserExportMaxRows)
	assert.Equal(t, []int{10, 20, 50, 100, 200, 500}, paymentSetting.AmountOptions)
	assert.NotNil(t, paymentSetting.AmountDiscount)
	assert.Equal(t, "v1", CurrentComplianceTermsVersion)
	assert.Equal(t, 10000, DefaultUserExportMaxRows)
}

// ── payment_setting_old.go ────────────────────────────────────────────────

func savePayMethods(t *testing.T) {
	t.Helper()
	orig := PayMethods
	t.Cleanup(func() { PayMethods = orig })
}

func TestUpdatePayMethodsByJsonString_Valid(t *testing.T) {
	savePayMethods(t)
	err := UpdatePayMethodsByJsonString(`[{"name":"X","type":"custom1"}]`)
	require.NoError(t, err)
	require.Len(t, PayMethods, 1)
	assert.Equal(t, "custom1", PayMethods[0]["type"])
}

func TestUpdatePayMethodsByJsonString_InvalidJSON(t *testing.T) {
	savePayMethods(t)
	err := UpdatePayMethodsByJsonString(`{not json`)
	require.Error(t, err)
	// On failure the slice is reset to empty (not left at previous value).
	assert.Empty(t, PayMethods)
}

func TestUpdatePayMethodsByJsonString_EmptyArray(t *testing.T) {
	savePayMethods(t)
	err := UpdatePayMethodsByJsonString(`[]`)
	require.NoError(t, err)
	assert.Empty(t, PayMethods)
}

func TestPayMethods2JsonString_RoundTrip(t *testing.T) {
	savePayMethods(t)
	PayMethods = []map[string]string{{"type": "alipay"}}
	s := PayMethods2JsonString()
	assert.Contains(t, s, "alipay")

	// Round-trips back through the parser.
	require.NoError(t, UpdatePayMethodsByJsonString(s))
	assert.Equal(t, "alipay", PayMethods[0]["type"])
}

func TestPayMethods2JsonString_Empty(t *testing.T) {
	savePayMethods(t)
	PayMethods = []map[string]string{}
	assert.Equal(t, "[]", PayMethods2JsonString())
}

// ContainsPayMethod: found / not-found equivalence classes.
func TestContainsPayMethod(t *testing.T) {
	savePayMethods(t)
	PayMethods = []map[string]string{
		{"type": "alipay"},
		{"type": "wxpay"},
	}
	assert.True(t, ContainsPayMethod("alipay"))
	assert.True(t, ContainsPayMethod("wxpay"))
	assert.False(t, ContainsPayMethod("custom1"))
	assert.False(t, ContainsPayMethod(""))
}

func TestContainsPayMethod_EmptyList(t *testing.T) {
	savePayMethods(t)
	PayMethods = []map[string]string{}
	assert.False(t, ContainsPayMethod("alipay"))
}
