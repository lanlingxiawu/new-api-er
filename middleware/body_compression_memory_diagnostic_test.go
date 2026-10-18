package middleware

import (
	"bytes"
	"compress/gzip"
	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBodyMemoryDiagnosticCompressedDiskBudget(t *testing.T) {
	previous := common.GetDiskCacheConfig()
	previousMax := constant.MaxRequestBodyMB
	t.Cleanup(func() { common.SetDiskCacheConfig(previous); constant.MaxRequestBodyMB = previousMax })
	constant.MaxRequestBodyMB = 4
	common.SetDiskCacheConfig(common.DiskCacheConfig{Enabled: true, ThresholdMB: 0, MaxSizeMB: 2, Path: t.TempDir()})
	before := common.GetDiskCacheStats()
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	_, err := w.Write(bytes.Repeat([]byte("x"), 3<<20))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	compressedLength := compressed.Len()
	router := gin.New()
	router.Use(DecompressRequestMiddleware())
	router.POST("/", func(c *gin.Context) {
		require.Equal(t, int64(compressedLength), c.Request.ContentLength, "wire length remains after decompression")
		storage, err := common.CreateBodyStorageFromReader(c.Request.Body, c.Request.ContentLength, 4<<20)
		require.NoError(t, err)
		defer storage.Close()
		require.True(t, storage.IsDisk())
		require.Equal(t, int64(3<<20), storage.Size())
		require.Equal(t, int64(3<<20), common.GetDiskCacheStats().CurrentDiskUsageBytes-before.CurrentDiskUsageBytes)
		c.Status(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(compressed.Bytes()))
	req.Header.Set("Content-Encoding", "gzip")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.Equal(t, before.CurrentDiskUsageBytes, common.GetDiskCacheStats().CurrentDiskUsageBytes)
	t.Logf("%d compressed bytes admitted against 2 MiB disk cap, then 3 MiB written", compressedLength)
}

func TestBodyGovernanceCompressedActualBytes(t *testing.T) {
	for _, encoding := range []string{"gzip", "br", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			old := common.GetDiskCacheConfig()
			defer common.SetDiskCacheConfig(old)
			common.SetDiskCacheConfig(common.DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 1, Enabled: true, ThresholdMB: 0, MaxSizeMB: 2, Path: t.TempDir()})
			var compressed bytes.Buffer
			var writer io.WriteCloser
			switch encoding {
			case "gzip":
				writer = gzip.NewWriter(&compressed)
			case "br":
				writer = brotli.NewWriter(&compressed)
			case "zstd":
				var err error
				writer, err = zstd.NewWriter(&compressed)
				require.NoError(t, err)
			}
			_, err := writer.Write(bytes.Repeat([]byte("x"), 3<<20))
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			router := gin.New()
			router.Use(DecompressRequestMiddleware())
			router.POST("/", func(c *gin.Context) {
				_, err := common.GetBodyStorage(c)
				require.ErrorIs(t, err, common.ErrBodyCacheCapacity)
				c.Status(503)
			})
			for _, length := range []int64{-1, int64(compressed.Len()), 1} {
				req := httptest.NewRequest("POST", "/", bytes.NewReader(compressed.Bytes()))
				req.ContentLength = length
				req.Header.Set("Content-Encoding", encoding)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, req)
				require.Equal(t, 503, response.Code)
				require.Zero(t, common.GetBodyMemoryReserved())
				require.Zero(t, common.GetDiskCacheStats().DiskReservedBytes)
			}
		})
	}
}
