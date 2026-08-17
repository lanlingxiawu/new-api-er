package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildVeridropDetectionCleanupPayload(t *testing.T) {
	const now = int64(2_000_000_000)

	tests := []struct {
		name          string
		retentionDays int
		wantCutoff    int64
		wantErr       bool
	}{
		{name: "negative", retentionDays: -1, wantErr: true},
		{name: "all terminal records before task start", retentionDays: 0, wantCutoff: now},
		{name: "one day", retentionDays: 1, wantCutoff: now - 86400},
		{name: "maximum", retentionDays: 3650, wantCutoff: now - 3650*86400},
		{name: "above maximum", retentionDays: 3651, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := buildVeridropDetectionCleanupPayload(tt.retentionDays, now)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrInvalidVeridropCleanupRetention)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.retentionDays, payload.RetentionDays)
			require.Equal(t, tt.wantCutoff, payload.TargetTimestamp)
			require.Equal(t, 500, payload.BatchSize)
		})
	}
}
