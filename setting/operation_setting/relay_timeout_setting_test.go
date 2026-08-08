package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRelayTimeoutSettingBoundaries(t *testing.T) {
	for _, setting := range []RelayTimeoutSetting{
		{Enabled: false, ResponseTimeoutSeconds: 0, TotalTimeoutSeconds: 0},
		{Enabled: true, ResponseTimeoutSeconds: 1, TotalTimeoutSeconds: 1},
		{Enabled: true, ResponseTimeoutSeconds: MaxRelayTimeoutSettingSeconds, TotalTimeoutSeconds: MaxRelayTimeoutSettingSeconds},
	} {
		require.NoError(t, ValidateRelayTimeoutSetting(setting))
	}

	for _, setting := range []RelayTimeoutSetting{
		{Enabled: true, ResponseTimeoutSeconds: -1},
		{Enabled: true, ResponseTimeoutSeconds: MaxRelayTimeoutSettingSeconds + 1},
		{Enabled: true, TotalTimeoutSeconds: -1},
		{Enabled: true, TotalTimeoutSeconds: MaxRelayTimeoutSettingSeconds + 1},
	} {
		require.Error(t, ValidateRelayTimeoutSetting(setting))
	}
}

func TestReplaceRelayTimeoutSettingPublishesImmutableSnapshot(t *testing.T) {
	previous := GetRelayTimeoutSetting()
	t.Cleanup(func() { ReplaceRelayTimeoutSetting(previous) })

	first := RelayTimeoutSetting{Enabled: true, ResponseTimeoutSeconds: 12, TotalTimeoutSeconds: 34}
	ReplaceRelayTimeoutSetting(first)
	first.ResponseTimeoutSeconds = 99

	snapshot := GetRelayTimeoutSnapshot()
	require.NotNil(t, snapshot)
	assert.True(t, snapshot.Enabled)
	assert.Equal(t, 12, snapshot.ResponseTimeoutSeconds)
	assert.Equal(t, 34, snapshot.TotalTimeoutSeconds)

	ReplaceRelayTimeoutSetting(RelayTimeoutSetting{Enabled: false, ResponseTimeoutSeconds: 56, TotalTimeoutSeconds: 78})
	assert.True(t, snapshot.Enabled, "a request-held snapshot must not change in place")
	assert.False(t, GetRelayTimeoutSnapshot().Enabled)
}

func TestApplyRelayTimeoutEnvDefaultsUsesParsedStartupGlobals(t *testing.T) {
	previousSetting := GetRelayTimeoutSetting()
	previousResponse := constant.StreamingTimeout
	previousTotal := common.RelayTimeout
	t.Cleanup(func() {
		constant.StreamingTimeout = previousResponse
		common.RelayTimeout = previousTotal
		ReplaceRelayTimeoutSetting(previousSetting)
	})

	constant.StreamingTimeout = 123
	common.RelayTimeout = 456
	ApplyRelayTimeoutEnvDefaults()

	snapshot := GetRelayTimeoutSnapshot()
	require.NotNil(t, snapshot)
	assert.True(t, snapshot.Enabled)
	assert.Equal(t, 123, snapshot.ResponseTimeoutSeconds)
	assert.Equal(t, 456, snapshot.TotalTimeoutSeconds)
}
