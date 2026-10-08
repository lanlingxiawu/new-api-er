package model

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupRetryStatusConfigRejectsInvalidBeforePersistence(t *testing.T) {
	old := operation_setting.GetGroupRetryStatusSetting()
	t.Cleanup(func() { operation_setting.ReplaceGroupRetryStatusSetting(old) })
	operation_setting.ReplaceGroupRetryStatusSetting(operation_setting.GroupRetryStatusSetting{Enabled: true, Rules: `{"keep":"500"}`})
	for _, values := range []map[string]string{
		{"rules": `{"keep":null}`}, {"enabled": "maybe"}, {"unknown": "true"}, {"rules": "[]"},
	} {
		applied, err := SaveConfigGroup("group_retry_status_setting", values)
		assert.False(t, applied)
		require.ErrorIs(t, err, operation_setting.ErrGroupRetryStatusInvalid)
		allowed, found := operation_setting.GroupRetryStatusAllowed("keep", 500)
		assert.True(t, allowed)
		assert.True(t, found)
	}
	require.ErrorIs(t, UpdateOption("group_retry_status_setting.rules", `{"a":null}`), operation_setting.ErrGroupRetryStatusInvalid)
}
