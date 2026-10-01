package ratio_setting

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Branch audit of the ParseUserGroupRatios memo table. ParseUserGroupRatios runs
// inside TokenAuth (UserBase.WriteContext) on every relay request, so the
// counter that drives the CLOCK sweep must never drift from the real table size
// under concurrent LoadOrStore / sweep races: a counter that drifts upward
// makes every miss run an O(n) sweep; one that drifts downward disables the cap.

func memoEntryCount() int64 {
	var n int64
	parsedUserGroupRatios.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}

func TestBranchAuditParseUserGroupRatios_ConcurrentSameKeyCountedOnce(t *testing.T) {
	t.Cleanup(resetUserGroupRatioCache)
	resetUserGroupRatioCache()
	const raw = `{"audit-same":0.75}`
	const workers = 64

	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]map[string]float64, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = ParseUserGroupRatios(raw)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, got := range results {
		require.NotNil(t, got, "worker %d", i)
		assert.Equal(t, 0.75, got["audit-same"])
	}
	assert.Equal(t, int64(1), parsedUserGroupRatiosCount.Load(), "losing LoadOrStore racers must not increment the counter")
	assert.Equal(t, int64(1), memoEntryCount())
}

func TestBranchAuditParseUserGroupRatios_NoCountDriftUnderSweepContention(t *testing.T) {
	t.Cleanup(resetUserGroupRatioCache)
	resetUserGroupRatioCache()
	SetUserGroupRatioCacheMax(8)
	const workers, perWorker, distinct = 16, 300, 97

	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < perWorker; i++ {
				key := (w*perWorker + i) % distinct
				got := ParseUserGroupRatios(fmt.Sprintf(`{"audit-%d":%d}`, key, key+1))
				if assert.NotNil(t, got) {
					assert.Equal(t, float64(key+1), got[fmt.Sprintf("audit-%d", key)])
				}
			}
		}(w)
	}
	close(start)
	wg.Wait()

	assert.Equal(t, memoEntryCount(), parsedUserGroupRatiosCount.Load(), "counter drifted from the real table size")
	assert.False(t, parsedUserGroupRatiosSweep.Load(), "sweep guard must be released")

	// Two quiescent sweeps: the first clears every referenced bit, the second
	// evicts everything untouched since — the counter must land exactly on 0.
	sweepUserGroupRatioCache()
	sweepUserGroupRatioCache()
	assert.Equal(t, int64(0), memoEntryCount())
	assert.Equal(t, int64(0), parsedUserGroupRatiosCount.Load())
}

// Lowering the cap to 0 at runtime (UserExclusiveGroupRatioCacheMax option)
// stops new insertions; configs already cached keep being served unchanged.
func TestBranchAuditParseUserGroupRatios_CapLoweredToZeroAtRuntime(t *testing.T) {
	t.Cleanup(resetUserGroupRatioCache)
	resetUserGroupRatioCache()
	cached := ParseUserGroupRatios(`{"audit-a":1}`)
	require.NotNil(t, cached)
	require.Equal(t, int64(1), parsedUserGroupRatiosCount.Load())

	SetUserGroupRatioCacheMax(0)
	fresh := ParseUserGroupRatios(`{"audit-b":2}`)
	require.NotNil(t, fresh)
	assert.Equal(t, 2.0, fresh["audit-b"])
	assert.Equal(t, int64(1), parsedUserGroupRatiosCount.Load(), "cap 0 must not insert")
	_, stored := parsedUserGroupRatios.Load(`{"audit-b":2}`)
	assert.False(t, stored)

	again := ParseUserGroupRatios(`{"audit-a":1}`)
	assert.Equal(t, 1.0, again["audit-a"])
}

// Inputs that decode to an empty or nil map are never memoized, so they cannot
// consume cap slots or be shared as a mutable empty map.
func TestBranchAuditParseUserGroupRatios_EmptyDecodesNotCached(t *testing.T) {
	t.Cleanup(resetUserGroupRatioCache)
	resetUserGroupRatioCache()
	for _, raw := range []string{"null", "{ }", "{\n}", `{"a":1`, `[]`, `"x"`} {
		assert.Nil(t, ParseUserGroupRatios(raw), raw)
	}
	assert.Equal(t, int64(0), parsedUserGroupRatiosCount.Load())
	assert.Equal(t, int64(0), memoEntryCount())
}

// Content-keyed memo: an edited config is a different key, so an admin edit is
// visible on the very next parse without any explicit invalidation.
func TestBranchAuditParseUserGroupRatios_EditedConfigIsNewKey(t *testing.T) {
	t.Cleanup(resetUserGroupRatioCache)
	resetUserGroupRatioCache()
	before := ParseUserGroupRatios(`{"audit-vip":0.5}`)
	after := ParseUserGroupRatios(`{"audit-vip":0.9}`)
	assert.Equal(t, 0.5, before["audit-vip"])
	assert.Equal(t, 0.9, after["audit-vip"])
	assert.Equal(t, int64(2), parsedUserGroupRatiosCount.Load())
}
