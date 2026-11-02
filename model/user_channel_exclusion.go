package model

import (
	"errors"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const MaxUserChannelExclusions = 256
const MaxUserChannelExclusionBytes = 8192

var ErrInvalidUserChannelExclusions = errors.New("invalid user channel exclusions")

// The relation is authoritative; User.ExcludedChannelIDs is its cached projection.
type UserChannelExclusion struct {
	UserID    int `gorm:"primaryKey;autoIncrement:false;index:idx_exclusion_channel_user,priority:2"`
	ChannelID int `gorm:"primaryKey;autoIncrement:false;index:idx_exclusion_channel_user,priority:1"`
}

func ParseUserChannelExclusions(raw string) ([]int, error) {
	if len(raw) > MaxUserChannelExclusionBytes {
		return nil, ErrInvalidUserChannelExclusions
	}
	if raw == "" || raw == "[]" {
		return nil, nil
	} // Legacy SQL NULL / empty projection.
	if strings.TrimSpace(raw) == "null" {
		return nil, ErrInvalidUserChannelExclusions
	}
	var ids []int
	if err := common.UnmarshalJsonStr(raw, &ids); err != nil || ids == nil || len(ids) > MaxUserChannelExclusions {
		return nil, ErrInvalidUserChannelExclusions
	}
	for _, id := range ids {
		if id <= 0 {
			return nil, ErrInvalidUserChannelExclusions
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

// ReplaceUserChannelExclusions runs inside the admin user-edit transaction.
// Channel locks serialize existence checks against deletion; the user profile
// update in the same transaction fences concurrent projection rebuilds.
func ReplaceUserChannelExclusions(tx *gorm.DB, userID int, ids []int) (kept, removed []int, err error) {
	kept = make([]int, 0, len(ids))
	if len(ids) > 0 {
		err = lockForUpdate(tx).Model(&Channel{}).Where("id IN ?", ids).Order("id").Pluck("id", &kept).Error
		if err != nil {
			return nil, nil, err
		}
	}
	for _, id := range ids {
		if _, found := slices.BinarySearch(kept, id); !found {
			removed = append(removed, id)
		}
	}
	// Serialize two editors even when their channel sets are disjoint and the
	// existing relation set is empty (no relation rows exist to lock yet).
	var user User
	if err = lockForUpdate(tx).Select("id").First(&user, userID).Error; err != nil {
		return nil, nil, err
	}
	if err = tx.Where("user_id = ?", userID).Delete(&UserChannelExclusion{}).Error; err != nil {
		return nil, nil, err
	}
	if len(kept) > 0 {
		rows := make([]UserChannelExclusion, len(kept))
		for i, id := range kept {
			rows[i] = UserChannelExclusion{UserID: userID, ChannelID: id}
		}
		err = tx.CreateInBatches(rows, 100).Error
	}
	return kept, removed, err
}
