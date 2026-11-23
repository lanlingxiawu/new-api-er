package auditsecurity

import (
	"context"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

type securityActorName struct {
	id       int
	username *string
}

// Only pass actors from records that already passed the reader's visibility
// checks. Names are current display data, never authentication evidence.
func loadSecurityActorNames(ctx context.Context, db *gorm.DB, actors []securityActorName) error {
	ids := make([]int, 0, len(actors))
	targets := make(map[int][]*string, len(actors))
	for _, actor := range actors {
		*actor.username = ""
		if actor.id <= 0 {
			continue
		}
		if _, seen := targets[actor.id]; !seen {
			ids = append(ids, actor.id)
		}
		targets[actor.id] = append(targets[actor.id], actor.username)
	}
	for start := 0; start < len(ids); start += 100 {
		var users []struct {
			ID       int
			Username string
		}
		if err := db.WithContext(ctx).Model(&model.User{}).Select("id,username").Where("id IN ?", ids[start:min(start+100, len(ids))]).Find(&users).Error; err != nil {
			return err
		}
		for _, user := range users {
			for _, target := range targets[user.ID] {
				*target = user.Username
			}
		}
	}
	return nil
}
