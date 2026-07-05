package operation_setting

import (
	"time"

	"github.com/QuantumNous/new-api/setting/config"
)

// 统计/重置周期模式。
const (
	// CommissionPeriodModeResetDay 按重置日统计：周期以 ResetDay 为锚点，
	// 上一次重置日 → 下一次重置日为一个周期（当月天数不足 ResetDay 时取当月最后一天）。
	CommissionPeriodModeResetDay = "reset_day"
	// CommissionPeriodModeNaturalMonth 按自然月统计：周期固定为每月 1 日 → 当月最后一天，
	// 与 ResetDay 无关，重置发生在每月 1 日 00:00:00（配置时刻）。
	CommissionPeriodModeNaturalMonth = "natural_month"
)

// CommissionTierResetSetting configures the monthly employee tier reset.
type CommissionTierResetSetting struct {
	Enabled     bool   `json:"enabled"`
	PeriodMode  string `json:"period_mode"`
	ResetDay    int    `json:"reset_day"`
	ResetHour   int    `json:"reset_hour"`
	ResetMinute int    `json:"reset_minute"`
	ResetSecond int    `json:"reset_second"`
	Timezone    string `json:"timezone"`
	LastResetAt int64  `json:"last_reset_at"`
}

var commissionTierResetSetting = CommissionTierResetSetting{
	Enabled:     false,
	PeriodMode:  CommissionPeriodModeResetDay,
	ResetDay:    10,
	ResetHour:   0,
	ResetMinute: 0,
	ResetSecond: 0,
	Timezone:    "Asia/Shanghai",
	LastResetAt: 0,
}

// IsNaturalMonthMode 是否按自然月（每月 1 日 → 月底最后一天）统计与重置。
func (s *CommissionTierResetSetting) IsNaturalMonthMode() bool {
	return s != nil && s.PeriodMode == CommissionPeriodModeNaturalMonth
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
