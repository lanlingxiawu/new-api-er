package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// perf_metric.go — PerfMetric upsert/query/aggregate/delete helpers.
//
// Isolation strategy: every test uses a unique group AND a unique, far-future
// bucket_ts window (year ~2033+) derived from nextTestID(), so range queries
// hit only this test's rows and never collide with real data.

func perfBaseTs() int64 { return int64(2_000_000_000) + int64(nextTestID()) }

func cleanupPerfByModel(t *testing.T, modelName string) {
	t.Helper()
	t.Cleanup(func() {
		if DB != nil {
			DB.Where("model_name = ?", modelName).Delete(&PerfMetric{})
		}
	})
}

func TestPerfMetric_TableName(t *testing.T) {
	assert.Equal(t, "perf_metrics", PerfMetric{}.TableName())
}

func TestUpsertPerfMetric_NilAndZeroAreNoOps(t *testing.T) {
	requireDB(t)
	// nil metric
	assert.NoError(t, UpsertPerfMetric(nil))
	// RequestCount == 0 -> skipped (no row created)
	model := uniq("perfzero")
	cleanupPerfByModel(t, model)
	assert.NoError(t, UpsertPerfMetric(&PerfMetric{
		ModelName: model, Group: "g", BucketTs: perfBaseTs(), RequestCount: 0,
	}))
	var count int64
	require.NoError(t, DB.Model(&PerfMetric{}).Where("model_name = ?", model).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestUpsertPerfMetric_InsertThenConflictAccumulates(t *testing.T) {
	requireDB(t)
	model := uniq("perfups")
	group := uniq("g")
	ts := perfBaseTs()
	cleanupPerfByModel(t, model)

	require.NoError(t, UpsertPerfMetric(&PerfMetric{
		ModelName: model, Group: group, BucketTs: ts,
		RequestCount: 10, SuccessCount: 8, TotalLatencyMs: 1000,
		TtftSumMs: 200, TtftCount: 8, OutputTokens: 500, GenerationMs: 300,
	}))

	// same (model, group, bucket) -> OnConflict adds deltas
	require.NoError(t, UpsertPerfMetric(&PerfMetric{
		ModelName: model, Group: group, BucketTs: ts,
		RequestCount: 5, SuccessCount: 4, TotalLatencyMs: 500,
		TtftSumMs: 100, TtftCount: 4, OutputTokens: 250, GenerationMs: 150,
	}))

	var got PerfMetric
	require.NoError(t, DB.Where("model_name = ? AND "+commonGroupCol+" = ? AND bucket_ts = ?", model, group, ts).First(&got).Error)
	assert.Equal(t, int64(15), got.RequestCount)
	assert.Equal(t, int64(12), got.SuccessCount)
	assert.Equal(t, int64(1500), got.TotalLatencyMs)
	assert.Equal(t, int64(300), got.TtftSumMs)
	assert.Equal(t, int64(12), got.TtftCount)
	assert.Equal(t, int64(750), got.OutputTokens)
	assert.Equal(t, int64(450), got.GenerationMs)

	// a different group at the same (model,bucket) is a distinct row
	group2 := uniq("g")
	require.NoError(t, UpsertPerfMetric(&PerfMetric{
		ModelName: model, Group: group2, BucketTs: ts, RequestCount: 1,
	}))
	var rows int64
	require.NoError(t, DB.Model(&PerfMetric{}).Where("model_name = ?", model).Count(&rows).Error)
	assert.Equal(t, int64(2), rows)
}

func TestGetPerfMetrics_RangeAndGroupFilter(t *testing.T) {
	requireDB(t)
	model := uniq("perfget")
	group := uniq("g")
	other := uniq("g")
	ts := perfBaseTs()
	cleanupPerfByModel(t, model)

	require.NoError(t, UpsertPerfMetric(&PerfMetric{ModelName: model, Group: group, BucketTs: ts, RequestCount: 1}))
	require.NoError(t, UpsertPerfMetric(&PerfMetric{ModelName: model, Group: group, BucketTs: ts + 60, RequestCount: 2}))
	require.NoError(t, UpsertPerfMetric(&PerfMetric{ModelName: model, Group: other, BucketTs: ts + 120, RequestCount: 3}))

	// no group filter -> all three, ordered by bucket_ts ASC
	all, err := GetPerfMetrics(model, "", ts-1, ts+1000)
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, ts, all[0].BucketTs)
	assert.Equal(t, ts+60, all[1].BucketTs)
	assert.Equal(t, ts+120, all[2].BucketTs)

	// group filter -> only matching group's rows
	only, err := GetPerfMetrics(model, group, ts-1, ts+1000)
	require.NoError(t, err)
	assert.Len(t, only, 2)

	// range excludes out-of-window rows
	narrow, err := GetPerfMetrics(model, "", ts-1, ts+1)
	require.NoError(t, err)
	assert.Len(t, narrow, 1)
}

func TestGetPerfMetricsSummaryAll(t *testing.T) {
	requireDB(t)
	modelA := uniq("perfsumA")
	modelB := uniq("perfsumB")
	group := uniq("g")
	ts := perfBaseTs()
	cleanupPerfByModel(t, modelA)
	cleanupPerfByModel(t, modelB)

	// modelA in two buckets, modelB in one — all in same narrow window/group
	require.NoError(t, UpsertPerfMetric(&PerfMetric{ModelName: modelA, Group: group, BucketTs: ts, RequestCount: 10, SuccessCount: 9, TotalLatencyMs: 100, OutputTokens: 50, GenerationMs: 30}))
	require.NoError(t, UpsertPerfMetric(&PerfMetric{ModelName: modelA, Group: group, BucketTs: ts + 60, RequestCount: 5, SuccessCount: 5, TotalLatencyMs: 50, OutputTokens: 25, GenerationMs: 15}))
	require.NoError(t, UpsertPerfMetric(&PerfMetric{ModelName: modelB, Group: group, BucketTs: ts, RequestCount: 2, SuccessCount: 1, TotalLatencyMs: 20, OutputTokens: 10, GenerationMs: 5}))

	// nil groups -> no group filter; scope by narrow ts window + our group only
	// (use group filter to isolate against unrelated production rows)
	sums, err := GetPerfMetricsSummaryAll(ts-1, ts+1000, []string{group})
	require.NoError(t, err)
	byModel := map[string]PerfMetricSummary{}
	for _, s := range sums {
		byModel[s.ModelName] = s
	}
	require.Contains(t, byModel, modelA)
	require.Contains(t, byModel, modelB)
	assert.Equal(t, int64(15), byModel[modelA].RequestCount)
	assert.Equal(t, int64(14), byModel[modelA].SuccessCount)
	assert.Equal(t, int64(150), byModel[modelA].TotalLatencyMs)
	assert.Equal(t, int64(75), byModel[modelA].OutputTokens)
	assert.Equal(t, int64(45), byModel[modelA].GenerationMs)
	assert.Equal(t, int64(2), byModel[modelB].RequestCount)

	// empty (non-nil) groups slice -> short-circuit, empty result
	empty, err := GetPerfMetricsSummaryAll(ts-1, ts+1000, []string{})
	require.NoError(t, err)
	assert.Empty(t, empty)

	// group filter that matches nothing -> empty
	none, err := GetPerfMetricsSummaryAll(ts-1, ts+1000, []string{uniq("nogroup")})
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestGetPerfMetricsSummaryBucketsAll(t *testing.T) {
	requireDB(t)
	model := uniq("perfbkt")
	group := uniq("g")
	ts := perfBaseTs()
	cleanupPerfByModel(t, model)

	require.NoError(t, UpsertPerfMetric(&PerfMetric{ModelName: model, Group: group, BucketTs: ts + 60, RequestCount: 5}))
	require.NoError(t, UpsertPerfMetric(&PerfMetric{ModelName: model, Group: group, BucketTs: ts, RequestCount: 3}))

	buckets, err := GetPerfMetricsSummaryBucketsAll(ts-1, ts+1000, []string{group})
	require.NoError(t, err)
	require.Len(t, buckets, 2)
	// ordered by bucket_ts ASC
	assert.Equal(t, ts, buckets[0].BucketTs)
	assert.Equal(t, ts+60, buckets[1].BucketTs)
	assert.Equal(t, int64(3), buckets[0].RequestCount)
	assert.Equal(t, int64(5), buckets[1].RequestCount)

	// empty groups -> short-circuit
	empty, err := GetPerfMetricsSummaryBucketsAll(ts-1, ts+1000, []string{})
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestDeletePerfMetricsBefore(t *testing.T) {
	requireDB(t)
	model := uniq("perfdel")
	group := uniq("g")
	ts := perfBaseTs()
	cleanupPerfByModel(t, model)

	require.NoError(t, UpsertPerfMetric(&PerfMetric{ModelName: model, Group: group, BucketTs: ts, RequestCount: 1}))
	require.NoError(t, UpsertPerfMetric(&PerfMetric{ModelName: model, Group: group, BucketTs: ts + 100, RequestCount: 1}))

	// cutoff <= 0 -> no-op
	require.NoError(t, DeletePerfMetricsBefore(0))
	require.NoError(t, DeletePerfMetricsBefore(-5))
	var count int64
	require.NoError(t, DB.Model(&PerfMetric{}).Where("model_name = ?", model).Count(&count).Error)
	assert.Equal(t, int64(2), count)

	// delete rows with bucket_ts < cutoff (strictly less)
	require.NoError(t, DeletePerfMetricsBefore(ts+100))
	require.NoError(t, DB.Model(&PerfMetric{}).Where("model_name = ?", model).Count(&count).Error)
	assert.Equal(t, int64(1), count)

	var remaining PerfMetric
	require.NoError(t, DB.Where("model_name = ?", model).First(&remaining).Error)
	assert.Equal(t, ts+100, remaining.BucketTs)
}

func TestPerfMetricStartTime(t *testing.T) {
	now := time.Now()

	// hours <= 0 defaults to 24h
	def := PerfMetricStartTime(0)
	assert.InDelta(t, now.Add(-24*time.Hour).Unix(), def, 5)
	defNeg := PerfMetricStartTime(-3)
	assert.InDelta(t, now.Add(-24*time.Hour).Unix(), defNeg, 5)

	// positive hours honored
	got := PerfMetricStartTime(1)
	assert.InDelta(t, now.Add(-1*time.Hour).Unix(), got, 5)
}
