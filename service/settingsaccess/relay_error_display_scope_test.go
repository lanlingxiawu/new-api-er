package settingsaccess

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The error-message section saves its whole group in one request and reads it
// back through the scoped GET; a key missing from either list makes the section
// unsavable or silently shows fallbacks.
func TestRelayErrorDisplayScope(t *testing.T) {
	uiPayload := map[string]string{
		"enabled":              "true",
		"hide_upstream_errors": "true",
		"default_message":      "服务暂时不可用",
		"rules":                "[]",
	}
	assert.True(t, AllowsGroup("system-tuning.relay-error-display", "relay_error_display_setting", uiPayload))
	assert.False(t, AllowsGroup("system-tuning.relay-error-display", "relay_timeout_setting", map[string]string{"enabled": "true"}),
		"the scope must not reach another module")

	keys, ok := OptionKeys("system-tuning.relay-error-display")
	require.True(t, ok)
	for _, key := range []string{
		"relay_error_display_setting.enabled",
		"relay_error_display_setting.hide_upstream_errors",
		"relay_error_display_setting.default_message",
		"relay_error_display_setting.rules",
	} {
		assert.Contains(t, keys, key)
	}
}
