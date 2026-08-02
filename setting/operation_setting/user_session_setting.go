package operation_setting

import (
	"fmt"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

const userSessionConfigName = "user_session_setting"

type UserSessionSetting struct {
	ActiveLimit          int `json:"active_limit"`
	IssuanceLimit        int `json:"issuance_limit"`
	IssuanceWindowSec    int `json:"issuance_window_sec"`
	RevokedRetentionDays int `json:"revoked_retention_days"`
	HourlyAlertThreshold int `json:"hourly_alert_threshold"`
}

type UserSessionSnapshot struct {
	ActiveLimit           int
	IssuanceLimit         int
	IssuanceWindowSeconds int64
	RevokedRetentionDays  int
	HourlyAlertThreshold  int
}

var userSessionSetting = UserSessionSetting{50, 100, 86400, 7, 5000}
var userSessionSnapshot atomic.Pointer[UserSessionSnapshot]

func init() {
	config.GlobalConfig.Register(userSessionConfigName, &userSessionSetting)
	PublishUserSessionSetting()
}

func GetUserSessionSetting() UserSessionSetting {
	var out UserSessionSetting
	config.WithConfigDraft(func() { out = userSessionSetting })
	return out
}

func ReplaceUserSessionSetting(setting UserSessionSetting) {
	config.WithConfigDraft(func() { userSessionSetting = setting })
	PublishUserSessionSetting()
}

func PublishUserSessionSetting() {
	setting := GetUserSessionSetting()
	snapshot := UserSessionSnapshot{setting.ActiveLimit, setting.IssuanceLimit, int64(setting.IssuanceWindowSec), setting.RevokedRetentionDays, setting.HourlyAlertThreshold}
	userSessionSnapshot.Store(&snapshot)
}

func GetUserSessionSnapshot() *UserSessionSnapshot { return userSessionSnapshot.Load() }

func ValidateUserSessionSetting(setting UserSessionSetting) error {
	if setting.ActiveLimit < 1 || setting.ActiveLimit > 10_000 {
		return fmt.Errorf("active session limit is out of range")
	}
	if setting.IssuanceLimit < 1 || setting.IssuanceLimit > 100_000 {
		return fmt.Errorf("session issuance limit is out of range")
	}
	if setting.IssuanceWindowSec < 1 || setting.IssuanceWindowSec > 365*24*60*60 {
		return fmt.Errorf("session issuance window is out of range")
	}
	if setting.RevokedRetentionDays < 1 || setting.RevokedRetentionDays > 365 {
		return fmt.Errorf("revoked session retention is out of range")
	}
	if setting.HourlyAlertThreshold < 1 || setting.HourlyAlertThreshold > 10_000_000 {
		return fmt.Errorf("hourly session alert threshold is out of range")
	}
	if int64(setting.IssuanceWindowSec) > int64(setting.RevokedRetentionDays)*24*60*60 {
		return fmt.Errorf("session issuance window cannot exceed revoked retention")
	}
	return nil
}

func ApplyUserSessionEnvDefaults() {
	setting := &userSessionSetting
	setting.ActiveLimit = common.GetEnvOrDefault("USER_SESSION_ACTIVE_LIMIT", setting.ActiveLimit)
	setting.IssuanceLimit = common.GetEnvOrDefault("USER_SESSION_ISSUANCE_LIMIT", setting.IssuanceLimit)
	setting.IssuanceWindowSec = common.GetEnvOrDefault("USER_SESSION_ISSUANCE_WINDOW_SECONDS", setting.IssuanceWindowSec)
	setting.RevokedRetentionDays = common.GetEnvOrDefault("USER_SESSION_REVOKED_RETENTION_DAYS", setting.RevokedRetentionDays)
	setting.HourlyAlertThreshold = common.GetEnvOrDefault("USER_SESSION_HOURLY_ALERT_THRESHOLD", setting.HourlyAlertThreshold)
	if setting.ActiveLimit < 1 {
		setting.ActiveLimit = 50
	}
	if setting.IssuanceLimit < 1 {
		setting.IssuanceLimit = 100
	}
	if setting.IssuanceWindowSec < 1 {
		setting.IssuanceWindowSec = 86400
	}
	if setting.RevokedRetentionDays < 1 {
		setting.RevokedRetentionDays = 7
	}
	if setting.HourlyAlertThreshold < 1 {
		setting.HourlyAlertThreshold = 5000
	}
	if setting.ActiveLimit > 10_000 {
		setting.ActiveLimit = 50
	}
	if setting.IssuanceLimit > 100_000 {
		setting.IssuanceLimit = 100
	}
	if setting.IssuanceWindowSec > 365*24*60*60 {
		setting.IssuanceWindowSec = 86400
	}
	if setting.RevokedRetentionDays > 365 {
		setting.RevokedRetentionDays = 7
	}
	if setting.HourlyAlertThreshold > 10_000_000 {
		setting.HourlyAlertThreshold = 5000
	}
	retentionSeconds := int64(setting.RevokedRetentionDays) * 24 * 60 * 60
	if int64(setting.IssuanceWindowSec) > retentionSeconds {
		common.SysError(fmt.Sprintf("USER_SESSION_ISSUANCE_WINDOW_SECONDS exceeds revoked retention; configured_window_seconds=%d revoked_retention_seconds=%d effective_window_seconds=%d", setting.IssuanceWindowSec, retentionSeconds, retentionSeconds))
		setting.IssuanceWindowSec = int(retentionSeconds)
	}
	PublishUserSessionSetting()
}
