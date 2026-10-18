package common

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBodyCacheOwnershipAndFailedDeletion(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true, GovernanceEnabled: true, MemoryBudgetMB: 1, ThresholdMB: 0, MaxSizeMB: 2})
	storage, err := CreateBodyStorage(bytes.Repeat([]byte("a"), 1<<20))
	require.NoError(t, err)
	path := storage.(*diskStorage).filePath
	require.NoError(t, storage.(*diskStorage).file.Close())
	moved := path + ".moved"
	require.NoError(t, os.Rename(path, moved))
	require.NoError(t, os.WriteFile(path, []byte("not owned"), 0600))
	require.ErrorIs(t, storage.Close(), os.ErrPermission)
	require.Equal(t, int64(1<<20), diskBytesReserved.Load())
	require.ErrorIs(t, CleanupOldDiskCacheFiles(0), os.ErrPermission)
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Rename(moved, path))
	require.NoError(t, CleanupOldDiskCacheFiles(0))
	require.NoError(t, storage.Close())
	require.Zero(t, diskBytesReserved.Load())
	require.Zero(t, GetDiskCacheStats().CurrentDiskUsageBytes)
	unknown := filepath.Join(GetDiskCacheDir(), "body-old.tmp")
	require.NoError(t, os.WriteFile(unknown, []byte("foreign"), 0600))
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(unknown, old, old))
	require.NoError(t, CleanupOldDiskCacheFiles(0))
	require.FileExists(t, unknown)
}

func TestBodyCacheFileSourceSharesDiskBudget(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true, GovernanceEnabled: true, MemoryBudgetMB: 1, ThresholdMB: 0, MaxSizeMB: 1})
	path, err := WriteDiskCacheFile(DiskCacheTypeFile, make([]byte, 1<<20))
	require.NoError(t, err)
	defer RemoveDiskCacheFile(path)
	_, err = WriteDiskCacheFile(DiskCacheTypeBody, []byte("x"))
	require.ErrorIs(t, err, ErrBodyCacheCapacity)
	require.NoError(t, CleanupOldDiskCacheFiles(0))
	require.FileExists(t, path)
	require.Equal(t, int64(1<<20), diskBytesReserved.Load())
}

func TestBodyCacheConfigSwitchKeepsOwnership(t *testing.T) {
	firstDir := useTempDiskCache(t, DiskCacheConfig{Enabled: true, GovernanceEnabled: true, MemoryBudgetMB: 1, ThresholdMB: 0, MaxSizeMB: 2})
	storage, err := CreateBodyStorage([]byte("original"))
	require.NoError(t, err)
	cfg := GetDiskCacheConfig()
	cfg.Path = t.TempDir()
	cfg.MaxSizeMB = 0
	SetDiskCacheConfig(cfg)
	require.NoError(t, storage.Close())
	entries, err := os.ReadDir(filepath.Join(firstDir, diskCacheDir))
	require.NoError(t, err)
	require.Empty(t, entries)
	require.Zero(t, diskBytesReserved.Load())
}
