package common

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// These are bounded characterization tests for the OOM investigation, not a
// claim that the observed fallback/retention behavior is the desired policy.
// They use only temporary files and synthetic bodies, with no DB or upstream.
func TestBodyMemoryDiagnosticDiskBudgetFallback(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: 1, MaxSizeMB: 2})
	ResetDiskCacheUsage()
	baseline := GetDiskCacheStats()
	var retained []BodyStorage
	t.Cleanup(func() {
		for _, storage := range retained {
			require.NoError(t, storage.Close())
		}
	})
	// Exactly at the threshold; exactly fill the disk budget, then exceed it.
	for i := 0; i < 6; i++ {
		storage, err := CreateBodyStorageFromReader(strings.NewReader(strings.Repeat("a", 1<<20)), 1<<20, 2<<20)
		require.NoError(t, err)
		retained = append(retained, storage)
		require.Equal(t, i < 2, storage.IsDisk())
	}
	stats := GetDiskCacheStats()
	require.Equal(t, int64(2<<20), stats.CurrentDiskUsageBytes)
	require.Equal(t, int64(4<<20), stats.CurrentMemoryUsageBytes-baseline.CurrentMemoryUsageBytes)
	t.Log("6 x 1 MiB bodies with 2 MiB disk budget: disk=2 MiB, memory fallback=4 MiB, no admission error")
}

func TestBodyMemoryDiagnosticCloseReleasesBackingArray(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: false})
	baseline := GetDiskCacheStats()
	storage := newMemoryStorage(bytes.Repeat([]byte("x"), 4<<20))
	require.NoError(t, storage.Close())
	require.Equal(t, baseline.CurrentMemoryUsageBytes, GetDiskCacheStats().CurrentMemoryUsageBytes)
	// The storage remains referenced, but its own payload references are gone.
	runtime.GC()
	require.Nil(t, storage.data)
	require.Nil(t, storage.reader)
	t.Log("after Close: referenced storage releases its internal backing-array references")
	runtime.KeepAlive(storage)
}

func TestBodyMemoryDiagnosticDiskBytesCopies(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: 1, MaxSizeMB: 16})
	ResetDiskCacheUsage()
	baseline := GetDiskCacheStats()
	storage, err := CreateBodyStorageFromReader(strings.NewReader(strings.Repeat("b", 4<<20)), 4<<20, 8<<20)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, storage.Close()) })
	require.True(t, storage.IsDisk())
	first, err := storage.Bytes()
	require.NoError(t, err)
	second, err := storage.Bytes()
	require.NoError(t, err)
	require.Len(t, first, 4<<20)
	require.Len(t, second, 4<<20)
	first[0] = 'c'
	require.Equal(t, byte('b'), second[0], "each call owns a separate full-sized array")
	require.Equal(t, baseline.CurrentMemoryUsageBytes, GetDiskCacheStats().CurrentMemoryUsageBytes)
	t.Log("two Bytes calls on 4 MiB disk body: 8 MiB independent arrays, memory-cache accounting delta=0")
}

func TestBodyMemoryDiagnosticGeminiWorkingSet(t *testing.T) {
	// Exercise the actual reusable-body decoder, Gemini DTO, DeepCopy,
	// marshaler and outbound storage, without executing the relay hot path.
	// Holding these objects models overlapping request lifetimes; it is not
	// a production concurrency/latency benchmark or a whole-handler profile.
	// Compare Linux results with Linux production: Windows file handles can
	// retain read/write buffers and materially change the disk-backed result.
	for _, tc := range []struct {
		name      string
		threshold int
		budget    int
	}{
		{"memory_threshold_10MiB", 10, 128},
		{"disk_threshold_1MiB", 1, 128},
		{"disk_budget_exhausted", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: tc.threshold, MaxSizeMB: tc.budget})
			ResetDiskCacheUsage()
			payload := []byte(`{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"` + strings.Repeat("A", 4<<20) + `"}}]}]}`)
			type liveRequest struct {
				context  *gin.Context
				original *dto.GeminiChatRequest
				copied   *dto.GeminiChatRequest
				outbound BodyStorage
			}
			retained := make([]liveRequest, 0, 12)
			t.Cleanup(func() {
				for _, req := range retained {
					CleanupBodyStorage(req.context)
					require.NoError(t, req.outbound.Close())
				}
			})
			runtime.GC()
			runtime.GC() // remove pooled JSON buffers from earlier subtests
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			baseline := GetDiskCacheStats()
			var copyAlloc uint64
			for i := 0; i < cap(retained); i++ {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/v1beta/models/test:generateContent", bytes.NewReader(payload))
				c.Request.Header.Set("Content-Type", "application/json")
				original := new(dto.GeminiChatRequest)
				require.NoError(t, UnmarshalBodyReusable(c, original))
				var copyBefore, copyAfter runtime.MemStats
				runtime.ReadMemStats(&copyBefore)
				copied, err := DeepCopy(original)
				require.NoError(t, err)
				runtime.ReadMemStats(&copyAfter)
				copyAlloc += copyAfter.TotalAlloc - copyBefore.TotalAlloc
				encoded, err := Marshal(copied)
				require.NoError(t, err)
				outbound, err := CreateBodyStorage(encoded)
				require.NoError(t, err)
				retained = append(retained, liveRequest{c, original, copied, outbound})
			}
			runtime.GC()
			runtime.GC()
			runtime.ReadMemStats(&after)
			stats := GetDiskCacheStats()
			wantDisk := tc.threshold == 1 && tc.budget == 128
			for _, req := range retained {
				require.Equal(t, wantDisk, req.outbound.IsDisk())
				require.Len(t, req.original.Contents[0].Parts[0].InlineData.Data, 4<<20)
			}
			t.Logf("12 x 4 MiB Gemini bodies: live heap delta=%.2f MiB, cumulative allocations=%.2f MiB, memory cache=%.2f MiB (%d buffers), disk=%.2f MiB; DeepCopy cumulative allocations=%.3f MiB",
				float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/(1<<20),
				float64(after.TotalAlloc-before.TotalAlloc)/(1<<20),
				float64(stats.CurrentMemoryUsageBytes-baseline.CurrentMemoryUsageBytes)/(1<<20),
				stats.ActiveMemoryBuffers-baseline.ActiveMemoryBuffers,
				float64(stats.CurrentDiskUsageBytes)/(1<<20), float64(copyAlloc)/(1<<20))
			// Optional local-only evidence capture while all modeled requests
			// remain live. The directory must already exist.
			if dir := os.Getenv("BODY_MEMORY_PROFILE_DIR"); dir != "" {
				profile, err := os.Create(filepath.Join(dir, tc.name+".pprof"))
				require.NoError(t, err)
				require.NoError(t, pprof.WriteHeapProfile(profile))
				require.NoError(t, profile.Close())
			}
			runtime.KeepAlive(retained)
			for _, req := range retained {
				CleanupBodyStorage(req.context)
				require.NoError(t, req.outbound.Close())
			}
			retained = nil
			runtime.GC()
			runtime.GC()
			var released runtime.MemStats
			runtime.ReadMemStats(&released)
			require.Equal(t, baseline.CurrentMemoryUsageBytes, GetDiskCacheStats().CurrentMemoryUsageBytes)
			require.Zero(t, GetDiskCacheStats().CurrentDiskUsageBytes)
			t.Logf("after cleanup + dropping modeled requests + GC: heap delta=%.2f MiB", float64(int64(released.HeapAlloc)-int64(before.HeapAlloc))/(1<<20))
			runtime.KeepAlive(payload)
		})
	}
}
