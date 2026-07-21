package model

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// Shared test helpers for the business-stats batch (fallback queue + settings).
// Defined once here; visible to every *_test.go in package model.
// ===========================================================================

var (
	bsFallbackOnce sync.Once
	bsFallbackDir  string
)

// bsInitFallback lazily creates ONE persistent fallback write goroutine pointed at
// a temp dir. It MUST be initialised exactly once per process: multiple
// InitFallbackQueue calls would leave several goroutines competing for the same
// global queue channel and racing on the close handshake. All fallback-dependent
// tests call this. The temp dir is left on disk (OS temp) — acceptable for tests.
func bsInitFallback(t *testing.T) string {
	t.Helper()
	bsFallbackOnce.Do(func() {
		dir, err := os.MkdirTemp("", "bstats_fallback")
		require.NoError(t, err)
		bsFallbackDir = dir
		d := dir
		common.LogDir = &d
		InitFallbackQueue(1 << 20) // large capacity so writes never spuriously drop
	})
	return bsFallbackDir
}

// bsWithPipelineSetting mutates the global LedgerPipelineSetting for the duration
// of a test and restores it afterwards.
func bsWithPipelineSetting(t *testing.T, mut func(s *operation_setting.LedgerPipelineSetting)) {
	t.Helper()
	s := operation_setting.GetLedgerPipelineSetting()
	saved := *s
	mut(s)
	t.Cleanup(func() { *s = saved })
}

// bsWithRetrySetting mutates the global LedgerRetryQueueSetting for a test.
func bsWithRetrySetting(t *testing.T, mut func(s *operation_setting.LedgerRetryQueueSetting)) {
	t.Helper()
	s := operation_setting.GetLedgerRetryQueueSetting()
	saved := *s
	mut(s)
	t.Cleanup(func() { *s = saved })
}

// bsWithCircuitSetting mutates the global circuit-breaker setting for a test.
func bsWithCircuitSetting(t *testing.T, mut func(s *operation_setting.BusinessStatsCircuitBreakerSetting)) {
	t.Helper()
	s := operation_setting.GetBusinessStatsCircuitBreakerSetting()
	saved := *s
	mut(s)
	t.Cleanup(func() { *s = saved })
}

// bsWithFallbackBackfillSetting mutates the global backfill setting for a test.
func bsWithFallbackBackfillSetting(t *testing.T, mut func(s *operation_setting.BusinessStatsFallbackBackfillSetting)) {
	t.Helper()
	s := operation_setting.GetBusinessStatsFallbackBackfillSetting()
	saved := *s
	mut(s)
	t.Cleanup(func() { *s = saved })
}

// bsWithCurrentDate overrides currentDateFunc (used to fix the fallback file name)
// for the duration of a test.
func bsWithCurrentDate(t *testing.T, date string) {
	t.Helper()
	saved := currentDateFunc
	currentDateFunc = func() string { return date }
	t.Cleanup(func() { currentDateFunc = saved })
}

// bsReadFallbackFile flushes the queue and reads the named fallback/dead-letter file.
func bsReadFallbackFile(t *testing.T, dir, basename, date string) string {
	t.Helper()
	FlushAndCloseFallbackFiles()
	path := filepath.Join(dir, fallbackFilename(basename, date))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// ===========================================================================
// business_stats_dead_letter.go
// ===========================================================================

func TestFallbackFilename(t *testing.T) {
	assert.Equal(t, "business_stats_fallback.2026-06-20.log", fallbackFilename("business_stats_fallback", "2026-06-20"))
	assert.Equal(t, "business_stats_fallback.2026-06-20.log", FallbackFilename("2026-06-20"))
	assert.Equal(t, businessStatsFallbackFile, FallbackFileBasename)
}

func TestCurrentDateFunc(t *testing.T) {
	bsWithCurrentDate(t, "2099-12-31")
	assert.Equal(t, "2099-12-31", currentDate())
}

func TestResolveFallbackDir(t *testing.T) {
	dir := bsInitFallback(t)

	// default: shares the app log dir
	bsWithFallbackBackfillSetting(t, func(s *operation_setting.BusinessStatsFallbackBackfillSetting) {
		s.UseSeparateFallbackDir = false
	})
	assert.Equal(t, dir, resolveFallbackDir())
	assert.Equal(t, dir, FallbackLogDir())

	// separate: nested "fallback/" subdirectory
	bsWithFallbackBackfillSetting(t, func(s *operation_setting.BusinessStatsFallbackBackfillSetting) {
		s.UseSeparateFallbackDir = true
	})
	assert.Equal(t, filepath.Join(dir, fallbackSubDir), resolveFallbackDir())
}

func TestWriteBusinessStatsFallback_RoundTrip(t *testing.T) {
	dir := bsInitFallback(t)
	bsWithFallbackBackfillSetting(t, func(s *operation_setting.BusinessStatsFallbackBackfillSetting) {
		s.UseSeparateFallbackDir = false
	})
	date := uniq("2099-01")
	bsWithCurrentDate(t, date)

	ok := writeBusinessStatsFallback("business_stats_skipped", "unit_reason", map[string]any{"n": 7})
	require.True(t, ok)

	content := bsReadFallbackFile(t, dir, businessStatsFallbackFile, date)
	assert.Contains(t, content, `"kind":"business_stats_skipped"`)
	assert.Contains(t, content, `"reason":"unit_reason"`)
	assert.Contains(t, content, `"payload"`)
}

func TestWriteBusinessStatsDeadLetter_RoundTrip(t *testing.T) {
	dir := bsInitFallback(t)
	bsWithFallbackBackfillSetting(t, func(s *operation_setting.BusinessStatsFallbackBackfillSetting) {
		s.UseSeparateFallbackDir = false
	})
	date := uniq("2099-02")
	bsWithCurrentDate(t, date)

	writeBusinessStatsDeadLetter("parse_error", "bad_line", 3, "rawpayload")
	WriteBackfillDeadLetter([]byte("garbage-json-line"), "unmarshal failed")

	content := bsReadFallbackFile(t, dir, businessStatsDeadLetterFile, date)
	assert.Contains(t, content, `"kind":"parse_error"`)
	assert.Contains(t, content, `"reason":"bad_line"`)
	assert.Contains(t, content, `"attempts":3`)
	assert.Contains(t, content, "garbage-json-line")
}

func TestWriteStatFallbackHelpers(t *testing.T) {
	dir := bsInitFallback(t)
	bsWithFallbackBackfillSetting(t, func(s *operation_setting.BusinessStatsFallbackBackfillSetting) {
		s.UseSeparateFallbackDir = false
	})
	date := uniq("2099-03")
	bsWithCurrentDate(t, date)

	assert.True(t, writeStatPlatformFallback("r1", &platformStatDelta{StatDate: 1, ChannelId: 2, RevenueQuota: 10}))
	assert.True(t, writeStatCommissionFallback("r2", &commissionStatDelta{StatDate: 1, EmployeeUserId: 3, CommissionQuota: 5}))
	assert.True(t, writeStatCustomerCommissionFallback("r3", &customerCommissionStatDelta{StatDate: 1, EmployeeUserId: 3, CustomerUserId: 4}))
	assert.True(t, writeStatResetDailyFallback("r4", &commissionResetPeriodDailyDelta{ResetStartedAt: 9, StatDate: 1, EmployeeUserId: 3}))

	content := bsReadFallbackFile(t, dir, businessStatsFallbackFile, date)
	assert.Contains(t, content, `"kind":"stat_platform"`)
	assert.Contains(t, content, `"kind":"stat_commission"`)
	assert.Contains(t, content, `"kind":"stat_customer_commission"`)
	assert.Contains(t, content, `"kind":"stat_reset_daily"`)
}

func TestWriteBusinessStatsJSONLine_QueueUnavailableReturnsFalse(t *testing.T) {
	// When the queue is nil (uninitialised / full), the write must fail closed.
	saved := fallbackQueue
	fallbackQueue = nil
	t.Cleanup(func() { fallbackQueue = saved })

	assert.False(t, writeBusinessStatsFallback("k", "r", nil))
	assert.False(t, writeStatPlatformFallback("r", &platformStatDelta{}))
}

func TestWriteBusinessStatsJSONLine_DateCapturedAtEnqueue(t *testing.T) {
	dir := bsInitFallback(t)
	bsWithFallbackBackfillSetting(t, func(s *operation_setting.BusinessStatsFallbackBackfillSetting) {
		s.UseSeparateFallbackDir = false
	})
	date := uniq("2099-04")
	// set the date, enqueue, then change the date before the goroutine writes:
	// the entry must still land in the file for the enqueue-time date.
	saved := currentDateFunc
	currentDateFunc = func() string { return date }
	require.True(t, writeBusinessStatsFallback("enqueue_date", "captured", nil))
	currentDateFunc = func() string { return "2099-99-changed" }
	t.Cleanup(func() { currentDateFunc = saved })

	content := bsReadFallbackFile(t, dir, businessStatsFallbackFile, date)
	assert.Contains(t, content, `"kind":"enqueue_date"`)

	// the "changed" date file must not exist
	_, err := os.Stat(filepath.Join(dir, fallbackFilename(businessStatsFallbackFile, "2099-99-changed")))
	assert.True(t, os.IsNotExist(err))
}

func TestBusinessDayStartAndDate(t *testing.T) {
	const ts = int64(1_700_000_000)
	assert.Equal(t, localDayStart(ts), BusinessDayStart(ts))
	// BusinessDayDate formats a valid yyyy-mm-dd string
	d := BusinessDayDate(ts)
	assert.Len(t, d, 10)
	assert.Equal(t, 2, strings.Count(d, "-"))
}
