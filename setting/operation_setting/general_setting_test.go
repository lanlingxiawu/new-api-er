package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// saveGeneralSetting captures the mutable global and restores it after the test.
func saveGeneralSetting(t *testing.T) {
	t.Helper()
	orig := generalSetting
	t.Cleanup(func() { generalSetting = orig })
}

func TestGetGeneralSetting_ReturnsGlobalPointer(t *testing.T) {
	saveGeneralSetting(t)
	got := GetGeneralSetting()
	require.NotNil(t, got)
	assert.Same(t, &generalSetting, got)
}

// IsCurrencyDisplay: true for every non-TOKENS type, false only for TOKENS.
func TestIsCurrencyDisplay_DecisionCoverage(t *testing.T) {
	saveGeneralSetting(t)
	cases := []struct {
		dtype string
		want  bool
	}{
		{QuotaDisplayTypeUSD, true},
		{QuotaDisplayTypeCNY, true},
		{QuotaDisplayTypeCustom, true},
		{QuotaDisplayTypeTokens, false},
		{"", true}, // unknown non-tokens value still "currency"
	}
	for _, c := range cases {
		generalSetting.QuotaDisplayType = c.dtype
		assert.Equalf(t, c.want, IsCurrencyDisplay(), "type=%q", c.dtype)
	}
}

func TestIsCNYDisplay_DecisionCoverage(t *testing.T) {
	saveGeneralSetting(t)
	generalSetting.QuotaDisplayType = QuotaDisplayTypeCNY
	assert.True(t, IsCNYDisplay())
	for _, other := range []string{QuotaDisplayTypeUSD, QuotaDisplayTypeCustom, QuotaDisplayTypeTokens, ""} {
		generalSetting.QuotaDisplayType = other
		assert.Falsef(t, IsCNYDisplay(), "type=%q", other)
	}
}

func TestGetQuotaDisplayType_ReturnsConfiguredValue(t *testing.T) {
	saveGeneralSetting(t)
	generalSetting.QuotaDisplayType = QuotaDisplayTypeCustom
	assert.Equal(t, QuotaDisplayTypeCustom, GetQuotaDisplayType())
}

// GetCurrencySymbol: exercise every switch arm + the empty-symbol fallback.
func TestGetCurrencySymbol_AllBranches(t *testing.T) {
	saveGeneralSetting(t)

	t.Run("usd", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeUSD
		assert.Equal(t, "$", GetCurrencySymbol())
	})
	t.Run("cny", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeCNY
		assert.Equal(t, "¥", GetCurrencySymbol())
	})
	t.Run("custom-with-symbol", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeCustom
		generalSetting.CustomCurrencySymbol = "€"
		assert.Equal(t, "€", GetCurrencySymbol())
	})
	t.Run("custom-empty-symbol-falls-back", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeCustom
		generalSetting.CustomCurrencySymbol = ""
		assert.Equal(t, "¤", GetCurrencySymbol())
	})
	t.Run("tokens-default-empty", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeTokens
		assert.Equal(t, "", GetCurrencySymbol())
	})
	t.Run("unknown-default-empty", func(t *testing.T) {
		generalSetting.QuotaDisplayType = "WEIRD"
		assert.Equal(t, "", GetCurrencySymbol())
	})
}

// GetUsdToCurrencyRate: every switch arm + custom rate boundary at 0.
func TestGetUsdToCurrencyRate_AllBranches(t *testing.T) {
	saveGeneralSetting(t)

	t.Run("usd-always-one", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeUSD
		assert.Equal(t, 1.0, GetUsdToCurrencyRate(7.3))
	})
	t.Run("cny-uses-arg", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeCNY
		assert.Equal(t, 7.3, GetUsdToCurrencyRate(7.3))
	})
	t.Run("custom-positive-rate", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeCustom
		generalSetting.CustomCurrencyExchangeRate = 2.5
		assert.Equal(t, 2.5, GetUsdToCurrencyRate(7.3))
	})
	t.Run("custom-zero-rate-boundary", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeCustom
		generalSetting.CustomCurrencyExchangeRate = 0 // boundary: >0 is false
		assert.Equal(t, 1.0, GetUsdToCurrencyRate(7.3))
	})
	t.Run("custom-negative-rate", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeCustom
		generalSetting.CustomCurrencyExchangeRate = -3
		assert.Equal(t, 1.0, GetUsdToCurrencyRate(7.3))
	})
	t.Run("tokens-default-one", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeTokens
		assert.Equal(t, 1.0, GetUsdToCurrencyRate(7.3))
	})
}

func TestGeneralSetting_Defaults(t *testing.T) {
	// Sanity on registered defaults (documents the shipped baseline).
	assert.Equal(t, QuotaDisplayTypeUSD, generalSetting.QuotaDisplayType)
	assert.Equal(t, "¤", generalSetting.CustomCurrencySymbol)
	assert.Equal(t, 1.0, generalSetting.CustomCurrencyExchangeRate)
	assert.Equal(t, 60, generalSetting.PingIntervalSeconds)
}
