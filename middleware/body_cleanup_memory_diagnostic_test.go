package middleware

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Can be run with body_cleanup.go as an explicit file-list test package to
// avoid unrelated DB-backed middleware TestMain setup. This exercises the
// real cleanup middleware, with outer recovery ordered as in main/router.
func TestBodyCleanupMemoryDiagnosticExitPaths(t *testing.T) {
	for _, disk := range []bool{false, true} {
		for _, exit := range []string{"normal", "abort", "malformed", "cancel", "deadline", "panic"} {
			name := "memory/" + exit
			if disk {
				name = "disk/" + exit
			}
			t.Run(name, func(t *testing.T) {
				oldConfig := common.GetDiskCacheConfig()
				common.SetDiskCacheConfig(common.DiskCacheConfig{Enabled: disk, ThresholdMB: 0, MaxSizeMB: 32, Path: t.TempDir()})
				t.Cleanup(func() { common.SetDiskCacheConfig(oldConfig) })
				baseline := common.GetDiskCacheStats()
				var storages []common.BodyStorage
				t.Cleanup(func() {
					// Restore accounting and remove files even when demonstrating
					// that the middleware itself skipped cleanup after a panic.
					for _, storage := range storages {
						require.NoError(t, storage.Close())
					}
				})
				router := gin.New()
				router.Use(gin.RecoveryWithWriter(io.Discard), BodyStorageCleanup())
				router.POST("/", func(c *gin.Context) {
					storage, err := common.GetBodyStorage(c)
					if err != nil {
						c.AbortWithStatus(http.StatusBadRequest)
						return
					}
					storages = append(storages, storage)
					filePath, fileErr := common.WriteDiskCacheFile(common.DiskCacheTypeFile, []byte("file source"))
					if fileErr != nil {
						c.AbortWithStatus(500)
						return
					}
					cached := types.NewDiskCachedData(filePath, "text/plain", 11)
					cached.RemoveFile = common.RemoveDiskCacheFile
					source := &types.URLSource{}
					source.SetCache(cached)
					c.Set(string(constant.ContextKeyFileSourcesToCleanup), []types.FileSource{source})
					switch exit {
					case "panic":
						panic("synthetic diagnostic panic")
					case "abort":
						c.AbortWithStatus(http.StatusBadRequest)
					case "malformed":
						var decoded map[string]any
						if common.UnmarshalBodyReusable(c, &decoded) != nil {
							c.AbortWithStatus(http.StatusBadRequest)
						}
					case "cancel", "deadline":
						<-c.Request.Context().Done()
						c.Status(http.StatusRequestTimeout)
					default:
						c.Status(http.StatusNoContent)
					}
				})
				const repetitions = 20
				payload := bytes.Repeat([]byte("x"), 256<<10)
				for i := 0; i < repetitions; i++ {
					req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
					req.Header.Set("Content-Type", "application/json")
					ctx, cancel := context.WithCancel(req.Context())
					if exit == "cancel" {
						cancel()
					}
					if exit == "deadline" {
						cancel()
						ctx, cancel = context.WithDeadline(req.Context(), time.Now().Add(-time.Second))
					}
					writer := httptest.NewRecorder()
					router.ServeHTTP(writer, req.WithContext(ctx))
					cancel()
					wantStatus := http.StatusNoContent
					switch exit {
					case "abort", "malformed":
						wantStatus = http.StatusBadRequest
					case "cancel", "deadline":
						wantStatus = http.StatusRequestTimeout
					case "panic":
						wantStatus = http.StatusInternalServerError
					}
					require.Equal(t, wantStatus, writer.Code)
				}
				require.Len(t, storages, repetitions)
				stats := common.GetDiskCacheStats()
				count, size, err := common.GetDiskCacheInfo()
				require.NoError(t, err)
				require.Zero(t, count)
				require.Zero(t, size)
				require.Equal(t, baseline.CurrentMemoryUsageBytes, stats.CurrentMemoryUsageBytes)
				require.Equal(t, baseline.CurrentDiskUsageBytes, stats.CurrentDiskUsageBytes)
				require.Equal(t, baseline.ActiveMemoryBuffers, stats.ActiveMemoryBuffers)
				require.Equal(t, baseline.ActiveDiskFiles, stats.ActiveDiskFiles)
			})
		}
	}
}
