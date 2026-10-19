package common

import (
	"bytes"
	"fmt"
	"io"
)

func createGovernedBodyBytes(data []byte, cfg DiskCacheConfig) (BodyStorage, error) {
	if cfg.Enabled && int64(len(data)) >= cacheBudgetBytes(cfg.ThresholdMB) {
		if storage, err := newDiskStorageWithConfig(bytes.NewReader(data), int64(len(data)), cfg); err == nil {
			return storage, nil
		}
	}
	if reserveBodyMemory(int64(cap(data)), cfg) {
		return newReservedMemoryStorage(data), nil
	}
	if cfg.Enabled {
		storage, err := newDiskStorageWithConfig(bytes.NewReader(data), int64(len(data)), cfg)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrBodyCacheCapacity, err)
		}
		return storage, nil
	}
	return nil, ErrBodyCacheCapacity
}

// Reserve before allocating, including both arrays during growth. The length
// header is deliberately not trusted: middleware may already have decompressed
// the reader. Only bytes returned by Read count towards the request size limit.
func createGovernedBodyReader(reader io.Reader, maxBytes int64, cfg DiskCacheConfig) (result BodyStorage, err error) {
	if maxBytes < 0 {
		return nil, ErrRequestBodyTooLarge
	}
	scratchSize := int64(32 << 10)
	if maxBytes < scratchSize {
		scratchSize = maxBytes + 1
	}
	if !reserveBodyMemory(scratchSize, cfg) {
		return nil, ErrBodyCacheCapacity
	}
	scratch := make([]byte, int(scratchSize))
	defer bodyMemoryReserved.Add(-scratchSize)
	var data []byte
	var disk *diskStorage
	success := false
	defer func() {
		if data != nil {
			bodyMemoryReserved.Add(-int64(cap(data)))
		}
		if !success && disk != nil {
			_ = disk.Close()
		}
	}()
	var total int64
	emptyReads := 0
	spillFailed := false
	for {
		n, readErr := reader.Read(scratch)
		if n < 0 || n > len(scratch) {
			return nil, io.ErrShortBuffer
		}
		if int64(n) > maxBytes-total {
			return nil, ErrRequestBodyTooLarge
		}
		total += int64(n)
		if n > 0 {
			emptyReads = 0
			spill := cfg.Enabled && !spillFailed && total >= cacheBudgetBytes(cfg.ThresholdMB)
			if disk == nil && !spill && len(data)+n > cap(data) {
				data, err = growBodyMemory(data, len(data)+n, cfg)
				if err != nil && cfg.Enabled && !spillFailed {
					spill = true
				} else if err != nil {
					return nil, ErrBodyCacheCapacity
				}
			}
			if disk == nil && spill {
				path, file, spillErr := createDiskCacheFile(DiskCacheTypeBody, cfg)
				if spillErr == nil {
					disk = &diskStorage{file: file, filePath: path}
					_, spillErr = (cacheFileWriter{file}).Write(data)
					if spillErr == nil {
						_, spillErr = (cacheFileWriter{file}).Write(scratch[:n])
					}
				}
				if spillErr != nil {
					if disk != nil {
						_ = disk.Close()
						disk = nil
					}
					spillFailed = true
					data, err = growBodyMemory(data, len(data)+n, cfg)
					if err != nil {
						return nil, fmt.Errorf("%w: %w", ErrBodyCacheCapacity, spillErr)
					}
				} else {
					bodyMemoryReserved.Add(-int64(cap(data)))
					data = nil
					n = 0 // this chunk was included in the successful migration
				}
			}
			if disk != nil {
				if _, err = (cacheFileWriter{disk.file}).Write(scratch[:n]); err != nil {
					return nil, fmt.Errorf("%w: %w", ErrBodyCacheCapacity, err)
				}
			} else {
				data = append(data, scratch[:n]...)
			}
		} else if readErr == nil {
			emptyReads++
			if emptyReads >= 100 {
				return nil, io.ErrNoProgress
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	if disk != nil {
		disk.size = total
		if _, err = disk.file.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		IncrementDiskCacheHits()
		success = true
		return disk, nil
	}
	storage := newReservedMemoryStorage(data)
	data = nil // ownership transfers to storage
	IncrementMemoryCacheHits()
	return storage, nil
}

func growBodyMemory(data []byte, needed int, cfg DiskCacheConfig) ([]byte, error) {
	if needed <= cap(data) {
		return data, nil
	}
	capacity := max(needed, cap(data)*2)
	if !reserveBodyMemory(int64(capacity), cfg) {
		return data, ErrBodyCacheCapacity
	}
	grown := make([]byte, len(data), capacity)
	copy(grown, data)
	bodyMemoryReserved.Add(-int64(cap(data)))
	return grown, nil
}
