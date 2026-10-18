package common

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// DiskCacheType 磁盘缓存类型
type DiskCacheType string

const (
	DiskCacheTypeBody DiskCacheType = "body" // 请求体缓存
	DiskCacheTypeFile DiskCacheType = "file" // 文件数据缓存
)

// 统一的缓存目录名
const diskCacheDir = "new-api-body-cache"

// GetDiskCacheDir 获取统一的磁盘缓存目录
// 注意：每次调用都会重新计算，以响应配置变化
func GetDiskCacheDir() string {
	cachePath := GetDiskCachePath()
	if cachePath == "" {
		cachePath = os.TempDir()
	}
	return filepath.Join(cachePath, diskCacheDir)
}

// EnsureDiskCacheDir 确保缓存目录存在
func EnsureDiskCacheDir() error {
	dir := GetDiskCacheDir()
	return os.MkdirAll(dir, 0755)
}

// CreateDiskCacheFile 创建磁盘缓存文件
// cacheType: 缓存类型（body/file）
// 返回文件路径和文件句柄
func CreateDiskCacheFile(cacheType DiskCacheType) (string, *os.File, error) {
	return createDiskCacheFile(cacheType, GetDiskCacheConfig())
}

func createDiskCacheFile(cacheType DiskCacheType, cfg DiskCacheConfig) (string, *os.File, error) {
	base := cfg.Path
	if base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, diskCacheDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	filename := fmt.Sprintf("%s-%s-%d.tmp", cacheType, uuid.New().String()[:8], time.Now().UnixNano())
	filePath := filepath.Join(dir, filename)

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0600)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create cache file: %w", err)
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		_ = os.Remove(filePath)
		return "", nil, err
	}
	cacheFiles.Store(filePath, &cacheFileLease{identity: info, config: cfg})
	IncrementDiskFiles(0)
	return filePath, file, nil
}

// WriteDiskCacheFile 写入数据到磁盘缓存文件
// 返回文件路径
func WriteDiskCacheFile(cacheType DiskCacheType, data []byte) (string, error) {
	filePath, file, err := CreateDiskCacheFile(cacheType)
	if err != nil {
		return "", err
	}

	_, err = (cacheFileWriter{file}).Write(data)
	if err != nil {
		file.Close()
		RemoveDiskCacheFile(filePath)
		return "", fmt.Errorf("failed to write cache file: %w", err)
	}

	if err := file.Close(); err != nil {
		RemoveDiskCacheFile(filePath)
		return "", fmt.Errorf("failed to close cache file: %w", err)
	}

	return filePath, nil
}

// WriteDiskCacheFileString 写入字符串到磁盘缓存文件
func WriteDiskCacheFileString(cacheType DiskCacheType, data string) (string, error) {
	return WriteDiskCacheFile(cacheType, []byte(data))
}

// ReadDiskCacheFile 读取磁盘缓存文件
func ReadDiskCacheFile(filePath string) ([]byte, error) {
	return os.ReadFile(filePath)
}

// ReadDiskCacheFileString 读取磁盘缓存文件为字符串
func ReadDiskCacheFileString(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// RemoveDiskCacheFile 删除磁盘缓存文件
// RemoveDiskCacheFile only removes a file owned by this process. Repeated
// calls are harmless, including after a successful cleanup retry.
func RemoveDiskCacheFile(filePath string) error {
	value, ok := cacheFiles.Load(filePath)
	if !ok {
		return nil
	}
	lease := value.(*cacheFileLease)
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if !lease.inactive {
		lease.inactive = true
		lease.closedAt = time.Now()
	}
	return removeCacheFile(filePath, lease)
}

// CleanupOldDiskCacheFiles retries failed deletion of closed, owned files.
// Age is never evidence that an active request or another process is dead.
func CleanupOldDiskCacheFiles(maxAge time.Duration) error {
	var firstErr error
	cacheFiles.Range(func(key, value any) bool {
		lease := value.(*cacheFileLease)
		lease.mu.Lock()
		if lease.inactive && time.Since(lease.closedAt) >= maxAge {
			if err := removeCacheFile(key.(string), lease); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		lease.mu.Unlock()
		return true
	})
	return firstErr
}

// GetDiskCacheInfo 获取磁盘缓存目录信息
func GetDiskCacheInfo() (fileCount int, totalSize int64, err error) {
	dir := GetDiskCacheDir()

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		fileCount++
		totalSize += info.Size()
	}
	return fileCount, totalSize, nil
}

// ShouldUseDiskCache 判断是否应该使用磁盘缓存
func ShouldUseDiskCache(dataSize int64) bool {
	if !IsDiskCacheEnabled() {
		return false
	}
	threshold := GetDiskCacheThresholdBytes()
	if dataSize < threshold {
		return false
	}
	return IsDiskCacheAvailable(dataSize)
}
