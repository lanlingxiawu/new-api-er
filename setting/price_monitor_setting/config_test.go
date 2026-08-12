package price_monitor_setting

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestGetPriceMonitorSettingPublishesImmutableSnapshot(t *testing.T) {
	original := *GetPriceMonitorSetting()
	t.Cleanup(func() {
		config.GlobalConfig.UpdateFromMap("price_monitor_setting", map[string]string{
			"enabled":            strconv.FormatBool(original.Enabled),
			"interval_minutes":   strconv.Itoa(original.IntervalMinutes),
			"timeout_seconds":    strconv.Itoa(original.TimeoutSeconds),
			"include_official":   strconv.FormatBool(original.IncludeOfficial),
			"include_models_dev": strconv.FormatBool(original.IncludeModelsDev),
			"model_whitelist":    original.ModelWhitelist,
		})
	})

	before := GetPriceMonitorSetting()
	require.NoError(t, config.GlobalConfig.UpdateFromMap("price_monitor_setting", map[string]string{
		"enabled":          "true",
		"interval_minutes": "90",
		"timeout_seconds":  "15",
		"model_whitelist":  "gpt-4.1,claude-3",
	}))
	after := GetPriceMonitorSetting()

	require.NotSame(t, before, after)
	require.True(t, after.Enabled)
	require.Equal(t, 90, after.IntervalMinutes)
	require.Equal(t, 15, after.TimeoutSeconds)
	require.Equal(t, "gpt-4.1,claude-3", after.ModelWhitelist)
	require.Equal(t, 360, before.IntervalMinutes)
}

func TestNormalizedClampsUnsafeValues(t *testing.T) {
	setting := PriceMonitorSetting{IntervalMinutes: 1, TimeoutSeconds: 0}
	normalized := setting.Normalized()
	require.Equal(t, 5, normalized.IntervalMinutes)
	require.Equal(t, 10, normalized.TimeoutSeconds)
	require.Equal(t, 120, (PriceMonitorSetting{IntervalMinutes: 5, TimeoutSeconds: 999}).Normalized().TimeoutSeconds)
}

func TestNormalizedAlwaysIncludesOfficialPrices(t *testing.T) {
	setting := (PriceMonitorSetting{IncludeOfficial: false}).Normalized()
	require.True(t, setting.IncludeOfficial)
}
