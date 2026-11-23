package perfmetrics

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/stretchr/testify/require"
)

func TestRecordConcurrentBucketReuse(t *testing.T) {
	resetHotBuckets(t)
	require.True(t, perf_metrics_setting.GetSetting().Enabled)
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			for range 100 {
				Record(Sample{Model: "concurrent-fixture", Group: "g", Success: true, LatencyMs: 17})
			}
		})
	}
	workers.Wait()
	var count, successes, latency int64
	hotBuckets.Range(func(key, value any) bool {
		if key.(bucketKey).model == "concurrent-fixture" {
			c := value.(*atomicBucket).snapshot()
			count += c.requestCount
			successes += c.successCount
			latency += c.totalLatencyMs
		}
		return true
	})
	require.EqualValues(t, 1600, count)
	require.EqualValues(t, count, successes)
	require.EqualValues(t, 1600*17, latency)
}

func BenchmarkRecordExistingBucket(b *testing.B) {
	require.True(b, perf_metrics_setting.GetSetting().Enabled)
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	b.Cleanup(func() {
		common.RedisEnabled = oldRedis
		hotBuckets.Range(func(key, _ any) bool {
			if key.(bucketKey).model == "benchmark-collection" {
				hotBuckets.Delete(key)
			}
			return true
		})
	})
	sample := Sample{Model: "benchmark-collection", Group: "g", Success: true, LatencyMs: 17}
	Record(sample)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		Record(sample)
	}
}
