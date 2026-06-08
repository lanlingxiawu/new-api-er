package model

import "gorm.io/gorm"

func applyCreatedAtTimeRange(tx *gorm.DB, startTime, endTime int64) *gorm.DB {
	if startTime != 0 {
		tx = tx.Where("created_at >= ?", startTime)
	}
	if endTime != 0 {
		tx = tx.Where("created_at <= ?", endTime)
	}
	return tx
}
