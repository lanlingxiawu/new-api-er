package operation_setting

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/config"
)

const (
	relayTimeoutConfigName          = "relay_timeout_setting"
	MaxRelayTimeoutSettingSeconds   = 7 * 24 * 60 * 60
	defaultRelayResponseTimeoutSecs = 300
)

type RelayTimeoutSetting struct {
	Enabled                bool `json:"enabled"`
	ResponseTimeoutSeconds int  `json:"response_timeout_seconds"`
	TotalTimeoutSeconds    int  `json:"total_timeout_seconds"`
}

var relayTimeoutSetting = RelayTimeoutSetting{
	Enabled:                true,
	ResponseTimeoutSeconds: defaultRelayResponseTimeoutSecs,
	TotalTimeoutSeconds:    0,
}

var relayTimeoutSnapshot config.Snapshot[RelayTimeoutSetting]

func init() {
	config.GlobalConfig.RegisterSnapshot(relayTimeoutConfigName, &relayTimeoutSetting, publishRelayTimeoutSetting)
}

// publishRelayTimeoutSetting runs while the config draft lock is held.
func publishRelayTimeoutSetting() {
	relayTimeoutSnapshot.Publish(relayTimeoutSetting)
}

func GetRelayTimeoutSnapshot() *RelayTimeoutSetting {
	return relayTimeoutSnapshot.Load()
}

func GetRelayTimeoutSetting() RelayTimeoutSetting {
	var out RelayTimeoutSetting
	config.WithConfigDraft(func() { out = relayTimeoutSetting })
	return out
}

func ReplaceRelayTimeoutSetting(setting RelayTimeoutSetting) {
	config.WithConfigDraft(func() {
		relayTimeoutSetting = setting
		publishRelayTimeoutSetting()
	})
}

func ValidateRelayTimeoutSetting(setting RelayTimeoutSetting) error {
	if setting.ResponseTimeoutSeconds < 0 || setting.ResponseTimeoutSeconds > MaxRelayTimeoutSettingSeconds {
		return fmt.Errorf("relay response timeout must be between 0 and %d seconds", MaxRelayTimeoutSettingSeconds)
	}
	if setting.TotalTimeoutSeconds < 0 || setting.TotalTimeoutSeconds > MaxRelayTimeoutSettingSeconds {
		return fmt.Errorf("relay total timeout must be between 0 and %d seconds", MaxRelayTimeoutSettingSeconds)
	}
	return nil
}

func relayTimeoutEnvDefault(value int) int {
	if value <= 0 {
		return 0
	}
	if value > MaxRelayTimeoutSettingSeconds {
		return MaxRelayTimeoutSettingSeconds
	}
	return value
}

// ApplyRelayTimeoutEnvDefaults copies the already-parsed deployment values
// into the DB-backed draft before options are loaded. Saved DB options still
// win during ConfigManager.LoadFromDB.
func ApplyRelayTimeoutEnvDefaults() {
	config.WithConfigDraft(func() {
		relayTimeoutSetting = RelayTimeoutSetting{
			Enabled:                true,
			ResponseTimeoutSeconds: relayTimeoutEnvDefault(constant.StreamingTimeout),
			TotalTimeoutSeconds:    relayTimeoutEnvDefault(common.RelayTimeout),
		}
		publishRelayTimeoutSetting()
	})
}
