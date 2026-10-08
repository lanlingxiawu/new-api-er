package settingsaccess

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The AI Request Timeout section saves every field of the group in one
// request. A field the UI renders but this allowlist omits makes AllowsGroup
// reject the whole save, so the section becomes unsavable rather than just
// losing one field.
func TestRelayTimeoutScopeAllowsFullUIPayload(t *testing.T) {
	uiPayload := map[string]string{
		"enabled":                  "true",
		"response_timeout_seconds": "300",
		"total_timeout_seconds":    "0",
		"retry_min_budget_seconds": "10",
		"max_total_attempts":       "6",
	}
	assert.True(t, AllowsGroup("system-tuning.relay-timeout", "relay_timeout_setting", uiPayload))

	// Each retry gate on its own too: the group save is per-field allowlisted.
	for _, key := range []string{"retry_min_budget_seconds", "max_total_attempts"} {
		assert.True(t, AllowsGroup("system-tuning.relay-timeout", "relay_timeout_setting", map[string]string{key: "1"}), key)
	}
}

// The scoped GET only returns allowlisted keys; a missing key makes the UI
// silently show its fallback instead of the saved value.
func TestRelayTimeoutScopeExposesRetryGates(t *testing.T) {
	keys, ok := OptionKeys("system-tuning.relay-timeout")
	require.True(t, ok)
	for _, key := range []string{
		"relay_timeout_setting.enabled",
		"relay_timeout_setting.response_timeout_seconds",
		"relay_timeout_setting.total_timeout_seconds",
		"relay_timeout_setting.retry_min_budget_seconds",
		"relay_timeout_setting.max_total_attempts",
	} {
		assert.Contains(t, keys, key)
	}
}

// The per-group retry editor reads and writes a single bare option key under
// its own scope. An unregistered scope fails in the middleware before the
// request reaches the controller, for every role.
func TestGroupRetryTimesScope(t *testing.T) {
	_, ok := Resolve("system-tuning.group-retry-times")
	require.True(t, ok)

	keys, ok := OptionKeys("system-tuning.group-retry-times")
	require.True(t, ok)
	assert.Equal(t, map[string]struct{}{
		"GroupRetryTimes":                    {},
		"group_retry_status_setting.enabled": {},
		"group_retry_status_setting.rules":   {},
	}, keys)
	assert.True(t, AllowsGroup("system-tuning.group-retry-times", "group_retry_status_setting", map[string]string{"enabled": "true", "rules": "{}"}))
	assert.False(t, AllowsGroup("system-tuning.relay-timeout", "group_retry_status_setting", map[string]string{"enabled": "true"}))

	assert.True(t, AllowsOption("system-tuning.group-retry-times", "GroupRetryTimes"))
	// The scope must not become a side door to the pricing map it resembles.
	assert.False(t, AllowsOption("system-tuning.group-retry-times", "GroupRatio"))
	assert.False(t, AllowsOption("system-tuning.group-retry-times", "RetryTimes"))
}
