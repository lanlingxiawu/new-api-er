package operation_setting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRelayLogPipelineSettingFallbacks(t *testing.T) {
	s := RelayLogPipelineSetting{}
	require.Equal(t, 20_000, s.GetConsumeBufMaxEntries())
	require.Equal(t, 5_000, s.GetErrorBufMaxEntries())
	require.Equal(t, 500, s.GetInnerBatchSize())
	require.Equal(t, 5*time.Second, s.GetFlushInterval())
	require.Equal(t, 3*time.Second, s.GetWriteTimeout())
	require.Equal(t, 20*time.Second, s.GetShutdownTimeout())
}

func TestRelayLogPipelineDefaultsEnabled(t *testing.T) {
	require.True(t, relayLogPipelineSetting.Enabled)
}

func TestRelayLogRetrySettingFallbacks(t *testing.T) {
	s := RelayLogRetrySetting{}
	require.Equal(t, 2*time.Second, s.GetRetryFlushInterval())
	require.Equal(t, 10, s.GetMaxRetries())
	require.Equal(t, 50_000, s.GetRetryBufMaxEntries())
	require.Equal(t, 5, s.GetCircuitFailureThreshold())
	require.Equal(t, 10*time.Second, s.GetCircuitOpenDuration())
}

func TestRelayLogSettingsClampHotUpdatedValues(t *testing.T) {
	pipeline := RelayLogPipelineSetting{
		ConsumeBufMaxEntries: 9_000_000,
		InnerBatchSize:       9_000_000,
		FlushIntervalMs:      9_000_000,
		WriteTimeoutSec:      9_000_000,
	}
	require.Equal(t, 1_000_000, pipeline.GetConsumeBufMaxEntries())
	require.Equal(t, 10_000, pipeline.GetInnerBatchSize())
	require.Equal(t, time.Minute, pipeline.GetFlushInterval())
	require.Equal(t, 30*time.Second, pipeline.GetWriteTimeout())

	retry := RelayLogRetrySetting{MaxRetries: 9_000_000, RetryBufMaxEntries: 9_000_000}
	require.Equal(t, 100, retry.GetMaxRetries())
	require.Equal(t, 1_000_000, retry.GetRetryBufMaxEntries())
}

func TestRelayLogAuxiliaryQueueCapacitiesHaveNoFixedMaximum(t *testing.T) {
	pipeline := RelayLogPipelineSetting{
		ContinuationBufMaxEntries: 200_001,
		FallbackQueueCapacity:     100_001,
	}
	require.Equal(t, 200_001, pipeline.GetContinuationBufMaxEntries())
	require.Equal(t, 100_001, pipeline.GetFallbackQueueCapacity())

	invalid := RelayLogPipelineSetting{}
	require.Equal(t, defaultRelayLogContinuationBufMaxEntries, invalid.GetContinuationBufMaxEntries())
	require.Equal(t, defaultRelayLogFallbackQueueCapacity, invalid.GetFallbackQueueCapacity())
}
