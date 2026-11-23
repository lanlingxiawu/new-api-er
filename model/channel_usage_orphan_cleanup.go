package model

import (
	"context"
	"sync"
	"time"
)

// Each sweep is used only by maintenance, never by the relay or usage flusher.
// Pinning the upper ID lets the cursor wrap even while new rows keep arriving.
type channelUsageOrphanSweep struct {
	mu      sync.Mutex
	afterID int
	lastID  int
}

var dailyUsageOrphanSweep channelUsageOrphanSweep
var periodUsageOrphanSweep channelUsageOrphanSweep

func (s *channelUsageOrphanSweep) run(model any) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	db := DB.WithContext(ctx)
	if s.lastID == 0 {
		if err := db.Model(model).Select("COALESCE(MAX(id), 0)").Scan(&s.lastID).Error; err != nil {
			return 0, err
		}
		if s.lastID == 0 {
			return 0, nil
		}
	}
	var ids []int
	// Bound candidates before checking ownership. LIMIT on the orphan predicate
	// itself would still scan the whole table when there are no orphan rows.
	if err := db.Model(model).Where("id > ? AND id <= ?", s.afterID, s.lastID).
		Order("id").Limit(channelDailyUsageCleanupBatch).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		s.afterID, s.lastID = 0, 0
		return 0, nil
	}
	result := db.Where("id IN ?", ids).
		Where("channel_id NOT IN (?)", db.Model(&Channel{}).Select("id")).Delete(model)
	if result.Error != nil {
		return 0, result.Error
	}
	s.afterID = ids[len(ids)-1]
	if s.afterID >= s.lastID || len(ids) < channelDailyUsageCleanupBatch {
		s.afterID, s.lastID = 0, 0
	}
	return result.RowsAffected, nil
}
