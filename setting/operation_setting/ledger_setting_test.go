package operation_setting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── ledger_detail_setting.go ──────────────────────────────────────────────

func TestGetLedgerDetailSetting_ReturnsGlobalPointer(t *testing.T) {
	got := GetLedgerDetailSetting()
	require.NotNil(t, got)
	assert.Same(t, &ledgerDetailSetting, got)
}

// Every ledger-detail getter follows the same "<=0 falls back to default" contract.
// Table drives all 12 through their zero / negative / one / custom cases.
func TestLedgerDetailSetting_IntGetters_Fallback(t *testing.T) {
	cases := []struct {
		name    string
		def     int
		get     func(s *LedgerDetailSetting) int
		set     func(s *LedgerDetailSetting, v int)
	}{
		{"ExportUserCooldownSec", DefaultLedgerDetailExportUserCooldownSec,
			func(s *LedgerDetailSetting) int { return s.GetExportUserCooldownSec() },
			func(s *LedgerDetailSetting, v int) { s.ExportUserCooldownSec = v }},
		{"ExportBatchSize", DefaultLedgerDetailExportBatchSize,
			func(s *LedgerDetailSetting) int { return s.GetExportBatchSize() },
			func(s *LedgerDetailSetting, v int) { s.ExportBatchSize = v }},
		{"ExportBatchSleepMs", DefaultLedgerDetailExportBatchSleepMs,
			func(s *LedgerDetailSetting) int { return s.GetExportBatchSleepMs() },
			func(s *LedgerDetailSetting, v int) { s.ExportBatchSleepMs = v }},
		{"ExportRowsPerFile", DefaultLedgerDetailExportRowsPerFile,
			func(s *LedgerDetailSetting) int { return s.GetExportRowsPerFile() },
			func(s *LedgerDetailSetting, v int) { s.ExportRowsPerFile = v }},
		{"ExportTimeoutSec", DefaultLedgerDetailExportTimeoutSec,
			func(s *LedgerDetailSetting) int { return s.GetExportTimeoutSec() },
			func(s *LedgerDetailSetting, v int) { s.ExportTimeoutSec = v }},
		{"ListDefaultLimit", DefaultLedgerDetailListDefaultLimit,
			func(s *LedgerDetailSetting) int { return s.GetListDefaultLimit() },
			func(s *LedgerDetailSetting, v int) { s.ListDefaultLimit = v }},
		{"ListMaxLimit", DefaultLedgerDetailListMaxLimit,
			func(s *LedgerDetailSetting) int { return s.GetListMaxLimit() },
			func(s *LedgerDetailSetting, v int) { s.ListMaxLimit = v }},
		{"ListScanBatchSize", DefaultLedgerDetailListScanBatchSize,
			func(s *LedgerDetailSetting) int { return s.GetListScanBatchSize() },
			func(s *LedgerDetailSetting, v int) { s.ListScanBatchSize = v }},
		{"ListScanRowsPerReq", DefaultLedgerDetailListScanRowsPerReq,
			func(s *LedgerDetailSetting) int { return s.GetListScanRowsPerReq() },
			func(s *LedgerDetailSetting, v int) { s.ListScanRowsPerReq = v }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s LedgerDetailSetting
			c.set(&s, 0)
			assert.Equal(t, c.def, c.get(&s), "zero falls back to default")
			c.set(&s, -3)
			assert.Equal(t, c.def, c.get(&s), "negative falls back to default")
			c.set(&s, 1)
			assert.Equal(t, 1, c.get(&s), "lower valid boundary")
			c.set(&s, 12345)
			assert.Equal(t, 12345, c.get(&s), "custom valid value")
		})
	}
}

// The three int64-returning range getters.
func TestLedgerDetailSetting_Int64Getters_Fallback(t *testing.T) {
	cases := []struct {
		name string
		def  int64
		get  func(s *LedgerDetailSetting) int64
		set  func(s *LedgerDetailSetting, v int)
	}{
		{"ExportMaxRangeSec", DefaultLedgerDetailExportMaxRangeSec,
			func(s *LedgerDetailSetting) int64 { return s.GetExportMaxRangeSec() },
			func(s *LedgerDetailSetting, v int) { s.ExportMaxRangeSec = v }},
		{"ListMaxRangeSec", DefaultLedgerDetailListMaxRangeSec,
			func(s *LedgerDetailSetting) int64 { return s.GetListMaxRangeSec() },
			func(s *LedgerDetailSetting, v int) { s.ListMaxRangeSec = v }},
		{"ListDefaultRangeSec", DefaultLedgerDetailListDefaultRangeSec,
			func(s *LedgerDetailSetting) int64 { return s.GetListDefaultRangeSec() },
			func(s *LedgerDetailSetting, v int) { s.ListDefaultRangeSec = v }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s LedgerDetailSetting
			c.set(&s, 0)
			assert.Equal(t, c.def, c.get(&s))
			c.set(&s, -1)
			assert.Equal(t, c.def, c.get(&s))
			c.set(&s, 7200)
			assert.Equal(t, int64(7200), c.get(&s))
		})
	}
}

// ── ledger_setting.go: LedgerPipelineSetting ──────────────────────────────

func TestGetLedgerPipelineSetting_ReturnsGlobalPointer(t *testing.T) {
	got := GetLedgerPipelineSetting()
	require.NotNil(t, got)
	assert.Same(t, &ledgerPipelineSetting, got)
}

func TestLedgerPipeline_SimpleFallbackGetters(t *testing.T) {
	cases := []struct {
		name string
		def  int
		get  func(s *LedgerPipelineSetting) int
		set  func(s *LedgerPipelineSetting, v int)
	}{
		{"DedupRedisTTLSec", DefaultLedgerDedupRedisTTLSec,
			func(s *LedgerPipelineSetting) int { return s.GetDedupRedisTTLSec() },
			func(s *LedgerPipelineSetting, v int) { s.DedupRedisTTLSec = v }},
		{"OuterBatchSize", DefaultLedgerOuterBatchSize,
			func(s *LedgerPipelineSetting) int { return s.GetOuterBatchSize() },
			func(s *LedgerPipelineSetting, v int) { s.OuterBatchSize = v }},
		{"InnerBatchSize", DefaultLedgerInnerBatchSize,
			func(s *LedgerPipelineSetting) int { return s.GetInnerBatchSize() },
			func(s *LedgerPipelineSetting, v int) { s.InnerBatchSize = v }},
		{"SettlementFlushMaxPerCycle", DefaultLedgerSettlementFlushMaxPerCycle,
			func(s *LedgerPipelineSetting) int { return s.GetSettlementFlushMaxPerCycle() },
			func(s *LedgerPipelineSetting, v int) { s.SettlementFlushMaxPerCycle = v }},
		{"BufMaxEntries", DefaultLedgerBufMaxEntries,
			func(s *LedgerPipelineSetting) int { return s.GetBufMaxEntries() },
			func(s *LedgerPipelineSetting, v int) { s.BufMaxEntries = v }},
		{"DedupMemMaxEntries", DefaultLedgerDedupMemMaxEntries,
			func(s *LedgerPipelineSetting) int { return s.GetDedupMemMaxEntries() },
			func(s *LedgerPipelineSetting, v int) { s.DedupMemMaxEntries = v }},
		{"FallbackQueueCapacity", DefaultLedgerFallbackQueueCapacity,
			func(s *LedgerPipelineSetting) int { return s.GetFallbackQueueCapacity() },
			func(s *LedgerPipelineSetting, v int) { s.FallbackQueueCapacity = v }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s LedgerPipelineSetting
			c.set(&s, 0)
			assert.Equal(t, c.def, c.get(&s))
			c.set(&s, -9)
			assert.Equal(t, c.def, c.get(&s))
			c.set(&s, 1)
			assert.Equal(t, 1, c.get(&s))
			c.set(&s, 777)
			assert.Equal(t, 777, c.get(&s))
		})
	}
}

// GetCostOuterBatchSize: inherits GetOuterBatchSize when unset (0/negative).
func TestGetCostOuterBatchSize_InheritanceMatrix(t *testing.T) {
	t.Run("both-unset-uses-default", func(t *testing.T) {
		s := LedgerPipelineSetting{CostOuterBatchSize: 0, OuterBatchSize: 0}
		assert.Equal(t, DefaultLedgerOuterBatchSize, s.GetCostOuterBatchSize())
	})
	t.Run("cost-unset-inherits-outer", func(t *testing.T) {
		s := LedgerPipelineSetting{CostOuterBatchSize: 0, OuterBatchSize: 555}
		assert.Equal(t, 555, s.GetCostOuterBatchSize())
	})
	t.Run("cost-negative-inherits-outer", func(t *testing.T) {
		s := LedgerPipelineSetting{CostOuterBatchSize: -1, OuterBatchSize: 321}
		assert.Equal(t, 321, s.GetCostOuterBatchSize())
	})
	t.Run("cost-set-wins", func(t *testing.T) {
		s := LedgerPipelineSetting{CostOuterBatchSize: 42, OuterBatchSize: 999}
		assert.Equal(t, 42, s.GetCostOuterBatchSize())
	})
}

// GetCostFlushMaxPerCycle: 0 = unbounded (kept), negative → default(0), positive kept.
func TestGetCostFlushMaxPerCycle(t *testing.T) {
	t.Run("zero-unbounded-kept", func(t *testing.T) {
		s := LedgerPipelineSetting{CostFlushMaxPerCycle: 0}
		assert.Equal(t, 0, s.GetCostFlushMaxPerCycle())
	})
	t.Run("negative-falls-back-to-default-zero", func(t *testing.T) {
		s := LedgerPipelineSetting{CostFlushMaxPerCycle: -5}
		assert.Equal(t, DefaultLedgerCostFlushMaxPerCycle, s.GetCostFlushMaxPerCycle())
		assert.Equal(t, 0, s.GetCostFlushMaxPerCycle())
	})
	t.Run("positive-kept", func(t *testing.T) {
		s := LedgerPipelineSetting{CostFlushMaxPerCycle: 8000}
		assert.Equal(t, 8000, s.GetCostFlushMaxPerCycle())
	})
}

// Duration getters.
func TestGetFlushDBTimeout(t *testing.T) {
	assert.Equal(t, DefaultLedgerFlushDBTimeoutSec*time.Second,
		(&LedgerPipelineSetting{FlushDBTimeoutSec: 0}).GetFlushDBTimeout())
	assert.Equal(t, DefaultLedgerFlushDBTimeoutSec*time.Second,
		(&LedgerPipelineSetting{FlushDBTimeoutSec: -1}).GetFlushDBTimeout())
	assert.Equal(t, 45*time.Second,
		(&LedgerPipelineSetting{FlushDBTimeoutSec: 45}).GetFlushDBTimeout())
}

func TestGetShutdownTimeout(t *testing.T) {
	assert.Equal(t, DefaultLedgerShutdownTimeoutSec*time.Second,
		(&LedgerPipelineSetting{ShutdownTimeoutSec: 0}).GetShutdownTimeout())
	assert.Equal(t, DefaultLedgerShutdownTimeoutSec*time.Second,
		(&LedgerPipelineSetting{ShutdownTimeoutSec: -1}).GetShutdownTimeout())
	assert.Equal(t, 10*time.Second,
		(&LedgerPipelineSetting{ShutdownTimeoutSec: 10}).GetShutdownTimeout())
}

// GetCacheTTLSec: valid element, negative idx, out-of-range idx, non-positive element.
func TestGetCacheTTLSec(t *testing.T) {
	s := LedgerPipelineSetting{CacheTTLSecs: []int{120, 0, -5}}

	assert.Equal(t, 120, s.GetCacheTTLSec(0), "positive element returned")
	assert.Equal(t, DefaultLedgerCacheTTLSec, s.GetCacheTTLSec(1), "zero element falls back")
	assert.Equal(t, DefaultLedgerCacheTTLSec, s.GetCacheTTLSec(2), "negative element falls back")
	assert.Equal(t, DefaultLedgerCacheTTLSec, s.GetCacheTTLSec(3), "idx past slice length falls back")
	assert.Equal(t, DefaultLedgerCacheTTLSec, s.GetCacheTTLSec(-1), "negative idx falls back")

	// Empty slice always falls back.
	empty := LedgerPipelineSetting{}
	assert.Equal(t, DefaultLedgerCacheTTLSec, empty.GetCacheTTLSec(CacheIdxChannelCostRatio))
}

// GetCacheTTLJitterPercent: clamped to [0,100].
func TestGetCacheTTLJitterPercent(t *testing.T) {
	assert.Equal(t, DefaultLedgerCacheTTLJitterPercent,
		(&LedgerPipelineSetting{CacheTTLJitterPercent: -1}).GetCacheTTLJitterPercent())
	assert.Equal(t, DefaultLedgerCacheTTLJitterPercent,
		(&LedgerPipelineSetting{CacheTTLJitterPercent: 101}).GetCacheTTLJitterPercent())
	assert.Equal(t, 0, (&LedgerPipelineSetting{CacheTTLJitterPercent: 0}).GetCacheTTLJitterPercent(), "0 is valid")
	assert.Equal(t, 100, (&LedgerPipelineSetting{CacheTTLJitterPercent: 100}).GetCacheTTLJitterPercent(), "100 is valid")
	assert.Equal(t, 33, (&LedgerPipelineSetting{CacheTTLJitterPercent: 33}).GetCacheTTLJitterPercent())
}

// GetJitteredCacheTTL: jp==0 returns exact base; jp>0 stays within band and never < 1s.
func TestGetJitteredCacheTTL(t *testing.T) {
	t.Run("zero-jitter-returns-exact-base", func(t *testing.T) {
		s := LedgerPipelineSetting{CacheTTLSecs: []int{300}, CacheTTLJitterPercent: 0}
		assert.Equal(t, 300*time.Second, s.GetJitteredCacheTTL(0))
	})

	t.Run("jitter-within-band", func(t *testing.T) {
		base := 300
		jp := 20
		s := LedgerPipelineSetting{CacheTTLSecs: []int{base}, CacheTTLJitterPercent: jp}
		span := float64(base) * float64(jp) / 100.0 // ±60s
		low := time.Duration((float64(base)-span)*float64(time.Second)) - time.Second
		high := time.Duration((float64(base)+span)*float64(time.Second)) + time.Second
		for i := 0; i < 500; i++ {
			ttl := s.GetJitteredCacheTTL(0)
			assert.GreaterOrEqual(t, ttl, low)
			assert.LessOrEqual(t, ttl, high)
			assert.GreaterOrEqual(t, ttl, time.Second, "never below 1s")
		}
	})

	t.Run("floor-at-one-second", func(t *testing.T) {
		// base=1s, jitter=100% → raw ttl spans [0,2]s; the <1 floor must clamp to >=1s.
		s := LedgerPipelineSetting{CacheTTLSecs: []int{1}, CacheTTLJitterPercent: 100}
		for i := 0; i < 2000; i++ {
			assert.GreaterOrEqual(t, s.GetJitteredCacheTTL(0), time.Second)
		}
	})
}

// ── ledger_setting.go: LedgerRetryQueueSetting ────────────────────────────

func TestGetLedgerRetryQueueSetting_ReturnsGlobalPointer(t *testing.T) {
	got := GetLedgerRetryQueueSetting()
	require.NotNil(t, got)
	assert.Same(t, &ledgerRetryQueueSetting, got)
}

func TestLedgerRetryQueue_FallbackGetters(t *testing.T) {
	cases := []struct {
		name string
		def  int
		get  func(s *LedgerRetryQueueSetting) int
		set  func(s *LedgerRetryQueueSetting, v int)
	}{
		{"RetryFlushIntervalSec", DefaultLedgerRetryFlushIntervalSec,
			func(s *LedgerRetryQueueSetting) int { return s.GetRetryFlushIntervalSec() },
			func(s *LedgerRetryQueueSetting, v int) { s.RetryFlushIntervalSec = v }},
		{"StatUpsertMaxRetries", DefaultLedgerStatUpsertMaxRetries,
			func(s *LedgerRetryQueueSetting) int { return s.GetStatUpsertMaxRetries() },
			func(s *LedgerRetryQueueSetting, v int) { s.StatUpsertMaxRetries = v }},
		{"RetryQueueMaxEntries", DefaultLedgerRetryQueueMaxEntries,
			func(s *LedgerRetryQueueSetting) int { return s.GetRetryQueueMaxEntries() },
			func(s *LedgerRetryQueueSetting, v int) { s.RetryQueueMaxEntries = v }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s LedgerRetryQueueSetting
			c.set(&s, 0)
			assert.Equal(t, c.def, c.get(&s))
			c.set(&s, -2)
			assert.Equal(t, c.def, c.get(&s))
			c.set(&s, 1)
			assert.Equal(t, 1, c.get(&s))
			c.set(&s, 99)
			assert.Equal(t, 99, c.get(&s))
		})
	}
}

func TestLedgerCacheIndices_AreDistinctAndCounted(t *testing.T) {
	// Guards the iota block against accidental reordering.
	assert.Equal(t, 0, CacheIdxChannelCostRatio)
	assert.Equal(t, 4, CacheIdxTierDefinitions)
	assert.Equal(t, 5, LedgerCacheTTLCount)
}
