package model

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
)

// Out-of-range retry gates are rejected by the settings save path before
// anything is persisted or published: a negative max_total_attempts would
// otherwise read as "unlimited" and a negative budget as "off", silently
// disabling the incident switches an admin just tried to set.
func TestBranchAuditSaveConfigGroupRejectsOutOfRangeRetryGates(t *testing.T) {
	before := operation_setting.GetRelayTimeoutSetting()
	for name, values := range map[string]map[string]string{
		"attempts above limit": {"max_total_attempts": "101"},
		"attempts negative":    {"max_total_attempts": "-1"},
		"attempts not integer": {"max_total_attempts": "1.5"},
		"attempts overflow":    {"max_total_attempts": "99999999999999999999"},
		"budget negative":      {"retry_min_budget_seconds": "-1"},
		"budget above limit":   {"retry_min_budget_seconds": "604801"},
		"one bad field in a whole-group save": {
			"enabled": "true", "response_timeout_seconds": "300", "total_timeout_seconds": "600",
			"retry_min_budget_seconds": "10", "max_total_attempts": "-3",
		},
	} {
		t.Run(name, func(t *testing.T) {
			applied, err := SaveConfigGroup("relay_timeout_setting", values)
			assert.Error(t, err)
			assert.False(t, applied)
			assert.Equal(t, before, operation_setting.GetRelayTimeoutSetting(), "a rejected save must not change the live setting")
			assert.Equal(t, before, *operation_setting.GetRelayTimeoutSnapshot(), "nor the snapshot the relay path reads")
		})
	}
}

// The boundary values themselves are valid (validation only, no persistence).
func TestBranchAuditRetryGateBoundaryValuesValidate(t *testing.T) {
	for _, s := range []operation_setting.RelayTimeoutSetting{
		{MaxTotalAttempts: 0},
		{MaxTotalAttempts: 1},
		{MaxTotalAttempts: operation_setting.MaxRelayTotalAttemptsLimit},
		{RetryMinBudgetSeconds: operation_setting.MaxRelayTimeoutSettingSeconds},
	} {
		assert.NoError(t, operation_setting.ValidateRelayTimeoutSetting(s), "%+v", s)
	}
}
