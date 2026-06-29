package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// BusinessStatsFallbackBackfillSetting controls the fallback-log backfill feature:
// how the admin-triggered task reads the fallback file, batches records, and flushes
// them into the ledger tables.
type BusinessStatsFallbackBackfillSetting struct {
	// Enabled gates the entire feature. When false, the fallback_hint field in ledger
	// query responses is always null, status endpoint returns has_file=false, and the
	// backfill trigger endpoint returns an error.
	Enabled bool `json:"enabled"`

	// UseSeparateFallbackDir switches fallback files to a dedicated subdirectory
	// ("fallback/") inside the app log directory, separating them from regular logs.
	// When false (default), fallback files share the app log directory (--log-dir).
	UseSeparateFallbackDir bool `json:"use_separate_fallback_dir"`

	// StatusCacheSeconds is the TTL (seconds) for the per-date fallback file stat
	// result cached in process memory. Keeps frequent ledger polls from hitting the FS.
	StatusCacheSeconds int `json:"status_cache_seconds"`

	// MaxReadLineBytes is the maximum byte length of a single JSON log line that the
	// scanner will read. Lines exceeding this limit are discarded.
	MaxReadLineBytes int `json:"max_read_line_bytes"`

	// WriteBatchSize is the threshold at which the accumulated in-memory batch is
	// flushed to the DB. The flush also triggers when FlushIntervalSec elapses.
	// This also serves as the effective in-memory buffer cap: the task flushes
	// as soon as the buffer reaches this size.
	WriteBatchSize int `json:"write_batch_size"`

	// FlushIntervalSec is the minimum seconds between consecutive flushes during the
	// backfill task. This is not a background ticker; it is evaluated inside the task
	// loop on each read iteration.
	FlushIntervalSec int `json:"flush_interval_sec"`

	// BatchSleepMs is the milliseconds the task goroutine sleeps after each flush to
	// yield CPU and reduce instantaneous DB write pressure.
	BatchSleepMs int `json:"batch_sleep_ms"`
}

var businessStatsFallbackBackfillSetting = BusinessStatsFallbackBackfillSetting{
	Enabled:            true,
	StatusCacheSeconds: 15,
	MaxReadLineBytes:   1048576, // 1 MiB
	WriteBatchSize:     200,
	FlushIntervalSec:   5,
	BatchSleepMs:       200,
}

func init() {
	config.GlobalConfig.Register("business_stats_fallback_backfill_setting", &businessStatsFallbackBackfillSetting)
}

func GetBusinessStatsFallbackBackfillSetting() *BusinessStatsFallbackBackfillSetting {
	return &businessStatsFallbackBackfillSetting
}
