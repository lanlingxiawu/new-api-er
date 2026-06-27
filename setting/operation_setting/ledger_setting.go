package operation_setting

import (
	"time"

	"github.com/QuantumNous/new-api/setting/config"
)

// LedgerPipelineSetting controls the in-memory ledger pipeline that batches
// cost/commission records before writing to DB.
type LedgerPipelineSetting struct {
	// FlushIntervalSec is seconds between each flush cycle.
	// Throughput = PairedFlushMaxPerCycle × 60 ÷ FlushIntervalSec per minute.
	FlushIntervalSec int `json:"flush_interval_sec"`
	// OuterBatchSize is records pulled per outer loop iteration (error-recovery granularity).
	OuterBatchSize int `json:"outer_batch_size"`
	// InnerBatchSize is rows per SQL INSERT statement. Larger = fewer round-trips.
	InnerBatchSize int `json:"inner_batch_size"`
	// PairedFlushMaxPerCycle is max paired cost+commission records dequeued per flush cycle.
	// Ignored when FullDrain is true.
	PairedFlushMaxPerCycle int `json:"paired_flush_max_per_cycle"`
	// FullDrain drains the entire buffer each cycle, ignoring PairedFlushMaxPerCycle.
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
}

// Default values for the ledger pipeline. The getters below fall back to these on a
// non-positive (0/negative) configured value, so an out-of-range admin setting can
// never freeze the flush loop (e.g. OuterBatchSize=0 would otherwise loop forever).
const (
	// DefaultLedgerDedupRedisTTLSec is the default Redis dedup key TTL (seconds).
	DefaultLedgerDedupRedisTTLSec = 21600 // 6h

	DefaultLedgerOuterBatchSize         = 2000
	DefaultLedgerInnerBatchSize         = 500
	DefaultLedgerPairedFlushMaxPerCycle = 15000
	DefaultLedgerBufMaxEntries          = 100000
	DefaultLedgerDedupMemMaxEntries     = 200000
	DefaultLedgerFlushDBTimeoutSec      = 30
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

// GetInnerBatchSize returns the rows-per-INSERT batch size, clamped to a positive value.
func (s *LedgerPipelineSetting) GetInnerBatchSize() int {
	if s.InnerBatchSize <= 0 {
		return DefaultLedgerInnerBatchSize
	}
	return s.InnerBatchSize
}

// GetPairedFlushMaxPerCycle returns the per-cycle paired dequeue cap, clamped to a
// positive value (ignored when FullDrain is true).
func (s *LedgerPipelineSetting) GetPairedFlushMaxPerCycle() int {
	if s.PairedFlushMaxPerCycle <= 0 {
		return DefaultLedgerPairedFlushMaxPerCycle
	}
	return s.PairedFlushMaxPerCycle
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

var ledgerPipelineSetting = LedgerPipelineSetting{
	FlushIntervalSec:       8,
	OuterBatchSize:         DefaultLedgerOuterBatchSize,
	InnerBatchSize:         DefaultLedgerInnerBatchSize,
	PairedFlushMaxPerCycle: DefaultLedgerPairedFlushMaxPerCycle,
	FullDrain:              false,
	BufMaxEntries:          DefaultLedgerBufMaxEntries,
	DedupMemMaxEntries:     DefaultLedgerDedupMemMaxEntries,
	DedupUseRedis:          false,
	DedupRedisTTLSec:       DefaultLedgerDedupRedisTTLSec,
	FlushDBTimeoutSec:      DefaultLedgerFlushDBTimeoutSec,
}

func init() {
	config.GlobalConfig.Register("ledger_pipeline_setting", &ledgerPipelineSetting)
}

func GetLedgerPipelineSetting() *LedgerPipelineSetting {
	return &ledgerPipelineSetting
}
