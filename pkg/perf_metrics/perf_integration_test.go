package perfmetrics

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetHotBuckets empties the shared hotBuckets map so a flush/query test sees
// only the state it seeds. Tests in a package run sequentially, so this is safe.
func resetHotBuckets(t *testing.T) {
	t.Helper()
	hotBuckets.Range(func(key, _ any) bool { hotBuckets.Delete(key); return true })
	t.Cleanup(func() {
		hotBuckets.Range(func(key, _ any) bool { hotBuckets.Delete(key); return true })
	})
}

func seedHotBucket(model, group string, ts int64, c counters) {
	b := &atomicBucket{}
	b.addCounters(c)
	hotBuckets.Store(bucketKey{model: model, group: group, bucketTs: ts}, b)
}

// ---------------------------------------------------------------------------
// RecordRelaySample — TTFT / latency / generation derivation (in-memory).
// ---------------------------------------------------------------------------

func TestRecordRelaySample_NilInfoIsNoOp(t *testing.T) {
	resetHotBuckets(t)
	RecordRelaySample(nil, true, 10)
	empty := true
	hotBuckets.Range(func(_, _ any) bool { empty = false; return false })
	assert.True(t, empty)
}

func TestRecordRelaySample_StreamRecordsTtft(t *testing.T) {
	resetHotBuckets(t)
	start := time.Now().Add(-10 * time.Second)
	info := &relaycommon.RelayInfo{
		OriginModelName:   "rrs-stream",
		UsingGroup:        "g",
		IsStream:          true,
		StartTime:         start,
		FirstResponseTime: start.Add(2 * time.Second), // After(start) => HasSendResponse
	}
	RecordRelaySample(info, true, 100)

	got, ok := loadBucket(t, "rrs-stream", "g", currentBucketTs())
	require.True(t, ok)
	assert.Equal(t, int64(1), got.requestCount)
	assert.Equal(t, int64(1), got.successCount)
	assert.Equal(t, int64(2000), got.ttftSumMs, "TTFT = FirstResponseTime - StartTime")
	assert.Equal(t, int64(1), got.ttftCount)
	assert.Equal(t, int64(100), got.outputTokens)
	assert.Greater(t, got.generationMs, int64(0))
	assert.Less(t, got.generationMs, got.totalLatencyMs, "generation window is shorter than total latency")
}

func TestRecordRelaySample_NonStreamNoTtft(t *testing.T) {
	resetHotBuckets(t)
	start := time.Now().Add(-5 * time.Second)
	info := &relaycommon.RelayInfo{
		OriginModelName:   "rrs-nonstream",
		UsingGroup:        "g",
		IsStream:          false,
		StartTime:         start,
		FirstResponseTime: start.Add(2 * time.Second),
	}
	RecordRelaySample(info, false, 50) // tokens>0 so generationMs is recorded

	got, ok := loadBucket(t, "rrs-nonstream", "g", currentBucketTs())
	require.True(t, ok)
	assert.Equal(t, int64(0), got.ttftCount, "non-stream => no TTFT")
	// generationMs falls back to latencyMs and equals totalLatencyMs.
	assert.Equal(t, got.totalLatencyMs, got.generationMs)
}

func TestRecordRelaySample_StreamWithoutResponseHasNoTtft(t *testing.T) {
	resetHotBuckets(t)
	start := time.Now().Add(-5 * time.Second)
	info := &relaycommon.RelayInfo{
		OriginModelName:   "rrs-noresp",
		UsingGroup:        "g",
		IsStream:          true,
		StartTime:         start,
		FirstResponseTime: start, // == start => HasSendResponse false
	}
	RecordRelaySample(info, true, 10)

	got, _ := loadBucket(t, "rrs-noresp", "g", currentBucketTs())
	assert.Equal(t, int64(0), got.ttftCount, "no response sent => no TTFT even when streaming")
}

func TestRecordRelaySample_NonPositiveGenerationFallsBackToLatency(t *testing.T) {
	resetHotBuckets(t)
	start := time.Now().Add(-5 * time.Second)
	info := &relaycommon.RelayInfo{
		OriginModelName:   "rrs-genfallback",
		UsingGroup:        "g",
		IsStream:          true,
		StartTime:         start,
		FirstResponseTime: start.Add(time.Hour), // in the future => now-FRT < 0
	}
	RecordRelaySample(info, true, 10)

	got, _ := loadBucket(t, "rrs-genfallback", "g", currentBucketTs())
	// hasTtft is true (FRT after start), but generation window is negative =>
	// generationMs is replaced by latencyMs (== totalLatencyMs here).
	assert.Equal(t, got.totalLatencyMs, got.generationMs)
}

// ---------------------------------------------------------------------------
// recordRedis + mergeRedisActiveBuckets (real Redis).
// ---------------------------------------------------------------------------

func TestRecordRedis_And_MergeActiveBucket(t *testing.T) {
	requireRedis(t)
	ts := currentBucketTs()
	key := bucketKey{model: "redis-perf", group: "g", bucketTs: ts}
	t.Cleanup(func() { common.RDB.Del(context.Background(), redisBucketKey(key)) })

	recordRedis(key, Sample{Success: true, LatencyMs: 100, HasTtft: true, TtftMs: 20, OutputTokens: 5, GenerationMs: 50})
	recordRedis(key, Sample{Success: false, LatencyMs: 60})

	merged := map[bucketKey]counters{}
	mergeRedisActiveBuckets(merged, QueryParams{Model: "redis-perf", Group: "g"}, ts-10, ts+10)

	got := merged[key]
	assert.Equal(t, int64(2), got.requestCount)
	assert.Equal(t, int64(1), got.successCount)
	assert.Equal(t, int64(160), got.totalLatencyMs)
	assert.Equal(t, int64(20), got.ttftSumMs)
	assert.Equal(t, int64(5), got.outputTokens)
}

func TestMergeRedisActiveBuckets_GuardsOnEmptyParams(t *testing.T) {
	requireRedis(t)
	merged := map[bucketKey]counters{}
	// Missing group => guarded early return, nothing added.
	mergeRedisActiveBuckets(merged, QueryParams{Model: "x", Group: ""}, 0, 1<<62)
	assert.Empty(t, merged)
}

func TestMergeRedisActiveBuckets_ActiveBucketOutOfRange(t *testing.T) {
	requireRedis(t)
	merged := map[bucketKey]counters{}
	// The active bucket is "now"; a window entirely in the past excludes it.
	mergeRedisActiveBuckets(merged, QueryParams{Model: "x", Group: "g"}, 0, 100)
	assert.Empty(t, merged)
}

// ---------------------------------------------------------------------------
// flushCompletedBuckets — completed bucket persists; current bucket skipped;
// empty old bucket dropped.
// ---------------------------------------------------------------------------

func TestFlushCompletedBuckets_PersistsCompletedBucket(t *testing.T) {
	requireDB(t)
	resetHotBuckets(t)
	past := currentBucketTs() - 3600 // one full bucket in the past => completed
	seedHotBucket("flush-model", "g", past, counters{requestCount: 3, successCount: 2, totalLatencyMs: 300})

	flushCompletedBuckets()

	rows, err := model.GetPerfMetrics("flush-model", "g", past-1, past+1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(3), rows[0].RequestCount)
	assert.Equal(t, int64(2), rows[0].SuccessCount)

	// Bucket was drained in place (still recent => kept but zeroed).
	got, ok := loadBucket(t, "flush-model", "g", past)
	if ok {
		assert.Equal(t, int64(0), got.requestCount, "flushed bucket must be drained")
	}
}

func TestFlushCompletedBuckets_SkipsCurrentBucket(t *testing.T) {
	requireDB(t)
	resetHotBuckets(t)
	cur := currentBucketTs()
	seedHotBucket("flush-current", "g", cur, counters{requestCount: 5})

	flushCompletedBuckets()

	rows, err := model.GetPerfMetrics("flush-current", "g", cur-1, cur+1)
	require.NoError(t, err)
	assert.Empty(t, rows, "the still-open current bucket must not be flushed")
	got, ok := loadBucket(t, "flush-current", "g", cur)
	require.True(t, ok)
	assert.Equal(t, int64(5), got.requestCount, "current bucket retains its counters")
}

func TestFlushCompletedBuckets_DropsEmptyOldBucket(t *testing.T) {
	requireDB(t)
	resetHotBuckets(t)
	// Empty bucket older than 24h => drained (0 rows) then deleted from hotBuckets.
	old := bucketStart(time.Now().Add(-48*time.Hour).Unix())
	hotBuckets.Store(bucketKey{model: "flush-empty", group: "g", bucketTs: old}, &atomicBucket{})

	flushCompletedBuckets()

	_, ok := loadBucket(t, "flush-empty", "g", old)
	assert.False(t, ok, "empty >24h bucket must be removed from hotBuckets")
	rows, _ := model.GetPerfMetrics("flush-empty", "g", old-1, old+1)
	assert.Empty(t, rows, "empty bucket writes no DB row")
}

// ---------------------------------------------------------------------------
// deleteOldEmptyBucket — >24h removed, recent kept (pure hotBuckets).
// ---------------------------------------------------------------------------

func TestDeleteOldEmptyBucket(t *testing.T) {
	resetHotBuckets(t)
	oldTs := bucketStart(time.Now().Add(-48*time.Hour).Unix())
	recentTs := currentBucketTs()
	oldKey := bucketKey{model: "d", group: "g", bucketTs: oldTs}
	recentKey := bucketKey{model: "d", group: "g", bucketTs: recentTs}
	hotBuckets.Store(oldKey, &atomicBucket{})
	hotBuckets.Store(recentKey, &atomicBucket{})

	deleteOldEmptyBucket(oldKey, oldKey)
	deleteOldEmptyBucket(recentKey, recentKey)

	_, oldExists := hotBuckets.Load(oldKey)
	_, recentExists := hotBuckets.Load(recentKey)
	assert.False(t, oldExists, ">24h bucket deleted")
	assert.True(t, recentExists, "recent bucket kept")
}

// ---------------------------------------------------------------------------
// Query — merges DB rows with active hotBuckets; Hours clamping.
// ---------------------------------------------------------------------------

func TestQuery_MergesDbAndHotBuckets(t *testing.T) {
	requireDB(t)
	resetHotBuckets(t)
	cur := currentBucketTs()
	past := cur - 3600
	require.NoError(t, model.UpsertPerfMetric(&model.PerfMetric{
		ModelName: "q-model", Group: "g", BucketTs: past,
		RequestCount: 4, SuccessCount: 4, TotalLatencyMs: 400,
	}))
	seedHotBucket("q-model", "g", cur, counters{requestCount: 2, successCount: 1, totalLatencyMs: 100})

	res, err := Query(QueryParams{Model: "q-model", Group: "g", Hours: 24})
	require.NoError(t, err)
	require.Len(t, res.Groups, 1)
	grp := res.Groups[0]
	assert.Equal(t, "g", grp.Group)
	require.Len(t, grp.Series, 2, "past DB bucket + active hot bucket")
	assert.Equal(t, past, grp.Series[0].Ts)
	assert.Equal(t, cur, grp.Series[1].Ts)
	// Aggregate success rate: (4+1)/(4+2) = 83.33%.
	assert.InDelta(t, 83.33, grp.SuccessRate, 0.01)
}

func TestQuery_ClampsHours(t *testing.T) {
	requireDB(t)
	resetHotBuckets(t)
	// Hours<=0 => defaulted to 24; Hours>720 => capped. Both must succeed.
	_, err := Query(QueryParams{Model: "none", Hours: 0})
	require.NoError(t, err)
	_, err = Query(QueryParams{Model: "none", Hours: 99999})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// QuerySummaryAll — ranking + recent success rates + group filter.
// ---------------------------------------------------------------------------

func TestQuerySummaryAll_RanksByRequestCount(t *testing.T) {
	requireDB(t)
	resetHotBuckets(t)
	cur := currentBucketTs()
	require.NoError(t, model.UpsertPerfMetric(&model.PerfMetric{
		ModelName: "big", Group: "g", BucketTs: cur - 3600,
		RequestCount: 10, SuccessCount: 9, TotalLatencyMs: 1000, OutputTokens: 100, GenerationMs: 1000,
	}))
	require.NoError(t, model.UpsertPerfMetric(&model.PerfMetric{
		ModelName: "small", Group: "g", BucketTs: cur - 3600,
		RequestCount: 3, SuccessCount: 3, TotalLatencyMs: 300,
	}))

	res, err := QuerySummaryAll(24, nil)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(res.Models), 2)
	assert.Equal(t, "big", res.Models[0].ModelName, "highest request count ranks first")
	assert.InDelta(t, 90.0, res.Models[0].SuccessRate, 0.01)
	assert.NotEmpty(t, res.Models[0].RecentSuccessRates)
}

func TestQuerySummaryAll_HotBucketsAndGroupFilter(t *testing.T) {
	requireDB(t)
	resetHotBuckets(t)
	cur := currentBucketTs()
	// In-range, allowed group, with tokens => contributes and exercises avgTps>0.
	seedHotBucket("hot-allowed", "vip", cur, counters{
		requestCount: 5, successCount: 5, totalLatencyMs: 500, outputTokens: 100, generationMs: 1000,
	})
	// Allowed group but zero requests => skipped by the snap.requestCount==0 guard.
	seedHotBucket("hot-zero", "vip", cur, counters{requestCount: 0})
	// Disallowed group => filtered out by allowedGroups membership check.
	seedHotBucket("hot-blocked", "secret", cur, counters{requestCount: 9, successCount: 9})

	res, err := QuerySummaryAll(24, []string{"vip"})
	require.NoError(t, err)

	names := map[string]ModelSummary{}
	for _, m := range res.Models {
		names[m.ModelName] = m
	}
	require.Contains(t, names, "hot-allowed")
	assert.Greater(t, names["hot-allowed"].AvgTps, 0.0, "tokens present => tps computed")
	assert.NotContains(t, names, "hot-zero", "zero-request bucket excluded")
	assert.NotContains(t, names, "hot-blocked", "group not in filter excluded")
}

func TestQuerySummaryAll_EmptyGroupFilterReturnsNoModels(t *testing.T) {
	requireDB(t)
	resetHotBuckets(t)
	// A non-nil but empty group slice short-circuits the DB query to empty.
	res, err := QuerySummaryAll(24, []string{})
	require.NoError(t, err)
	assert.Empty(t, res.Models)
}

// ---------------------------------------------------------------------------
// cleanupExpiredMetrics — retention boundary.
// ---------------------------------------------------------------------------

func TestCleanupExpiredMetrics(t *testing.T) {
	requireDB(t)
	resetHotBuckets(t)
	now := time.Now()
	oldTs := now.Add(-10 * 24 * time.Hour).Unix()
	recentTs := now.Add(-1 * time.Hour).Unix()
	require.NoError(t, model.UpsertPerfMetric(&model.PerfMetric{ModelName: "c", Group: "g", BucketTs: oldTs, RequestCount: 1}))
	require.NoError(t, model.UpsertPerfMetric(&model.PerfMetric{ModelName: "c", Group: "g", BucketTs: recentTs, RequestCount: 1}))

	// retentionDays<=0 => no-op.
	cleanupExpiredMetrics(0)
	rows, _ := model.GetPerfMetrics("c", "g", oldTs-1, recentTs+1)
	assert.Len(t, rows, 2, "retention<=0 keeps everything")

	// retentionDays=7 => the 10-day-old row is purged, the 1h-old row survives.
	cleanupExpiredMetrics(7)
	rows, _ = model.GetPerfMetrics("c", "g", oldTs-1, recentTs+1)
	require.Len(t, rows, 1)
	assert.Equal(t, recentTs, rows[0].BucketTs)
}

// ---------------------------------------------------------------------------
// Init — spawns the flush loop goroutine (covers the wiring statement).
// ---------------------------------------------------------------------------

func TestInit_StartsWithoutPanic(t *testing.T) {
	assert.NotPanics(t, func() { Init() })
}
