package operation_setting

import (
	"time"

	"github.com/QuantumNous/new-api/setting/config"
)

// LedgerPipelineSetting controls the in-memory ledger pipeline that batches
// cost/commission records before writing to DB.
type LedgerPipelineSetting struct {
	// FlushIntervalSec is seconds between each flush cycle.
	// Throughput = SettlementFlushMaxPerCycle × 60 ÷ FlushIntervalSec per minute.
	FlushIntervalSec int `json:"flush_interval_sec"`
	// OuterBatchSize is records pulled per outer loop iteration for the settlement queue
	// (error-recovery granularity; also serves as fallback for CostOuterBatchSize when <= 0).
	OuterBatchSize int `json:"outer_batch_size"`
	// CostOuterBatchSize overrides OuterBatchSize for the cost-only flush path.
	// 0 (default) means inherit OuterBatchSize.
	CostOuterBatchSize int `json:"cost_outer_batch_size"`
	// InnerBatchSize is rows per SQL INSERT statement. Larger = fewer round-trips.
	InnerBatchSize int `json:"inner_batch_size"`
	// SettlementFlushMaxPerCycle is max commission-settlement cost+commission records dequeued per flush cycle.
	// Ignored when FullDrain is true.
	SettlementFlushMaxPerCycle int `json:"settlement_flush_max_per_cycle"`
	// CostFlushMaxPerCycle is max cost-only records dequeued from costLedgerBuf per flush cycle.
	// 0 (default) means drain all — preserving the original unbounded behaviour.
	// Ignored when FullDrain is true.
	CostFlushMaxPerCycle int `json:"cost_flush_max_per_cycle"`
	// FullDrain drains the entire buffer each cycle, ignoring both SettlementFlushMaxPerCycle
	// and CostFlushMaxPerCycle.
	FullDrain bool `json:"full_drain"`
	// BufMaxEntries is the in-memory buffer ceiling per queue.
	// Oldest entries are dropped when exceeded to prevent OOM during DB outages.
	BufMaxEntries int `json:"buf_max_entries"`
	// DedupMemMaxEntries is the commission log-id dedup set ceiling.
	// Set is rebuilt when exceeded; DB ON CONFLICT guarantees idempotence.
	DedupMemMaxEntries int `json:"dedup_mem_max_entries"`
	// DedupUseRedis controls the commission aggregation-gating dedup backend.
	// false (default) = process-local in-memory set (correct for single instance).
	// true = shared Redis SETNX (required for multi-instance, otherwise each
	// instance dedups independently and employee summaries double-count).
	DedupUseRedis bool `json:"dedup_use_redis"`
	// DedupRedisTTLSec is the TTL (seconds) of each Redis dedup key. It only needs
	// to cover the settlement retry/replay window. Larger TTL = more Redis memory
	// (one key per settled commission log_id while live). <=0 falls back to default.
	DedupRedisTTLSec int `json:"dedup_redis_ttl_sec"`
	// FlushDBTimeoutSec is the per-flush DB write deadline (seconds). A flush DB call
	// exceeding it is treated as a failure (requeue + backoff), so a single stuck SQL
	// statement cannot stall the whole flush loop. <=0 falls back to default.
	FlushDBTimeoutSec int `json:"flush_db_timeout_sec"`
	// FallbackQueueCapacity is the soft capacity limit of the async fallback-file
	// write queue.  Writes that would push len(queue) past this threshold are
	// rejected and trigger the circuit-breaker hard-disable.  The underlying
	// channel is allocated at a fixed hard cap (3×default); this value controls
	// the effective limit at runtime.  <=0 falls back to default.
	FallbackQueueCapacity int `json:"fallback_queue_capacity"`
	// ShutdownTimeoutSec is the total wall-clock budget (seconds) given to
	// ShutdownStatsFlush during graceful shutdown — covers goroutine stop,
	// final DB flush, and fallback drain.  Must be less than the HTTP server
	// shutdown timeout (default 30 s) to ensure the DB flush completes before
	// the process exits.  <=0 falls back to DefaultLedgerShutdownTimeoutSec.
	ShutdownTimeoutSec int `json:"shutdown_timeout_sec"`
}

// Default values for the ledger pipeline. The getters below fall back to these on a
// non-positive (0/negative) configured value, so an out-of-range admin setting can
// never freeze the flush loop (e.g. OuterBatchSize=0 would otherwise loop forever).
const (
	// DefaultLedgerDedupRedisTTLSec is the default Redis dedup key TTL (seconds).
	DefaultLedgerDedupRedisTTLSec = 21600 // 6h

	DefaultLedgerOuterBatchSize             = 2000
	DefaultLedgerInnerBatchSize             = 500
	DefaultLedgerSettlementFlushMaxPerCycle = 15000
	DefaultLedgerCostFlushMaxPerCycle       = 0 // 0 = drain all (unbounded)
	DefaultLedgerBufMaxEntries              = 100000
	DefaultLedgerDedupMemMaxEntries         = 200000
	DefaultLedgerFlushDBTimeoutSec          = 30
	DefaultLedgerRetryFlushIntervalSec      = 2
	DefaultLedgerStatUpsertMaxRetries       = 10
	DefaultLedgerFallbackQueueCapacity      = 10000
	DefaultLedgerShutdownTimeoutSec         = 25
	DefaultLedgerRetryQueueMaxEntries       = 50000
)

// GetDedupRedisTTLSec returns the Redis dedup key TTL, falling back to default on non-positive.
func (s *LedgerPipelineSetting) GetDedupRedisTTLSec() int {
	if s.DedupRedisTTLSec <= 0 {
		return DefaultLedgerDedupRedisTTLSec
	}
	return s.DedupRedisTTLSec
}

// GetOuterBatchSize returns the error-recovery batch granularity, clamped to a
// positive value (0 would make the flush loop spin forever).
func (s *LedgerPipelineSetting) GetOuterBatchSize() int {
	if s.OuterBatchSize <= 0 {
		return DefaultLedgerOuterBatchSize
	}
	return s.OuterBatchSize
}

// GetCostOuterBatchSize returns the outer batch size for the cost-only flush path.
// Falls back to GetOuterBatchSize when CostOuterBatchSize is not set (0).
func (s *LedgerPipelineSetting) GetCostOuterBatchSize() int {
	if s.CostOuterBatchSize <= 0 {
		return s.GetOuterBatchSize()
	}
	return s.CostOuterBatchSize
}

// GetInnerBatchSize returns the rows-per-INSERT batch size, clamped to a positive value.
func (s *LedgerPipelineSetting) GetInnerBatchSize() int {
	if s.InnerBatchSize <= 0 {
		return DefaultLedgerInnerBatchSize
	}
	return s.InnerBatchSize
}

// GetSettlementFlushMaxPerCycle returns the per-cycle settlement dequeue cap, clamped to a
// positive value (ignored when FullDrain is true).
func (s *LedgerPipelineSetting) GetSettlementFlushMaxPerCycle() int {
	if s.SettlementFlushMaxPerCycle <= 0 {
		return DefaultLedgerSettlementFlushMaxPerCycle
	}
	return s.SettlementFlushMaxPerCycle
}

// GetCostFlushMaxPerCycle returns the per-cycle cost-only dequeue cap.
// 0 means unbounded (drain all); positive values cap the flush to avoid DB burst on recovery.
// Ignored when FullDrain is true.
func (s *LedgerPipelineSetting) GetCostFlushMaxPerCycle() int {
	if s.CostFlushMaxPerCycle < 0 {
		return DefaultLedgerCostFlushMaxPerCycle
	}
	return s.CostFlushMaxPerCycle
}

// GetBufMaxEntries returns the in-memory buffer ceiling per queue, clamped to a
// positive value (0 would drop every record on every append).
func (s *LedgerPipelineSetting) GetBufMaxEntries() int {
	if s.BufMaxEntries <= 0 {
		return DefaultLedgerBufMaxEntries
	}
	return s.BufMaxEntries
}

// GetDedupMemMaxEntries returns the in-memory dedup set ceiling, clamped to a positive value.
func (s *LedgerPipelineSetting) GetDedupMemMaxEntries() int {
	if s.DedupMemMaxEntries <= 0 {
		return DefaultLedgerDedupMemMaxEntries
	}
	return s.DedupMemMaxEntries
}

// GetFlushDBTimeout returns the per-flush DB write deadline, falling back to default
// on non-positive configured value.
func (s *LedgerPipelineSetting) GetFlushDBTimeout() time.Duration {
	if s.FlushDBTimeoutSec <= 0 {
		return DefaultLedgerFlushDBTimeoutSec * time.Second
	}
	return time.Duration(s.FlushDBTimeoutSec) * time.Second
}

// GetFallbackQueueCapacity returns the soft capacity limit of the fallback write queue.
func (s *LedgerPipelineSetting) GetFallbackQueueCapacity() int {
	if s.FallbackQueueCapacity <= 0 {
		return DefaultLedgerFallbackQueueCapacity
	}
	return s.FallbackQueueCapacity
}

// GetShutdownTimeout returns the graceful-shutdown budget for ShutdownStatsFlush,
// falling back to DefaultLedgerShutdownTimeoutSec on non-positive configured value.
func (s *LedgerPipelineSetting) GetShutdownTimeout() time.Duration {
	if s.ShutdownTimeoutSec <= 0 {
		return DefaultLedgerShutdownTimeoutSec * time.Second
	}
	return time.Duration(s.ShutdownTimeoutSec) * time.Second
}

var ledgerPipelineSetting = LedgerPipelineSetting{
	FlushIntervalSec:           8,
	OuterBatchSize:             DefaultLedgerOuterBatchSize,
	InnerBatchSize:             DefaultLedgerInnerBatchSize,
	SettlementFlushMaxPerCycle: DefaultLedgerSettlementFlushMaxPerCycle,
	CostFlushMaxPerCycle:       DefaultLedgerCostFlushMaxPerCycle,
	FullDrain:                  false,
	BufMaxEntries:              DefaultLedgerBufMaxEntries,
	DedupMemMaxEntries:         DefaultLedgerDedupMemMaxEntries,
	DedupUseRedis:              false,
	DedupRedisTTLSec:           DefaultLedgerDedupRedisTTLSec,
	FlushDBTimeoutSec:          DefaultLedgerFlushDBTimeoutSec,
	FallbackQueueCapacity:      DefaultLedgerFallbackQueueCapacity,
	ShutdownTimeoutSec:         DefaultLedgerShutdownTimeoutSec,
}

func init() {
	config.GlobalConfig.Register("ledger_pipeline_setting", &ledgerPipelineSetting)
}

func GetLedgerPipelineSetting() *LedgerPipelineSetting {
	return &ledgerPipelineSetting
}

// LedgerRetryQueueSetting controls the independent retry goroutine that re-flushes
// ledger records that failed in the main flush loop.
// Registered separately from LedgerPipelineSetting so it can be tuned independently.
type LedgerRetryQueueSetting struct {
	// RetryFlushIntervalSec is the wake-up interval (seconds) of the dedicated retry goroutine.
	// Should be ≤ LedgerPipelineSetting.FlushIntervalSec/4. <=0 falls back to default.
	RetryFlushIntervalSec int `json:"retry_flush_interval_sec"`
	// StatUpsertMaxRetries is the maximum number of times a failed stat-buffer delta is
	// retried before it is written to the fallback log file for manual backfill.
	StatUpsertMaxRetries int `json:"stat_upsert_max_retries"`
	// RetryQueueMaxEntries is the per-queue in-memory ceiling for costRetryQueue and
	// pairRetryQueue (each capped independently). Overflow entries are written to the
	// fallback file and the circuit-breaker failure counter is incremented.
	// <=0 falls back to DefaultLedgerRetryQueueMaxEntries.
	RetryQueueMaxEntries int `json:"retry_queue_max_entries"`
	// AllowConcurrentFlush controls whether the retry goroutine may write to DB
	// concurrently with the main flush loop.
	// false (default) = TryLock(businessStatsFlushMu) before each retry cycle — skips if
	//   main flush is in progress, eliminating connection-pool contention on shared tables.
	// true = run fully concurrently (higher retry throughput under sustained load, but retry
	//   and main flush compete for DB connections when both fire at the same time).
	AllowConcurrentFlush bool `json:"allow_concurrent_flush"`
}

// GetRetryFlushIntervalSec returns the retry goroutine wake-up interval, clamped to a positive value.
func (s *LedgerRetryQueueSetting) GetRetryFlushIntervalSec() int {
	if s.RetryFlushIntervalSec <= 0 {
		return DefaultLedgerRetryFlushIntervalSec
	}
	return s.RetryFlushIntervalSec
}

// GetStatUpsertMaxRetries returns the max retry count before writing to the fallback log.
func (s *LedgerRetryQueueSetting) GetStatUpsertMaxRetries() int {
	if s.StatUpsertMaxRetries <= 0 {
		return DefaultLedgerStatUpsertMaxRetries
	}
	return s.StatUpsertMaxRetries
}

// GetRetryQueueMaxEntries returns the per-retry-queue in-memory ceiling, clamped to a positive value.
func (s *LedgerRetryQueueSetting) GetRetryQueueMaxEntries() int {
	if s.RetryQueueMaxEntries <= 0 {
		return DefaultLedgerRetryQueueMaxEntries
	}
	return s.RetryQueueMaxEntries
}

var ledgerRetryQueueSetting = LedgerRetryQueueSetting{
	RetryFlushIntervalSec: DefaultLedgerRetryFlushIntervalSec,
	StatUpsertMaxRetries:  DefaultLedgerStatUpsertMaxRetries,
	RetryQueueMaxEntries:  DefaultLedgerRetryQueueMaxEntries,
	AllowConcurrentFlush:  false,
}

func init() {
	config.GlobalConfig.Register("ledger_retry_setting", &ledgerRetryQueueSetting)
}

func GetLedgerRetryQueueSetting() *LedgerRetryQueueSetting {
	return &ledgerRetryQueueSetting
}
