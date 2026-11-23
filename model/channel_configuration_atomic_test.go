package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChannelCreationRollsBackCostFailure(t *testing.T) {
	db := newAdminTokenModelDB(t)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}, &ChannelCreator{}, &ChannelCostConfig{}))
	zero := 0.0
	channels := []Channel{{Name: "atomic-create", Key: "secret", Models: "m", Group: "default", CostRatio: &zero}}
	tables := []any{&Channel{}, &Ability{}, &ChannelCreator{}}
	before := make([]int64, len(tables))
	for i, table := range tables {
		require.NoError(t, db.Model(table).Count(&before[i]).Error)
	}
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:atomic_channel_cost_create", func(tx *gorm.DB) {
		if tx.Statement.Table == "channel_cost_configs" {
			tx.AddError(errors.New("injected cost create failure"))
		}
	}))
	err := BatchInsertChannels(channels, 41)
	require.ErrorContains(t, err, "injected cost create failure")
	for i, table := range tables {
		var count int64
		require.NoError(t, db.Model(table).Count(&count).Error)
		require.Equal(t, before[i], count)
	}

	db.Callback().Create().Remove("test:atomic_channel_cost_create")
	require.NoError(t, BatchInsertChannels(channels, 41))
	t.Cleanup(func() {
		db.Where("channel_id = ?", channels[0].Id).Delete(&ChannelCostConfig{})
		db.Where("channel_id = ?", channels[0].Id).Delete(&Ability{})
		db.Where("channel_id = ?", channels[0].Id).Delete(&ChannelCreator{})
		db.Unscoped().Delete(&Channel{}, channels[0].Id)
	})
	var config ChannelCostConfig
	require.NoError(t, db.Where("channel_id = ?", channels[0].Id).First(&config).Error)
	require.Zero(t, config.CostRatio)
	require.NoError(t, RequireChannelCreator(t.Context(), 41, []int{channels[0].Id}))
}

func TestChannelUpdateConfigurationRollsBackAssociatedFailures(t *testing.T) {
	for _, failure := range []string{"daily_limit", "abilities", "cost"} {
		t.Run(failure, func(t *testing.T) {
			db := newAdminTokenModelDB(t)
			require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}, &ChannelCreator{}, &ChannelCostConfig{}))
			channels := []Channel{{Name: "before", Key: "old-secret", Models: "old", Group: "default", DailyQuotaLimit: 100}}
			require.NoError(t, BatchInsertChannels(channels, 41))
			id := channels[0].Id
			t.Cleanup(func() {
				db.Where("channel_id = ?", id).Delete(&ChannelCostConfig{})
				db.Where("channel_id = ?", id).Delete(&Ability{})
				db.Where("channel_id = ?", id).Delete(&ChannelCreator{})
				db.Unscoped().Delete(&Channel{}, id)
			})
			require.NoError(t, UpsertChannelCostConfig(id, 1.5, ""))
			callbackName := "test:atomic_channel_update_" + failure
			switch failure {
			case "daily_limit":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
					if tx.Statement.Table != "channels" {
						return
					}
					if updates, ok := tx.Statement.Dest.(map[string]any); ok {
						if _, exists := updates["daily_quota_limit"]; exists {
							tx.AddError(errors.New("injected daily limit failure"))
						}
					}
				}))
			case "abilities":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
					if tx.Statement.Table == "abilities" {
						tx.AddError(errors.New("injected abilities failure"))
					}
				}))
			case "cost":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
					if tx.Statement.Table == "channel_cost_configs" {
						tx.AddError(errors.New("injected cost update failure"))
					}
				}))
			}

			updated := Channel{Id: id, Name: "after", Key: "new-secret", Models: "new", Group: "default"}
			zeroLimit := int64(0)
			nextRatio := 2.0
			updated.CostRatio = &nextRatio
			edit := DailyLimitEdit{QuotaLimit: &zeroLimit}
			err := updated.UpdateConfiguration(edit, 41)
			require.Error(t, err)
			assertOriginalChannelConfiguration(t, db, id)

			db.Callback().Update().Remove(callbackName)
			db.Callback().Create().Remove(callbackName)
			require.NoError(t, updated.UpdateConfiguration(edit, 41))
			var stored Channel
			require.NoError(t, db.First(&stored, id).Error)
			require.Equal(t, "after", stored.Name)
			require.Equal(t, "new-secret", stored.Key)
			require.Zero(t, stored.DailyQuotaLimit)
			var config ChannelCostConfig
			require.NoError(t, db.Where("channel_id = ?", id).First(&config).Error)
			require.Equal(t, nextRatio, config.CostRatio)
			var abilities []Ability
			require.NoError(t, db.Where("channel_id = ?", id).Find(&abilities).Error)
			require.Len(t, abilities, 1)
			require.Equal(t, "new", abilities[0].Model)
		})
	}
}

func assertOriginalChannelConfiguration(t *testing.T, db *gorm.DB, id int) {
	t.Helper()
	var stored Channel
	require.NoError(t, db.First(&stored, id).Error)
	require.Equal(t, "before", stored.Name)
	require.Equal(t, "old-secret", stored.Key)
	require.Equal(t, "old", stored.Models)
	require.EqualValues(t, 100, stored.DailyQuotaLimit)
	var config ChannelCostConfig
	require.NoError(t, db.Where("channel_id = ?", id).First(&config).Error)
	require.Equal(t, 1.5, config.CostRatio)
	var abilities []Ability
	require.NoError(t, db.Where("channel_id = ?", id).Find(&abilities).Error)
	require.Len(t, abilities, 1)
	require.Equal(t, "old", abilities[0].Model)
}
