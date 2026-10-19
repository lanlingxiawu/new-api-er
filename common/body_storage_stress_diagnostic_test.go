package common

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Test fixture for overlapping request lifetimes, not the full relay handler.
type bodyMemoryRequestFixture struct {
	context  *gin.Context
	original *dto.GeminiChatRequest
	outbound BodyStorage
}

func newBodyMemoryRequestFixture(payload []byte, unknownLength bool, attempts int) (_ *bodyMemoryRequestFixture, err error) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1beta/models/test:generateContent", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	if unknownLength {
		c.Request.ContentLength = -1
	}
	fixture := &bodyMemoryRequestFixture{context: c, original: new(dto.GeminiChatRequest)}
	defer func() {
		if err != nil {
			fixture.close()
		}
	}()
	if err = UnmarshalBodyReusable(c, fixture.original); err != nil {
		return nil, err
	}
	for i := 0; i < attempts; i++ {
		if fixture.outbound != nil {
			if err = fixture.outbound.Close(); err != nil {
				return nil, err
			}
		}
		copied, copyErr := DeepCopy(fixture.original)
		if copyErr != nil {
			return nil, copyErr
		}
		encoded, encodeErr := Marshal(copied)
		if encodeErr != nil {
			return nil, encodeErr
		}
		fixture.outbound, err = CreateBodyStorage(encoded)
		if err != nil {
			return nil, err
		}
	}
	return fixture, nil
}

func (f *bodyMemoryRequestFixture) close() {
	CleanupBodyStorage(f.context)
	if f.outbound != nil {
		_ = f.outbound.Close()
	}
}

// The barrier holds all readers after the admission decision but before the
// file size is added to the global counter, deterministically exposing whether
// concurrent admissions reserve space. No timing/scheduler luck is required.
type bodyMemoryBarrierReader struct {
	reader  io.Reader
	entered chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (r *bodyMemoryBarrierReader) Read(p []byte) (int, error) {
	r.once.Do(func() {
		r.entered <- struct{}{}
		<-r.release
	})
	return r.reader.Read(p)
}

func TestBodyMemoryDiagnosticConcurrentDiskAdmission(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: 1, MaxSizeMB: 2})
	ResetDiskCacheUsage()
	const workers = 16
	entered := make(chan struct{}, workers)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	type result struct {
		storage BodyStorage
		err     error
	}
	results := make(chan result, workers)
	payload := bytes.Repeat([]byte("x"), 1<<20)
	for i := 0; i < workers; i++ {
		go func() {
			r := &bodyMemoryBarrierReader{reader: bytes.NewReader(payload), entered: entered, release: release}
			storage, err := CreateBodyStorageFromReader(r, int64(len(payload)), 2<<20)
			results <- result{storage, err}
		}()
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	admitted := 0
waiting:
	for admitted < workers {
		select {
		case <-entered:
			admitted++
		case <-deadline.C:
			break waiting
		}
	}
	releaseOnce.Do(func() { close(release) })
	var retained []BodyStorage
	t.Cleanup(func() {
		for _, storage := range retained {
			require.NoError(t, storage.Close())
		}
	})
	for i := 0; i < workers; i++ {
		result := <-results
		if result.storage != nil {
			retained = append(retained, result.storage)
		}
		require.NoError(t, result.err)
	}
	require.Equal(t, workers, admitted)
	for _, storage := range retained {
		require.True(t, storage.IsDisk())
	}
	require.Equal(t, int64(workers<<20), GetDiskCacheStats().CurrentDiskUsageBytes)
	t.Log("16 simultaneous 1 MiB bodies all admitted to a 2 MiB disk budget: usage=16 MiB (8x configured budget)")
}

func TestBodyMemoryStressRequestLifetimes(t *testing.T) {
	if os.Getenv("BODY_MEMORY_STRESS") != "1" {
		t.Skip("set BODY_MEMORY_STRESS=1 for bounded multi-wave diagnostics")
	}
	for _, tc := range []struct {
		name      string
		threshold int
		budget    int
		workers   int
		rounds    int
		bodySize  int
		attempts  int
	}{
		{"completed_10000_small_requests", 10, 128, 1, 10000, 8 << 10, 3},
		{"memory_20_waves_32_concurrent", 10, 128, 32, 20, 256 << 10, 2},
		{"disk_20_waves_32_concurrent", 0, 128, 32, 20, 256 << 10, 2},
		{"disk_full_20_waves_32_concurrent", 0, 1, 32, 20, 256 << 10, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cacheDir := useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: tc.threshold, MaxSizeMB: tc.budget})
			ResetDiskCacheUsage()
			baseline := GetDiskCacheStats()
			payload := []byte(`{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"` + strings.Repeat("A", tc.bodySize) + `"}}]}]}`)
			runtime.GC()
			runtime.GC()
			var before, current runtime.MemStats
			runtime.ReadMemStats(&before)
			goroutines := runtime.NumGoroutine()
			var peakHeap uint64
			var peakBuffers int64
			// No forced GC during the request waves: retain normal GC behavior.
			for round := 1; round <= tc.rounds; round++ {
				type result struct {
					fixture *bodyMemoryRequestFixture
					err     error
				}
				results := make(chan result, tc.workers)
				for worker := 0; worker < tc.workers; worker++ {
					unknownLength := (round+worker)%2 == 0
					go func() {
						fixture, err := newBodyMemoryRequestFixture(payload, unknownLength, tc.attempts)
						results <- result{fixture, err}
					}()
				}
				retained := make([]*bodyMemoryRequestFixture, 0, tc.workers)
				var errors []error
				for worker := 0; worker < tc.workers; worker++ {
					result := <-results
					if result.err != nil {
						errors = append(errors, result.err)
					} else {
						retained = append(retained, result.fixture)
					}
				}
				runtime.ReadMemStats(&current)
				peakHeap = max(peakHeap, current.HeapAlloc)
				stats := GetDiskCacheStats()
				peakBuffers = max(peakBuffers, stats.ActiveMemoryBuffers-baseline.ActiveMemoryBuffers)
				for _, fixture := range retained {
					fixture.close()
				}
				retained = nil
				require.Empty(t, errors)
				require.Equal(t, baseline.CurrentMemoryUsageBytes, GetDiskCacheStats().CurrentMemoryUsageBytes)
				require.Equal(t, baseline.ActiveMemoryBuffers, GetDiskCacheStats().ActiveMemoryBuffers)
				require.Zero(t, GetDiskCacheStats().CurrentDiskUsageBytes)
				require.Zero(t, GetDiskCacheStats().ActiveDiskFiles)
				if round == 1 || round%(tc.rounds/4) == 0 {
					t.Logf("completed=%d natural-GC heap=%.2f MiB, observed peak=%.2f MiB, GC cycles=%d", round*tc.workers, float64(current.HeapAlloc)/(1<<20), float64(peakHeap)/(1<<20), current.NumGC-before.NumGC)
				}
			}
			runtime.GC()
			runtime.GC()
			runtime.ReadMemStats(&current)
			_, diskBytes, err := GetDiskCacheInfo()
			require.NoError(t, err)
			require.Zero(t, diskBytes, "temp cache files must be removed: %s", cacheDir)
			delta := int64(current.HeapAlloc) - int64(before.HeapAlloc)
			// A generous bound catches persistent payload retention without
			// depending on exact runtime/allocator/platform overheads.
			require.Less(t, delta, int64(16<<20), "completed requests retained excessive heap")
			t.Logf("requests=%d attempts=%d final heap delta=%.2f MiB, total allocated=%.2f MiB, peak buffers=%d, goroutine delta=%d", tc.workers*tc.rounds, tc.attempts, float64(delta)/(1<<20), float64(current.TotalAlloc-before.TotalAlloc)/(1<<20), peakBuffers, runtime.NumGoroutine()-goroutines)
			runtime.KeepAlive(payload)
		})
	}
}

func TestBodyMemoryDiagnosticFailedReadsDoNotAccumulate(t *testing.T) {
	for _, disk := range []bool{false, true} {
		t.Run(fmt.Sprintf("disk_%t", disk), func(t *testing.T) {
			useTempDiskCache(t, DiskCacheConfig{Enabled: disk, ThresholdMB: 0, MaxSizeMB: 8})
			ResetDiskCacheUsage()
			baseline := GetDiskCacheStats()
			for i := 0; i < 100; i++ {
				_, err := CreateBodyStorageFromReader(strings.NewReader("oversized"), 9, 4)
				require.ErrorIs(t, err, ErrRequestBodyTooLarge)
			}
			count, size, err := GetDiskCacheInfo()
			require.NoError(t, err)
			require.Zero(t, count)
			require.Zero(t, size)
			require.Equal(t, baseline.CurrentMemoryUsageBytes, GetDiskCacheStats().CurrentMemoryUsageBytes)
			require.Zero(t, GetDiskCacheStats().CurrentDiskUsageBytes)
		})
	}
}

func TestBodyMemoryDiagnosticTruncatedErrorLosesStructuredFields(t *testing.T) {
	// Validate the proposed LimitReader-before-JSON-parse behavior without
	// invoking service TestMain (which provisions real databases).
	encoded, err := Marshal(map[string]any{
		"error":   map[string]string{"message": "quota exceeded", "type": "insufficient_quota"},
		"padding": strings.Repeat("x", 300<<10),
	})
	require.NoError(t, err)
	var full dto.GeneralErrorResponse
	require.NoError(t, Unmarshal(encoded, &full))
	require.Equal(t, "object", GetJsonType(full.Error))
	limited, err := io.ReadAll(io.LimitReader(bytes.NewReader(encoded), 256<<10))
	require.NoError(t, err)
	var truncated dto.GeneralErrorResponse
	require.Error(t, Unmarshal(limited, &truncated))
	t.Log("valid >256 KiB quota error becomes invalid JSON after truncation; structured-error branch cannot be preserved automatically")
}

func TestBodyMemoryStressLongHTTPRequests(t *testing.T) {
	if os.Getenv("BODY_MEMORY_STRESS") != "1" {
		t.Skip("set BODY_MEMORY_STRESS=1; this local HTTP test holds requests for 30 seconds per mode")
	}
	// A local HTTP fixture holds parsed requests and outbound bodies while
	// awaiting a simulated upstream. It does not run real relay routing,
	// billing, timeout controllers, response transforms or external APIs.
	for _, tc := range []struct {
		name      string
		threshold int
		budget    int
	}{
		{"memory", 10, 256},
		{"disk", 1, 256},
		{"disk_exhausted", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: tc.threshold, MaxSizeMB: tc.budget})
			ResetDiskCacheUsage()
			baseline := GetDiskCacheStats()
			const requests = 64
			ready := make(chan error, requests)
			release := make(chan struct{}, requests)
			finished := make(chan error, requests)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				c, _ := gin.CreateTestContext(w)
				c.Request = req
				defer CleanupBodyStorage(c)
				original := new(dto.GeminiChatRequest)
				if err := UnmarshalBodyReusable(c, original); err != nil {
					ready <- err
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				encoded, err := Marshal(original)
				if err != nil {
					ready <- err
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				outbound, err := CreateBodyStorage(encoded)
				if err != nil {
					ready <- err
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				defer outbound.Close()
				encoded = nil
				ready <- nil
				<-release // represent an upstream that has not completed yet
				runtime.KeepAlive(original)
				runtime.KeepAlive(outbound)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			defer close(release) // release handlers before closing the server, including on failure
			client := server.Client()
			client.Timeout = 90 * time.Second
			defer client.CloseIdleConnections()
			payload := []byte(`{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"` + strings.Repeat("A", 1<<20) + `"}}]}]}`)
			runtime.GC()
			runtime.GC()
			var before, current runtime.MemStats
			runtime.ReadMemStats(&before)
			previous := 0
			for _, target := range []int{8, 32, 64} {
				for i := previous; i < target; i++ {
					go func() {
						resp, err := client.Post(server.URL, "application/json", bytes.NewReader(payload))
						if err == nil {
							_ = resp.Body.Close()
							if resp.StatusCode != http.StatusNoContent {
								err = fmt.Errorf("fixture returned HTTP %d", resp.StatusCode)
							}
						}
						finished <- err
					}()
				}
				for i := previous; i < target; i++ {
					select {
					case err := <-ready:
						require.NoError(t, err)
					case <-time.After(20 * time.Second):
						require.FailNow(t, "local HTTP fixtures did not become ready")
					}
				}
				runtime.GC()
				runtime.GC()
				runtime.ReadMemStats(&current)
				stats := GetDiskCacheStats()
				t.Logf("inflight=%d post-GC heap delta=%.2f MiB cache memory=%.2f MiB disk=%.2f MiB", target, float64(int64(current.HeapAlloc)-int64(before.HeapAlloc))/(1<<20), float64(stats.CurrentMemoryUsageBytes-baseline.CurrentMemoryUsageBytes)/(1<<20), float64(stats.CurrentDiskUsageBytes)/(1<<20))
				previous = target
			}
			// No forced GC while waiting: distinguish elapsed time from an
			// increasing number of retained requests.
			for checkpoint := 1; checkpoint <= 3; checkpoint++ {
				time.Sleep(10 * time.Second)
				runtime.ReadMemStats(&current)
				t.Logf("inflight=64 held=%ds heap delta=%.2f MiB", checkpoint*10, float64(int64(current.HeapAlloc)-int64(before.HeapAlloc))/(1<<20))
			}
			for _, remaining := range []int{32, 0} {
				for i := 0; i < 32; i++ {
					release <- struct{}{}
				}
				for i := 0; i < 32; i++ {
					require.NoError(t, <-finished)
				}
				runtime.GC()
				runtime.GC()
				runtime.ReadMemStats(&current)
				t.Logf("after drain inflight=%d post-GC heap delta=%.2f MiB", remaining, float64(int64(current.HeapAlloc)-int64(before.HeapAlloc))/(1<<20))
			}
			require.Equal(t, baseline.CurrentMemoryUsageBytes, GetDiskCacheStats().CurrentMemoryUsageBytes)
			require.Equal(t, baseline.ActiveMemoryBuffers, GetDiskCacheStats().ActiveMemoryBuffers)
			require.Zero(t, GetDiskCacheStats().CurrentDiskUsageBytes)
			require.Zero(t, GetDiskCacheStats().ActiveDiskFiles)
			runtime.KeepAlive(payload)
		})
	}
}
