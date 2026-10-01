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
	// The two retry gates below are opt-in enhancements and default to off, so
	// an unconfigured deployment retries exactly as before they existed: the
	// number of attempts is decided by RetryTimes alone.
	//
	// defaultRetryMinBudgetSecs: when set, another attempt is not started once
	// less than this much of the total budget remains — it would be cancelled
	// mid-flight while upstream still bills for the tokens it produced.
	defaultRetryMinBudgetSecs = 0
	// defaultMaxTotalAttempts: when set, bounds one request end to end. Unlike
	// RetryTimes it is NOT reset when auto-group routing moves to the next
	// group, which is what lets a request reach 分组数 × (RetryTimes+1) attempts.
	defaultMaxTotalAttempts = 0
	// MaxRelayTotalAttemptsLimit keeps an operator typo from disabling the gate
	// by way of an absurd value.
	MaxRelayTotalAttemptsLimit = 100
)

type RelayTimeoutSetting struct {
	Enabled                bool `json:"enabled"`
	ResponseTimeoutSeconds int  `json:"response_timeout_seconds"`
	TotalTimeoutSeconds    int  `json:"total_timeout_seconds"`
	// RetryMinBudgetSeconds: stop retrying when less than this much of the total
	// budget remains. 0 disables the check. Only meaningful when a total timeout
	// is configured — without one there is no budget to measure against.
	RetryMinBudgetSeconds int `json:"retry_min_budget_seconds"`
	// MaxTotalAttempts caps attempts per request across all groups. 0 means
	// unlimited (legacy behaviour). Never interpreted as "zero attempts": the
	// retry loop must always run at least once or the pre-consumed quota would
	// be neither refunded nor settled.
	MaxTotalAttempts int `json:"max_total_attempts"`
}

var relayTimeoutSetting = RelayTimeoutSetting{
	Enabled:                true,
	ResponseTimeoutSeconds: defaultRelayResponseTimeoutSecs,
	TotalTimeoutSeconds:    0,
	RetryMinBudgetSeconds:  defaultRetryMinBudgetSecs,
	MaxTotalAttempts:       defaultMaxTotalAttempts,
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
	if setting.RetryMinBudgetSeconds < 0 || setting.RetryMinBudgetSeconds > MaxRelayTimeoutSettingSeconds {
		return fmt.Errorf("retry min budget must be between 0 and %d seconds", MaxRelayTimeoutSettingSeconds)
	}
	// Negative would be indistinguishable from the "unlimited" zero once the
	// gate compares against it, so reject it outright.
	if setting.MaxTotalAttempts < 0 || setting.MaxTotalAttempts > MaxRelayTotalAttemptsLimit {
		return fmt.Errorf("max total attempts must be between 0 and %d", MaxRelayTotalAttemptsLimit)
	}
	return nil
}

// RelayMaxTotalAttempts returns the effective per-request attempt cap, or 0 when
// unlimited. No floor is applied here: the first attempt is guaranteed by
// service.ShouldAttemptRelay, which admits it regardless of the cap (design §7.2).
func (setting *RelayTimeoutSetting) RelayMaxTotalAttempts() int {
	if setting == nil || setting.MaxTotalAttempts <= 0 {
		return 0
	}
	return setting.MaxTotalAttempts
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
			RetryMinBudgetSeconds:  defaultRetryMinBudgetSecs,
			MaxTotalAttempts:       defaultMaxTotalAttempts,
		}
		publishRelayTimeoutSetting()
	})
}
