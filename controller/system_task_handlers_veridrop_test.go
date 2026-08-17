package controller

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestVeridropDetectionHandlerScheduleSettings(t *testing.T) {
	original := operation_setting.GetVeridropMonitorSetting()
	setting := original
	t.Cleanup(func() { operation_setting.ReplaceVeridropMonitorSetting(original) })
	handler := veridropDetectionHandler{}

	setting.Enabled = false
	setting.AutoDetectionEnabled = true
	setting.BaseURL = "https://veridrop.example"
	operation_setting.ReplaceVeridropMonitorSetting(setting)
	require.False(t, handler.Enabled())

	setting.Enabled = true
	setting.AutoDetectionEnabled = false
	operation_setting.ReplaceVeridropMonitorSetting(setting)
	require.False(t, handler.Enabled())

	setting.AutoDetectionEnabled = true
	setting.BaseURL = ""
	operation_setting.ReplaceVeridropMonitorSetting(setting)
	require.False(t, handler.Enabled())

	setting.BaseURL = "https://veridrop.example"
	setting.DetectionIntervalMinutes = 30
	operation_setting.ReplaceVeridropMonitorSetting(setting)
	require.True(t, handler.Enabled())
	require.Equal(t, 30*time.Minute, handler.Interval())

	payloadJSON, err := common.Marshal(handler.NewPayload())
	require.NoError(t, err)
	var payload service.VeridropDetectionTaskPayload
	require.NoError(t, common.Unmarshal(payloadJSON, &payload))
	require.True(t, payload.Batch)
}
