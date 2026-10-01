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

// The channel cost check settings keep the documented defaults when unset and
// are clamped into range. Zero is meaningful for log queries (logs disabled),
// so only the two durations fall back.
func TestNormalizedChannelCostCheckSettings(t *testing.T) {
	defaults := (PriceMonitorSetting{}).Normalized()
	require.Equal(t, 0, defaults.UpstreamLogQueriesPerHost)
	require.Equal(t, DefaultUpstreamRatioRefreshHours, defaults.UpstreamRatioRefreshHours)
	require.Equal(t, DefaultUpstreamRatioMaxAgeDays, defaults.UpstreamRatioMaxAgeDays)

	high := (PriceMonitorSetting{
		UpstreamLogQueriesPerHost: 99,
		UpstreamRatioRefreshHours: 9999,
		UpstreamRatioMaxAgeDays:   9999,
	}).Normalized()
	require.Equal(t, MaxUpstreamLogQueriesPerHost, high.UpstreamLogQueriesPerHost)
	require.Equal(t, MaxUpstreamRatioRefreshHours, high.UpstreamRatioRefreshHours)
	require.Equal(t, MaxUpstreamRatioMaxAgeDays, high.UpstreamRatioMaxAgeDays)

	negative := (PriceMonitorSetting{UpstreamLogQueriesPerHost: -1}).Normalized()
	require.Equal(t, 0, negative.UpstreamLogQueriesPerHost)
}

// The interval is also the share password lifetime; it is capped at 30 days so the expiry
// computation cannot overflow.
func TestNormalizedCapsInterval(t *testing.T) {
	require.Equal(t, MaxIntervalMinutes, (PriceMonitorSetting{IntervalMinutes: MaxIntervalMinutes}).Normalized().IntervalMinutes)
	require.Equal(t, MaxIntervalMinutes, (PriceMonitorSetting{IntervalMinutes: MaxIntervalMinutes + 1}).Normalized().IntervalMinutes)
	require.Equal(t, MaxIntervalMinutes, (PriceMonitorSetting{IntervalMinutes: int(^uint(0) >> 1)}).Normalized().IntervalMinutes)
	require.Equal(t, MinIntervalMinutes, (PriceMonitorSetting{IntervalMinutes: -5}).Normalized().IntervalMinutes)
}

// IsNormalized is the save-time gate: every editable field, including the cost-check fields,
// must be in range.
func TestIsNormalizedCoversEveryEditableField(t *testing.T) {
	valid := defaultPriceMonitorSetting()
	require.True(t, valid.IsNormalized())
	for name, mutate := range map[string]func(*PriceMonitorSetting){
		"interval below min":      func(s *PriceMonitorSetting) { s.IntervalMinutes = MinIntervalMinutes - 1 },
		"interval above max":      func(s *PriceMonitorSetting) { s.IntervalMinutes = MaxIntervalMinutes + 1 },
		"timeout above max":       func(s *PriceMonitorSetting) { s.TimeoutSeconds = MaxTimeoutSeconds + 1 },
		"log queries above max":   func(s *PriceMonitorSetting) { s.UpstreamLogQueriesPerHost = MaxUpstreamLogQueriesPerHost + 1 },
		"log queries negative":    func(s *PriceMonitorSetting) { s.UpstreamLogQueriesPerHost = -1 },
		"refresh hours zero":      func(s *PriceMonitorSetting) { s.UpstreamRatioRefreshHours = 0 },
		"refresh hours above max": func(s *PriceMonitorSetting) { s.UpstreamRatioRefreshHours = MaxUpstreamRatioRefreshHours + 1 },
		"max age days zero":       func(s *PriceMonitorSetting) { s.UpstreamRatioMaxAgeDays = 0 },
		"max age days above max":  func(s *PriceMonitorSetting) { s.UpstreamRatioMaxAgeDays = MaxUpstreamRatioMaxAgeDays + 1 },
		"official comparison off": func(s *PriceMonitorSetting) { s.IncludeOfficial = false },
	} {
		setting := defaultPriceMonitorSetting()
		mutate(&setting)
		require.False(t, setting.IsNormalized(), name)
	}
	boundary := defaultPriceMonitorSetting()
	boundary.IntervalMinutes = MaxIntervalMinutes
	boundary.UpstreamLogQueriesPerHost = 0
	boundary.UpstreamRatioRefreshHours = MaxUpstreamRatioRefreshHours
	boundary.UpstreamRatioMaxAgeDays = 1
	require.True(t, boundary.IsNormalized(), "boundary values are valid")
}

// A deployment that never saved these keys runs with the design defaults.
func TestDefaultChannelCostCheckSettings(t *testing.T) {
	require.Equal(t, 8, defaultPriceMonitorSetting().UpstreamLogQueriesPerHost)
	require.Equal(t, 6, defaultPriceMonitorSetting().UpstreamRatioRefreshHours)
	require.Equal(t, 7, defaultPriceMonitorSetting().UpstreamRatioMaxAgeDays)
}
