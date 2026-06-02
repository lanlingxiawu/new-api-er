package service

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
)

// nodeID uniquely identifies this process in the cluster (hostname:pid).
var (
	nodeIDOnce   sync.Once
	cachedNodeID string
)

func getNodeID() string {
	nodeIDOnce.Do(func() {
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "unknown"
		}
		cachedNodeID = fmt.Sprintf("%s:%d", hostname, os.Getpid())
	})
	return cachedNodeID
}

// TryAcquireLock attempts to acquire a distributed lock for the given key.
// Returns true if this node now holds the lock, false if another node holds it.
// The lock automatically expires after ttlSeconds so a crashed node cannot
// block the task indefinitely.
func TryAcquireLock(lockKey string, ttlSeconds int) (bool, error) {
	now := time.Now().Unix()
	owner := getNodeID()

	// Delete any expired locks for this key first (idempotent cleanup).
	if err := model.DB.Where("lock_key = ? AND expires_at < ?", lockKey, now).
		Delete(&model.DistributedLock{}).Error; err != nil {
		return false, fmt.Errorf("lock cleanup error for %s: %w", lockKey, err)
	}

	lock := &model.DistributedLock{
		LockKey:    lockKey,
		Owner:      owner,
		AcquiredAt: now,
		ExpiresAt:  now + int64(ttlSeconds),
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	// INSERT relies on the UNIQUE constraint; if another node holds the lock,
	// the insert fails and we return false (not an error).
	result := model.DB.Create(lock)
	if result.Error != nil {
		var activeCount int64
		err := model.DB.Model(&model.DistributedLock{}).
			Where("lock_key = ? AND expires_at >= ?", lockKey, now).
			Count(&activeCount).Error
		if err != nil {
			return false, fmt.Errorf("lock create error for %s: %v; active-lock check failed: %w", lockKey, result.Error, err)
		}
		if activeCount > 0 {
			return false, nil
		}
		return false, fmt.Errorf("lock create error for %s: %w", lockKey, result.Error)
	}
	return true, nil
}

// ReleaseLock releases the distributed lock owned by this node.
func ReleaseLock(lockKey string) {
	owner := getNodeID()
	model.DB.Where("lock_key = ? AND owner = ?", lockKey, owner).
		Delete(&model.DistributedLock{})
}

// RefreshLock extends the lock TTL for long-running tasks.
func RefreshLock(lockKey string, ttlSeconds int) {
	owner := getNodeID()
	newExpiry := time.Now().Unix() + int64(ttlSeconds)
	model.DB.Model(&model.DistributedLock{}).
		Where("lock_key = ? AND owner = ?", lockKey, owner).
		Update("expires_at", newExpiry)
}

// RunWithLock acquires a distributed lock, runs fn, then releases the lock.
// If the lock cannot be acquired (another node holds it), fn is not called
// and no error is returned — this is the normal cluster behaviour.
func RunWithLock(lockKey string, ttlSeconds int, fn func() error) error {
	acquired, err := TryAcquireLock(lockKey, ttlSeconds)
	if err != nil {
		return fmt.Errorf("lock acquire error for %s: %w", lockKey, err)
	}
	if !acquired {
		logger.LogInfo(context.Background(), fmt.Sprintf("distributed lock busy, skipping task: %s", lockKey))
		return nil
	}
	defer ReleaseLock(lockKey)
	return fn()
}
