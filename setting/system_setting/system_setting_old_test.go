package system_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// EnableWorker() returns WorkerUrl != "" — a single decision with both sides
// exercised (boundary: empty vs. any non-empty string).
func TestEnableWorker(t *testing.T) {
	orig := WorkerUrl
	t.Cleanup(func() { WorkerUrl = orig })

	tests := []struct {
		name      string
		workerUrl string
		want      bool
	}{
		{name: "empty_is_disabled", workerUrl: "", want: false},
		{name: "non_empty_is_enabled", workerUrl: "https://worker.example", want: true},
		{name: "whitespace_is_enabled", workerUrl: " ", want: true}, // no trimming: any non-empty enables
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			WorkerUrl = tc.workerUrl
			assert.Equal(t, tc.want, EnableWorker())
		})
	}
}

// Package-level default values documented in system_setting_old.go.
func TestSystemSettingOld_Defaults(t *testing.T) {
	assert.Equal(t, "http://localhost:3000", ServerAddress)
	assert.Equal(t, "", WorkerUrl)
	assert.Equal(t, "", WorkerValidKey)
	assert.False(t, WorkerAllowHttpImageRequestEnabled)
	assert.Equal(t, "", NodeControlServiceUrl)
}
