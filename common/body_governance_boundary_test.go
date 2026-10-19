package common

import (
	"bytes"
	"fmt"
	"math"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBodyGovernanceSpillThresholdBoundary(t *testing.T) {
	for _, fromReader := range []bool{false, true} {
		for _, size := range []int{(1 << 20) - 1, 1 << 20, (1 << 20) + 1} {
			t.Run(fmt.Sprintf("reader=%t/bytes=%d", fromReader, size), func(t *testing.T) {
				useTempDiskCache(t, DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 16, Enabled: true, ThresholdMB: 1, MaxSizeMB: 4})
				payload := bytes.Repeat([]byte("x"), size)
				var storage BodyStorage
				var err error
				if fromReader {
					storage, err = CreateBodyStorageFromReader(bytes.NewReader(payload), -1, 2<<20)
				} else {
					storage, err = CreateBodyStorage(payload)
				}
				require.NoError(t, err)
				defer storage.Close()
				require.Equal(t, size >= 1<<20, storage.IsDisk())
				got, err := storage.Bytes()
				require.NoError(t, err)
				require.Equal(t, payload, got)
				require.NoError(t, storage.Close())
				require.Zero(t, GetBodyMemoryReserved())
				require.Zero(t, diskBytesReserved.Load())
			})
		}
	}
}

func TestBodyGovernanceCapacityNotLength(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 1})
	storage, err := CreateBodyStorage(make([]byte, 1, 1<<20))
	require.NoError(t, err)
	defer storage.Close()
	require.Equal(t, int64(1<<20), GetBodyMemoryReserved())
	_, err = CreateBodyStorage([]byte("x"))
	require.ErrorIs(t, err, ErrBodyCacheCapacity)
	require.NoError(t, storage.Close())
	require.Zero(t, GetBodyMemoryReserved())
}

func TestBodyGovernanceGrowthOverlapBudget(t *testing.T) {
	useTempDiskCache(t, DiskCacheConfig{GovernanceEnabled: true, MemoryBudgetMB: 1})
	storage, err := CreateBodyStorage(make([]byte, 512<<10))
	require.NoError(t, err)
	defer storage.Close()
	data, err := storage.Bytes()
	require.NoError(t, err)
	// The new 1MiB allocation and old 512KiB array coexist during copying.
	grown, err := growBodyMemory(data, len(data)+1, GetDiskCacheConfig())
	require.ErrorIs(t, err, ErrBodyCacheCapacity)
	require.Equal(t, cap(data), cap(grown))
	require.Equal(t, int64(512<<10), GetBodyMemoryReserved())
	require.NoError(t, storage.Close())
	require.Zero(t, GetBodyMemoryReserved())
}

func TestBodyGovernanceBudgetArithmetic(t *testing.T) {
	require.Zero(t, cacheBudgetBytes(0))
	require.Zero(t, cacheBudgetBytes(-1))
	require.Equal(t, int64(1048576), cacheBudgetBytes(1))
	var reserved atomic.Int64
	require.True(t, reserveCacheBytes(&reserved, 1023, 1024, true))
	require.True(t, reserveCacheBytes(&reserved, 1, 1024, true))
	require.False(t, reserveCacheBytes(&reserved, 1, 1024, true))
	require.False(t, reserveCacheBytes(&reserved, -1, 1024, true))
	require.Equal(t, int64(1024), reserved.Load())
	reserved.Store(math.MaxInt64)
	require.False(t, reserveCacheBytes(&reserved, 1, math.MaxInt64, false))
	require.Equal(t, int64(math.MaxInt64), reserved.Load())
}
