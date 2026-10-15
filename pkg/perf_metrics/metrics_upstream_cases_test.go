package perfmetrics

import (
	"context"
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func currentBucketTs() int64 { return bucketStart(time.Now().Unix()) }

func TestAtomicBucket_Add_FullSample(t *testing.T) {
	b := &atomicBucket{}
	b.add(Sample{Success: true, LatencyMs: 100, HasTtft: true, TtftMs: 20, OutputTokens: 50, GenerationMs: 80})
	got := b.snapshot()
	assert.Equal(t, counters{
		requestCount: 1, successCount: 1, totalLatencyMs: 100,
		ttftSumMs: 20, ttftCount: 1, outputTokens: 50, generationMs: 80,
	}, got)
}

func TestAtomicBucket_Add_FailureNoSuccessCount(t *testing.T) {
	b := &atomicBucket{}
	b.add(Sample{Success: false, LatencyMs: 10})
	got := b.snapshot()
	assert.Equal(t, int64(1), got.requestCount)
	assert.Equal(t, int64(0), got.successCount, "failed sample must not bump successCount")
}

func TestAtomicBucket_Add_ZeroAndNegativeGuards(t *testing.T) {
	b := &atomicBucket{}
	// LatencyMs 0 (not >0), HasTtft but TtftMs negative, OutputTokens>0 but
	// GenerationMs 0 => none of the optional counters move.
	b.add(Sample{Success: true, LatencyMs: 0, HasTtft: true, TtftMs: -1, OutputTokens: 5, GenerationMs: 0})
	got := b.snapshot()
	assert.Equal(t, int64(1), got.requestCount)
	assert.Equal(t, int64(1), got.successCount)
	assert.Equal(t, int64(0), got.totalLatencyMs, "LatencyMs<=0 excluded")
	assert.Equal(t, int64(0), got.ttftCount, "negative TtftMs excluded")
	assert.Equal(t, int64(0), got.outputTokens, "GenerationMs=0 excludes tokens/gen")
	assert.Equal(t, int64(0), got.generationMs)
}

func TestAtomicBucket_Add_TtftZeroBoundaryIncluded(t *testing.T) {
	b := &atomicBucket{}
	// TtftMs == 0 satisfies >= 0, so it counts (boundary).
	b.add(Sample{HasTtft: true, TtftMs: 0})
	got := b.snapshot()
	assert.Equal(t, int64(1), got.ttftCount, "TtftMs==0 is a valid TTFT")
}

func TestAtomicBucket_Add_Accumulates(t *testing.T) {
	b := &atomicBucket{}
	b.add(Sample{Success: true, LatencyMs: 100})
	b.add(Sample{Success: false, LatencyMs: 50})
	got := b.snapshot()
	assert.Equal(t, int64(2), got.requestCount)
	assert.Equal(t, int64(1), got.successCount)
	assert.Equal(t, int64(150), got.totalLatencyMs)
}

func TestAtomicBucket_Drain_ResetsAndReturnsPrevious(t *testing.T) {
	b := &atomicBucket{}
	b.add(Sample{Success: true, LatencyMs: 100, HasTtft: true, TtftMs: 5, OutputTokens: 3, GenerationMs: 9})
	drained := b.drain()
	assert.Equal(t, int64(1), drained.requestCount)
	assert.Equal(t, counters{}, b.snapshot(), "drain must zero every field")
}

func TestAtomicBucket_AddCounters_RoundTripFromDrain(t *testing.T) {
	b := &atomicBucket{}
	b.add(Sample{Success: true, LatencyMs: 100, HasTtft: true, TtftMs: 5, OutputTokens: 3, GenerationMs: 9})
	drained := b.drain()
	b.addCounters(drained) // put it back (flush-failure recovery path)
	assert.Equal(t, drained, b.snapshot())
}

func TestAtomicBucket_AddCounters_AllZeroIsNoOp(t *testing.T) {
	b := &atomicBucket{}
	b.add(Sample{Success: true})
	before := b.snapshot()
	b.addCounters(counters{}) // every field zero => each guarded Add skipped
	assert.Equal(t, before, b.snapshot())
}

func TestAvg(t *testing.T) {
	assert.Equal(t, int64(0), avg(100, 0), "count<=0 => 0")
	assert.Equal(t, int64(0), avg(100, -1))
	assert.Equal(t, int64(50), avg(100, 2))
	assert.Equal(t, int64(33), avg(100, 3), "integer division truncates")
}

func TestSuccessRate(t *testing.T) {
	assert.Equal(t, 0.0, successRate(counters{requestCount: 0}))
	assert.Equal(t, 100.0, successRate(counters{requestCount: 2, successCount: 2}))
	assert.Equal(t, 50.0, successRate(counters{requestCount: 2, successCount: 1}))
}

func TestAvgTps(t *testing.T) {
	assert.Equal(t, 0.0, avgTps(counters{outputTokens: 0, generationMs: 1000}), "no tokens => 0")
	assert.Equal(t, 0.0, avgTps(counters{outputTokens: 10, generationMs: 0}), "no gen time => 0")
	assert.Equal(t, 100.0, avgTps(counters{outputTokens: 100, generationMs: 1000}), "100 tok / 1s")
}

func TestMergeCounters(t *testing.T) {
	m := map[bucketKey]counters{}
	k := bucketKey{model: "m", group: "g", bucketTs: 3600}
	mergeCounters(m, k, counters{requestCount: 0, successCount: 5}) // guard: skipped
	assert.Empty(t, m)
	mergeCounters(m, k, counters{requestCount: 2, successCount: 1, totalLatencyMs: 10})
	mergeCounters(m, k, counters{requestCount: 3, successCount: 2, totalLatencyMs: 20})
	assert.Equal(t, counters{requestCount: 5, successCount: 3, totalLatencyMs: 30}, m[k])
}

func TestMergeModelTotals(t *testing.T) {
	m := map[string]counters{}
	mergeModelTotals(m, "gpt", counters{requestCount: 0}) // skipped
	assert.Empty(t, m)
	mergeModelTotals(m, "gpt", counters{requestCount: 1, outputTokens: 4})
	mergeModelTotals(m, "gpt", counters{requestCount: 2, outputTokens: 6})
	assert.Equal(t, counters{requestCount: 3, outputTokens: 10}, m["gpt"])
}

func TestMergeModelBucket(t *testing.T) {
	m := map[string]map[int64]counters{}
	mergeModelBucket(m, "gpt", 3600, counters{requestCount: 0}) // skipped
	assert.Empty(t, m)
	mergeModelBucket(m, "gpt", 3600, counters{requestCount: 1})
	mergeModelBucket(m, "gpt", 3600, counters{requestCount: 2})
	mergeModelBucket(m, "gpt", 7200, counters{requestCount: 5})
	assert.Equal(t, int64(3), m["gpt"][3600].requestCount)
	assert.Equal(t, int64(5), m["gpt"][7200].requestCount)
}

func TestRecentSuccessSeries_Empty(t *testing.T) {
	assert.Nil(t, recentSuccessSeries(map[int64]counters{}))
	assert.Nil(t, recentSuccessSeries(map[int64]counters{3600: {requestCount: 0}}), "hours without requests are skipped")
}

func TestRecentSuccessSeries_MergesHoursSortedByTs(t *testing.T) {
	buckets := map[int64]counters{
		7200:  {requestCount: 4, successCount: 1}, // hour 7200
		7260:  {requestCount: 2, successCount: 2}, // same hour => 3/6 = 50%
		3600:  {requestCount: 3, successCount: 1}, // 33.33%
		10800: {requestCount: 0},                  // skipped
	}
	got := recentSuccessSeries(buckets)
	assert.Equal(t, []SuccessRatePoint{
		{Ts: 3600, SuccessRate: 33.33},
		{Ts: 7200, SuccessRate: 50},
	}, got)
}

func TestAllowedGroupSet(t *testing.T) {
	assert.Nil(t, allowedGroupSet(nil), "nil groups => nil (no filter)")
	set := allowedGroupSet([]string{"a", "b"})
	require.NotNil(t, set)
	_, hasA := set["a"]
	_, hasC := set["c"]
	assert.True(t, hasA)
	assert.False(t, hasC)
}

func TestBucketStart_AlignsToBucket(t *testing.T) {
	// Default bucket_time "hour" => 3600s. 7325 => 7200; exact multiple stays.
	assert.Equal(t, int64(7200), bucketStart(7325))
	assert.Equal(t, int64(7200), bucketStart(7200))
	assert.Equal(t, int64(0), bucketStart(59))
}

func TestBucketPoint(t *testing.T) {
	p := bucketPoint(3600, counters{
		requestCount: 2, successCount: 1, totalLatencyMs: 200,
		ttftSumMs: 40, ttftCount: 2, outputTokens: 100, generationMs: 1000,
	})
	assert.Equal(t, int64(3600), p.Ts)
	assert.Equal(t, int64(20), p.AvgTtftMs)     // 40/2
	assert.Equal(t, int64(100), p.AvgLatencyMs) // 200/2
	assert.Equal(t, 50.0, p.SuccessRate)        // 1/2
	assert.Equal(t, 100.0, p.AvgTps)            // 100/(1000/1000)
}

func TestBuildQueryResult_MultiGroupSortedWithSeries(t *testing.T) {
	merged := map[bucketKey]counters{
		{model: "gpt", group: "vip", bucketTs: 7200}:     {requestCount: 2, successCount: 2, totalLatencyMs: 200},
		{model: "gpt", group: "vip", bucketTs: 3600}:     {requestCount: 2, successCount: 1, totalLatencyMs: 100},
		{model: "gpt", group: "default", bucketTs: 3600}: {requestCount: 4, successCount: 4, totalLatencyMs: 400},
		{model: "gpt", group: "empty", bucketTs: 3600}:   {requestCount: 0}, // dropped
	}
	res := buildQueryResult("gpt", merged)
	assert.Equal(t, "gpt", res.ModelName)
	assert.Equal(t, seriesSchema, res.SeriesSchema)

	require.Len(t, res.Groups, 2, "empty-request group is excluded")
	// Groups sorted alphabetically: default, vip.
	assert.Equal(t, "default", res.Groups[0].Group)
	assert.Equal(t, "vip", res.Groups[1].Group)

	// vip series sorted by ts ascending: 3600 then 7200.
	vip := res.Groups[1]
	require.Len(t, vip.Series, 2)
	assert.Equal(t, int64(3600), vip.Series[0].Ts)
	assert.Equal(t, int64(7200), vip.Series[1].Ts)
	// vip totals: req 4, succ 3 => 75%.
	assert.Equal(t, 75.0, vip.SuccessRate)
	assert.Equal(t, int64(75), vip.AvgLatencyMs) // 300/4
}

func TestRedisBucketKey(t *testing.T) {
	assert.Equal(t, "perf:gpt-4:vip:3600",
		redisBucketKey(bucketKey{model: "gpt-4", group: "vip", bucketTs: 3600}))
}

func TestParseRedisInt(t *testing.T) {
	assert.Equal(t, int64(0), parseRedisInt(""))
	assert.Equal(t, int64(0), parseRedisInt("not-a-number"))
	assert.Equal(t, int64(42), parseRedisInt("42"))
	assert.Equal(t, int64(-7), parseRedisInt("-7"))
}

func TestRedisCounters(t *testing.T) {
	got := redisCounters(map[string]string{
		"req": "10", "ok": "8", "lat": "1000",
		"ttft": "200", "ttft_n": "10", "out": "500", "gen_ms": "5000",
	})
	assert.Equal(t, counters{
		requestCount: 10, successCount: 8, totalLatencyMs: 1000,
		ttftSumMs: 200, ttftCount: 10, outputTokens: 500, generationMs: 5000,
	}, got)
}

func TestRedisCounters_MissingKeysAreZero(t *testing.T) {
	got := redisCounters(map[string]string{"req": "3"})
	assert.Equal(t, counters{requestCount: 3}, got)
}

func loadBucket(t *testing.T, model, group string, ts int64) (counters, bool) {
	t.Helper()
	v, ok := hotBuckets.Load(bucketKey{model: model, group: group, bucketTs: ts})
	if !ok {
		return counters{}, false
	}
	return v.(*atomicBucket).snapshot(), true
}

func cleanupBucket(t *testing.T, model, group string) {
	t.Helper()
	t.Cleanup(func() {
		hotBuckets.Range(func(key, _ any) bool {
			if k := key.(bucketKey); k.model == model && k.group == group {
				hotBuckets.Delete(key)
			}
			return true
		})
	})
}

func TestRecord_EmptyModelIsIgnored(t *testing.T) {
	// No model => dropped before touching hotBuckets. Nothing to assert beyond
	// "does not panic / does not create a bucket"; use an impossible key sweep.
	countBefore := 0
	hotBuckets.Range(func(_, _ any) bool { countBefore++; return true })
	Record(Sample{Model: "", Success: true})
	countAfter := 0
	hotBuckets.Range(func(_, _ any) bool { countAfter++; return true })
	assert.Equal(t, countBefore, countAfter)
}

func TestRecord_DefaultsGroupToDefault(t *testing.T) {
	model := "test-record-defgroup"
	cleanupBucket(t, model, "default")
	Record(Sample{Model: model, Group: "", Success: true, LatencyMs: 100})
	ts := currentBucketTs()
	got, ok := loadBucket(t, model, "default", ts)
	require.True(t, ok, "empty group must land under 'default'")
	assert.Equal(t, int64(1), got.requestCount)
	assert.Equal(t, int64(100), got.totalLatencyMs)
}

func TestRecord_ClampsNegativeLatency(t *testing.T) {
	model := "test-record-negclamp"
	cleanupBucket(t, model, "g")
	Record(Sample{Model: model, Group: "g", Success: true, LatencyMs: -50})
	ts := currentBucketTs()
	got, ok := loadBucket(t, model, "g", ts)
	require.True(t, ok)
	assert.Equal(t, int64(1), got.requestCount)
	assert.Equal(t, int64(0), got.totalLatencyMs, "negative latency clamped to 0 => not accumulated")
}

func TestRecord_AccumulatesIntoSameBucket(t *testing.T) {
	model := "test-record-accum"
	cleanupBucket(t, model, "g")
	Record(Sample{Model: model, Group: "g", Success: true, LatencyMs: 10})
	Record(Sample{Model: model, Group: "g", Success: false, LatencyMs: 20})
	ts := currentBucketTs()
	got, _ := loadBucket(t, model, "g", ts)
	assert.Equal(t, int64(2), got.requestCount)
	assert.Equal(t, int64(1), got.successCount)
	assert.Equal(t, int64(30), got.totalLatencyMs)
}

func TestClassifyRelayOutcome(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  *types.NewAPIError
		want Outcome
	}{
		{"success", context.Background(), nil, OutcomeSuccess},
		{"upstream bad request", context.Background(), types.InitOpenAIError("unknown", 400), OutcomeIgnored},
		{"upstream credentials returned as 400", context.Background(), types.InitOpenAIError("invalid_api_key", 400), OutcomeFailure},
		{"wrapped credentials keep their cause", context.Background(), types.NewErrorWithStatusCode(types.InitOpenAIError("invalid_api_key", 400), types.ErrorCodeInvalidRequest, 400), OutcomeFailure},
		{"upstream context limit returned as 500", context.Background(), types.InitOpenAIError("context_length_exceeded", 500), OutcomeIgnored},
		{"local rate limit", context.Background(), types.NewErrorWithStatusCode(errors.New("limited"), types.ErrorCodeInvalidRequest, 429), OutcomeIgnored},
		{"upstream rate limit", context.Background(), types.InitOpenAIError("rate_limit_exceeded", 429), OutcomeFailure},
		{"local quota", context.Background(), types.NewError(errors.New("quota"), types.ErrorCodeInsufficientUserQuota), OutcomeIgnored},
		{"local violation fee", context.Background(), types.NewError(errors.New("csam"), types.ErrorCodeViolationFeeGrokCSAM), OutcomeIgnored},
		{"upstream quota", context.Background(), types.InitOpenAIError("insufficient_quota", 429), OutcomeFailure},
		{"upstream gateway quota", context.Background(), types.InitOpenAIError(types.ErrorCodeInsufficientUserQuota, 403), OutcomeFailure},
		{"unavailable channel", context.Background(), types.NewErrorWithStatusCode(errors.New("disabled"), types.ErrorCodeGetChannelFailed, 403), OutcomeFailure},
		{"empty upstream response", context.Background(), types.NewError(errors.New("empty"), types.ErrorCodeEmptyResponse), OutcomeFailure},
		{"network failure", context.Background(), types.NewOpenAIError(errors.New("connection refused"), types.ErrorCodeDoRequestFailed, 500), OutcomeFailure},
		{"client cancellation", canceled, types.NewOpenAIError(errors.New("context canceled"), types.ErrorCodeDoRequestFailed, 500), OutcomeIgnored},
		{"upstream deadline", context.Background(), types.NewOpenAIError(context.DeadlineExceeded, types.ErrorCodeDoRequestFailed, 504), OutcomeFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ClassifyRelayOutcome(tc.ctx, &relaycommon.RelayInfo{}, tc.err))
		})
	}
	assert.Equal(t, OutcomeIgnored, ClassifyRelayOutcome(context.Background(), &relaycommon.RelayInfo{PerformanceBusinessRejection: true}, nil))
}

func TestStreamOutcomeClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		mark func(*relaycommon.StreamStatus)
		end  relaycommon.StreamEndReason
		want Outcome
	}{
		{"completed", (*relaycommon.StreamStatus).MarkCompleted, relaycommon.StreamEndReasonEOF, OutcomeSuccess},
		{"business rejection", func(s *relaycommon.StreamStatus) { s.MarkFailed("context_length_exceeded", "", 0) }, relaycommon.StreamEndReasonEOF, OutcomeIgnored},
		{"service error", func(s *relaycommon.StreamStatus) { s.MarkFailed("server_error", "", 0) }, relaycommon.StreamEndReasonEOF, OutcomeFailure},
		{"error after completion", func(s *relaycommon.StreamStatus) { s.MarkCompleted(); s.MarkFailed("", "server_error", 0) }, relaycommon.StreamEndReasonEOF, OutcomeFailure},
		{"output limit", func(s *relaycommon.StreamStatus) { s.MarkIncomplete("max_output_tokens") }, relaycommon.StreamEndReasonEOF, OutcomeSuccess},
		{"content filter", func(s *relaycommon.StreamStatus) { s.MarkIncomplete("content_filter") }, relaycommon.StreamEndReasonEOF, OutcomeIgnored},
		{"client cancel", (*relaycommon.StreamStatus).MarkCancelled, relaycommon.StreamEndReasonEOF, OutcomeIgnored},
		{"client gone", nil, relaycommon.StreamEndReasonClientGone, OutcomeIgnored},
		{"timeout", nil, relaycommon.StreamEndReasonTimeout, OutcomeFailure},
		{"missing terminal", nil, relaycommon.StreamEndReasonEOF, OutcomeFailure},
		{"done marker without terminal", nil, relaycommon.StreamEndReasonDone, OutcomeSuccess},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := relaycommon.NewStreamStatus()
			stream.RequireTerminal()
			if tc.mark != nil {
				tc.mark(stream)
			}
			stream.SetEndReason(tc.end, nil)
			assert.Equal(t, tc.want, ClassifyRelayOutcome(context.Background(), &relaycommon.RelayInfo{StreamStatus: stream}, nil))
		})
	}
}

func TestClientCancellationDuringUpstreamRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream := relaycommon.NewStreamStatus()
	stream.RequireTerminal()
	stream.SetEndReason(relaycommon.StreamEndReasonScannerErr, errors.New("reader closed"))
	assert.Equal(t, OutcomeIgnored, ClassifyRelayOutcome(ctx, &relaycommon.RelayInfo{StreamStatus: stream}, nil))

	deadline := relaycommon.NewStreamStatus()
	deadline.SetEndReason(relaycommon.StreamEndReasonClientGone, context.DeadlineExceeded)
	assert.Equal(t, OutcomeFailure, ClassifyRelayOutcome(context.Background(), &relaycommon.RelayInfo{StreamStatus: deadline}, nil))
}

func TestPerformanceWindowIncludesCurrentHour(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 37, 0, 0, time.UTC)
	start, end := queryWindow(now, 24)
	assert.Equal(t, time.Date(2026, 9, 13, 13, 0, 0, 0, time.UTC).Unix(), start)
	assert.Equal(t, now.Unix(), end)
}

func TestHourlySuccessSeriesWeightsSmallerBuckets(t *testing.T) {
	points := recentSuccessSeries(map[int64]counters{
		3600: {requestCount: 100, successCount: 100},
		3900: {requestCount: 1},
		7200: {requestCount: 1, successCount: 1},
	})
	assert.Equal(t, []SuccessRatePoint{{Ts: 3600, SuccessRate: 99.01}, {Ts: 7200, SuccessRate: 100}}, points)
}

func TestRecentHistoryRejectsOversizedModelLists(t *testing.T) {
	_, err := QuerySummaryAll(24, nil, make([]string, 51)...)
	require.EqualError(t, err, "Invalid parameters")
}

func TestRecordTaskResultSamplesTerminalTasks(t *testing.T) {
	hotBuckets.Clear()
	t.Cleanup(func() { hotBuckets.Clear() })
	now := time.Now().Unix()
	RecordTaskResult(&model.Task{
		Status:     model.TaskStatusSuccess,
		Group:      "a",
		SubmitTime: now - 120,
		StartTime:  now - 100,
		FinishTime: now,
		Properties: model.Properties{OriginModelName: "video-model"},
	}, &relaycommon.TaskInfo{TotalTokens: 5000})
	RecordTaskResult(&model.Task{
		Status:     model.TaskStatusFailure,
		Group:      "a",
		SubmitTime: now - 60,
		FinishTime: now,
		Properties: model.Properties{OriginModelName: "video-model"},
	}, relaycommon.FailTaskInfo("boom"))
	RecordTaskResult(&model.Task{Status: model.TaskStatusSuccess, FinishTime: now}, nil)

	merged := map[bucketKey]counters{}
	hotBuckets.Range(func(key, value any) bool {
		k := key.(bucketKey)
		require.Equal(t, "video-model", k.model)
		require.Equal(t, "a", k.group)
		k.bucketTs = 0
		mergeCounters(merged, k, value.(*atomicBucket).snapshot())
		return true
	})
	assert.Equal(t, counters{
		requestCount:   2,
		successCount:   1,
		totalLatencyMs: 180000,
		outputTokens:   5000,
		generationMs:   100000,
	}, merged[bucketKey{model: "video-model", group: "a"}])
}

func TestPerformanceAggregationAndFlush(t *testing.T) {
	for _, dialect := range []struct{ name, env string }{
		{"sqlite", ""}, {"mysql", "TEST_PERF_MYSQL_DSN"}, {"postgres", "TEST_PERF_POSTGRES_DSN"},
	} {
		t.Run(dialect.name, func(t *testing.T) {
			dsn := ""
			if dialect.env != "" {
				dsn = os.Getenv(dialect.env)
				if dsn == "" {
					t.Skip("isolated test database DSN is not configured")
				}
			}
			t.Setenv("SQL_DSN", dsn)
			oldDB, oldPath, oldMaster, oldRedis := model.DB, common.SQLitePath, common.IsMasterNode, common.RedisEnabled
			oldType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
			common.SQLitePath, common.IsMasterNode, common.RedisEnabled = filepath.Join(t.TempDir(), "perf.db"), false, false
			hotBuckets.Clear()
			t.Cleanup(func() {
				model.DB, common.SQLitePath, common.IsMasterNode, common.RedisEnabled = oldDB, oldPath, oldMaster, oldRedis
				common.SetDatabaseTypes(oldType, oldLogType)
				hotBuckets.Clear()
			})
			require.NoError(t, model.InitDB())
			db := model.DB
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			require.NoError(t, db.Migrator().DropTable(&model.PerfMetric{}))
			require.NoError(t, db.AutoMigrate(&model.PerfMetric{}))
			versionQuery := "SELECT version()"
			if dialect.name == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database version: %s", version)

			now := time.Now()
			start, _ := queryWindow(now, 24)
			hour := now.Unix() - now.Unix()%3600 - 3600
			// Historical counters remain usable without reclassification or migration.
			for _, row := range []model.PerfMetric{
				{ModelName: "test-model", Group: "a", BucketTs: hour, RequestCount: 100, SuccessCount: 100, TotalLatencyMs: 100000, TtftCount: 100, TtftSumMs: 10000, OutputTokens: 200, GenerationMs: 40000},
				{ModelName: "test-model", Group: "inactive", BucketTs: hour, RequestCount: 100},
				{ModelName: "test-model", Group: "a", BucketTs: start - 3600, RequestCount: 100},
			} {
				require.NoError(t, model.UpsertPerfMetric(&row))
			}
			groups := []string{"a", "b"}

			RecordRelayResult(context.Background(), &relaycommon.RelayInfo{OriginModelName: "test-model", UsingGroup: "b", StartTime: now}, types.InitOpenAIError("unknown", 400))
			businessRejected, err := QuerySummaryAll(24, groups)
			require.NoError(t, err)
			require.NotNil(t, businessRejected.Summary)
			assert.Equal(t, 100.0, businessRejected.Summary.SuccessRate)

			failure := &atomicBucket{}
			failure.add(Sample{LatencyMs: 2000})
			hotBuckets.Store(bucketKey{model: "test-model", group: "b", bucketTs: hour}, failure)
			before, err := Query(QueryParams{Model: "test-model", Hours: 24, AllowedGroups: groups})
			require.NoError(t, err)
			require.NotNil(t, before.Summary)
			assert.Equal(t, Summary{SuccessRate: 99.01, AvgLatencyMs: 1009, AvgTps: 5}, *before.Summary)
			assert.Equal(t, start, before.WindowStart)
			require.Len(t, before.Series, 1)
			assert.Equal(t, hour, before.Series[0].Ts)
			assert.InDelta(t, 99.01, before.Series[0].SuccessRate, 0.01)
			require.Len(t, before.Groups, 2)

			summary, err := QuerySummaryAll(24, groups)
			require.NoError(t, err)
			assert.Equal(t, before.Summary, summary.Summary)
			require.Len(t, summary.Models, 1)
			assert.Equal(t, 99.01, summary.Models[0].SuccessRate)
			assert.Equal(t, 99.01, summary.Models[0].RecentSuccessSeries[0].SuccessRate)
			encoded, err := common.Marshal(summary)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "request_count")
			assert.NotContains(t, string(encoded), "success_count")

			flushCompletedBuckets()
			flushCompletedBuckets()
			after, err := Query(QueryParams{Model: "test-model", Hours: 24, AllowedGroups: groups})
			require.NoError(t, err)
			assert.Equal(t, before.Summary, after.Summary)
			assert.Equal(t, before.Series, after.Series)

			RecordRelayResult(context.Background(), &relaycommon.RelayInfo{OriginModelName: "test-model", UsingGroup: "a", StartTime: now}, types.InitOpenAIError("context_length_exceeded", 400))
			after, err = Query(QueryParams{Model: "test-model", Hours: 24, AllowedGroups: groups})
			require.NoError(t, err)
			assert.Equal(t, before.Summary, after.Summary)
			onlyA, err := Query(QueryParams{Model: "test-model", Group: "a", Hours: 24, AllowedGroups: groups})
			require.NoError(t, err)
			assert.Equal(t, 100.0, onlyA.Summary.SuccessRate)
			empty, err := Query(QueryParams{Model: "missing", Hours: 24, AllowedGroups: groups})
			require.NoError(t, err)
			assert.Nil(t, empty.Summary)
			assert.Empty(t, empty.Series)

			require.NoError(t, model.UpsertPerfMetric(&model.PerfMetric{ModelName: "second-model", Group: "a", BucketTs: hour, RequestCount: 1}))
			combined, err := QuerySummaryAll(24, groups)
			require.NoError(t, err)
			assert.Equal(t, 98.04, combined.Summary.SuccessRate)
			assert.Equal(t, 99.01, combined.Models[0].SuccessRate)

			t.Run("recent history does not change window summaries", func(t *testing.T) {
				for i := range 26 {
					require.NoError(t, model.UpsertPerfMetric(&model.PerfMetric{
						ModelName: "history-model", Group: "a", BucketTs: hour - int64(i+1)*6*3600,
						RequestCount: 1, SuccessCount: 1,
					}))
				}
				for _, row := range []model.PerfMetric{
					{ModelName: "history-model", Group: "a", BucketTs: hour + 60, RequestCount: 100, SuccessCount: 100},
					{ModelName: "history-model", Group: "inactive", BucketTs: hour + 120, RequestCount: 100},
					{ModelName: "history-model", Group: "a", BucketTs: hour + 2*3600, RequestCount: 1},
					{ModelName: "history-idle", Group: "a", BucketTs: hour - 3*24*3600, RequestCount: 1},
					{ModelName: "history-hidden", Group: "inactive", BucketTs: hour - 3*24*3600, RequestCount: 1},
				} {
					require.NoError(t, model.UpsertPerfMetric(&row))
				}
				require.NoError(t, db.Create(&model.PerfMetric{ModelName: "history-model", Group: "a", BucketTs: hour - 3600}).Error)
				hotFailure := &atomicBucket{}
				hotFailure.add(Sample{})
				hotBuckets.Store(bucketKey{model: "history-model", group: "b", bucketTs: hour + 120}, hotFailure)
				hotSuccess := &atomicBucket{}
				hotSuccess.add(Sample{Success: true})
				hotBuckets.Store(bucketKey{model: "history-hot", group: "a", bucketTs: hour}, hotSuccess)
				hiddenFailure := &atomicBucket{}
				hiddenFailure.add(Sample{})
				hotBuckets.Store(bucketKey{model: "history-hidden", group: "inactive", bucketTs: hour}, hiddenFailure)

				window, err := QuerySummaryAll(24, groups)
				require.NoError(t, err)
				history, err := QuerySummaryAll(24, groups, "history-model", "history-idle", "history-hidden", "history-hot", "missing")
				require.NoError(t, err)
				assert.Equal(t, window.Summary, history.Summary)
				assert.ElementsMatch(t, window.Models, history.Models)
				assert.Equal(t, window.WindowStart, history.WindowStart)
				points := history.RecentSuccessSeries["history-model"]
				require.Len(t, points, 24)
				expected := make([]SuccessRatePoint, 0, 24)
				for i := 23; i > 0; i-- {
					expected = append(expected, SuccessRatePoint{Ts: hour - int64(i)*6*3600, SuccessRate: 100})
				}
				expected = append(expected, SuccessRatePoint{Ts: hour, SuccessRate: 99.01})
				assert.Equal(t, expected, points)
				assert.Equal(t, []SuccessRatePoint{{Ts: hour - 3*24*3600, SuccessRate: 0}}, history.RecentSuccessSeries["history-idle"])
				assert.Equal(t, []SuccessRatePoint{{Ts: hour, SuccessRate: 100}}, history.RecentSuccessSeries["history-hot"])
				assert.Empty(t, history.RecentSuccessSeries["history-hidden"])
				assert.Empty(t, history.RecentSuccessSeries["missing"])

				denied, err := QuerySummaryAll(24, []string{}, "history-model", "history-hidden")
				require.NoError(t, err)
				assert.Empty(t, denied.RecentSuccessSeries)
				flushCompletedBuckets()
				persisted, err := QuerySummaryAll(24, groups, "history-model", "history-hot")
				require.NoError(t, err)
				assert.Equal(t, points, persisted.RecentSuccessSeries["history-model"])
				assert.Equal(t, history.RecentSuccessSeries["history-hot"], persisted.RecentSuccessSeries["history-hot"])
			})

			t.Run("recent history stops at the 7 day boundary even with fewer than 24 samples", func(t *testing.T) {
				historyStart, _ := queryWindow(now, 7*24)
				for _, row := range []model.PerfMetric{
					{ModelName: "history-boundary", Group: "a", BucketTs: historyStart, RequestCount: 1, SuccessCount: 1},
					{ModelName: "history-boundary", Group: "a", BucketTs: historyStart - 3600, RequestCount: 1},
					{ModelName: "history-expired", Group: "a", BucketTs: historyStart - 3600, RequestCount: 1},
				} {
					require.NoError(t, model.UpsertPerfMetric(&row))
				}
				hotExpired := &atomicBucket{}
				hotExpired.add(Sample{Success: true})
				hotBuckets.Store(bucketKey{model: "history-hot-expired", group: "a", bucketTs: historyStart - 3600}, hotExpired)
				hotBoundary := &atomicBucket{}
				hotBoundary.add(Sample{Success: true})
				hotBuckets.Store(bucketKey{model: "history-hot-boundary", group: "a", bucketTs: historyStart}, hotBoundary)

				history, err := QuerySummaryAll(24, groups, "history-boundary", "history-expired", "history-hot-expired", "history-hot-boundary")
				require.NoError(t, err)
				assert.Equal(t, []SuccessRatePoint{{Ts: historyStart, SuccessRate: 100}}, history.RecentSuccessSeries["history-boundary"])
				assert.Equal(t, []SuccessRatePoint{{Ts: historyStart, SuccessRate: 100}}, history.RecentSuccessSeries["history-hot-boundary"])
				assert.Empty(t, history.RecentSuccessSeries["history-expired"])
				assert.Empty(t, history.RecentSuccessSeries["history-hot-expired"])
			})
		})
	}
}
