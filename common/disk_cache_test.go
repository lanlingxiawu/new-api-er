package common

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// useTempDiskCache points the disk cache at a fresh temp dir and restores the
// previous config + stats afterwards.
func useTempDiskCache(t *testing.T, cfg DiskCacheConfig) string {
	t.Helper()
	dir := t.TempDir()
	cfg.Path = dir
	origCfg := GetDiskCacheConfig()
	origStats := GetDiskCacheStats()
	SetDiskCacheConfig(cfg)
	t.Cleanup(func() {
		SetDiskCacheConfig(origCfg)
		atomic.StoreInt64(&diskCacheStats.ActiveDiskFiles, origStats.ActiveDiskFiles)
		atomic.StoreInt64(&diskCacheStats.CurrentDiskUsageBytes, origStats.CurrentDiskUsageBytes)
		atomic.StoreInt64(&diskCacheStats.ActiveMemoryBuffers, origStats.ActiveMemoryBuffers)
		atomic.StoreInt64(&diskCacheStats.CurrentMemoryUsageBytes, origStats.CurrentMemoryUsageBytes)
		atomic.StoreInt64(&diskCacheStats.DiskCacheHits, origStats.DiskCacheHits)
		atomic.StoreInt64(&diskCacheStats.MemoryCacheHits, origStats.MemoryCacheHits)
	})
	return dir
}

func TestDiskCacheConfigGetters(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: 10, MaxSizeMB: 1024})

	assert.True(t, IsDiskCacheEnabled())
	assert.Equal(t, int64(10)<<20, GetDiskCacheThresholdBytes())
	assert.Equal(t, int64(1024)<<20, GetDiskCacheMaxSizeBytes())
	assert.NotEmpty(t, GetDiskCachePath())
}

func TestGetDiskCacheDir(t *testing.T) {
	dir := useTempDiskCache(t, DiskCacheConfig{Enabled: true})
	assert.Equal(t, filepath.Join(dir, diskCacheDir), GetDiskCacheDir())

	// empty path falls back to the OS temp dir
	SetDiskCacheConfig(DiskCacheConfig{Enabled: true, Path: ""})
	assert.Equal(t, filepath.Join(os.TempDir(), diskCacheDir), GetDiskCacheDir())
}

func TestEnsureAndCreateDiskCacheFile(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true})
	require.NoError(t, EnsureDiskCacheDir())

	path, file, err := CreateDiskCacheFile(DiskCacheTypeBody)
	require.NoError(t, err)
	require.NotNil(t, file)
	t.Cleanup(func() { _ = file.Close(); _ = os.Remove(path) })
	assert.FileExists(t, path)
	assert.Contains(t, filepath.Base(path), "body-")
}

func TestWriteReadRemoveDiskCacheFile(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true})

	path, err := WriteDiskCacheFileString(DiskCacheTypeFile, "hello disk")
	require.NoError(t, err)
	assert.Contains(t, filepath.Base(path), "file-")

	data, err := ReadDiskCacheFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hello disk", string(data))

	s, err := ReadDiskCacheFileString(path)
	require.NoError(t, err)
	assert.Equal(t, "hello disk", s)

	require.NoError(t, RemoveDiskCacheFile(path))
	assert.NoFileExists(t, path)

	// reading a missing file errors
	_, err = ReadDiskCacheFileString(path)
	assert.Error(t, err)
}

func TestGetDiskCacheInfoAndCleanup(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true})

	// no dir yet -> zero info, no error
	count, size, err := GetDiskCacheInfo()
	require.NoError(t, err)
	assert.Equal(t, 0, count)
	assert.Equal(t, int64(0), size)

	p1, err := WriteDiskCacheFileString(DiskCacheTypeBody, "aaaa")
	require.NoError(t, err)
	_, err = WriteDiskCacheFileString(DiskCacheTypeBody, "bbbbbb")
	require.NoError(t, err)

	count, size, err = GetDiskCacheInfo()
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	assert.Equal(t, int64(10), size)

	// age p1 by an hour; cleanup with a 1-minute cutoff removes only p1 and
	// leaves the freshly-written p2 (deterministic, no timing race).
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(p1, old, old))
	require.NoError(t, CleanupOldDiskCacheFiles(time.Minute))
	count, _, err = GetDiskCacheInfo()
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.NoFileExists(t, p1)
}

func TestCleanupOldDiskCacheFiles_MissingDir(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true})
	// dir never created -> returns nil (nothing to clean)
	assert.NoError(t, CleanupOldDiskCacheFiles(time.Minute))
}

func TestShouldUseDiskCache(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: 1, MaxSizeMB: 10})

	// below threshold -> false
	assert.False(t, ShouldUseDiskCache(100))
	// above threshold and within capacity -> true
	assert.True(t, ShouldUseDiskCache(2<<20))

	// disabled -> always false
	SetDiskCacheConfig(DiskCacheConfig{Enabled: false, ThresholdMB: 1, MaxSizeMB: 10})
	assert.False(t, ShouldUseDiskCache(2<<20))
}

func TestIsDiskCacheAvailable(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true, MaxSizeMB: 1}) // 1 MB budget
	ResetDiskCacheUsage()

	assert.True(t, IsDiskCacheAvailable(512<<10)) // 0.5 MB fits
	// exactly at the limit is allowed (<=)
	assert.True(t, IsDiskCacheAvailable(1<<20))
	// over the limit rejected
	assert.False(t, IsDiskCacheAvailable((1<<20)+1))

	SetDiskCacheConfig(DiskCacheConfig{Enabled: false, MaxSizeMB: 1})
	assert.False(t, IsDiskCacheAvailable(1))
}

func TestDiskCacheStatsCounters(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: 1, MaxSizeMB: 100})
	ResetDiskCacheUsage()
	ResetDiskCacheStats()

	IncrementDiskFiles(100)
	IncrementMemoryBuffers(200)
	IncrementDiskCacheHits()
	IncrementMemoryCacheHits()

	stats := GetDiskCacheStats()
	assert.Equal(t, int64(1), stats.ActiveDiskFiles)
	assert.Equal(t, int64(100), stats.CurrentDiskUsageBytes)
	assert.Equal(t, int64(1), stats.ActiveMemoryBuffers)
	assert.Equal(t, int64(200), stats.CurrentMemoryUsageBytes)
	assert.Equal(t, int64(1), stats.DiskCacheHits)
	assert.Equal(t, int64(1), stats.MemoryCacheHits)

	DecrementDiskFiles(100)
	DecrementMemoryBuffers(200)
	stats = GetDiskCacheStats()
	assert.Equal(t, int64(0), stats.ActiveDiskFiles)
	assert.Equal(t, int64(0), stats.CurrentDiskUsageBytes)

	// Decrement below zero clamps disk files/usage back to 0.
	DecrementDiskFiles(50)
	stats = GetDiskCacheStats()
	assert.Equal(t, int64(0), stats.ActiveDiskFiles)
	assert.Equal(t, int64(0), stats.CurrentDiskUsageBytes)

	ResetDiskCacheStats()
	stats = GetDiskCacheStats()
	assert.Equal(t, int64(0), stats.DiskCacheHits)
	assert.Equal(t, int64(0), stats.MemoryCacheHits)
}

func TestSyncDiskCacheStats(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true})
	ResetDiskCacheUsage()

	_, err := WriteDiskCacheFileString(DiskCacheTypeBody, "1234567890")
	require.NoError(t, err)

	SyncDiskCacheStats()
	stats := GetDiskCacheStats()
	assert.Equal(t, int64(1), stats.ActiveDiskFiles)
	assert.Equal(t, int64(10), stats.CurrentDiskUsageBytes)
}
