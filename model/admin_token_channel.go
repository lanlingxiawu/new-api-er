package model

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// UpdateCreatedChannels atomically applies a bounded management change to the
// immutable creator set. Routing changes use the same IDs and transaction,
// including when a tag is renamed to an existing tag.
func UpdateCreatedChannels(ctx context.Context, userID int, ids []int, updates map[string]any, daily DailyLimitEdit) error {
	_, err := UpdateCreatedChannelsWithChanges(ctx, userID, ids, updates, daily)
	return err
}

// CreatedChannelChanges describes committed configuration changes without
// copying credentials, configuration values or internal accounting state.
type CreatedChannelChanges struct {
	Audit        *AuditChanges
	IDs          []int
	Fields       []string
	CacheChanged bool // Includes internal daily-limit resets excluded from audit fields.
}

func UpdateCreatedChannelsWithChanges(ctx context.Context, userID int, ids []int, updates map[string]any, daily DailyLimitEdit) (CreatedChannelChanges, error) {
	result := CreatedChannelChanges{IDs: []int{}, Fields: []string{}}
	var auditRows [][2]Channel
	if len(ids) == 0 || len(ids) > 100 {
		return result, ErrChannelNotAccessible
	}
	if err := daily.Validate(); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	statement := &gorm.Statement{DB: DB}
	if err := statement.Parse(&Channel{}); err != nil {
		return result, err
	}
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var channels []Channel
		if err := lockForUpdate(ChannelsCreatedBy(tx, userID).Where("channels.id IN ?", ids)).Omit("key").Find(&channels).Error; err != nil {
			return err
		}
		if len(channels) != len(ids) {
			return ErrChannelNotAccessible
		}
		before := make(map[int]Channel, len(channels))
		now := common.GetTimestamp()
		for _, channel := range channels {
			before[channel.Id] = channel
			pending := make(map[string]any, len(updates)+4)
			maps.Copy(pending, updates)
			if status, ok := updates["status"].(int); ok && status == common.ChannelStatusEnabled && channel.Status != status && channel.DailyLimitRecoverMinutes > 0 {
				pending["daily_limit_period_start"] = now
			}
			dailyUpdates := daily.updates(now)
			delete(dailyUpdates, "daily_limit_period_start")
			if minutes, ok := dailyUpdates["daily_limit_recover_minutes"].(int); ok && minutes != channel.DailyLimitRecoverMinutes {
				dailyUpdates["daily_limit_period_start"] = now
			}
			maps.Copy(pending, dailyUpdates)
			for name, value := range pending {
				field := statement.Schema.LookUpField(name)
				if field == nil {
					return fmt.Errorf("unknown channel management field: %s", name)
				}
				previous, _ := field.ValueOf(ctx, reflect.ValueOf(&channel))
				oldValue, newValue := reflect.ValueOf(previous), reflect.ValueOf(value)
				if oldValue.IsValid() && oldValue.Kind() == reflect.Pointer {
					oldValue = oldValue.Elem()
				}
				if newValue.IsValid() && newValue.Kind() == reflect.Pointer {
					newValue = newValue.Elem()
				}
				if !oldValue.IsValid() && !newValue.IsValid() {
					delete(pending, name)
				} else if oldValue.IsValid() && newValue.IsValid() && newValue.CanConvert(oldValue.Type()) && reflect.DeepEqual(oldValue.Interface(), newValue.Convert(oldValue.Type()).Interface()) {
					delete(pending, name)
				}
			}
			if len(pending) == 0 {
				continue
			}
			if err := ChannelsCreatedBy(tx, userID).Where("channels.id = ?", channel.Id).Updates(pending).Error; err != nil {
				return err
			}
			result.CacheChanged = true
		}
		if !result.CacheChanged {
			return nil
		}
		if err := ChannelsCreatedBy(tx, userID).Where("channels.id IN ?", ids).Omit("key").Find(&channels).Error; err != nil {
			return err
		}
		changedFields := make(map[string]bool)
		for i := range channels {
			channel := &channels[i]
			previous := before[channel.Id]
			fields := map[string][2]any{
				"name": {previous.Name, channel.Name}, "remark": {previous.Remark, channel.Remark},
				"tag": {previous.Tag, channel.Tag}, "status": {previous.Status, channel.Status},
				"models": {previous.Models, channel.Models}, "group": {previous.Group, channel.Group},
				"priority": {previous.Priority, channel.Priority}, "weight": {previous.Weight, channel.Weight},
				"model_mapping":   {previous.ModelMapping, channel.ModelMapping},
				"param_override":  {previous.ParamOverride, channel.ParamOverride},
				"header_override": {previous.HeaderOverride, channel.HeaderOverride},
				"daily_limit": {
					[3]any{previous.DailyQuotaLimit, previous.DailyLimitAutoRecover, previous.DailyLimitRecoverMinutes},
					[3]any{channel.DailyQuotaLimit, channel.DailyLimitAutoRecover, channel.DailyLimitRecoverMinutes},
				},
			}
			changed := false
			for field, pair := range fields {
				if !reflect.DeepEqual(pair[0], pair[1]) {
					changed = true
					changedFields[field] = true
				}
			}
			if changed {
				auditRows = append(auditRows, [2]Channel{previous, *channel})
				result.IDs = append(result.IDs, channel.Id)
			}
			if status, ok := updates["status"].(int); ok && previous.Status != channel.Status {
				info := channel.GetOtherInfo()
				info["status_reason"] = "manual operation"
				info["status_time"] = common.GetTimestamp()
				channel.SetOtherInfo(info)
				if status == common.ChannelStatusEnabled {
					delete(info, "status_reason")
					channel.SetOtherInfo(info)
				}
				if err := ChannelsCreatedBy(tx, userID).Where("channels.id = ?", channel.Id).Update("other_info", channel.OtherInfo).Error; err != nil {
					return err
				}
			}
			if previous.Models != channel.Models || previous.Group != channel.Group {
				if err := channel.UpdateAbilities(tx); err != nil {
					return err
				}
				continue
			}
			abilityUpdates := make(map[string]any)
			if previous.Status != channel.Status {
				abilityUpdates["enabled"] = channel.Status == common.ChannelStatusEnabled
			}
			if !reflect.DeepEqual(previous.Priority, channel.Priority) {
				abilityUpdates["priority"] = channel.Priority
			}
			if previous.GetWeight() != channel.GetWeight() {
				abilityUpdates["weight"] = uint(channel.GetWeight())
			}
			if !reflect.DeepEqual(previous.Tag, channel.Tag) {
				abilityUpdates["tag"] = channel.Tag
			}
			if len(abilityUpdates) > 0 {
				if err := tx.Model(&Ability{}).Where("channel_id = ?", channel.Id).Updates(abilityUpdates).Error; err != nil {
					return err
				}
			}
		}
		for field := range changedFields {
			result.Fields = append(result.Fields, field)
		}
		return nil
	})
	if err != nil {
		return CreatedChannelChanges{}, err
	}
	slices.Sort(result.IDs)
	result.Audit = NewAuditChanges()
	for _, pair := range auditRows {
		target := AuditTarget("channel", pair[1].Id, pair[1].Name)
		target.Consistency = "atomic"
		result.Audit.Add(target, ChannelAuditSnapshot(&pair[0]), ChannelAuditSnapshot(&pair[1]))
	}
	slices.Sort(result.Fields)
	return result, nil
}
