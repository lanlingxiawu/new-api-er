package model

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

var configGroupMu sync.Mutex

func SaveConfigGroup(module string, values map[string]string) (bool, error) {
	configGroupMu.Lock()
	defer configGroupMu.Unlock()
	if len(values) == 0 {
		return false, fmt.Errorf("configuration values are required")
	}
	prefixed := make(map[string]string, len(values))
	switch module {
	case "rate_limit_setting":
		draft := operation_setting.GetRateLimitSetting()
		if err := validateGroupFields(values, rateLimitFields); err != nil {
			return false, err
		}
		if err := config.UpdateConfigFromMap(&draft, values); err != nil {
			return false, err
		}
		if err := operation_setting.ValidateRateLimitSetting(draft); err != nil {
			return false, err
		}
		for k, v := range values {
			prefixed[module+"."+k] = v
		}
		if err := persistOptionsTx(prefixed); err != nil {
			return false, err
		}
		operation_setting.ReplaceRateLimitSetting(draft)
	case "db_pool_setting":
		draft := operation_setting.GetDBPoolSetting()
		if err := validateGroupFields(values, dbPoolFields); err != nil {
			return false, err
		}
		if err := config.UpdateConfigFromMap(&draft, values); err != nil {
			return false, err
		}
		if err := operation_setting.ValidateDBPoolSetting(draft); err != nil {
			return false, err
		}
		for k, v := range values {
			prefixed[module+"."+k] = v
		}
		if err := persistOptionsTx(prefixed); err != nil {
			return false, err
		}
		operation_setting.ReplaceDBPoolSetting(draft)
	case "user_session_setting":
		draft := operation_setting.GetUserSessionSetting()
		if err := validateGroupFields(values, userSessionFields); err != nil {
			return false, err
		}
		if err := config.UpdateConfigFromMap(&draft, values); err != nil {
			return false, err
		}
		if err := operation_setting.ValidateUserSessionSetting(draft); err != nil {
			return false, err
		}
		for k, v := range values {
			prefixed[module+"."+k] = v
		}
		if err := persistOptionsTx(prefixed); err != nil {
			return false, err
		}
		operation_setting.ReplaceUserSessionSetting(draft)
	default:
		return false, fmt.Errorf("configuration module is not editable")
	}
	common.OptionMapRWMutex.Lock()
	for k, v := range prefixed {
		common.OptionMap[k] = v
	}
	common.OptionMapRWMutex.Unlock()
	if module == "db_pool_setting" {
		if err := ApplyDBPoolSetting(); err != nil {
			return true, err
		}
	}
	return true, nil
}

func validateGroupFields(values map[string]string, allowed map[string]struct{}) error {
	for k, v := range values {
		if k == "" || strings.Contains(k, ".") {
			return fmt.Errorf("invalid configuration field")
		}
		if _, ok := allowed[k]; !ok {
			return fmt.Errorf("configuration field is not editable")
		}
		if strings.HasSuffix(k, "_enabled") {
			if _, err := strconv.ParseBool(v); err != nil {
				return fmt.Errorf("invalid boolean configuration value")
			}
		} else {
			if _, err := strconv.Atoi(v); err != nil {
				return fmt.Errorf("invalid integer configuration value")
			}
		}
	}
	return nil
}

var rateLimitFields = fieldSet("global_api_enabled", "global_api_num", "global_api_duration_sec", "global_api_user_enabled", "global_api_user_num", "global_api_user_duration_sec", "global_web_enabled", "global_web_num", "global_web_duration_sec", "critical_enabled", "critical_num", "critical_duration_sec", "auth_refresh_enabled", "auth_refresh_num", "auth_refresh_ip_num", "auth_refresh_duration_sec", "search_enabled", "search_num", "search_duration_sec", "log_export_enabled", "log_export_num", "log_export_duration_sec", "redis_timeout_ms")
var dbPoolFields = fieldSet("max_idle_conns", "max_open_conns", "max_lifetime_sec", "log_max_idle_conns", "log_max_open_conns")
var userSessionFields = fieldSet("active_limit", "issuance_limit", "issuance_window_sec", "revoked_retention_days", "hourly_alert_threshold")

func fieldSet(fields ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		out[f] = struct{}{}
	}
	return out
}
func persistOptionsTx(values map[string]string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		for k, v := range values {
			o := Option{Key: k}
			if err := tx.FirstOrCreate(&o, Option{Key: k}).Error; err != nil {
				return err
			}
			o.Value = v
			if err := tx.Save(&o).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
