package common

import (
	"errors"
	"math"
	"sync/atomic"
)

// ErrBodyCacheCapacity is local capacity exhaustion, never an upstream failure.
var ErrBodyCacheCapacity = errors.New("request body cache capacity exhausted")
var bodyMemoryReserved atomic.Int64
var bodyCapacityRejects atomic.Int64

func GetBodyMemoryReserved() int64 { return bodyMemoryReserved.Load() }

func cacheBudgetBytes(mb int) int64 {
	if mb <= 0 {
		return 0
	}
	if uint64(mb) > uint64(math.MaxInt64>>20) {
		return math.MaxInt64
	}
	return int64(mb) << 20
}

func reserveCacheBytes(counter *atomic.Int64, size, limit int64, enforce bool) bool {
	if size == 0 {
		return true
	}
	if size < 0 {
		return false
	}
	for {
		current := counter.Load()
		if size > math.MaxInt64-current || (enforce && (current > limit || size > limit-current)) {
			bodyCapacityRejects.Add(1)
			return false
		}
		if counter.CompareAndSwap(current, current+size) {
			return true
		}
	}
}

func reserveBodyMemory(size int64, cfg DiskCacheConfig) bool {
	return reserveCacheBytes(&bodyMemoryReserved, size, cacheBudgetBytes(cfg.MemoryBudgetMB), cfg.GovernanceEnabled)
}
