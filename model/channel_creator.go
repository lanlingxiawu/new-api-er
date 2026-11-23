package model

import (
	"context"
	"errors"
	"slices"

	"gorm.io/gorm"
)

// ChannelCreator is management-only metadata. It never enters the relay cache.
type ChannelCreator struct {
	ChannelID     int   `gorm:"primaryKey;autoIncrement:false;index:idx_channel_creator_user,priority:2"`
	CreatorUserID int   `gorm:"not null;index:idx_channel_creator_user,priority:1"`
	CreatedAt     int64 `gorm:"not null"`
}

var ErrChannelNotAccessible = errors.New("This channel is not accessible. Use the dashboard or select a channel you created.")

func ChannelsCreatedBy(db *gorm.DB, userID int) *gorm.DB {
	return db.Model(&Channel{}).Where("channels.id IN (?)", db.Session(&gorm.Session{NewDB: true}).Model(&ChannelCreator{}).Select("channel_id").Where("creator_user_id = ?", userID))
}

func RequireChannelCreator(ctx context.Context, userID int, ids []int) error {
	if userID <= 0 || len(ids) == 0 || len(ids) > 100 {
		return ErrChannelNotAccessible
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if ids[0] <= 0 {
		return ErrChannelNotAccessible
	}
	var count int64
	if err := ChannelsCreatedBy(DB.WithContext(ctx), userID).Where("channels.id IN ?", ids).Count(&count).Error; err != nil {
		return err
	}
	if count != int64(len(ids)) {
		return ErrChannelNotAccessible
	}
	return nil
}
