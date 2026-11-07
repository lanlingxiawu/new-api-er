package model

import (
	"compress/gzip"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/go-sql-driver/mysql"
	"github.com/shirou/gopsutil/process"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type repriceLoadResult struct {
	Name                string  `json:"name"`
	DefaultThrottle     bool    `json:"default_throttle"`
	Rows                int     `json:"rows"`
	Seconds             float64 `json:"seconds"`
	RowsPerSecond       float64 `json:"rows_per_second"`
	CPUSeconds          float64 `json:"cpu_seconds"`
	AverageCPUCores     float64 `json:"average_cpu_cores"`
	HeapStartMiB        float64 `json:"heap_start_mib"`
	HeapPeakMiB         float64 `json:"heap_peak_mib"`
	RSSStartMiB         float64 `json:"rss_start_mib"`
	RSSPeakMiB          float64 `json:"rss_peak_mib"`
	AllocatedMiB        float64 `json:"allocated_mib"`
	GCs                 uint32  `json:"gcs"`
	Queries             int64   `json:"queries"`
	PoolPeak            int     `json:"pool_peak"`
	PoolWaits           int64   `json:"pool_waits"`
	ThrottleSeconds     float64 `json:"throttle_seconds"`
	Parts               int     `json:"parts"`
	Bytes               int64   `json:"bytes"`
	VerifiedDetailRows  int     `json:"verified_detail_rows"`
	VerifiedSummaryRows int     `json:"verified_summary_rows"`
}

// The oracle uses small exact integer fractions independently of the decimal
// implementation. Zero/missing original ratios deliberately keep the charge.
func repriceLoadQuota(n int, override bool) int64 {
	q := int64(1000 + n%997)
	if !override {
		return q
	}
	numerators := [...]int64{3, 3, 1, 1, 1, 15, 1500000, 3}
	denominators := [...]int64{4, 8, 1, 1, 1, 8, 333333, 5}
	i := n % 8
	return (2*q*numerators[i] + denominators[i]) / (2 * denominators[i])
}

func repriceLoadRatios(n int, override bool) (string, string) {
	ratios := [...]string{"2", "4", "0", "", "1.5", "0.8", "0.333333", "2.5"}
	group := ratios[n%8]
	special := ""
	if group != "" && n%3 == 0 {
		special = group
	}
	if override && group != "" && group != "0" {
		group = "1.5"
		if special != "" {
			special = "1.5"
		}
	}
	return group, special
}

// This test never runs in the ordinary suite and refuses shared/project DBs.
// Example: EXPORT_REPRICE_LOAD=1 LOG_EXPORT_TEST_DSN=<.../reprice_export_load>
func TestLogExportRepriceLargeData(t *testing.T) {
	if os.Getenv("EXPORT_REPRICE_LOAD") != "1" {
		t.Skip("opt-in million-row export test")
	}
	exportDir := t.TempDir()
	t.Setenv("TMP", exportDir)
	t.Setenv("TEMP", exportDir)
	require.Nil(t, DB, "run with SQL_DSN unset; the load test must not use a project DB")
	require.Nil(t, LOG_DB, "run with LOG_SQL_DSN unset")
	dsn := os.Getenv("LOG_EXPORT_TEST_DSN")
	database := ""
	if strings.HasPrefix(dsn, "postgres") {
		u, err := url.Parse(dsn)
		require.NoError(t, err)
		database = strings.TrimPrefix(u.Path, "/")
	} else {
		cfg, err := mysql.ParseDSN(dsn)
		require.NoError(t, err)
		database = cfg.DBName
	}
	require.Equal(t, "reprice_export_load", database, "only a disposable database with this exact name is allowed")
	repriceTestLogDB(t)
	useUserCacheMiniRedis(t)
	LOG_DB = LOG_DB.Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	conn, err := LOG_DB.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(8)
	conn.SetMaxIdleConns(8)
	const total = 1_000_000
	const base = int64(1790812800) // fixed time range; no dependence on the wall clock
	username := uniq("reprice_load")
	userID := nextTestID()
	if reuse := os.Getenv("EXPORT_REPRICE_LOAD_USER"); reuse != "" {
		username = reuse
		var count int64
		require.NoError(t, LOG_DB.Model(&Log{}).Where("username = ?", username).Count(&count).Error)
		require.EqualValues(t, total, count, "only a complete existing synthetic dataset can be reused")
		var first Log
		require.NoError(t, LOG_DB.Where("username = ?", username).Order("created_at,id").First(&first).Error)
		require.Equal(t, "load-00000000", first.RequestId)
		require.Equal(t, base, first.CreatedAt)
		userID = first.UserId
	}
	t.Cleanup(func() { require.NoError(t, LOG_DB.Where("username = ?", username).Delete(&Log{}).Error) })
	started := time.Now()
	for start := 0; os.Getenv("EXPORT_REPRICE_LOAD_USER") == "" && start < total; start += 1000 {
		rows := make([]Log, 1000)
		for i := range rows {
			n := start + i
			group, special := repriceLoadRatios(n, false)
			other := `{"usage_source":"upstream"`
			if group != "" {
				other += `,"group_ratio":` + group
			}
			if special != "" {
				other += `,"user_group_ratio":` + special
			}
			padBytes := 192
			if n%5 == 0 {
				padBytes = 4096
			}
			word := strconv.FormatUint(uint64(n+1)*2654435761, 16) + ":"
			pad := strings.Repeat(word, padBytes/len(word)+1)[:padBytes]
			other += `,"diagnostic":"` + pad + `"}`
			rows[i] = Log{UserId: userID, Username: username, CreatedAt: base + int64(n/16), Type: LogTypeConsume,
				RequestId: fmt.Sprintf("load-%08d", n), ModelName: fmt.Sprintf("model-%02d", n%31),
				Quota: int(repriceLoadQuota(n, false)), PromptTokens: 100 + n%400, CompletionTokens: 20 + n%100,
				Other: other, Group: "load", TokenName: "load"}
		}
		require.NoError(t, LOG_DB.Create(&rows).Error)
		if (start+1000)%100000 == 0 {
			t.Logf("SEED rows=%d seconds=%.2f", start+1000, time.Since(started).Seconds())
		}
	}
	cfg := operation_setting.GetLogExportSetting()
	previous := *cfg
	t.Cleanup(func() { *cfg = previous })
	cfg.BatchSize = 3000
	cfg.BatchSleepMs = 100
	cfg.MaxRowsPerSec = 20000
	cfg.GzipLevel = 1
	cfg.RowsPerFile = 200000
	cfg.MaxParts = 100
	cfg.WindowSec = 3600
	cfg.TimeoutSec = 900
	cfg.MinFreeDiskMB = 256
	cfg.OffpeakOnly = false
	// No production CPU sampler runs in TestMain; fixed throttling stays active.
	proc, err := process.NewProcess(int32(os.Getpid()))
	require.NoError(t, err)
	var queries atomic.Int64
	callback := "test:reprice_load_query_count"
	require.NoError(t, LOG_DB.Callback().Query().After("gorm:query").Register(callback, func(*gorm.DB) { queries.Add(1) }))
	t.Cleanup(func() { require.NoError(t, LOG_DB.Callback().Query().Remove(callback)) })
	var results []repriceLoadResult
	for _, scenario := range []struct {
		name, mode string
		rows       int
		override   bool
		defaults   bool
	}{
		{"override_100k", LogExportModeDetail, 100000, true, true},
		{"detail_baseline", LogExportModeDetail, total, false, false},
		{"detail_override", LogExportModeDetail, total, true, false},
		{"summary_baseline", LogExportModeSummary, total, false, false},
		{"summary_override", LogExportModeSummary, total, true, false},
		{"both_override", LogExportModeBoth, total, true, false},
		{"default_override", LogExportModeDetail, total, true, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			cfg.BatchSleepMs, cfg.MaxRowsPerSec = 0, operation_setting.MaxLogExportMaxRowsPerSec
			if scenario.defaults {
				cfg.BatchSleepMs, cfg.MaxRowsPerSec = 100, 20000
			}
			job := newExportJobFor(username, base, base+int64((scenario.rows-1)/16), []string{
				"request_id", "quota", "cost_usd", "group_ratio", "user_group_ratio", "model_name", "prompt_tokens", "completion_tokens"})
			job.CreatorRole = common.RoleAdminUser
			job.Mode = scenario.mode
			job.SummaryDims = []string{LogSummaryDimModel}
			if scenario.override {
				ratio := "1.5"
				job.GroupRatioOverride = &ratio
				job.RepriceQuotaPerUnit = common.QuotaPerUnit
			}
			require.True(t, AcquireLogExportSlot(job.JobID, 1, 20*time.Minute))
			t.Cleanup(func() { ReleaseLogExportSlot(job.JobID) })
			require.NoError(t, CreateLogExportJob(job))
			t.Cleanup(func() { RemoveLogExportJobFiles(job); DeleteLogExportJob(job) })
			debug.FreeOSMemory()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)
			rss, err := proc.MemoryInfo()
			require.NoError(t, err)
			cpu, err := proc.Times()
			require.NoError(t, err)
			poolBefore := conn.Stats()
			queryBefore := queries.Load()
			peakHeap, peakRSS := before.HeapAlloc, rss.RSS
			peakPool := 0
			stop, done := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(done)
				tick := time.NewTicker(25 * time.Millisecond)
				defer tick.Stop()
				for {
					select {
					case <-stop:
						return
					case <-tick.C:
						var mem runtime.MemStats
						runtime.ReadMemStats(&mem)
						peakHeap = max(peakHeap, mem.HeapAlloc)
						if sample, e := proc.MemoryInfo(); e == nil {
							peakRSS = max(peakRSS, sample.RSS)
						}
						peakPool = max(peakPool, conn.Stats().InUse)
					}
				}
			}()
			begin := time.Now()
			runLogExport(job)
			elapsed := time.Since(begin)
			close(stop)
			<-done
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			cpuAfter, err := proc.Times()
			require.NoError(t, err)
			poolAfter := conn.Stats()
			require.Equal(t, LogExportStatusReady, job.Status, job.Error)
			require.EqualValues(t, scenario.rows, job.ScannedRows)
			require.Less(t, peakHeap, uint64(512*1024*1024), "a bounded batch must not retain the million-row dataset")
			detail, summary := verifyRepriceLoadFiles(t, job, scenario.rows, scenario.override)
			result := repriceLoadResult{Name: scenario.name, DefaultThrottle: scenario.defaults, Rows: scenario.rows, Seconds: elapsed.Seconds(),
				RowsPerSecond: float64(scenario.rows) / elapsed.Seconds(), CPUSeconds: cpuAfter.Total() - cpu.Total(),
				HeapStartMiB: float64(before.HeapAlloc) / 1048576, HeapPeakMiB: float64(peakHeap) / 1048576,
				RSSStartMiB: float64(rss.RSS) / 1048576, RSSPeakMiB: float64(peakRSS) / 1048576,
				AllocatedMiB: float64(after.TotalAlloc-before.TotalAlloc) / 1048576, GCs: after.NumGC - before.NumGC,
				Queries: queries.Load() - queryBefore, PoolPeak: peakPool, PoolWaits: poolAfter.WaitCount - poolBefore.WaitCount,
				ThrottleSeconds: float64(job.ThrottledMs) / 1000, Parts: len(job.Parts), Bytes: job.BytesOut,
				VerifiedDetailRows: detail, VerifiedSummaryRows: summary}
			result.AverageCPUCores = result.CPUSeconds / result.Seconds
			results = append(results, result)
			encoded, err := common.Marshal(result)
			require.NoError(t, err)
			t.Logf("RESULT %s", encoded)
		})
	}
	require.Len(t, results, 7)
	require.Equal(t, results[1].Queries, results[2].Queries, "overriding must not add per-row SQL")
	require.Equal(t, results[3].Queries, results[4].Queries, "summary override must use the same query count")
	if report := os.Getenv("EXPORT_REPRICE_LOAD_REPORT"); report != "" {
		encoded, err := common.Marshal(results)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(report, encoded, 0600))
	}
}

// Decode every generated part with a streaming CSV reader. The sequential
// request ID checks omissions/duplicates across both batch and file boundaries.
func verifyRepriceLoadFiles(t *testing.T, job *LogExportJob, total int, override bool) (int, int) {
	t.Helper()
	type summary struct{ calls, prompt, completion, quota int64 }
	expected := make(map[string]*summary, 31)
	for n := range total {
		key := fmt.Sprintf("model-%02d", n%31)
		if expected[key] == nil {
			expected[key] = &summary{}
		}
		r := expected[key]
		r.calls++
		r.prompt += int64(100 + n%400)
		r.completion += int64(20 + n%100)
		r.quota += repriceLoadQuota(n, override)
	}
	detailRows, summaryRows := 0, 0
	seenGroups := make(map[string]bool, 31)
	for _, part := range job.Parts {
		f, err := os.Open(part.Path)
		require.NoError(t, err)
		gz, err := gzip.NewReader(f)
		require.NoError(t, err)
		reader := csv.NewReader(gz)
		reader.ReuseRecord = true
		_, err = reader.Read()
		require.NoError(t, err)
		for {
			row, e := reader.Read()
			if e == io.EOF {
				break
			}
			require.NoError(t, e)
			if part.Kind == LogExportPartKindSummary {
				require.Len(t, row, 7)
				want := expected[row[0]]
				require.NotNil(t, want)
				require.False(t, seenGroups[row[0]])
				seenGroups[row[0]] = true
				require.Equal(t, []string{row[0], strconv.FormatInt(want.calls, 10), strconv.FormatInt(want.prompt, 10), strconv.FormatInt(want.completion, 10), strconv.FormatInt(want.prompt+want.completion, 10), strconv.FormatInt(want.quota, 10), strconv.FormatFloat(float64(want.quota)/common.QuotaPerUnit, 'f', 6, 64)}, row)
				summaryRows++
				continue
			}
			n := detailRows
			if len(row) != 8 || row[0] != fmt.Sprintf("load-%08d", n) {
				require.FailNow(t, "missing, duplicated or malformed detail row", "row=%d data=%v", n, row)
			}
			group, special := repriceLoadRatios(n, override)
			quota := repriceLoadQuota(n, override)
			if row[1] != strconv.FormatInt(quota, 10) || row[3] != group || row[4] != special || row[5] != fmt.Sprintf("model-%02d", n%31) || row[6] != strconv.Itoa(100+n%400) || row[7] != strconv.Itoa(20+n%100) {
				require.FailNow(t, "incorrect detail values", "row=%d data=%v", n, row)
			}
			cost, e := strconv.ParseFloat(row[2], 64)
			require.NoError(t, e)
			if math.Abs(cost-float64(quota)/common.QuotaPerUnit) > 1e-12 {
				require.FailNow(t, "incorrect exported charge", "row=%d cost=%s quota=%d", n, row[2], quota)
			}
			detailRows++
		}
		require.NoError(t, gz.Close())
		require.NoError(t, f.Close())
	}
	if job.Mode != LogExportModeSummary {
		require.Equal(t, total, detailRows)
	}
	if job.Mode != LogExportModeDetail {
		require.Equal(t, 31, summaryRows)
	}
	return detailRows, summaryRows
}
