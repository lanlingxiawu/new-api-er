package model

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// Regression checks for the export defects reproduced from the 2026-11-05 CSV.
// They use only in-memory log records and must not initialize a database.
func TestProductionLogIncidentRetryCountReproduction(t *testing.T) {
	for _, tc := range []struct {
		name, chain string
		attempts    int
	}{
		{"first attempt only", `["491"]`, 1},
		{"one retry", `["499","275"]`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &Log{Other: `{"admin_info":{"use_channel":` + tc.chain + `}}`}
			observed := renderOne(t, "retry_count", log, newTestRowCtx())
			require.Equal(t, strconv.Itoa(tc.attempts-1), observed)
			minimum := tc.attempts
			require.False(t, (LogExportFilter{MinRetryCount: &minimum}).matchesRow(log, newTestRowCtx()))
		})
	}
}

func TestProductionLogIncidentReleasedAnomalyReproduction(t *testing.T) {
	for _, state := range []string{"released", "settled", "failed"} {
		t.Run(state, func(t *testing.T) {
			log := &Log{Other: `{"stream_result":{"usage_source":"upstream","settlement_state":"` + state + `"}}`}
			flags := logAnomalyFlags(log, newTestRowCtx())
			if state == "settled" || state == "released" {
				require.Empty(t, flags)
				return
			}
			require.Contains(t, flags, LogAnomalyUnsettled)
		})
	}
}
