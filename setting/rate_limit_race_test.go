package setting

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Regression for D7/F9: UpdateModelRequestRateLimitGroupByJSONString must hold
// the WRITE lock. It reassigns+populates the shared ModelRequestRateLimitGroup
// map, so running it under RLock concurrently with GetGroupRateLimit readers
// (the relay hot path, also under RLock) is a concurrent map read+write that Go
// aborts with an UNRECOVERABLE `fatal error: concurrent map read and map write`
// — detected by the runtime even WITHOUT the -race detector (which is
// unavailable in this environment). This test hammers readers+writers
// concurrently: with the correct Lock() it completes cleanly; if the lock ever
// regresses to RLock, the concurrent access crashes the whole test binary.
func TestUpdateModelRequestRateLimitGroup_ConcurrentReadWriteNoFatal(t *testing.T) {
	orig := ModelRequestRateLimitGroup
	t.Cleanup(func() { ModelRequestRateLimitGroup = orig })

	stop := make(chan struct{})
	var wg sync.WaitGroup

	payloads := []string{
		`{"vip":[100,50],"default":[10,5]}`,
		`{"a":[1,1]}`,
		`{}`,
		`{"g1":[3,2],"g2":[4,3],"g3":[5,4]}`,
	}
	// Writers continuously replace the map.
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = UpdateModelRequestRateLimitGroupByJSONString(payloads[n%len(payloads)])
				}
			}
		}(w)
	}
	// Readers continuously read (relay hot path).
	for r := 0; r < 32; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _, _ = GetGroupRateLimit("vip")
					_, _, _ = GetGroupRateLimit("default")
				}
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()

	// Sanity: the write path still applies correctly under the write lock.
	err := UpdateModelRequestRateLimitGroupByJSONString(`{"final":[7,6]}`)
	assert.NoError(t, err)
	total, success, found := GetGroupRateLimit("final")
	assert.True(t, found)
	assert.Equal(t, 7, total)
	assert.Equal(t, 6, success)
}
