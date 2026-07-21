package perfmetrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// currentBucketTs returns the bucket timestamp Record would compute right now.
func currentBucketTs() int64 { return bucketStart(time.Now().Unix()) }

// ---------------------------------------------------------------------------
// atomicBucket.add — conditional-branch matrix. Each guard (Success,
// LatencyMs>0, HasTtft&&TtftMs>=0, OutputTokens>0&&GenerationMs>0) is exercised
// on and off, verified through snapshot().
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// avg / successRate / avgTps — boundary + condition coverage.
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// merge helpers — requestCount==0 guard + accumulation.
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// recentSuccessRates — empty / limit<=0 / tail-of-latest.
// ---------------------------------------------------------------------------

func TestRecentSuccessRates_EmptyOrZeroLimit(t *testing.T) {
	assert.Nil(t, recentSuccessRates(map[int64]counters{}, 3))
	assert.Nil(t, recentSuccessRates(map[int64]counters{1: {requestCount: 1}}, 0))
}

func TestRecentSuccessRates_KeepsLatestSortedByTs(t *testing.T) {
	buckets := map[int64]counters{
		100: {requestCount: 4, successCount: 4}, // 100%
		200: {requestCount: 4, successCount: 2}, // 50%
		300: {requestCount: 4, successCount: 1}, // 25%
		400: {requestCount: 4, successCount: 3}, // 75%
	}
	// limit 3 => drop the oldest (ts=100), keep 200,300,400 in ascending order.
	got := recentSuccessRates(buckets, 3)
	assert.Equal(t, []float64{50, 25, 75}, got)
}

// ---------------------------------------------------------------------------
// allowedGroupSet — nil passthrough vs. set membership.
// ---------------------------------------------------------------------------

func TestAllowedGroupSet(t *testing.T) {
	assert.Nil(t, allowedGroupSet(nil), "nil groups => nil (no filter)")
	set := allowedGroupSet([]string{"a", "b"})
	require.NotNil(t, set)
	_, hasA := set["a"]
	_, hasC := set["c"]
	assert.True(t, hasA)
	assert.False(t, hasC)
}

// ---------------------------------------------------------------------------
// bucketStart — aligns to the hour (default 3600s bucket).
// ---------------------------------------------------------------------------

func TestBucketStart_AlignsToBucket(t *testing.T) {
	// Default bucket_time "hour" => 3600s. 7325 => 7200; exact multiple stays.
	assert.Equal(t, int64(7200), bucketStart(7325))
	assert.Equal(t, int64(7200), bucketStart(7200))
	assert.Equal(t, int64(0), bucketStart(59))
}

// ---------------------------------------------------------------------------
// bucketPoint + buildQueryResult — aggregation, group sorting, series ordering.
// ---------------------------------------------------------------------------

func TestBucketPoint(t *testing.T) {
	p := bucketPoint(3600, counters{
		requestCount: 2, successCount: 1, totalLatencyMs: 200,
		ttftSumMs: 40, ttftCount: 2, outputTokens: 100, generationMs: 1000,
	})
	assert.Equal(t, int64(3600), p.Ts)
	assert.Equal(t, int64(20), p.AvgTtftMs)    // 40/2
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

// ---------------------------------------------------------------------------
// redisBucketKey / redisCounters / parseRedisInt — pure serialization.
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Record — in-memory path (setting enabled by default; Redis disabled in tests).
// Uses unique model names + white-box cleanup so hotBuckets stays isolated.
// ---------------------------------------------------------------------------

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
