package model

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// Regression (branch audit §5): the upstream-log trace must resolve to the
// final attempt. Id order is not response order: under backlog the flush budget
// is spent on consume logs first (design §8.1), so a retried request's earlier
// error log is inserted after its final consume log in the same second.
// Retry-buffer and fallback-replay inserts break id order the same way (§8.5),
// so GetLogTraceByRequestId breaks same-second ties by row type, not by id.
func TestRelayLogBranchAuditRegression_TraceResolvesToEarlierErrorLogUnderBacklog(t *testing.T) {
	useRelayLogPipelineSQLite(t)
	useRelayLogFallbackDir(t)
	cfg := operation_setting.GetRelayLogPipelineSetting()
	old := *cfg
	t.Cleanup(func() { *cfg = old; resetRelayLogPipelineForTest() })
	cfg.FlushMaxPerCycle, cfg.FullDrain = 1, false

	// Attempt 1 fails on channel 1, the retry succeeds on channel 2, same second.
	require.True(t, enqueueRelayLog(relayLogKindError, &relayLogEvent{Log: &Log{RequestId: "trace-order", Type: LogTypeError, CreatedAt: 100, ChannelId: 1}}))
	require.True(t, enqueueRelayLog(relayLogKindConsume, &relayLogEvent{Log: &Log{RequestId: "trace-order", Type: LogTypeConsume, CreatedAt: 100, ChannelId: 2}}))
	flushRelayLogs()
	flushRelayLogs()

	got, err := GetLogTraceByRequestId("trace-order")
	require.NoError(t, err)
	require.Equal(t, 2, got.ChannelId, "trace must resolve to the final (successful) attempt")
}

func TestLogTraceNewer(t *testing.T) {
	consume := func(id int, at int64) Log { return Log{Id: id, CreatedAt: at, Type: LogTypeConsume} }
	failed := func(id int, at int64) Log { return Log{Id: id, CreatedAt: at, Type: LogTypeError} }
	for _, tc := range []struct {
		name string
		a, b Log
		want bool
	}{
		{"later second wins over consume", failed(1, 101), consume(9, 100), true},
		{"earlier second loses even with higher id", failed(9, 100), consume(1, 101), false},
		{"same second: consume beats higher-id error", consume(1, 100), failed(9, 100), true},
		{"same second: error loses to lower-id consume", failed(9, 100), consume(1, 100), false},
		{"same second, both errors: higher id", failed(9, 100), failed(1, 100), true},
		{"same second, both consume: higher id", consume(2, 100), consume(3, 100), false},
		{"identical row is not newer", consume(3, 100), consume(3, 100), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, logTraceNewer(tc.a, tc.b))
		})
	}
}
