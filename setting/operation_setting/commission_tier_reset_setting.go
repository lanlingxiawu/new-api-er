package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// CommissionTierResetSetting 提成等级与"本期业绩/本期提成"月度自动重置配置。
//
// 重置时刻 = 每月 ResetDay 日 ResetHour:ResetMinute:ResetSecond（服务器本地时区）；
// 若当月天数小于 ResetDay，自动取该月最后一天。
//
// LastResetAt 为运行态字段，记录上次实际执行重置的"调度时刻"（unix 秒），
// 由后台任务（service.StartCommissionTierResetTask）维护，不通过管理员表单提交。
type CommissionTierResetSetting struct {
	Enabled     bool   `json:"enabled"`
	ResetDay    int    `json:"reset_day"`
	ResetHour   int    `json:"reset_hour"`
	ResetMinute int    `json:"reset_minute"`
	ResetSecond int    `json:"reset_second"`
	Timezone    string `json:"timezone"`
	LastResetAt int64  `json:"last_reset_at"`
}

// 默认配置：默认关闭，每月 1 日 00:00:00 重置。
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

// GetCommissionTierResetSetting 获取提成等级月度重置配置。
func GetCommissionTierResetSetting() *CommissionTierResetSetting {
	return &commissionTierResetSetting
}

// IsCommissionTierResetEnabled 是否启用提成等级月度自动重置。
func IsCommissionTierResetEnabled() bool {
	return commissionTierResetSetting.Enabled
}
