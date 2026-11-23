package perfmetrics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedisDeltaMergesSamplesForOneBucket(t *testing.T) {
	key := bucketKey{model: t.Name(), group: "g", bucketTs: 1}
	perfRedisMu.Lock()
	defer perfRedisMu.Unlock()
	t.Cleanup(func() {
		perfRedisMu.Lock()
		delete(perfRedisPending, key)
		perfRedisMu.Unlock()
	})

	current := perfRedisPending[key]
	current.add(redisDeltaFrom(Sample{
		Success: true, LatencyMs: 10, HasTtft: true, TtftMs: 4, OutputTokens: 3, GenerationMs: 8,
	}))
	current.add(redisDeltaFrom(Sample{Success: false, LatencyMs: 5}))
	perfRedisPending[key] = current

	got := perfRedisPending[key]
	require.EqualValues(t, 2, got.req)
	require.EqualValues(t, 1, got.ok)
	require.EqualValues(t, 15, got.lat)
	require.EqualValues(t, 4, got.ttft)
	require.EqualValues(t, 1, got.ttftN)
	require.EqualValues(t, 3, got.out)
	require.EqualValues(t, 8, got.genMs)
	delete(perfRedisPending, key)
}
