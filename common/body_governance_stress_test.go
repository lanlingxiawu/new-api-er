package common

import (
	"bytes"
	"fmt"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBodyGovernanceLongHTTP(t *testing.T) {
	if os.Getenv("BODY_MEMORY_STRESS") != "1" {
		t.Skip("opt-in local long request test")
	}
	clients, hold, payloadKB, memoryMB := 64, 5, 64, 4
	if raw := os.Getenv("BODY_STRESS_PAYLOAD_KB"); raw != "" {
		value, err := strconv.Atoi(raw)
		require.NoError(t, err)
		require.Greater(t, value, 0)
		require.LessOrEqual(t, value, 4096)
		payloadKB = value
	}
	if raw := os.Getenv("BODY_STRESS_MEMORY_MB"); raw != "" {
		value, err := strconv.Atoi(raw)
		require.NoError(t, err)
		require.Greater(t, value, 0)
		require.LessOrEqual(t, value, 512)
		memoryMB = value
	}
	if raw := os.Getenv("BODY_STRESS_CLIENTS"); raw != "" {
		value, err := strconv.Atoi(raw)
		require.NoError(t, err)
		require.GreaterOrEqual(t, value, 64)
		require.LessOrEqual(t, value, 10000)
		clients = value
	}
	// Bound the local fixture's total inbound payload to 1 GiB. This is a test
	// host safeguard, not a production concurrency or request-size setting.
	require.LessOrEqual(t, int64(clients)*int64(payloadKB), int64(1<<20))
	if raw := os.Getenv("BODY_STRESS_HOLD_SECONDS"); raw != "" {
		value, err := strconv.Atoi(raw)
		require.NoError(t, err)
		require.GreaterOrEqual(t, value, 1)
		require.LessOrEqual(t, value, 120)
		hold = value
	}
	for _, mode := range []string{"memory", "disk", "disk_exhausted"} {
		t.Run(mode, func(t *testing.T) {
			diskMB := max(16, clients*payloadKB/256)
			if mode == "disk_exhausted" {
				diskMB = 1
			}
			useTempDiskCache(t, DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: memoryMB, Enabled: mode != "memory", ThresholdMB: 0, MaxSizeMB: diskMB})
			var before, held, drained runtime.MemStats
			runtime.ReadMemStats(&before)
			ready := make(chan int, clients)
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				incoming, err := CreateBodyStorageFromReader(r.Body, -1, int64(max(1<<20, (payloadKB<<10)+1)))
				if err != nil {
					ready <- 503
					w.WriteHeader(503)
					return
				}
				defer incoming.Close()
				payload, err := io.ReadAll(incoming)
				if err != nil {
					ready <- 500
					w.WriteHeader(500)
					return
				}
				outgoing, err := CreateBodyStorage(payload)
				if err != nil {
					ready <- 503
					w.WriteHeader(503)
					return
				}
				defer outgoing.Close()
				ready <- 204
				<-release
				actual, err := io.ReadAll(outgoing)
				if err != nil || !bytes.Equal(actual, payload) {
					w.WriteHeader(500)
					return
				}
				w.WriteHeader(204)
			}))
			defer server.Close()
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			var wg sync.WaitGroup
			results := make(chan error, clients)
			for i := 0; i < clients; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					resp, err := server.Client().Post(server.URL, "application/json", strings.NewReader(strings.Repeat("x", payloadKB<<10)))
					if err == nil {
						_ = resp.Body.Close()
						if resp.StatusCode != 204 && resp.StatusCode != 503 {
							err = fmt.Errorf("status %d", resp.StatusCode)
						}
					}
					results <- err
				}()
			}
			accepted, rejected := 0, 0
			for i := 0; i < clients; i++ {
				select {
				case status := <-ready:
					if status == 204 {
						accepted++
					} else {
						require.Equal(t, 503, status)
						rejected++
					}
				case <-time.After(10 * time.Second):
					require.FailNow(t, "requests did not reach barrier")
				}
			}
			require.Positive(t, accepted)
			if mode == "memory" || mode == "disk_exhausted" {
				require.Positive(t, rejected)
			}
			for i := 0; i < hold; i++ {
				require.LessOrEqual(t, GetBodyMemoryReserved(), int64(memoryMB<<20))
				require.LessOrEqual(t, diskBytesReserved.Load(), int64(diskMB<<20))
				require.NoError(t, CleanupOldDiskCacheFiles(0))
				time.Sleep(time.Second)
			}
			runtime.ReadMemStats(&held)
			t.Logf("clients=%d payloadKiB=%d held=%ds accepted=%d rejected=%d memory=%d disk=%d heap_before=%d heap_held=%d goroutines_held=%d", clients, payloadKB, hold, accepted, rejected, GetBodyMemoryReserved(), diskBytesReserved.Load(), before.HeapAlloc, held.HeapAlloc, runtime.NumGoroutine())
			releaseOnce.Do(func() { close(release) })
			wg.Wait()
			close(results)
			for err := range results {
				require.NoError(t, err)
			}
			runtime.GC()
			require.Zero(t, GetBodyMemoryReserved())
			require.Zero(t, diskBytesReserved.Load())
			count, _, err := GetDiskCacheInfo()
			require.NoError(t, err)
			require.Zero(t, count)
			runtime.ReadMemStats(&drained)
			t.Logf("drained heap=%d memory_reserved=%d disk_reserved=%d files=%d", drained.HeapAlloc, GetBodyMemoryReserved(), diskBytesReserved.Load(), count)
		})
	}
}
