package auditsecurity

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

func DetailedHealth(ctx context.Context) Health {
	result := Status()
	cfg := operation_setting.GetAuditSecuritySetting()
	result.IPIntelligenceEnabled = cfg.IPIntelligenceEnabled
	db, err := model.AuditReadDB(false)
	if err != nil {
		result.StorageReady = false
		result.ErrorCode = "storage_unavailable"
		return result
	}
	result.StorageReady = true
	if err := db.WithContext(ctx).Model(&model.SecurityActivityState{}).Where("name = ?", "activity").Select("revision").Scan(&result.ActivityRevision).Error; err != nil {
		result.StorageReady = false
		result.ErrorCode = "storage_unavailable"
		return result
	}
	capacity, err := GetSecurityCapacity(ctx, db, 100)
	if err != nil {
		result.StorageReady = false
		result.ErrorCode = "storage_unavailable"
		return result
	}
	result.Capacity = capacity
	providers, err := operation_setting.ParseIPIntelligenceProviders(cfg.IPIntelligenceProvidersJSON)
	if err != nil || cfg.IPIntelligenceEnabled && operation_setting.ActiveIPIntelligenceProvider(providers) == nil {
		result.ErrorCode = "invalid_provider_config"
		return result
	}
	result.Providers = currentIPProviderHealth(ctx, db, providers)
	if result.Enabled && !result.Suspended {
		var state model.SecurityState
		if err := db.WithContext(ctx).Select("last_scan,cursor_at").Where("name = ?", "scanner").Take(&state).Error; err == nil {
			result.LastScan = max(result.LastScan, state.LastScan)
			result.CursorAt = max(result.CursorAt, state.CursorAt)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			result.ErrorCode = "storage_unavailable"
		}
	}
	return result
}
