package model

import "time"

// DistributedLock provides a database-backed distributed lock
// to prevent the same task from running on multiple cluster nodes simultaneously.
type DistributedLock struct {
	ID         int    `gorm:"primaryKey" json:"id"`
	LockKey    string `gorm:"uniqueIndex;type:varchar(128);not null" json:"lock_key"`
	Owner      string `gorm:"type:varchar(128)" json:"owner"`
	AcquiredAt int64  `json:"acquired_at"`
	ExpiresAt  int64  `json:"expires_at"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

func (d *DistributedLock) TableName() string {
	return "distributed_locks"
}

// GetActiveLocks returns all locks that have not yet expired.
func GetActiveLocks() ([]DistributedLock, error) {
	var locks []DistributedLock
	now := time.Now().Unix()
	err := DB.Where("expires_at > ?", now).Find(&locks).Error
	return locks, err
}
