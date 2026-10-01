package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The settings API validates field names against a per-group whitelist. A new
// setting that is not listed there can never be saved — single-field or whole
// group — so the default total-attempt cap would be untunable without a code
// change, and could not be switched off in an incident.
func TestValidateRelayTimeoutFieldsAcceptsRetryControls(t *testing.T) {
	accepted := map[string]string{
		"enabled":                  "true",
		"response_timeout_seconds": "300",
		"total_timeout_seconds":    "600",
		"retry_min_budget_seconds": "10",
		"max_total_attempts":       "6",
	}
	for key, value := range accepted {
		assert.NoError(t, validateRelayTimeoutFields(map[string]string{key: value}),
			"field %q must be editable via the settings API", key)
	}
	// Saving the whole group at once must work too.
	assert.NoError(t, validateRelayTimeoutFields(accepted))
}

func TestValidateRelayTimeoutFieldsRejectsUnknownAndNonNumeric(t *testing.T) {
	assert.Error(t, validateRelayTimeoutFields(map[string]string{"nope": "1"}))
	assert.Error(t, validateRelayTimeoutFields(map[string]string{"max_total_attempts": "abc"}))
	assert.Error(t, validateRelayTimeoutFields(map[string]string{"retry_min_budget_seconds": "x"}))
	assert.Error(t, validateRelayTimeoutFields(map[string]string{"enabled": "notabool"}))
}
