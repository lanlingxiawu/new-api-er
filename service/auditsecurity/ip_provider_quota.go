package auditsecurity

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func reserveIPProviderCall(ctx context.Context, db *gorm.DB, provider operation_setting.IPIntelligenceProvider, hash string) (bool, error) {
	reserved := false
	now := time.Now().Unix()
	day := time.Now().UTC().Format("2006-01-02")
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state := model.SecurityIPProviderState{ProviderID: provider.ID, Day: day}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
			return err
		}
		if err := tx.Where("provider_id = ? AND day = ?", provider.ID, day).Take(&state).Error; err != nil {
			return err
		}
		if state.ConfigHash != hash {
			updates := map[string]any{"config_hash": hash, "failures": 0, "circuit_until": 0, "last_error": "", "updated_at": now}
			if err := tx.Model(&model.SecurityIPProviderState{}).Where("provider_id = ? AND day = ?", provider.ID, day).Updates(updates).Error; err != nil {
				return err
			}
			state.CircuitUntil = 0
		}
		query := tx.Model(&model.SecurityIPProviderState{}).Where("provider_id = ? AND day = ? AND circuit_until <= ?", provider.ID, day, now)
		if provider.DailyLimit > 0 {
			query = query.Where("used < ?", provider.DailyLimit)
		}
		result := query.Updates(map[string]any{"used": gorm.Expr("used + 1"), "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		reserved = result.RowsAffected == 1
		return nil
	})
	return reserved, err
}

func recordIPProviderResult(ctx context.Context, db *gorm.DB, provider operation_setting.IPIntelligenceProvider, hash, failure string, retryAfter int64) {
	now := time.Now().Unix()
	day := time.Now().UTC().Format("2006-01-02")
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state model.SecurityIPProviderState
		if err := tx.Where("provider_id = ? AND day = ?", provider.ID, day).Take(&state).Error; err != nil {
			return err
		}
		if state.ConfigHash != hash {
			return nil
		}
		updates := map[string]any{"updated_at": now, "last_error": failure}
		if failure == "" {
			updates["failures"] = 0
			updates["circuit_until"] = 0
		} else {
			failures := state.Failures + 1
			updates["failures"] = failures
			backoff := min(300*(1<<min(failures-1, 3)), 3600)
			circuitUntil := now + int64(backoff)
			if failure == "quota_exceeded" || failure == "http_429" {
				circuitUntil = retryAfter
				if circuitUntil <= now {
					circuitUntil = time.Now().UTC().AddDate(0, 0, 1).Truncate(24 * time.Hour).Unix()
				}
			} else if failure == "http_403" || failure == "http_401" {
				circuitUntil = now + 86400
			}
			updates["circuit_until"] = circuitUntil
		}
		return tx.Model(&model.SecurityIPProviderState{}).Where("provider_id = ? AND day = ?", provider.ID, day).Updates(updates).Error
	}); err != nil {
		common.SysError("audit IP intelligence state persistence failed")
	}
}

func currentIPProviderHealth(ctx context.Context, db *gorm.DB, providers []operation_setting.IPIntelligenceProvider) []SecurityProviderHealth {
	items := make([]SecurityProviderHealth, 0, len(providers))
	day := time.Now().UTC().Format("2006-01-02")
	for _, provider := range providers {
		item := SecurityProviderHealth{
			ID: provider.ID, Name: provider.Name, Type: provider.Type, Enabled: provider.Enabled,
			Priority: provider.Priority, DailyLimit: provider.DailyLimit, KeyConfigured: provider.APIKey != "",
			Capabilities: provider.Capabilities,
		}
		var state model.SecurityIPProviderState
		if err := db.WithContext(ctx).Where("provider_id = ? AND day = ?", provider.ID, day).Take(&state).Error; err == nil {
			item.Used, item.CircuitUntil, item.LastError = state.Used, state.CircuitUntil, state.LastError
		}
		items = append(items, item)
	}
	return items
}

type SecurityProviderHealth struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	Enabled       bool     `json:"enabled"`
	Priority      int      `json:"priority"`
	DailyLimit    int64    `json:"daily_limit"`
	KeyConfigured bool     `json:"key_configured"`
	Capabilities  []string `json:"capabilities"`
	Used          int64    `json:"used"`
	CircuitUntil  int64    `json:"circuit_until"`
	LastError     string   `json:"last_error"`
}
