package operation_setting

import (
	"time"

	"github.com/QuantumNous/new-api/setting/config"
)

const (
	defaultRelayLogConsumeBufMaxEntries      = 20_000
	defaultRelayLogErrorBufMaxEntries        = 5_000
	defaultRelayLogContinuationBufMaxEntries = 20_000
	defaultRelayLogOuterBatchSize            = 2_000
	defaultRelayLogInnerBatchSize            = 500
	defaultRelayLogFlushMaxPerCycle          = 15_000
	defaultRelayLogFlushIntervalMs           = 5_000
	defaultRelayLogWriteTimeoutSec           = 3
	defaultRelayLogFallbackQueueCapacity     = 10_000
	defaultRelayLogShutdownTimeoutSec        = 20
	defaultRelayLogRetryFlushIntervalMs      = 2_000
	defaultRelayLogMaxRetries                = 10
	defaultRelayLogRetryBufMaxEntries        = 50_000
	defaultRelayLogCircuitFailureThreshold   = 5
	defaultRelayLogCircuitOpenSec            = 10
)

type RelayLogPipelineSetting struct {
	Enabled                   bool `json:"enabled"`
	ConsumeBufMaxEntries      int  `json:"consume_buf_max_entries"`
	ErrorBufMaxEntries        int  `json:"error_buf_max_entries"`
	ContinuationBufMaxEntries int  `json:"continuation_buf_max_entries"`
	OuterBatchSize            int  `json:"outer_batch_size"`
	InnerBatchSize            int  `json:"inner_batch_size"`
	FlushMaxPerCycle          int  `json:"flush_max_per_cycle"`
	FullDrain                 bool `json:"full_drain"`
	FlushIntervalMs           int  `json:"flush_interval_ms"`
	WriteTimeoutSec           int  `json:"write_timeout_sec"`
	FallbackQueueCapacity     int  `json:"fallback_queue_capacity"`
	FallbackMaxFileSizeMB     int  `json:"fallback_max_file_size_mb"`
	FallbackMaxFiles          int  `json:"fallback_max_files"`
	ShutdownTimeoutSec        int  `json:"shutdown_timeout_sec"`
}

func positive(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}
func bounded(value, fallback, maximum int) int {
	value = positive(value, fallback)
	if value > maximum {
		return maximum
	}
	return value
}
func (s *RelayLogPipelineSetting) GetConsumeBufMaxEntries() int {
	return bounded(s.ConsumeBufMaxEntries, defaultRelayLogConsumeBufMaxEntries, 1_000_000)
}
func (s *RelayLogPipelineSetting) GetErrorBufMaxEntries() int {
	return bounded(s.ErrorBufMaxEntries, defaultRelayLogErrorBufMaxEntries, 1_000_000)
}
func (s *RelayLogPipelineSetting) GetContinuationBufMaxEntries() int {
	return positive(s.ContinuationBufMaxEntries, defaultRelayLogContinuationBufMaxEntries)
}
func (s *RelayLogPipelineSetting) GetOuterBatchSize() int {
	return bounded(s.OuterBatchSize, defaultRelayLogOuterBatchSize, 10_000)
}
func (s *RelayLogPipelineSetting) GetInnerBatchSize() int {
	return bounded(s.InnerBatchSize, defaultRelayLogInnerBatchSize, 10_000)
}
func (s *RelayLogPipelineSetting) GetFlushMaxPerCycle() int {
	return bounded(s.FlushMaxPerCycle, defaultRelayLogFlushMaxPerCycle, 100_000)
}
func (s *RelayLogPipelineSetting) GetFlushInterval() time.Duration {
	return time.Duration(bounded(s.FlushIntervalMs, defaultRelayLogFlushIntervalMs, 60_000)) * time.Millisecond
}
func (s *RelayLogPipelineSetting) GetWriteTimeout() time.Duration {
	return time.Duration(bounded(s.WriteTimeoutSec, defaultRelayLogWriteTimeoutSec, 30)) * time.Second
}
func (s *RelayLogPipelineSetting) GetFallbackQueueCapacity() int {
	return positive(s.FallbackQueueCapacity, defaultRelayLogFallbackQueueCapacity)
}
func (s *RelayLogPipelineSetting) GetShutdownTimeout() time.Duration {
	return time.Duration(positive(s.ShutdownTimeoutSec, defaultRelayLogShutdownTimeoutSec)) * time.Second
}

var relayLogPipelineSetting = RelayLogPipelineSetting{
	Enabled:              true,
	ConsumeBufMaxEntries: defaultRelayLogConsumeBufMaxEntries, ErrorBufMaxEntries: defaultRelayLogErrorBufMaxEntries,
	ContinuationBufMaxEntries: defaultRelayLogContinuationBufMaxEntries, OuterBatchSize: defaultRelayLogOuterBatchSize,
	InnerBatchSize: defaultRelayLogInnerBatchSize, FlushMaxPerCycle: defaultRelayLogFlushMaxPerCycle,
	FlushIntervalMs: defaultRelayLogFlushIntervalMs, WriteTimeoutSec: defaultRelayLogWriteTimeoutSec,
	FallbackQueueCapacity: defaultRelayLogFallbackQueueCapacity, FallbackMaxFileSizeMB: 256,
	FallbackMaxFiles: 8, ShutdownTimeoutSec: defaultRelayLogShutdownTimeoutSec,
}

type RelayLogRetrySetting struct {
	RetryFlushIntervalMs    int  `json:"retry_flush_interval_ms"`
	MaxRetries              int  `json:"max_retries"`
	RetryBufMaxEntries      int  `json:"retry_buf_max_entries"`
	AllowConcurrentFlush    bool `json:"allow_concurrent_flush"`
	CircuitFailureThreshold int  `json:"circuit_failure_threshold"`
	CircuitOpenSec          int  `json:"circuit_open_sec"`
}

func (s *RelayLogRetrySetting) GetRetryFlushInterval() time.Duration {
	return time.Duration(positive(s.RetryFlushIntervalMs, defaultRelayLogRetryFlushIntervalMs)) * time.Millisecond
}
func (s *RelayLogRetrySetting) GetMaxRetries() int {
	return bounded(s.MaxRetries, defaultRelayLogMaxRetries, 100)
}
func (s *RelayLogRetrySetting) GetRetryBufMaxEntries() int {
	return bounded(s.RetryBufMaxEntries, defaultRelayLogRetryBufMaxEntries, 1_000_000)
}
func (s *RelayLogRetrySetting) GetCircuitFailureThreshold() int {
	return positive(s.CircuitFailureThreshold, defaultRelayLogCircuitFailureThreshold)
}
func (s *RelayLogRetrySetting) GetCircuitOpenDuration() time.Duration {
	return time.Duration(positive(s.CircuitOpenSec, defaultRelayLogCircuitOpenSec)) * time.Second
}

var relayLogRetrySetting = RelayLogRetrySetting{RetryFlushIntervalMs: defaultRelayLogRetryFlushIntervalMs, MaxRetries: defaultRelayLogMaxRetries, RetryBufMaxEntries: defaultRelayLogRetryBufMaxEntries, CircuitFailureThreshold: defaultRelayLogCircuitFailureThreshold, CircuitOpenSec: defaultRelayLogCircuitOpenSec}

func init() {
	config.GlobalConfig.Register("relay_log_pipeline_setting", &relayLogPipelineSetting)
	config.GlobalConfig.Register("relay_log_retry_setting", &relayLogRetrySetting)
}
func GetRelayLogPipelineSetting() *RelayLogPipelineSetting { return &relayLogPipelineSetting }
func GetRelayLogRetrySetting() *RelayLogRetrySetting       { return &relayLogRetrySetting }
