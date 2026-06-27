package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

type ExportSetting struct {
	// RateLimitCooldownSec 单用户导出冷却窗口（秒）。<=0 时回退默认值。
	RateLimitCooldownSec int `json:"rate_limit_cooldown_sec"`
	// HardCeilingRows 导出绝对安全上限（行），用户上限和管理员导出均不超过此值。<=0 时回退默认值。
	HardCeilingRows int `json:"hard_ceiling_rows"`
	// UserExportEnabled controls whether regular users can export billing CSVs.
	UserExportEnabled bool `json:"user_export_enabled"`
}

const (
	DefaultExportRateLimitCooldownSec = 600
	DefaultExportHardCeilingRows      = 1000000
)

var exportSetting = ExportSetting{
	RateLimitCooldownSec: DefaultExportRateLimitCooldownSec,
	HardCeilingRows:      DefaultExportHardCeilingRows,
	UserExportEnabled:    true,
}

func (s *ExportSetting) GetRateLimitCooldownSec() int {
	if s.RateLimitCooldownSec <= 0 {
		return DefaultExportRateLimitCooldownSec
	}
	return s.RateLimitCooldownSec
}

func (s *ExportSetting) GetHardCeilingRows() int {
	if s.HardCeilingRows <= 0 {
		return DefaultExportHardCeilingRows
	}
	return s.HardCeilingRows
}

func (s *ExportSetting) GetUserExportEnabled() bool {
	return s.UserExportEnabled
}

func init() {
	config.GlobalConfig.Register("export_setting", &exportSetting)
}

func GetExportSetting() *ExportSetting { return &exportSetting }
