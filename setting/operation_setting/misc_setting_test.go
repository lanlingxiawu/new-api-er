package operation_setting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── operation_setting.go: AutomaticDisableKeywords ────────────────────────

func saveDisableKeywords(t *testing.T) {
	t.Helper()
	orig := AutomaticDisableKeywords
	t.Cleanup(func() { AutomaticDisableKeywords = orig })
}

func TestAutomaticDisableKeywordsToString(t *testing.T) {
	saveDisableKeywords(t)
	AutomaticDisableKeywords = []string{"a", "b", "c"}
	assert.Equal(t, "a\nb\nc", AutomaticDisableKeywordsToString())
}

// FromString: trims, lower-cases, and drops blank lines (path coverage over the loop).
func TestAutomaticDisableKeywordsFromString(t *testing.T) {
	saveDisableKeywords(t)
	AutomaticDisableKeywordsFromString("  Hello \n\n  WORLD\n   \nFoo")
	assert.Equal(t, []string{"hello", "world", "foo"}, AutomaticDisableKeywords)
}

func TestAutomaticDisableKeywordsFromString_EmptyResetsToEmpty(t *testing.T) {
	saveDisableKeywords(t)
	AutomaticDisableKeywordsFromString("")
	assert.Empty(t, AutomaticDisableKeywords)
}

func TestAutomaticDisableKeywordsFromString_OnlyWhitespace(t *testing.T) {
	saveDisableKeywords(t)
	AutomaticDisableKeywordsFromString("   \n\t\n  ")
	assert.Empty(t, AutomaticDisableKeywords)
}

func TestAutomaticDisableKeywords_RoundTrip(t *testing.T) {
	saveDisableKeywords(t)
	AutomaticDisableKeywords = []string{"one", "two"}
	AutomaticDisableKeywordsFromString(AutomaticDisableKeywordsToString())
	assert.Equal(t, []string{"one", "two"}, AutomaticDisableKeywords)
}

// ── quota_setting.go ──────────────────────────────────────────────────────

func TestGetQuotaSetting(t *testing.T) {
	got := GetQuotaSetting()
	require.NotNil(t, got)
	assert.Same(t, &quotaSetting, got)
	assert.True(t, quotaSetting.EnableFreeModelPreConsume, "default enables free-model pre-consume")
}

// ── token_setting.go ──────────────────────────────────────────────────────

func TestGetTokenSetting(t *testing.T) {
	got := GetTokenSetting()
	require.NotNil(t, got)
	assert.Same(t, &tokenSetting, got)
}

func TestGetMaxUserTokens(t *testing.T) {
	orig := tokenSetting.MaxUserTokens
	t.Cleanup(func() { tokenSetting.MaxUserTokens = orig })
	assert.Equal(t, 1000, GetMaxUserTokens(), "shipped default")
	tokenSetting.MaxUserTokens = 42
	assert.Equal(t, 42, GetMaxUserTokens())
}

// ── checkin_setting.go ────────────────────────────────────────────────────

func TestGetCheckinSetting(t *testing.T) {
	got := GetCheckinSetting()
	require.NotNil(t, got)
	assert.Same(t, &checkinSetting, got)
}

func TestIsCheckinEnabled(t *testing.T) {
	orig := checkinSetting.Enabled
	t.Cleanup(func() { checkinSetting.Enabled = orig })
	checkinSetting.Enabled = false
	assert.False(t, IsCheckinEnabled())
	checkinSetting.Enabled = true
	assert.True(t, IsCheckinEnabled())
}

func TestGetCheckinQuotaRange(t *testing.T) {
	orig := checkinSetting
	t.Cleanup(func() { checkinSetting = orig })
	checkinSetting.MinQuota = 500
	checkinSetting.MaxQuota = 9000
	min, max := GetCheckinQuotaRange()
	assert.Equal(t, 500, min)
	assert.Equal(t, 9000, max)
}

// ── business_stats_circuit_breaker_setting.go ─────────────────────────────

func TestGetBusinessStatsCircuitBreakerSetting(t *testing.T) {
	got := GetBusinessStatsCircuitBreakerSetting()
	require.NotNil(t, got)
	assert.Same(t, &businessStatsCircuitBreakerSetting, got)
	// Document shipped defaults.
	assert.True(t, got.Enabled)
	assert.False(t, got.ManualDisabled)
	assert.Equal(t, 3, got.FailureThreshold)
	assert.Equal(t, int64(60), got.InitialCooldownSeconds)
	assert.Equal(t, int64(3600), got.MaxCooldownSeconds)
	assert.Equal(t, 800, got.SideEffectDBTimeoutMs)
}

// ── business_stats_fallback_backfill_setting.go ───────────────────────────

func TestGetBusinessStatsFallbackBackfillSetting(t *testing.T) {
	got := GetBusinessStatsFallbackBackfillSetting()
	require.NotNil(t, got)
	assert.Same(t, &businessStatsFallbackBackfillSetting, got)
	assert.True(t, got.Enabled)
	assert.Equal(t, 15, got.StatusCacheSeconds)
	assert.Equal(t, 1048576, got.MaxReadLineBytes)
	assert.Equal(t, 200, got.WriteBatchSize)
	assert.Equal(t, 5, got.FlushIntervalSec)
	assert.Equal(t, 200, got.BatchSleepMs)
}

// ── channel_affinity_setting.go ───────────────────────────────────────────

func TestGetChannelAffinitySetting(t *testing.T) {
	got := GetChannelAffinitySetting()
	require.NotNil(t, got)
	// 返回不可变快照，不再是可变全局的指针。
	assert.NotSame(t, &channelAffinitySetting, got)
	assert.True(t, got.Enabled)
	assert.True(t, got.SwitchOnSuccess)
	assert.Equal(t, 100_000, got.MaxEntries)
	assert.Equal(t, 3600, got.DefaultTTLSeconds)
	require.Len(t, got.Rules, 2)
	assert.Equal(t, "codex cli trace", got.Rules[0].Name)
	assert.Equal(t, "claude cli trace", got.Rules[1].Name)
}

// buildPassHeaderTemplate / buildCodexPassHeaderTemplate produce a
// pass_headers operation carrying a *copy* of the header list.
func TestBuildPassHeaderTemplate_Structure(t *testing.T) {
	src := []string{"A", "B"}
	tpl := buildPassHeaderTemplate(src)

	ops, ok := tpl["operations"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, ops, 1)
	assert.Equal(t, "pass_headers", ops[0]["mode"])
	assert.Equal(t, true, ops[0]["keep_origin"])

	val, ok := ops[0]["value"].([]string)
	require.True(t, ok)
	assert.Equal(t, []string{"A", "B"}, val)

	// Mutating the source must not alter the already-built template (defensive clone).
	src[0] = "MUTATED"
	assert.Equal(t, "A", val[0])
}

func TestBuildCodexPassHeaderTemplate_ContainsKnownHeader(t *testing.T) {
	tpl := buildCodexPassHeaderTemplate()
	ops := tpl["operations"].([]map[string]interface{})
	val := ops[0]["value"].([]string)
	assert.Contains(t, val, "Originator")
	assert.Contains(t, val, "User-Agent")
}

// ── commission_tier_reset_setting.go ──────────────────────────────────────

func TestGetCommissionTierResetSetting(t *testing.T) {
	got := GetCommissionTierResetSetting()
	require.NotNil(t, got)
	// 返回不可变快照，不再是可变全局的指针。
	assert.NotSame(t, &commissionTierResetSetting, got)
}

// IsNaturalMonthMode: nil receiver, natural-month, and reset-day equivalence classes.
func TestIsNaturalMonthMode(t *testing.T) {
	var nilPtr *CommissionTierResetSetting
	assert.False(t, nilPtr.IsNaturalMonthMode(), "nil receiver is safe and false")

	assert.True(t, (&CommissionTierResetSetting{PeriodMode: CommissionPeriodModeNaturalMonth}).IsNaturalMonthMode())
	assert.False(t, (&CommissionTierResetSetting{PeriodMode: CommissionPeriodModeResetDay}).IsNaturalMonthMode())
	assert.False(t, (&CommissionTierResetSetting{PeriodMode: ""}).IsNaturalMonthMode())
}

func TestIsCommissionTierResetEnabled(t *testing.T) {
	orig := commissionTierResetSetting.Enabled
	t.Cleanup(func() { commissionTierResetSetting.Enabled = orig })
	commissionTierResetSetting.Enabled = false
	assert.False(t, IsCommissionTierResetEnabled())
	commissionTierResetSetting.Enabled = true
	assert.True(t, IsCommissionTierResetEnabled())
}

// ResolveCommissionTierResetLocation: 4 branches — "Local", "", valid tz, invalid tz.
func TestResolveCommissionTierResetLocation(t *testing.T) {
	t.Run("local-literal", func(t *testing.T) {
		loc, name := ResolveCommissionTierResetLocation("Local")
		assert.Equal(t, time.Local, loc)
		assert.Equal(t, "Local", name)
	})
	t.Run("empty-defaults-to-shanghai", func(t *testing.T) {
		loc, name := ResolveCommissionTierResetLocation("")
		require.NotNil(t, loc)
		assert.Equal(t, "Asia/Shanghai", name)
		assert.Equal(t, "Asia/Shanghai", loc.String())
	})
	t.Run("valid-timezone", func(t *testing.T) {
		loc, name := ResolveCommissionTierResetLocation("America/New_York")
		require.NotNil(t, loc)
		assert.Equal(t, "America/New_York", name)
		assert.Equal(t, "America/New_York", loc.String())
	})
	t.Run("invalid-timezone-falls-back-to-local", func(t *testing.T) {
		loc, name := ResolveCommissionTierResetLocation("Not/AZone")
		assert.Equal(t, time.Local, loc)
		assert.Equal(t, "Local", name)
	})
}

// ── monitor_setting.go: GetMonitorSetting env overrides ───────────────────

func saveMonitorSetting(t *testing.T) {
	t.Helper()
	orig := monitorSetting
	t.Cleanup(func() { monitorSetting = orig })
}

func TestGetMonitorSetting_NoEnv_ForcesScheduledMode(t *testing.T) {
	saveMonitorSetting(t)
	// Ensure no env vars leak in.
	t.Setenv("CHANNEL_TEST_FREQUENCY", "")
	t.Setenv("CHANNEL_TEST_ENABLED", "")
	// Non-canonical mode must be normalized to scheduled_all.
	monitorSetting = MonitorSetting{ChannelTestMode: "garbage"}
	got := GetMonitorSetting()
	assert.Equal(t, ChannelTestModeScheduledAll, got.ChannelTestMode)
}

func TestGetMonitorSetting_PassiveRecoveryModePreserved(t *testing.T) {
	saveMonitorSetting(t)
	t.Setenv("CHANNEL_TEST_FREQUENCY", "")
	t.Setenv("CHANNEL_TEST_ENABLED", "")
	monitorSetting = MonitorSetting{ChannelTestMode: ChannelTestModePassiveRecovery}
	got := GetMonitorSetting()
	assert.Equal(t, ChannelTestModePassiveRecovery, got.ChannelTestMode, "passive recovery is a valid mode, not overwritten")
}

func TestGetMonitorSetting_FrequencyEnvValid(t *testing.T) {
	saveMonitorSetting(t)
	t.Setenv("CHANNEL_TEST_FREQUENCY", "15")
	t.Setenv("CHANNEL_TEST_ENABLED", "")
	monitorSetting = MonitorSetting{AutoTestChannelEnabled: false, AutoTestChannelMinutes: 10}
	got := GetMonitorSetting()
	assert.True(t, got.AutoTestChannelEnabled)
	assert.Equal(t, float64(15), got.AutoTestChannelMinutes)
	assert.Equal(t, ChannelTestModeScheduledAll, got.ChannelTestMode)
}

func TestGetMonitorSetting_FrequencyEnvNonNumericIgnored(t *testing.T) {
	saveMonitorSetting(t)
	t.Setenv("CHANNEL_TEST_FREQUENCY", "abc")
	t.Setenv("CHANNEL_TEST_ENABLED", "")
	monitorSetting = MonitorSetting{AutoTestChannelEnabled: false, AutoTestChannelMinutes: 7}
	got := GetMonitorSetting()
	assert.False(t, got.AutoTestChannelEnabled, "invalid frequency leaves enabled untouched")
	assert.Equal(t, float64(7), got.AutoTestChannelMinutes)
}

func TestGetMonitorSetting_FrequencyEnvZeroIgnored(t *testing.T) {
	saveMonitorSetting(t)
	t.Setenv("CHANNEL_TEST_FREQUENCY", "0") // boundary: frequency>0 is false
	t.Setenv("CHANNEL_TEST_ENABLED", "")
	monitorSetting = MonitorSetting{AutoTestChannelEnabled: false, AutoTestChannelMinutes: 9}
	got := GetMonitorSetting()
	assert.False(t, got.AutoTestChannelEnabled)
	assert.Equal(t, float64(9), got.AutoTestChannelMinutes)
}

func TestGetMonitorSetting_EnabledEnvOverridesFrequency(t *testing.T) {
	saveMonitorSetting(t)
	// Frequency enables, then CHANNEL_TEST_ENABLED=false disables (evaluated last).
	t.Setenv("CHANNEL_TEST_FREQUENCY", "5")
	t.Setenv("CHANNEL_TEST_ENABLED", "false")
	monitorSetting = MonitorSetting{AutoTestChannelEnabled: true, AutoTestChannelMinutes: 20}
	got := GetMonitorSetting()
	assert.False(t, got.AutoTestChannelEnabled)
	assert.Equal(t, float64(5), got.AutoTestChannelMinutes)
}

func TestGetMonitorSetting_EnabledEnvCanEnable(t *testing.T) {
	saveMonitorSetting(t)
	t.Setenv("CHANNEL_TEST_FREQUENCY", "")
	t.Setenv("CHANNEL_TEST_ENABLED", "true")
	monitorSetting = MonitorSetting{AutoTestChannelEnabled: false, AutoTestChannelMinutes: 12}
	got := GetMonitorSetting()
	assert.True(t, got.AutoTestChannelEnabled)
}

func TestGetMonitorSetting_EnabledEnvInvalidIgnored(t *testing.T) {
	saveMonitorSetting(t)
	t.Setenv("CHANNEL_TEST_FREQUENCY", "")
	t.Setenv("CHANNEL_TEST_ENABLED", "notabool")
	monitorSetting = MonitorSetting{AutoTestChannelEnabled: true, AutoTestChannelMinutes: 11}
	got := GetMonitorSetting()
	assert.True(t, got.AutoTestChannelEnabled, "unparseable bool leaves config untouched")
}
