package perf_metrics_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// White-box: the tests mutate the unexported perfMetricsSetting var and restore
// it, so they are order-independent.

func withBucketTime(t *testing.T, v string) {
	t.Helper()
	orig := perfMetricsSetting.BucketTime
	perfMetricsSetting.BucketTime = v
	t.Cleanup(func() { perfMetricsSetting.BucketTime = orig })
}

func withFlushInterval(t *testing.T, v int) {
	t.Helper()
	orig := perfMetricsSetting.FlushInterval
	perfMetricsSetting.FlushInterval = v
	t.Cleanup(func() { perfMetricsSetting.FlushInterval = orig })
}

func TestGetSetting_ReturnsCurrentValues(t *testing.T) {
	got := GetSetting()
	// Defaults from the package var (Enabled true, hour bucket, flush 5).
	assert.Equal(t, perfMetricsSetting.Enabled, got.Enabled)
	assert.Equal(t, perfMetricsSetting.BucketTime, got.BucketTime)
	assert.Equal(t, perfMetricsSetting.FlushInterval, got.FlushInterval)
	assert.Equal(t, perfMetricsSetting.RetentionDays, got.RetentionDays)
}

func TestGetBucketSeconds(t *testing.T) {
	cases := map[string]int64{
		"minute":  60,
		"5min":    300,
		"hour":    3600,
		"":        3600, // default branch
		"unknown": 3600, // default branch
	}
	for bucketTime, want := range cases {
		t.Run(bucketTime, func(t *testing.T) {
			withBucketTime(t, bucketTime)
			assert.Equal(t, want, GetBucketSeconds())
		})
	}
}

func TestGetFlushIntervalMinutes(t *testing.T) {
	cases := map[int]int{
		0:  1,  // <1 => clamped to 1
		-3: 1,  // negative => 1
		1:  1,  // boundary
		5:  5,  // normal
		30: 30, // normal
	}
	for in, want := range cases {
		withFlushInterval(t, in)
		assert.Equal(t, want, GetFlushIntervalMinutes(), "flush interval %d", in)
	}
}
