package common

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type bodyMemoryFailureReader struct{}

func TestBodyMemoryDiagnosticUnderreportedLength(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: 0, MaxSizeMB: 2})
	ResetDiskCacheUsage()
	storage, err := CreateBodyStorageFromReader(strings.NewReader(strings.Repeat("x", 3<<20)), 1<<20, 4<<20)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, storage.Close()) })
	require.True(t, storage.IsDisk())
	require.Equal(t, int64(3<<20), storage.Size())
	require.Equal(t, int64(3<<20), GetDiskCacheStats().CurrentDiskUsageBytes)
	t.Log("declared 1 MiB passes 2 MiB disk check; actual 3 MiB is written without additional reservation")
}

func (bodyMemoryFailureReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestBodyMemoryDiagnosticInterruptedReads(t *testing.T) {
	for _, disk := range []bool{false, true} {
		for _, length := range []int64{-1, 1 << 20} {
			t.Run(fmt.Sprintf("disk_%t_length_%d", disk, length), func(t *testing.T) {
				useTempDiskCache(t, DiskCacheConfig{Enabled: disk, ThresholdMB: 0, MaxSizeMB: 8})
				ResetDiskCacheUsage()
				before := GetDiskCacheStats()
				for i := 0; i < 100; i++ {
					reader := io.MultiReader(strings.NewReader(strings.Repeat("x", 64<<10)), bodyMemoryFailureReader{})
					storage, err := CreateBodyStorageFromReader(reader, length, 2<<20)
					require.ErrorIs(t, err, io.ErrUnexpectedEOF)
					require.Nil(t, storage)
				}
				count, size, err := GetDiskCacheInfo()
				require.NoError(t, err)
				require.Zero(t, count)
				require.Zero(t, size)
				require.Equal(t, before.CurrentMemoryUsageBytes, GetDiskCacheStats().CurrentMemoryUsageBytes)
				require.Zero(t, GetDiskCacheStats().CurrentDiskUsageBytes)
			})
		}
	}
}

func TestBodyMemoryDiagnosticDiskCreationFailure(t *testing.T) {
	dir := useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: 0, MaxSizeMB: 8})
	ResetDiskCacheUsage()
	// A regular file at the directory path makes creation fail on all OSes,
	// including root: no reliance on writable permission bits or a full disk.
	require.NoError(t, os.WriteFile(filepath.Join(dir, diskCacheDir), []byte("blocked"), 0600))
	storage, err := CreateBodyStorage(bytes.Repeat([]byte("x"), 1<<20))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, storage.Close()) })
	require.False(t, storage.IsDisk())
	_, err = CreateBodyStorageFromReader(strings.NewReader("body"), 4, 8)
	require.Error(t, err)
	t.Log("disk-create failure: byte-slice API falls back to memory; reader API returns error without fallback")
}

func TestBodyMemoryDiagnosticLiveFileAgeCleanup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("tests Linux unlink semantics for still-open files")
	}
	useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: 0, MaxSizeMB: 8})
	ResetDiskCacheUsage()
	first, err := CreateBodyStorage(bytes.Repeat([]byte("a"), 1<<20))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, first.Close()) })
	second, err := CreateBodyStorage(bytes.Repeat([]byte("b"), 1<<20))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close()) })
	old := time.Now().Add(-20 * time.Minute)
	require.NoError(t, os.Chtimes(first.(*diskStorage).filePath, old, old))
	require.NoError(t, CleanupOldDiskCacheFiles(10*time.Minute))
	_, err = os.Stat(first.(*diskStorage).filePath)
	require.NoError(t, err, "active files survive age cleanup")
	buf := make([]byte, 1)
	_, err = first.Read(buf)
	require.NoError(t, err, "Linux keeps an unlinked open file readable")
	require.Equal(t, int64(2<<20), GetDiskCacheStats().CurrentDiskUsageBytes)
	require.NoError(t, first.Close())
	require.Equal(t, int64(1<<20), GetDiskCacheStats().CurrentDiskUsageBytes)
	count, size, err := GetDiskCacheInfo()
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, int64(1<<20), size)
}

func TestBodyMemoryDiagnosticStatsSyncDuringWrite(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires immediately visible file-size metadata during open writes")
	}
	useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: 0, MaxSizeMB: 8})
	ResetDiskCacheUsage()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	result := make(chan BodyStorage, 1)
	failures := make(chan error, 1)
	go func() {
		reader := io.MultiReader(bytes.NewReader(make([]byte, 256<<10)), &bodyMemoryBarrierReader{
			reader: bytes.NewReader(make([]byte, 768<<10)), entered: entered, release: release,
		})
		storage, err := CreateBodyStorageFromReader(reader, 1<<20, 2<<20)
		result <- storage
		failures <- err
	}()
	<-entered
	SyncDiskCacheStats()
	partial := GetDiskCacheStats()
	close(release)
	storage := <-result
	require.NoError(t, <-failures)
	t.Cleanup(func() { require.NoError(t, storage.Close()) })
	require.Equal(t, int64(256<<10), partial.CurrentDiskUsageBytes)
	require.Equal(t, int64(1<<20), GetDiskCacheStats().CurrentDiskUsageBytes)
	require.Equal(t, int64(1), GetDiskCacheStats().ActiveDiskFiles)
	require.NoError(t, storage.Close())
	require.Zero(t, GetDiskCacheStats().CurrentDiskUsageBytes)
}

func TestBodyMemoryDiagnosticConcurrentConfigAndClose(t *testing.T) {
	dir := useTempDiskCache(t, DiskCacheConfig{Enabled: true, ThresholdMB: 0, MaxSizeMB: 64})
	ResetDiskCacheUsage()
	before := GetDiskCacheStats()
	configs := []DiskCacheConfig{
		{Enabled: true, ThresholdMB: 0, MaxSizeMB: 64, Path: dir},
		{Enabled: false, ThresholdMB: 10, MaxSizeMB: 1, Path: dir},
	}
	results := make(chan error, 9)
	go func() {
		for i := 0; i < 1000; i++ {
			SetDiskCacheConfig(configs[i%2])
		}
		results <- nil
	}()
	for worker := 0; worker < 8; worker++ {
		go func() {
			for i := 0; i < 100; i++ {
				storage, err := CreateBodyStorageFromReader(bytes.NewReader(make([]byte, 64<<10)), 64<<10, 128<<10)
				if err != nil {
					results <- err
					return
				}
				closed := make(chan error, 2)
				go func() { closed <- storage.Close() }()
				go func() { closed <- storage.Close() }()
				err = errors.Join(<-closed, <-closed)
				if err != nil {
					results <- err
					return
				}
			}
			results <- nil
		}()
	}
	for i := 0; i < 9; i++ {
		require.NoError(t, <-results)
	}
	require.Equal(t, before.CurrentMemoryUsageBytes, GetDiskCacheStats().CurrentMemoryUsageBytes)
	require.Equal(t, before.ActiveMemoryBuffers, GetDiskCacheStats().ActiveMemoryBuffers)
	require.Zero(t, GetDiskCacheStats().CurrentDiskUsageBytes)
	require.Zero(t, GetDiskCacheStats().ActiveDiskFiles)
	count, _, err := GetDiskCacheInfo()
	require.NoError(t, err)
	require.Zero(t, count)
}
