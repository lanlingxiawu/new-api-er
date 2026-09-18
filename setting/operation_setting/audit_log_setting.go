package operation_setting

import (
	"github.com/QuantumNous/new-api/setting/config"
)

// AuditLogSetting 控制审计日志（audit_logs）的保留期。
//
// audit_logs 与消费日志（logs）是两张独立的表：logs 的清理由「清理历史日志」
// 系统任务按管理员选定的时间点执行，audit_logs 则按本配置的保留天数在同一个
// 任务里顺带清理。带鉴权的后台请求每次都会落一行审计（AccessTokenAudit），
// 量级远超登录日志，没有保留期这张表会无上限增长。
type AuditLogSetting struct {
	// RetentionDays 审计日志保留天数；0 表示永久保留、不清理。
	RetentionDays int `json:"retention_days"`
}

const (
	// DefaultAuditLogRetentionDays 默认保留半年：足够覆盖常规的安全回溯需求，
	// 又不至于让这张高频写入的表无限膨胀。
	DefaultAuditLogRetentionDays = 180
	// MaxAuditLogRetentionDays 上限 10 年。
	MaxAuditLogRetentionDays = 3650
	// MinAuditLogRetentionDays 下限 7 天：再短就会把仍在排障窗口内的记录删掉。
	MinAuditLogRetentionDays = 7
)

var auditLogSetting = AuditLogSetting{
	RetentionDays: DefaultAuditLogRetentionDays,
}

func init() {
	config.GlobalConfig.Register("audit_log_setting", &auditLogSetting)
}

// GetAuditLogSetting 返回全局配置。调用方不得跨请求缓存该指针。
func GetAuditLogSetting() *AuditLogSetting {
	return &auditLogSetting
}

// GetRetentionDays 返回生效的保留天数；0 表示不清理，其余值夹在合法区间内。
func (s *AuditLogSetting) GetRetentionDays() int {
	if s.RetentionDays <= 0 {
		return 0
	}
	return clampInt(s.RetentionDays, MinAuditLogRetentionDays, MaxAuditLogRetentionDays)
}
