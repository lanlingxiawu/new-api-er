package operation_setting

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func guardTestRule() GroupRetryGuardRule {
	return GroupRetryGuardRule{Enabled: true, WindowSeconds: 60, RPMThreshold: 300, LatencyThresholdMs: 10000, SlowCountThreshold: 5, CooldownMode: "progressive", CooldownSeconds: 60, CooldownMultiplier: 2, MaxCooldownSeconds: 300, ResetAfterSeconds: 600}
}

func TestGroupRetryGuardValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{"short window", "window_seconds", 9}, {"long window", "window_seconds", 3601},
		{"zero rpm", "rpm_threshold", 0}, {"fraction", "rpm_threshold", 1.5},
		{"zero latency", "latency_threshold_ms", 0}, {"zero count", "slow_count_threshold", 0},
		{"bad mode", "cooldown_mode", "other"}, {"zero cooldown", "cooldown_seconds", 0},
		{"low cap", "max_cooldown_seconds", 59}, {"bad multiplier", "cooldown_multiplier", 1},
		{"zero reset", "reset_after_seconds", 0}, {"null", "window_seconds", nil}, {"unknown", "other", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := common.Marshal(guardTestRule())
			require.NoError(t, err)
			var fields map[string]any
			require.NoError(t, common.Unmarshal(raw, &fields))
			fields[tc.field] = tc.value
			raw, err = common.Marshal(map[string]any{"g": fields})
			require.NoError(t, err)
			_, err = CompileGroupRetryGuard(GroupRetryGuardSetting{Enabled: true, Rules: string(raw)})
			require.ErrorIs(t, err, ErrGroupRetryGuardInvalid)
		})
	}
	for _, window := range []int{10, 60, 120, 3600} {
		rule := guardTestRule()
		rule.WindowSeconds = window
		raw, err := common.Marshal(map[string]GroupRetryGuardRule{"g": rule})
		require.NoError(t, err)
		compiled, err := CompileGroupRetryGuard(GroupRetryGuardSetting{Enabled: true, Rules: string(raw)})
		require.NoError(t, err)
		require.Equal(t, window, compiled.Rules["g"].WindowSeconds)
	}
	for _, raw := range []string{`null`, `[]`, `{"g":null}`, `{"g":{}}`, strings.Repeat(" ", 65537)} {
		_, err := CompileGroupRetryGuard(GroupRetryGuardSetting{Rules: raw})
		require.ErrorIs(t, err, ErrGroupRetryGuardInvalid)
	}
}

func TestGroupRetryGuardSnapshotKeepsLastValid(t *testing.T) {
	old := GetGroupRetryGuardSetting()
	t.Cleanup(func() { ReplaceGroupRetryGuardSetting(old) })
	raw, err := common.Marshal(map[string]GroupRetryGuardRule{"g": guardTestRule()})
	require.NoError(t, err)
	ReplaceGroupRetryGuardSetting(GroupRetryGuardSetting{Enabled: true, Rules: string(raw), Revision: "one"})
	require.True(t, GroupRetryGuardSnapshot().Enabled)
	ReplaceGroupRetryGuardSetting(GroupRetryGuardSetting{Enabled: true, Rules: `null`})
	require.Equal(t, "one", GroupRetryGuardSnapshot().Revision)
	ReplaceGroupRetryGuardSetting(GroupRetryGuardSetting{Rules: string(raw), Revision: "two"})
	require.False(t, GroupRetryGuardSnapshot().Enabled)
}
