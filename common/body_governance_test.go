package common

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type bodyPanicReader struct{}

type configSwitchReader struct {
	reader   io.Reader
	switched bool
}

func (r *configSwitchReader) Read(p []byte) (int, error) {
	if !r.switched {
		cfg := GetDiskCacheConfig()
		cfg.GovernanceEnabled = false
		SetDiskCacheConfig(cfg)
		r.switched = true
	}
	return r.reader.Read(p)
}

func TestBodyGovernanceSnapshotDuringRead(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 1})
	s, err := CreateBodyStorageFromReader(&configSwitchReader{reader: strings.NewReader(strings.Repeat("x", 2<<20))}, -1, 3<<20)
	if s != nil {
		defer s.Close()
	}
	require.ErrorIs(t, err, ErrBodyCacheCapacity)
	require.Zero(t, GetBodyMemoryReserved())
}

func TestBodyGovernanceDiskSnapshot(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 1, Enabled: true, MaxSizeMB: 1})
	path, f, err := CreateDiskCacheFile(DiskCacheTypeBody)
	require.NoError(t, err)
	defer func() { _ = f.Close(); require.NoError(t, RemoveDiskCacheFile(path)) }()
	cfg := GetDiskCacheConfig()
	cfg.GovernanceEnabled = false
	SetDiskCacheConfig(cfg)
	_, err = (cacheFileWriter{f}).Write(make([]byte, 2<<20))
	require.ErrorIs(t, err, ErrBodyCacheCapacity)
	require.Zero(t, diskBytesReserved.Load())
}

func TestBodyGovernanceSpillCreationFallback(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 2, Enabled: true, ThresholdMB: 0, MaxSizeMB: 1})
	require.NoError(t, os.WriteFile(GetDiskCacheDir(), []byte("not a directory"), 0600))
	payload := strings.Repeat("x", 64<<10)
	s, err := CreateBodyStorageFromReader(strings.NewReader(payload), -1, 1<<20)
	require.NoError(t, err)
	defer s.Close()
	require.False(t, s.IsDisk())
	got, err := io.ReadAll(s)
	require.NoError(t, err)
	require.Equal(t, payload, string(got))
	require.NoError(t, s.Close())
	require.Zero(t, GetBodyMemoryReserved())
	require.Zero(t, diskBytesReserved.Load())
}

func TestBodyGovernanceSpillWriteFallback(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 2, Enabled: true, ThresholdMB: 0, MaxSizeMB: 0})
	payload := strings.Repeat("x", 64<<10)
	s, err := CreateBodyStorageFromReader(strings.NewReader(payload), -1, 1<<20)
	require.NoError(t, err)
	defer s.Close()
	require.False(t, s.IsDisk())
	got, err := io.ReadAll(s)
	require.NoError(t, err)
	require.Equal(t, payload, string(got))
	require.NoError(t, s.Close())
	require.Zero(t, GetBodyMemoryReserved())
	require.Zero(t, diskBytesReserved.Load())
	count, _, err := GetDiskCacheInfo()
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestBodyGovernanceCommittedSpillFailure(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 8, Enabled: true, ThresholdMB: 0, MaxSizeMB: 1})
	s, err := CreateBodyStorageFromReader(strings.NewReader(strings.Repeat("x", 2<<20)), -1, 3<<20)
	require.ErrorIs(t, err, ErrBodyCacheCapacity)
	require.Nil(t, s)
	require.Zero(t, GetBodyMemoryReserved())
	require.Zero(t, diskBytesReserved.Load())
}

func (bodyPanicReader) Read([]byte) (int, error) { panic("reader panic") }

func TestBodyGovernanceReaderPanic(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 1, Enabled: true, ThresholdMB: 0, MaxSizeMB: 1})
	require.Panics(t, func() {
		_, _ = CreateBodyStorageFromReader(io.MultiReader(strings.NewReader("prefix"), bodyPanicReader{}), -1, 1024)
	})
	require.Zero(t, GetBodyMemoryReserved())
	require.Zero(t, diskBytesReserved.Load())
	count, _, err := GetDiskCacheInfo()
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestBodyGovernanceMemoryBudget(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 1})
	before := GetBodyMemoryReserved()
	first, err := CreateBodyStorage(make([]byte, 1<<20))
	require.NoError(t, err)
	defer first.Close()
	_, err = CreateBodyStorage([]byte("x"))
	require.ErrorIs(t, err, ErrBodyCacheCapacity)
	require.NoError(t, first.Close())
	require.Equal(t, before, GetBodyMemoryReserved())
	require.Nil(t, first.(*memoryStorage).data)
	require.Nil(t, first.(*memoryStorage).reader)
}

func TestBodyGovernanceUnknownLength(t *testing.T) {
	for _, length := range []int64{-1, 1, 3 << 20} {
		useTempDiskCache(t, DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 1, Enabled: true, ThresholdMB: 1, MaxSizeMB: 4})
		before := GetBodyMemoryReserved()
		payload := strings.Repeat("x", 2<<20)
		storage, err := CreateBodyStorageFromReader(strings.NewReader(payload), length, 3<<20)
		require.NoError(t, err)
		require.True(t, storage.IsDisk())
		actual, err := io.ReadAll(storage)
		require.NoError(t, err)
		require.Equal(t, payload, string(actual))
		require.NoError(t, storage.Close())
		require.Equal(t, before, GetBodyMemoryReserved())
	}
}

func TestBodyGovernanceConcurrentDiskBudget(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 1, Enabled: true, ThresholdMB: 0, MaxSizeMB: 2})
	var wg sync.WaitGroup
	results := make(chan BodyStorage, 16)
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, e := CreateBodyStorage(bytes.Repeat([]byte("x"), 1<<20))
			results <- s
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	require.LessOrEqual(t, GetDiskCacheStats().CurrentDiskUsageBytes, int64(2<<20))
	require.LessOrEqual(t, GetBodyMemoryReserved(), int64(1<<20))
	for s := range results {
		if s != nil {
			require.NoError(t, s.Close())
		}
	}
	for err := range errs {
		if err != nil {
			require.ErrorIs(t, err, ErrBodyCacheCapacity)
		}
	}
	require.Zero(t, GetBodyMemoryReserved())
	require.Zero(t, GetDiskCacheStats().CurrentDiskUsageBytes)
}

func TestBodyGovernanceReaderLimitsAndFailures(t *testing.T) {
	for _, disk := range []bool{false, true} {
		useTempDiskCache(t, DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 1, Enabled: disk, ThresholdMB: 0, MaxSizeMB: 1})
		_, err := CreateBodyStorageFromReader(strings.NewReader("12345"), 1, 4)
		require.ErrorIs(t, err, ErrRequestBodyTooLarge)
		_, err = CreateBodyStorageFromReader(io.MultiReader(strings.NewReader("x"), bodyMemoryFailureReader{}), -1, 8)
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		require.Zero(t, GetBodyMemoryReserved())
		require.Zero(t, GetDiskCacheStats().CurrentDiskUsageBytes)
	}
}
