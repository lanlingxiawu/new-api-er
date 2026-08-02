package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// applyGeneralSetting 把对草稿的直接修改同步到快照。
// 生产代码通过管理接口改配置，ConfigManager 会自动重发快照；
// 测试直接改包级草稿，所以要显式同步一次。
func applyGeneralSetting() { ReplaceGeneralSetting(generalSetting) }

// saveGeneralSetting captures the mutable global and restores it after the test.
func saveGeneralSetting(t *testing.T) {
	t.Helper()
	orig := generalSetting
	t.Cleanup(func() { ReplaceGeneralSetting(orig) })
}

// GetGeneralSetting 现在返回的是不可变快照，不再是可写的全局指针。
//
// 这份配置含 string 字段且被 relay 路径读取，而配置写入是在 configDraftMutex 下用
// 反射原地改草稿的——读侧直接持裸指针会读到写了一半的字符串。改成快照之后，
// 读侧拿到的副本在其生命周期内不会被改写。
func TestGetGeneralSetting_ReturnsImmutableSnapshot(t *testing.T) {
	saveGeneralSetting(t)

	got := GetGeneralSetting()
	require.NotNil(t, got)
	assert.NotSame(t, &generalSetting, got, "不能再把可写的草稿指针交出去")
	assert.Equal(t, generalSetting, *got, "快照内容必须与草稿一致")

	// 已持有的快照不受后续变更影响
	before := GetGeneralSetting()
	beforeType := before.QuotaDisplayType
	ReplaceGeneralSetting(GeneralSetting{QuotaDisplayType: QuotaDisplayTypeTokens})
	assert.Equal(t, beforeType, before.QuotaDisplayType, "旧快照被改写了")
	assert.Equal(t, QuotaDisplayTypeTokens, GetGeneralSetting().QuotaDisplayType)
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
		applyGeneralSetting()
		assert.Equalf(t, c.want, IsCurrencyDisplay(), "type=%q", c.dtype)
	}
}

func TestIsCNYDisplay_DecisionCoverage(t *testing.T) {
	saveGeneralSetting(t)
	generalSetting.QuotaDisplayType = QuotaDisplayTypeCNY
	applyGeneralSetting()
	assert.True(t, IsCNYDisplay())
	for _, other := range []string{QuotaDisplayTypeUSD, QuotaDisplayTypeCustom, QuotaDisplayTypeTokens, ""} {
		generalSetting.QuotaDisplayType = other
		applyGeneralSetting()
		assert.Falsef(t, IsCNYDisplay(), "type=%q", other)
	}
}

func TestGetQuotaDisplayType_ReturnsConfiguredValue(t *testing.T) {
	saveGeneralSetting(t)
	generalSetting.QuotaDisplayType = QuotaDisplayTypeCustom
	applyGeneralSetting()
	assert.Equal(t, QuotaDisplayTypeCustom, GetQuotaDisplayType())
}

// GetCurrencySymbol: exercise every switch arm + the empty-symbol fallback.
func TestGetCurrencySymbol_AllBranches(t *testing.T) {
	saveGeneralSetting(t)

	t.Run("usd", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeUSD
		applyGeneralSetting()
		assert.Equal(t, "$", GetCurrencySymbol())
	})
	t.Run("cny", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeCNY
		applyGeneralSetting()
		assert.Equal(t, "¥", GetCurrencySymbol())
	})
	t.Run("custom-with-symbol", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeCustom
		applyGeneralSetting()
		generalSetting.CustomCurrencySymbol = "€"
		applyGeneralSetting()
		assert.Equal(t, "€", GetCurrencySymbol())
	})
	t.Run("custom-empty-symbol-falls-back", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeCustom
		applyGeneralSetting()
		generalSetting.CustomCurrencySymbol = ""
		applyGeneralSetting()
		assert.Equal(t, "¤", GetCurrencySymbol())
	})
	t.Run("tokens-default-empty", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeTokens
		applyGeneralSetting()
		assert.Equal(t, "", GetCurrencySymbol())
	})
	t.Run("unknown-default-empty", func(t *testing.T) {
		generalSetting.QuotaDisplayType = "WEIRD"
		applyGeneralSetting()
		assert.Equal(t, "", GetCurrencySymbol())
	})
}

// GetUsdToCurrencyRate: every switch arm + custom rate boundary at 0.
func TestGetUsdToCurrencyRate_AllBranches(t *testing.T) {
	saveGeneralSetting(t)

	t.Run("usd-always-one", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeUSD
		applyGeneralSetting()
		assert.Equal(t, 1.0, GetUsdToCurrencyRate(7.3))
	})
	t.Run("cny-uses-arg", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeCNY
		applyGeneralSetting()
		assert.Equal(t, 7.3, GetUsdToCurrencyRate(7.3))
	})
	t.Run("custom-positive-rate", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeCustom
		applyGeneralSetting()
		generalSetting.CustomCurrencyExchangeRate = 2.5
		applyGeneralSetting()
		assert.Equal(t, 2.5, GetUsdToCurrencyRate(7.3))
	})
	t.Run("custom-zero-rate-boundary", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeCustom
		applyGeneralSetting()
		generalSetting.CustomCurrencyExchangeRate = 0 // boundary: >0 is false
		applyGeneralSetting()
		assert.Equal(t, 1.0, GetUsdToCurrencyRate(7.3))
	})
	t.Run("custom-negative-rate", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeCustom
		applyGeneralSetting()
		generalSetting.CustomCurrencyExchangeRate = -3
		applyGeneralSetting()
		assert.Equal(t, 1.0, GetUsdToCurrencyRate(7.3))
	})
	t.Run("tokens-default-one", func(t *testing.T) {
		generalSetting.QuotaDisplayType = QuotaDisplayTypeTokens
		applyGeneralSetting()
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

// TestLegacyDisplayInCurrencyToggleReachesSnapshot 是"改了草稿忘了发布快照"的回归。
//
// 旧的 DisplayInCurrencyEnabled 开关会同步到 general_setting.quota_display_type。
// 它原本直接调包级 UpdateConfigFromMap 改全局草稿——迁移到快照之后那样写，
// 草稿变了但读侧仍看旧快照，开关会静默失效。必须走 ConfigManager.UpdateFromMap，
// 由它在草稿锁内改字段并重新发布。
func TestLegacyDisplayInCurrencyToggleReachesSnapshot(t *testing.T) {
	saveGeneralSetting(t)
	ReplaceGeneralSetting(GeneralSetting{QuotaDisplayType: QuotaDisplayTypeTokens})
	require.Equal(t, QuotaDisplayTypeTokens, GetGeneralSetting().QuotaDisplayType)

	require.NoError(t, config.GlobalConfig.UpdateFromMap("general_setting",
		map[string]string{"quota_display_type": QuotaDisplayTypeUSD}))

	assert.Equal(t, QuotaDisplayTypeUSD, GetGeneralSetting().QuotaDisplayType,
		"改了草稿但快照没重发，读侧看不到这次修改")
	assert.True(t, IsCurrencyDisplay(), "模块内部的辅助函数也必须读到新值")
}
