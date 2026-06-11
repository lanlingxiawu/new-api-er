package operation_setting

import (
	"time"

	"github.com/QuantumNous/new-api/setting/config"
)

// CommissionTierResetSetting configures the monthly employee tier reset.
type CommissionTierResetSetting struct {
	Enabled     bool   `json:"enabled"`
	ResetDay    int    `json:"reset_day"`
	ResetHour   int    `json:"reset_hour"`
	ResetMinute int    `json:"reset_minute"`
	ResetSecond int    `json:"reset_second"`
	Timezone    string `json:"timezone"`
	LastResetAt int64  `json:"last_reset_at"`
}

var commissionTierResetSetting = CommissionTierResetSetting{
	Enabled:     false,
	ResetDay:    10,
	ResetHour:   0,
	ResetMinute: 0,
	ResetSecond: 0,
	Timezone:    "Asia/Shanghai",
	LastResetAt: 0,
}

func init() {
	config.GlobalConfig.Register("commission_tier_reset_setting", &commissionTierResetSetting)
}

func GetCommissionTierResetSetting() *CommissionTierResetSetting {
	return &commissionTierResetSetting
}

func ResolveCommissionTierResetLocation(timezone string) (*time.Location, string) {
	if timezone == "Local" {
		return time.Local, "Local"
	}
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Local, "Local"
	}
	return loc, timezone
}

func IsCommissionTierResetEnabled() bool {
	return commissionTierResetSetting.Enabled
}
