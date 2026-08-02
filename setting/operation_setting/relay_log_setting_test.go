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

// 这两项的上界同时是底层 channel 的物理容量（设计文档 §21.1），所以钳制不是
// 产品口味问题：越界的配置会让"后台显示已扩容、投递仍按物理容量丢弃"重新出现。
func TestRelayLogAuxiliaryQueueCapacitiesClampToPhysicalMaximum(t *testing.T) {
	atMax := RelayLogPipelineSetting{
		ContinuationBufMaxEntries: MaxRelayLogContinuationBufMaxEntries,
		FallbackQueueCapacity:     MaxRelayLogFallbackQueueCapacity,
	}
	require.Equal(t, MaxRelayLogContinuationBufMaxEntries, atMax.GetContinuationBufMaxEntries())
	require.Equal(t, MaxRelayLogFallbackQueueCapacity, atMax.GetFallbackQueueCapacity())

	overMax := RelayLogPipelineSetting{
		ContinuationBufMaxEntries: MaxRelayLogContinuationBufMaxEntries + 1,
		FallbackQueueCapacity:     MaxRelayLogFallbackQueueCapacity + 1,
	}
	require.Equal(t, MaxRelayLogContinuationBufMaxEntries, overMax.GetContinuationBufMaxEntries())
	require.Equal(t, MaxRelayLogFallbackQueueCapacity, overMax.GetFallbackQueueCapacity())

	invalid := RelayLogPipelineSetting{}
	require.Equal(t, defaultRelayLogContinuationBufMaxEntries, invalid.GetContinuationBufMaxEntries())
	require.Equal(t, defaultRelayLogFallbackQueueCapacity, invalid.GetFallbackQueueCapacity())
	require.LessOrEqual(t, defaultRelayLogContinuationBufMaxEntries, MaxRelayLogContinuationBufMaxEntries)
	require.LessOrEqual(t, defaultRelayLogFallbackQueueCapacity, MaxRelayLogFallbackQueueCapacity)
}
