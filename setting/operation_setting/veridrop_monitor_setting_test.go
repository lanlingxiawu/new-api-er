package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateVeridropMonitorSetting(t *testing.T) {
	valid := GetVeridropMonitorSetting()
	tests := []struct {
		name   string
		mutate func(*VeridropMonitorSetting)
	}{
		{name: "enabled without base URL", mutate: func(setting *VeridropMonitorSetting) {
			setting.Enabled = true
			setting.BaseURL = ""
		}},
		{name: "invalid base URL", mutate: func(setting *VeridropMonitorSetting) {
			setting.Enabled = true
			setting.BaseURL = "not-a-url"
		}},
		{name: "unsupported mode", mutate: func(setting *VeridropMonitorSetting) { setting.DefaultMode = "fast" }},
		{name: "unsupported wire api", mutate: func(setting *VeridropMonitorSetting) { setting.DefaultOpenAIWireAPI = "legacy" }},
		{name: "concurrency too low", mutate: func(setting *VeridropMonitorSetting) { setting.MaxConcurrent = 0 }},
		{name: "batch too high", mutate: func(setting *VeridropMonitorSetting) { setting.BatchSize = 201 }},
		{name: "poll exceeds job timeout", mutate: func(setting *VeridropMonitorSetting) {
			setting.PollIntervalSeconds = 30
			setting.JobTimeoutSeconds = 10
		}},
		{name: "interval too low", mutate: func(setting *VeridropMonitorSetting) { setting.DetectionIntervalMinutes = 14 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			require.Error(t, ValidateVeridropMonitorSetting(candidate))
		})
	}
	require.NoError(t, ValidateVeridropMonitorSetting(valid))
}

func TestVeridropDetectionIntervalMinutesBounds(t *testing.T) {
	original := GetVeridropMonitorSetting()
	t.Cleanup(func() { ReplaceVeridropMonitorSetting(original) })

	tests := []struct {
		name  string
		value int
		want  int
	}{
		{name: "zero uses default", value: 0, want: 1440},
		{name: "below minimum uses minimum", value: 14, want: 15},
		{name: "minimum", value: 15, want: 15},
		{name: "maximum", value: 43200, want: 43200},
		{name: "above maximum uses maximum", value: 43201, want: 43200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setting := original
			setting.DetectionIntervalMinutes = tt.value
			ReplaceVeridropMonitorSetting(setting)
			require.Equal(t, tt.want, GetVeridropMonitorSnapshot().DetectionIntervalMinutes)
		})
	}
}

func TestVeridropMonitorSnapshotIsImmutable(t *testing.T) {
	original := GetVeridropMonitorSetting()
	t.Cleanup(func() { ReplaceVeridropMonitorSetting(original) })

	first := original
	first.BatchSize = 25
	ReplaceVeridropMonitorSetting(first)
	oldSnapshot := GetVeridropMonitorSnapshot()

	second := first
	second.BatchSize = 50
	ReplaceVeridropMonitorSetting(second)

	require.Equal(t, 25, oldSnapshot.BatchSize)
	require.Equal(t, 50, GetVeridropMonitorSnapshot().BatchSize)
}

func TestVeridropMonitorSnapshotNormalizesOperationalBounds(t *testing.T) {
	original := GetVeridropMonitorSetting()
	t.Cleanup(func() { ReplaceVeridropMonitorSetting(original) })

	setting := original
	setting.MaxConcurrent = 99
	setting.BatchSize = 0
	setting.SubmitTimeoutSeconds = 0
	setting.PollIntervalSeconds = 0
	setting.JobTimeoutSeconds = 1
	ReplaceVeridropMonitorSetting(setting)

	snapshot := GetVeridropMonitorSnapshot()
	require.Equal(t, 20, snapshot.MaxConcurrent)
	require.Equal(t, 100, snapshot.BatchSize)
	require.Equal(t, 30, snapshot.SubmitTimeoutSeconds)
	require.Equal(t, 5, snapshot.PollIntervalSeconds)
	require.Equal(t, 300, snapshot.JobTimeoutSeconds)
}
