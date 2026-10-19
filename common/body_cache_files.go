package common

import (
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// A lease outlives the file handle. Closed files whose deletion failed retain
// their reservation until a later Close or explicit cleanup succeeds.
type cacheFileLease struct {
	mu       sync.Mutex
	size     int64
	inactive bool
	closedAt time.Time
	identity os.FileInfo
	config   DiskCacheConfig
}

var cacheFiles sync.Map // absolute path -> *cacheFileLease
var diskBytesReserved atomic.Int64

type cacheFileWriter struct{ file *os.File }

func (w cacheFileWriter) Write(p []byte) (int, error) {
	value, ok := cacheFiles.Load(w.file.Name())
	if !ok {
		return 0, os.ErrClosed
	}
	lease := value.(*cacheFileLease)
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.inactive {
		return 0, os.ErrClosed
	}
	cfg := lease.config
	size := int64(len(p))
	if !reserveCacheBytes(&diskBytesReserved, size, cacheBudgetBytes(cfg.MaxSizeMB), cfg.GovernanceEnabled) {
		return 0, ErrBodyCacheCapacity
	}
	n, err := w.file.Write(p)
	diskBytesReserved.Add(-size + int64(n))
	lease.size += int64(n)
	atomic.AddInt64(&diskCacheStats.CurrentDiskUsageBytes, int64(n))
	if n < len(p) && err == nil {
		err = io.ErrShortWrite
	}
	return n, err
}

func removeCacheFile(path string, lease *cacheFileLease) error {
	info, err := os.Lstat(path)
	if err == nil && (info.Mode()&os.ModeSymlink != 0 || !os.SameFile(lease.identity, info)) {
		return os.ErrPermission
	}
	if err == nil {
		err = os.Remove(path)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if cacheFiles.CompareAndDelete(path, lease) {
		diskBytesReserved.Add(-lease.size)
		DecrementDiskFiles(lease.size)
	}
	return nil
}
