package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Review item 6: a relay_error_display_setting key saved through the
// single-option endpoint skipped ValidateRelayErrorDisplaySetting, so rules the
// group save refuses were stored with success. The live setting with the one
// field changed is now validated like a group save.
func TestValidateOptionValue_RelayErrorDisplay(t *testing.T) {
	previous := operation_setting.GetRelayErrorDisplaySetting()
	t.Cleanup(func() { operation_setting.ReplaceRelayErrorDisplaySetting(previous) })
	live := operation_setting.RelayErrorDisplaySetting{HideUpstreamErrors: true}
	operation_setting.ReplaceRelayErrorDisplaySetting(live)

	for _, tc := range []struct {
		key, value string
	}{
		{"relay_error_display_setting.rules", `[{"source":"upstream","action":"replace"}]`},
		{"relay_error_display_setting.rules", `{`},
		{"relay_error_display_setting.rules", `[{"source":"any","action":"edit","edits":[{"find":"a*","regex":true}]}]`},
		{"relay_error_display_setting.default_message", strings.Repeat("m", operation_setting.MaxRelayErrorMessageLen+1)},
		{"relay_error_display_setting.enabled", "maybe"},
		{"relay_error_display_setting.unknown", "x"},
	} {
		err := validateOptionValue(tc.key, tc.value)
		require.Error(t, err, "%s=%q", tc.key, tc.value)
		assert.ErrorIs(t, err, operation_setting.ErrRelayErrorDisplayInvalid, "%s=%q: answered with the translated message", tc.key, tc.value)
	}
	for _, tc := range []struct{ key, value string }{
		{"relay_error_display_setting.rules", `[{"source":"upstream","keywords":["quota"],"action":"replace","message":"Busy"}]`},
		{"relay_error_display_setting.rules", ""},
		{"relay_error_display_setting.enabled", "true"},
		{"relay_error_display_setting.hide_upstream_errors", "false"},
		{"relay_error_display_setting.default_message", "Try again later"},
	} {
		assert.NoError(t, validateOptionValue(tc.key, tc.value), "%s=%q", tc.key, tc.value)
	}
	assert.Equal(t, live, operation_setting.GetRelayErrorDisplaySetting(), "validation never changes the live setting")
}
